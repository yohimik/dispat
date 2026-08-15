package plan

import (
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// depthUnbounded is the "+*" / "all" depth of §8.3: the full transitive
// closure. It is spelled as ccme's own value so the two never drift apart.
const depthUnbounded = int(ccme.DepthAll)

// commitKey identifies a commit within a pending window.
//
// The SHA is the real identity. The message fallback exists only for Git
// implementations that do not report one; it is enough to keep windows
// distinct in that case, and its failure mode — two identical messages
// collapsing into one window entry — is confined to such implementations.
func commitKey(c gitx.Commit) string {
	if c.SHA != "" {
		return c.SHA
	}
	return "msg:" + c.Message
}

// ---------------------------------------------------------------------------
// versions
// ---------------------------------------------------------------------------

func versionLess(a, b ccme.Version) bool { return a.Compare(b) < 0 }

func sameCore(a, b ccme.Version) bool {
	return a.Major == b.Major && a.Minor == b.Minor && a.Patch == b.Patch
}

// ---------------------------------------------------------------------------
// unit accessors
// ---------------------------------------------------------------------------

// unitScopes returns the unit's scope-set and whether one was written at all.
// An unwritten set is resolved from the commit's changed files (§6.2), which
// is a different question from an empty one.
func unitScopes(u *ccme.Unit) (ccme.ScopeSet, bool) {
	return u.Header.Scopes, u.Header.HasScopeSet
}

// releaseAs is a unit's Release-As directive, already parsed by ccme.
type releaseAs struct {
	kind    ccme.ReleaseAsKind
	version ccme.Version
	raw     string
}

func (r releaseAs) isHold() bool  { return r.kind == ccme.ReleaseAsNone }
func (r releaseAs) isAuto() bool  { return r.kind == ccme.ReleaseAsAuto }
func (r releaseAs) isExact() bool { return r.kind == ccme.ReleaseAsExact }

func unitReleaseAs(u *ccme.Unit) (releaseAs, bool) {
	ra := u.Directives.ReleaseAs
	if ra == nil {
		return releaseAs{}, false
	}
	return releaseAs{kind: ra.Kind, version: ra.Version, raw: ra.Raw}, true
}

// ---------------------------------------------------------------------------
// propagation knobs (§8.2 - §8.5a)
// ---------------------------------------------------------------------------

// propagation is one unit's resolved bump-axis configuration.
//
// targets is the resolved Propagate-Scope (§8.5); nil means "every package",
// which is the default. kinds is the set of dependency edges the propagation
// travels (§8.4); nil means every kind.
type propagation struct {
	Bump  ccme.Bump
	Depth int

	targets map[string]bool
	scoped  bool
	kinds   map[model.DepKind]bool
}

func (p propagation) allowsTarget(name string) bool {
	return p.targets == nil || p.targets[name]
}

// reaches nobody reports the two inert shapes of §8.3b, which the parser has
// already warned about (W152 / W201) and which cost a traversal to discover.
func (p propagation) inert() bool { return p.Bump == ccme.BumpNone || p.Depth == 0 }

// channelPropagation is the same for the channel axis (§8.3a, §8.5a).
type channelPropagation struct {
	Value ccme.ChannelValue
	Depth int

	targets map[string]bool
	scoped  bool
	kinds   map[model.DepKind]bool
}

func (p channelPropagation) allowsTarget(name string) bool {
	return p.targets == nil || p.targets[name]
}

// inert reports a channel axis that reaches nobody: depth 0, or the explicit
// "none" value, which disables propagation whatever the depth (§8.3a).
func (p channelPropagation) inert() bool {
	return p.Depth == 0 || p.Value.Word == ccme.ChannelNone || p.Value.IsZero()
}

// kindSet maps ccme's dependency kinds onto the graph's. A nil result means
// every kind, which is what the "*" wildcard and an unrecognised list both
// denote.
func kindSet(kinds []ccme.DependencyKind) map[model.DepKind]bool {
	if len(kinds) == 0 {
		return nil
	}
	out := make(map[model.DepKind]bool, len(kinds))
	for _, k := range kinds {
		switch k {
		case ccme.KindAll:
			return nil
		case ccme.KindDependencies:
			out[model.KindDependencies] = true
		case ccme.KindDevDependencies:
			out[model.KindDevDependencies] = true
		case ccme.KindPeerDependencies:
			out[model.KindPeerDependencies] = true
		case ccme.KindOptionalDependencies:
			out[model.KindOptionalDependencies] = true
		}
	}
	return out
}

// unitPropagation resolves the bump axis for one unit.
//
// "inherit" is resolved against the unit's own bump here rather than at the
// point of use, so that §9.2's per-target loop reads a plain Bump.
func (cp *computation) unitPropagation(u *ccme.Unit, rec *commitRec) propagation {
	d := u.Directives
	p := propagation{
		Bump:  d.Propagate.Bump(u.Bump),
		Depth: int(d.Depth),
		kinds: kindSet(d.Kinds),
	}
	if d.PropagateScopeSet {
		res := cp.resolveScopeSet(d.PropagateScope, true, rec)
		cp.reportScope(res, rec, ccme.FooterPropagateScope)
		p.targets, p.scoped = res.packages, true
	}
	return p
}

// unitChannelPropagation resolves the channel axis for one unit.
//
// Propagate-Channel-Scope defaults to the unit's Propagate-Scope (§8.5a): a
// unit that restricts propagation once restricts both axes, which is nearly
// always the intent.
func (cp *computation) unitChannelPropagation(u *ccme.Unit, rec *commitRec) channelPropagation {
	d := u.Directives
	p := channelPropagation{
		Value: d.PropagateChannel,
		Depth: int(d.ChannelDepth),
		kinds: kindSet(d.Kinds),
	}
	switch {
	case d.PropagateChannelScopeSet:
		res := cp.resolveScopeSet(d.PropagateChannelScope, true, rec)
		cp.reportScope(res, rec, ccme.FooterPropagateChannelScope)
		p.targets, p.scoped = res.packages, true
	case d.PropagateScopeSet:
		res := cp.resolveScopeSet(d.PropagateScope, true, rec)
		p.targets, p.scoped = res.packages, true
	}
	return p
}
