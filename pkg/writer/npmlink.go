package writer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// npm, Yarn and pnpm all keep a map of overrides keyed by package name, and
// all three accept a "file:" specifier in it, which is how a package.json
// points at a folder:
//
//	"overrides": { "@acme/core": "file:../core" }
//
// The map has a different name in each, so the field is chosen by looking at
// the file rather than guessing. An existing map wins, then the packageManager
// field, then npm's "overrides" as the default.
//
// One caveat belongs to npm alone: it refuses an override for a package the
// manifest depends on directly unless the two specs match exactly. Overrides
// there are aimed at transitive dependencies. Yarn and pnpm have no such rule.

// npmOverrideField picks the map to write, as a path of keys from the document
// root.
func npmOverrideField(doc map[string]any) []string {
	if _, ok := doc["resolutions"]; ok {
		return []string{"resolutions"}
	}
	if pnpm, ok := doc["pnpm"].(map[string]any); ok {
		if _, ok := pnpm["overrides"]; ok {
			return []string{"pnpm", "overrides"}
		}
	}
	if _, ok := doc["overrides"]; ok {
		return []string{"overrides"}
	}
	if pm, ok := doc["packageManager"].(string); ok {
		switch {
		case strings.HasPrefix(pm, "pnpm"):
			return []string{"pnpm", "overrides"}
		case strings.HasPrefix(pm, "yarn"):
			return []string{"resolutions"}
		}
	}
	return []string{"overrides"}
}

// npmSpec renders a redirect the way all three managers read it.
func npmSpec(path string) string { return "file:" + path }

// linkNpm points packages at local folders through the override map. Each link
// is applied to freshly parsed bytes, because an insertion moves every offset
// after it and one manifest is small enough that re-reading it is cheaper than
// tracking the shift.
func linkNpm(path string, links []Link) (LinkResult, error) {
	rep, err := openReplacer(path)
	if err != nil {
		return LinkResult{}, err
	}
	data := rep.bytes()
	var res LinkResult
	for _, r := range links {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return res, fmt.Errorf("%s: %w", path, err)
		}
		field := npmOverrideField(doc)
		next, applied, err := npmApplyLink(data, field, r)
		if err != nil {
			return res, fmt.Errorf("%s: %w", path, err)
		}
		switch {
		case applied:
			res.Applied = append(res.Applied, r)
			data = next
		case r.Path == "":
			res.Missing = append(res.Missing, r)
		}
	}
	if len(res.Applied) > 0 {
		rep.setWhole(data)
	}
	return res, rep.commit(func(out []byte) error {
		return npmVerifyLinks(out, res.Applied)
	})
}

// npmApplyLink writes one redirect, reporting whether anything changed.
func npmApplyLink(data []byte, field []string, r Link) ([]byte, bool, error) {
	obj, found, err := jsonObjectAt(data, field...)
	if err != nil {
		return nil, false, err
	}
	if !found {
		if r.Path == "" {
			return data, false, nil // nothing to remove
		}
		return npmCreateField(data, field, r)
	}
	entry, at := obj.entry(r.Name)
	switch {
	case at < 0 && r.Path == "":
		return data, false, nil
	case at < 0:
		return jsonInsertEntry(data, obj, jsonEntryText(r.Name, npmSpec(r.Path))), true, nil
	case r.Path == "":
		out := jsonRemoveEntry(data, obj, at)
		// An emptied map is noise; take the field with it.
		if len(obj.entries) == 1 {
			out = npmDropField(out, field)
		}
		return out, true, nil
	default:
		want := string(quote(npmSpec(r.Path)))
		if string(data[entry.value.start:entry.value.end]) == want {
			return data, false, nil // already pointing there
		}
		out := append([]byte{}, data[:entry.value.start]...)
		out = append(out, want...)
		return append(out, data[entry.value.end:]...), true, nil
	}
}

// npmCreateField writes the override map itself, and the pnpm object around it
// when that is missing too.
func npmCreateField(data []byte, field []string, r Link) ([]byte, bool, error) {
	inner := jsonEntryText(r.Name, npmSpec(r.Path))
	// Walk as far down the chain as the file already goes.
	for depth := len(field); depth > 0; depth-- {
		obj, found, err := jsonObjectAt(data, field[:depth-1]...)
		if err != nil {
			return nil, false, err
		}
		if !found {
			continue
		}
		// Nest whatever keys the file is missing, from the inside out.
		nested := "{" + inner + "}"
		for i := len(field) - 1; i >= depth; i-- {
			nested = `{"` + field[i] + `": ` + nested + "}"
		}
		return jsonInsertEntry(data, obj, `"`+field[depth-1]+`": `+nested), true, nil
	}
	return nil, false, fmt.Errorf("no object to write %q into", strings.Join(field, "."))
}

// npmDropField removes an override map that has just been emptied, and the
// pnpm object with it when that empties too.
func npmDropField(data []byte, field []string) []byte {
	for depth := len(field); depth > 0; depth-- {
		parent, found, err := jsonObjectAt(data, field[:depth-1]...)
		if err != nil || !found {
			return data
		}
		_, at := parent.entry(field[depth-1])
		if at < 0 {
			return data
		}
		child, ok, err := jsonObjectAt(data, field[:depth]...)
		if err != nil || !ok || len(child.entries) > 0 {
			return data
		}
		data = jsonRemoveEntry(data, parent, at)
	}
	return data
}

// npmVerifyLinks re-reads the written bytes and checks every applied
// redirect reads back as the path it asked for. Insertion can change a
// document's structure, so proving it parses is not by itself enough.
func npmVerifyLinks(out []byte, applied []Link) error {
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return fmt.Errorf("rewrite produced invalid JSON: %w", err)
	}
	entries := map[string]any{}
	for _, field := range [][]string{{"overrides"}, {"resolutions"}, {"pnpm", "overrides"}} {
		cur := doc
		for i, key := range field {
			next, ok := cur[key].(map[string]any)
			if !ok {
				break
			}
			if i == len(field)-1 {
				for k, v := range next {
					entries[k] = v
				}
			}
			cur = next
		}
	}
	for _, r := range applied {
		value, declared := entries[r.Name]
		if r.Path == "" {
			if declared {
				return fmt.Errorf("rewrite left %s still overridden", r.Name)
			}
			continue
		}
		if got, _ := value.(string); got != npmSpec(r.Path) {
			return fmt.Errorf("rewrite left %s pointing at %q, want %q", r.Name, got, npmSpec(r.Path))
		}
	}
	return nil
}

// jsonEntry is one key/value pair inside an object, with the spans a splice or
// a removal needs.
type jsonEntry struct {
	key      string
	keyStart int64 // the opening quote of the key
	value    span  // the value's own bytes, quotes included for a string
}

// jsonObject is one object's shape: where it opens and closes, and what it
// holds, in the order the file holds it.
type jsonObject struct {
	openEnd int64 // just past '{'
	closeAt int64 // the '}' itself
	entries []jsonEntry
}

// entry finds one key, reporting its position in the object's order.
func (o jsonObject) entry(key string) (jsonEntry, int) {
	for i, e := range o.entries {
		if e.key == key {
			return e, i
		}
	}
	return jsonEntry{}, -1
}

// jsonObjectAt walks a path of keys from the document root and describes the
// object it lands on. An empty path describes the document itself.
func jsonObjectAt(data []byte, keys ...string) (jsonObject, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return jsonObject{}, false, err
	}
	if tok != json.Delim('{') {
		return jsonObject{}, false, fmt.Errorf("top level is not an object")
	}
	return jsonDescend(data, dec, keys)
}

// jsonDescend reads the object the decoder has just entered, either following
// the remaining keys or recording its entries.
func jsonDescend(data []byte, dec *json.Decoder, keys []string) (jsonObject, bool, error) {
	obj := jsonObject{openEnd: dec.InputOffset()}
	for dec.More() {
		prev := dec.InputOffset()
		keyTok, err := dec.Token()
		if err != nil {
			return jsonObject{}, false, err
		}
		key, _ := keyTok.(string)
		keyStart := prev + int64(bytes.IndexByte(data[prev:], '"'))
		afterKey := dec.InputOffset()

		if len(keys) > 0 && key == keys[0] {
			open, err := dec.Token()
			if err != nil {
				return jsonObject{}, false, err
			}
			if open != json.Delim('{') {
				return jsonObject{}, false, nil // present but not an object
			}
			return jsonDescend(data, dec, keys[1:])
		}
		value, err := jsonValueSpan(data, dec, afterKey)
		if err != nil {
			return jsonObject{}, false, err
		}
		if len(keys) == 0 {
			obj.entries = append(obj.entries, jsonEntry{key: key, keyStart: keyStart, value: value})
		}
	}
	prev := dec.InputOffset()
	if _, err := dec.Token(); err != nil { // the closing '}'
		return jsonObject{}, false, err
	}
	obj.closeAt = prev + int64(bytes.IndexByte(data[prev:], '}'))
	if len(keys) > 0 {
		return jsonObject{}, false, nil // the chain ran out before the key did
	}
	return obj, true, nil
}

// jsonValueSpan measures one value, composite or scalar, from the first byte
// after the colon to the decoder's position once the value is read.
func jsonValueSpan(data []byte, dec *json.Decoder, afterKey int64) (span, error) {
	start := afterKey
	for start < int64(len(data)) && (data[start] == ':' || data[start] == ' ' ||
		data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
		start++
	}
	tok, err := dec.Token()
	if err != nil {
		return span{}, err
	}
	if d, ok := tok.(json.Delim); ok && (d == '{' || d == '[') {
		for depth := 1; depth > 0; {
			t, err := dec.Token()
			if err != nil {
				return span{}, err
			}
			if dd, ok := t.(json.Delim); ok {
				if dd == '{' || dd == '[' {
					depth++
				} else {
					depth--
				}
			}
		}
	}
	return span{start: start, end: dec.InputOffset()}, nil
}

// jsonEntryText renders one entry, quoting the value as a string.
func jsonEntryText(key, value string) string {
	return string(quote(key)) + ": " + string(quote(value))
}

// jsonInsertEntry writes entryText into an object, after its last entry so the
// file keeps whatever order its author chose. The indentation is copied from
// the entry above, or from the closing brace when the object is empty.
func jsonInsertEntry(data []byte, obj jsonObject, entryText string) []byte {
	if len(obj.entries) == 0 {
		closeIndent := jsonIndentBefore(data, obj.closeAt)
		body := "\n" + closeIndent + jsonIndentStep(data) + entryText + "\n" + closeIndent
		out := append([]byte{}, data[:obj.openEnd]...)
		out = append(out, body...)
		return append(out, data[obj.closeAt:]...)
	}
	last := obj.entries[len(obj.entries)-1]
	indent := jsonIndentBefore(data, last.keyStart)
	out := append([]byte{}, data[:last.value.end]...)
	out = append(out, (",\n" + indent + entryText)...)
	return append(out, data[last.value.end:]...)
}

// jsonRemoveEntry deletes one entry and the comma that joined it to its
// neighbour, leaving an empty object behind when it was the only one.
func jsonRemoveEntry(data []byte, obj jsonObject, at int) []byte {
	var from, to int64
	switch {
	case len(obj.entries) == 1:
		from, to = obj.openEnd, obj.closeAt
	case at == 0:
		from, to = obj.entries[0].keyStart, obj.entries[1].keyStart
	default:
		from, to = obj.entries[at-1].value.end, obj.entries[at].value.end
	}
	out := append([]byte{}, data[:from]...)
	return append(out, data[to:]...)
}

// jsonIndentBefore reports the whitespace between the start of a line and the
// given offset, which is the indent of whatever sits there.
func jsonIndentBefore(data []byte, at int64) string {
	start := at
	for start > 0 && data[start-1] != '\n' {
		start--
	}
	indent := data[start:at]
	if len(bytes.TrimLeft(indent, " \t")) != 0 {
		return "" // something other than whitespace shares the line
	}
	return string(indent)
}

// jsonIndentStep guesses one level of indentation from the first indented line
// in the file, so an inserted entry matches what is already there.
func jsonIndentStep(data []byte) string {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || len(trimmed) == len(line) {
			continue
		}
		return string(line[:len(line)-len(trimmed)])
	}
	return "  "
}
