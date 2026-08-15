package writer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// rewriteNpm edits a package.json by replacing only the bytes of the version
// strings being changed: the file is tokenised once to find each target
// value's exact span, then the spans are spliced back to front. Everything
// else (indentation, key order, trailing newline) is untouched bytes.
func rewriteNpm(path, version string, edits []Edit) (Result, error) {
	// A package.json names its dependency objects after the kinds themselves,
	// so the field an edit targets is just the kind spelled out.
	return rewriteJSON(path, version, edits, func(e Edit) string { return e.Kind.String() })
}

// rewriteJSON is the byte-precise JSON splice shared by package.json and
// composer.json. The two formats differ only in what their dependency objects
// are called, which fieldOf resolves; everything else (the single tokenising
// pass, the back-to-front splice, the re-validation) is identical.
func rewriteJSON(path, version string, edits []Edit, fieldOf func(Edit) string) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	spans, versionSpan, err := npmSpans(sp.bytes(), edits, fieldOf)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}

	var res Result
	for i, e := range edits {
		s, ok := spans[i]
		if !ok {
			res.Missing = append(res.Missing, e)
			continue
		}
		if string(current(sp.bytes(), s)) == e.Range {
			continue // already the wanted text: no change, not missing
		}
		res.Applied = append(res.Applied, e)
		sp.replace(s, quote(e.Range))
	}
	if version != "" && versionSpan != nil {
		if string(current(sp.bytes(), *versionSpan)) != version {
			res.VersionWritten = true
			sp.replace(*versionSpan, quote(version))
		}
	}
	return res, sp.commit(verifyJSON)
}

// verifyJSON is the JSON formats' proof that a rewrite still parses.
func verifyJSON(out []byte) error {
	if !json.Valid(out) {
		return errors.New("rewrite produced invalid JSON")
	}
	return nil
}

// current is the decoded string a span holds.
func current(data []byte, s span) []byte {
	var out string
	if json.Unmarshal(data[s.start:s.end], &out) == nil {
		return []byte(out)
	}
	return data[s.start:s.end]
}

// quote renders a Go string as a JSON string literal.
func quote(s string) []byte {
	out, _ := json.Marshal(s) // a string cannot fail to marshal
	return out
}

// npmSpans locates, in one pass over the token stream, the value span of the
// manifest's own top-level "version" and of each edit's dependency entry
// inside its field object. fieldOf names the object an edit targets. Returned
// spans are keyed by edit index.
func npmSpans(data []byte, edits []Edit, fieldOf func(Edit) string) (map[int]span, *span, error) {
	// Which top-level field objects hold edits, and which names inside them.
	type target struct{ field, name string }
	wanted := make(map[target]int, len(edits))
	fields := make(map[string]bool, len(edits))
	for i, e := range edits {
		f := fieldOf(e)
		if f == "" {
			continue // a kind this format has no object for
		}
		wanted[target{f, e.Name}] = i
		fields[f] = true
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if tok != json.Delim('{') {
		return nil, nil, fmt.Errorf("top level is not an object")
	}

	spans := make(map[int]span)
	var versionSpan *span
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, _ := keyTok.(string)
		afterKey := dec.InputOffset()
		switch {
		case key == "version":
			s, err := scalarSpan(data, dec, afterKey)
			if err != nil {
				return nil, nil, err
			}
			versionSpan = &s
		case fields[key]:
			// Descend into the field object and record the wanted entries.
			open, err := dec.Token()
			if err != nil {
				return nil, nil, err
			}
			if open != json.Delim('{') {
				return nil, nil, fmt.Errorf("%q is not an object", key)
			}
			for dec.More() {
				nameTok, err := dec.Token()
				if err != nil {
					return nil, nil, err
				}
				name, _ := nameTok.(string)
				afterName := dec.InputOffset()
				s, err := scalarSpan(data, dec, afterName)
				if err != nil {
					return nil, nil, err
				}
				if i, ok := wanted[target{key, name}]; ok {
					spans[i] = s
				}
			}
			if _, err := dec.Token(); err != nil { // closing '}'
				return nil, nil, err
			}
		default:
			if err := skipValue(dec); err != nil {
				return nil, nil, err
			}
		}
	}
	return spans, versionSpan, nil
}

// scalarSpan consumes the value after a key token and returns its byte span:
// from the first non-space byte after the colon to the decoder's offset once
// the value is read. Only scalars qualify: a composite value is an error where
// a version string belongs.
func scalarSpan(data []byte, dec *json.Decoder, afterKey int64) (span, error) {
	tok, err := dec.Token()
	if err != nil {
		return span{}, err
	}
	if d, ok := tok.(json.Delim); ok {
		return span{}, fmt.Errorf("expected a string value, found %q", d)
	}
	start := afterKey
	for start < int64(len(data)) && (data[start] == ':' || data[start] == ' ' ||
		data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
		start++
	}
	return span{start, dec.InputOffset()}, nil
}

// skipValue consumes one value, balancing nested delimiters.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}
