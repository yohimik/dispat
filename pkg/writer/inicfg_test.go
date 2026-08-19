package writer

import (
	"strings"
	"testing"
)

func TestIniValueSpanMeasuresWhatItCanSafelySplice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		d      iniDialect
		line   string
		key    string
		want   string
		quoted bool
		ok     bool
	}{
		{"godot literal", godotDialect, `config/version="1.2.3"`, "config/version", "1.2.3", true, true},
		{"godot literal with spaces around it", godotDialect, `config/version = "1.2.3"`, "config/version", "1.2.3", true, true},
		{"godot integer", godotDialect, `version/code=37`, "version/code", "37", false, true},
		{"godot value before a comment", godotDialect, `config/version="1.2.3" ; shipped`, "config/version", "1.2.3", true, true},
		{"unreal bare value", unrealDialect, `ProjectVersion=1.0.0.0`, "ProjectVersion", "1.0.0.0", false, true},
		{"defold bare value", defoldDialect, `version = 1.2.3`, "version", "1.2.3", false, true},
		// The refusals. Godot writes a call for its engine features and spreads
		// array and dictionary literals over several lines; splicing inside one
		// leaves a file the editor cannot load.
		{"a call is refused", godotDialect, `config/features=PackedStringArray("4.3")`, "config/features", "", false, false},
		{"an array is refused", godotDialect, `_global_script_classes=[{`, "_global_script_classes", "", false, false},
		{"a dictionary is refused", godotDialect, `shader_globals={`, "shader_globals", "", false, false},
		{"an unterminated literal is refused", godotDialect, `config/version="1.2.3`, "config/version", "", false, false},
		{"an empty value is refused", godotDialect, `config/version=`, "config/version", "", false, false},
		{"another key on the line is not this one", godotDialect, `config/name="x"`, "config/version", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, quoted, ok := iniValueSpan(tc.line, tc.key, tc.d)
			if ok != tc.ok {
				t.Fatalf("iniValueSpan ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got := tc.line[start:end]; got != tc.want || quoted != tc.quoted {
				t.Errorf("iniValueSpan = %q,quoted=%v, want %q,quoted=%v", got, quoted, tc.want, tc.quoted)
			}
		})
	}
}

func TestIniSpliceWritesEveryOccurrenceOfARepeatedSection(t *testing.T) {
	// The whole reason iniSplice exists. An export_presets.cfg carries one
	// preset per store, and a counter written into the first alone ships a
	// stale one everywhere else.
	src := `[preset.0]

name="Android"

[preset.0.options]

version/code=37
version/name="1.2.3"

[preset.1]

name="iOS"

[preset.1.options]

version/code=37
application/short_version="1.2.3"
`
	lines := strings.Split(src, "\n")
	found, changed := iniSplice(lines, godotDialect,
		func(section string) bool { return strings.HasSuffix(section, ".options") },
		func(key, current string, _ bool) (string, bool) {
			if key != "version/code" {
				return "", false
			}
			return "42", true
		})
	if found != 2 || changed != 2 {
		t.Fatalf("iniSplice found %d changed %d, want 2 and 2", found, changed)
	}
	out := strings.Join(lines, "\n")
	if strings.Count(out, "version/code=42") != 2 {
		t.Errorf("both counters should read 42:\n%s", out)
	}
	// Everything else survives, including the versions in the same sections.
	if strings.Count(out, `version/name="1.2.3"`) != 1 || strings.Count(out, `application/short_version="1.2.3"`) != 1 {
		t.Errorf("a marketing version moved when only the counter was asked for:\n%s", out)
	}
}

func TestIniSpliceStaysOutOfSectionsItWasNotAsked(t *testing.T) {
	src := `[application]

config/version="1.0.0"

[rendering]

config/version="1.0.0"
`
	lines := strings.Split(src, "\n")
	found, changed := iniSplice(lines, godotDialect,
		func(section string) bool { return section == "application" },
		func(key, _ string, _ bool) (string, bool) {
			if key != "config/version" {
				return "", false
			}
			return "2.0.0", true
		})
	if found != 1 || changed != 1 {
		t.Fatalf("iniSplice found %d changed %d, want 1 and 1", found, changed)
	}
	out := strings.Join(lines, "\n")
	if !strings.Contains(out, "[application]\n\nconfig/version=\"2.0.0\"") {
		t.Errorf("the application version did not move:\n%s", out)
	}
	if !strings.Contains(out, "[rendering]\n\nconfig/version=\"1.0.0\"") {
		t.Errorf("a key of the same name in another section moved:\n%s", out)
	}
}

func TestIniSpliceLeavesAConvergedFileAlone(t *testing.T) {
	lines := strings.Split("[application]\nconfig/version=\"1.0.0\"\n", "\n")
	before := strings.Join(lines, "\n")
	found, changed := iniSplice(lines, godotDialect,
		func(string) bool { return true },
		func(key, _ string, _ bool) (string, bool) {
			if key != "config/version" {
				return "", false
			}
			return "1.0.0", true
		})
	if found != 1 || changed != 0 {
		t.Errorf("iniSplice found %d changed %d, want 1 and 0", found, changed)
	}
	if got := strings.Join(lines, "\n"); got != before {
		t.Errorf("a converged splice rewrote the file:\n%s", got)
	}
}

func TestIniRefuseRejectsWhatCannotSurviveAsOneValue(t *testing.T) {
	for _, bad := range []string{`1.0"`, `1.0\n`, "1.0\n2.0", "1.0\r", "[1.0]", "1.0;note"} {
		if err := iniRefuse("project.godot", bad, godotDialect); err == nil {
			t.Errorf("iniRefuse(%q) allowed it", bad)
		}
	}
	for _, good := range []string{"1.2.3", "1.2.3-rc.1", "1.2.3+build.4", "37"} {
		if err := iniRefuse("project.godot", good, godotDialect); err != nil {
			t.Errorf("iniRefuse(%q) refused it: %v", good, err)
		}
	}
	// Defold has no comment token, so a '#' is an ordinary byte there and a
	// semicolon is too.
	if err := iniRefuse("game.project", "1.2.3", defoldDialect); err != nil {
		t.Errorf("iniRefuse refused a plain version: %v", err)
	}
}

func TestIniVerifyCatchesAWriteThatDidNotLand(t *testing.T) {
	before := []byte("[application]\nconfig/version=\"1.0.0\"\n")
	want := func(s string) bool { return s == "application" }
	mine := func(k string) bool { return k == "config/version" }

	after := []byte("[application]\nconfig/version=\"2.0.0\"\n")
	if err := iniVerify(before, after, godotDialect, want, mine, "2.0.0", 1); err != nil {
		t.Errorf("a good rewrite was refused: %v", err)
	}
	// The value did not move.
	if err := iniVerify(before, before, godotDialect, want, mine, "2.0.0", 1); err == nil {
		t.Error("a rewrite that wrote nothing was accepted")
	}
	// A section was lost.
	lost := []byte("config/version=\"2.0.0\"\n")
	if err := iniVerify(before, lost, godotDialect, want, mine, "2.0.0", 1); err == nil {
		t.Error("a rewrite that dropped a section header was accepted")
	}
	// An entry was gained.
	gained := []byte("[application]\nconfig/version=\"2.0.0\"\nconfig/name=\"x\"\n")
	if err := iniVerify(before, gained, godotDialect, want, mine, "2.0.0", 1); err == nil {
		t.Error("a rewrite that added an entry was accepted")
	}
}
