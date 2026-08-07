package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// This file is §9.2, which is a three-phase procedure whose phases MUST NOT be
// merged:
//
//	phase 1  propagateChannels  — the channel axis, proposing a channel per package
//	phase 2  resolveChannels    — §13.8, settling channel(P) for every package
//	phase 3  propagateBumps     — the bump axis, admitted only where the origin's
//	                              release is resolvable by the target (§9.3a)
//
// The order is forced because phase 3 reads channel(P), which phase 2
// produces. There is no circularity in the other direction: phase 1 reads only
// the units and the packages' *baselines*, never a value computed in this run.
//
// The cost is a second pass over the unit list and a second set of graph
// traversals — not a second pass over history, which is walked once in §13.3
// and read by both. In a repository not running a prerelease train no unit
// carries a channel directive at all and phase 1 does nothing.

// target is one package reached by a traversal, with the source package it was
// reached from and the number of edges away it sits.
type target struct {
	name  string
	from  string
	level int
}

// walk is reach() from §9.2: a breadth-first traversal down dependency edges
// from a unit's source packages, bounded by depth.
//
// G1 (termination) holds because every package is marked seen at most once, so
// the walk performs at most |V| expansions regardless of depth, cycles or "+*".
// G5 (no blast-radius widening) holds because the traversal depends only on the
// graph at HEAD and the unit's own knobs — never on what any run published — so
// a later run can only ever admit a subset of the first run's targets, as
// windows advance.
//
// Two properties are easy to lose and both are normative. A package reachable
// by several paths is visited once, at its *shortest* depth. And depth is
// measured from the originating source set as a whole, never recomputed from an
// intermediate package: a unit written "+1" reaches exactly the direct
// consumers of its own packages, in this run and in every later catch-up run,
// whatever released in between.
func (cp *computation) walk(sources map[string]bool, depth int, kinds map[model.DepKind]bool) []target {
	seen := make(map[string]bool, len(sources))
	origin := make(map[string]string, len(sources))
	var out []target

	queue := make([]target, 0, len(sources))
	for _, s := range sortedKeys(sources) {
		seen[s] = true
		origin[s] = s
		queue = append(queue, target{name: s, from: s, level: 0})
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if depth != depthUnbounded && cur.level >= depth {
			continue
		}
		for _, e := range cp.edges[cur.name] {
			if kinds != nil && !kinds[e.kind] {
				continue
			}
			if seen[e.to] {
				continue
			}
			seen[e.to] = true
			origin[e.to] = origin[cur.name]
			next := target{name: e.to, from: origin[cur.name], level: cur.level + 1}
			out = append(out, next)
			queue = append(queue, next)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// phase 1: the channel axis (§9.2, §9.3)
// ---------------------------------------------------------------------------

// channelPick is a proposed channel for one package, with the unit that won.
type channelPick struct {
	channel  string
	provider string // the source package the proposal came from
	commit   string
}

func (cp *computation) propagateChannels() {
	for _, rec := range cp.commits { // newest commit first
		// §11.6 order: within a commit, the *last* unit wins, so it is visited
		// first and the first proposal recorded for a package is the winner.
		for i := len(rec.units) - 1; i >= 0; i-- {
			u := rec.units[i]
			if u.IsCancel() {
				continue
			}
			cprop := cp.unitChannelPropagation(u, rec)
			if cprop.inert() {
				continue
			}
			sources := cp.sourcePackages(rec, i)
			if len(sources) == 0 {
				continue
			}
			origin := cp.originChannel(u, sources, rec)

			reached, matched := 0, 0
			walked := cp.walk(sources, cprop.Depth, cprop.kinds)
			for _, t := range walked {
				if !cprop.allowsTarget(t.name) {
					continue
				}
				reached++
				// Admission is the TARGET's window, exactly as on the bump
				// axis (§13.3, fourth row).
				if !cp.window[t.name][rec.key] {
					continue
				}
				if cp.containedInBaseline(t.name, rec.key) {
					// The target's baseline prerelease already answered this
					// proposal — whatever channel it released on is the
					// answer. Re-resolving would re-emit W200/W207 for a
					// decision made when the target published.
					continue
				}
				if cp.cancelledFor(rec.key, t.name) { // §13.5a
					continue
				}
				base := cp.baselineChannel(t.name)
				p := resolveChannelValue(cprop.Value, base, origin, false)
				if p.target == "" {
					// W199 and an unmatched transition are ordinary
					// non-events and would be noise on every target; W200 is
					// reported because a suppressed graduation is a decision.
					if p.code == CodeChannelNoGraduate || p.code == CodeTransitionInert {
						cp.relWarn(t.name, p.code, rec.key, p.detail)
					}
					continue
				}
				matched++
				cp.proposeChannel(t.name, channelPick{
					channel:  p.target,
					provider: t.from,
					commit:   rec.key,
				})
			}

			if cprop.scoped && len(walked) > 0 && reached == 0 {
				cp.warn(CodeChannelScopeExcludedAll, "", rec.key,
					ccme.FooterPropagateChannelScope+" excluded every dependent this unit reached")
			}
			if cprop.Value.IsTransition() && reached > 0 && matched == 0 {
				cp.warn(CodeTransitionUnmatched, "", rec.key,
					"channel transition "+cprop.Value.String()+" matched no dependent it reached")
			}
		}
	}
}

// proposeChannel records a propagated channel, resolving conflicts by §9.3:
// the unit in the newest commit wins, then the last unit within it. Because
// the traversal visits units in exactly that order, the first proposal wins
// and any later one is the conflict.
func (cp *computation) proposeChannel(pkg string, pick channelPick) {
	prev, ok := cp.proposed[pkg]
	if !ok {
		cp.proposed[pkg] = pick
		return
	}
	if prev.channel != pick.channel {
		cp.relWarn(pkg, CodePropagatedChannelConflict, pick.commit,
			fmt.Sprintf("conflicting propagated channels %q and %q; the newer %q wins",
				prev.channel, pick.channel, prev.channel))
	}
}

// originChannel resolves "inherit" per §9.3.
//
// In a catch-up run the originating packages are typically no longer in the
// plan, so "the channel of the origin's release" has to be defined without
// one. Every input is read from the unit and from tags at HEAD, so the result
// does not depend on which earlier run published the origin, and does not
// depend on any other unit.
func (cp *computation) originChannel(u *ccme.Unit, sources map[string]bool, rec *commitRec) string {
	names := sortedKeys(sources)
	if len(names) == 0 {
		return ccme.ChannelStable
	}
	value := func(name string) string {
		base := cp.baselineChannel(name)
		if !u.Directives.ChannelSet {
			return base
		}
		// A direct Channel directive on the unit does graduate its own
		// packages, so it is resolved with graduates=true.
		if p := resolveChannelValue(u.Directives.Channel, base, base, true); p.target != "" {
			return p.target
		}
		return base
	}
	first := value(names[0])
	for _, name := range names[1:] {
		if value(name) != first {
			cp.warn(CodePropagatedChannelConflict, "", rec.key,
				fmt.Sprintf("source packages disagree about the channel to inherit; using %q from %q",
					first, names[0]))
			break
		}
	}
	return first
}

// ---------------------------------------------------------------------------
// phase 2: channel resolution (§13.8)
// ---------------------------------------------------------------------------

// channelCandidate is one direct Channel directive applying to one package.
type channelCandidate struct {
	value  ccme.ChannelValue
	commit string
}

// resolveChannels determines channel(P) for *every* package in the workspace,
// whether or not it will be released.
//
// The candidate lists are built by pushing from units to the packages they
// resolve to, rather than by rescanning the union window once per package: the
// latter is O(P·U) and does that work for the great majority of packages that
// no Channel directive names at all.
func (cp *computation) resolveChannels() {
	// ---- pass 1: push from units, in §11.6 precedence order ----
	cands := make(map[string][]channelCandidate)
	for _, rec := range cp.commits { // newest commit first
		for i := len(rec.units) - 1; i >= 0; i-- { // last unit within it first
			u := rec.units[i]
			if u.IsCancel() || !u.Directives.ChannelSet {
				continue
			}
			for _, name := range sortedKeys(rec.scope[i]) {
				if !cp.window[name][rec.key] { // §13.4a
					continue
				}
				if cp.containedInBaseline(name, rec.key) {
					// The directive's work is recorded in the baseline tag it
					// produced: the package IS on the channel it asked for.
					// Re-considering it would re-warn "already on beta" (W199)
					// on every run until graduation — reporting the mechanism
					// working as an anomaly.
					continue
				}
				if cp.cancelledFor(rec.key, name) {
					continue
				}
				cands[name] = append(cands[name], channelCandidate{
					value:  u.Directives.Channel,
					commit: rec.key,
				})
			}
		}
	}

	// ---- pass 2: read, once per package ----
	for _, p := range cp.pkgs {
		base := cp.baselineChannel(p.Name)
		if direct, ok := cp.directChannelFor(p.Name, cands[p.Name], base); ok {
			cp.channel[p.Name] = direct
			continue
		}
		// A direct directive beats every propagated one regardless of age;
		// only in its absence does a propagated channel apply.
		if pick, ok := cp.proposed[p.Name]; ok {
			cp.channel[p.Name] = pick.channel
			cp.channelFrom[p.Name] = pick.provider
			continue
		}
		cp.channel[p.Name] = base
	}
}

// directChannelFor picks the winning direct Channel directive for one package.
//
// The whole candidate list is examined before a winner is returned, because
// W186 counts candidates that actually propose something — a transition that
// does not match, or a value equal to the package's current channel, is not a
// competitor at all and must not be counted.
func (cp *computation) directChannelFor(pkg string, cands []channelCandidate, base string) (string, bool) {
	if len(cands) == 0 { // the overwhelmingly common case
		return "", false
	}
	type proposal struct {
		channel string
		commit  string
	}
	var proposals []proposal
	for _, c := range cands {
		// graduates=true: a direct directive is the deliberate, reviewable way
		// to end a train, and is the only non-transition form that may (§11.5).
		p := resolveChannelValue(c.value, base, base, true)
		if p.target == "" {
			// A direct directive that proposes nothing is worth reporting:
			// the author wrote it and it did not do what it looks like it
			// does. The propagated case is deliberately quieter — there the
			// same finding would repeat for every target reached.
			//
			// An unmatched transition is the exception on both axes. It is
			// not a failed directive but the mechanism working: a transition
			// is matched against the baseline precisely so that packages
			// which already moved are skipped, which is what makes the same
			// directive correct on the first run and the fifth.
			if p.code != "" && p.code != CodeTransitionUnmatched {
				cp.relWarn(pkg, p.code, c.commit, p.detail)
			}
			continue
		}
		proposals = append(proposals, proposal{channel: p.target, commit: c.commit})
	}
	if len(proposals) == 0 {
		return "", false
	}
	if len(proposals) > 1 {
		cp.relWarn(pkg, CodeChannelConflict, proposals[0].commit,
			fmt.Sprintf("conflicting channel directives %q and %q; the newer %q wins",
				proposals[0].channel, proposals[1].channel, proposals[0].channel))
	}
	return proposals[0].channel, true
}

// ---------------------------------------------------------------------------
// phase 3: the bump axis (§9.2, §9.3a)
// ---------------------------------------------------------------------------

func (cp *computation) propagateBumps() {
	for _, rec := range cp.commits {
		for i, u := range rec.units {
			if u.IsCancel() || u.Bump == ccme.BumpNone {
				// The bump axis requires a bump: a unit whose type maps to
				// none propagates no bump at any depth. The channel axis does
				// not, which is what makes `release` usable for graduation.
				continue
			}
			prop := cp.unitPropagation(u, rec)
			if prop.inert() { // "^none", "+0" — already warned by the parser
				continue
			}
			sources := cp.sourcePackages(rec, i)
			if len(sources) == 0 { // entirely suppressed
				continue
			}

			// §9.3a, hoisted out of the target loop. The predicate reads the
			// target only in the membership test below; everything else is a
			// function of the source set alone, and re-deriving it per target
			// is the one place in §9.2 where a naive transcription is
			// quadratic in quantities that are both large.
			srcChan := make(map[string]bool, len(sources))
			anyStable := false
			for name := range sources {
				ch := cp.channel[name]
				srcChan[ch] = true
				if ch == ccme.ChannelStable {
					anyStable = true
				}
			}

			reached := 0
			walked := cp.walk(sources, prop.Depth, prop.kinds)
			for _, t := range walked {
				if !prop.allowsTarget(t.name) { // Propagate-Scope (§8.5)
					continue
				}
				reached++
				// Admission is against the TARGET's window. This one test is
				// the whole of catch-up: a consumer that missed a run still
				// has the commit pending, so it is still admitted, whatever
				// the source has since released (§13.7a, G2).
				if !cp.window[t.name][rec.key] {
					continue
				}
				if cp.cancelledFor(rec.key, t.name) { // §13.5a
					continue
				}
				// A contribution the target's baseline prerelease already
				// published bypasses the §9.3a gate: the resolvability
				// question was settled when it shipped, its bump must keep
				// counting toward the train's target, and re-warning W208
				// about it would report a done deal as a suppression.
				if !cp.containedInBaseline(t.name, rec.key) &&
					!(anyStable || srcChan[cp.channel[t.name]]) {
					// §9.3a: a bump is a claim that the dependent has
					// something new to pick up, and across a channel boundary
					// that claim is false — the dependent goes on resolving
					// the origin by its stable range exactly as before.
					cp.relWarn(t.name, CodeBumpSuppressed, rec.key,
						fmt.Sprintf("propagated bump from %s suppressed: %s releases on %q, which %s (on %q) cannot resolve",
							t.from, t.from, cp.channel[t.from], t.name, cp.channel[t.name]))
					continue
				}
				rel := cp.rel[t.name]
				rel.PropagatedBump = ccme.MaxBump(rel.PropagatedBump, prop.Bump)
				if !cp.containedInBaseline(t.name, rec.key) {
					rel.NewWork = true
				}
				rel.Sources = append(rel.Sources, StaleSource{
					Provider: t.from,
					Commit:   rec.key,
					Level:    t.level,
					Bump:     prop.Bump,
				})
			}

			if prop.scoped && len(walked) > 0 && reached == 0 {
				cp.warn(CodeScopeExcludedAll, "", rec.key,
					ccme.FooterPropagateScope+" excluded every dependent this unit reached")
			}
		}
	}

	for _, rel := range cp.rel {
		sort.SliceStable(rel.Sources, func(i, j int) bool {
			return rel.Sources[i].Provider < rel.Sources[j].Provider
		})
		rel.DueTo = dedupeProviders(rel.Sources)
	}
}

// dedupeProviders lists the distinct origin packages of a set of
// contributions, in name order.
func dedupeProviders(src []StaleSource) []string {
	if len(src) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(src))
	out := make([]string, 0, len(src))
	for _, s := range src {
		if seen[s.Provider] {
			continue
		}
		seen[s.Provider] = true
		out = append(out, s.Provider)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinSorted(m map[string]bool) string { return strings.Join(sortedKeys(m), ",") }
