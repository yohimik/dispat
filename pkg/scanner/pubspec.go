package scanner

import (
	"gopkg.in/yaml.v3"
)

// pubspecManifest is the subset of pubspec.yaml the scanner reads.
// Dependency values are a version string, or a map carrying path / sdk /
// git / hosted details, so they decode as `any`.
type pubspecManifest struct {
	Name            string         `yaml:"name"`
	Version         string         `yaml:"version"`
	Dependencies    map[string]any `yaml:"dependencies"`
	DevDependencies map[string]any `yaml:"dev_dependencies"`
	Overrides       map[string]any `yaml:"dependency_overrides"`
}

// parsePubspec reads a Dart/Flutter pubspec.yaml: name, version,
// dependencies and dev_dependencies (devDependencies), plus
// dependency_overrides — which is where monorepos point names at local
// folders, so its path entries matter even though it declares no range.
// `path:` entries yield the local-path signal; sdk dependencies (flutter)
// carry no version and match no workspace package, which keeps them inert.
func parsePubspec(rel string, data []byte) (Manifest, error) {
	var raw pubspecManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemPub,
		Name:      raw.Name,
		Version:   raw.Version,
		Root:      isRoot(rel),
	}
	pubDeps(&m, raw.Dependencies, KindDependencies)
	pubDeps(&m, raw.DevDependencies, KindDevDependencies)
	pubDeps(&m, raw.Overrides, KindDependencies)
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// pubDeps coerces one pubspec dependency table.
func pubDeps(m *Manifest, table map[string]any, kind Kind) {
	for name, value := range table {
		dep := DeclaredDep{Name: name, Kind: kind}
		switch v := value.(type) {
		case nil: // `core:` — any version
		case string:
			dep.Range = v
		case map[string]any:
			if rng, ok := v["version"].(string); ok {
				dep.Range = rng
			}
			if p, ok := v["path"].(string); ok {
				dep.LocalPath = p
			}
		default:
			continue
		}
		m.Deps = append(m.Deps, dep)
	}
}
