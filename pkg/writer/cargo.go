package writer

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/yohimik/dispat/pkg/manifest"
)

// cargoManifest is the subset of a Cargo.toml the writer models, mirroring the
// scanner's shape so the two agree on what an entry means. The values decode
// as `any` because a dependency is either a version string or an inline table,
// and the package version may itself be `{ workspace = true }`.
type cargoManifest struct {
	Package struct {
		Version any `toml:"version"`
	} `toml:"package"`
	Dependencies      map[string]any `toml:"dependencies"`
	DevDependencies   map[string]any `toml:"dev-dependencies"`
	BuildDependencies map[string]any `toml:"build-dependencies"`
}

// cargoTables pairs each dependency table with the kind the scanner reports
// for it. build-dependencies count as plain dependencies for the same reason
// there: a build dependency's change still forces the consumer to rebuild.
var cargoTables = []struct {
	table string
	kind  manifest.Kind
}{
	{"dependencies", manifest.KindDependencies},
	{"dev-dependencies", manifest.KindDevDependencies},
	{"build-dependencies", manifest.KindDependencies},
}

// rewriteCargo edits a Cargo.toml by replacing only the bytes of the version
// literals being changed: the package's own `version`, and each dependency's
// version wherever it is spelled — as the whole value (`serde = "1.0"`) or as
// the `version` key of an inline table.
//
// A renamed dependency is keyed by its alias but declares its real name in the
// `package` key, and the scanner reports the real name, so the writer resolves
// an edit back through that rename. A dependency that inherits from the
// workspace (`serde = { workspace = true }`) or carries no version at all has
// nothing to replace and is reported missing; adding a version to it would
// override the workspace on purpose, which is dependency management rather
// than version syncing.
func rewriteCargo(path, version string, edits []Edit) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var raw cargoManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	// Resolve each edit to the table and key its version literal sits under.
	type slot struct{ table, key string }
	slots := make(map[string]slot, len(edits))
	// declared holds every entry, writable or not, so an edit naming a
	// workspace-inherited or version-less dependency is reported as skipped
	// rather than missing.
	declared := make(map[string]bool, len(edits))
	for _, t := range cargoTables {
		keys := make([]string, 0, len(cargoTableOf(&raw, t.table)))
		for key := range cargoTableOf(&raw, t.table) {
			keys = append(keys, key)
		}
		sort.Strings(keys) // a name two keys declare resolves the same way each run
		for _, key := range keys {
			name, writable := cargoDependencyName(key, cargoTableOf(&raw, t.table)[key])
			if name == "" {
				continue
			}
			id := string(t.kind) + "\x00" + name
			declared[id] = true
			if !writable {
				continue
			}
			if _, taken := slots[id]; !taken {
				slots[id] = slot{table: t.table, key: key}
			}
		}
	}

	var (
		res     Result
		lines   = strings.Split(string(data), "\n")
		changed bool
	)
	for _, e := range edits {
		kind := e.Kind
		if kind == "dependencies" {
			kind = manifest.KindDependencies
		}
		id := string(kind) + "\x00" + e.Name
		s, ok := slots[id]
		if !ok {
			if declared[id] {
				res.Skipped = append(res.Skipped, e)
			} else {
				res.Missing = append(res.Missing, e)
			}
			continue
		}
		idx, start, end, ok := cargoVersionSpan(lines, s.table, s.key)
		if !ok {
			res.Skipped = append(res.Skipped, e)
			continue
		}
		if lines[idx][start:end] == e.Range {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, e)
		lines[idx] = lines[idx][:start] + e.Range + lines[idx][end:]
		changed = true
	}

	if version != "" {
		if _, inherited := raw.Package.Version.(map[string]any); !inherited {
			if idx, start, end, ok := cargoVersionSpan(lines, "package", "version"); ok && lines[idx][start:end] != version {
				res.VersionWritten = true
				lines[idx] = lines[idx][:start] + version + lines[idx][end:]
				changed = true
			}
		}
	}
	if !changed {
		return res, nil
	}

	// The splice is span-precise, but a manifest is user data: never write
	// bytes back without proving they still parse.
	out := []byte(strings.Join(lines, "\n"))
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return res, fmt.Errorf("%s: internal error: rewrite produced invalid TOML: %w", path, err)
	}
	return res, atomicWrite(path, out)
}

// cargoTableOf selects one of the decoded dependency tables by name.
func cargoTableOf(raw *cargoManifest, table string) map[string]any {
	switch table {
	case "dev-dependencies":
		return raw.DevDependencies
	case "build-dependencies":
		return raw.BuildDependencies
	}
	return raw.Dependencies
}

// cargoDependencyName reports the name the scanner would give one entry — the
// `package` key of a renamed dependency, otherwise the table key — and whether
// it declares a version literal at all.
func cargoDependencyName(key string, value any) (name string, writable bool) {
	switch v := value.(type) {
	case string:
		return key, true
	case map[string]any:
		if pkg, ok := v["package"].(string); ok && pkg != "" {
			key = pkg
		}
		_, versioned := v["version"].(string)
		return key, versioned
	}
	return "", false
}

// cargoVersionSpan locates the version literal for one table entry: the whole
// value when it is a plain string, or the inline table's `version` key.
func cargoVersionSpan(lines []string, table, key string) (idx, start, end int, ok bool) {
	idx, afterEq, ok := catalogEntryLine(lines, table, key)
	if !ok {
		return 0, 0, 0, false
	}
	body := stripTOMLComment(lines[idx])
	i := afterEq
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	if i < len(body) && body[i] == '{' {
		start, end, ok = tomlInlineValueSpan(body, i, "version")
		return idx, start, end, ok
	}
	start, end, ok = tomlQuotedSpan(body, afterEq)
	return idx, start, end, ok
}
