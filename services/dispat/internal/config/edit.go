package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// ErrTOMLEdit reports that the config is TOML, which dispat cannot rewrite
// format-preservingly; the caller renders a paste-ready snippet instead.
var ErrTOMLEdit = errors.New("a TOML config cannot be rewritten in place")

// BackupSuffix is appended to the config file name for the pre-edit copy.
const BackupSuffix = ".backup"

// ReplaceDependencies rewrites only the dependency list at keyPath of the
// config file at path — ["dependencies"] for the file's own top-level list,
// ["packages", <key>, "dependencies"] for a packages entry of the root
// config — leaving every other byte of a JSON config (formatting, key order,
// comments) untouched; a YAML config keeps its comments but is re-encoded,
// so unrelated formatting may reflow, and a shorthand-authored list comes
// back in the canonical object form. The previous bytes are saved at path +
// BackupSuffix first, and the write itself is atomic (temp + rename). TOML
// returns ErrTOMLEdit.
//
// The file is re-read here: deps must be the caller's complete intended
// list, and an edit made to the file by someone else between the caller's
// read and this call is overwritten for that one key (every other key keeps
// the concurrent edit).
func ReplaceDependencies(path string, keyPath []string, deps []DependencyConfig) error {
	return replaceKey(path, keyPath, deps)
}

// ReplaceStringList is ReplaceDependencies for the package-level dependency
// shape: a plain list of provider names.
func ReplaceStringList(path string, keyPath []string, items []string) error {
	return replaceKey(path, keyPath, items)
}

// replaceKey rewrites the value at keyPath of the config file at path with
// the rendered value, backing the previous bytes up and writing atomically.
func replaceKey(path string, keyPath []string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []byte
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		out, err = replaceValueJSON(data, keyPath, value)
	case ".yaml", ".yml":
		out, err = replaceValueYAML(data, keyPath, value)
	case ".toml":
		return ErrTOMLEdit
	default:
		return fmt.Errorf("%s: unknown config format", path)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if bytes.Equal(out, data) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	// The backup carries the config's own permissions: a 0600 config must not
	// leak through a world-readable copy.
	if err := os.WriteFile(path+BackupSuffix, data, mode); err != nil {
		return fmt.Errorf("saving backup: %w", err)
	}
	return atomicWrite(path, out, mode)
}

// atomicWrite replaces path via a same-directory temp file and rename, so a
// crash mid-write can truncate the temp file but never the config itself.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
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

// RenderDependenciesTOML renders the dependencies list as [[dependencies]]
// blocks — the paste-ready fallback for TOML configs.
func RenderDependenciesTOML(deps []DependencyConfig) (string, error) {
	// Lowercase-keyed maps, because DependencyConfig has no toml tags and the
	// file's keys are the mapstructure ones.
	rows := make([]map[string]any, 0, len(deps))
	for _, d := range deps {
		row := map[string]any{"consumer": d.Consumer, "provider": d.Provider}
		if d.Kind != "" {
			row["kind"] = d.Kind
		}
		if d.Keep {
			row["keep"] = d.Keep
		}
		rows = append(rows, row)
	}
	out, err := toml.Marshal(map[string]any{"dependencies": rows})
	return string(out), err
}

// RenderStringListTOML renders a package-level dependency list at its key
// path — the paste-ready fallback for TOML configs.
func RenderStringListTOML(keyPath []string, items []string) (string, error) {
	var wrapped any = items
	for i := len(keyPath) - 1; i >= 0; i-- {
		wrapped = map[string]any{keyPath[i]: wrapped}
	}
	out, err := toml.Marshal(wrapped)
	return string(out), err
}

// emptyList reports a zero-length (or nil) slice value, which must render as
// "[]" rather than "null".
func emptyList(value any) bool {
	v := reflect.ValueOf(value)
	return v.Kind() == reflect.Slice && v.Len() == 0
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
	if emptyList(value) {
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
