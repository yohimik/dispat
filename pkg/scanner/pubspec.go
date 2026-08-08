package scanner

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// pubspecManifest is the subset of pubspec.yaml the scanner reads.
// Dependency values are a version string, or a map carrying path / sdk /
// git / hosted details, so they decode as `any` — and so do the identity
// fields, because YAML happily types `version: 1.0` as a float and a strict
// string field would fail the whole manifest over it.
type pubspecManifest struct {
	Name            any            `yaml:"name"`
	Version         any            `yaml:"version"`
	Dependencies    map[string]any `yaml:"dependencies"`
	DevDependencies map[string]any `yaml:"dev_dependencies"`
	Overrides       map[string]any `yaml:"dependency_overrides"`
}

// parsePubspec reads a Dart/Flutter pubspec.yaml: name, version,
// dependencies and dev_dependencies (devDependencies), plus
// dependency_overrides — which is where monorepos point names at local
// folders. An override *annotates* the declaration it overrides (its path
// becomes the local-path signal) rather than appearing as a second
// declaration of the same name; an override for an undeclared name is kept
// as a plain dependency. sdk dependencies (flutter) carry no version and
// match no workspace package, which keeps them inert.
func parsePubspec(rel string, data []byte) (Manifest, error) {
	var raw pubspecManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemPub,
		Name:      yamlScalar(raw.Name),
		Version:   yamlScalar(raw.Version),
		Root:      isRoot(rel),
	}
	pubDeps(&m, raw.Dependencies, KindDependencies)
	pubDeps(&m, raw.DevDependencies, KindDevDependencies)
	applyPubOverrides(&m, raw.Overrides)
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// yamlScalar renders a YAML scalar as its string spelling: a string as-is,
// anything else (the float `version: 1.0` case) via fmt.
func yamlScalar(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

// applyPubOverrides folds dependency_overrides onto the declarations: an
// override's path (and, where the declaration had none, its version) attaches
// to every existing entry of that name, whatever table it came from.
func applyPubOverrides(m *Manifest, table map[string]any) {
	if len(table) == 0 {
		return
	}
	var overrides Manifest
	pubDeps(&overrides, table, KindDependencies)
	for _, o := range overrides.Deps {
		found := false
		for i := range m.Deps {
			if m.Deps[i].Name != o.Name {
				continue
			}
			found = true
			if o.LocalPath != "" {
				m.Deps[i].LocalPath = o.LocalPath
			}
			if m.Deps[i].Range == "" {
				m.Deps[i].Range = o.Range
			}
		}
		if !found {
			m.Deps = append(m.Deps, o)
		}
	}
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
