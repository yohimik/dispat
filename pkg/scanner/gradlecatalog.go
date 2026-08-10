package scanner

import (
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// gradleCatalog is the subset of a Gradle version catalog the scanner reads.
// Both tables hold values that are either a string or an inline table, so
// they decode as `any` and are coerced afterwards — the same shape the Cargo
// parser uses for the identical reason.
type gradleCatalog struct {
	Versions  map[string]any `toml:"versions"`
	Libraries map[string]any `toml:"libraries"`
}

// parseGradleCatalog reads a Gradle version catalog (conventionally
// gradle/libs.versions.toml). Dependencies come from [libraries], named by
// their Maven coordinate rather than their catalog alias: the alias is a
// file-local label, whereas the coordinate is what a pom.xml or a build script
// declaring the same library also spells, so the three unify in one graph.
//
// A version.ref is resolved through the [versions] table, unlike the Maven
// parser's verbatim ${property}: both tables sit in this one file, so the
// lookup is unambiguous rather than a guess about some other file's contents.
//
// A catalog carries no identity of its own, and no kinds: which configuration
// a library ends up in is decided by the build script that consumes the alias,
// not here, so every entry is a plain dependency. [plugins] and [bundles] are
// not read — a bundle is a list of aliases rather than a dependency, and a
// plugin is not a library coordinate.
func parseGradleCatalog(rel string, data []byte) (Manifest, error) {
	var raw gradleCatalog
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemGradle, Root: isRoot(rel)}
	for _, value := range raw.Libraries {
		if dep, ok := gradleLibraryDep(value, raw.Versions); ok {
			m.Deps = append(m.Deps, dep)
		}
	}
	// Two aliases may name the same library at the same version; that is one
	// declaration, not two.
	m.Deps = dedupeDeps(m.Deps)
	sortDeps(m.Deps)
	return m, nil
}

// gradleLibraryDep coerces one [libraries] entry into a declaration. The
// entry is either the "group:artifact:version" shorthand string or a table
// spelling the module as `module` or as separate `group` and `name` keys.
func gradleLibraryDep(value any, versions map[string]any) (DeclaredDep, bool) {
	switch v := value.(type) {
	case string:
		return gradleCoordinateDep(v, KindDependencies)
	case map[string]any:
		group, _ := v["group"].(string)
		name, _ := v["name"].(string)
		if module, ok := v["module"].(string); ok {
			g, n, found := strings.Cut(module, ":")
			if !found {
				return DeclaredDep{}, false
			}
			group, name = g, n
		}
		if group == "" || name == "" {
			return DeclaredDep{}, false
		}
		return DeclaredDep{
			Name:  mavenCoord(group, name),
			Range: gradleVersionText(v["version"], versions),
			Kind:  KindDependencies,
		}, true
	}
	return DeclaredDep{}, false
}

// gradleCoordinateDep splits a "group:artifact:version" coordinate — the
// catalog's shorthand entry form and a build script's literal notation are the
// same spelling — tolerating the version being omitted.
func gradleCoordinateDep(coordinate string, kind Kind) (DeclaredDep, bool) {
	parts := strings.Split(coordinate, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return DeclaredDep{}, false
	}
	dep := DeclaredDep{Name: mavenCoord(parts[0], parts[1]), Kind: kind}
	if len(parts) == 3 {
		dep.Range = parts[2]
	}
	return dep, true
}

// gradleVersionText renders a catalog version value as its version string: a
// plain string, a `version.ref` pointing into [versions], or a rich version
// table. The recursive lookup passes no versions table of its own, so a
// catalog whose [versions] entry somehow holds another ref cannot loop.
func gradleVersionText(value any, versions map[string]any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if ref, ok := v["ref"].(string); ok {
			return gradleVersionText(versions[ref], nil)
		}
		// A rich version declares its constraints separately; the required
		// version is the one a consumer resolves to.
		for _, key := range []string{"require", "strictly", "prefer"} {
			if s, ok := v[key].(string); ok {
				return s
			}
		}
	}
	return ""
}
