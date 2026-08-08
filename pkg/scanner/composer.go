package scanner

import (
	"encoding/json"
	"strings"
)

// composerManifest is the subset of composer.json the scanner reads.
type composerManifest struct {
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
}

// parseComposer reads a composer.json: vendor/package name, the (rarely
// declared) version, require and require-dev. Platform requirements (php,
// ext-*, lib-*, composer-*) are constraints on the runtime, not packages,
// and are skipped. Composer's path repositories are declared per repository,
// not per dependency, so no local-path signal is attributable to a specific
// requirement — workspace edges come from name matching alone here.
func parseComposer(rel string, data []byte) (Manifest, error) {
	var raw composerManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemComposer,
		Name:      raw.Name,
		Version:   raw.Version,
		Root:      isRoot(rel),
	}
	for kind, table := range map[Kind]map[string]string{
		KindDependencies:    raw.Require,
		KindDevDependencies: raw.RequireDev,
	} {
		for name, rng := range table {
			if composerPlatform(name) {
				continue
			}
			m.Deps = append(m.Deps, DeclaredDep{Name: name, Range: rng, Kind: kind})
		}
	}
	sortDeps(m.Deps)
	return m, nil
}

// composerPlatform reports a platform requirement: the PHP runtime itself,
// an extension or a system library.
func composerPlatform(name string) bool {
	return name == "php" || name == "composer" ||
		strings.HasPrefix(name, "php-") ||
		strings.HasPrefix(name, "ext-") ||
		strings.HasPrefix(name, "lib-") ||
		strings.HasPrefix(name, "composer-")
}
