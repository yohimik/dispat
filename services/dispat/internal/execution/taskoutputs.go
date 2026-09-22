// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The two halves of a task that are not commands: getting the providers'
// verified outputs in place before anything starts, and describing what this
// task produced before anything is reported (CCME §28.5).
//
// Both are refusals rather than best efforts. A prerequisite whose bytes
// cannot be retrieved and verified fails the task with no command run at all,
// because a build that started anyway would be a build against whatever the
// checkout happened to hold; and a build that did not produce what its
// configuration promises fails too, because its consumers would otherwise be
// told to use an output set that is not there.
//
// A digest, a file name or a branch reference is not a transfer. Everything
// an assignment names has to arrive as bytes this node fetched, listed and
// hashed, or the task fails.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// installTaskInputs fetches, verifies and installs every provider output set
// this assignment names, and answers what went in.
//
// The order is fetch, verify, install, per input, and the whole of it happens
// before the frame: §28.5 puts the atomicity at the task-input boundary, so a
// command may only start once every root of every input is in place.
func (w *Worker) installTaskInputs(ctx context.Context, assignment Assignment, checkout *Checkout,
	dir string, log zerolog.Logger) ([]ManifestInput, OutputReason, error) {
	if len(assignment.Inputs) == 0 {
		return nil, "", nil
	}
	if err := w.Mailbox.Fetch(ctx, formatInputBranches(assignment.Inputs)); err != nil {
		return nil, ReasonBytesMissing, err
	}
	installed := make([]ManifestInput, 0, len(assignment.Inputs))
	for _, input := range assignment.Inputs {
		manifest, reason, err := w.readInputManifest(ctx, assignment, input)
		if err != nil {
			return nil, reason, err
		}
		if err := InstallOutputs(ctx, InstallRequest{
			Git: w.Cache, Manifest: manifest, Dir: checkout.Dir(input.Path),
			Staging: filepath.Join(dir, stagingDirName, formatPathWord(input.Package)), Log: log,
		}); err != nil {
			return nil, OutputFaultReason(err), err
		}
		log.Debug().Str("package", input.Package).Str("commit", input.Commit).
			Int("files", manifest.Files).Int64("bytes", manifest.Bytes).Msg("inputs installed")
		installed = append(installed, ManifestInput{
			Package: input.Package, OutputTree: manifest.OutputTree, Digest: manifest.Digest,
		})
	}
	return installed, "", nil
}

// formatInputBranches is the branches one task's inputs are fetchable from,
// each named once, so that two inputs of one provider cost one fetch.
func formatInputBranches(inputs []AssignmentInput) []string {
	branches := make([]string, 0, len(inputs))
	named := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		if input.Branch == "" || named[input.Branch] {
			continue
		}
		named[input.Branch] = true
		branches = append(branches, input.Branch)
	}
	return branches
}

// readInputManifest reads the provider's result at the exact object the
// assignment named and answers the output manifest it carries.
//
// Three things are proven before anything is believed. The object is on the
// branch that was fetched, so a tip somebody moved cannot change what this
// task consumes; the result is signed with this run's secret; and the manifest
// is the one the orchestrator admitted, which is what the digest in the
// assignment is for. The roots the entries are held to are the manifest's own,
// and that is sound precisely because the digest covers them: the orchestrator
// compared them with the plan before it named this manifest.
func (w *Worker) readInputManifest(ctx context.Context, assignment Assignment,
	input AssignmentInput) (*OutputManifest, OutputReason, error) {
	if input.Digest == "" {
		return nil, ReasonOutputDigest, fmt.Errorf(
			"execution: the assignment names the outputs of %s without a manifest digest", input.Package)
	}
	tip, err := w.Mailbox.Inspect(ctx, gitx.RemoteHead{Name: input.Branch, OID: input.Commit})
	if err != nil {
		return nil, ReasonBytesMissing, fmt.Errorf(
			"execution: reading the outputs of %s: %w", input.Package, err)
	}
	if tip.Kind != MessageResult {
		return nil, ReasonBytesMissing, fmt.Errorf(
			"execution: the object named as the outputs of %s carries no result", input.Package)
	}
	document, err := w.Mailbox.Read(ctx, tip, assignment.Limits.MaxManifestBytes)
	if err != nil {
		return nil, ReasonOutputIdentity, fmt.Errorf(
			"execution: reading the outputs of %s: %w", input.Package, err)
	}
	var result Result
	if err := json.Unmarshal(document, &result); err != nil {
		return nil, ReasonOutputIdentity, fmt.Errorf(
			"execution: reading the outputs of %s: %w", input.Package, err)
	}
	if result.Outputs == nil {
		return nil, ReasonBytesMissing, fmt.Errorf(
			"execution: the result of %s describes no outputs", input.Package)
	}
	if _, err := ValidateOutputs(ctx, w.Cache, result.Outputs, OutputExpectation{
		Run: assignment.Run, PlanDigest: assignment.PlanDigest, Generation: assignment.Generation,
		Task: input.Task, Roots: result.Outputs.Roots, Platforms: assignment.Platforms,
		Digest: input.Digest, Limits: assignment.Limits,
	}); err != nil {
		return nil, OutputFaultReason(err), fmt.Errorf(
			"execution: the outputs of %s cannot be used: %w", input.Package, err)
	}
	return result.Outputs, "", nil
}

// captureTaskOutputs describes what a successful frame produced, and turns a
// build that did not produce it into a failed task.
//
// The capture happens inside the task rather than beside it because the files
// exist only while the checkout does: the folder is removed the moment the
// task ends, so a set described afterwards would be a set described from
// nothing.
func (w *Worker) captureTaskOutputs(ctx context.Context, assignment Assignment, checkout *Checkout,
	installed []ManifestInput, outcome taskOutcome, log zerolog.Logger) taskOutcome {
	if len(assignment.Outputs) == 0 {
		return outcome
	}
	owner := ownerPathOf(assignment)
	packagePath := resolveCapturePath(assignment)
	manifest, err := CaptureOutputs(ctx, CaptureRequest{
		Git:               &gitx.LocalGitx{Dir: checkout.Dir(owner), Log: log},
		Dir:               checkout.Dir(owner, packagePath),
		PackagePath:       packagePath,
		Roots:             assignment.Outputs,
		Limits:            assignment.Limits,
		IsAbsentRootEmpty: assignment.Kind == KindRun,
		Manifest: OutputManifest{
			Run: assignment.Run, PlanDigest: assignment.PlanDigest, Task: assignment.Task,
			Attempt: assignment.Attempt, Generation: assignment.Generation, Node: w.Node,
			Package: assignment.Package.Name, Version: assignment.Package.Version,
			Repositories: formatManifestRepositories(assignment.Repositories),
			Platform:     Platform{OS: w.Report.OS, Arch: w.Report.Arch, Dispat: w.Report.Dispat},
			Inputs:       installed,
		},
	})
	if err != nil {
		log.Warn().Err(err).Str("reason", string(OutputFaultReason(err))).
			Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the task's declared outputs could not be captured")
		outcome.status, outcome.failedPart = StatusFailed, release.PartOutputs
		outcome.reason = OutputFaultReason(err)
		return outcome
	}
	if err := checkManifestSize(manifest, assignment.Limits); err != nil {
		log.Warn().Err(err).Str("reason", string(ReasonManifestOversize)).
			Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the task's declared outputs could not be described within the run's ceiling")
		outcome.status, outcome.failedPart = StatusFailed, release.PartOutputs
		outcome.reason = ReasonManifestOversize
		return outcome
	}
	log.Debug().Int("files", manifest.Files).Int64("bytes", manifest.Bytes).
		Str("outputTree", manifest.OutputTree).Msg("outputs captured")
	outcome.outputs = manifest
	return outcome
}

// resolveCapturePath is the folder one task's declared roots are relative to,
// inside the repository that owns the package: the package folder for a build,
// and the repository root for a sweep task, whose roots are shared by every
// package of the sweep (§28.10).
func resolveCapturePath(assignment Assignment) string {
	if assignment.Kind == KindRun {
		return ""
	}
	return assignment.Package.Dir
}

// checkManifestSize refuses a description too large for the ceiling the run
// holds it to, on the node that wrote it.
//
// Here rather than only at the reader, because the reader's refusal would be a
// document it could not read at all: a node that reported an oversized
// manifest would look exactly like a node that never answered, and the run
// would wait out the task deadline to find out. Refusing it where it is
// produced turns that into one sentence naming the ceiling.
func checkManifestSize(manifest *OutputManifest, limits TransferLimits) error {
	if limits.MaxManifestBytes <= 0 {
		return nil
	}
	document, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("execution: writing the output manifest: %w", err)
	}
	if int64(len(document)) <= limits.MaxManifestBytes {
		return nil
	}
	return fmt.Errorf("execution: the manifest of %d files is %d bytes and the run allows %d: %w",
		manifest.Files, len(document), limits.MaxManifestBytes, refuseOutputs(ReasonManifestOversize))
}

// formatManifestRepositories is the prepared input states a build consumed, as
// a manifest names them: the repository and the exact commit, without the
// branch the state travelled on, which is transport rather than provenance.
func formatManifestRepositories(repositories []AssignmentRepository) []ManifestRepository {
	states := make([]ManifestRepository, 0, len(repositories))
	for _, repository := range repositories {
		states = append(states, ManifestRepository{Name: repository.Name, Snapshot: repository.Snapshot})
	}
	return states
}
