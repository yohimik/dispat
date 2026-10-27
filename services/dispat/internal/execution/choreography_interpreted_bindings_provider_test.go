// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

func TestInterpretedBindingsCarryPreparedProviderAcrossPrivateRepositories(t *testing.T) {
	fixture := newMailboxFixture(t)
	ctx := t.Context()
	root, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"root":true}`), nil, nil)
	require.NoError(t, err)
	coreHead, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"core":true}`), nil, nil)
	require.NoError(t, err)
	assetsHead, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"assets":true}`), nil, nil)
	require.NoError(t, err)
	coreSnapshot, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"coreSnapshot":true}`), []string{coreHead}, nil)
	require.NoError(t, err)
	assetsSnapshot, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"assetsSnapshot":true}`), []string{assetsHead}, nil)
	require.NoError(t, err)
	providerResult, err := fixture.mailbox.commitMessage(ctx, MessageResult,
		[]byte(`{"status":"succeeded"}`), nil, nil)
	require.NoError(t, err)
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	request := release.StageRequest{TaskID: "ui:build", Dir: fixture.git.Dir,
		Release:    &plan.Release{Pkg: &model.Package{Name: "ui", Repository: "core"}},
		Descriptor: release.Stage{Inputs: release.InputsProviderOutputs},
		Env:        []string{"DISPAT_UI=private"}}
	sources := []Source{{Name: "core", Path: ".", Dir: fixture.git.Dir, Head: coreHead},
		{Name: "assets", Path: "packages/assets", Dir: fixture.git.Dir, Head: assetsHead}}
	producer := Coordinator{dispatch: Dispatch{Store: fixture.git}}
	repositories, err := producer.carrySourceInputs(ctx, sources, []string{coreSnapshot, assetsSnapshot})
	require.NoError(t, err)
	provider := ChoreographyVerifiedOutput{Task: "assets:prepare", Package: "assets",
		ResultOID: providerResult, Manifest: &OutputManifest{Digest: "signed-manifest"}}
	input := AssignmentInput{Task: provider.Task, Package: provider.Package,
		Commit: provider.ResultOID, Digest: provider.Manifest.Digest, Path: "packages/assets"}
	prepared := ChoreographyPreparedCarrier{ChoreographyRunBinding: binding,
		GraphRootOID: root, Task: request.TaskID, TemplateDigest: "template",
		PredecessorResultOIDs: []string{providerResult},
		Bindings: ChoreographyTaskBindings{Repositories: repositories,
			Inputs: []AssignmentInput{input}, Env: request.Env,
			PredecessorResultOIDs: []string{providerResult}}}
	preparedOID, err := fixture.mailbox.PrepareChoreographyPreparedCarrier(ctx, prepared,
		interpretedBindingTestLimit)
	require.NoError(t, err)
	options := ChoreographyInterpretedBindingOptions{Request: request,
		PreparedOID: preparedOID,
		State: ChoreographyVerifiedBindingState{PreparedInputOID: preparedOID,
			PredecessorTaskOIDs:     []string{providerResult},
			VerifiedOutputByPackage: map[string]ChoreographyVerifiedOutput{"assets": provider}},
		Sources: sources, InputProviders: []InputPackage{{Package: "assets", Path: "packages/assets", IsPrepared: true}},
		Mailbox: fixture.mailbox, Store: fixture.git, Binding: binding,
		GraphRootOID: root, TemplateDigest: "template", MaxBytes: interpretedBindingTestLimit}
	bindings, err := ResolveInterpretedChoreographyBindings(ctx, options)
	require.NoError(t, err)
	require.Equal(t, []AssignmentInput{input}, bindings.Inputs)
	require.Equal(t, repositories, bindings.Repositories)

	changed := options
	changed.InputProviders = []InputPackage{{Package: "assets", Path: "wrong", IsPrepared: true}}
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, interpretedBindingComputedMismatchReason)
	changed = options
	changed.State.VerifiedOutputByPackage = map[string]ChoreographyVerifiedOutput{}
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, "assets")
	changed = options
	changed.Sources = append([]Source(nil), sources...)
	changed.Sources[1].Path = "other-assets"
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, interpretedBindingSourceLayoutMismatchReason)
}
