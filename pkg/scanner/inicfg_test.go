package scanner

import (
	"reflect"
	"testing"
)

func TestIniStripCommentKeepsWhatIsInsideALiteral(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    iniDialect
		line string
		want string
	}{
		{"godot cuts a real comment", godotDialect, `config/version="1.0" ; the shipped one`, `config/version="1.0" `},
		{"godot keeps a semicolon inside the value", godotDialect, `config/name="a;b"`, `config/name="a;b"`},
		{"godot keeps an escaped quote", godotDialect, `config/name="say \"hi\"; now"`, `config/name="say \"hi\"; now"`},
		{"godot cuts a whole-line comment", godotDialect, `; a note`, ``},
		{"unreal cuts a real comment", unrealDialect, `ProjectVersion=1.0.0.0 ; stamped`, `ProjectVersion=1.0.0.0 `},
		// Defold has no comment token on purpose: its dependency keys are
		// spelled dependencies#0, and a '#' rule would eat the key.
		{"defold keeps its hashes", defoldDialect, `dependencies#0 = https://x/a.zip`, `dependencies#0 = https://x/a.zip`},
		{"defold keeps a hash in a value", defoldDialect, `title = Game #1`, `title = Game #1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := iniStripComment(tc.line, tc.d); got != tc.want {
				t.Errorf("iniStripComment(%q) = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestIniSectionReadsHeadersAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
		ok   bool
	}{
		{"[application]", "application", true},
		{"[preset.0.options]", "preset.0.options", true},
		{"  [project]  ", "project", true},
		{"[/Script/EngineSettings.GeneralProjectSettings]", "/Script/EngineSettings.GeneralProjectSettings", true},
		{"[]", "", false},
		{"config/version=\"1.0\"", "", false},
		// Godot 3 writes multi-line array literals; their opening line is not
		// a header and must not be read as one.
		{"_global_script_classes=[{", "", false},
		{"}]", "", false},
	} {
		got, ok := iniSection(tc.line)
		if ok != tc.ok || got != tc.want {
			t.Errorf("iniSection(%q) = %q,%v, want %q,%v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIniEntrySplitsKeysThatCarryPunctuation(t *testing.T) {
	for _, tc := range []struct {
		line       string
		key, value string
		ok         bool
	}{
		{`config/version="1.2.3"`, "config/version", `"1.2.3"`, true},
		{`version/code=37`, "version/code", "37", true},
		{`dependencies#0 = https://x/a.zip`, "dependencies#0", "https://x/a.zip", true},
		{`ProjectVersion=1.0.0.0`, "ProjectVersion", "1.0.0.0", true},
		{`  title = My Game  `, "title", "My Game", true},
		// An Unreal array operation keeps its prefix, so it reads as the
		// distinct key it is and never stands in for a plain assignment.
		{`+ProjectVersion=9.9.9`, "+ProjectVersion", "9.9.9", true},
		{`no equals here`, "", "", false},
		{`=orphan`, "", "", false},
	} {
		key, value, ok := iniEntry(tc.line)
		if ok != tc.ok || key != tc.key || value != tc.value {
			t.Errorf("iniEntry(%q) = %q,%q,%v, want %q,%q,%v", tc.line, key, value, ok, tc.key, tc.value, tc.ok)
		}
	}
}

func TestIniUnquoteAcceptsOnlyWhatTheDialectCallsAString(t *testing.T) {
	for _, tc := range []struct {
		name  string
		d     iniDialect
		value string
		want  string
		ok    bool
	}{
		{"godot literal", godotDialect, `"1.2.3"`, "1.2.3", true},
		{"godot empty literal", godotDialect, `""`, "", true},
		{"godot escaped quote", godotDialect, `"say \"hi\""`, `say "hi"`, true},
		{"godot integer is not a string", godotDialect, `37`, "", false},
		{"godot boolean is not a string", godotDialect, `true`, "", false},
		{"godot call is not a string", godotDialect, `PackedStringArray("4.3")`, "", false},
		{"unreal bare value", unrealDialect, `1.0.0.0`, "1.0.0.0", true},
		{"unreal empty value", unrealDialect, ``, "", false},
		{"defold bare value", defoldDialect, `My Game`, "My Game", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := iniUnquote(tc.value, tc.d)
			if ok != tc.ok || got != tc.want {
				t.Errorf("iniUnquote(%q) = %q,%v, want %q,%v", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestIniScanVisitsEveryOccurrenceOfARepeatedSection(t *testing.T) {
	// An export_presets.cfg carries one preset per platform, and the sections
	// repeat rather than merge. A reader that merged them would report one
	// build counter where there are three.
	data := []byte(`config_version=5

[preset.0]

name="Android"

[preset.0.options]

version/code=37
version/name="1.2.3"

[preset.1]

name="iOS"

[preset.1.options]

version/code=38
application/short_version="1.2.3"
`)
	var got []string
	iniScan(data, godotDialect, func(section, key, value string) {
		got = append(got, section+"|"+key+"="+value)
	})
	want := []string{
		"|config_version=5",
		"preset.0|name=\"Android\"",
		"preset.0.options|version/code=37",
		"preset.0.options|version/name=\"1.2.3\"",
		"preset.1|name=\"iOS\"",
		"preset.1.options|version/code=38",
		"preset.1.options|application/short_version=\"1.2.3\"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("iniScan visited\n%q\nwant\n%q", got, want)
	}
}

func TestIniScanHandlesCarriageReturnsAndAMissingFinalNewline(t *testing.T) {
	data := []byte("[application]\r\nconfig/version=\"1.0\"\r\nconfig/name=\"Last\"")
	got := map[string]string{}
	iniScan(data, godotDialect, func(section, key, value string) {
		text, _ := iniUnquote(value, godotDialect)
		got[section+"/"+key] = text
	})
	want := map[string]string{
		"application/config/version": "1.0",
		"application/config/name":    "Last",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("iniScan read %q, want %q", got, want)
	}
}

func TestIniStringTakesTheLastOccurrenceAndClonesIt(t *testing.T) {
	data := []byte("[application]\nconfig/version=\"1.0.0\"\nconfig/version=\"2.0.0\"\n")
	if got := iniString(data, godotDialect, "application", "config/version"); got != "2.0.0" {
		t.Errorf("iniString = %q, want 2.0.0", got)
	}
	// A key that is not there, and a section that is not there, both read
	// empty rather than reaching for the same key somewhere else.
	if got := iniString(data, godotDialect, "application", "config/name"); got != "" {
		t.Errorf("iniString for an absent key = %q, want empty", got)
	}
	if got := iniString(data, godotDialect, "rendering", "config/version"); got != "" {
		t.Errorf("iniString for an absent section = %q, want empty", got)
	}
}
