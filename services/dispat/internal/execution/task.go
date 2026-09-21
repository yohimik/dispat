// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Executing somebody else's stage frame.
//
// A node runs what it was given and resolves nothing of its own (§28.3). The
// commands, the folder, the shell and the computed environment all travel in
// the assignment; the one thing the node adds is its own environment, because
// that is where the secrets a static pair refers to live and it is the whole
// reason those pairs travel unresolved.
//
// Two rules hold over every command a task starts. It runs under worker
// authority, so a nested dispat inside a build script cannot start a release
// of its own however deeply it is nested; and it inherits no composed-workspace
// context from whatever started this node, because a node serving a task is
// not taking part in the workspace that node happens to sit in.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// workspaceContextPrefix is what a composed run carries to the commands it
// starts. A node serving somebody else's task strips it: the checkout it
// materialized is not the workspace whose context it inherited, and a nested
// dispat that believed otherwise would be reading another machine's layout.
const workspaceContextPrefix = "DISPAT_INTERNAL_WORKSPACE_"

// taskReportTimeout bounds the one write a finished task still owes its
// orchestrator, on a context detached from the run's own. A node asked to stop
// mid-task has already had its commands killed; reporting what became of them
// is the last thing it does, and it must not be cancelled by the same signal.
const taskReportTimeout = 30 * time.Second

// taskOutcome is what running one frame produced, in the shape a result
// message states it.
type taskOutcome struct {
	status      string
	failedPart  string
	exports     []plan.Output
	strayWrites int
}

// runTask materializes the task's checkouts, runs its frame and answers what
// became of it.
//
// Nothing here fails the node. A frame that could not be prepared and a frame
// whose script exited non-zero are the same thing to the run that dispatched
// it: a task that did not succeed, reported as one, on a node that carries on
// serving.
func (w *Worker) runTask(ctx context.Context, assignment Assignment, log zerolog.Logger) taskOutcome {
	if assignment.Package == nil || assignment.Frame == nil || len(assignment.Repositories) == 0 {
		log.Warn().Str("code", CodeAuthority).Str("category", CategoryAuthority).
			Msg("the assignment describes no frame to run")
		return taskOutcome{status: StatusFailed}
	}
	dir := filepath.Join(w.StateDir, taskWorkDir,
		formatTaskFolder(assignment.Run, assignment.Task, assignment.Attempt))
	defer w.clearTaskDir(dir, log)
	branches := make([]string, 0, len(assignment.Repositories))
	for _, repository := range assignment.Repositories {
		branches = append(branches, repository.Branch)
	}
	if err := w.Mailbox.Fetch(ctx, branches); err != nil {
		log.Error().Err(err).Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the task's input states could not be fetched")
		return taskOutcome{status: StatusFailed}
	}
	checkout := NewCheckout(w.Cache, filepath.Join(dir, taskCheckoutDir), log)
	defer checkout.Remove(context.WithoutCancel(ctx))
	if err := checkout.Materialize(ctx, assignment.Repositories); err != nil {
		log.Error().Err(err).Str("code", CodeIntegrity).Str("category", CategoryIntegrity).
			Msg("the task's checkouts could not be materialized")
		return taskOutcome{status: StatusFailed}
	}
	owner := ownerPathOf(assignment)
	outcome := w.runFrame(ctx, assignment, checkout.Dir(owner, assignment.Package.Dir), log)
	outcome.strayWrites = checkout.CountStrayWrites(ctx, owner)
	return outcome
}

// The folders one attempt owns under the node's own state folder.
const (
	taskWorkDir     = "tasks"
	taskCheckoutDir = "checkout"
)

// ownerPathOf is where the repository the package belongs to sits inside the
// checkout.
func ownerPathOf(assignment Assignment) string {
	for _, repository := range assignment.Repositories {
		if repository.Name == assignment.Package.Repository {
			return repository.Path
		}
	}
	return "."
}

// runFrame runs the three sequences of one stage frame in order, each
// fail-fast, and answers at the first one that did not succeed.
//
// The exports accumulate across the three exactly as they do at home: a hook
// that exported a value before the stage's own script ran is a hook whose
// value that script reads, so the environment is rebuilt for each sequence
// from what the ones before it produced.
func (w *Worker) runFrame(ctx context.Context, assignment Assignment, dir string, log zerolog.Logger) taskOutcome {
	// carried is everything the run had exported before this frame plus
	// everything the frame exports, which is what the scripts read; produced is
	// this frame's own, which is what travels back.
	carried := &plan.Release{Outputs: formatOutputs(assignment.Exports)}
	produced := &plan.Release{}
	stage := assignment.Kind
	for _, part := range []struct {
		name     string
		stage    string
		commands []string
	}{
		{release.PartBefore, "before" + formatStageTitle(stage), assignment.Frame.Before},
		{release.PartCommands, stage, assignment.Frame.Commands},
		{release.PartAfter, "post" + formatStageTitle(stage), assignment.Frame.After},
	} {
		if len(part.commands) == 0 {
			continue
		}
		sequence := release.Sequence{
			Runner:   &script.ShellRunner{Shell: assignment.Shell, Log: log},
			Dir:      dir,
			Stage:    part.stage,
			Commands: part.commands,
			Env:      w.formatTaskEnv(assignment, part.stage, carried),
			Log:      log.With().Str("package", assignment.Package.Name).Str("stage", part.stage).Logger(),
			FailFast: true,
		}
		log.Info().Str("stage", part.stage).Int("commands", len(part.commands)).Msg("stage started")
		exported, err := sequence.RunCollectingOutputs(ctx, assignment.Package.Name+":"+part.stage)
		release.MergeOutputs(carried, exported)
		release.MergeOutputs(produced, exported)
		if err == nil {
			continue
		}
		log.Warn().Err(err).Str("stage", part.stage).Str("part", part.name).Msg("stage failed")
		return taskOutcome{status: StatusFailed, failedPart: part.name, exports: produced.Outputs}
	}
	return taskOutcome{status: StatusSucceeded, exports: produced.Outputs}
}

// formatStageTitle renders a stage name as it appears inside a hook name, so
// that DISPAT_STAGE on a node carries the same word it carries at home.
func formatStageTitle(stage string) string {
	if stage == "" {
		return ""
	}
	return strings.ToUpper(stage[:1]) + stage[1:]
}

// formatTaskEnv is the environment one sequence of a task runs under.
//
// The order is what makes it dependable. The configuration's own pairs are
// expanded here, from this node's environment, and placed first, so a computed
// DISPAT_* variable always wins a name clash; then this node's authority,
// which nothing may override; then the blanking of the workspace context this
// process may have inherited from whoever started it.
func (w *Worker) formatTaskEnv(assignment Assignment, stage string, carried *plan.Release) []string {
	computed := append(append([]string{}, assignment.Env...), "DISPAT_STAGE="+stage)
	computed = append(computed, carried.OutputVars()...)
	env := release.StaticEnv(assignment.StaticEnv, computed)
	env = append(env, FormatWorkerAuthorityEnv(w.Node)...)
	return append(env, formatInheritedBlanks(os.Environ())...)
}

// formatInheritedBlanks empties the composed-workspace variables this process
// inherited, one pair per name that is actually set.
//
// Emptying rather than removing, because a child's environment is this
// process's own plus what is appended to it: an empty value is what every
// reader of these variables treats as absent, and it is the only way to unset
// an inherited name without rebuilding the environment of a process that has
// already started.
func formatInheritedBlanks(environ []string) []string {
	var blanks []string
	for _, pair := range environ {
		name, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(name, workspaceContextPrefix) {
			blanks = append(blanks, name+"=")
		}
	}
	return blanks
}

// clearTaskDir removes one attempt's folder, whatever became of the attempt.
func (w *Worker) clearTaskDir(dir string, log zerolog.Logger) {
	if err := os.RemoveAll(dir); err != nil {
		log.Warn().Err(err).Str("code", CodeTransportRetained).
			Str("category", CategoryTransportCleanup).Msg("a task folder was not removed")
		return
	}
	log.Debug().Msg("task folder removed")
}

// formatOutputs turns transported export values into the plan's own, which is
// the shape every rule about them is written against.
func formatOutputs(exports []ExportedValue) []plan.Output {
	outputs := make([]plan.Output, 0, len(exports))
	for _, export := range exports {
		outputs = append(outputs, plan.Output{Name: export.Name, Value: export.Value, Source: export.Source})
	}
	return outputs
}

// formatExports is formatOutputs the other way round: what a node reports back
// about the values its frame exported.
func formatExports(outputs []plan.Output) []ExportedValue {
	exports := make([]ExportedValue, 0, len(outputs))
	for _, output := range outputs {
		exports = append(exports, ExportedValue{Name: output.Name, Value: output.Value, Source: output.Source})
	}
	return exports
}
