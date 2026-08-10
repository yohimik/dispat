package scanner

import (
	"path/filepath"
	"strings"
)

// csprojManifest is the subset of an SDK-style .csproj the scanner reads.
// Properties repeat across PropertyGroups (per-configuration), so every
// group is collected and the first non-empty value wins.
type csprojManifest struct {
	PropertyGroups []struct {
		PackageID    string `xml:"PackageId"`
		AssemblyName string `xml:"AssemblyName"`
		Version      string `xml:"Version"`
	} `xml:"PropertyGroup"`
	ItemGroups []struct {
		PackageReferences []struct {
			Include string `xml:"Include,attr"`
			Version string `xml:"Version,attr"`
			// Version may also be a child element instead of an attribute.
			VersionElem string `xml:"Version"`
		} `xml:"PackageReference"`
		ProjectReferences []struct {
			Include string `xml:"Include,attr"`
		} `xml:"ProjectReference"`
	} `xml:"ItemGroup"`
}

// parseCsproj reads an SDK-style .csproj. The package name is PackageId, then
// AssemblyName, then the file's base name: the same fallback NuGet applies.
// PackageReference entries are registry dependencies; ProjectReference entries
// are in-repo couplings, declared as the referenced project's base name with
// the (slash-normalised) relative path as the local-path signal: the strongest
// workspace evidence a .NET solution has.
func parseCsproj(rel string, data []byte) (Manifest, error) {
	var raw csprojManifest
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemNuGet, Root: isRoot(rel)}
	// PackageId from any group beats AssemblyName from any group: a project
	// often sets AssemblyName in an early per-configuration group and its
	// PackageId in a later one, and NuGet's precedence is by property, not by
	// group order.
	for _, pg := range raw.PropertyGroups {
		if m.Name == "" && pg.PackageID != "" {
			m.Name = pg.PackageID
		}
		if m.Version == "" {
			m.Version = pg.Version
		}
	}
	if m.Name == "" {
		for _, pg := range raw.PropertyGroups {
			if pg.AssemblyName != "" {
				m.Name = pg.AssemblyName
				break
			}
		}
	}
	if m.Name == "" {
		m.Name = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	}
	for _, ig := range raw.ItemGroups {
		for _, pr := range ig.PackageReferences {
			rng := pr.Version
			if rng == "" {
				rng = strings.TrimSpace(pr.VersionElem)
			}
			m.Deps = append(m.Deps, DeclaredDep{Name: pr.Include, Range: rng, Kind: KindDependencies})
		}
		for _, pr := range ig.ProjectReferences {
			// MSBuild paths use backslashes on every platform.
			path := strings.ReplaceAll(pr.Include, `\`, "/")
			base := filepath.Base(path)
			m.Deps = append(m.Deps, DeclaredDep{
				Name:      strings.TrimSuffix(base, filepath.Ext(base)),
				Kind:      KindDependencies,
				LocalPath: path,
			})
		}
	}
	sortDeps(m.Deps)
	return m, nil
}
