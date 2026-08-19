package scanner

import (
	"encoding/json"
	"strings"
)

// The ProjectSettings.asset keys the scanner reads, and the indent they sit
// at. The file holds one document whose single root mapping is PlayerSettings,
// so the settings themselves are one level in.
const (
	unityPlayerIndent     = 2
	unityKeyProductName   = "productName"
	unityKeyBundleVersion = "bundleVersion"
	unityKeyAndroidCode   = "AndroidBundleVersionCode"
	unityKeyBuildNumber   = "buildNumber"
)

// unityPackagesManifest is the subset of Packages/manifest.json the scanner
// reads. Unity's package manifest declares one flat dependency map and no
// identity of its own: the project is named by its folder, not by this file.
type unityPackagesManifest struct {
	Dependencies map[string]string `json:"dependencies"`
}

// parseUnityPackages reads a Packages/manifest.json, the file the Unity
// package manager resolves a project against. Every entry is a dependency:
// a registry version ("3.0.6"), a folder ("file:../../packages/core"), or a
// git URL ("https://github.com/acme/core.git#v1.2.3"). All three are kept
// verbatim, and the folder form also yields the local path, which is what
// makes an embedded package a workspace edge.
//
// The manifest declares no name and no version. It says what the project
// consumes, not what it is.
func parseUnityPackages(rel string, data []byte) (Manifest, error) {
	var raw unityPackagesManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, err
	}
	m := Manifest{Path: rel, Ecosystem: EcosystemUnity, Root: isRoot(rel)}
	for name, rng := range raw.Dependencies {
		m.Deps = append(m.Deps, DeclaredDep{
			Name:      name,
			Range:     rng,
			Kind:      KindDependencies,
			LocalPath: npmLocalPath(rng),
		})
	}
	sortDeps(m.Deps)
	return m, nil
}

// parseUnityProjectSettings reads a ProjectSettings/ProjectSettings.asset: the
// product name, the marketing version (bundleVersion) and the store build
// counter beside it. It declares no dependencies, so it is an identity-only
// manifest the way an Info.plist is.
//
// The file is YAML, but not YAML any library will parse. Unity writes its own
// tag directive and tags the document with a class id:
//
//	%TAG !u! tag:unity3d.com,2011:
//	--- !u!129 &1
//
// A conforming parser refuses the unresolvable !u!129, which would mean
// refusing every real Unity project, so this reads the file by line the way
// the Xcode project reader does. Only the settings one level in are read: the
// nested mappings below them carry keys of their own, and a flat walk would
// happily take a version out of one.
//
// The counter reported is AndroidBundleVersionCode, the one every Unity
// project has. Where a project sets only the per-platform counters under
// buildNumber, the first of those stands in, so a project stamping iOS alone
// still reports the counter it uses.
func parseUnityProjectSettings(rel string, data []byte) (Manifest, error) {
	m := Manifest{Path: rel, Ecosystem: EcosystemUnity, Root: isRoot(rel)}
	var (
		inBuildNumber bool
		platformCode  string
	)
	unityScan(data, func(indent int, key, value string) {
		switch {
		case indent == unityPlayerIndent:
			inBuildNumber = key == unityKeyBuildNumber
			switch key {
			case unityKeyProductName:
				m.Name = strings.Clone(value)
			case unityKeyBundleVersion:
				m.Version = strings.Clone(value)
			case unityKeyAndroidCode:
				m.BuildNumber = strings.Clone(value)
			}
		case indent > unityPlayerIndent && inBuildNumber:
			if platformCode == "" && value != "" {
				platformCode = strings.Clone(value)
			}
		default:
			inBuildNumber = false
		}
	})
	if m.BuildNumber == "" {
		m.BuildNumber = platformCode
	}
	return m, nil
}

// unityScan walks a Unity asset file by line, calling visit for every mapping
// entry with the indent it sits at. Document markers, tag directives and list
// items are stepped over; everything else is a key, its indent, and the text
// after the colon.
//
// The strings handed to visit share the file's memory, and a ProjectSettings
// asset runs to megabytes. A caller keeping one clones it.
func unityScan(data []byte, visit func(indent int, key, value string)) {
	for text := string(data); text != ""; {
		line := text
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			text = ""
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "" || line[0] == '%' || strings.HasPrefix(line, "---") {
			continue
		}
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}
		body := line[indent:]
		if body == "" || body[0] == '#' || body[0] == '-' {
			continue
		}
		colon := strings.IndexByte(body, ':')
		if colon < 0 {
			continue
		}
		visit(indent, body[:colon], strings.TrimSpace(unityStripComment(body[colon+1:])))
	}
}

// unityStripComment cuts a trailing YAML comment, which begins at a '#'
// preceded by space. A '#' with no space before it is part of the value, which
// is what keeps a product name like "Level #1" intact.
func unityStripComment(value string) string {
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && (value[i-1] == ' ' || value[i-1] == '\t') {
			return value[:i]
		}
	}
	return value
}
