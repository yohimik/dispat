package writer

import (
	"fmt"
	"slices"
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
// version wherever it is spelled, as the whole value (`serde = "1.0"`) or as
// the `version` key of an inline table.
//
// A renamed dependency is keyed by its alias but declares its real name in the
// `package` key, and the scanner reports the real name, so the writer resolves
// an edit back through that rename. A dependency that inherits from the
// workspace (`serde = { workspace = true }`) or carries no version at all has
// nothing to replace and is reported skipped; adding a version to it would
// override the workspace on purpose, which is dependency management rather
// than version syncing.
func rewriteCargo(path, version string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	var raw cargoManifest
	if err := toml.Unmarshal(sp.bytes(), &raw); err != nil {
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
		lines   = sp.lines()
		index   = buildTOMLIndex(lines)
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
		idx, start, end, ok := cargoVersionSpan(index, lines, s.table, s.key)
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
			if idx, start, end, ok := cargoVersionSpan(index, lines, "package", "version"); ok && lines[idx][start:end] != version {
				res.VersionWritten = true
				lines[idx] = lines[idx][:start] + version + lines[idx][end:]
				changed = true
			}
		}
	}
	if changed {
		sp.setLines(lines)
	}
	return res, sp.commit(verifyTOML)
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

// cargoDependencyName reports the name the scanner would give one entry (the
// `package` key of a renamed dependency, otherwise the table key) and whether
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
//
// Cargo spells a detailed dependency two ways. The inline one keeps it on the
// entry's own line, and the sub-table one gives it a header of its own:
//
//	[dependencies]
//	serde = { version = "1.0", features = ["derive"] }
//
//	[dependencies.serde]
//	version = "1.0"
//	features = ["derive"]
//
// Both are ordinary in real crates, so both are searched: the entry first,
// then a `version` key under `[<table>.<key>]`.
func cargoVersionSpan(index tomlIndex, lines []string, table, key string) (idx, start, end int, ok bool) {
	// A dependency literally named "core.version" can coexist with a dotted
	// core.version assignment. The flat index cannot distinguish their text,
	// so resolve names containing a dot from the authored key segments.
	if strings.Contains(key, ".") {
		if idx, start, end, ok = findCargoKeyVersionSpan(lines, []string{table}, []string{key}); ok {
			return idx, start, end, true
		}
	} else {
		idx, afterEq, found := index.entry(table, key)
		if found {
			var lineState tomlLineState
			body := lineState.maskStringContents(lines[idx])
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
		// An ordinary unquoted subtable has an exact entry in the line
		// index. Quoted literal dots cannot collide with this lookup: the
		// index retains quote marks from their table headers.
		if idx, start, end, ok = catalogEntryValueSpan(index, lines, table+"."+key, "version"); ok {
			return idx, start, end, true
		}
		// A quoted table header is decoded by go-toml but kept verbatim by
		// the ordinary line index, so locate its authored segments instead.
		if idx, start, end, ok = findCargoKeyVersionSpan(lines, []string{table}, []string{key}); ok {
			return idx, start, end, true
		}
	}
	// TOML also permits dotted keys under the parent table:
	// [dependencies] followed by core.version = "1.0". The decoder
	// presents this as the same nested map as [dependencies.core], so
	// locate the literal where the author actually wrote it.
	if idx, start, end, ok = findCargoKeyVersionSpan(lines, []string{table}, []string{key, "version"}); ok {
		return idx, start, end, true
	}
	return findCargoKeyVersionSpan(lines, []string{table, key}, []string{"version"})
}

// findCargoKeyVersionSpan locates a scalar by its parsed key segments. A literal
// quoted key such as "core.version" has one segment; core.version has two.
func findCargoKeyVersionSpan(lines []string, tableParts, keyParts []string) (idx, start, end int, ok bool) {
	var current []string
	var state tomlLineState
	for li, raw := range lines {
		body := state.maskStringContents(raw)
		if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "[") {
			if strings.HasSuffix(trimmed, "]") {
				current, _ = parseTOMLDottedKeyParts(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			} else {
				current = nil
			}
			continue
		}
		if !slices.Equal(current, tableParts) {
			continue
		}
		_, afterEq, entry := tomlKeyValue(body)
		if !entry {
			continue
		}
		parts, valid := parseTOMLDottedKeyParts(body[:afterEq-1])
		if !valid || !slices.Equal(parts, keyParts) {
			continue
		}
		value := afterEq
		for value < len(body) && (body[value] == ' ' || body[value] == '\t') {
			value++
		}
		if value < len(body) && body[value] == '{' {
			start, end, ok = tomlInlineValueSpan(body, value, "version")
		} else {
			start, end, ok = tomlQuotedSpan(body, afterEq)
		}
		return li, start, end, ok
	}
	return 0, 0, 0, false
}

// cargoPatchTable is where Cargo keeps redirects for crates.io dependencies.
// A patch for another registry or a git source lives under its own table, and
// this writer does not go looking for those: the workspace case is a local
// path standing in for the published crate.
const cargoPatchTable = "patch.crates-io"

// linkCargo points crates at local folders through [patch.crates-io], which
// is Cargo's equivalent of a go.mod replace. The older [replace] table does
// the same job and is deprecated, so a redirect is always written as a patch.
func linkCargo(path string, links []Link) (LinkResult, error) {
	return tomlLink(path, cargoPatchTable, links)
}
