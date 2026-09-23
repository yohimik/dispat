// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Locating an atomic output staging folder for a repository checkout.
//
// A normal checkout's Git index and working tree share a filesystem, so its
// private Git directory is the safest place to stage: no Git status, commit,
// snapshot or script can see the temporary files. A linked worktree's index
// lives under the original checkout, possibly on another filesystem. In that
// case a private sibling outside every enclosing working tree preserves both
// properties: a rename into the destination stays atomic and no repository
// can accidentally record the temporary files.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type outputStagingSpec struct {
	indexPath   string
	ownerDir    string
	destination string
	run         string
	packageName string
}

func resolveOutputStagingPath(spec outputStagingSpec) (string, error) {
	private := filepath.Dir(spec.indexPath)
	same, err := isSameFileSystem(private, spec.destination)
	if err != nil {
		return "", fmt.Errorf("execution: checking the output staging filesystem: %w", err)
	}
	// Names are bounded and exact-identity based. Replacing punctuation with
	// dashes would make two distinct package names share a staging folder, and
	// an unbounded package name could exceed one filesystem component's limit.
	name := "dispat-outputs-" + formatIdentityHash(spec.run) + "-" +
		formatIdentityHash(spec.packageName) + "-" + formatRandomHex(8)
	if same {
		return filepath.Join(private, name), nil
	}
	owner, err := filepath.Abs(spec.ownerDir)
	if err != nil {
		return "", fmt.Errorf("execution: locating the owning checkout for output staging: %w", err)
	}
	owner, err = filepath.EvalSymlinks(owner)
	if err != nil {
		return "", fmt.Errorf("execution: resolving the owning checkout for output staging: %w", err)
	}
	parent, err := findPrivateStagingParent(owner)
	if err != nil {
		return "", err
	}
	same, err = isSameFileSystem(parent, spec.destination)
	if err != nil {
		return "", fmt.Errorf("execution: checking the linked worktree staging filesystem: %w", err)
	}
	if !same {
		return "", fmt.Errorf("execution: the linked worktree's Git directory and its private sibling are both on a different filesystem from %s; output roots cannot be installed atomically", spec.destination)
	}
	return filepath.Join(parent, "."+formatIdentityHash(owner)+"-"+name), nil
}

func formatIdentityHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:16])
}

// findPrivateStagingParent avoids putting temporary output bytes inside a
// control repository that happens to contain the owning repository, too.
// Every ancestor carrying a .git directory or linked-worktree file is a
// working tree that could otherwise stage or publish the temporary folder.
func findPrivateStagingParent(ownerDir string) (string, error) {
	outer := ownerDir
	found := false
	for dir := ownerDir; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			outer = dir
			found = true
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("execution: inspecting enclosing Git checkouts: %w", err)
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	if !found || filepath.Dir(outer) == outer {
		return "", fmt.Errorf("execution: no private sibling outside the owning Git checkout is available for atomic output staging")
	}
	return filepath.Dir(outer), nil
}
