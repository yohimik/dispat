// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentAdmittedOutputRootRequiresExactVerifiedTree(t *testing.T) {
	fixture := newOutputFixture(t, "packages/docs")
	fixture.write(t, "dist/from-assets.txt", "signed output\n", 0o644)
	manifest, err := fixture.capture(t, []string{"dist"}, TransferLimits{
		MaxFiles: 8, MaxBytes: 1024, MaxManifestBytes: 4096})
	require.NoError(t, err)
	coordinator := &Coordinator{outputs: newOutputRegistry()}
	coordinator.outputs.rememberStage("docs", "build", &admittedOutputs{
		store: fixture.git, manifest: newAdmittedManifest(manifest)})
	fixture.run(t, "add", "-f", "packages/docs/dist/from-assets.txt")
	fixture.run(t, "commit", "-q", "-m", "installed admitted output")
	check := AdmittedOutputRootCheck{Package: "docs", Root: "dist",
		RepositoryPath: "packages/docs/dist", CurrentRepository: fixture.git,
		CurrentSnapshot: fixture.run(t, "rev-parse", "HEAD")}
	matched, err := coordinator.IsCurrentAdmittedOutputRoot(t.Context(), check)
	require.NoError(t, err)
	require.True(t, matched)

	fixture.write(t, "dist/from-assets.txt", "changed after admission\n", 0o644)
	fixture.run(t, "add", "-f", "packages/docs/dist/from-assets.txt")
	fixture.run(t, "commit", "-q", "-m", "native output mutation")
	check.CurrentSnapshot = fixture.run(t, "rev-parse", "HEAD")
	matched, err = coordinator.IsCurrentAdmittedOutputRoot(t.Context(), check)
	require.NoError(t, err)
	require.False(t, matched, "a later write cannot borrow the signed output exception")
}
