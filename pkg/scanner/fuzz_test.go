package scanner

import (
	"testing"
)

// Fuzzing the hand-rolled parsers: none of them may panic on any input, and
// the library-backed ones ride along for free through parserFor. Malformed
// input must come back as an error (or a well-formed partial Manifest), never
// as a crash — a scanner walks whatever a checkout contains.

// fuzzManifestNames drives every registered parser: exact names, a suffix
// name and a pattern name.
var fuzzManifestNames = []string{
	"package.json", "go.mod", "Cargo.toml", "pyproject.toml",
	"composer.json", "pom.xml", "pubspec.yaml", "App.csproj",
	"requirements.txt", "requirements-dev.txt",
}

func FuzzParsers(f *testing.F) {
	seeds := []string{
		"",
		"{",
		`{"name": "a", "dependencies": {"b": "^1.0.0"}}`,
		"module example.com/m\n\ngo 1.25.0\nrequire example.com/dep v1.0.0\n",
		"[package]\nname = \"a\"\nversion = \"1.0.0\"\n[dependencies]\nserde = { version = \"1\", package = 42 }\n",
		"[project]\nname = \"a\"\ndependencies = [\"b>=1\"]\n",
		"<Project><PropertyGroup><PackageId>A</PackageId></PropertyGroup></Project>",
		"<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><project><artifactId>a</artifactId></project>",
		"name: app\nversion: 1.0\ndependencies:\n  http: ^1.0.0\n",
		"pkg==1.0 \\\n  --hash=sha256:abc\n-e ./local\n",
		"\xff\xfe\x00broken",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data string) {
		for _, name := range fuzzManifestNames {
			parse, ok := parserFor(name)
			if !ok {
				t.Fatalf("no parser for %q", name)
			}
			m, err := parse(name, []byte(data))
			if err != nil {
				continue
			}
			for _, d := range m.Deps {
				_ = d.Kind.String()
			}
		}
	})
}

func FuzzPep508Dep(f *testing.F) {
	for _, s := range []string{
		"requests>=2.0,<3", "Acme_Core==1.0.0", "uvicorn[standard]>=0.30",
		"bare-name", "a;python_version<'3'", "x @ https://example.com/x.whl", "==", "[",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		d := pep508Dep(line, KindDependencies)
		_ = d.Name
	})
}
