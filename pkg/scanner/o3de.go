package scanner

import (
	"encoding/json"
	"strings"
)

// o3deManifest is the subset of an O3DE project.json or gem.json the scanner
// reads. The two files are the same shape naming themselves differently: a
// project says project_name, a gem says gem_name.
type o3deManifest struct {
	ProjectName  string   `json:"project_name"`
	GemName      string   `json:"gem_name"`
	Version      string   `json:"version"`
	Dependencies []string `json:"dependencies"`
}

// parseO3DEProject reads an O3DE project.json: the project's name, its version
// and the gems it depends on.
//
// The base name project.json is not unique to O3DE, so a file of that name
// belonging to something else parses to an empty manifest rather than a wrong
// one: with no project_name there is no name, and with no version there is no
// version. That is the same answer the plist reader gives a property list with
// no root dictionary, and it is the honest one.
func parseO3DEProject(rel string, data []byte) (Manifest, error) {
	return parseO3DE(rel, data, func(raw o3deManifest) string { return raw.ProjectName })
}

// parseO3DEGem reads an O3DE gem.json, the manifest of one reusable gem, on
// the same terms.
func parseO3DEGem(rel string, data []byte) (Manifest, error) {
	return parseO3DE(rel, data, func(raw o3deManifest) string { return raw.GemName })
}

// parseO3DE reads either O3DE manifest, nameOf picking the field that carries
// the identity.
func parseO3DE(rel string, data []byte, nameOf func(o3deManifest) string) (Manifest, error) {
	var raw o3deManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemO3DE,
		Name:      nameOf(raw),
		Version:   raw.Version,
		Root:      isRoot(rel),
	}
	for _, spec := range raw.Dependencies {
		if name, rng, ok := o3deSpec(spec); ok {
			m.Deps = append(m.Deps, DeclaredDep{Name: name, Range: rng, Kind: KindDependencies})
		}
	}
	sortDeps(m.Deps)
	return m, nil
}

// o3deSpec splits a dependency specifier into its gem name and version text:
// "Atom==1.0.0" into "Atom" and "==1.0.0", a bare "Camera" into "Camera" and
// no range at all.
//
// The operator stays with the range, the way every other range in this package
// keeps its own text, so a rewrite puts back what the file spells rather than
// a normalised form. The name is kept verbatim: O3DE gem names are
// capitalised, and the PEP 503 folding the Python reader applies would turn
// Atom into atom and stop it matching the gem that declares it.
func o3deSpec(spec string) (name, rng string, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", false
	}
	for i := 0; i < len(spec); i++ {
		if strings.IndexByte("<>=!~", spec[i]) >= 0 {
			return strings.TrimSpace(spec[:i]), strings.TrimSpace(spec[i:]), i > 0
		}
	}
	return spec, "", true
}
