// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"fmt"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
)

// ---------------------------------------------------------------------------
// §13.4 parse and resolve
// ---------------------------------------------------------------------------

// parseAndResolve parses every commit of the union, reads the changed files of
// the commits whose units derive a scope from them (files.go), and then
// resolves the units commit by commit.
func (cp *computation) parseAndResolve() error {
	parsed := make([]*ccme.Result, len(cp.commits))
	var needFiles []*commitRec
	for i, rec := range cp.commits {
		data, err := cp.parserFor(rec).Parse(rec.commit.Message)
		if data == nil {
			return fmt.Errorf("plan: %s: %w", rec.key, err)
		}
		parsed[i] = data
		if rec.commit.AreFilesDeferred && areFilesNeededBy(data.ValidUnits()) {
			needFiles = append(needFiles, rec)
		}
	}
	if err := cp.readDeferredFiles(needFiles); err != nil {
		return err
	}
	for i, rec := range cp.commits {
		cp.resolveCommit(rec, parsed[i])
		parsed[i] = nil
		// The paths have served their one purpose, the derived set, which
		// derivedSet keeps; nothing after this pass reads them.
		rec.commit.Files = nil
	}
	return cp.filesErr
}

// parserFor is the parser configured for the history carrying rec.
func (cp *computation) parserFor(rec *commitRec) *ccme.Parser {
	if configured := cp.parsers[globx.Fold(rec.repository)]; configured != nil {
		return configured
	}
	return cp.parser
}

// resolveCommit is §13.4 for one parsed commit: its diagnostics, its authors,
// and every unit's scope-set and propagation.
func (cp *computation) resolveCommit(rec *commitRec, data *ccme.Result) {
	// A parse error invalidates only the offending unit (§16); its
	// siblings still apply, so the error itself is reported rather than
	// returned.
	cp.liftDiagnostics(data, rec.key)

	rec.units = data.ValidUnits()
	rec.unitCount = len(data.Units)
	cp.resolveAuthors(rec)
	rec.scope = make([]map[string]bool, len(rec.units))
	rec.propagations = make([]propagation, len(rec.units))
	rec.channelPropagations = make([]channelPropagation, len(rec.units))
	for i, u := range rec.units {
		// The commit behind the unit, recorded here because this is the
		// one place a unit and the record that carried it are both in
		// hand. Every unit of one message shares its key.
		cp.unitCommits[u] = rec.key
		scopes, written := unitScopes(u)
		res := cp.resolveScopeSet(scopes, written, rec)
		cp.reportScope(res, rec, "")
		// A correction with no scope-set takes the union of its targets'
		// packages, and §7.4.2 disapplies the file-derived fallback for it.
		// Its resolution here is provisional, so neither the inert warning
		// nor the set itself means anything until §13.4b has settled it.
		if res.inert() && !(isCorrection(u) && !written) {
			cp.warn(CodeInertUnit, "", rec.key,
				"unit resolved to no package and is inert: "+u.Header.Raw)
		}
		rec.scope[i] = res.packages
		if !u.IsCancel() {
			// The unit's Propagate-Scope restricts both axes (§8.5a), so
			// it is resolved once, by whichever axis asks first.
			var propagateScope *scopeResult
			rec.channelPropagations[i] = cp.unitChannelPropagation(u, rec, &propagateScope)
			if u.Bump != ccme.BumpNone {
				rec.propagations[i] = cp.unitPropagation(u, rec, &propagateScope)
			}
		}
	}
}

// liftDiagnostics carries ccme's diagnostics into the plan's, preserving code
// and severity. Flattening them onto one code would lose exactly the
// information §16 assigns a blast radius to.
func (cp *computation) liftDiagnostics(res *ccme.Result, commit string) {
	repository, revision := splitHistoryKey(commit)
	for _, d := range res.Diagnostics {
		level := LevelWarn
		if d.Severity == ccme.SeverityError {
			level = LevelError
		}
		cp.diags = append(cp.diags, Diagnostic{
			Code:       d.Code,
			Level:      level,
			Commit:     revision,
			Message:    d.Message,
			Repository: repository,
		})
	}
}

// ---------------------------------------------------------------------------
// §13.4a source packages
// ---------------------------------------------------------------------------

// sourcePackages is §13.4a. A unit's sources are its resolved scope-set minus
// every package whose contribution has been suppressed — but suppression only
// ever applies to *undischarged* work.
//
// Once a package has published the version carrying the unit, the artefact its
// consumers are owed is public and nothing landing afterwards retracts it:
// a later `cancel` on it is a no-op (W170) and a later `Release-As: none`
// stops only its future releases. Treating the two suppressors differently
// would be indefensible — §7.3 presents them as a ladder from weakest to
// strongest, and it would be perverse for the weaker to destroy an obligation
// the stronger leaves intact.
func (cp *computation) sourcePackages(rec *commitRec, i int) map[string]bool {
	out := make(map[string]bool)
	for name := range rec.scope[i] {
		// Discharged means published: the commit left the package's window
		// (stable release), or a prerelease of its train shipped it — the
		// window still contains it then, because the window is measured from
		// the stable tag, but the artefact is just as public.
		discharged := !cp.inWindow(name, rec.key) || cp.containedInBaseline(name, rec.key)
		if discharged || !(cp.cancelledFor(rec.key, name) || cp.held[name]) {
			out[name] = true
		}
	}
	return out
}
