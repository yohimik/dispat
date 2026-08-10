package writer

import (
	"encoding/json"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRewriteNpm hammers the byte-splice path: whatever the input file held,
// a rewrite either errors or leaves valid JSON behind — never a torn or
// corrupted manifest. That is the writer's whole contract.
func FuzzRewriteNpm(f *testing.F) {
	seeds := []string{
		`{"name":"a","version":"1.0.0","dependencies":{"b":"^1.0.0"}}`,
		"{\n\t\"dependencies\": { \"b\": \"workspace:*\" }\n}",
		`{"dependencies":{"b":{"version":"1.0.0"}}}`,
		`{"version":"1.0.0"}`,
		`[]`,
		`{`,
		``,
		`{"dependencies":{"we\"ird":"1"}}`,
	}
	for _, s := range seeds {
		f.Add(s, "b", "^2.0.0", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "package.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Skip()
		}
		_, err := Rewrite(path, version, []Edit{{Name: name, Range: rng}})
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("manifest vanished: %v", readErr)
		}
		if err == nil && json.Valid([]byte(content)) && !json.Valid(data) {
			t.Fatalf("rewrite corrupted valid JSON:\n in: %q\nout: %q", content, data)
		}
		if err != nil && string(data) != content {
			t.Fatalf("a failed rewrite modified the file:\n in: %q\nout: %q", content, data)
		}
	})
}

// The mobile formats' fuzz targets all assert the same two-part contract as
// FuzzRewriteNpm, because it is the writer's whole promise: a rewrite that
// fails leaves the file exactly as it found it, and a rewrite that succeeds
// never turns a document that parsed into one that does not.

// fuzzRewrite runs one rewrite and checks the invariant both halves share.
func fuzzRewrite(t *testing.T, name, content, version string, edits []Edit) ([]byte, bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Skip()
	}
	_, err := Rewrite(path, version, edits)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("manifest vanished: %v", readErr)
	}
	if err != nil {
		if string(data) != content {
			t.Fatalf("a failed rewrite modified the file:\n in: %q\nout: %q", content, data)
		}
		return data, false
	}
	return data, true
}

func FuzzRewritePlist(f *testing.F) {
	for _, s := range []string{
		"<plist version=\"1.0\"><dict><key>CFBundleShortVersionString</key><string>1.0</string></dict></plist>",
		"<plist><dict><key>CFBundleShortVersionString</key><string/></dict></plist>",
		"<plist><dict><key>CFBundleShortVersionString</key><string>$(MARKETING_VERSION)</string></dict></plist>",
		"<plist><dict><key>a</key><array><dict><key>CFBundleShortVersionString</key><string>9</string></dict></array></dict></plist>",
		"<plist><dict>", "", "<", "<plist><array/></plist>",
	} {
		f.Add(s, "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, version string) {
		data, ok := fuzzRewrite(t, "Info.plist", content, version, nil)
		if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
			t.Fatalf("rewrite corrupted well-formed XML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteAndroidManifest(f *testing.F) {
	for _, s := range []string{
		`<manifest package="com.a" android:versionName="1.0"/>`,
		`<manifest android:versionName='1.0'><application/></manifest>`,
		`<manifest/>`, `<resources/>`, "<manifest", "", `<manifest android:versionName="&amp;"/>`,
	} {
		f.Add(s, "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, version string) {
		data, ok := fuzzRewrite(t, "AndroidManifest.xml", content, version, nil)
		if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
			t.Fatalf("rewrite corrupted well-formed XML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteGradleCatalog(f *testing.F) {
	for _, s := range []string{
		"[versions]\na = \"1\"\n[libraries]\nb = { module = \"g:a\", version.ref = \"a\" }\n",
		"[libraries]\nb = { module = \"g:a\", version = \"1\" }\n",
		"[libraries]\nb = \"g:a:1\"\n",
		"[libraries]\nb = { group = \"g\", name = \"a\", version = { require = \"1\" } }\n",
		"[versions]\n", "[", "", "b = ",
	} {
		f.Add(s, "g:a", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng string) {
		data, ok := fuzzRewrite(t, "libs.versions.toml", content, "", []Edit{{Name: name, Range: rng}})
		var in, out map[string]any
		if ok && toml.Unmarshal([]byte(content), &in) == nil && toml.Unmarshal(data, &out) != nil {
			t.Fatalf("rewrite corrupted valid TOML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteXcodeProj(f *testing.F) {
	for _, s := range []string{
		"{\n\tbuildSettings = {\n\t\tMARKETING_VERSION = 1.0;\n\t};\n}\n",
		"{ MARKETING_VERSION = \"1.0\"; }\n",
		"{ MARKETING_VERSION = 1.0; MARKETING_VERSION = 2.0; }\n",
		"{ MARKETING_VERSION[sdk=*] = 1.0; }\n", "{", "", "MARKETING_VERSION =",
	} {
		f.Add(s, "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, version string) {
		data, ok := fuzzRewrite(t, "project.pbxproj", content, version, nil)
		if !ok {
			return
		}
		// There is no grammar to check against, so the structural invariant the
		// writer guards on must hold when checked from the outside too.
		for _, token := range []string{"{", "}", "(", ")", ";"} {
			if a, b := strings.Count(content, token), strings.Count(string(data), token); a != b {
				t.Fatalf("rewrite changed the %q count (%d -> %d):\n in: %q\nout: %q", token, a, b, content, data)
			}
		}
	})
}

func FuzzRewritePodfile(f *testing.F) {
	for _, s := range []string{
		"target 'A' do\n  pod 'B', '~> 1.0'\nend\n",
		"pod \"B\",  \"1.0\"\n", "pod 'B', '>= 1', '< 2'\n", "pod 'B', :path => '../b'\n",
		"pod 'B'\n", "pod '", "", "# pod 'B', '1.0'\n",
	} {
		f.Add(s, "B", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng string) {
		data, ok := fuzzRewrite(t, "Podfile", content, "", []Edit{{Name: name, Range: rng}})
		if ok && strings.Count(content, "\n") != strings.Count(string(data), "\n") {
			t.Fatalf("rewrite changed the line count:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteGradleBuild(f *testing.F) {
	for _, s := range []string{
		"android {\n defaultConfig {\n versionName \"1.0\"\n }\n}\ndependencies {\n implementation 'g:a:1'\n}\n",
		"android {\n defaultConfig {\n versionName = \"1.0\"\n }\n}\n",
		"dependencies {\n implementation(\"g:a:1\")\n implementation(libs.x)\n}\n",
		"dependencies {\n implementation \"g:a:$v\"\n}\n",
		"dependencies {\n /* open\n}\n", "{", "", "}",
	} {
		f.Add(s, "2.0.0", "g:a", "3.0.0")
	}
	f.Fuzz(func(t *testing.T, content, version, name, rng string) {
		data, ok := fuzzRewrite(t, "build.gradle", content, version, []Edit{{Name: name, Range: rng}})
		if !ok {
			return
		}
		for _, brace := range []string{"{", "}", "(", ")"} {
			if a, b := strings.Count(content, brace), strings.Count(string(data), brace); a != b {
				t.Fatalf("rewrite changed the %q count (%d -> %d):\n in: %q\nout: %q", brace, a, b, content, data)
			}
		}
	})
}
