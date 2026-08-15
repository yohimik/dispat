package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestEveryLinkerHasALister(t *testing.T) {
	// Listing and writing share one idea of where a redirect may live; a
	// format joining one table without the other would let a link be written
	// that no verification can find.
	for format := range linkers {
		if _, ok := listers[format]; !ok {
			t.Errorf("format %q has a linker but no lister", format)
		}
	}
	for format := range listers {
		if _, ok := linkers[format]; !ok {
			t.Errorf("format %q has a lister but no linker", format)
		}
	}
}

func TestLinksReadsEveryGoModReplaceShape(t *testing.T) {
	// Inline and block form, relative and absolute targets, and a versioned
	// replace: all filesystem redirects. A replace onto another module
	// version is not a local link and stays out of the answer.
	src := `module example.com/m

go 1.25.0

require (
	example.com/a v1.0.0
	example.com/b v1.1.0
	example.com/c v1.2.0
	example.com/d v1.3.0
)

replace example.com/a => ../a

replace (
	example.com/b => /abs/b
	example.com/c v1.2.0 => ../c
	example.com/d => example.com/d-fork v1.3.1
)
`
	path := seed(t, "go.mod", src)
	links, err := Links(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		{Name: "example.com/a", Path: "../a"},
		{Name: "example.com/b", Path: "/abs/b"},
		{Name: "example.com/c", Version: "v1.2.0", Path: "../c"},
	}
	if len(links) != len(want) {
		t.Fatalf("Links = %+v, want %+v", links, want)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("Links[%d] = %+v, want %+v", i, links[i], want[i])
		}
	}

	res, err := DropLinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 3 {
		t.Errorf("DropLinks applied %+v, want all three", res.Applied)
	}
	got := read(t, path)
	if strings.Contains(got, "=> ../a") || strings.Contains(got, "/abs/b") || strings.Contains(got, "=> ../c") {
		t.Errorf("a filesystem replace survived:\n%s", got)
	}
	if !strings.Contains(got, "example.com/d-fork") {
		t.Errorf("the module-version replace must survive a drop:\n%s", got)
	}
}

func TestLinksAndDropOnTheTOMLFormats(t *testing.T) {
	cases := map[string]struct{ file, src, gone string }{
		"cargo": {
			"Cargo.toml",
			"[package]\nname = \"acme\"\n\n[dependencies]\ncore = \"1.0\"\n\n[patch.crates-io]\ncore = { path = \"../core\" }\ngit-dep = { git = \"https://example.com/x\" }\n",
			"[patch.crates-io]",
		},
		"pyproject": {
			"pyproject.toml",
			"[project]\nname = \"acme\"\n\n[tool.uv.sources]\ncore = { path = \"../core\" }\n",
			"[tool.uv.sources]",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := seed(t, tc.file, tc.src)
			links, err := Links(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 1 || links[0].Name != "core" || links[0].Path != "../core" {
				t.Fatalf("Links = %+v, want core -> ../core alone (a git patch is not a local link)", links)
			}
			if _, err := DropLinks(path); err != nil {
				t.Fatal(err)
			}
			got := read(t, path)
			if strings.Contains(got, "../core") {
				t.Errorf("the path redirect survived:\n%s", got)
			}
			if name == "pyproject" && strings.Contains(got, tc.gone) {
				t.Errorf("an emptied table must go with its last entry:\n%s", got)
			}
			after, err := Links(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != 0 {
				t.Errorf("Links after a drop = %+v, want none", after)
			}
		})
	}
}

func TestLinksAndDropOnPubspec(t *testing.T) {
	src := "name: acme\ndependencies:\n  core: ^1.0.0\n\ndependency_overrides:\n  core:\n    path: ../core\n"
	path := seed(t, "pubspec.yaml", src)
	links, err := Links(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Name != "core" || links[0].Path != "../core" {
		t.Fatalf("Links = %+v", links)
	}
	if _, err := DropLinks(path); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "dependency_overrides") {
		t.Errorf("an emptied overrides block must go with its last entry:\n%s", got)
	}
}

func TestLinksReadsEveryNpmOverrideField(t *testing.T) {
	// The three managers keep their redirects in three fields; the lister
	// reads all of them, whatever field a writer would choose today, and a
	// version override is a pin rather than a redirect.
	src := `{
  "name": "@acme/web",
  "resolutions": { "left-pad": "file:../left-pad" },
  "pnpm": { "overrides": { "core": "link:../core" } },
  "overrides": { "pinned": "^1.2.3" }
}`
	path := seed(t, "package.json", src)
	links, err := Links(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("Links = %+v, want core and left-pad", links)
	}
	if links[0].Name != "core" || links[0].Path != "../core" {
		t.Errorf("Links[0] = %+v", links[0])
	}
	if links[1].Name != "left-pad" || links[1].Path != "../left-pad" {
		t.Errorf("Links[1] = %+v", links[1])
	}
}

func TestLinksOnFilesWithoutAny(t *testing.T) {
	// A linkable manifest with no directive, and a recognised format that
	// cannot hold one: both report empty without an error, and a drop leaves
	// both untouched.
	for _, tc := range []struct{ file, src string }{
		{"go.mod", "module example.com/m\n\ngo 1.25.0\n"},
		{"pom.xml", "<project><version>1.0.0</version></project>"},
	} {
		path := seed(t, tc.file, tc.src)
		links, err := Links(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if len(links) != 0 {
			t.Errorf("%s: Links = %+v, want none", tc.file, links)
		}
		res, err := DropLinks(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if len(res.Applied) != 0 {
			t.Errorf("%s: DropLinks applied %+v, want nothing", tc.file, res.Applied)
		}
		if got := read(t, path); got != tc.src {
			t.Errorf("%s: the file changed:\n%s", tc.file, got)
		}
	}
}

func TestLinksRefusesWhatNoFormatClaims(t *testing.T) {
	path := seed(t, "notes.txt", "nothing\n")
	if _, err := Links(path); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("Links on a non-manifest: got %v, want ErrUnsupportedManifest", err)
	}
	if _, err := DropLinks(path); !errors.Is(err, ErrUnsupportedManifest) {
		t.Errorf("DropLinks on a non-manifest: got %v, want ErrUnsupportedManifest", err)
	}
}
