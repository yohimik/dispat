package scanner

import (
	"testing"
)

// The hand-rolled readers get tested directly as well as through Scan. A whole
// manifest proves the common path; these prove the edges a healthy file never
// reaches.

func TestPlistTopLevelStringsEdges(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      map[string]string
		wantErr   bool
	}{
		{"no dictionary", `<plist version="1.0"><array/></plist>`, map[string]string{}, false},
		{"bare dict document", `<dict><key>a</key><string>1</string></dict>`, map[string]string{"a": "1"}, false},
		{"unrelated root", `<resources><string>x</string></resources>`, map[string]string{}, false},
		{"dangling key", `<plist><dict><key>a</key></dict></plist>`, map[string]string{}, false},
		{"value with no key", `<plist><dict><string>orphan</string><key>a</key><string>1</string></dict></plist>`,
			map[string]string{"a": "1"}, false},
		{"key whitespace trimmed", "<plist><dict><key>  a  </key><string>1</string></dict></plist>",
			map[string]string{"a": "1"}, false},
		{"malformed", `<plist><dict><key>a</key><string>1</dict>`, nil, true},
		{"empty", ``, map[string]string{}, false},
	} {
		got, err := plistTopLevelStrings([]byte(tc.src))
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: want an error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", tc.name, k, got[k], v)
			}
		}
	}
}

func TestIsBuildSettingRef(t *testing.T) {
	for _, v := range []string{"$(MARKETING_VERSION)", "${VERSION}", "  $(X)  "} {
		if !isBuildSettingRef(v) {
			t.Errorf("%q should read as a reference", v)
		}
	}
	for _, v := range []string{"1.0.0", "", "$VERSION", "$(unterminated", "a$(X)b"} {
		if isBuildSettingRef(v) {
			t.Errorf("%q should read as a literal", v)
		}
	}
}

func TestGradleProjectPathForms(t *testing.T) {
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{`implementation project(':core')`, ":core", true},
		{`implementation project(path: ':a:b')`, ":a:b", true},
		{`implementation project(path:':a')`, ":a", true},
		{`implementation project("::")`, "::", true},
		{`implementation project()`, "", false},
		{`implementation project`, "", false},
		{`implementation project(name: ':a')`, "", false},
		{`implementation project(":$x")`, "", false},
	} {
		masked, _ := gradleMask(tc.line, false)
		i := len("implementation ")
		got, ok := gradleProjectPath(tc.line, masked, i)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("gradleProjectPath(%q) = %q,%v, want %q,%v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPoetryDepsValueShapes(t *testing.T) {
	var m Manifest
	poetryDeps(&m, map[string]any{
		"plain":    "^1.0",
		"table":    map[string]any{"version": "^2.0"},
		"local":    map[string]any{"path": "../local"},
		"python":   "^3.11", // a platform constraint, not a dependency
		"multiple": []any{map[string]any{"version": "^1"}},
		"odd":      42,
	}, KindDependencies)
	got := map[string]DeclaredDep{}
	for _, d := range m.Deps {
		got[d.Name] = d
	}
	if _, ok := got["python"]; ok {
		t.Error("python is a platform constraint, not a dependency")
	}
	for _, name := range []string{"multiple", "odd"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s has no single constraint and should be skipped", name)
		}
	}
	if got["plain"].Range != "^1.0" || got["table"].Range != "^2.0" {
		t.Errorf("version shapes misread: %+v", m.Deps)
	}
	if got["local"].LocalPath != "../local" || got["local"].Range != "" {
		t.Errorf("path-only dependency misread: %+v", got["local"])
	}
}

func TestDecodeXMLCharsets(t *testing.T) {
	type doc struct {
		V string `xml:"v"`
	}
	for _, tc := range []struct {
		name, src string
		wantErr   bool
	}{
		{"utf-8", `<?xml version="1.0" encoding="UTF-8"?><d><v>x</v></d>`, false},
		{"latin-1", `<?xml version="1.0" encoding="ISO-8859-1"?><d><v>x</v></d>`, false},
		{"windows-1252", `<?xml version="1.0" encoding="windows-1252"?><d><v>x</v></d>`, false},
		{"us-ascii", `<?xml version="1.0" encoding="us-ascii"?><d><v>x</v></d>`, false},
		{"unsupported", `<?xml version="1.0" encoding="EBCDIC"?><d><v>x</v></d>`, true},
	} {
		var out doc
		err := decodeXML([]byte(tc.src), &out)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
