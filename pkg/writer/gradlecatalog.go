package writer

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ErrConflictingEdits marks two edits that resolve onto one version literal
// while asking for different text: a shared [versions] entry cannot be two
// versions at once. Test with errors.Is.
var ErrConflictingEdits = errors.New("writer: edits disagree on one shared version")

// gradleCatalog is the subset of a Gradle version catalog the writer models,
// mirroring the scanner's shape so the two agree on what an entry means.
type gradleCatalog struct {
	Versions  map[string]any `toml:"versions"`
	Libraries map[string]any `toml:"libraries"`
}

// catalogSlot is where one library's version text physically lives.
type catalogSlot struct {
	// table is "versions" when the entry defers to a version.ref, and
	// "libraries" when it spells its version inline.
	table string
	// key is the [versions] key or the [libraries] alias.
	key string
	// shorthand marks the "group:artifact:version" string form, whose version
	// is a segment of a larger literal rather than the whole of it.
	shorthand bool
}

// rewriteGradleCatalog edits a Gradle version catalog by replacing only the
// bytes of the version literals being changed. Edits name Maven coordinates,
// the way the scanner reports them, so the writer resolves each coordinate to
// its [libraries] entry and from there to wherever the version text actually
// sits: inline in the entry, inside its "group:artifact:version" shorthand, or
// in the [versions] table behind a version.ref.
//
// A [versions] entry several libraries share fans out: changing one
// coordinate changes every library pinned to that ref. That follows from the
// file the author wrote rather than from anything decided here, and is the
// reason two edits landing on one shared entry with different text are refused
// outright instead of letting the last one win.
//
// A catalog declares no version of its own, so Rewrite's version argument has
// no target here.
func rewriteGradleCatalog(path string, edits []Edit) (Result, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return Result{}, err
	}
	var raw gradleCatalog
	if err := toml.Unmarshal(rep.bytes(), &raw); err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	slots := catalogSlots(&raw)

	// Group the edits by the literal they land on, so a shared ref is written
	// once and a disagreement over it is caught before anything is spliced.
	type target struct {
		slot  catalogSlot
		text  string
		edits []Edit
	}
	var (
		res    Result
		order  []string
		wanted = map[string]*target{}
	)
	for _, e := range edits {
		slot, ok := slots[e.Name]
		// A catalog declares no kinds: which configuration an alias ends up in
		// is the consuming build script's decision, so a kinded edit names
		// something this file cannot express.
		if e.Kind != "" || !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		id := slot.table + "\x00" + slot.key
		t, seen := wanted[id]
		if !seen {
			wanted[id] = &target{slot: slot, text: e.Range, edits: []Edit{e}}
			order = append(order, id)
			continue
		}
		if t.text != e.Range {
			return res, fmt.Errorf("%s: %w: %q and %q both resolve to [%s] %s",
				path, ErrConflictingEdits, t.edits[0].Name, e.Name, slot.table, slot.key)
		}
		t.edits = append(t.edits, e)
	}

	lines := rep.lines()
	index := buildTOMLIndex(lines)
	changed := false
	for _, id := range order {
		t := wanted[id]
		idx, start, end, ok := catalogVersionSpan(index, lines, t.slot)
		if !ok {
			// The library is in the file; its version is not a literal this
			// writer can reach.
			res.Skipped = append(res.Skipped, t.edits...)
			continue
		}
		if lines[idx][start:end] == t.text {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, t.edits...)
		lines[idx] = lines[idx][:start] + t.text + lines[idx][end:]
		changed = true
	}
	if changed {
		rep.setLines(lines)
	}
	return res, rep.commit(verifyTOML)
}

// catalogSlots indexes every library's version location by its Maven
// coordinate. Aliases are visited in sorted order, so a coordinate two aliases
// declare resolves to the same one of them on every run.
func catalogSlots(raw *gradleCatalog) map[string]catalogSlot {
	aliases := make([]string, 0, len(raw.Libraries))
	for alias := range raw.Libraries {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	slots := make(map[string]catalogSlot, len(aliases))
	for _, alias := range aliases {
		coordinate, slot, ok := catalogSlotFor(alias, raw.Libraries[alias])
		if !ok {
			continue
		}
		if _, taken := slots[coordinate]; !taken {
			slots[coordinate] = slot
		}
	}
	return slots
}

// catalogSlotFor reads one [libraries] entry into its coordinate and the
// location of its version text. An entry declaring no version at all has
// nothing to write and is skipped.
func catalogSlotFor(alias string, value any) (coordinate string, slot catalogSlot, ok bool) {
	switch v := value.(type) {
	case string:
		parts := strings.Split(v, ":")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
			return "", catalogSlot{}, false
		}
		return parts[0] + ":" + parts[1], catalogSlot{table: "libraries", key: alias, shorthand: true}, true
	case map[string]any:
		group, _ := v["group"].(string)
		name, _ := v["name"].(string)
		if module, found := v["module"].(string); found {
			g, n, split := strings.Cut(module, ":")
			if !split {
				return "", catalogSlot{}, false
			}
			group, name = g, n
		}
		if group == "" || name == "" {
			return "", catalogSlot{}, false
		}
		coordinate = group + ":" + name
		switch version := v["version"].(type) {
		case string:
			return coordinate, catalogSlot{table: "libraries", key: alias}, true
		case map[string]any:
			if ref, isRef := version["ref"].(string); isRef {
				return coordinate, catalogSlot{table: "versions", key: ref}, true
			}
			return coordinate, catalogSlot{table: "libraries", key: alias}, true
		}
	}
	return "", catalogSlot{}, false
}

// catalogVersionSpan locates the version literal for one slot: the line it
// sits on and the byte range it occupies within that line.
func catalogVersionSpan(index tomlIndex, lines []string, slot catalogSlot) (idx, start, end int, ok bool) {
	idx, afterEq, ok := index.entry(slot.table, slot.key)
	if !ok {
		return 0, 0, 0, false
	}
	body := stripTOMLComment(lines[idx])

	i := afterEq
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	if i < len(body) && body[i] == '{' {
		// A rich version spells its constraints as separate keys; the required
		// one is what a consumer resolves to.
		keys := []string{"version"}
		if slot.table == "versions" {
			keys = []string{"require", "strictly", "prefer"}
		}
		for _, key := range keys {
			if start, end, ok := tomlInlineValueSpan(body, i, key); ok {
				return idx, start, end, true
			}
		}
		return 0, 0, 0, false
	}

	start, end, ok = tomlQuotedSpan(body, afterEq)
	if !ok {
		return 0, 0, 0, false
	}
	if slot.shorthand {
		// Only the third segment of "group:artifact:version" is the version.
		value := body[start:end]
		group := strings.IndexByte(value, ':')
		if group < 0 {
			return 0, 0, 0, false
		}
		artifact := strings.IndexByte(value[group+1:], ':')
		if artifact < 0 {
			return 0, 0, 0, false
		}
		start += group + 1 + artifact + 1
	}
	return idx, start, end, true
}
