package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
)

// ErrTOMLEdit reports that the config is TOML, which dispat cannot rewrite
// format-preservingly; the caller renders a paste-ready snippet instead.
var ErrTOMLEdit = errors.New("a TOML config cannot be rewritten in place")

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

// ReplaceDependencies rewrites only the dependency list at keyPath of the
// config file at path — ["dependencies"] for the file's own top-level list,
// ["packages", <key>, "dependencies"] for a packages entry of the root
// config — leaving every other byte of a JSON config (formatting, key order,
// comments) untouched; a YAML config keeps its comments but is re-encoded,
// so unrelated formatting may reflow. The previous bytes are saved at path +
// BackupSuffix first, and the write itself is atomic (temp + rename). TOML
// returns ErrTOMLEdit.
//
// deps is the Dependencies type rather than a bare slice so that the value
// goes through that type's marshaller: the key is written as the object keyed
// by consumer, which is the only shape the loader reads back.
//
// The file is re-read here: deps must be the caller's complete intended
// list, and an edit made to the file by someone else between the caller's
// read and this call is overwritten for that one key (every other key keeps
// the concurrent edit).
func ReplaceDependencies(path string, keyPath []string, deps Dependencies) error {
	return ReplaceKeys(path, []Edit{{KeyPath: keyPath, Value: deps}})
}

// ReplaceStringList is ReplaceDependencies for the package-level dependency
// shape: a plain list of provider names.
func ReplaceStringList(path string, keyPath []string, items []string) error {
	return ReplaceKeys(path, []Edit{{KeyPath: keyPath, Value: items}})
}

// ReplaceKeys applies every edit to one config file in a single pass: the
// file is read once, each value spliced onto the result of the previous
// splice, one backup written and one atomic rename performed. Two keys of the
// same file must go through one call rather than two — a second call would
// read the already-edited file and save that as the backup, so the pre-edit
// copy the user reaches for would be gone.
//
// Everything ReplaceDependencies documents about formats holds here: JSON
// keeps every byte outside the spliced spans, YAML keeps its comments but is
// re-encoded, TOML returns ErrTOMLEdit. An edit set that changes nothing
// writes nothing.
//
// An edit with no key path replaces the file's whole document, which is what
// a key whose value is a `$ref` resolves to: the referenced file holds nothing
// but that value, so there is no span to preserve around it.
func ReplaceKeys(path string, edits []Edit) error {
	p, err := PrepareKeys(path, edits)
	if err != nil {
		return err
	}
	return p.Commit()
}

// PreparedEdit is one file's fully rendered replacement, ready to commit.
// Preparing every file before committing any is what keeps a multi-file edit
// from stopping halfway through: a render failure in the last file must
// surface before the first file is rewritten.
type PreparedEdit struct {
	Path string
	data []byte      // the pre-edit bytes, saved as the backup
	out  []byte      // the rendered replacement
	mode os.FileMode // the file's own permissions, kept across the rewrite
	noop bool        // the edit set changes nothing; Commit writes nothing
}

// PrepareKeys renders every edit against the file's current bytes without
// writing anything — the validating half of ReplaceKeys.
func PrepareKeys(path string, edits []Edit) (*PreparedEdit, error) {
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
// instead — and the backup carries the config's own permissions: a 0600
// config must not leak through a world-readable copy.
func (p *PreparedEdit) Commit() error {
	if p.noop {
		return nil
	}
	if err := fsx.WriteFileAtomic(p.Path+BackupSuffix, p.data, p.mode); err != nil {
		return fmt.Errorf("saving backup: %w", err)
	}
	return fsx.WriteFileAtomic(p.Path, p.out, p.mode)
}

// RenderDependenciesTOML renders the dependencies as a `dependencies` table
// keyed by consumer — the paste-ready fallback for TOML configs, in the same
// consumer-keyed form the JSON and YAML writers splice in.
//
// Every provider is written as a table, even one carrying nothing but its
// name. The other two formats shorten that to the bare name, but an array
// holding a name beside a table is not something a TOML encoder will write,
// and one uniform shape beats a rendering that fails on the configs that most
// need the fallback.
func RenderDependenciesTOML(deps Dependencies) (string, error) {
	grouped := deps.Grouped()
	rows := make(map[string][]map[string]any, len(grouped))
	for consumer, providers := range grouped {
		for _, p := range providers {
			// Lowercase keys, because DependencyConfig has no toml tags and
			// the file's keys are the mapstructure ones.
			row := map[string]any{"provider": p.Provider}
			if p.Kind != "" {
				row["kind"] = p.Kind
			}
			if p.Keep {
				row["keep"] = true
			}
			rows[consumer] = append(rows[consumer], row)
		}
	}
	out, err := toml.Marshal(map[string]any{"dependencies": rows})
	return string(out), err
}

// RenderKeyTOML renders one value nested under its key path — the paste-ready
// fallback for TOML configs, used for a package-level dependency list and for
// the initials map alike.
func RenderKeyTOML(keyPath []string, value any) (string, error) {
	wrapped := value
	for i := len(keyPath) - 1; i >= 0; i-- {
		wrapped = map[string]any{keyPath[i]: wrapped}
	}
	out, err := toml.Marshal(wrapped)
	return string(out), err
}

// ErrRefEdit reports a key whose value is composed from a referenced file and
// the keys written beside the reference. There is no single file the new value
// could be written to, so the caller explains the two ways out rather than
// picking one.
var ErrRefEdit = errors.New("a key composed from a $ref and the keys beside it cannot be rewritten in place")

// ErrMultiRefEdit reports a key whose value is merged from several referenced
// files. No one of them holds the value, so the caller explains the ways out
// rather than choosing a file to write to.
var ErrMultiRefEdit = errors.New("a key merged from several $ref files cannot be rewritten in place")

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
// spliced. A key no file holds comes back unchanged, for the writers to
// create or refuse as they already do.
func ResolveEdit(path string, keyPath []string) (string, []string, error) {
	return resolveEdit(path, keyPath, 0)
}

// resolveEdit is ResolveEdit with the number of references already followed,
// which is what bounds it: the loader refuses a cycle long before an edit is
// collected, so this only has to stop rather than explain.
func resolveEdit(path string, keyPath []string, followed int) (string, []string, error) {
	if followed > maxRefDepth {
		return "", nil, fmt.Errorf("$ref nesting is more than %d files deep at %s", maxRefDepth, path)
	}
	doc, err := decodeFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	node, ok := doc.(map[string]any)
	if !ok {
		return path, keyPath, nil
	}
	for depth, key := range keyPath {
		value, found := lookupFold(node, key)
		if !found {
			return path, keyPath, nil
		}
		child, ok := value.(map[string]any)
		if !ok {
			return path, keyPath, nil
		}
		targets, isRef, err := refTargets(child)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %s: %w", path, strings.Join(keyPath[:depth+1], "."), err)
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
				return "", nil, fmt.Errorf("%s: %s: %w; write %s beside the $ref, or leave the $ref as the whole value",
					path, strings.Join(keyPath[:depth+1], "."), ErrRefEdit, keyPath[len(keyPath)-1])
			}
			if _, beside := lookupFold(child, rest[0]); beside {
				return path, keyPath, nil
			}
		}
		if len(targets) > 1 {
			// The value is merged from every file the reference names, so no
			// one of them can be handed the write.
			return "", nil, fmt.Errorf("%s: %s: %w; write %s beside the $ref, or point the $ref at a single file",
				path, strings.Join(keyPath[:depth+1], "."), ErrMultiRefEdit, keyPath[len(keyPath)-1])
		}
		return resolveEdit(refPath(targets[0], path), rest, followed+1)
	}
	return path, keyPath, nil
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

// StringMapAt reads the string map at keyPath of the config file at path,
// exactly as the file spells it. It exists because the loaded *File cannot be
// written back: viper lowercases every map key, so round-tripping the parsed
// `initials` through a write would rename the user's entries. A key the file
// does not carry is no error and returns a nil map; a value that is not a map
// of scalars is.
//
// Unlike the writers this reads TOML too: a TOML config still needs its
// current entries to render the paste-ready block.
func StringMapAt(path string, keyPath []string) (map[string]string, error) {
	t, err := readTree(path)
	if err != nil {
		return nil, err
	}
	node := t.root
	for depth, key := range keyPath {
		value, ok := lookupFold(node, key)
		if !ok {
			return nil, nil
		}
		child, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %s is not an object", path, strings.Join(keyPath[:depth+1], "."))
		}
		node = child
	}
	out := make(map[string]string, len(node))
	for key, value := range node {
		// Every value dispat reads through this is written as a quoted string
		// in each format it accepts, and a version the loader would reject —
		// an unquoted YAML 1.0, which decodes as a number — never reaches a
		// write, because the config carrying it does not load.
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s.%s is not a string", path, strings.Join(keyPath, "."), key)
		}
		out[key] = text
	}
	return out, nil
}

// lookupFold finds a key case-insensitively, the way the splicing writers
// match theirs.
func lookupFold(node map[string]any, key string) (any, bool) {
	for name, value := range node {
		if strings.EqualFold(name, key) {
			return value, true
		}
	}
	return nil, false
}

// nullRendering reports that a value marshalled to JSON's "null", which is
// what a nil slice does. An emptied list must read as "[]": a key holding
// null is a config that no longer says "nothing depends on anything", it says
// nothing at all.
//
// A value that renders itself — Dependencies writes its own "{}" — never
// produces null, so this leaves it alone.
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
		return nil, fmt.Errorf("key %s not found", strings.Join(keyPath, "."))
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
// brace when a single absent top-level key is asked for. An absent ancestor
// is an error.
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
			return 0, 0, 0, fmt.Errorf("key %s not found", strings.Join(keyPath, "."))
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
// two spaces when the file gives no hint (what `dispat init` writes).
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
// absent top-level key) and re-encodes; yaml.v3 nodes carry their comments,
// so the rest of the file survives the round trip.
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
				return nil, fmt.Errorf("key %s not found", strings.Join(keyPath, "."))
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
