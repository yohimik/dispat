package scanner

import (
	"encoding/json"
	"strings"
)

// npmManifest is the subset of package.json the scanner reads. Dependency
// values that are not strings (invalid per npm) fail the decode and mark the
// manifest malformed.
type npmManifest struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// parseNpm reads a package.json: name, version and all four dependency
// fields. "file:" and "link:" ranges also yield the local path.
func parseNpm(rel string, data []byte) (Manifest, error) {
	var raw npmManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemNpm,
		Name:      raw.Name,
		Version:   raw.Version,
		Root:      isRoot(rel),
	}
	for kind, field := range map[Kind]map[string]string{
		KindDependencies:         raw.Dependencies,
		KindDevDependencies:      raw.DevDependencies,
		KindPeerDependencies:     raw.PeerDependencies,
		KindOptionalDependencies: raw.OptionalDependencies,
	} {
		for name, rng := range field {
			m.Deps = append(m.Deps, DeclaredDep{
				Name:      name,
				Range:     rng,
				Kind:      kind,
				LocalPath: npmLocalPath(rng),
			})
		}
	}
	sortDeps(m.Deps)
	return m, nil
}

// npmLocalPath extracts the filesystem path of a "file:" or "link:" range;
// other ranges (registry, workspace:, git URLs) yield "".
func npmLocalPath(rng string) string {
	for _, prefix := range []string{"file:", "link:"} {
		if strings.HasPrefix(rng, prefix) {
			return strings.TrimPrefix(rng, prefix)
		}
	}
	return ""
}
