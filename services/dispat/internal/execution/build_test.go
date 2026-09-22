// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What the orchestrator believes about a reply, and where it tells a node to
// run a package's commands.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// offeredAttempt is one dispatched task as the watcher holds it: the exact
// assignment object this run created, and the document it was created from.
func offeredAttempt(branch string) *attemptState {
	header := Header{
		Protocol: ProtocolVersion, Kind: KindBuild, Run: "run-1", PlanDigest: "digest-1",
		Task: "core:build", Attempt: 1, Generation: "generation-1", Node: "build-a",
		Branch: branch, IssuedAt: time.Now().UTC().Format(time.RFC3339),
	}
	return &attemptState{offered: "assignment-oid", assignment: &Assignment{Header: header}}
}

// authenticResult is the reply that node would write, which every row below
// varies from.
func authenticResult(branch string) Result {
	waiting := offeredAttempt(branch)
	return Result{
		Header:     waiting.assignment.Header,
		Assignment: waiting.offered,
		Status:     StatusSucceeded,
	}
}

// TestResultsThisRunDoesNotBelieve: a reply is this attempt's only when every
// binding matches. Each row is a reply that is authentic in every other way,
// which is what an attacker with push access to a mailbox can produce.
func TestResultsThisRunDoesNotBelieve(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-build-cafe"
	for name, tc := range map[string]struct {
		forge func(*Result)
		tip   ChainTip
		want  RejectReason
	}{
		"a reply naming another assignment": {
			forge: func(r *Result) { r.Assignment = "another-oid" }, want: ReasonReplay},
		"a reply of another run": {
			forge: func(r *Result) { r.Run = "run-2" }, want: ReasonReplay},
		"a reply for another task": {
			forge: func(r *Result) { r.Task = "other:build" }, want: ReasonReplay},
		"a reply of another attempt": {
			forge: func(r *Result) { r.Attempt = 2 }, want: ReasonReplay},
		"a reply from an earlier ownership": {
			forge: func(r *Result) { r.Generation = "generation-0" }, want: ReasonReplay},
		"a reply belonging to another plan": {
			forge: func(r *Result) { r.PlanDigest = "digest-0" }, want: ReasonReplay},
		"a reply addressed to another node": {
			forge: func(r *Result) { r.Node = "build-b" }, want: ReasonNode},
		"a reply copied onto this branch": {
			forge: func(r *Result) { r.Branch = "dispat-worker-build-a-20260921-build-beef" },
			want:  ReasonBranch},
		"a reply of a protocol this run does not speak": {
			forge: func(r *Result) { r.Protocol = ProtocolVersion + 1 }, want: ReasonProtocol},
		"a reply outside the replay window": {
			forge: func(r *Result) { r.IssuedAt = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339) },
			want:  ReasonIssuedAt},
		"a reply written where no worker could have written one": {
			forge: func(*Result) {},
			tip:   ChainTip{Branch: branch, Kind: MessageResult, Previous: MessageAssignment},
			want:  ReasonChain},
	} {
		t.Run(name, func(t *testing.T) {
			result := authenticResult(branch)
			tc.forge(&result)
			tip := tc.tip
			if tip.Branch == "" {
				tip = ChainTip{Branch: branch, Kind: MessageResult, Previous: MessageClaim}
			}

			assert.Equal(t, tc.want, checkTaskResult(result, "build-a", tip, offeredAttempt(branch)))
		})
	}
}

// TestAnAuthenticResultIsAccepted keeps the table above honest: the reply it
// varies from is one this run acts on.
func TestAnAuthenticResultIsAccepted(t *testing.T) {
	branch := "dispat-worker-build-a-20260921-build-cafe"
	tip := ChainTip{Branch: branch, Kind: MessageResult, Previous: MessageClaim}

	assert.Empty(t, checkTaskResult(authenticResult(branch), "build-a", tip, offeredAttempt(branch)))
}

// TestPackageDirIsResolvedAgainstItsOwnRepository: what travels is where the
// commands run inside the checkout, which is the package folder relative to
// the repository that owns it and never an orchestrator's path.
func TestPackageDirIsResolvedAgainstItsOwnRepository(t *testing.T) {
	sources := []Source{
		{Name: "sdk", Path: "sdk", Dir: filepath.FromSlash("/work/app/sdk")},
		{Name: "app", Path: ".", Dir: filepath.FromSlash("/work/app")},
	}
	request := release.StageRequest{
		Release: &plan.Release{Pkg: &model.Package{Name: "core", Repository: "sdk"}},
		Dir:     filepath.FromSlash("/work/app/sdk/packages/core"),
	}

	dir, err := resolvePackageDir(sources, request)

	require.NoError(t, err)
	assert.Equal(t, "packages/core", dir)
}

// TestPackageDirWithoutItsRepositoryIsRefused: a closure that does not name
// the repository the package lives in is a dispatch nothing could execute, so
// it is refused here rather than as a folder that does not exist on a node.
func TestPackageDirWithoutItsRepositoryIsRefused(t *testing.T) {
	request := release.StageRequest{
		Release: &plan.Release{Pkg: &model.Package{Name: "core", Repository: "sdk"}},
		Dir:     filepath.FromSlash("/work/app/sdk/packages/core"),
	}

	_, err := resolvePackageDir([]Source{{Name: "app", Path: "."}}, request)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sdk")
}

// TestSingleHistoryPackageDir: a repository that is not composed has one
// unnamed source, and the package folder is relative to it.
func TestSingleHistoryPackageDir(t *testing.T) {
	request := release.StageRequest{
		Release: &plan.Release{Pkg: &model.Package{Name: "core"}},
		Dir:     filepath.FromSlash("/work/app/packages/core"),
	}

	dir, err := resolvePackageDir([]Source{{Path: ".", Dir: filepath.FromSlash("/work/app")}}, request)

	require.NoError(t, err)
	assert.Equal(t, "packages/core", dir)
}

// TestOutputsOfAPlacedAgainTaskAreAdmittedUnderItsOwnAttempt: a task whose
// first assignment queued unclaimed is placed again as its second attempt, and
// the node binds the outputs it captures to that attempt. The orchestrator
// used to hold every admitted set to attempt 1, so the build that finally ran
// was refused for carrying its own attempt, and its package failed although
// nothing about it was wrong.
func TestOutputsOfAPlacedAgainTaskAreAdmittedUnderItsOwnAttempt(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "built\n", 0o644)
	manifest, err := CaptureOutputs(t.Context(), CaptureRequest{
		Git: fixture.git, Dir: fixture.pkgDir, PackagePath: fixture.pkgPath, Roots: []string{"dist"},
		Limits: testLimits,
		Manifest: OutputManifest{Run: "run-1", PlanDigest: "digest", Task: "core:build", Attempt: 2,
			Generation: "generation", Node: "build-a", Package: "core",
			Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"}},
	})
	require.NoError(t, err)
	coordinator := &Coordinator{Run: "run-1", PlanDigest: "digest", Generation: "generation",
		Limits: testLimits, outputs: newOutputRegistry()}
	request := release.StageRequest{Release: &plan.Release{
		Pkg: &model.Package{Name: "core", Dir: fixture.pkgDir, Space: &model.Space{BuildOutputs: []string{"dist"}}},
	}}

	err = coordinator.admitOutputs(t.Context(), "core:build", producedOutputs{
		node: "build-a", store: fixture.git, manifest: manifest, attempt: 2, isInstalledHere: true,
	}, request)

	require.NoError(t, err, "the set of attempt 2 is admitted as attempt 2's")
	require.NotNil(t, coordinator.outputs.find("core"))
	err = coordinator.admitOutputs(t.Context(), "core:build", producedOutputs{
		node: "build-a", store: fixture.git, manifest: manifest, attempt: 1, isInstalledHere: true,
	}, request)
	assert.Equal(t, ReasonOutputIdentity, OutputFaultReason(err),
		"and a set bound to another attempt than the one that answered is still refused")
}
