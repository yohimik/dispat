package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateCapturedSources holds a loaded configuration's complete reference
// closure inside one private materialization. An empty root preserves the
// ordinary loader's support for configuration files elsewhere on the host.
func ValidateCapturedSources(root string, files []string) error {
	if root == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return err
		}
		if !capturedSourceContains(root, absolute) {
			return capturedSourceEscape(file)
		}
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil {
			return err
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return err
		}
		if !capturedSourceContains(root, resolved) {
			return capturedSourceEscape(file)
		}
	}
	return nil
}

func capturedSourceContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func capturedSourceEscape(file string) error {
	return WithDiagnostic(DiagnosticExecution,
		fmt.Errorf("configuration source %q escapes captured sources", file))
}
