package scanner

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type aquaImportConfig struct {
	ImportDir string `yaml:"import_dir"`
	Packages  []struct {
		Import string `yaml:"import"`
	} `yaml:"packages"`
}

func parseAquaImports(data []byte) (string, []string, error) {
	var c aquaImportConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return "", nil, err
	}
	var imports []string
	for _, p := range c.Packages {
		if p.Import != "" {
			imports = append(imports, p.Import)
		}
	}
	return c.ImportDir, imports, nil
}

// parseAqua reads only literal package pins from an Aqua configuration. It
// intentionally does not use Aqua's runtime reader: that reader evaluates
// version_expr/go_version_file and resolves imports and registries, operations
// a manifest inventory must never perform while inspecting a checkout.
func parseAqua(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemAqua, Root: isRoot(rel)}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return m, err
	}
	root := yamlNode(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return m, fmt.Errorf("aqua: document must be a mapping")
	}
	packages := yamlMapValue(root, "packages")
	if packages == nil {
		return m, nil
	}
	packages = yamlNode(packages)
	if packages.Kind != yaml.SequenceNode {
		return m, fmt.Errorf("aqua: packages must be a sequence")
	}
	for i, item := range packages.Content {
		item = yamlNode(item)
		if item == nil || item.Kind != yaml.MappingNode {
			return m, fmt.Errorf("aqua: packages[%d] must be a mapping", i)
		}
		if aquaYAMLScalar(item, "import") != "" {
			continue
		}
		name := aquaYAMLScalar(item, "name")
		if name == "" {
			return m, fmt.Errorf("aqua: packages[%d] needs name or import", i)
		}
		version := aquaYAMLScalar(item, "version")
		if n, v, ok := strings.Cut(name, "@"); ok {
			name, version = n, v // Aqua's inline version wins.
		}
		registry := aquaYAMLScalar(item, "registry")
		if registry != "" && registry != "standard" {
			name = registry + ":" + name
		}
		switch {
		case aquaYAMLScalar(item, "version_expr") != "":
			m.Dropped = append(m.Dropped, name+": version_expr is dynamic and was not evaluated")
			continue
		case aquaYAMLScalar(item, "go_version_file") != "":
			m.Dropped = append(m.Dropped, name+": go_version_file is dynamic and was not read")
			continue
		case version == "":
			m.Dropped = append(m.Dropped, name+": no literal version")
			continue
		}
		m.Deps = append(m.Deps, DeclaredDep{Name: name, Range: version})
	}
	sort.Slice(m.Deps, func(i, j int) bool { return m.Deps[i].Name < m.Deps[j].Name })
	sort.Strings(m.Dropped)
	return m, nil
}

func yamlNode(n *yaml.Node) *yaml.Node {
	for n != nil && (n.Kind == yaml.DocumentNode || n.Kind == yaml.AliasNode) {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		} else if len(n.Content) != 0 {
			n = n.Content[0]
		} else {
			return nil
		}
	}
	return n
}

func yamlMapValue(n *yaml.Node, key string) *yaml.Node {
	n = yamlNode(n)
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

func aquaYAMLScalar(n *yaml.Node, key string) string {
	v := yamlNode(yamlMapValue(n, key))
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}
