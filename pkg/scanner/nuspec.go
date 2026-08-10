package scanner

import (
	"strings"
)

// nuspecManifest is the subset of a .nuspec the scanner reads. Dependencies
// are declared either flat or inside <group> elements scoped to a target
// framework; both spellings mean the same thing to a dependency graph, so both
// are collected.
type nuspecManifest struct {
	Metadata struct {
		ID           string `xml:"id"`
		Version      string `xml:"version"`
		Dependencies struct {
			Direct []nuspecDep `xml:"dependency"`
			Groups []struct {
				Dependencies []nuspecDep `xml:"dependency"`
			} `xml:"group"`
		} `xml:"dependencies"`
	} `xml:"metadata"`
}

type nuspecDep struct {
	ID      string `xml:"id,attr"`
	Version string `xml:"version,attr"`
}

// parseNuspec reads a .nuspec — NuGet's own package manifest, the format that
// actually describes a published package, where a .csproj describes the
// project that builds one. It is the .NET analogue of a podspec or a gemspec:
// full identity plus dependencies.
//
// A nuspec may be a template rather than a finished manifest: `<id>$id$</id>`
// and `<version>$version$</version>` are replacement tokens NuGet fills in from
// the project at pack time. A token version is kept verbatim, matching how the
// Maven parser keeps a ${property}; a token *identifier* is dropped, because
// every templated package spells it identically and NameIndex would report the
// shared literal as an ambiguous name.
//
// Version text is kept exactly as written, including NuGet's interval notation
// ("[1.0,2.0)"), which names an exact dependency just as a bare version does.
func parseNuspec(rel string, data []byte) (Manifest, error) {
	var raw nuspecManifest
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemNuGet,
		Version:   raw.Metadata.Version,
		Root:      isRoot(rel),
	}
	if id := raw.Metadata.ID; !isNuspecToken(id) {
		m.Name = id
	}
	deps := raw.Metadata.Dependencies.Direct
	for _, g := range raw.Metadata.Dependencies.Groups {
		deps = append(deps, g.Dependencies...)
	}
	for _, d := range deps {
		if d.ID == "" {
			continue
		}
		m.Deps = append(m.Deps, DeclaredDep{Name: d.ID, Range: d.Version, Kind: KindDependencies})
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// isNuspecToken reports a $token$ NuGet replaces at pack time rather than a
// literal value.
func isNuspecToken(v string) bool {
	v = strings.TrimSpace(v)
	return len(v) > 2 && strings.HasPrefix(v, "$") && strings.HasSuffix(v, "$")
}
