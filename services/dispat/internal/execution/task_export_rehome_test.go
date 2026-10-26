// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypedPriorExportRequiresInstalledSignedProviderFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "coverage", "core.asset")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte("verified"), 0o600))
	checkout := &Checkout{root: root}
	assignment := Assignment{Repositories: []AssignmentRepository{{Name: "source", Path: "src"}},
		Exports: []ExportedValue{
			{Name: "CORE_ASSET", DeclaredPath: &ExportedDeclaredPath{
				Repository: "source", Package: "core", Root: "coverage", Relative: "core.asset"}},
			{Name: "LITERAL", Value: "dispat-output://coverage/core.asset"},
		}}
	proofs := []verifiedTaskInput{{input: AssignmentInput{Package: "core", Path: "src"},
		manifest: &OutputManifest{Roots: []string{"coverage"},
			Entries: []ManifestEntry{{Path: "coverage/core.asset"}}}}}
	rehomed, err := rehomeVerifiedTaskExports(assignment, checkout, proofs)
	require.NoError(t, err)
	require.Equal(t, file, rehomed.Exports[0].Value)
	require.Nil(t, rehomed.Exports[0].DeclaredPath)
	require.Equal(t, "dispat-output://coverage/core.asset", rehomed.Exports[1].Value)
	require.Equal(t, "", assignment.Exports[0].Value, "the signed assignment remains immutable")

	for _, mutate := range []func(*Assignment){
		func(a *Assignment) { a.Exports[0].DeclaredPath.Repository = "other" },
		func(a *Assignment) { a.Exports[0].DeclaredPath.Relative = "../secret" },
		func(a *Assignment) { a.Exports[0].Value = "literal-and-typed" },
	} {
		copy := assignment
		copy.Exports = append([]ExportedValue(nil), assignment.Exports...)
		declared := *copy.Exports[0].DeclaredPath
		copy.Exports[0].DeclaredPath = &declared
		mutate(&copy)
		_, err := rehomeVerifiedTaskExports(copy, checkout, proofs)
		require.Error(t, err)
	}
	require.NoError(t, os.Remove(file))
	_, err = rehomeVerifiedTaskExports(assignment, checkout, proofs)
	require.ErrorContains(t, err, "no matching installed output")

	mono := assignment
	mono.Repositories = []AssignmentRepository{{Name: "", Path: "src"}}
	mono.Exports = []ExportedValue{{Name: "OUTPUT_DIR", DeclaredPath: &ExportedDeclaredPath{
		Repository: "", Package: "core", Root: "coverage", Relative: ""}}}
	rootValue, err := rehomeVerifiedTaskExports(mono, checkout, proofs)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "src", "coverage"), rootValue.Exports[0].Value)
	mono.Repositories[0].Name = "different"
	_, err = rehomeVerifiedTaskExports(mono, checkout, proofs)
	require.ErrorContains(t, err, "no matching installed output")
}
