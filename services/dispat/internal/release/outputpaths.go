// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// RemapOutputEnvironment gives a task the path to its own materialized copy
// of an earlier stage's declared output. Other exported values are unchanged.
func RemapOutputEnvironment(env []string, mappings []OutputPathMapping) []string {
	if len(mappings) == 0 {
		return env
	}
	converted := make([]string, len(env))
	for index, pair := range env {
		name, value, ok := strings.Cut(pair, "=")
		if !ok || !strings.HasPrefix(name, "DISPAT_OUTPUT_") {
			converted[index] = pair
			continue
		}
		converted[index] = name + "=" + remapDeclaredPath(value, mappings, false)
	}
	return converted
}

// RemapCapturedOutputs replaces private checkout paths under declared output
// roots with paths in the authoritative source checkout.
func RemapCapturedOutputs(outputs []plan.Output, mappings []OutputPathMapping) {
	for index := range outputs {
		outputs[index].Value = remapDeclaredPath(outputs[index].Value, mappings, true)
	}
}

// remapDeclaredPath changes only a complete absolute path beneath a declared
// output root. An output value that merely contains a path remains literal.
func remapDeclaredPath(value string, mappings []OutputPathMapping, toSource bool) string {
	bestLength := -1
	mapped := value
	for _, mapping := range mappings {
		var relative string
		var ok bool
		fromLength := len(mapping.SourceRoot)
		if toSource {
			if !filepath.IsAbs(value) {
				continue
			}
			relative, ok = relativeTaskPath(value, mapping.TaskRoot)
			fromLength = len(mapping.TaskRoot)
		} else {
			relative, ok = relativeSourcePath(value, mapping.SourceRoot)
		}
		if !ok {
			continue
		}
		for _, root := range mapping.Roots {
			root = path.Clean(strings.ReplaceAll(root, "\\", "/"))
			if relative != root && !strings.HasPrefix(relative, root+"/") {
				continue
			}
			if fromLength > bestLength {
				bestLength = fromLength
				if toSource {
					mapped = joinSourcePath(mapping.SourceRoot, relative)
				} else {
					mapped = filepath.Join(mapping.TaskRoot, filepath.FromSlash(relative))
				}
			}
		}
	}
	return mapped
}

func relativeTaskPath(value, root string) (string, bool) {
	relative, err := filepath.Rel(root, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// The source path is from the orchestrator's OS, which may differ from this
// worker's OS. Compare its signed prefix lexically with that OS's separators.
func relativeSourcePath(value, root string) (string, bool) {
	isWindows := isWindowsPath(root)
	if !isWindows && !strings.HasPrefix(root, "/") {
		return "", false
	}
	if isWindows {
		if strings.HasPrefix(root, `\\`) && !strings.HasPrefix(value, `\\`) &&
			!strings.HasPrefix(value, "//") {
			return "", false
		}
		value = strings.ReplaceAll(value, "\\", "/")
		root = strings.ReplaceAll(root, "\\", "/")
	}
	value, root = path.Clean(value), path.Clean(root)
	if isWindows {
		if strings.EqualFold(value, root) {
			return ".", true
		}
		if len(value) > len(root) && strings.EqualFold(value[:len(root)], root) && value[len(root)] == '/' {
			return value[len(root)+1:], true
		}
		return "", false
	}
	if value == root {
		return ".", true
	}
	if root == "/" && strings.HasPrefix(value, "/") {
		return strings.TrimPrefix(value, "/"), true
	}
	if len(value) > len(root) && strings.HasPrefix(value, root) && value[len(root)] == '/' {
		return value[len(root)+1:], true
	}
	return "", false
}

func isWindowsPath(value string) bool {
	return (len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') ||
		(value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' &&
		(value[2] == '\\' || value[2] == '/')) || strings.HasPrefix(value, `\\`)
}

func joinSourcePath(root, relative string) string {
	if isWindowsPath(root) {
		separator := "/"
		if strings.Contains(root, "\\") {
			separator = `\`
		}
		return strings.TrimRight(root, `/\`) + separator + strings.ReplaceAll(relative, "/", separator)
	}
	return path.Join(root, relative)
}
