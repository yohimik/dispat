package publicapi_test

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

func TestPublicAPIWriterRefusesNonObjectNpmOverrideContainers(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"npm", `{"name":"acme","overrides":5}`, `"overrides" is not an object`},
		{"yarn", `{"name":"acme","resolutions":false}`, `"resolutions" is not an object`},
		{"pnpm field", `{"name":"acme","pnpm":{"overrides":"locked"}}`, `"overrides" is not an object`},
		{"pnpm parent", `{"name":"acme","packageManager":"pnpm@9","pnpm":"locked"}`, `"pnpm" is not an object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "package.json", tc.body)
			res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../core"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Relink = %+v, %v, want %q", res, err, tc.want)
			}
			if got := readFile(t, path); got != tc.body {
				t.Fatalf("refusal changed package.json:\n got %q\nwant %q", got, tc.body)
			}
		})
	}
}

func TestPublicAPIWriterDropsOnlyLocalPubspecOverrides(t *testing.T) {
	src := `name: acme
dependencies:
  core: ^1.0.0
dependency_overrides:
  constrained: ^2.0.0
  path_looking_constraint: ../still-a-constraint
  from_git:
    git:
      url: https://example.com/from_git.git
      path: not-a-top-level-path
  local:
    path: ../local
  flow_local: {path: "../flow dir"}
  flow_git: {git: https://example.com/flow.git}
`
	path := writeFile(t, t.TempDir(), "pubspec.yaml", src)
	links, err := writer.Links(path)
	if err != nil || renderLinks(links) != "flow_local=../flow dir,local=../local" {
		t.Fatalf("Links = %+v, %v", links, err)
	}
	before := readFile(t, path)
	missing, err := writer.Relink(path, []writer.Link{{Name: "constrained"}, {Name: "from_git"}, {Name: "flow_git"}})
	if err != nil || len(missing.Applied) != 0 || len(missing.Missing) != 3 {
		t.Fatalf("removing nonlocal overrides = %+v, %v", missing, err)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("nonlocal removal changed the file:\n%s", got)
	}

	res, err := writer.DropLinks(path)
	if err != nil || len(res.Applied) != 2 || len(res.Missing) != 0 {
		t.Fatalf("DropLinks = %+v, %v", res, err)
	}
	out := readFile(t, path)
	for _, keep := range []string{
		"constrained: ^2.0.0",
		"path_looking_constraint: ../still-a-constraint",
		"url: https://example.com/from_git.git",
		"path: not-a-top-level-path",
		"flow_git: {git: https://example.com/flow.git}",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("nonlocal override %q was removed:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "  local:") || strings.Contains(out, "  flow_local:") {
		t.Fatalf("a local override survived:\n%s", out)
	}
	links, err = writer.Links(path)
	if err != nil || len(links) != 0 {
		t.Fatalf("Links after DropLinks = %+v, %v", links, err)
	}
}

// TestPublicAPIWriterDoesNotInventALinkFromBrokenPubspecFlowSyntax keeps a
// truncated inline override from being treated as a local folder. The
// scanner may report an invalid manifest separately, but link inventory and
// cleanup must not turn a half-written mapping into an actionable redirect.
func TestPublicAPIWriterDoesNotInventALinkFromBrokenPubspecFlowSyntax(t *testing.T) {
	const body = "name: acme\ndependency_overrides:\n  core: {path: ../core\n"
	path := writeFile(t, t.TempDir(), "pubspec.yaml", body)
	links, err := writer.Links(path)
	if err != nil {
		t.Fatalf("Links: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("truncated flow mapping became local links: %+v", links)
	}
	res, err := writer.DropLinks(path)
	if err != nil || len(res.Applied) != 0 {
		t.Fatalf("DropLinks = %+v, %v", res, err)
	}
	if got := readFile(t, path); got != body {
		t.Fatalf("cleanup changed malformed source bytes: %q", got)
	}
}
