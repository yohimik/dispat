package publicapi_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"
)

func writeReleaseEdgeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPublicAPIWriterEditsEscapedAndRichTOMLVersions(t *testing.T) {
	t.Run("escaped basic string", func(t *testing.T) {
		path := writeReleaseEdgeFile(t, "libs.versions.toml", `[versions]
core = "1.0\\\"preview"
[libraries]
core = { module = "com.acme:core", version.ref = "core" }
`)
		res, err := writer.Rewrite(path, "", []writer.Edit{{Name: "com.acme:core", Range: "2.0.0"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Applied) != 1 || len(res.Missing) != 0 || len(res.Skipped) != 0 {
			t.Fatalf("rewrite result = %+v", res)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := `[versions]
core = "2.0.0"
[libraries]
core = { module = "com.acme:core", version.ref = "core" }
`
		if string(got) != want {
			t.Fatalf("rewritten catalog = %q, want %q", got, want)
		}
	})

	for _, key := range []string{"strictly", "prefer"} {
		t.Run("rich "+key, func(t *testing.T) {
			body := "[versions]\ncore = { " + key + " = \"1.0.0\" }\n[libraries]\ncore = { module = \"com.acme:core\", version.ref = \"core\" }\n"
			path := writeReleaseEdgeFile(t, "libs.versions.toml", body)
			res, err := writer.Rewrite(path, "", []writer.Edit{{Name: "com.acme:core", Range: "2.0.0"}})
			if err != nil || len(res.Applied) != 1 {
				t.Fatalf("rewrite result = %+v, err = %v", res, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "[versions]\ncore = { " + key + " = \"2.0.0\" }\n[libraries]\ncore = { module = \"com.acme:core\", version.ref = \"core\" }\n"
			if string(got) != want {
				t.Fatalf("rewritten catalog = %q, want %q", got, want)
			}
		})
	}
}

func TestPublicAPIWriterReportsUnrepresentableCatalogVersionsWithoutChangingTheFile(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantMissing bool
	}{
		{"rich version without a constraint", "[libraries]\ncore = { module = \"com.acme:core\", version = { reject = \"1.0.0\" } }\n", false},
		{"shorthand without a version", "[libraries]\ncore = \"com.acme:core\"\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReleaseEdgeFile(t, "libs.versions.toml", tc.body)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			res, err := writer.Rewrite(path, "", []writer.Edit{{Name: "com.acme:core", Range: "2.0.0"}})
			if err != nil {
				t.Fatal(err)
			}
			wantMissing, wantSkipped := 0, 1
			if tc.wantMissing {
				wantMissing, wantSkipped = 1, 0
			}
			if len(res.Applied) != 0 || len(res.Missing) != wantMissing || len(res.Skipped) != wantSkipped {
				t.Fatalf("rewrite result = %+v", res)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("declined rewrite changed file: before %q, after %q", before, after)
			}
		})
	}
}

func TestPublicAPIWriterRefusesInvalidGoModuleRequestsWithoutChangingTheFile(t *testing.T) {
	const body = "module example.com/app\n\ngo 1.26\n\nrequire example.com/core v1.0.0\n"
	cases := []struct {
		name string
		act  func(string) error
	}{
		{
			name: "invalid required version",
			act: func(path string) error {
				_, err := writer.Rewrite(path, "", []writer.Edit{{Name: "example.com/core", Range: "release"}})
				return err
			},
		},
		{
			name: "invalid replacement module version",
			act: func(path string) error {
				_, err := writer.Relink(path, []writer.Link{{Name: "example.com/core", Version: "release", Path: "../core"}})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReleaseEdgeFile(t, "go.mod", body)
			err := tc.act(path)
			if err == nil {
				t.Fatal("invalid go.mod edit succeeded")
			}
			if errors.Is(err, writer.ErrUnsupportedManifest) || errors.Is(err, writer.ErrManifestTooLarge) {
				t.Fatalf("invalid edit returned unrelated sentinel: %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != body {
				t.Fatalf("refused edit changed go.mod: %q", got)
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o640 {
				t.Fatalf("refused edit changed mode to %o", info.Mode().Perm())
			}
		})
	}
}

func TestPublicAPIWriterCanonicalizesLongDependencyKindWithoutMutatingCaller(t *testing.T) {
	path := writeReleaseEdgeFile(t, "package.json", `{"name":"app","dependencies":{"core":"1.0.0"}}`)
	edits := []writer.Edit{{Name: "core", Kind: manifest.Kind("dependencies"), Range: "2.0.0"}}
	res, err := writer.RewriteAs(path, manifest.FormatNpm, "", edits)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("rewrite result = %+v, err = %v", res, err)
	}
	if edits[0].Kind != manifest.Kind("dependencies") {
		t.Fatalf("RewriteAs mutated caller edit kind to %q", edits[0].Kind)
	}
}

func TestPublicAPIWriterLeavesStructurallyInvalidManifestsIntact(t *testing.T) {
	cases := []struct {
		name, path, body string
		edits            []writer.Edit
		want             string
	}{
		{"empty npm document", "package.json", "", nil, ""},
		{"empty O3DE document", "project.json", "", nil, ""},
		{"empty Unreal document", "Acme.uplugin", "", nil, ""},
		{
			"O3DE dependencies object",
			"gem.json",
			`{"gem_name":"Acme","version":"1.0.0","dependencies":{"Atom_RHI":"==1.0.0"}}`,
			[]writer.Edit{{Name: "Atom_RHI", Range: "==2.0.0"}},
			"",
		},
		{
			"O3DE empty dependency specifier",
			"gem.json",
			`{"gem_name":"Acme","version":"1.0.0","dependencies":[""]}`,
			[]writer.Edit{{Name: "Atom_RHI", Range: "==2.0.0"}},
			`{"gem_name":"Acme","version":"2.0.0","dependencies":[""]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeReleaseEdgeFile(t, tc.path, tc.body)
			_, rewriteErr := writer.Rewrite(path, "2.0.0", tc.edits)
			if tc.name != "O3DE empty dependency specifier" && rewriteErr == nil {
				t.Fatal("structurally invalid manifest was accepted")
			}
			if tc.name == "O3DE empty dependency specifier" && rewriteErr != nil {
				t.Fatalf("safe empty dependency entry was rejected: %v", rewriteErr)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if want == "" {
				want = tc.body
			}
			if string(got) != want {
				t.Fatalf("refused rewrite changed manifest: %q", got)
			}
		})
	}
}
