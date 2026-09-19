// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
)

// logWorkspaceComposition reports what composition decided, at the first
// moment the configured log level is known and before any repository
// operation has happened.
//
// Participation is the part worth the default level: a repository the control
// file excluded is absent from every later line, so a reader who never sees
// this line has no way to tell an excluded repository from one that simply had
// nothing to release. A choreographed fleet adds the saga and the entry to that
// line for the same reason: which repository the run started in decides which
// fleet it found.
func logWorkspaceComposition(log zerolog.Logger, workspace *config.Workspace) {
	if workspace == nil {
		return
	}
	participating := make([]string, 0, len(workspace.Repositories))
	for i := range workspace.Repositories {
		participating = append(participating, workspace.Repositories[i].Name)
	}
	// The anchor is named for what it is. An orchestrated fleet's is the
	// control repository, and every reader and log filter knows that field; a
	// choreographed fleet has no control repository at all, so calling its
	// entry root `control` would name a thing that does not exist.
	event := log.Info()
	if workspace.IsChoreographed() {
		event = event.Str("root", workspace.ControlRoot)
	} else {
		event = event.Str("control", workspace.ControlRoot)
	}
	event = event.Strs("repositories", participating)
	if workspace.IsChoreographed() {
		event = event.Str("saga", config.SagaChoreography)
		if entry := workspace.EntryRepository(); entry != nil {
			event = event.Str("entry", entry.Name)
		}
	}
	if excluded := workspace.DisabledRepositoryNames(); len(excluded) > 0 {
		event = event.Strs("excludedRepositories", excluded)
	}
	event.Msg("polyrepo workspace composed")
	for _, repository := range workspace.DisabledRepositories() {
		log.Info().Str("repository", repository.Name).Str("path", repository.Path).
			Str("reason", "repositoryOverrides."+repository.Name+".enabled=false").
			Msg("repository excluded from the release")
	}
	if spaces := workspace.ExcludedSpaces(); len(spaces) > 0 {
		log.Debug().Strs("spaces", spaces).
			Msg("control space declarations excluded with their repository")
	}
	logLinkFindings(log, workspace)
	for i := range workspace.Repositories {
		repository := &workspace.Repositories[i]
		event := log.Trace().Str("repository", repository.Name).Str("root", repository.Root).
			Str("gitlink", repository.GitlinkPath).Bool("control", repository.Control).
			Bool("imported", repository.Imported).Str("revision", repository.CompositionHead)
		if workspace.IsChoreographed() {
			event = event.Bool("entry", repository.Entry).Str("linker", repository.Linker)
		}
		event.Msg("repository composed")
		logRepositoryLinks(log, workspace, repository)
	}
}

// logLinkFindings reports every recoverable link problem the walk observed.
// They are warnings because each one is a fleet that still composes and a
// repair `dispat compute` can propose, and because every W diagnostic is read
// at this level.
func logLinkFindings(log zerolog.Logger, workspace *config.Workspace) {
	for _, finding := range workspace.LinkFindings() {
		log.Warn().Str("code", finding.Code).Str("repository", finding.Repository).
			Str("peer", finding.Peer).Msg(finding.Message)
	}
}

// logRepositoryLinks says how the walk reached one repository and which links
// it holds: the decision at debug, every link at trace. Together they are the
// composed fleet read back as the walk that found it, which is the only way to
// see why a repository is in the run or why the walk stopped where it did.
func logRepositoryLinks(log zerolog.Logger, workspace *config.Workspace, repository *config.Repository) {
	if !workspace.IsChoreographed() {
		return
	}
	if repository.Linker != "" {
		log.Debug().Str("repository", repository.Name).Str("linker", repository.Linker).
			Str("path", repository.GitlinkPath).Msg("fleet repository reached through a link")
	}
	for _, peer := range repository.LinkPeers() {
		event := log.Trace().Str("repository", repository.Name).Str("peer", peer).
			Str("path", repository.Links[peer])
		if peer == repository.Linker {
			// The back-link every two-sided pair carries. It is where the walk
			// turned round rather than entering a second copy of the linker.
			event = event.Bool("backLink", true)
		}
		event.Msg("fleet link")
	}
}
