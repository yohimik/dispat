package writer

import (
	"os"
	"strings"
	"testing"
)

func TestCargoRewritePreservesEveryOtherByte(t *testing.T) {
	// Every value shape a Cargo.toml can spell, plus comments and odd spacing.
	src := `[package]
name = "acme-cli"
version = "0.3.1"    # the shipped version

[dependencies]
serde = "1.0"
core = { path = "../core", version = "0.3" }
pretty = { package = "acme-pretty", version = "0.1" }
inherited = { workspace = true }
pathonly = { path = "../pathonly" }

[dev-dependencies]
insta = "1.34"

[build-dependencies]
cc = "1.0"
`
	path := seed(t, "Cargo.toml", src)

	res, err := Rewrite(path, "0.4.0", []Edit{
		{Name: "serde", Range: "1.1"},
		{Name: "core", Range: "0.4"},
		{Name: "acme-pretty", Range: "0.2"}, // resolved through the rename
		{Name: "insta", Kind: "devDependencies", Range: "1.40"},
		{Name: "cc", Range: "1.1"},        // build-dependencies count as plain
		{Name: "inherited", Range: "9.9"}, // workspace-inherited: nothing to write
		{Name: "pathonly", Range: "9.9"},  // no version key
		{Name: "insta", Range: "9.9"},     // wrong kind for this table
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`version = "0.3.1"    # the shipped version`, `version = "0.4.0"    # the shipped version`,
		`serde = "1.0"`, `serde = "1.1"`,
		`{ path = "../core", version = "0.3" }`, `{ path = "../core", version = "0.4" }`,
		`{ package = "acme-pretty", version = "0.1" }`, `{ package = "acme-pretty", version = "0.2" }`,
		`insta = "1.34"`, `insta = "1.40"`,
		`cc = "1.0"`, `cc = "1.1"`,
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	// The workspace-inherited and version-less entries are declared but carry
	// nothing to write, so they are skipped. Only the wrong-kind edit names
	// something the file does not declare.
	if !res.VersionWritten || len(res.Applied) != 5 {
		t.Errorf("result mismatch: %+v", res)
	}
	// insta is declared under [dev-dependencies] only, so the edit naming it as
	// a plain dependency is asking for something the file does not declare.
	if len(res.Skipped) != 2 || len(res.Missing) != 1 ||
		res.Missing[0].Name != "insta" || res.Missing[0].Kind != "" {
		t.Errorf("skipped/missing split wrong: skipped=%+v missing=%+v", res.Skipped, res.Missing)
	}
}

func TestCargoRewriteWorkspaceVersionLeftAlone(t *testing.T) {
	src := "[package]\nname = \"member\"\nversion = { workspace = true }\n"
	path := seed(t, "Cargo.toml", src)
	res, err := Rewrite(path, "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.VersionWritten || read(t, path) != src {
		t.Errorf("a workspace-inherited version must not be overridden: %+v", res)
	}
}

func TestCargoRewriteNoChangeLeavesFileAlone(t *testing.T) {
	src := "[package]\nversion = \"1.0.0\"\n\n[dependencies]\nserde = \"1.0\"\n"
	path := seed(t, "Cargo.toml", src)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Rewrite(path, "1.0.0", []Edit{{Name: "serde", Range: "1.0"}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 0 || len(res.Missing) != 0 || res.VersionWritten {
		t.Errorf("no-op rewrite reported work: %+v", res)
	}
	if read(t, path) != src || !after.ModTime().Equal(before.ModTime()) {
		t.Error("no-op rewrite touched the file")
	}
}

func TestCargoRewriteSubTableDependencyForm(t *testing.T) {
	// The sub-table spelling is as ordinary as the inline one in real crates,
	// and the scanner reads both, so the writer has to reach both.
	src := `[package]
name = "acme"
version = "1.0.0"

[dependencies]
serde = "1.0"

[dependencies.tokio]
version = "1.35"
features = ["full"]

[dev-dependencies.insta]
version = "1.34"

[dependencies.renamed]
package = "acme-real"
version = "0.1"
`
	path := seed(t, "Cargo.toml", src)
	res, err := Rewrite(path, "2.0.0", []Edit{
		{Name: "tokio", Range: "1.40"},
		{Name: "insta", Kind: "devDependencies", Range: "1.40"},
		{Name: "acme-real", Range: "0.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		"version = \"1.0.0\"", "version = \"2.0.0\"",
		"version = \"1.35\"", "version = \"1.40\"",
		"version = \"1.34\"", "version = \"1.40\"",
		"version = \"0.1\"", "version = \"0.2\"",
	).Replace(src)
	if got := read(t, path); got != want {
		t.Errorf("file mismatch:\n got: %q\nwant: %q", got, want)
	}
	if !res.VersionWritten || len(res.Applied) != 3 || len(res.Missing) != 0 || len(res.Skipped) != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}
