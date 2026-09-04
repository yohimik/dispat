package writer

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// rewriteAqua changes literal Aqua package pins. Aqua configurations do not
// declare their own package version, and dynamic version sources are left
// untouched: evaluating them would execute policy rather than edit a manifest.
func rewriteAqua(path, _ string, edits []Edit) (Result, error) {
	sp, err := openSplicer(path)
	if err != nil {
		return Result{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(sp.bytes(), &doc); err != nil {
		return Result{}, err
	}
	root := writerYAMLNode(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return Result{}, fmt.Errorf("aqua: document must be a mapping")
	}
	packages := writerYAMLMap(root, "packages")
	if aquaYAMLHasSharedSource(packages) {
		return Result{}, fmt.Errorf("aqua: anchors and aliases in packages are not safely writable")
	}
	if packages != nil {
		packages = writerYAMLNode(packages)
	}
	if packages != nil && packages.Kind != yaml.SequenceNode {
		return Result{}, fmt.Errorf("aqua: packages must be a sequence")
	}
	if packages != nil && packages.Style&yaml.FlowStyle != 0 {
		return Result{}, fmt.Errorf("aqua: flow-style packages are not safely writable")
	}
	lines := sp.lines()
	seen := make([]bool, len(edits))
	applied := make([]bool, len(edits))
	skipped := make([]bool, len(edits))
	changed := false
	for _, rawItem := range nodeContent(packages) {
		if rawItem.Kind == yaml.AliasNode {
			return Result{}, fmt.Errorf("aqua: package aliases are not safely writable")
		}
		item := writerYAMLNode(rawItem)
		if item == nil || item.Kind != yaml.MappingNode || writerYAMLScalar(item, "import") != "" {
			continue
		}
		if item.Style&yaml.FlowStyle != 0 {
			return Result{}, fmt.Errorf("aqua: flow-style package entries are not safely writable")
		}
		nameNode := writerYAMLNode(writerYAMLMap(item, "name"))
		if nameNode == nil || nameNode.Kind != yaml.ScalarNode {
			continue
		}
		if nameNode.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.ContainsAny(nameNode.Value, "\r\n") {
			return Result{}, fmt.Errorf("aqua: block-scalar package names are not safely writable")
		}
		name, inlineVersion, inline := strings.Cut(nameNode.Value, "@")
		registry := writerYAMLScalar(item, "registry")
		identity := name
		if registry != "" && registry != "standard" {
			identity = registry + ":" + name
		}
		for i, edit := range edits {
			if edit.Kind != manifest.KindDependencies || edit.Name != identity {
				continue
			}
			seen[i] = true
			if writerYAMLScalar(item, "version_expr") != "" || writerYAMLScalar(item, "go_version_file") != "" {
				skipped[i] = true
				continue
			}
			valueNode := writerYAMLNode(writerYAMLMap(item, "version"))
			old := ""
			if inline {
				valueNode, old = nameNode, inlineVersion
			} else if valueNode != nil {
				old = valueNode.Value
			}
			if valueNode == nil || valueNode.Kind != yaml.ScalarNode {
				skipped[i] = true
				continue
			}
			if valueNode.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 || strings.ContainsAny(valueNode.Value, "\r\n") {
				return Result{}, fmt.Errorf("aqua: block-scalar package versions are not safely writable")
			}
			if old == edit.Range {
				continue
			}
			if !isYAMLWritable(edit.Range) {
				return Result{}, fmt.Errorf("%s: refusing to write %q into a YAML scalar", path, edit.Range)
			}
			li := valueNode.Line - 1
			if li < 0 || li >= len(lines) {
				return Result{}, fmt.Errorf("%s: invalid YAML source position", path)
			}
			line := lines[li]
			plain := stripYAMLComment(line)
			colon := strings.IndexByte(plain, ':')
			if colon < 0 {
				return Result{}, fmt.Errorf("%s: cannot locate Aqua version", path)
			}
			start, end, ok := yamlScalarSpan(plain, colon+1)
			if !ok {
				return Result{}, fmt.Errorf("%s: cannot locate Aqua version", path)
			}
			next := edit.Range
			if inline {
				next = name + "@" + edit.Range
			}
			lines[li] = line[:start] + next + line[end:]
			valueNode.Value = next
			if inline {
				inlineVersion = edit.Range
			} else {
				old = edit.Range
			}
			applied[i] = true
			changed = true
		}
	}
	res := Result{}
	// A literal target that already has the requested pin is a successful
	// no-op; only absent/dynamic targets are missing/skipped respectively.
	for i, e := range edits {
		if !seen[i] {
			res.Missing = append(res.Missing, e)
			continue
		}
		if skipped[i] {
			res.Skipped = append(res.Skipped, e)
			continue
		}
		if applied[i] {
			res.Applied = append(res.Applied, e)
		}
	}
	if changed {
		sp.setLines(lines)
	}
	return res, sp.commit(func(out []byte) error { var n yaml.Node; return yaml.Unmarshal(out, &n) })
}

func writerYAMLNode(n *yaml.Node) *yaml.Node {
	for n != nil && (n.Kind == yaml.DocumentNode || n.Kind == yaml.AliasNode) {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else if len(n.Content) > 0 {
			n = n.Content[0]
		} else {
			return nil
		}
	}
	return n
}
func writerYAMLMap(n *yaml.Node, key string) *yaml.Node {
	n = writerYAMLNode(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func writerYAMLScalar(n *yaml.Node, key string) string {
	v := writerYAMLNode(writerYAMLMap(n, key))
	if v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
func nodeContent(n *yaml.Node) []*yaml.Node {
	if n == nil {
		return nil
	}
	return n.Content
}

func aquaYAMLHasSharedSource(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	for _, child := range n.Content {
		if aquaYAMLHasSharedSource(child) {
			return true
		}
	}
	return false
}
