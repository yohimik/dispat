// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
)

// ---------------------------------------------------------------------------
// §13.4b apply corrections
// ---------------------------------------------------------------------------
//
// A correction is an ordinary unit carrying an `Edits` or `Deletes` footer
// (§7.4). It releases like any other unit and additionally rewrites the pending
// records it names: `Deletes` discards them, `Edits` discards them and stands in
// their place.
//
// The pass rewrites the tuple stream of §13.4 in place, which is what lets every
// later phase stay ignorant of corrections. A tuple is a (package, commit, unit)
// triple, and the stream is held as `commitRec.scope[i]`: the set of packages
// unit i addresses. Discarding a record is therefore removing one package from
// one unit's scope-set, never removing the unit. Removing the unit would
// renumber its siblings, and `#n` selectors are written against the message.

// correctionKind discriminates the two footers of §7.4.
type correctionKind uint8

const (
	kindEdit correctionKind = iota
	kindDelete
)

func (k correctionKind) String() string {
	if k == kindEdit {
		return "Edits"
	}
	return "Deletes"
}

// dropKey identifies one (package, record) pair of the tuple stream.
//
// idx is the target unit's ccme index, which is its position in the *message*.
// rec.units holds only the units that parsed, so the two differ as soon as a
// commit carries an invalid unit, and the selector grammar of §7.4.1 counts
// message positions.
type dropKey struct {
	pkg    string
	commit string
	idx    int
}

// resolvedTarget is one correction footer value after phase 1 of §13.4b.
//
// rec is nil when the value named a commit outside every pending window: real
// history, but no record left to act on. unit is nil when the named unit failed
// to parse, which is the same situation seen from the other side. Both end in
// W209 rather than in an error.
type resolvedTarget struct {
	kind     correctionKind
	wildcard bool
	key      string
	rec      *commitRec
	pos      int
	unit     *ccme.Unit
	raw      string
}

// correctionRec is one correction unit with its targets resolved and its
// effective scope settled (§7.4.2).
type correctionRec struct {
	rec     *commitRec
	pos     int
	unit    *ccme.Unit
	targets []resolvedTarget
	scope   map[string]bool
	label   string
}

// commitResolver resolves an abbreviated sha against the whole repository.
//
// It is a capability rather than a method on gitx.Git because only the real
// implementation can answer it: an in-memory fake knows the commits it was
// given and nothing else. Without it the pass matches prefixes against the
// commits it examined, which is exact for a full sha and for any target still
// pending somewhere (the only targets a correction can act on), and reports an
// abbreviation of an already-released commit as unknown (E210) where the truth
// is "discharged" (W209).
type commitResolver interface {
	ResolveCommit(ctx context.Context, rev string) (string, error)
}

// isCorrection reports a unit carrying either correction footer. The parser has
// already rejected the two control types (E171 on `cancel`, E173 on `release`),
// so no type check is needed here.
func isCorrection(u *ccme.Unit) bool {
	return len(u.Directives.Edits)+len(u.Directives.Deletes) > 0
}

// applyCorrections is §13.4b.
//
// It runs after collectCancels rather than before it, which the specification's
// ordering permits and case 119 of §15.7 requires: a correction whose target a
// cancel barrier already covers has nothing to act on and must say so (W209),
// and that answer needs the barriers. The two passes do not interfere, because
// a correction can never discard a control unit: a sha target that names one is
// E212 and the wildcard skips them. The cancel set is therefore the same before
// and after this pass.
func (cp *computation) applyCorrections() {
	corr := cp.resolveCorrections()
	if len(corr) == 0 {
		return
	}
	cp.dropRecords(corr)
	cp.commitDrops()
}

// resolveCorrections is phases 1 and 2 of §13.4b: every correction unit's
// targets resolved, and its effective scope settled.
//
// It walks oldest first, the reverse of the apply pass. A correction unit's
// effective scope can be inherited from its targets, and a target can itself be
// a correction, so the target's scope has to be settled before the correction
// naming it asks for it. Targets are proper ancestors, which makes oldest-first
// well-founded at any nesting depth.
func (cp *computation) resolveCorrections() []*correctionRec {
	var corr []*correctionRec
	for i := len(cp.commits) - 1; i >= 0; i-- {
		rec := cp.commits[i]
		for pos, u := range rec.units {
			if !isCorrection(u) {
				continue
			}
			c := cp.resolveCorrection(rec, pos, u)
			if c == nil {
				continue
			}
			corr = append(corr, c)
		}
	}
	// The apply pass reads §11.6 precedence order: newest commit first, and
	// within a commit the last unit first.
	sort.SliceStable(corr, func(a, b int) bool {
		if corr[a].rec.rank != corr[b].rec.rank {
			return corr[a].rec.rank < corr[b].rec.rank
		}
		return corr[a].pos > corr[b].pos
	})
	return corr
}

// resolveCorrection resolves one correction unit, or returns nil when a
// unit-scoped error voids it. A voided unit contributes nothing at all, so its
// scope-set is emptied: §7.4.2 gives E210 through E213 the blast radius of a
// parse failure.
func (cp *computation) resolveCorrection(rec *commitRec, pos int, u *ccme.Unit) *correctionRec {
	c := &correctionRec{rec: rec, pos: pos, unit: u}
	label := joinSorted(rec.scope[pos])

	for _, t := range correctionFooters(u) {
		rt, err := cp.resolveTarget(rec, t)
		if err != nil {
			cp.err(err.code, label, rec.key, err.message)
			rec.scope[pos] = map[string]bool{}
			return nil
		}
		c.targets = append(c.targets, rt)
	}

	if !cp.reconcileScope(c) {
		rec.scope[pos] = map[string]bool{}
		return nil
	}
	// The effective scope replaces §6 resolution for this unit (§13.4b), so the
	// correction's own record enters the stream under it exactly as a written
	// scope-set of that value would have.
	rec.scope[pos] = c.scope
	c.label = joinSorted(c.scope)
	return c
}

// correctionFooters returns the unit's correction targets in written order.
//
// The parser splits them into two slices by kind, but §13.4b resolves them in
// the order they were written, and a unit may mix the two footers freely. The
// footer block still carries the written order, so the two slices are consumed
// against it.
func correctionFooters(u *ccme.Unit) []taggedTarget {
	out := make([]taggedTarget, 0, len(u.Directives.Edits)+len(u.Directives.Deletes))
	var edits, deletes int
	for _, f := range u.Footers {
		switch f.CanonicalKey {
		case ccme.FooterEdits:
			if edits < len(u.Directives.Edits) {
				out = append(out, taggedTarget{kind: kindEdit, target: u.Directives.Edits[edits]})
				edits++
			}
		case ccme.FooterDeletes:
			if deletes < len(u.Directives.Deletes) {
				out = append(out, taggedTarget{kind: kindDelete, target: u.Directives.Deletes[deletes]})
				deletes++
			}
		}
	}
	// A directive that never appeared as a footer cannot happen today, but
	// dropping one silently would turn a parser change into a correction that
	// quietly stops applying.
	for ; edits < len(u.Directives.Edits); edits++ {
		out = append(out, taggedTarget{kind: kindEdit, target: u.Directives.Edits[edits]})
	}
	for ; deletes < len(u.Directives.Deletes); deletes++ {
		out = append(out, taggedTarget{kind: kindDelete, target: u.Directives.Deletes[deletes]})
	}
	return out
}

// taggedTarget is one written correction value together with its footer's kind.
type taggedTarget struct {
	kind   correctionKind
	target ccme.CorrectionTarget
}

// correctionError is a unit-scoped error carrying the code to report it under.
type correctionError struct {
	code    string
	message string
}

func (e *correctionError) Error() string { return e.code + ": " + e.message }

// resolveTarget is one line of phase 1: a footer value turned into the record
// it names, or the error that voids the whole unit.
func (cp *computation) resolveTarget(rec *commitRec, t taggedTarget) (resolvedTarget, *correctionError) {
	out := resolvedTarget{kind: t.kind, raw: t.target.Raw}
	if t.target.IsWildcard() {
		out.wildcard = true
		return out, nil
	}

	full, err := cp.resolveSHA(t.target.SHA)
	if err != nil {
		return out, err
	}
	// Proper ancestor, never self: this is what lets one commit discard old
	// records and supply their restatement together (§7.4.2).
	if full == rec.key || !cp.ancestorOrSelf(full, rec.key) {
		return out, &correctionError{CodeCorrectionUnknownTarget, fmt.Sprintf(
			"%s: %s is not a proper ancestor of this commit; a correction reaches only earlier commits (§7.4.2)",
			t.kind, t.target.Raw)}
	}
	out.key = full
	out.pos = -1

	target := cp.byKey[full]
	if target == nil {
		// A real commit that no pending window still holds. There is no record
		// left to act on, which the apply pass reports as W209.
		return out, nil
	}
	out.rec = target

	n := t.target.UnitSelector
	if n == 0 {
		if target.unitCount > 1 {
			return out, &correctionError{CodeCorrectionBadSelector, fmt.Sprintf(
				"%s: %s names a commit carrying %d units; name the unit, as %s#1 (§7.4.1)",
				t.kind, t.target.Raw, target.unitCount, t.target.SHA)}
		}
		n = 1
	}
	if n > target.unitCount {
		return out, &correctionError{CodeCorrectionBadSelector, fmt.Sprintf(
			"%s: unit selector %d is out of range; %s carries %d unit(s) (§7.4.1)",
			t.kind, n, t.target.SHA, target.unitCount)}
	}
	for i, u := range target.units {
		if u.Index != n-1 {
			continue
		}
		if u.IsControl() {
			return out, &correctionError{CodeCorrectionControlTarget, fmt.Sprintf(
				"%s: %s names a %s unit, which carries no record to correct (§7.4.2)",
				t.kind, t.target.Raw, u.Header.Type)}
		}
		out.pos = i
		out.unit = u
		break
	}
	// No match means the named unit failed to parse and contributes nothing
	// already, which is a no-op rather than an error.
	return out, nil
}

// resolveSHA turns a written target sha into a full commit key.
//
// Answers are memoised: the same target named twice, in one unit or across a
// history, costs one lookup.
func (cp *computation) resolveSHA(sha string) (string, *correctionError) {
	if _, ok := cp.byKey[sha]; ok {
		return sha, nil
	}
	if full, ok := cp.shaCache[sha]; ok {
		if full == "" {
			return "", unknownTarget(sha)
		}
		return full, nil
	}

	full, ok := cp.lookupSHA(sha)
	if cp.shaCache == nil {
		cp.shaCache = make(map[string]string)
	}
	cp.shaCache[sha] = full
	if !ok {
		return "", unknownTarget(sha)
	}
	return full, nil
}

func unknownTarget(sha string) *correctionError {
	return &correctionError{CodeCorrectionUnknownTarget, fmt.Sprintf(
		"%s names no commit, or names more than one; a correction target is a full or unambiguous abbreviated sha (§7.4.1)", sha)}
}

// lookupSHA asks git, and falls back to matching the abbreviation against the
// commits already examined. The fallback is exact for every target a correction
// can still act on, which is what makes a Git implementation without the
// capability usable rather than wrong.
func (cp *computation) lookupSHA(sha string) (string, bool) {
	if r, ok := cp.git.(commitResolver); ok {
		full, err := r.ResolveCommit(cp.ctx, sha)
		if err != nil || full == "" {
			return "", false
		}
		return full, true
	}
	var found string
	for key := range cp.byKey {
		if !strings.HasPrefix(key, sha) {
			continue
		}
		if found != "" {
			return "", false // ambiguous
		}
		found = key
	}
	return found, found != ""
}

// reconcileScope is phase 2 of §13.4b: containment, not equality.
//
// It reports whether the unit survives; E213 voids it.
func (cp *computation) reconcileScope(c *correctionRec) bool {
	var shaSets []map[string]bool
	var sha bool
	for _, t := range c.targets {
		if t.wildcard {
			continue
		}
		sha = true
		if t.unit != nil {
			shaSets = append(shaSets, t.rec.scope[t.pos])
		}
	}

	if !c.unit.Header.HasScopeSet {
		// The recommended form. A correction commit is typically empty, so the
		// file-derived fallback of §6.2 would resolve to nothing while the
		// targets already say which packages are meant (§7.4.2).
		c.scope = make(map[string]bool)
		switch {
		case !sha:
			// Wildcard-only: the reach defaults to the whole workspace.
			for _, name := range cp.order {
				c.scope[name] = true
			}
		default:
			for _, set := range shaSets {
				for name := range set {
					c.scope[name] = true
				}
			}
		}
		return true
	}

	// A written scope-set stands, and must be contained in every sha target's
	// set. Narrowing is how a record scoped (*) is corrected for some of its
	// packages only; widening would extend someone else's record to packages it
	// never claimed.
	c.scope = c.rec.scope[c.pos]
	// One diagnostic per offending package, not one per target that omits it:
	// two targets missing the same package is one mistake in the scope-set.
	outside := make(map[string]bool)
	for _, set := range shaSets {
		for name := range c.scope {
			if !set[name] {
				outside[name] = true
			}
		}
	}
	for _, name := range sortedKeys(outside) {
		cp.err(CodeCorrectionWidens, name, c.rec.key, fmt.Sprintf(
			"correction names %s, which its target's record does not; a correction may narrow a record, never widen it (§7.4.2)", name))
	}
	return len(outside) == 0
}

// dropRecords is phase 3 of §13.4b: the corrections applied newest first, each
// claiming the records it names.
//
// Nothing is removed from the stream here. Claims accumulate in cp.dropped and
// commitDrops applies them, so that every correction reads the same unclaimed
// stream and "already claimed by a newer correction" stays a question about
// cp.dropped rather than about iteration order.
func (cp *computation) dropRecords(corr []*correctionRec) {
	trace := cp.log.Trace().Enabled()
	voided := 0

	for _, c := range corr {
		if len(c.scope) == 0 {
			// The correction reached no package at all: every sha target named
			// a commit no pending window still holds, so there was nothing for
			// it to inherit a scope from. Reporting it is the whole point of
			// W209 being non-suppressible, and skipping straight to the next
			// correction here would make it the one shape that fails silently.
			cp.reportEmptyReach(c)
			continue
		}
		live := make(map[string]bool, len(c.scope))
		for _, name := range sortedKeys(c.scope) {
			if cp.dropped[dropKey{name, c.rec.key, c.unit.Index}] != nil {
				// The correction's own record is gone for this package, so the
				// correction is void there: none of its effects apply, exactly
				// as if the unit had failed to parse (§7.4.2).
				voided++
				cp.warn(CodeCorrectionVoid, name, c.rec.key,
					"correction is void here: a newer correction discarded its own record for this package (§7.4.2)")
				continue
			}
			live[name] = true
		}
		if len(live) == 0 {
			// Fully void. W215 has already named every package, so there is
			// nothing left to say.
			continue
		}
		if trace {
			cp.log.Trace().
				Str("commit", shortKey(c.rec.key)).
				Int("unit", c.unit.Index).
				Str("scope", c.label).
				Strs("targets", targetLabels(c.targets)).
				Msg("plan: correction resolved")
		}
		for _, t := range c.targets {
			if t.wildcard {
				cp.dropPending(c, t, live, trace)
				continue
			}
			cp.dropTarget(c, t, live, trace)
		}
	}

	cp.log.Debug().
		Int("corrections", len(corr)).
		Int("dropped", len(cp.dropped)).
		Int("voided", voided).
		Msg("plan: corrections applied")
}

// reportEmptyReach reports a correction that addresses no package: one W209
// per target, naming what it looked for.
func (cp *computation) reportEmptyReach(c *correctionRec) {
	for _, t := range c.targets {
		cp.warn(CodeCorrectionNoop, "", c.rec.key, fmt.Sprintf(
			"%s: %s addresses no pending record; released work is history and cannot be corrected (§7.4.2)", t.kind, t.raw))
	}
}

// dropTarget claims one named record, per package.
func (cp *computation) dropTarget(c *correctionRec, t resolvedTarget, live map[string]bool, trace bool) {
	if t.unit == nil {
		// Either the commit left every pending window, or the named unit did
		// not parse. Both mean there is nothing left to correct.
		cp.warn(CodeCorrectionNoop, c.label, c.rec.key, fmt.Sprintf(
			"%s: %s addresses no pending record; released work is history and cannot be corrected (§7.4.2)", t.kind, t.raw))
		return
	}

	var claimed []string
	for _, name := range sortedKeys(t.rec.scope[t.pos]) {
		if !live[name] {
			continue
		}
		if !cp.pending(name, t.key) {
			cp.warn(CodeCorrectionNoop, name, c.rec.key, fmt.Sprintf(
				"%s: %s is already released for this package; a correction reaches only undischarged work (§7.4.2)", t.kind, t.raw))
			continue
		}
		key := dropKey{name, t.key, t.unit.Index}
		if prev := cp.dropped[key]; prev != nil {
			// A unit naming one target twice is redundant rather than
			// superseded: §7.4.1 collapses several targets into the one
			// carrying record, so there is no older correction to report.
			if prev != c {
				cp.warn(CodeCorrectionSuperseded, name, c.rec.key, fmt.Sprintf(
					"%s: %s was already corrected by the newer commit %s; the newest correction wins (§7.4.2)",
					t.kind, t.raw, shortKey(prev.rec.key)))
			}
			continue
		}
		cp.dropped[key] = c
		claimed = append(claimed, name)
		if trace {
			cp.log.Trace().
				Str("commit", shortKey(c.rec.key)).
				Str("package", name).
				Str("target", shortKey(t.key)).
				Str("kind", t.kind.String()).
				Msg("plan: record discarded")
		}
	}
	if t.kind == kindEdit {
		cp.markRestatement(c, t, claimed)
	}
}

// dropPending claims every pending record a wildcard reaches.
//
// The reach is every proper ancestor of the correction's own commit, which is
// where the wildcard differs from `cancel`: a cancel barrier is
// ancestor-or-self (§10.3), so it takes its own commit's units with it, while a
// correction never reaches its own commit.
//
// Control units are left alone. They carry no record to discard, which is what
// E212 says about naming one directly, and discarding them here would erase the
// cancel barriers and graduation directives the later phases run on.
func (cp *computation) dropPending(c *correctionRec, t resolvedTarget, live map[string]bool, trace bool) {
	var claimed []string
	for _, rec := range cp.commits {
		if rec.key == c.rec.key || !cp.ancestorOrSelf(rec.key, c.rec.key) {
			continue
		}
		for pos, u := range rec.units {
			if u.IsControl() {
				continue
			}
			for _, name := range sortedKeys(rec.scope[pos]) {
				if !live[name] || !cp.pending(name, rec.key) {
					continue
				}
				key := dropKey{name, rec.key, u.Index}
				if cp.dropped[key] != nil {
					continue // claimed by a newer correction; nothing to report
				}
				cp.dropped[key] = c
				claimed = append(claimed, name)
				if trace {
					cp.log.Trace().
						Str("commit", shortKey(c.rec.key)).
						Str("package", name).
						Str("target", shortKey(rec.key)).
						Str("kind", t.kind.String()).
						Msg("plan: record discarded")
				}
			}
		}
	}
	if len(claimed) == 0 {
		cp.warn(CodeCorrectionNoop, c.label, c.rec.key, fmt.Sprintf(
			"%s: * addresses no pending record in this scope (§7.4.2)", t.kind))
		return
	}
	if t.kind == kindEdit {
		cp.markRestatement(c, t, claimed)
	}
}

// markRestatement records that the carrying unit stands in for the target
// (§13.10), and reports a restatement that changes nothing.
func (cp *computation) markRestatement(c *correctionRec, t resolvedTarget, claimed []string) {
	if len(claimed) == 0 {
		return
	}
	label := "*"
	if !t.wildcard {
		label = shortKey(t.key)
		if t.unit != nil && t.rec.unitCount > 1 {
			label = fmt.Sprintf("%s#%d", label, t.unit.Index+1)
		}
	}
	for _, name := range claimed {
		if cp.corrects[name] == nil {
			cp.corrects[name] = make(map[*ccme.Unit][]string)
		}
		cp.corrects[name][c.unit] = append(cp.corrects[name][c.unit], label)
	}
	if t.unit != nil && sameRecord(c.unit, t.unit) {
		cp.warn(CodeCorrectionIdentical, c.label, c.rec.key, fmt.Sprintf(
			"Edits: %s restates it as the same type, marker and description; the record does not change (§7.4.2)", t.raw))
	}
}

// sameRecord reports two units that say the same thing about the work: what
// §7.4.2 compares a restatement against.
func sameRecord(a, b *ccme.Unit) bool {
	return a.Header.Type == b.Header.Type &&
		a.Breaking == b.Breaking &&
		a.Header.Description == b.Header.Description
}

// commitDrops applies the claims to the tuple stream. A discarded record is a
// package removed from one unit's scope-set, after which the unit is invisible
// to that package everywhere: no direct bump, no propagation on either axis, no
// channel, no changelog entry (§7.4.2).
func (cp *computation) commitDrops() {
	for key := range cp.dropped {
		rec := cp.byKey[key.commit]
		if rec == nil {
			continue
		}
		for pos, u := range rec.units {
			if u.Index == key.idx {
				delete(rec.scope[pos], key.pkg)
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// §7.3 reverted changelog entries
// ---------------------------------------------------------------------------

// suppressRevertedNotes implements the changelog half of §7.3.
//
// For the *bump*, `Reverts:` is informational: a feat! and a revert of it in one
// window still take the package to a major, because consumers may already have
// seen the feature in a prerelease and quietly downgrading would be worse. For
// the *changelog* it is not informational at all. The release contains neither
// the change nor its removal, so documenting either would describe work that is
// not there, and both entries go.
//
// It runs after §13.4b, which is what makes a discarded revert suppress
// nothing (§7.4.2): a correction that discarded the revert's record has already
// removed the package from the revert's scope, so the loop below never reaches
// it and the reverted entry returns.
func (cp *computation) suppressRevertedNotes() {
	trace := cp.log.Trace().Enabled()
	suppressed := 0

	for _, rec := range cp.commits {
		for pos, u := range rec.units {
			for _, raw := range u.Directives.Reverts {
				if !isTargetSHA(raw) {
					continue // W214 at parse; the footer stays informational
				}
				suppressed += cp.suppressOneRevert(rec, pos, u, raw, trace)
			}
		}
	}

	if suppressed > 0 {
		cp.log.Debug().Int("entries", suppressed).Msg("plan: reverted entries suppressed")
	}
}

// suppressOneRevert handles one well-formed Reverts value and returns how many
// entries it took out of the notes.
func (cp *computation) suppressOneRevert(rec *commitRec, pos int, u *ccme.Unit, raw string, trace bool) int {
	full, ok := cp.resolveRevertTarget(raw)
	// Ancestor-or-self, where a correction demands a proper ancestor: a revert
	// is a claim about code, and a commit may carry the inverse diff of
	// something in the same commit.
	if !ok || !cp.ancestorOrSelf(full, rec.key) {
		cp.warn(CodeRevertNonAncestor, joinSorted(rec.scope[pos]), rec.key, fmt.Sprintf(
			"Reverts: %s names no commit reachable from this one; the footer is informational (§7.3)", raw))
		return 0
	}
	target := cp.byKey[full]
	if target == nil {
		return 0 // released, so its entry is published history (§7.3)
	}

	n := 0
	for _, name := range sortedKeys(rec.scope[pos]) {
		if !cp.pending(name, rec.key) || !cp.pending(name, full) {
			continue
		}
		var hit bool
		for tpos, tu := range target.units {
			if tu.IsControl() || !target.scope[tpos][name] {
				continue
			}
			cp.suppressNote(name, tu)
			hit = true
			n++
		}
		if !hit {
			continue
		}
		// The revert's own entry goes with the entry it reverted: a changelog
		// carrying "revert X" with no X in it describes a removal the release
		// does not contain either.
		cp.suppressNote(name, u)
		n++
		cp.warn(CodeRevertSuppressed, name, rec.key, fmt.Sprintf(
			"revert and the entries of %s leave the changelog together; both still count toward the bump (§7.3)",
			shortKey(full)))
		if trace {
			cp.log.Trace().
				Str("package", name).
				Str("revert", shortKey(rec.key)).
				Str("target", shortKey(full)).
				Msg("plan: reverted entries suppressed")
		}
	}
	return n
}

func (cp *computation) suppressNote(pkg string, u *ccme.Unit) {
	if cp.noteDrops[pkg] == nil {
		cp.noteDrops[pkg] = make(map[*ccme.Unit]bool)
	}
	cp.noteDrops[pkg][u] = true
}

// resolveRevertTarget resolves a Reverts value without raising E210: an
// unresolvable target is one of §7.3's two degraded forms, not an error, and
// the caller reports it as W213.
func (cp *computation) resolveRevertTarget(sha string) (string, bool) {
	if _, ok := cp.byKey[sha]; ok {
		return sha, true
	}
	if full, ok := cp.shaCache[sha]; ok {
		return full, full != ""
	}
	full, ok := cp.lookupSHA(sha)
	if cp.shaCache == nil {
		cp.shaCache = make(map[string]string)
	}
	cp.shaCache[sha] = full
	return full, ok
}

// isTargetSHA is the sha shape of §7.4.1, which §7.3 shares: 7 to 64 lowercase
// hexadecimal characters. A `Reverts` value that fails it is W214 at parse and
// informational here, so dispat must not report it a second time.
func isTargetSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// pending reports that a package's window still holds a commit and that the
// commit is undischarged: not published under a prerelease of the package's
// current train, and not behind a cancel barrier.
//
// All three are the same question, "is there still a record here to act on",
// and a correction that finds no record reports W209 rather than acting.
func (cp *computation) pending(pkg, key string) bool {
	if !cp.window[pkg][key] || cp.containedInBaseline(pkg, key) {
		return false
	}
	return !cp.cancelledFor(key, pkg)
}

// shortKey abbreviates a commit key for a diagnostic. The keys are full shas,
// except for Git implementations that report no sha at all, whose synthetic
// keys are left as they are.
func shortKey(key string) string {
	if strings.HasPrefix(key, syntheticKeyPrefix) || len(key) <= 12 {
		return key
	}
	return key[:12]
}

func targetLabels(targets []resolvedTarget) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.kind.String()+": "+t.raw)
	}
	return out
}
