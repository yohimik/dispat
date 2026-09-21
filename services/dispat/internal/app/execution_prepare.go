// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// The build frame of a provider this run does not release (CCME §28.5).
//
// A consumer reads the folder its provider builds, and whether that provider
// is in this run's plan is a question about releasing rather than about
// compiling. So when it is not, the run builds it anyway, and the one
// interesting decision is what such a build is told about itself.
//
// It is told exactly what `dispat run` tells the same package: the version it
// already carries, a bump of none, every provider of its own live. That is the
// answer a script of that package reads outside a release, and a script that
// behaved differently because somebody else's release happened to need its
// output would be a script whose behaviour depends on another package's
// changes. Nothing about the release is invented for it: it gets no new
// version, no tag, no changelog, no record and no plan entry, and Plan(I) and
// its digest are what they were.
//
// What does not come from `dispat run` is the split between the computed
// DISPAT_* pairs and the configuration's own: a frame that may travel to
// another machine carries its static pairs unresolved, so a value naming a
// secret is expanded on the node that runs the command and never in a mailbox.

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// The two hooks that bracket a build frame, under the names an operator wrote
// them and a log reports them.
const (
	beforeBuildStage = "beforeBuild"
	postBuildStage   = "postBuild"
)

// resolvePreparedProviders answers, for one provider this run does not
// release, the build frame that produces its declared outputs.
//
// The workspace listing is built once here for the same reason the run builds
// it once: it depends on the plan alone, and one preparation per provider is
// the most this run will ask for.
func (a *App) resolvePreparedProviders(pl *plan.Plan,
	runner script.Runnerx) func(string) (*execution.PreparedProvider, error) {
	wsVars := release.WorkspaceEnv(pl, a.log)
	return func(packageName string) (*execution.PreparedProvider, error) {
		rel := pl.Releases[packageName]
		if rel == nil {
			return nil, fmt.Errorf(
				"the plan holds no entry for %s, so the outputs its consumers read cannot be built",
				packageName)
		}
		space := rel.Pkg.Space
		request := release.StageRequest{
			Release: rel,
			Stage:   execution.StageBuild,
			Frame: release.StageFrame{
				Before:   space.BeforeBuildScript,
				Commands: space.BuildScript,
				After:    space.PostBuildScript,
			},
			Env:       release.ComputedCommandEnv(pl, packageName, execution.StageBuild, wsVars),
			StaticEnv: space.Env,
			Dir:       rel.Pkg.Dir,
		}
		return &execution.PreparedProvider{
			Request: request,
			Here: func(ctx context.Context) (string, error) {
				return a.runPreparedFrame(ctx, runner, request)
			},
		}, nil
	}
}

// runPreparedFrame runs one prepared provider's build frame in this checkout,
// and answers the sentence naming the part that failed.
//
// The three sequences are the build stage's own, fail-fast and in order, and
// the exports accumulate across them exactly as they do for a stage the run is
// releasing: a beforeBuild hook that exported a value is a hook whose value
// the build script reads. They accumulate onto a carrier of their own rather
// than onto the plan's release, because a package this run is not releasing
// must not come out of the run carrying outputs nobody released it with.
func (a *App) runPreparedFrame(ctx context.Context, runner script.Runnerx,
	request release.StageRequest) (string, error) {
	rel := request.Release
	carried := &plan.Release{}
	for _, part := range []struct {
		label    string
		stage    string
		commands []string
	}{
		{beforeBuildStage + " hook failed", beforeBuildStage, request.Frame.Before},
		{execution.StageBuild + " script failed", execution.StageBuild, request.Frame.Commands},
		{postBuildStage + " hook failed", postBuildStage, request.Frame.After},
	} {
		if len(part.commands) == 0 {
			continue
		}
		log := a.log.With().Str("package", rel.Pkg.Name).Str("stage", part.stage).Logger()
		log.Debug().Int("commands", len(part.commands)).
			Msg("preparing the build outputs of a package this run does not release")
		exported, err := release.Sequence{
			Runner: runner, Dir: request.Dir, Stage: part.stage, Commands: part.commands,
			Env:      formatPreparedEnv(request, part.stage, carried),
			Log:      log,
			FailFast: true,
		}.RunCollectingOutputs(ctx, rel.Pkg.Name+":"+part.stage)
		release.MergeOutputs(carried, exported)
		if err != nil {
			return part.label, err
		}
	}
	return "", nil
}

// reportPreparedProviders names every provider this run built without
// releasing it, once the task graph has finished.
//
// It is one line per provider rather than a count, because a prepared
// provider is the one piece of work a release summary could not otherwise
// account for: it is in no plan, it produces no result and it emits no event
// claiming a release, so without this an operator reading the run would see
// build output for a package the summary never mentions. The three words are
// §28.9's, and the publication one always reads "none", which is the whole
// claim being made.
func (a *App) reportPreparedProviders(coordinator *execution.Coordinator) {
	for _, record := range coordinator.PreparedRecords() {
		event := a.log.Info().Str("package", record.Package).Str("task", record.Task).
			Str("computation", record.Computation).Str("outputs", record.Outputs).
			Str("publication", record.Publication)
		if record.Node != "" && record.Node != a.sender.Node {
			event = event.Str("worker", record.Node)
		}
		event.Msg("provider prepared, not released")
	}
}

// formatPreparedEnv is the environment one sequence of a prepared frame runs
// under: the configuration's own pairs expanded from this machine, then the
// computed pairs, then the stage being run and what the frame has exported so
// far. The order is the one every script of a release already runs under, so a
// computed DISPAT_* variable always wins a name clash.
func formatPreparedEnv(request release.StageRequest, stage string, carried *plan.Release) []string {
	computed := append(append([]string{}, request.Env...), "DISPAT_STAGE="+stage)
	return release.StaticEnv(request.StaticEnv, append(computed, carried.OutputVars()...))
}
