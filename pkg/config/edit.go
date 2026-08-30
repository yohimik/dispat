package config

// Writing one key back, without disturbing the rest of the file.
//
// A configuration file is written by hand, and a program that rewrites one
// owes it every byte it did not mean to change: the key order, the blank
// lines, the comments. JSON is spliced, so everything outside the replaced
// span survives exactly; YAML goes through a node tree that carries its
// comments, so it keeps them but may reflow; TOML has no such round trip here
// and is refused, for a caller to render a paste-ready snippet instead.
//
// A `$ref` crossed on the way to a key moves the write into the file that
// holds it, which is what makes a configuration split across files editable at
// all.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	yaml "gopkg.in/yaml.v3"
)

// BackupSuffix is appended to the config file name for the pre-edit copy.
const BackupSuffix = ".backup"

// Edit is one key of a config file and the value to write there.
type Edit struct {
	// KeyPath locates the key: ["dependencies"] for a top-level one,
	// ["packages", <key>, "dependencies"] for a nested one. Only a top-level
	// key may be created; a nested path must already exist.
	KeyPath []string
	// Value is rendered in the file's own format.
	Value any
}

// ApplyEdits applies every edit to one config file in a single pass: the file
// is read once, each value spliced onto the result of the previous splice, one
// backup written and one atomic rename performed. Two keys of the same file
// must go through one call rather than two — a second call would read the
// already-edited file and save that as the backup, so the pre-edit copy the
// user reaches for would be gone.
//
// JSON keeps every byte outside the spliced spans, YAML keeps its comments but
// is re-encoded, TOML returns ErrTOMLEdit. An edit set that changes nothing
// writes nothing.
//
// An edit with no key path replaces the file's whole document, which is what a
// key whose value is a `$ref` resolves to: the referenced file holds nothing
// but that value, so there is no span to preserve around it.
func ApplyEdits(ctx context.Context, path string, edits []Edit) error {
	p, err := PrepareEdits(ctx, path, edits)
	if err != nil {
		return err
	}
	return p.Commit()
}

// PreparedEdit is one file's fully rendered replacement, ready to commit.
// Preparing every file before committing any is what keeps a multi-file edit
// from stopping halfway through: a render failure in the last file must
// surface before the first file is rewritten.
//
// The buffers it holds are the file's whole content twice over. It is meant to
// be committed and dropped, not kept.
type PreparedEdit struct {
	Path string
	data []byte      // the pre-edit bytes, saved as the backup
	out  []byte      // the rendered replacement
	mode os.FileMode // the file's own permissions, kept across the rewrite
	noop bool        // the edit set changes nothing; Commit writes nothing
}

// PrepareEdits renders every edit against the file's current bytes without
// writing anything — the validating half of ApplyEdits.
func PrepareEdits(ctx context.Context, path string, edits []Edit) (*PreparedEdit, error) {
	p := &PreparedEdit{Path: path, noop: true}
	if len(edits) == 0 {
		return p, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(filepath.Ext(path))
	out := data
	for _, e := range edits {
		switch {
		case format == ".toml":
			return nil, ErrTOMLEdit
		case len(e.KeyPath) == 0:
			out, err = renderDocument(format, out, e.Value)
		case format == ".json":
			out, err = replaceValueJSON(out, e.KeyPath, e.Value)
		case format == ".yaml" || format == ".yml":
			out, err = replaceValueYAML(out, e.KeyPath, e.Value)
		default:
			return nil, fmt.Errorf("%s: unknown config format", path)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if bytes.Equal(out, data) {
		return p, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &PreparedEdit{Path: path, data: data, out: out, mode: info.Mode().Perm()}, nil
}

// Commit writes the prepared replacement: the backup first, then the file.
// Both writes are atomic — the backup exists for the moment something goes
// wrong, which is exactly when a truncated half-written copy would be found
// instead — and the backup carries the config's own permissions: a 0600 config
// must not leak through a world-readable copy.
func (p *PreparedEdit) Commit() error {
	if p.noop {
		return nil
	}
	if err := writeFileAtomic(p.Path+BackupSuffix, p.data, p.mode); err != nil {
		return fmt.Errorf("saving backup: %w", err)
	}
	return writeFileAtomic(p.Path, p.out, p.mode)
}

// RenderKeyTOML renders one value nested under its key path — the paste-ready
// fallback for the TOML configs the writers refuse.
func RenderKeyTOML(keyPath []string, value any) (string, error) {
	wrapped := value
	for i := len(keyPath) - 1; i >= 0; i-- {
		wrapped = map[string]any{keyPath[i]: wrapped}
	}
	out, err := toml.Marshal(wrapped)
	return string(out), err
}

// ResolveEdit reports which file holds a key, and under which key path there.
//
// A `$ref` crossed on the way down moves the edit into the file it names, with
// the rest of the path: a configuration split across files is written where
// each key is written, and the reference itself survives the write. A key
// written beside a reference keeps the edit in the file that wrote it, which
// is the same rule the loader reads those keys by.
//
// The returned key path is empty when the key *is* the reference: the whole
// referenced document is the value, so that file is rewritten rather than
// spliced. A key no file holds comes back unchanged, for the writers to create
// or refuse as they already do.
func (l *Loader) ResolveEdit(ctx context.Context, path string, keyPath []string) (string, []string, error) {
	return l.loader().resolveEdit(path, keyPath, 0)
}

// resolveEdit is ResolveEdit with the number of references already followed,
// which is what bounds it: the loader refuses a cycle long before an edit is
// collected, so this only has to stop rather than explain.
func (l *Loader) resolveEdit(path string, keyPath []string, followed int) (string, []string, error) {
	if followed > l.opts.MaxRefDepth {
		return "", nil, fmt.Errorf("$ref nesting is more than %d files deep at %s", l.opts.MaxRefDepth, path)
	}
	doc, err := l.decodeFile(path)
	if err != nil {
		return "", nil, &FileError{Path: path, Err: err}
	}
	node, ok := doc.(map[string]any)
	if !ok {
		return path, keyPath, nil
	}
	for depth, key := range keyPath {
		_, value, found := LookupFold(node, key)
		if !found {
			return path, keyPath, nil
		}
		child, ok := value.(map[string]any)
		if !ok {
			return path, keyPath, nil
		}
		targets, isRef, err := l.refTargets(child)
		if err != nil {
			return "", nil, &KeyError{File: path,
				Key: strings.Join(keyPath[:depth+1], DefaultKeyDelim), Err: err}
		}
		if !isRef {
			node = child
			continue
		}
		rest := keyPath[depth+1:]
		if len(child) > 1 {
			// The keys beside the reference are the nearer layer, so one of
			// them holding what comes next settles it here.
			if len(rest) == 0 {
				return "", nil, &KeyError{File: path,
					Key: strings.Join(keyPath[:depth+1], DefaultKeyDelim),
					Err: fmt.Errorf("%w; write %s beside the %s, or leave the %s as the whole value",
						ErrRefEdit, keyPath[len(keyPath)-1], l.opts.RefKey, l.opts.RefKey)}
			}
			if _, beside := FoldKey(child, rest[0]); beside {
				return path, keyPath, nil
			}
		}
		if len(targets) > 1 {
			// The value is merged from every file the reference names, so no
			// one of them can be handed the write.
			return "", nil, &KeyError{File: path,
				Key: strings.Join(keyPath[:depth+1], DefaultKeyDelim),
				Err: fmt.Errorf("%w; write %s beside the %s, or point the %s at a single file",
					ErrMultiRefEdit, keyPath[len(keyPath)-1], l.opts.RefKey, l.opts.RefKey)}
		}
		return l.resolveEdit(refPath(targets[0], path), rest, followed+1)
	}
	return path, keyPath, nil
}

// StringMapAt reads the string map at keyPath of the config file at path,
// exactly as the file spells it. It exists because a loaded configuration is a
// merged, validated view rather than the file: a write has to start from what
// the file holds, key for key, so the entries it already carries come back
// untouched. A key the file does not carry is no error and returns a nil map;
// a value that is not a map of scalars is.
//
// Unlike the writers this reads TOML too: a TOML config still needs its
// current entries to render the paste-ready block.
func (l *Loader) StringMapAt(ctx context.Context, path string, keyPath []string) (map[string]string, error) {
	l = l.loader()
	t, err := l.ReadTree(ctx, path)
	if err != nil {
		return nil, err
	}
	node := t.Root
	for depth, key := range keyPath {
		_, value, ok := LookupFold(node, key)
		if !ok {
			return nil, nil
		}
		child, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %s is not an object", path,
				strings.Join(keyPath[:depth+1], DefaultKeyDelim))
		}
		node = child
	}
	out := make(map[string]string, len(node))
	for key, value := range node {
		// A value read through this is written as a quoted string in each
		// format, and one the loader would reject — an unquoted YAML 1.0,
		// which decodes as a number — never reaches a write, because the
		// config carrying it does not load.
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s.%s is not a string", path,
				strings.Join(keyPath, DefaultKeyDelim), key)
		}
		out[key] = text
	}
	return out, nil
}

// renderDocument rewrites a file whose whole content is one value.
func renderDocument(format string, data []byte, value any) ([]byte, error) {
	switch format {
	case ".json":
		indent := detectJSONIndent(data)
		rendered, err := json.MarshalIndent(value, "", indent)
		if err != nil {
			return nil, err
		}
		if nullRendering(rendered) {
			rendered = []byte("[]")
		}
		return append(rendered, '\n'), nil
	case ".yaml", ".yml":
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(value); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown config format")
	}
}

// nullRendering reports that a value marshalled to JSON's "null", which is
// what a nil slice does. An emptied list must read as "[]": a key holding null
// is a config that no longer says "there is nothing here", it says nothing at
// all.
//
// A value that renders itself never produces null, so this leaves it alone.
func nullRendering(rendered []byte) bool {
	return bytes.Equal(bytes.TrimSpace(rendered), []byte("null"))
}

// replaceValueJSON splices a re-rendered value over the existing one at
// keyPath (or appends a top-level key when absent), byte-precise outside the
// spliced span.
func replaceValueJSON(data []byte, keyPath []string, value any) ([]byte, error) {
	indent := detectJSONIndent(data)
	rendered, err := json.MarshalIndent(value, strings.Repeat(indent, len(keyPath)), indent)
	if err != nil {
		return nil, err
	}
	if nullRendering(rendered) {
		rendered = []byte("[]")
	}

	start, end, closing, err := jsonKeySpan(data, keyPath)
	if err != nil {
		return nil, err
	}
	if start >= 0 {
		return splice(data, start, end, rendered), nil
	}
	// No such key: append it as the object's last member, before the closing
	// brace. Only a top-level key may be created — nested paths are only ever
	// edited, and their absence would be a caller bug.
	if len(keyPath) != 1 {
		return nil, fmt.Errorf("key %s not found", strings.Join(keyPath, DefaultKeyDelim))
	}
	head := bytes.TrimRight(data[:closing], " \t\n\r")
	sep := ",\n" + indent
	if bytes.HasSuffix(head, []byte("{")) {
		sep = "\n" + indent
	}
	entry := sep + `"` + keyPath[0] + `": ` + string(rendered) + "\n"
	return splice(data, int64(len(head)), closing, []byte(entry)), nil
}

// splice returns data with [start, end) replaced.
func splice(data []byte, start, end int64, repl []byte) []byte {
	out := make([]byte, 0, int64(len(data))-(end-start)+int64(len(repl)))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	return append(out, data[end:]...)
}

// jsonKeySpan tokenises the file and returns the byte span of the value at
// keyPath (keys matched case-insensitively, descending through nested
// objects), or start = -1 with the offset of the top-level object's closing
// brace when a single absent top-level key is asked for. An absent ancestor is
// an error.
func jsonKeySpan(data []byte, keyPath []string) (start, end, closing int64, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, 0, err
	}
	if tok != json.Delim('{') {
		return 0, 0, 0, errors.New("top level is not an object")
	}
	path := keyPath
	for {
		descended := false
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return 0, 0, 0, err
			}
			key, _ := keyTok.(string)
			afterKey := dec.InputOffset()
			if !strings.EqualFold(key, path[0]) {
				if err := skipJSONValue(dec); err != nil {
					return 0, 0, 0, err
				}
				continue
			}
			if len(path) > 1 {
				tok, err := dec.Token()
				if err != nil {
					return 0, 0, 0, err
				}
				if tok != json.Delim('{') {
					return 0, 0, 0, fmt.Errorf("key %q is not an object", path[0])
				}
				path = path[1:]
				descended = true
				break
			}
			if err := skipJSONValue(dec); err != nil {
				return 0, 0, 0, err
			}
			start := afterKey
			for start < int64(len(data)) && (data[start] == ':' || data[start] == ' ' ||
				data[start] == '\t' || data[start] == '\n' || data[start] == '\r') {
				start++
			}
			return start, dec.InputOffset(), 0, nil
		}
		if descended {
			continue
		}
		// The current object holds no matching key.
		if len(keyPath) > 1 {
			return 0, 0, 0, fmt.Errorf("key %s not found", strings.Join(keyPath, DefaultKeyDelim))
		}
		// The next token is the top-level object's closing brace.
		before := dec.InputOffset()
		if _, err := dec.Token(); err != nil {
			return 0, 0, 0, err
		}
		closing = dec.InputOffset() - 1
		if closing < before {
			closing = before
		}
		return -1, -1, closing, nil
	}
}

// skipJSONValue consumes one value, balancing nested delimiters.
func skipJSONValue(dec *json.Decoder) error {
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

// detectJSONIndent reads the file's indent unit from its first indented line;
// two spaces when the file gives no hint.
func detectJSONIndent(data []byte) string {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || len(trimmed) == len(line) {
			continue
		}
		return string(line[:len(line)-len(trimmed)])
	}
	return "  "
}

// replaceValueYAML swaps the document's value node at keyPath (appending an
// absent top-level key) and re-encodes; yaml.v3 nodes carry their comments, so
// the rest of the file survives the round trip.
func replaceValueYAML(data []byte, keyPath []string, value any) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("top level is not a mapping")
	}
	var rendered yaml.Node
	if err := rendered.Encode(value); err != nil {
		return nil, err
	}
	node := doc.Content[0]
	for depth, key := range keyPath {
		last := depth == len(keyPath)-1
		found := false
		for i := 0; i+1 < len(node.Content); i += 2 {
			if !strings.EqualFold(node.Content[i].Value, key) {
				continue
			}
			if last {
				node.Content[i+1] = &rendered
			} else {
				node = node.Content[i+1]
				if node.Kind != yaml.MappingNode {
					return nil, fmt.Errorf("key %q is not a mapping", key)
				}
			}
			found = true
			break
		}
		if !found {
			// Only a top-level key may be created; see replaceValueJSON.
			if len(keyPath) != 1 {
				return nil, fmt.Errorf("key %s not found", strings.Join(keyPath, DefaultKeyDelim))
			}
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
			node.Content = append(node.Content, keyNode, &rendered)
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFileAtomic replaces path via a same-directory temp file, fsync and
// rename. The temp file lands beside the target so the rename never crosses a
// filesystem, and it is removed on every failure.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
