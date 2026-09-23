package publicapi_test

import (
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/writer"
)

// A hand-edited package.json may carry the same local redirect under several
// package managers. Dropping build-time links must leave none of those paths.
func TestPublicAPIWriterDropsDuplicateNpmLinksAcrossOverrideFields(t *testing.T) {
	const body = `{
  "name": "app",
  "resolutions": {"core": "file:../core-yarn", "kept": "^2.0.0"},
  "pnpm": {"overrides": {"core": "link:../core-pnpm"}},
  "overrides": {"core": "file:../core-npm"}
}
`
	path := writeFile(t, t.TempDir(), "package.json", body)
	links, err := writer.Links(path)
	if err != nil || len(links) != 1 || links[0].Name != "core" {
		t.Fatalf("Links = %+v, %v", links, err)
	}
	res, err := writer.DropLinks(path)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("DropLinks = %+v, %v", res, err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "core-yarn") || strings.Contains(got, "core-pnpm") || strings.Contains(got, "core-npm") {
		t.Fatalf("a local redirect survived:\n%s", got)
	}
	if !strings.Contains(got, `"kept": "^2.0.0"`) {
		t.Fatalf("an unrelated registry override was removed:\n%s", got)
	}
	links, err = writer.Links(path)
	if err != nil || len(links) != 0 {
		t.Fatalf("Links after drop = %+v, %v", links, err)
	}
}

func TestPublicAPIWriterKeepsRegistryOverrideBesideDuplicateLocalLink(t *testing.T) {
	const body = `{
  "name": "app",
  "resolutions": {"core": "^2.0.0"},
  "pnpm": {"overrides": {"core": "file:../old"}},
  "overrides": {"core": "link:../older"}
}
`
	path := writeFile(t, t.TempDir(), "package.json", body)
	res, err := writer.Relink(path, []writer.Link{{Name: "core", Path: "../new"}})
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("repoint duplicate links = %+v, %v", res, err)
	}
	got := readFile(t, path)
	if strings.Count(got, `"core": "file:../new"`) != 2 ||
		!strings.Contains(got, `"resolutions": {"core": "^2.0.0"}`) {
		t.Fatalf("repoint did not preserve the registry override and update both links:\n%s", got)
	}
	res, err = writer.DropLinks(path)
	if err != nil || len(res.Applied) != 1 {
		t.Fatalf("drop duplicate links = %+v, %v", res, err)
	}
	got = readFile(t, path)
	if strings.Contains(got, "file:../new") || !strings.Contains(got, `"resolutions": {"core": "^2.0.0"}`) {
		t.Fatalf("drop disturbed the registry override or left a local link:\n%s", got)
	}
}
