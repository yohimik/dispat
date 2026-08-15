package scanner

import (
	"github.com/yohimik/dispat/pkg/manifest"

	"strings"

	"github.com/pelletier/go-toml/v2"
)

// pythonManifest is the subset of pyproject.toml the scanner reads: the PEP
// 621 [project] table with its dependency lists, the PEP 735
// [dependency-groups] table, and the Poetry tables older projects still use.
// Values that may be either strings or tables decode as `any`.
type pythonManifest struct {
	Project struct {
		Name         string              `toml:"name"`
		Version      string              `toml:"version"`
		Dependencies []string            `toml:"dependencies"`
		Optional     map[string][]string `toml:"optional-dependencies"`
	} `toml:"project"`
	DependencyGroups map[string][]any `toml:"dependency-groups"`
	Tool             struct {
		Poetry struct {
			Name            string         `toml:"name"`
			Version         string         `toml:"version"`
			Dependencies    map[string]any `toml:"dependencies"`
			DevDependencies map[string]any `toml:"dev-dependencies"`
			Group           map[string]struct {
				Dependencies map[string]any `toml:"dependencies"`
			} `toml:"group"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// parsePython reads a pyproject.toml. PEP 621 [project] wins the identity when
// present, Poetry's [tool.poetry] otherwise. Kinds: [project.dependencies] and
// Poetry's main group are runtime; [project.optional-dependencies] (extras)
// are optionalDependencies; PEP 735 dependency groups and every Poetry
// non-main group (dev, test, docs, ...) are devDependencies, they install for
// contributors, not consumers. Names are normalised per PEP 503
// (case-insensitive, runs of -_. collapse to -) on both the package and its
// dependencies, so "acme_core" and "Acme.Core" meet as one name.
func parsePython(rel string, data []byte) (Manifest, error) {
	var raw pythonManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemPython, Root: isRoot(rel)}

	name, version := raw.Project.Name, raw.Project.Version
	if name == "" {
		name, version = raw.Tool.Poetry.Name, raw.Tool.Poetry.Version
	}
	m.Name = normalizePyName(name)
	m.Version = version

	for _, req := range raw.Project.Dependencies {
		m.Deps = append(m.Deps, pep508Dep(req, KindDependencies))
	}
	for _, reqs := range raw.Project.Optional {
		for _, req := range reqs {
			m.Deps = append(m.Deps, pep508Dep(req, KindOptionalDependencies))
		}
	}
	for _, entries := range raw.DependencyGroups {
		for _, entry := range entries {
			// A group entry is a PEP 508 string or an {include-group = ...}
			// table; only the strings are dependencies.
			if req, ok := entry.(string); ok {
				m.Deps = append(m.Deps, pep508Dep(req, KindDevDependencies))
			}
		}
	}
	poetryDeps(&m, raw.Tool.Poetry.Dependencies, KindDependencies)
	poetryDeps(&m, raw.Tool.Poetry.DevDependencies, KindDevDependencies)
	for _, group := range raw.Tool.Poetry.Group {
		poetryDeps(&m, group.Dependencies, KindDevDependencies)
	}
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// poetryDeps coerces one Poetry dependency table: values are a version
// string ("^1.2") or a table with version/path keys. The "python" entry is a
// platform constraint, not a dependency.
func poetryDeps(m *Manifest, table map[string]any, kind Kind) {
	for name, value := range table {
		if strings.EqualFold(name, "python") {
			continue
		}
		dep := DeclaredDep{Name: normalizePyName(name), Kind: kind}
		switch v := value.(type) {
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
			continue // a list of constraint tables: platform-specific, skipped
		}
		m.Deps = append(m.Deps, dep)
	}
}

// pep508Dep splits one PEP 508 requirement string ("requests[socks]>=2.0,
// <3; python_version > '3.8'" or "core @ file:../core") into a declaration:
// the distribution name up to the first extras/version/marker/url character,
// the version specifier as the range, and a local path for relative file
// references.
func pep508Dep(req string, kind Kind) DeclaredDep {
	req = strings.TrimSpace(req)
	name := req
	rest := ""
	for i, r := range req {
		if strings.ContainsRune("[<>=!~;@( ", r) {
			name, rest = req[:i], strings.TrimSpace(req[i:])
			break
		}
	}
	dep := DeclaredDep{Name: normalizePyName(name), Kind: kind}
	// Extras ([socks]) sit between the name and the specifier and are not
	// version text.
	if strings.HasPrefix(rest, "[") {
		if end := strings.Index(rest, "]"); end >= 0 {
			rest = strings.TrimSpace(rest[end+1:])
		}
	}
	// The environment marker is a condition, not a version.
	if semi := strings.Index(rest, ";"); semi >= 0 {
		rest = strings.TrimSpace(rest[:semi])
	}
	if url, ok := strings.CutPrefix(rest, "@"); ok {
		url = strings.TrimSpace(url)
		dep.Range = "@ " + url
		if p, ok := strings.CutPrefix(url, "file:"); ok && isRelativePath(p) {
			dep.LocalPath = p
		} else if isRelativePath(url) {
			dep.LocalPath = url
		}
		return dep
	}
	dep.Range = strings.TrimSpace(strings.Trim(rest, "()"))
	return dep
}

// normalizePyName applies PEP 503: names compare case-insensitively with
// runs of -, _ and . collapsed to a single -.
func normalizePyName(name string) string { return manifest.NormalizePyName(name) }

// dedupeDeps drops exact duplicates: a package listed both in [project] and a
// Poetry table during a migration should not become two declarations. The
// result is a fresh slice; the caller's stays as it was, so the helper is safe
// for a slice the caller also retains.
func dedupeDeps(deps []DeclaredDep) []DeclaredDep {
	seen := make(map[DeclaredDep]bool, len(deps))
	out := make([]DeclaredDep, 0, len(deps))
	for _, d := range deps {
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
