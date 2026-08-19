package writer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/modfile"

	"github.com/yohimik/dispat/pkg/manifest"
)

// The read half of link management. Relink writes a directive knowing the
// dependency's name; these enumerate the directives a file already carries,
// which is what a CI gate needs to prove no local link survived a build, and
// what DropLinks needs to remove them all without being told their names.

// listFunc enumerates one format's link directives.
type listFunc func(path string) ([]Link, error)

// listers maps each linkable format onto its directive reader. It covers
// exactly the formats linkers does, fenced by a test, so listing and writing
// can never disagree about where a redirect may live.
var listers = map[manifest.Format]listFunc{
	manifest.FormatNpm:     listNpmLinks,
	manifest.FormatGoMod:   listGoModLinks,
	manifest.FormatPubspec: listPubspecLinks,
	manifest.FormatCargo: func(path string) ([]Link, error) {
		return tomlListLinks(path, "patch.crates-io")
	},
	manifest.FormatPyProject: func(path string) ([]Link, error) {
		return tomlListLinks(path, "tool.uv.sources")
	},
}

// Links enumerates the local-link directives the manifest at path carries:
// go.mod filesystem replaces, Cargo [patch.crates-io] and uv
// [tool.uv.sources] path entries, pubspec dependency_overrides paths, and npm
// file:/link: override specs. The result is sorted by name.
//
// A file name no manifest format claims gives ErrUnsupportedManifest,
// matching Relink. A recognised manifest whose format has no redirect carries
// none by definition and reports an empty list.
func Links(path string) ([]Link, error) {
	format, ok := manifest.FormatOfPath(path)
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, ErrUnsupportedManifest)
	}
	list, ok := listers[format]
	if !ok {
		return nil, nil
	}
	links, err := list(path)
	if err != nil {
		return nil, err
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })
	return links, nil
}

// DropLinks removes every local-link directive the manifest at path carries.
// It is Links followed by the matching removals, so the caller does not have
// to know the dependencies' names: the shape of an "unlink whatever the build
// linked" step. A manifest carrying no directive reports empty and is left
// untouched.
func DropLinks(path string) (LinkResult, error) {
	links, err := Links(path)
	if err != nil {
		return LinkResult{}, err
	}
	if len(links) == 0 {
		return LinkResult{Path: path}, nil
	}
	drops := make([]Link, len(links))
	for i, l := range links {
		drops[i] = Link{Name: l.Name, Version: l.Version}
	}
	return Relink(path, drops)
}

// listGoModLinks reads a go.mod's filesystem replaces. A replace whose
// replacement has no version points at a folder, relative or absolute; a
// replace onto another module version is not a local link and stays.
func listGoModLinks(path string) ([]Link, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return nil, err
	}
	f, err := modfile.Parse(path, sp.bytes(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Link
	for _, directive := range f.Replace {
		if directive.New.Version != "" {
			continue
		}
		out = append(out, Link{
			Name:    directive.Old.Path,
			Version: directive.Old.Version,
			Path:    directive.New.Path,
		})
	}
	return out, nil
}

// tomlListLinks reads the path entries of a package-keyed redirect table, the
// same table tomlLink writes. An entry redirecting to git or to a registry
// carries no path and is not a local link.
func tomlListLinks(path, table string) ([]Link, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(sp.bytes(), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	entries, ok := tomlLookupTable(doc, table)
	if !ok {
		return nil, nil
	}
	var out []Link
	for name, value := range entries {
		inline, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if p, ok := inline["path"].(string); ok && p != "" {
			out = append(out, Link{Name: name, Path: p})
		}
	}
	return out, nil
}

// listPubspecLinks reads a pubspec's dependency_overrides paths. An override
// redirecting to git or to a hosted source is not a local link and stays.
func listPubspecLinks(path string) ([]Link, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return nil, err
	}
	lines := sp.lines()
	start, end, ok := pubspecBlockBounds(lines, pubspecOverrides)
	if !ok {
		return nil, nil
	}
	depth := pubspecEntryIndent(lines, start, end)
	var out []Link
	for i := start; i < end; i++ {
		line := stripYAMLComment(lines[i])
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line)-len(strings.TrimLeft(line, " \t")) != depth {
			continue
		}
		name, _, ok := yamlKey(line)
		if !ok {
			continue
		}
		if entry, found := pubspecOverrideEntry(lines, name); found && entry.path != "" {
			out = append(out, Link{Name: name, Path: entry.path})
		}
	}
	return out, nil
}

// npmOverrideFields is every field a redirect may sit in, whatever manager
// the file belongs to; a lister has to look everywhere the three managers
// read, not only where linkNpm would write next.
var npmOverrideFields = [][]string{{"resolutions"}, {"pnpm", "overrides"}, {"overrides"}}

// listNpmLinks reads a package.json's file: and link: override specs. A
// version override ("^1.2.3") pins rather than redirects and is not a local
// link.
func listNpmLinks(path string) ([]Link, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(sp.bytes(), &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	var out []Link
	for _, field := range npmOverrideFields {
		obj := doc
		for _, key := range field {
			obj, _ = obj[key].(map[string]any)
			if obj == nil {
				break
			}
		}
		for name, value := range obj {
			spec, ok := value.(string)
			if !ok || seen[name] {
				continue
			}
			for _, prefix := range []string{"file:", "link:"} {
				if strings.HasPrefix(spec, prefix) {
					seen[name] = true
					out = append(out, Link{Name: name, Path: strings.TrimPrefix(spec, prefix)})
					break
				}
			}
		}
	}
	return out, nil
}
