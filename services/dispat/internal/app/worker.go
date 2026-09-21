// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Serving somebody else's work: what `dispat worker` needs before it can
// begin, and what it hands the loop that does the serving.
//
// A serving node is the same engine in a different posture (§28.1): it reads
// the same configuration format, refuses to initiate anything of its own, and
// executes what an orchestrator authorized. What it needs to start is
// therefore small and entirely local: a name to recognise work by, a mailbox
// to read it from, a secret to authenticate it with, and a folder of its own
// to keep what it has already answered. Each of the four is refused by name
// rather than discovered as a node that serves nothing.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// WorkerOptions are the invocation's own settings, as opposed to the node's
// configured ones.
type WorkerOptions struct {
	// StateDir is the folder this node keeps its cache and its record of
	// answered work in. Empty takes the user cache directory.
	StateDir string
	// IdleTimeout ends the process after this long with nothing to do. Zero
	// serves until the process is signalled.
	IdleTimeout time.Duration
	// Version is the dispat build, reported to whoever probes this node.
	Version string
}

// ServeTasks runs this node's serving loop until it is signalled or goes
// idle.
//
// It is one method rather than a command of its own because everything it
// needs is the App's: the configuration that says what this node is, the
// logger the run reports through, and git. Nothing it does touches a plan, a
// package or a release record, and that is the whole point of the role.
func (a *App) ServeTasks(ctx context.Context, opts WorkerOptions) error {
	settings := a.cfg.Execution
	if settings == nil {
		// Nothing about execution was stated at all, which is every required
		// setting missing at once; the refusals below name them one by one.
		settings = &config.ExecutionConfig{}
	}
	if err := a.checkWorkerSettings(settings); err != nil {
		return err
	}
	signer, err := execution.NewSigner(os.Getenv(settings.SecretEnv))
	if err != nil {
		return a.refuseToServe(execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			"execution.secretEnv names %s and it is unset or empty in this environment: a node that cannot authenticate its work cannot serve it",
			settings.SecretEnv))
	}
	root, err := resolveWorkerStateRoot(opts.StateDir)
	if err != nil {
		return a.refuseToServe(err)
	}
	state, release, err := execution.OpenNodeState(root, settings.Name, settings.Endpoint)
	if err != nil {
		return a.refuseToServe(err)
	}
	// The lock is this node's claim on its own folder, so giving it back is
	// reported rather than dropped: a lock left behind is taken over by the
	// next process, and an operator should still be able to see that it was.
	defer func() {
		if err := release(); err != nil {
			a.log.Warn().Err(err).Str("node", settings.Name).Msg("the worker state lock was not released")
		}
	}()
	seen, err := execution.LoadSeenSet(state.Seen, time.Now())
	if err != nil {
		return a.refuseToServe(err)
	}
	git := &gitx.LocalGitx{Dir: state.Cache, Log: a.log}
	worker := &execution.Worker{
		Node:        settings.Name,
		Endpoint:    settings.Endpoint,
		StateDir:    state.Dir,
		IdleTimeout: opts.IdleTimeout,
		Mailbox:     execution.NewGitMailbox(settings.Endpoint, git, signer, a.log),
		Cache:       git,
		Seen:        seen,
		Log:         a.log,
		Report: execution.FormatNodeReport(opts.Version, git.GitVersion(ctx),
			settings.ResolveConcurrency(), formatTransferLimits(settings)),
		// The cache is dispensable by design, so opening it is the same
		// operation as repairing it: a node whose folder was deleted under it
		// makes one again and pays a fetch.
		PrepareStore: func(ctx context.Context) error {
			if err := os.MkdirAll(state.Cache, 0o755); err != nil {
				return err
			}
			return git.InitBareStore(ctx)
		},
	}
	worker.Serve(ctx)
	return nil
}

// checkWorkerSettings refuses a node that could not serve, naming the setting
// that is missing.
//
// All three are optional in the configuration language, because an
// orchestrator that never serves states none of them, and all three are
// required here: a node with no name cannot recognise the work addressed to
// it, a node with no mailbox has nowhere to read it from, and a node with no
// secret cannot tell an assignment from anything else somebody pushed.
func (a *App) checkWorkerSettings(settings *config.ExecutionConfig) error {
	for _, required := range []struct{ key, value, why string }{
		{"execution.name", settings.Name,
			"it is how this node recognises the work addressed to it"},
		{"execution.endpoint", settings.Endpoint,
			"it is the mailbox this node reads its work from"},
		{"execution.secretEnv", settings.SecretEnv,
			"it names the variable holding the secret every message is signed with"},
	} {
		if required.value != "" {
			continue
		}
		return a.refuseToServe(execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			"%s is required to serve tasks: %s", required.key, required.why))
	}
	return nil
}

// refuseToServe writes one refusal at error level and hands it back to the
// caller to return, exactly as the release path's own refusals are reported
// where they are decided.
func (a *App) refuseToServe(err error) error {
	a.logError(err).Msg("cannot serve tasks")
	return err
}

// resolveWorkerStateRoot is where a node keeps its own folder when the
// invocation named none: the user's cache directory, because everything under
// it is reconstructible and a node that lost it pays one fetch.
func resolveWorkerStateRoot(stated string) (string, error) {
	if stated != "" {
		return stated, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"this system has no user cache directory to keep the worker state in, so --state-dir has to name one: %w", err)
	}
	return filepath.Join(cache, "dispat", "worker"), nil
}

// formatTransferLimits is the configured ceilings in the shape a message
// carries them, so that a node states the same numbers it would hold its own
// transfers to.
func formatTransferLimits(settings *config.ExecutionConfig) execution.TransferLimits {
	transfer := settings.ResolveTransfer()
	return execution.TransferLimits{
		MaxFiles:         transfer.MaxFiles,
		MaxBytes:         transfer.MaxBytes,
		MaxManifestBytes: transfer.MaxManifestBytes,
	}
}
