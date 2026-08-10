package writer

import (
	"strings"
	"testing"
)

func TestComposerRewriteRequireFields(t *testing.T) {
	src := `{
	"name": "acme/core",
	"version": "1.2.3",
	"require": { "php": ">=8.1", "monolog/monolog": "^2.0" },
	"require-dev": {
		"phpunit/phpunit": "^9.5"
	}
}`
	path := seed(t, "composer.json", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "monolog/monolog", Range: "^3.0"},
		{Name: "phpunit/phpunit", Kind: "devDependencies", Range: "^10.0"},
		// Composer has no peer field, so this names nothing it can express.
		{Name: "monolog/monolog", Kind: "peerDependencies", Range: "^9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`"version": "1.2.3"`, `"version": "2.0.0"`,
		`"monolog/monolog": "^2.0"`, `"monolog/monolog": "^3.0"`,
		`"phpunit/phpunit": "^9.5"`, `"phpunit/phpunit": "^10.0"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 2 || len(res.Missing) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestMavenRewriteDependencyAndProjectVersion(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <parent>
    <groupId>com.acme</groupId>
    <artifactId>parent</artifactId>
    <version>5.0.0</version>
  </parent>

  <artifactId>core</artifactId>
  <version>1.2.3</version>

  <dependencies>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-api</artifactId>
      <version>2.0.9</version>
    </dependency>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>util</artifactId>
      <version>${acme.version}</version>
    </dependency>
    <dependency>
      <groupId>org.managed</groupId>
      <artifactId>managed</artifactId>
    </dependency>
  </dependencies>
</project>
`
	path := seed(t, "pom.xml", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "org.slf4j:slf4j-api", Range: "2.0.13"},
		{Name: "com.acme:util", Range: "9.9.9"},       // ${property}: left alone
		{Name: "org.managed:managed", Range: "9.9.9"}, // no version element
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"<artifactId>core</artifactId>\n  <version>1.2.3</version>",
		"<artifactId>core</artifactId>\n  <version>2.0.0</version>",
		"<version>2.0.9</version>", "<version>2.0.13</version>",
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The parent's version selects which POM this inherits from; rewriting it
	// would repoint the build rather than release this module.
	if !strings.Contains(read(t, path), "<version>5.0.0</version>") {
		t.Error("the parent version was rewritten")
	}
	if !strings.Contains(read(t, path), "${acme.version}") {
		t.Error("a property reference was overwritten with a literal")
	}
	if !res.VersionWritten || len(res.Applied) != 1 || len(res.Missing) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestPubspecRewriteVersionAndConstraints(t *testing.T) {
	src := `name: acme
version: 1.2.3    # shipped

environment:
  sdk: '>=3.0.0 <4.0.0'

dependencies:
  http: ^1.0.0
  quoted: "^2.0.0"
  local:
    path: ../local
  flutter:
    sdk: flutter

dev_dependencies:
  test: ^1.24.0
`
	path := seed(t, "pubspec.yaml", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "http", Range: "^1.2.0"},
		{Name: "quoted", Range: "^2.1.0"},
		{Name: "test", Kind: "devDependencies", Range: "^1.25.0"},
		{Name: "local", Range: "^1.0.0"}, // a block, not a constraint
		{Name: "absent", Range: "^1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"version: 1.2.3    # shipped", "version: 2.0.0    # shipped",
		"http: ^1.0.0", "http: ^1.2.0",
		`quoted: "^2.0.0"`, `quoted: "^2.1.0"`,
		"test: ^1.24.0", "test: ^1.25.0",
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The environment block is not a dependency block and must be untouched.
	if !strings.Contains(read(t, path), "sdk: '>=3.0.0 <4.0.0'") {
		t.Error("the environment constraint was rewritten")
	}
	if !res.VersionWritten || len(res.Applied) != 3 || len(res.Missing) != 2 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestPyprojectRewritePep621AndPoetry(t *testing.T) {
	src := `[project]
name = "acme"
version = "1.2.3"
dependencies = [
    "requests>=2.0,<3",   # http
    "Acme_Core==1.0.0",
    "click",
]

[project.optional-dependencies]
cli = ["rich>=13.0", "typer"]

[dependency-groups]
dev = ["ruff>=0.5"]

[tool.poetry.dependencies]
python = "^3.11"
httpx = "^0.27"

[tool.poetry.group.test.dependencies]
pytest = { version = "^8.0", python = ">=3.11" }
`
	path := seed(t, "pyproject.toml", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "requests", Range: ">=2.5,<3"},
		{Name: "acme-core", Range: "==1.1.0"}, // matched PEP 503-normalised
		{Name: "rich", Kind: "optionalDependencies", Range: ">=13.7"},
		{Name: "ruff", Kind: "devDependencies", Range: ">=0.6"},
		{Name: "httpx", Range: "^0.28"},
		{Name: "pytest", Kind: "devDependencies", Range: "^8.2"},
		// A bare entry gains its first specifier, the same way a bare line in a
		// requirements file does.
		{Name: "click", Range: ">=8.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	for _, want := range []string{
		`version = "2.0.0"`,
		`"requests>=2.5,<3",   # http`,
		`"Acme_Core==1.1.0"`,
		`"rich>=13.7"`,
		`"ruff>=0.6"`,
		`httpx = "^0.28"`,
		`{ version = "^8.2", python = ">=3.11" }`,
		`"click>=8.0"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// python is a platform constraint the scanner skips, so it stays put.
	if !strings.Contains(got, `python = "^3.11"`) {
		t.Error("the python platform constraint was rewritten")
	}
	if !res.VersionWritten || len(res.Applied) != 7 || len(res.Missing) != 0 {
		t.Errorf("result mismatch: applied=%d %+v", len(res.Applied), res)
	}
}

func TestPubspecRewriteIgnoresKeysNestedInsideEntries(t *testing.T) {
	// "path" is both an ordinary pub package and the key a block dependency
	// spells its folder with. Matching by name alone rewrote the folder into a
	// version and broke the local dependency.
	src := `name: acme
version: 1.0.0

dependencies:
  path: ^1.8.0
  local:
    path: ../local
  from_git:
    git:
      url: https://example.com/g.git
      path: packages/g

dev_dependencies:
  test: ^1.24.0
`
	path := seed(t, "pubspec.yaml", src)
	res, err := Rewrite(path, "", []Edit{{Name: "path", Range: "^1.9.0"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src, "  path: ^1.8.0", "  path: ^1.9.0", 1)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(read(t, path), "    path: ../local") {
		t.Error("a block dependency's folder was rewritten into a version")
	}
	if !strings.Contains(read(t, path), "      path: packages/g") {
		t.Error("a key two levels down was rewritten")
	}
	if len(res.Applied) != 1 {
		t.Errorf("result mismatch: %+v", res)
	}
}
