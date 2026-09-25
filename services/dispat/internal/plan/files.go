// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// The changed files of §6.2, read only for the commits that need them.
//
// A commit's changed paths decide its packages only where a unit leaves its
// scope to them: a unit with no scope-set, one of exclusions alone, or one
// naming "." (§6.1), in its header or in a Propagate-Scope footer. Every other
// unit names its packages outright. Listing paths costs a diff of the commit
// against its parent, so a Git implementation that can (gitx.ChangedFilesx)
// reads the history without them, and the planner asks for the paths of the
// commits whose units need them, once, after the messages are parsed.

// isScopeSetDerived reports whether resolving the scope-set reads the commit's
// changed files: it has no inclusion, so its base is the file-derived set, or
// one of its terms is "." (resolveScopeSet, expandTerm).
func isScopeSetDerived(scopes ccme.ScopeSet) bool {
	if len(scopes.Includes()) == 0 {
		return true
	}
	for _, term := range scopes {
		if term.IsDerived() {
			return true
		}
	}
	return false
}

// areFilesNeededBy reports whether resolving the units' scopes can read the
// commit's changed files: the header scope-set of any unit, and the
// Propagate-Scope and Propagate-Channel-Scope footers where they are written.
// It is the pre-pass's forecast; derived still reads the files of a commit it
// is asked about and the forecast missed, so a wrong forecast costs a git
// process and never an answer.
func areFilesNeededBy(units []*ccme.Unit) bool {
	for _, u := range units {
		if scopes, written := unitScopes(u); !written || isScopeSetDerived(scopes) {
			return true
		}
		d := u.Directives
		if d.PropagateScopeSet && isScopeSetDerived(d.PropagateScope) {
			return true
		}
		if d.PropagateChannelScopeSet && isScopeSetDerived(d.PropagateChannelScope) {
			return true
		}
	}
	return false
}

// readDeferredFiles fills in the changed files of every record whose Git
// implementation deferred them, one read per repository.
func (cp *computation) readDeferredFiles(recs []*commitRec) error {
	type pending struct {
		repository string
		reader     gitx.ChangedFilesx
		recs       []*commitRec
		raw        []string
	}
	var order []*pending
	byRepository := make(map[string]*pending)
	for _, rec := range recs {
		if !rec.commit.AreFilesDeferred {
			continue
		}
		repository, raw := splitHistoryKey(rec.key)
		folded := globx.Fold(repository)
		p := byRepository[folded]
		if p == nil {
			git, _, _ := cp.gitForKey(rec.key)
			reader, isReader := git.(gitx.ChangedFilesx)
			if !isReader {
				return fmt.Errorf("plan: repository %q deferred the changed files of %s and cannot read them",
					repository, raw)
			}
			p = &pending{repository: repository, reader: reader}
			byRepository[folded] = p
			order = append(order, p)
		}
		p.recs = append(p.recs, rec)
		p.raw = append(p.raw, raw)
	}
	for _, p := range order {
		if err := cp.ctx.Err(); err != nil {
			return fmt.Errorf("plan: reading changed files: %w", err)
		}
		files, err := p.reader.ChangedFiles(cp.ctx, p.raw)
		if err != nil {
			return fmt.Errorf("plan: reading the changed files of %d commits in repository %q: %w",
				len(p.raw), p.repository, err)
		}
		for i, rec := range p.recs {
			rec.commit.Files = files[p.raw[i]]
			rec.commit.AreFilesDeferred = false
		}
		cp.log.Debug().Str("repository", p.repository).Int("commits", len(p.recs)).
			Msg("plan: changed files read")
	}
	return nil
}

// readUnforeseenFiles is derived's guard: a record whose files are still
// deferred when a scope asks for them is read alone. The pre-pass forecasts
// every such record, so this does nothing unless the forecast and the
// resolution disagree, and then it keeps the answer exact at the price of one
// git process. A failure is kept and reported after the resolution pass.
func (cp *computation) readUnforeseenFiles(rec *commitRec) {
	if !rec.commit.AreFilesDeferred {
		return
	}
	cp.log.Debug().Str("commit", rec.key).Msg("plan: changed files read for a commit the pre-pass did not forecast")
	if err := cp.readDeferredFiles([]*commitRec{rec}); err != nil && cp.filesErr == nil {
		cp.filesErr = err
	}
}
