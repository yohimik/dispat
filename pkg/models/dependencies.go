package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file is the `dependencies` key: the shapes it may be written in, and
// the one shape it is written back in.
//
// The graph is the same either way — a flat list of consumer -> provider
// edges — so everything downstream of decoding sees []DependencyConfig and
// never learns which form the file used. Only the wire format differs, and
// only here.

// Dependencies is the value of the `dependencies` key: the workspace's
// consumer -> provider edges.
//
// It is authored as an object keyed by consumer, which is the canonical form
// and what everything dispat writes emits:
//
//	"dependencies": {
//	  "app":  ["core", { "provider": "utils", "keep": true }],
//	  "docs": [{ "provider": "core", "kind": "devDependencies" }]
//	}
//
// A provider is a bare name when it has nothing else to say, and an object
// when it carries `kind` or `keep`. A consumer with exactly one provider may
// name it directly instead of wrapping it in an array.
//
// An array of {consumer, provider} edges is also accepted, since a generated
// config often has one to hand:
//
//	"dependencies": [{ "consumer": "app", "provider": "core" }]
//
// Items of that array may themselves be consumer-keyed objects, which is the
// same shorthand the map form uses for one consumer.
//
// The two forms mix freely and merge with the provider lists packages declare
// for themselves (PackageConfig.Dependencies) into one list.
type Dependencies []DependencyConfig

// MarshalJSON writes the canonical map form. Consumers come out in sorted
// order (encoding/json sorts map keys), each consumer's providers in the
// order they were declared, and each provider in the shortest shape that
// carries everything it says.
func (d Dependencies) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.canonical())
}

// MarshalYAML is MarshalJSON's counterpart for a YAML config; yaml.v3 sorts
// map keys too, so the two formats agree on ordering.
func (d Dependencies) MarshalYAML() (any, error) {
	return d.canonical(), nil
}

// UnmarshalJSON accepts every form the key may be written in.
func (d *Dependencies) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	deps, err := NormalizeDependencies(raw)
	if err != nil {
		return err
	}
	*d = deps
	return nil
}

// Grouped collects the edges by consumer, each consumer's providers in the
// order they were declared. It is the canonical form's skeleton, exported for
// writers that have to render the provider entries themselves: the TOML
// fallback cannot use the bare-name shorthand, because an array mixing names
// and tables is not something a TOML encoder will write.
func (d Dependencies) Grouped() map[string][]DependencyConfig {
	out := make(map[string][]DependencyConfig, len(d))
	for _, e := range d {
		out[e.Consumer] = append(out[e.Consumer], e)
	}
	return out
}

// canonical renders the edges as consumer -> providers, which is the shape
// both marshallers write.
func (d Dependencies) canonical() map[string][]any {
	out := make(map[string][]any, len(d))
	for _, e := range d {
		out[e.Consumer] = append(out[e.Consumer], providerItem(e))
	}
	return out
}

// providerItem renders one edge as the provider entry it becomes: the bare
// name when the edge says nothing else, an object when it carries kind or
// keep. Writing `{"provider": "core"}` for every plain edge would be noise on
// the line the reader is actually scanning.
func providerItem(e DependencyConfig) any {
	if e.Kind == "" && !e.Keep {
		return e.Provider
	}
	item := map[string]any{"provider": e.Provider}
	if e.Kind != "" {
		item["kind"] = e.Kind
	}
	if e.Keep {
		item["keep"] = true
	}
	return item
}

// ProviderList is a package's own `dependencies`: the providers it depends
// on, written where the package is configured rather than in the workspace's
// top-level object.
//
//	"packages": { "app": { "dependencies": ["core", {"provider": "utils", "keep": true}] } }
//
// The entries are exactly the ones a consumer lists in the top-level object,
// so an edge reads the same wherever it is declared and moving one between
// the two places is a cut and a paste. The consumer is the package itself and
// is left empty here; whoever reads the layer fills it in.
type ProviderList []DependencyConfig

// Providers builds a ProviderList of plain edges, which is what most package
// dependency lists are.
func Providers(names ...string) ProviderList {
	out := make(ProviderList, 0, len(names))
	for _, n := range names {
		out = append(out, DependencyConfig{Provider: n})
	}
	return out
}

// MarshalJSON writes the list in the canonical form: a bare name per plain
// edge, an object for one carrying kind or keep.
func (p ProviderList) MarshalJSON() ([]byte, error) {
	items := make([]any, 0, len(p))
	for _, e := range p {
		items = append(items, providerItem(e))
	}
	return json.Marshal(items)
}

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (p ProviderList) MarshalYAML() (any, error) {
	items := make([]any, 0, len(p))
	for _, e := range p {
		items = append(items, providerItem(e))
	}
	return items, nil
}

// UnmarshalJSON accepts one provider name, or an array of names and objects.
func (p *ProviderList) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	list, err := NormalizeProviders(raw, "dependencies")
	if err != nil {
		return err
	}
	*p = list
	return nil
}

// NormalizeProviders expands a package's own dependency list. where names the
// list for error messages, since the same shape is read at four override
// layers and the reader has to be told which one is wrong.
func NormalizeProviders(raw any, where string) (ProviderList, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := itemList(raw)
	if !ok {
		return nil, fmt.Errorf(
			"%s: wants a provider name, or an array of provider names and objects", where)
	}
	out := make(ProviderList, 0, len(items))
	for i, item := range items {
		edge, err := edgeFromItem(item, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, nil
}

// NormalizeDependencies expands any accepted form of the `dependencies` value
// into the flat edge list everything downstream works with.
//
// It is the single implementation behind both entry points — UnmarshalJSON
// here, and the CLI's decode hook, which hands over whatever its config reader
// produced. Two readers of one syntax would be two syntaxes eventually.
func NormalizeDependencies(raw any) (Dependencies, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		return normalizeEdgeList(v)
	default:
		m, ok := stringKeyed(raw)
		if !ok {
			return nil, fmt.Errorf(
				"dependencies wants an object keyed by consumer, or an array of {consumer, provider} edges")
		}
		return normalizeConsumerMap(m, "dependencies")
	}
}

// normalizeEdgeList expands the array form. An item carrying a `consumer` or
// `provider` key is one full edge; any other object is a consumer-keyed group,
// the same shape the map form is made of.
func normalizeEdgeList(items []any) (Dependencies, error) {
	var out Dependencies
	for i, item := range items {
		where := fmt.Sprintf("dependencies[%d]", i)
		m, ok := stringKeyed(item)
		if !ok {
			return nil, fmt.Errorf("%s: wants an object", where)
		}
		if isEdgeObject(m) {
			edge, err := edgeFromObject(m, where)
			if err != nil {
				return nil, err
			}
			out = append(out, edge)
			continue
		}
		group, err := normalizeConsumerMap(m, where)
		if err != nil {
			return nil, err
		}
		out = append(out, group...)
	}
	return out, nil
}

// normalizeConsumerMap expands one consumer -> providers object. Consumers are
// visited in sorted order so a map, which has none of its own, still produces
// the same edge order on every load.
func normalizeConsumerMap(m map[string]any, key string) (Dependencies, error) {
	consumers := make([]string, 0, len(m))
	for k := range m {
		consumers = append(consumers, k)
	}
	sort.Strings(consumers)

	var out Dependencies
	for _, consumer := range consumers {
		items, ok := itemList(m[consumer])
		if !ok {
			return nil, fmt.Errorf(
				"%s[%q]: wants a provider name, or an array of provider names and objects", key, consumer)
		}
		for i, item := range items {
			where := fmt.Sprintf("%s[%q][%d]", key, consumer, i)
			edge, err := edgeFromItem(item, where)
			if err != nil {
				return nil, err
			}
			edge.Consumer = consumer
			out = append(out, edge)
		}
	}
	return out, nil
}

// edgeFromItem reads one entry of a consumer's provider list: a bare name, or
// an object naming the provider and what the edge says about it.
func edgeFromItem(item any, where string) (DependencyConfig, error) {
	if name, ok := item.(string); ok {
		return DependencyConfig{Provider: name}, nil
	}
	m, ok := stringKeyed(item)
	if !ok {
		return DependencyConfig{}, fmt.Errorf("%s: wants a provider name or an object", where)
	}
	edge, err := edgeFromObject(m, where)
	if err != nil {
		return DependencyConfig{}, err
	}
	if edge.Consumer != "" {
		// A consumer key inside a consumer's own list is a config that means
		// two different things at once, and guessing which would put the edge
		// on the wrong package.
		return DependencyConfig{}, fmt.Errorf(
			"%s: consumer is already %q here, so the entry must not name another one", where, edge.Consumer)
	}
	return edge, nil
}

// edgeFromObject reads the four fields of an edge object, rejecting anything
// else so a typo is a load error rather than a silently ignored key.
func edgeFromObject(m map[string]any, where string) (DependencyConfig, error) {
	var edge DependencyConfig
	for _, k := range sortedMapKeys(m) {
		value := m[k]
		switch strings.ToLower(k) {
		case "consumer":
			s, ok := value.(string)
			if !ok {
				return edge, fmt.Errorf("%s: consumer wants a package name", where)
			}
			edge.Consumer = s
		case "provider":
			s, ok := value.(string)
			if !ok {
				return edge, fmt.Errorf("%s: provider wants a package name", where)
			}
			edge.Provider = s
		case "kind":
			s, ok := value.(string)
			if !ok {
				return edge, fmt.Errorf("%s: kind wants a manifest dependency field name", where)
			}
			edge.Kind = s
		case "keep":
			b, ok := value.(bool)
			if !ok {
				return edge, fmt.Errorf("%s: keep wants true or false", where)
			}
			edge.Keep = b
		default:
			return edge, fmt.Errorf("%s: unknown key %q, want consumer, provider, kind or keep", where, k)
		}
	}
	return edge, nil
}

// itemList reads a consumer's value: one provider entry, or an array of them.
// The scalar is lifted for the same reason every other "one or many" key in
// the config lifts it — a single provider is the common case and an array of
// one reads like a mistake.
func itemList(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case string:
		return []any{x}, true
	case nil:
		return nil, false
	default:
		if _, ok := stringKeyed(v); ok {
			return []any{v}, true
		}
		return nil, false
	}
}

// isEdgeObject reports whether an array item is a full edge rather than a
// consumer-keyed group.
func isEdgeObject(m map[string]any) bool {
	for k := range m {
		switch strings.ToLower(k) {
		case "consumer", "provider":
			return true
		}
	}
	return false
}

// stringKeyed normalises the two object shapes a config reader can produce:
// JSON gives map[string]any, some YAML readers give map[any]any.
func stringKeyed(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, value := range m {
			s, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[s] = value
		}
		return out, true
	}
	return nil, false
}

// sortedMapKeys visits an object's keys in order, so a config with two bad
// keys always reports the same one first.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
