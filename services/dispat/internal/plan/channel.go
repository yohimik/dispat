package plan

import (
	"strconv"

	"github.com/yohimik/dispat/pkg/ccme"
)

// channelOf derives a package's channel from a version (§11.1).
//
// The channel is derived from tags and from nothing else: no side file, no
// configuration, no value computed earlier in the same run. That is what makes
// the state recoverable from a bare clone, a wrong channel fixable by tagging,
// and — via §13.7c G7 — the channel axis convergent.
func channelOf(v ccme.Version, has bool) string {
	if !has || !v.IsPrerelease() {
		return ccme.ChannelStable
	}
	return v.Prerelease[0]
}

// matchesFrom implements the <from> side of a transition (§9.3). "*" matches
// any prerelease channel and never matches stable.
func matchesFrom(cur, from string) bool {
	if from == ccme.ChannelAnyPrerelease {
		return cur != ccme.ChannelStable
	}
	return cur == from
}

// channelProposal is one directive's answer for one package: the channel it
// proposes, or none at all, with the diagnostic that explains a refusal.
type channelProposal struct {
	target string // "" when nothing is proposed
	code   string // diagnostic to raise, "" when silent
	detail string
}

func proposes(target string) channelProposal { return channelProposal{target: target} }

func refuses(code, detail string) channelProposal {
	return channelProposal{code: code, detail: detail}
}

var noProposal = channelProposal{}

// resolveChannelValue applies one channel value to a package sitting on
// channel cur. It is the shared body of §9.3's propagatedChannelFor and
// §13.8's directChannelFor; the two differ only in the graduation rule, which
// the graduates flag selects.
//
// graduates=false is the propagated case: a non-transition value resolving to
// stable never graduates a dependent off a prerelease (W200). Graduation ends
// a train and publishes under a version consumers resolve by default, so it
// must not happen because an unrelated package's commit propagated a channel.
// A transition is the deliberate exception and bypasses the flag below: its
// author had to name the train being ended (`beta>stable`) in order to write
// it, and matching against the target's baseline is what keeps it precise —
// packages not on that train are simply unmatched. This is what makes the
// documented `release(core)@beta>stable@@beta>stable++*` form actually end
// the whole train, dependants included.
func resolveChannelValue(v ccme.ChannelValue, cur, origin string, graduates bool) channelProposal {
	var target string

	switch {
	case v.IsTransition():
		if v.From == v.To {
			return refuses(CodeTransitionInert,
				"transition "+v.String()+" has the same source and target")
		}
		if !matchesFrom(cur, v.From) {
			// Not a competitor at all: it proposes nothing for this package
			// and takes no part in the conflict rules (§11.6).
			return refuses(CodeTransitionUnmatched,
				"transition "+v.String()+" does not match channel "+cur)
		}
		target = v.To

	case v.Word == ccme.ChannelInherit:
		target = origin

	case v.Word == ccme.ChannelNone || v.IsZero():
		return noProposal

	default:
		target = v.To
	}

	if target == "" {
		return noProposal
	}
	if target == ccme.ChannelStable && cur != ccme.ChannelStable && !graduates && !v.IsTransition() {
		return refuses(CodeChannelNoGraduate,
			"a propagated "+ccme.ChannelStable+" would graduate this package off "+cur+
				"; write a transition to graduate deliberately")
	}
	// Compared against the *baseline* channel, never against a value computed
	// earlier in this run. This one comparison is what discharges the channel
	// axis (§13.7c G7): having arrived on beta, a package is already there, so
	// nothing further is proposed and it stops re-releasing.
	if target == cur {
		if target == ccme.ChannelStable {
			return refuses(CodeGraduateStable, "already on "+ccme.ChannelStable)
		}
		return refuses(CodeChannelRedundant, "already on "+cur)
	}
	return proposes(target)
}

// ---------------------------------------------------------------------------
// §11.4 prerelease versions
// ---------------------------------------------------------------------------

// prereleaseCounter reads the numeric counter of a prerelease version.
//
// §11.3 requires the counter to be a separate numeric identifier, because
// numeric identifiers compare numerically and a fused "beta10" compares as
// ASCII and misorders at ten. A tag that does not have one cannot be continued
// from, which is E182.
func prereleaseCounter(v ccme.Version) (uint64, bool) {
	if len(v.Prerelease) != 2 {
		return 0, false
	}
	n, err := strconv.ParseUint(v.Prerelease[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// withPrerelease builds "<core>-<channel>.<counter>".
func withPrerelease(core ccme.Version, channel string, counter uint64) ccme.Version {
	return ccme.Version{
		Major:      core.Major,
		Minor:      core.Minor,
		Patch:      core.Patch,
		Prerelease: []string{channel, strconv.FormatUint(counter, 10)},
	}
}

// nextPrerelease implements §11.4.
//
// The target is recomputed from the *stable* baseline on every run, which is
// why a breaking change arriving mid-train moves the whole train and resets
// the counter rather than continuing under a version that no longer describes
// the content. Continuing an existing counter requires all three of: a
// prerelease baseline, the same channel, and the same core.
func nextPrerelease(stable, baseline ccme.Version, hasBaseline bool, channel string, e ccme.Bump) (ccme.Version, bool) {
	target := stable.Bumped(e).Core()

	if !hasBaseline || !baseline.IsPrerelease() {
		return withPrerelease(target, channel, 0), true
	}
	if channelOf(baseline, true) != channel || !sameCore(baseline, target) {
		return withPrerelease(target, channel, 0), true
	}
	counter, ok := prereleaseCounter(baseline)
	if !ok {
		return ccme.Version{}, false // E182: the tag has no numeric counter
	}
	return withPrerelease(target, channel, counter+1), true
}
