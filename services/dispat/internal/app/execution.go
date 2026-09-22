// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Who may start a release, and what a release that delegates work may not be
// started with.
//
// Everything here runs before the first lock is pushed, because that is where
// the specification puts it: a node that may not initiate a release has to
// refuse before it has acquired ownership of anything, and a distributed run
// that could not be coordinated has to fail before it has dispatched a task
// to anybody. With no execution settings at all every check below is one
// comparison against a nil object, nothing is logged, and the release is the
// release it always was.

import (
	"os"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// checkExecutionEntry decides whether this process may start this release,
// and is the one call the release path makes into the execution profile
// before it takes a lock.
//
// The order is the order the answers become knowable, and it is also the
// order a reader wants them in: who is asking comes before what they are
// asking for. A process that may not initiate at all is refused before its
// configuration is examined any further, and the two rules a distributed run
// is held to are checked only once it is established that this run delegates
// work to anybody.
func (a *App) checkExecutionEntry(started runKind) error {
	a.logIgnoredExecutionSettings()
	if err := a.refuseWorkerInitiation(started); err != nil {
		return err
	}
	if !a.cfg.Execution.IsDistributed() {
		return nil
	}
	if err := a.refuseDispatchWithoutLock(started); err != nil {
		return err
	}
	return a.refuseDispatchWithoutSecret(started)
}

// runKind is what a process is about to start and may be refused the
// starting of: a release, which takes the release locks, or a command sweep,
// which takes none (§28.10). The refusals are the same rules for both, and the
// word is what each sentence says was refused.
type runKind string

// The two runs a process may start that reach other machines.
const (
	runRelease runKind = "release"
	runSweep   runKind = "sweep"
)

// refuseWorkerInitiation refuses a release that a worker would be starting,
// whether it says so in its configuration or is executing somebody else's
// task at this moment.
//
// The environment is asked first because it is the stronger statement: an
// orchestrator-role node serving a delegated task has worker authority for
// that task and nothing else, so its own role says nothing about what the
// hook it is running may do. Both refusals happen before any lock, which is
// what makes an indirect initiation from a delegated hook harmless rather
// than a second run competing with the one that authorized it.
func (a *App) refuseWorkerInitiation(started runKind) error {
	if execution.IsWorkerAuthority(os.Environ()) {
		return a.reportExecutionRefusal(started, execution.NewDiagnostic(
			execution.CodeAuthority, execution.CategoryAuthority,
			"a task running under worker authority cannot start a %s: it executes what its assignment authorized and owns no run of its own",
			started), nil, nil)
	}
	if a.cfg.Execution.IsWorker() {
		return a.reportExecutionRefusal(started, execution.NewDiagnostic(
			execution.CodeAuthority, execution.CategoryAuthority,
			"execution.role is %q on this node, and a worker cannot start a %s: it executes the tasks an orchestrator authorized",
			a.cfg.Execution.ResolveRole(), started), nil, nil)
	}
	return nil
}

// refuseDispatchWithoutLock refuses to dispatch work from a run that is
// releasing without a remote release lock.
//
// The bypass exists for a repository with no remote to coordinate through,
// where the alternative is no release at all. A run that reaches other
// machines is the opposite situation: every node it dispatches to writes
// through a remote, and the lock is the only thing that stops a second run
// authorizing the same publication from somewhere else. So the two settings
// that switch the lock off are a warning on their own and a refusal here.
//
// A sweep takes no lock at all, and the same two settings refuse it too
// (§28.10): they state that a repository has no remote to coordinate through,
// and a run that dispatches to other machines is a run with one.
func (a *App) refuseDispatchWithoutLock(started runKind) error {
	repositories, isByConfig := a.calculateLockBypass()
	if len(repositories) == 0 {
		return nil
	}
	settings := lockBypassSettings(isByConfig)
	if started == runSweep {
		return a.reportExecutionRefusal(started, execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			"execution.workers dispatches work to other machines, and %s is configured to run without the remote release lock (%s): a repository with no remote to coordinate through has none to dispatch a sweep through either",
			strings.Join(repositories, ", "), strings.Join(settings, ", ")), repositories, settings)
	}
	return a.reportExecutionRefusal(started, execution.NewDiagnostic(
		execution.CodeConfiguration, execution.CategoryConfiguration,
		"execution.workers dispatches work to other machines, and %s would release without the remote release lock (%s): a release nothing coordinates cannot be delegated",
		strings.Join(repositories, ", "), strings.Join(settings, ", ")), repositories, settings)
}

// refuseDispatchWithoutSecret refuses to dispatch work nothing could sign.
//
// The configuration is what requires the variable to be named; this is what
// requires it to hold something. The name is reported and the value never is,
// which is the whole reason the secret is named by a variable rather than
// written in the file.
func (a *App) refuseDispatchWithoutSecret(started runKind) error {
	name := a.cfg.Execution.SecretEnv
	if secret, isSet := os.LookupEnv(name); isSet && secret != "" {
		return nil
	}
	return a.reportExecutionRefusal(started, execution.NewDiagnostic(
		execution.CodeConfiguration, execution.CategoryConfiguration,
		"execution.secretEnv names %s and it is unset or empty in this environment: every message a mailbox carries is signed with the secret it names",
		name), nil, nil)
}

// reportExecutionRefusal writes one refusal at error level and hands it back
// to the caller to return.
//
// The refusal is logged where it is decided, so the code, the category and
// the scope are attached by the only code that knows them, and the release
// path stays a sequence of guards that return what they are given. The scope
// fields are the lock bypass's and are absent from the refusals that have no
// scope: which repositories would have released unlocked, and which of the
// two settings asked for it.
func (a *App) reportExecutionRefusal(started runKind, err error, repositories, settings []string) error {
	event := a.logError(err)
	if len(repositories) > 0 {
		event.Strs("repositories", repositories).Strs("setting", settings)
	}
	event.Msg("cannot start " + string(started))
	return err
}

// calculateLockBypass answers which participating repositories this run would
// release without a remote release lock, and whether a configuration rather
// than the environment asked for it.
//
// It is the question the lock acquisition itself asks, asked earlier: the
// decision per repository is the acquisition's own, so the two cannot come to
// different answers about the same run. The names are the ones the bypass
// warning would print, in the same sorted order, because a reader comparing
// the refusal with the warning it replaced should not have to translate.
func (a *App) calculateLockBypass() (repositories []string, isByConfig bool) {
	if a.workspace == nil {
		if !a.lockDisabled() {
			return nil, false
		}
		return []string{a.root}, a.cfg.UnsafeDisableLock
	}
	for i := range a.workspace.Repositories {
		repository := &a.workspace.Repositories[i]
		isBypassed, isStated := a.lockBypassOf(repository)
		if !isBypassed {
			continue
		}
		repositories = append(repositories, repository.Name)
		isByConfig = isByConfig || isStated
	}
	sort.Strings(repositories)
	return repositories, isByConfig
}

// logIgnoredExecutionSettings reports every repository of a composed
// workspace whose own configuration states execution settings this run will
// not read.
//
// Any peer may be another run's entry, so carrying the object is legitimate
// and is never an error. It is still worth one line: a peer that calls itself
// a worker, or lists workers of its own, looks from the outside exactly like
// a node whose settings were applied, and the only way to tell the two apart
// is to be told. A repository that shares the entry's configuration file is
// not reporting anything of its own and is skipped.
func (a *App) logIgnoredExecutionSettings() {
	if a.workspace == nil {
		return
	}
	for i := range a.workspace.Repositories {
		repository := &a.workspace.Repositories[i]
		if repository.Entry || !statesOwnExecution(repository, a.cfg) {
			continue
		}
		a.log.Debug().Str("repository", repository.Name).
			Msg("execution settings ignored outside the entry configuration")
	}
}

// statesOwnExecution reports whether one repository's own configuration
// states execution settings. A repository whose configuration is the entry's
// file states nothing of its own: the sources of a central fleet that declare
// no configuration are recorded against the control file itself.
func statesOwnExecution(repository *config.Repository, entry *config.File) bool {
	return repository.Config != nil && repository.Config != entry && repository.Config.Execution != nil
}
