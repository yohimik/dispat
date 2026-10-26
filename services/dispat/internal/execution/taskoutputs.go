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
func (w *Worker) installTaskInputs(ctx context.Context, assignmentOID string,
	assignment Assignment, checkout *Checkout,
	dir string, log zerolog.Logger) ([]ManifestInput, []verifiedTaskInput, OutputReason, error) {
	if len(assignment.Inputs) == 0 {
		return nil, nil, "", nil
	}
	carrier, ok := w.Mailbox.(slotMailboxx)
	if !ok {
		return nil, nil, ReasonBytesMissing, fmt.Errorf("execution: this mailbox cannot verify carried results")
	}
	view, err := carrier.OpenCarriedAssignment(ctx, assignmentOID, assignment.Limits.MaxManifestBytes)
	if err != nil {
		return nil, nil, ReasonOutputIdentity, fmt.Errorf("execution: verifying carried results: %w", err)
	}
	installed := make([]ManifestInput, 0, len(assignment.Inputs))
	proofs := make([]verifiedTaskInput, 0, len(assignment.Inputs))
	type inputGroup struct {
		name     string
		requests []InstallRequest
	}
	groups := make(map[string]*inputGroup)
	var ordered []*inputGroup
	for _, input := range assignment.Inputs {
		manifest, validated, reason, err := w.readInputManifest(ctx, view, assignment, input)
		if err != nil {
			return nil, nil, reason, err
		}
		key := input.Package + "\x00" + input.Path
		group := groups[key]
		if group == nil {
			group = &inputGroup{name: input.Package}
			groups[key] = group
			ordered = append(ordered, group)
		}
		group.requests = append(group.requests, InstallRequest{
			Git: w.Cache, Manifest: manifest, Objects: validated.Objects, Dir: checkout.Dir(input.Path),
			Staging: filepath.Join(dir, stagingDirName, formatIdentityHash(input.Package)), Log: log,
		})
		installed = append(installed, ManifestInput{
			Package: input.Package, OutputTree: manifest.OutputTree, Digest: manifest.Digest,
		})
		proofs = append(proofs, verifiedTaskInput{input: input, manifest: manifest})
	}
	for _, group := range ordered {
		if err := InstallOutputSets(ctx, group.requests); err != nil {
			return nil, nil, OutputFaultReason(err), err
		}
		log.Debug().Str("package", group.name).Int("sets", len(group.requests)).Msg("inputs installed")
	}
	return installed, proofs, "", nil
}

type verifiedTaskInput struct {
	input    AssignmentInput
	manifest *OutputManifest
}

// readInputManifest reads the provider's result at the exact object the
// assignment named and answers the output manifest it carries, with what
// validating it proved.
//
// The verified assignment carries the exact result as a direct parent, so a
// moving branch tip cannot change what this task consumes. The result is
// signed with this run's secret. Its manifest digest is the one the
// orchestrator admitted after comparing the roots with the plan.
func (w *Worker) readInputManifest(ctx context.Context, view *VerifiedCarriers, assignment Assignment,
	input AssignmentInput) (*OutputManifest, ValidatedOutputs, OutputReason, error) {
	if input.Digest == "" {
		return nil, ValidatedOutputs{}, ReasonOutputDigest, fmt.Errorf(
			"execution: the assignment names the outputs of %s without a manifest digest", input.Package)
	}
	document, err := view.ReadResult(ctx, input.Commit,
		assignment.Limits.MaxManifestBytes)
	if err != nil {
		return nil, ValidatedOutputs{}, ReasonOutputIdentity, fmt.Errorf(
			"execution: reading the outputs of %s: %w", input.Package, err)
	}
	var result Result
	if err := json.Unmarshal(document, &result); err != nil {
		return nil, ValidatedOutputs{}, ReasonOutputIdentity, fmt.Errorf(
			"execution: reading the outputs of %s: %w", input.Package, err)
	}
	if result.Outputs == nil {
		return nil, ValidatedOutputs{}, ReasonBytesMissing, fmt.Errorf(
			"execution: the result of %s describes no outputs", input.Package)
	}
	validated, err := ValidateOutputs(ctx, w.Cache, result.Outputs, OutputExpectation{
		Run: assignment.Run, PlanDigest: assignment.PlanDigest, Generation: assignment.Generation,
		Task: input.Task, Roots: result.Outputs.Roots, Platforms: assignment.Platforms,
		Digest: input.Digest, Limits: assignment.Limits,
	})
	if err != nil {
		return nil, ValidatedOutputs{}, OutputFaultReason(err), fmt.Errorf(
			"execution: the outputs of %s cannot be used: %w", input.Package, err)
	}
	return result.Outputs, validated, "", nil
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
		IsAbsentRootEmpty: assignment.Kind == KindRun || assignment.Kind == KindStage,
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
