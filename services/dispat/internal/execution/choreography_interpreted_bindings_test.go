// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const interpretedBindingTestLimit = 1 << 20

func TestInterpretedBindingsReconstructFromSignedSourcesAndPrivateRequest(t *testing.T) {
	fixture := newMailboxFixture(t)
	ctx := t.Context()
	root, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"root":true}`), nil, nil)
	require.NoError(t, err)
	head, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"head":true}`), nil, nil)
	require.NoError(t, err)
	snapshot, err := fixture.mailbox.commitMessage(ctx, choreographyBootstrapKind,
		[]byte(`{"snapshot":true}`), []string{head}, nil)
	require.NoError(t, err)
	binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	request := release.StageRequest{TaskID: "core:build", Dir: fixture.git.Dir,
		Release: &plan.Release{Pkg: &model.Package{Name: "core", Repository: "repo"}},
		Env:     []string{"DISPAT_VALUE=private"}}
	sources := []Source{{Name: "repo", Path: ".", Dir: request.Dir, Head: head}}
	producer := Coordinator{dispatch: Dispatch{Store: fixture.git}}
	repositories, err := producer.carrySourceInputs(ctx, sources, []string{snapshot})
	require.NoError(t, err)
	prepared := ChoreographyPreparedCarrier{ChoreographyRunBinding: binding,
		GraphRootOID: root, Task: request.TaskID, TemplateDigest: "template",
		Bindings: ChoreographyTaskBindings{Repositories: repositories,
			Env: []string{"DISPAT_VALUE=private"}}}
	preparedOID, err := fixture.mailbox.PrepareChoreographyPreparedCarrier(ctx, prepared,
		interpretedBindingTestLimit)
	require.NoError(t, err)
	options := ChoreographyInterpretedBindingOptions{Request: request,
		PreparedOID: preparedOID,
		State:       ChoreographyVerifiedBindingState{PreparedInputOID: preparedOID},
		Sources:     sources, Mailbox: fixture.mailbox, Store: fixture.git,
		Binding: binding, GraphRootOID: root, TemplateDigest: "template",
		MaxBytes: interpretedBindingTestLimit}
	bindings, err := ResolveInterpretedChoreographyBindings(ctx, options)
	require.NoError(t, err)
	require.Equal(t, preparedOID, bindings.PreparedInputOID)
	require.Equal(t, request.Env, bindings.Env)
	require.Equal(t, prepared.Bindings.Repositories, bindings.Repositories)

	changed := options
	changed.Request.Env = []string{"DISPAT_VALUE=forged"}
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, invalidChoreographyPreparedCarrierError)
	changed = options
	changed.Sources = []Source{{Name: "other", Path: ".", Dir: request.Dir, Head: head}}
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, invalidChoreographyPreparedCarrierError)

	forged := prepared
	forged.Bindings.Repositories[0].Path = "other"
	data, err := json.Marshal(forged)
	require.NoError(t, err)
	forgedOID, err := fixture.mailbox.commitMessage(ctx, choreographyPreparedCarrierKind,
		data, []string{root, snapshot}, nil)
	require.NoError(t, err)
	changed = options
	changed.PreparedOID = forgedOID
	changed.State.PreparedInputOID = forgedOID
	_, err = ResolveInterpretedChoreographyBindings(ctx, changed)
	require.ErrorContains(t, err, invalidChoreographyPreparedCarrierError)
}
