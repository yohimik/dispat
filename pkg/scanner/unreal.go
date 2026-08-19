package scanner

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// unrealDescriptor is the subset of a .uproject or .uplugin the scanner reads.
// The two are the same JSON shape with different fields filled in: a project
// declares plugins and no version, a plugin declares both.
type unrealDescriptor struct {
	// VersionName is the marketing version, the one a human reads.
	VersionName string `json:"VersionName"`
	// Version is the monotonic build counter beside it, written as a bare
	// integer. It is kept raw because that is the only way to tell the number
	// 4 from the string "4", and the writer has to put back the shape it
	// found.
	Version json.RawMessage `json:"Version"`
	// Plugins are the plugins the descriptor enables. They carry no version:
	// Unreal resolves a plugin by name against the project and the engine, so
	// the declaration is the name and nothing else.
	Plugins []unrealPluginRef `json:"Plugins"`
}

// unrealPluginRef is one entry of a descriptor's Plugins array.
type unrealPluginRef struct {
	Name string `json:"Name"`
}

// parseUProject reads a .uproject: the plugins the project enables, as
// versionless dependencies.
//
// The name is the file's own base name rather than anything inside it. That is
// what other descriptors reference a project or plugin by, and what the folder
// on disk is called, so it is the name a workspace can actually resolve.
//
// EngineAssociation is deliberately not read. It pins the engine the project
// builds against, which is a toolchain choice rather than something the
// project ships, the same reasoning that leaves a pom's parent version alone.
func parseUProject(rel string, data []byte) (Manifest, error) {
	return parseUnrealDescriptor(rel, data, false)
}

// parseUPlugin reads a .uplugin: the plugin's marketing version
// (VersionName), its build counter (Version), and the plugins it depends on in
// turn.
func parseUPlugin(rel string, data []byte) (Manifest, error) {
	return parseUnrealDescriptor(rel, data, true)
}

// parseUnrealDescriptor reads either descriptor. versioned says whether the
// format declares a version of its own; a .uproject does not, and reading one
// out of a stray VersionName would report a version no build ever uses.
func parseUnrealDescriptor(rel string, data []byte, versioned bool) (Manifest, error) {
	var raw unrealDescriptor
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	base := path.Base(rel)
	m := Manifest{
		Path:      rel,
		Ecosystem: EcosystemUnreal,
		Name:      strings.TrimSuffix(base, path.Ext(base)),
		Root:      isRoot(rel),
	}
	if versioned {
		m.Version = raw.VersionName
		m.BuildNumber = unrealCounter(raw.Version)
	}
	for i, p := range raw.Plugins {
		if p.Name == "" {
			// An entry with no name declares nothing this can resolve. It is
			// not an error, the descriptor parsed; it is a drop.
			m.Dropped = append(m.Dropped, fmt.Sprintf("plugin %d: no name", i))
			continue
		}
		// A disabled plugin is still declared, and the dependency edge is
		// still there: a build that turns it back on needs it released.
		m.Deps = append(m.Deps, DeclaredDep{Name: p.Name, Kind: KindDependencies})
	}
	sortDeps(m.Deps)
	sort.Strings(m.Dropped)
	return m, nil
}

// unrealCounter renders the raw Version field as text. Unreal writes a bare
// integer, and some hand-edited descriptors quote it; both read as the same
// counter, and anything composite is not a counter at all.
func unrealCounter(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return ""
	}
	if text[0] == '"' {
		var out string
		if json.Unmarshal(raw, &out) != nil {
			return ""
		}
		return out
	}
	if text[0] == '{' || text[0] == '[' {
		return ""
	}
	return text
}
