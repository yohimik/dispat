// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

// Where a worker link reaches: the repository being released, unless the link
// names another mailbox.
//
// The coordination branches of a distributed run have to live on a Git remote
// every node can reach, and a release already has one: the remote it takes its
// lock on and records to. A link that states no endpoint reaches that remote,
// at the push URL Git resolves for it, so a pool needs no second repository and
// the transport shares its objects with the source. An endpoint a link states
// is an override, for coordination that has to live somewhere else.
//
// The push URL is held to every rule a stated endpoint is, because an endpoint
// never carries a secret (CCME §28.2). It is resolved and checked when a run
// that dispatches starts, before any lock, so a remote whose push URL carries a
// token refuses the run before anything is written. A release reads it again
// from the destination its own lock was taken on, which is the destination its
// cleanup reaches, and checks it again there.

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// coordinationRemote is the remote a worker link with no endpoint reaches: its
// name as the configuration spells it, and the push URL Git resolves for it.
type coordinationRemote struct {
	name string
	url  string
}

// resolveWorkerLinks resolves, before any lock, the remote every link with no
// endpoint reaches, holds its push URL to the rules of an endpoint, and says
// where each such link goes in one debug line apiece.
//
// A run whose links all state an endpoint resolves nothing, so a remote nobody
// uses cannot refuse a run.
func (a *App) resolveWorkerLinks(ctx context.Context, started runKind) error {
	if !a.isCoordinationRemoteNeeded() {
		return nil
	}
	name, url, err := a.resolveCoordinationRemote(ctx)
	if err != nil {
		return a.reportExecutionRefusal(started, execution.NewDiagnostic(
			execution.CodeConfiguration, execution.CategoryConfiguration,
			"a worker link states no endpoint, and the release remote it would reach could not be resolved: %w",
			err), nil, nil)
	}
	remote := coordinationRemote{name: name, url: url}
	if _, err := a.formatWorkerLinks(remote); err != nil {
		return a.reportExecutionRefusal(started, err, nil, nil)
	}
	for _, worker := range a.cfg.Execution.Workers {
		if worker.Endpoint != "" {
			continue
		}
		a.log.Debug().Str("worker", worker.Name).Str("remote", gitx.RedactEndpoint(remote.name)).
			Str("endpoint", gitx.RedactEndpoint(remote.url)).
			Msg("worker link reaches the release remote")
	}
	a.coordination = remote
	return nil
}

// isCoordinationRemoteNeeded reports whether any link states no endpoint.
func (a *App) isCoordinationRemoteNeeded() bool {
	for _, worker := range a.cfg.Execution.Workers {
		if worker.Endpoint == "" {
			return true
		}
	}
	return false
}

// resolveCoordinationRemote names the remote a link with no endpoint reaches
// and the push URL it resolves to: the remote the release takes its lock on,
// which is the single history's own and, in a composed workspace, the entry
// repository's.
//
// The resolution is the lock's own, lockDestination, so the remote a worker is
// reached at and the remote the lock is taken on cannot be two answers to one
// question.
func (a *App) resolveCoordinationRemote(ctx context.Context) (name, url string, err error) {
	if a.workspace == nil {
		name = a.pushRemote()
		url, err = lockDestination(ctx, a.git, name, a.log)
		return name, url, err
	}
	for i := range a.workspace.Repositories {
		entry := &a.workspace.Repositories[i]
		if !entry.Entry {
			continue
		}
		name = commitRemote(entry.Commit)
		git := &gitx.LocalGitx{Dir: entry.Root, Log: a.log}
		url, err = lockDestination(ctx, git, name, a.log)
		return name, url, err
	}
	return "", "", fmt.Errorf("the composed workspace names no entry repository")
}

// resolveLockedEndpoint is the push URL this run's own lock was taken on: the
// single history's, or the entry repository's in a composed workspace, and
// empty for a run that holds no such lock.
func (a *App) resolveLockedEndpoint(fleet *workspaceRecorder) string {
	if fleet == nil {
		if a.releaseLock == nil {
			return ""
		}
		return a.releaseLock.Remote
	}
	for _, held := range fleet.held {
		if held.repository.repo.Entry {
			return held.lock.Remote
		}
	}
	return ""
}

// formatWorkerLinks is every configured link as the run reaches it: at its own
// endpoint when it states one, and at the release remote's push URL when it does
// not. That URL is held to the rules of an endpoint each time a link uses it.
func (a *App) formatWorkerLinks(remote coordinationRemote) ([]execution.Link, error) {
	workers := a.cfg.Execution.Workers
	links := make([]execution.Link, 0, len(workers))
	for _, worker := range workers {
		endpoint := worker.Endpoint
		if endpoint == "" {
			if err := requireCoordinationEndpoint(worker.Name, remote); err != nil {
				return nil, err
			}
			endpoint = remote.url
		}
		links = append(links, execution.Link{Name: worker.Name, Endpoint: endpoint})
	}
	return links, nil
}

// requireCoordinationEndpoint refuses, for the link that would reach it, a push
// URL no mailbox may be.
func requireCoordinationEndpoint(worker string, remote coordinationRemote) error {
	return requireReleaseRemoteEndpoint(releaseRemoteUse{
		subject: fmt.Sprintf("worker link %s states no endpoint, so it", worker),
		credentialRemedy: "state a credential-free endpoint, " +
			"or keep the credential in a credential helper or http.extraheader",
	}, remote)
}

// releaseRemoteUse is who reaches the release remote in place of an endpoint,
// and what they can do instead: when its push URL carries a credential, and,
// when there is anything to say, when it cannot be a mailbox at all.
type releaseRemoteUse struct {
	subject          string
	credentialRemedy string
	shapeRemedy      string
}

// requireReleaseRemoteEndpoint refuses a release remote's push URL no mailbox
// may be, for whichever party would reach it: an orchestrator's link with no
// endpoint, or a worker that states none of its own.
//
// Both the remote's name and its URL are written into the refusal redacted,
// because `commit.remote` may name a URL rather than a remote, and a URL
// refused for what it carries is told apart from one refused for its shape,
// because the remedy differs: a credential belongs in a credential helper or
// an extra header, never in an address every message is pushed to.
func requireReleaseRemoteEndpoint(use releaseRemoteUse, remote coordinationRemote) error {
	err := gitx.RequireTransportEndpoint(remote.url)
	if err == nil {
		return nil
	}
	name := gitx.RedactEndpoint(remote.name)
	if redacted := gitx.RedactEndpoint(remote.url); redacted != remote.url {
		return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"%s reaches the release remote %s, whose push URL %s carries credentials: %s",
			use.subject, name, redacted, use.credentialRemedy)
	}
	if use.shapeRemedy != "" {
		return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"%s reaches the release remote %s, whose push URL cannot be a mailbox (%s): %w",
			use.subject, name, use.shapeRemedy, err)
	}
	return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
		"%s reaches the release remote %s, whose push URL cannot be a mailbox: %w",
		use.subject, name, err)
}
