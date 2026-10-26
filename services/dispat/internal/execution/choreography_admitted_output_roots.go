// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	admittedOutputRootMissingIdentity = ""
	admittedOutputRootNoMatch         = false
	admittedOutputRootExactMatch      = true
	admittedOutputRootInvalidCheck    = "execution: admitted output root check has incomplete identity"
	admittedOutputRootMissingStore    = "execution: admitted output root has no verified object store"
)

// AdmittedOutputRootCheck compares a captured first-node root against the
// verified output tree installed from a signed Result. RepositoryPath is
// relative to the owner repository; Root is relative to the package folder.
type AdmittedOutputRootCheck struct {
	Package           string
	Root              string
	RepositoryPath    string
	CurrentSnapshot   string
	CurrentRepository *gitx.LocalGitx
}

// IsCurrentAdmittedOutputRoot recognizes only the exact bytes and tree mode
// of a verified admitted output root. A later native write or hook edit makes
// the current tree differ and remains a relevant input change.
func (c *Coordinator) IsCurrentAdmittedOutputRoot(ctx context.Context,
	check AdmittedOutputRootCheck) (bool, error) {
	if c == nil || c.outputs == nil || check.Package == admittedOutputRootMissingIdentity ||
		check.Root == admittedOutputRootMissingIdentity ||
		check.RepositoryPath == admittedOutputRootMissingIdentity ||
		check.CurrentSnapshot == admittedOutputRootMissingIdentity || check.CurrentRepository == nil {
		return admittedOutputRootNoMatch, fmt.Errorf(admittedOutputRootInvalidCheck)
	}
	admitted := c.outputs.findLatest(check.Package)
	if admitted == nil {
		return admittedOutputRootNoMatch, nil
	}
	if admitted.store == nil || admitted.manifest.outputTree == admittedOutputRootMissingIdentity {
		return admittedOutputRootNoMatch, fmt.Errorf(admittedOutputRootMissingStore)
	}
	expectedStore := gitx.NewPlumbing(admitted.store)
	expected, expectedFound := expectedStore.TreeEntryAtPath(ctx, admitted.manifest.outputTree, check.Root)
	if err := expectedStore.Err(); err != nil {
		return admittedOutputRootNoMatch, err
	}
	currentStore := gitx.NewPlumbing(check.CurrentRepository)
	current, currentFound := currentStore.TreeEntryAtPath(ctx, check.CurrentSnapshot, check.RepositoryPath)
	if err := currentStore.Err(); err != nil {
		return admittedOutputRootNoMatch, err
	}
	return expectedFound == currentFound &&
		(!expectedFound || expected.Mode == current.Mode && expected.OID == current.OID), nil
}
