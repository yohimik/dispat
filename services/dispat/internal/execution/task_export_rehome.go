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

// rehomeVerifiedTaskExports turns only structured declared-path exports into
// paths in this attempt's private checkout. The exact producer Result and
// manifest were direct Assignment carriers and have already been validated
// and installed; a literal string is never reinterpreted as a path token.
func rehomeVerifiedTaskExports(assignment Assignment, checkout *Checkout,
	proofs []verifiedTaskInput) (Assignment, error) {
	if len(assignment.Exports) == 0 {
		return assignment, nil
	}
	copy := assignment
	copy.Exports = append([]ExportedValue(nil), assignment.Exports...)
	for index, exported := range copy.Exports {
		ref := exported.DeclaredPath
		if ref == nil {
			continue
		}
		if exported.Value != "" || ref.Package == "" {
			return Assignment{}, fmt.Errorf("execution: typed prior export conflicts with a literal value or identity")
		}
		var sourceRoot string
		var matched bool
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
			if matched && sourceRoot != value {
				return Assignment{}, fmt.Errorf("execution: typed prior export has ambiguous verified inputs")
			}
			sourceRoot, matched = value, true
		}
		if !matched {
			return Assignment{}, fmt.Errorf("execution: typed prior export has no matching installed output")
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
		if repository.Path == "." || repository.Path == "" {
			return true
		}
		if inputPath == repository.Path || strings.HasPrefix(inputPath, repository.Path+"/") {
			return true
		}
	}
	return false
}

func manifestContainsDeclaredPath(manifest *OutputManifest, root, relative string) bool {
	if manifest == nil {
		return false
	}
	wanted := root
	if relative != "." && relative != "" {
		wanted = path.Join(root, relative)
	}
	for _, entry := range manifest.Entries {
		if entry.Path == wanted || (relative == "." || relative == "") &&
			strings.HasPrefix(entry.Path, wanted+"/") {
			return true
		}
	}
	return false
}
