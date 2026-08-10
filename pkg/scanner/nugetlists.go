package scanner

import "strings"

// The two NuGet formats that are dependency lists and nothing else: Central
// Package Management, where a repository pins every version in one file and
// its projects reference packages without versions, and the legacy
// packages.config that PackageReference replaced. Neither declares an identity,
// which makes them the .NET counterparts of a Gradle version catalog and a pip
// requirements file respectively.

// packagesProps is the subset of a Directory.Packages.props the scanner reads.
type packagesProps struct {
	ItemGroups []struct {
		PackageVersions []struct {
			Include string `xml:"Include,attr"`
			Version string `xml:"Version,attr"`
		} `xml:"PackageVersion"`
	} `xml:"ItemGroup"`
}

// parsePackagesProps reads a Directory.Packages.props: the central version for
// every package the repository's projects consume. It carries no identity, and
// no kinds — which configuration a package ends up in is decided by the
// project that references it, not here — so every entry is a plain dependency,
// exactly as in a Gradle version catalog.
func parsePackagesProps(rel string, data []byte) (Manifest, error) {
	var raw packagesProps
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemNuGet, Root: isRoot(rel)}
	for _, ig := range raw.ItemGroups {
		for _, pv := range ig.PackageVersions {
			if pv.Include == "" {
				continue
			}
			m.Deps = append(m.Deps, DeclaredDep{Name: pv.Include, Range: pv.Version, Kind: KindDependencies})
		}
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// packagesConfig is the subset of a legacy packages.config the scanner reads.
// Its attributes are lower-case, unlike every other MSBuild-adjacent format.
type packagesConfig struct {
	Packages []struct {
		ID                    string `xml:"id,attr"`
		Version               string `xml:"version,attr"`
		DevelopmentDependency string `xml:"developmentDependency,attr"`
	} `xml:"package"`
}

// parsePackagesConfig reads a legacy packages.config — the pre-PackageReference
// way a project listed its packages. It declares no identity, and a package
// flagged developmentDependency installs for the project's own build rather
// than for its consumers, which is what devDependencies means everywhere else
// here.
func parsePackagesConfig(rel string, data []byte) (Manifest, error) {
	var raw packagesConfig
	if err := decodeXML(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemNuGet, Root: isRoot(rel)}
	for _, p := range raw.Packages {
		if p.ID == "" {
			continue
		}
		kind := KindDependencies
		if strings.EqualFold(strings.TrimSpace(p.DevelopmentDependency), "true") {
			kind = KindDevDependencies
		}
		m.Deps = append(m.Deps, DeclaredDep{Name: p.ID, Range: p.Version, Kind: kind})
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}
