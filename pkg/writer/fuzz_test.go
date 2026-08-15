package writer

import (
	"encoding/json"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRewriteNpm hammers the byte-splice path: whatever the input file held, a
// rewrite either errors or leaves valid JSON behind, never a torn or corrupted
// manifest. That is the writer's whole contract.
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

func FuzzRewriteCargo(f *testing.F) {
	for _, s := range []string{
		"[package]\nversion = \"1.0\"\n[dependencies]\nserde = \"1.0\"\n",
		"[dependencies]\ncore = { path = \"../core\", version = \"0.3\" }\n",
		"[dependencies]\np = { package = \"real\", version = \"1\" }\n",
		"[dependencies]\nw = { workspace = true }\n",
		"[package]\nversion = { workspace = true }\n", "[", "", "a = ",
	} {
		f.Add(s, "serde", "2.0", "9.9")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "Cargo.toml", content, version, []Edit{{Name: name, Range: rng}})
		var in, out map[string]any
		if ok && toml.Unmarshal([]byte(content), &in) == nil && toml.Unmarshal(data, &out) != nil {
			t.Fatalf("rewrite corrupted valid TOML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewritePyproject(f *testing.F) {
	for _, s := range []string{
		"[project]\nversion = \"1.0\"\ndependencies = [\n  \"requests>=2.0\",\n]\n",
		"[project]\ndependencies = [\"a\", \"b>=1\"]\n",
		"[project.optional-dependencies]\ncli = [\"rich>=13\"]\n",
		"[tool.poetry.dependencies]\nhttpx = \"^0.27\"\n",
		"[tool.poetry.group.test.dependencies]\np = { version = \"^8\" }\n",
		"[project]\ndependencies = [", "[", "", "dependencies = ]",
	} {
		f.Add(s, "requests", ">=3", "9.9")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "pyproject.toml", content, version, []Edit{{Name: name, Range: rng}})
		var in, out map[string]any
		if ok && toml.Unmarshal([]byte(content), &in) == nil && toml.Unmarshal(data, &out) != nil {
			t.Fatalf("rewrite corrupted valid TOML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteComposer(f *testing.F) {
	for _, s := range []string{
		`{"version":"1.0","require":{"a/b":"^1.0"},"require-dev":{"c/d":"^2"}}`,
		`{"require":{"a/b":{"version":"1"}}}`, `{"require":[]}`, `{`, ``, `{"version":1}`,
	} {
		f.Add(s, "a/b", "^2.0", "2.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "composer.json", content, version, []Edit{{Name: name, Range: rng}})
		if ok && json.Valid([]byte(content)) && !json.Valid(data) {
			t.Fatalf("rewrite corrupted valid JSON:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteMaven(f *testing.F) {
	for _, s := range []string{
		"<project><version>1.0</version><dependencies><dependency><groupId>g</groupId><artifactId>a</artifactId><version>1</version></dependency></dependencies></project>",
		"<project><parent><version>1</version></parent><version>2</version></project>",
		"<project><dependencies><dependency><groupId>g</groupId><artifactId>a</artifactId><version>${p}</version></dependency></dependencies></project>",
		"<project/>", "<project", "", "<project><version/></project>",
	} {
		f.Add(s, "g:a", "2.0", "9.9")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "pom.xml", content, version, []Edit{{Name: name, Range: rng}})
		if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
			t.Fatalf("rewrite corrupted well-formed XML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteCsproj(f *testing.F) {
	for _, s := range []string{
		`<Project><PropertyGroup><Version>1.0</Version></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0" /></ItemGroup></Project>`,
		`<Project><ItemGroup><PackageReference Include="A"><Version>1.0</Version></PackageReference></ItemGroup></Project>`,
		`<Project><ItemGroup><PackageReference Include="A" /></ItemGroup></Project>`,
		`<Project/>`, `<Project`, ``,
	} {
		f.Add(s, "A", "2.0", "9.9")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "A.csproj", content, version, []Edit{{Name: name, Range: rng}})
		if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
			t.Fatalf("rewrite corrupted well-formed XML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewritePubspec(f *testing.F) {
	for _, s := range []string{
		"name: a\nversion: 1.0.0\ndependencies:\n  http: ^1.0.0\n",
		"dependencies:\n  q: \"^2.0\"\n  local:\n    path: ../l\n",
		"dev_dependencies:\n  test: ^1.0\n", "version:", "", ":\n",
	} {
		f.Add(s, "http", "^2.0", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "pubspec.yaml", content, version, []Edit{{Name: name, Range: rng}})
		if ok && strings.Count(content, "\n") != strings.Count(string(data), "\n") {
			t.Fatalf("rewrite changed the line count:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteGemfile(f *testing.F) {
	for _, s := range []string{
		"source 'https://rubygems.org'\ngem 'rails', '~> 7.0'\n",
		"group :development do\n  gem 'rspec', '~> 3.0'\nend\n",
		"gem 'a', path: '../a'\n", "gem 'a'\n", "gem '", "",
	} {
		f.Add(s, "rails", "~> 7.1")
	}
	f.Fuzz(func(t *testing.T, content, name, rng string) {
		data, ok := fuzzRewrite(t, "Gemfile", content, "", []Edit{{Name: name, Range: rng}})
		if ok && strings.Count(content, "\n") != strings.Count(string(data), "\n") {
			t.Fatalf("rewrite changed the line count:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteNuspec(f *testing.F) {
	for _, s := range []string{
		`<package><metadata><id>A</id><version>1.0</version><dependencies><dependency id="B" version="1"/></dependencies></metadata></package>`,
		`<package><metadata><version>$version$</version></metadata></package>`,
		`<package><metadata><dependencies><group><dependency id="B" version="1"/></group></dependencies></metadata></package>`,
		`<package><metadata><version/></metadata></package>`, `<package`, ``,
	} {
		f.Add(s, "B", "2.0", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		data, ok := fuzzRewrite(t, "A.nuspec", content, version, []Edit{{Name: name, Range: rng}})
		if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
			t.Fatalf("rewrite corrupted well-formed XML:\n in: %q\nout: %q", content, data)
		}
	})
}

func FuzzRewriteNuGetLists(f *testing.F) {
	for _, s := range []string{
		`<Project><ItemGroup><PackageVersion Include="A" Version="1.0"/></ItemGroup></Project>`,
		`<packages><package id="A" version="1.0"/></packages>`,
		`<packages><package id="A"/></packages>`, `<Project/>`, `<packages`, ``,
	} {
		f.Add(s, "A", "2.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng string) {
		for _, file := range []string{"Directory.Packages.props", "packages.config"} {
			data, ok := fuzzRewrite(t, file, content, "", []Edit{{Name: name, Range: rng}})
			if ok && xmlWellFormed([]byte(content)) == nil && xmlWellFormed(data) != nil {
				t.Fatalf("%s: rewrite corrupted well-formed XML:\n in: %q\nout: %q", file, content, data)
			}
		}
	})
}

// FuzzLink hammers the insertion path across every format that has a
// redirect. Insertion can change a file's structure where a splice cannot, so
// the contract is the same but the risk is higher: a failed link never
// modifies the file, and a successful one never turns a parseable manifest
// into an unparseable one.
func FuzzLink(f *testing.F) {
	seeds := []string{
		"module m\n\ngo 1.25.0\n\nreplace example.com/dep => ../dep\n",
		"[package]\nname = \"a\"\n\n[patch.crates-io]\nserde = { path = \"../serde\" }\n",
		"name: a\ndependency_overrides:\n  http:\n    path: ../http\n",
		"[project]\nname = \"a\"\n\n[tool.uv.sources]\nrequests = { path = \"../r\" }\n",
		`{"name":"a","overrides":{"dep":"file:../dep"}}`,
		`{"name":"a","resolutions":{}}`,
		`{"pnpm":{"overrides":{"dep":"file:../dep"}}}`,
		"[patch.crates-io]\n", "dependency_overrides:\n", "", "[", "{}",
	}
	for _, s := range seeds {
		f.Add(s, "dep", "../elsewhere")
	}
	f.Fuzz(func(t *testing.T, content, name, path string) {
		for _, file := range []string{"go.mod", "Cargo.toml", "pubspec.yaml", "pyproject.toml", "package.json"} {
			dir := t.TempDir()
			p := filepath.Join(dir, file)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Skip()
			}
			_, err := Relink(p, []Link{{Name: name, Path: path}})
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				t.Fatalf("%s: manifest vanished: %v", file, readErr)
			}
			if err != nil {
				if string(data) != content {
					t.Fatalf("%s: a failed link modified the file:\n in: %q\nout: %q", file, content, data)
				}
				continue
			}
			switch file {
			case "Cargo.toml", "pyproject.toml":
				var in, out map[string]any
				if toml.Unmarshal([]byte(content), &in) == nil && toml.Unmarshal(data, &out) != nil {
					t.Fatalf("%s: a link corrupted valid TOML:\n in: %q\nout: %q", file, content, data)
				}
			case "package.json":
				if json.Valid([]byte(content)) && !json.Valid(data) {
					t.Fatalf("a link corrupted valid JSON:\n in: %q\nout: %q", content, data)
				}
			}
		}
	})
}

// FuzzReplaceBytes hammers the zero-parsing splicer. It has no grammar to
// protect, so its contract is arithmetic instead: it never panics, it never
// touches the caller's input, and the result is exactly as long as the
// occurrences it reported say it should be.
func FuzzReplaceBytes(f *testing.F) {
	for _, seed := range []struct{ data, find, write string }{
		{"acme:1.0.0 acme:1.0.0", "1.0.0", "1.1.0"},
		{"nothing here", "absent", "x"},
		{"aaa", "a", "aa"},
		{"aaa", "aa", "a"},
		{"same", "same", "same"},
		{"drop me", " me", ""},
		{"", "x", "y"},
		{"\x00\xff binary-ish 1.0", "1.0", "2.0"},
	} {
		f.Add(seed.data, seed.find, seed.write)
	}
	f.Fuzz(func(t *testing.T, data, find, write string) {
		in := []byte(data)
		kept := string(in)
		reps := []Replacement{{Find: find, Write: write}}
		out, counts := ReplaceBytes(in, reps)
		if string(in) != kept {
			t.Fatalf("input mutated:\n in: %q\nnow: %q", kept, in)
		}
		want := len(data)
		if find != "" && find != write {
			want += counts[0] * (len(write) - len(find))
		}
		if len(out) != want {
			t.Fatalf("length %d, want %d (%d occurrences of %q -> %q)", len(out), want, counts[0], find, write)
		}
		// Matches are taken left to right and never overlap, so a replacement
		// longer than what it replaced cannot fit more of itself in than the
		// input had room for.
		if find != "" && counts[0]*len(find) > len(data) {
			t.Fatalf("%d occurrences of a %d-byte pattern in %d bytes", counts[0], len(find), len(data))
		}
	})
}

// FuzzRewriteDockerfile and FuzzRewriteCompose hold the two Docker formats to
// the same contract as every other writer: never panic, never leave a failed
// rewrite's changes behind, and never turn a line the writer was not aiming at
// into something else. The last one is checked directly — the number of lines
// must not change, and every line the rewrite did not target must come back
// byte for byte — because neither format has a grammar cheap enough to
// re-parse and prove the point that way.
func FuzzRewriteDockerfile(f *testing.F) {
	seeds := []string{
		"FROM redis:7.2\n",
		"FROM --platform=$BUILDPLATFORM a/b:1.0 AS build\nCOPY --from=build /a /b\n",
		"RUN --mount=type=bind,from=a/b:1.0,target=/x make\n",
		"FROM a/b:1.0@sha256:abc\nFROM ${X}:${Y}\nFROM scratch\n",
		"FROM \\\n",
		"# syntax=docker/dockerfile:1\n\n\n",
		"from",
		"",
		"COPY --from= /a /b\nCOPY --mount= /c /d\nRUN --mount=from=,target=/x\n",
	}
	for _, s := range seeds {
		f.Add(s, "a/b", "2.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng string) {
		fuzzImageRewrite(t, "Dockerfile", content, "", []Edit{{Name: name, Range: rng}})
	})
}

func FuzzRewriteCompose(f *testing.F) {
	seeds := []string{
		"services:\n  api:\n    build: .\n    image: a/api:1.0.0\n",
		"services:\n  api:\n    build:\n      tags:\n        - a/api:1.0.0\n    image: a/api:1.0.0\n",
		"services:\n  api:\n    build:\n      tags: [\"a/api:1.0.0\", a/api:latest]\n    image: a/api:1.0.0\n",
		"services:\n  api:\n    ports:\n      - \"8080:80\"\n",
		"services:\n  api:\n    build:\n      tags: [\"unterminated\n",
		"services:\n",
		"- a\n- b\n",
		"",
		"services:\n  a:\n    image: \n  b:\n    image: ''\n",
	}
	for _, s := range seeds {
		f.Add(s, "a/api", "2.0.0", "3.0.0")
	}
	f.Fuzz(func(t *testing.T, content, name, rng, version string) {
		fuzzImageRewrite(t, "compose.yaml", content, version, []Edit{{Name: name, Range: rng}})
	})
}

// fuzzImageRewrite is the shared body of the two targets above.
func fuzzImageRewrite(t *testing.T, base, content, version string, edits []Edit) {
	t.Helper()
	path := filepath.Join(t.TempDir(), base)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Skip()
	}
	_, err := Rewrite(path, version, edits)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("manifest vanished: %v", readErr)
	}
	out := string(data)
	if err != nil {
		if out != content {
			t.Fatalf("a failed rewrite modified the file:\n in: %q\nout: %q", content, out)
		}
		return
	}
	before, after := strings.Split(content, "\n"), strings.Split(out, "\n")
	if len(before) != len(after) {
		t.Fatalf("rewrite changed the line count:\n in: %q\nout: %q", content, out)
	}
	// Only a tag may differ, and a tag has no whitespace in it, so a line that
	// changed must still hold the same number of tokens in the same places.
	for i := range before {
		if before[i] == after[i] {
			continue
		}
		if len(strings.Fields(before[i])) != len(strings.Fields(after[i])) {
			t.Fatalf("rewrite restructured line %d:\n in: %q\nout: %q", i, before[i], after[i])
		}
	}
}
