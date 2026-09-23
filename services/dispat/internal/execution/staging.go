// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Where an admitted output set is assembled before it is installed into a
// repository checkout.
//
// A checkout's private Git directory is the first place: no Git status,
// commit, snapshot or script can see the temporary files there, and it
// usually shares a filesystem with the working tree, which is what makes
// installing a root a rename rather than a copy. A linked worktree's private
// Git directory lives under the checkout it was added to, possibly on another
// filesystem, and there the final rename fails. The set is then assembled
// again in a hidden folder beside the outermost enclosing checkout, which is
// still outside every repository that could record the temporary files.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// outputStagingSpec is what the staging folders of one set are derived from:
// the owning checkout's index, which names its private Git directory, the
// checkout itself, and the exact identities the folder names are hashed from.
type outputStagingSpec struct {
	indexPath   string
	ownerDir    string
	run         string
	packageName string
}

// installStaged installs one set through the owning checkout's private Git
// directory, and assembles it again beside the outermost checkout when the
// final rename out of the private Git directory fails. Every other failure,
// and a failure of the second attempt, is reported as it is.
//
// install is InstallOutputs or MergeInstallOutputs. Either one puts back what
// it moved when a rename fails and removes its staging folder on every path,
// so the second attempt starts from the destination the first one found.
func installStaged(ctx context.Context, spec outputStagingSpec, request InstallRequest,
	install func(context.Context, InstallRequest) error) error {
	request.Staging = resolvePrivateStagingPath(spec)
	err := install(ctx, request)
	if !isStagedRenameFailure(err) {
		return err
	}
	sibling, siblingErr := resolveSiblingStagingPath(spec)
	if siblingErr != nil {
		return errors.Join(err, siblingErr)
	}
	request.Log.Debug().Err(err).Str("staging", sibling).
		Msg("output staging moved beside the checkout: the private Git directory cannot rename into it")
	request.Staging = sibling
	return install(ctx, request)
}

// resolvePrivateStagingPath is the first staging folder: a folder of the
// owning checkout's private Git directory.
func resolvePrivateStagingPath(spec outputStagingSpec) string {
	return filepath.Join(filepath.Dir(spec.indexPath), formatStagingName(spec))
}

// resolveSiblingStagingPath is the second staging folder: a hidden folder
// beside the outermost checkout that encloses the owning one, named after the
// owning checkout as well as the set.
func resolveSiblingStagingPath(spec outputStagingSpec) (string, error) {
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
	return filepath.Join(parent, "."+formatIdentityHash(owner)+"-"+formatStagingName(spec)), nil
}

// formatStagingName names one staging folder. Names are bounded and
// exact-identity based: replacing punctuation with dashes would make two
// distinct package names share a staging folder, and an unbounded package
// name could exceed one filesystem component's limit. The random suffix keeps
// two attempts at one set apart.
func formatStagingName(spec outputStagingSpec) string {
	return "dispat-outputs-" + formatIdentityHash(spec.run) + "-" +
		formatIdentityHash(spec.packageName) + "-" + formatRandomHex(8)
}

func formatIdentityHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:16])
}

// stagedRenameError is a staged root or file that could not be renamed into
// its destination: the one install failure that staging somewhere else can
// cure.
type stagedRenameError struct {
	err error
}

func (e *stagedRenameError) Error() string { return e.err.Error() }

func (e *stagedRenameError) Unwrap() error { return e.err }

// isStagedRenameFailure reports whether an install failed at its final rename.
func isStagedRenameFailure(err error) bool {
	var renameErr *stagedRenameError
	return errors.As(err, &renameErr)
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
