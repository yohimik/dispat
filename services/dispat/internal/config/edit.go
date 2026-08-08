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
)

// ErrTOMLEdit reports that the config is TOML, which dispat cannot rewrite
// format-preservingly; the caller renders a paste-ready snippet instead.
var ErrTOMLEdit = errors.New("a TOML config cannot be rewritten in place")

// BackupSuffix is appended to the config file name for the pre-edit copy.
const BackupSuffix = ".backup"

// ReplaceDependencies rewrites only the `dependencies` key of the config file
// at path, leaving every other byte (formatting, key order, comments)
// untouched, after saving a byte-for-byte copy at path + BackupSuffix. JSON
// and YAML configs are supported; TOML returns ErrTOMLEdit.
func ReplaceDependencies(path string, deps []DependencyConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []byte
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		out, err = replaceDepsJSON(data, deps)
	case ".yaml", ".yml":
		out, err = replaceDepsYAML(data, deps)
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
	if err := os.WriteFile(path+BackupSuffix, data, 0o644); err != nil {
		return fmt.Errorf("saving backup: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, info.Mode().Perm())
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

// replaceDepsJSON splices a re-rendered dependencies array over the existing
// one (or appends the key when absent), byte-precise outside the array.
func replaceDepsJSON(data []byte, deps []DependencyConfig) ([]byte, error) {
	indent := detectJSONIndent(data)
	rendered, err := json.MarshalIndent(deps, indent, indent)
	if err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		rendered = []byte("[]")
	}

	start, end, closing, err := jsonDependenciesSpan(data)
	if err != nil {
		return nil, err
	}
	if start >= 0 {
		return splice(data, start, end, rendered), nil
	}
	// No dependencies key: append it as the last member, before the object's
	// closing brace. A separating comma is needed unless the object is empty.
	head := bytes.TrimRight(data[:closing], " \t\n\r")
	sep := ",\n" + indent
	if bytes.HasSuffix(head, []byte("{")) {
		sep = "\n" + indent
	}
	entry := sep + `"dependencies": ` + string(rendered) + "\n"
	return splice(data, int64(len(head)), closing, []byte(entry)), nil
}

// splice returns data with [start, end) replaced.
func splice(data []byte, start, end int64, repl []byte) []byte {
	out := make([]byte, 0, int64(len(data))-(end-start)+int64(len(repl)))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	return append(out, data[end:]...)
}

// jsonDependenciesSpan tokenises the file and returns the byte span of the
// top-level "dependencies" value, or start = -1 with the offset of the
// object's closing brace when the key is absent.
func jsonDependenciesSpan(data []byte) (start, end, closing int64, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, 0, err
	}
	if tok != json.Delim('{') {
		return 0, 0, 0, errors.New("top level is not an object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, 0, 0, err
		}
		key, _ := keyTok.(string)
		afterKey := dec.InputOffset()
		if !strings.EqualFold(key, "dependencies") {
			if err := skipJSONValue(dec); err != nil {
				return 0, 0, 0, err
			}
			continue
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
	// The next token is the object's closing brace.
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

// replaceDepsYAML swaps the document's top-level `dependencies` value node
// (appending the key when absent) and re-encodes; yaml.v3 nodes carry their
// comments, so the rest of the file survives the round trip.
func replaceDepsYAML(data []byte, deps []DependencyConfig) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("top level is not a mapping")
	}
	var value yaml.Node
	if err := value.Encode(deps); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if strings.EqualFold(root.Content[i].Value, "dependencies") {
			root.Content[i+1] = &value
			replaced = true
			break
		}
	}
	if !replaced {
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: "dependencies"}
		root.Content = append(root.Content, key, &value)
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
