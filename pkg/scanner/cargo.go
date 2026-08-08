package scanner

import (
	"github.com/pelletier/go-toml/v2"
)

// cargoManifest is the subset of Cargo.toml the scanner reads. Dependency
// tables hold either a version string or an inline table, so their values
// decode as `any` and are coerced afterwards; the package version may itself
// be `{ workspace = true }`, so it is `any` too.
type cargoManifest struct {
	Package struct {
		Name    string `toml:"name"`
		Version any    `toml:"version"`
	} `toml:"package"`
	Dependencies      map[string]any `toml:"dependencies"`
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
}

// parseCargo reads a Cargo.toml: [package] name/version plus the three
// dependency tables. dev-dependencies map onto devDependencies;
// build-dependencies count as plain dependencies — a build dependency's
// change still forces the consumer to rebuild, which is exactly what the
// runtime kind propagates. A renamed dependency (`alias = { package = "real"
// }`) is declared under its real package name.
func parseCargo(rel string, data []byte) (Manifest, error) {
	var raw cargoManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemCargo,
		Name:      raw.Package.Name,
		Root:      isRoot(rel),
	}
	if v, ok := raw.Package.Version.(string); ok {
		m.Version = v
	}
	cargoDeps(&m, KindDependencies, raw.Dependencies)
	cargoDeps(&m, KindDevDependencies, raw.DevDependencies)
	// build-dependencies count as plain dependencies: a build dependency's
	// change still forces the consumer to rebuild.
	cargoDeps(&m, KindDependencies, raw.BuildDependencies)
	sortDeps(m.Deps)
	return m, nil
}

// cargoDeps coerces one dependency table's entries into declarations.
func cargoDeps(m *Manifest, kind Kind, table map[string]any) {
	for name, value := range table {
		dep := DeclaredDep{Name: name, Kind: kind}
		switch v := value.(type) {
		case string: // plain `serde = "1.0"`
			dep.Range = v
		case map[string]any: // inline table
			if pkg, ok := v["package"].(string); ok && pkg != "" {
				dep.Name = pkg
			}
			if rng, ok := v["version"].(string); ok {
				dep.Range = rng
			}
			if p, ok := v["path"].(string); ok {
				dep.LocalPath = p
			}
		default:
			// An unrecognised value shape (a bool, a number) drops this entry
			// alone, matching how the python and pubspec parsers treat the
			// same situation — one odd declaration must not void a manifest.
			continue
		}
		m.Deps = append(m.Deps, dep)
	}
}
