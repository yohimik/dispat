// Package workspaceenv carries transient ownership information between nested
// dispat commands. It never changes Git state or persists release metadata.
package workspaceenv

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	Root    = "DISPAT_INTERNAL_WORKSPACE_ROOT"
	Config  = "DISPAT_INTERNAL_WORKSPACE_CONFIG"
	Imports = "DISPAT_INTERNAL_WORKSPACE_CONFIGS"
	Owners  = "DISPAT_INTERNAL_WORKSPACE_OWNERS"
	// Repositories is the exact list of source repository identities in the
	// enclosing workspace. Package export keys alone cannot carry this list:
	// two legal package names can collapse to the same EnvKey.
	Repositories = "DISPAT_INTERNAL_WORKSPACE_REPOSITORIES"
)

// Pins returns only full commit IDs exported for packages with a known owner
// in the enclosing workspace. An owner may have several sequential exports;
// the caller must compare its checkout with these exact IDs, never move HEAD.
func Pins(root, configPath string, env []string) (map[string][]string, error) {
	values := environmentValues(env)
	nestedRoot, nestedConfig := values[Root], values[Config]
	if nestedRoot == "" || nestedConfig == "" || values[Imports] == "" || !samePath(root, nestedRoot) {
		return nil, nil
	}
	if !filepath.IsAbs(nestedConfig) {
		nestedConfig = filepath.Join(nestedRoot, nestedConfig)
	}
	if !samePath(configPath, nestedConfig) {
		return nil, nil
	}
	var owners map[string]string
	if err := json.Unmarshal([]byte(values[Owners]), &owners); err != nil || owners == nil {
		return nil, nil
	}
	pins := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	addOwner := func(owner, value string) {
		if owner == "" || owner == "control" || !fullCommit(value) {
			return
		}
		if seen[owner] == nil {
			seen[owner] = make(map[string]bool)
		}
		if !seen[owner][value] {
			seen[owner][value] = true
			pins[owner] = append(pins[owner], value)
		}
	}
	add := func(name, value string) { addOwner(owners[name], value) }
	for name, value := range values {
		if name, ok := strings.CutPrefix(name, "DISPAT_OUTPUT_"); ok {
			add(name, value)
		}
	}
	// Two commands in one shell share the live output file before the outer
	// sequence merges it into the environment. Ignore unrelated malformed
	// exports here; the normal sequence parser reports them at its usual point.
	if path := values["DISPAT_OUTPUT"]; path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader := bufio.NewReader(f)
		maxLine := 0
		for name := range owners {
			maxLine = max(maxLine, len("DISPAT_OUTPUT_")+len(name)+1+64+1)
		}
		var line []byte
		oversized := false
		for {
			part, readErr := reader.ReadSlice('\n')
			if len(line)+len(part) > maxLine {
				oversized = true
			}
			if !oversized {
				line = append(line, part...)
			}
			if errors.Is(readErr, bufio.ErrBufferFull) {
				continue
			}
			if readErr == nil { // an unfinished concurrent write is not evidence
				name, value, ok := strings.Cut(strings.TrimSuffix(string(line), "\n"), "=")
				if ok && !oversized {
					add(strings.TrimPrefix(name, "DISPAT_OUTPUT_"), value)
				}
			}
			line, oversized = line[:0], false
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					return nil, readErr
				}
				break
			}
		}
	}
	live, err := livePins(root, configPath, values)
	if err != nil {
		return nil, err
	}
	for owner, revisions := range live {
		for _, revision := range revisions {
			addOwner(owner, revision)
		}
	}
	return pins, nil
}

func samePath(a, b string) bool {
	a, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	b, err = filepath.EvalSymlinks(b)
	return err == nil && filepath.Clean(a) == filepath.Clean(b)
}

func fullCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
