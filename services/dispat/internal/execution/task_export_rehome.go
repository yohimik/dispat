// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	verifiedTaskExportMissingValue          = ""
	verifiedTaskExportNoExports             = 0
	verifiedTaskExportRepositoryRoot        = "."
	verifiedTaskExportPathSeparator         = "/"
	verifiedTaskExportMatched               = true
	verifiedTaskExportUnmatched             = false
	verifiedTaskExportIdentityConflictError = "execution: typed prior export conflicts with a literal value or identity"
	verifiedTaskExportAmbiguousInputError   = "execution: typed prior export has ambiguous verified inputs"
	verifiedTaskExportMissingOutputError    = "execution: typed prior export has no matching installed output"
)

// rehomeVerifiedTaskExports turns only structured declared-path exports into
// paths in this attempt's private checkout. The exact producer Result and
// manifest were direct Assignment carriers and have already been validated
// and installed; a literal string is never reinterpreted as a path token.
func rehomeVerifiedTaskExports(assignment Assignment, checkout *Checkout,
	proofs []verifiedTaskInput) (Assignment, error) {
	if len(assignment.Exports) == verifiedTaskExportNoExports {
		return assignment, nil
	}
	copy := assignment
	copy.Exports = append([]ExportedValue(nil), assignment.Exports...)
	for index, exported := range copy.Exports {
		ref := exported.DeclaredPath
		if ref == nil {
			continue
		}
		if exported.Value != verifiedTaskExportMissingValue || ref.Package == verifiedTaskExportMissingValue {
			return Assignment{}, fmt.Errorf(verifiedTaskExportIdentityConflictError)
		}
		var sourceRoot string
		var matched bool
		var matchedInput AssignmentInput
		preferredPath := verifiedTaskExportMissingValue
		if assignment.Package != nil && ref.Package == assignment.Package.Name &&
			ref.Repository == assignment.Package.Repository {
			preferredPath = path.Join(ownerPathOf(assignment), assignment.Package.Dir)
		}
		for _, proof := range proofs {
			if proof.input.Package != ref.Package || proof.manifest == nil ||
				!inputBelongsToRepository(proof.input.Path, ref.Repository, assignment.Repositories) {
				continue
			}
			candidate := checkout.Dir(proof.input.Path)
			value, err := release.RehomeDeclaredOutputPath(release.DeclaredOutputPath{
				Root: ref.Root, Relative: ref.Relative}, candidate, proof.manifest.Roots)
			if err != nil || !manifestContainsDeclaredPath(proof.manifest, ref.Root, ref.Relative) {
				continue
			}
			if _, err := os.Lstat(value); err != nil {
				continue
			}
			if matched && (matchedInput.Task != proof.input.Task ||
				matchedInput.Commit != proof.input.Commit || matchedInput.Digest != proof.input.Digest) {
				return Assignment{}, fmt.Errorf(verifiedTaskExportAmbiguousInputError)
			}
			// The same signed Result can be installed at both the repository
			// input root and this package's own output folder. Both copies
			// have identical proved bytes; own-stage exports name the latter.
			if !matched || proof.input.Path == preferredPath {
				sourceRoot = value
			}
			matchedInput, matched = proof.input, verifiedTaskExportMatched
		}
		if !matched {
			return Assignment{}, fmt.Errorf(verifiedTaskExportMissingOutputError)
		}
		copy.Exports[index].Value = sourceRoot
		copy.Exports[index].DeclaredPath = nil
	}
	return copy, nil
}

func inputBelongsToRepository(inputPath, name string, repositories []AssignmentRepository) bool {
	for _, repository := range repositories {
		if repository.Name != name {
			continue
		}
		if repository.Path == verifiedTaskExportRepositoryRoot || repository.Path == verifiedTaskExportMissingValue {
			return verifiedTaskExportMatched
		}
		if inputPath == repository.Path || strings.HasPrefix(inputPath, repository.Path+verifiedTaskExportPathSeparator) {
			return verifiedTaskExportMatched
		}
	}
	return verifiedTaskExportUnmatched
}

func manifestContainsDeclaredPath(manifest *OutputManifest, root, relative string) bool {
	if manifest == nil {
		return verifiedTaskExportUnmatched
	}
	wanted := root
	if relative != verifiedTaskExportRepositoryRoot && relative != verifiedTaskExportMissingValue {
		wanted = path.Join(root, relative)
	}
	for _, entry := range manifest.Entries {
		if entry.Path == wanted || (relative == verifiedTaskExportRepositoryRoot || relative == verifiedTaskExportMissingValue) &&
			strings.HasPrefix(entry.Path, wanted+verifiedTaskExportPathSeparator) {
			return verifiedTaskExportMatched
		}
	}
	return verifiedTaskExportUnmatched
}
