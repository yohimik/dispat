// Package plan reads git history and computes the release plan: which
// packages changed, what their next versions and channels are, and in which
// order they must be processed.
//
// The implementation follows §13 of the release specification. Three
// properties of that text drive the whole design and are worth stating up
// front:
//
//   - Every question of the form "does this commit still count?" is answered
//     against a *pending window*, and which package's window is consulted
//     depends on the purpose. A unit bumps its own package when the commit is
//     in that package's window (§13.6); it bumps a dependent when the commit
//     is in the *dependent's* window (§13.7). Conflating the two silently
//     orphans consumers after a partial publish — the failure §13.7a exists to
//     prevent.
//
//   - Catch-up is therefore not a repair pass. There is no second traversal
//     and no timestamp comparison anywhere in this package; a consumer that is
//     behind is simply a consumer whose window still contains the commit, and
//     it falls out of the ordinary rule.
//
//   - The window is measured from the last *stable* tag, not the last tag of
//     any kind. For a package on the stable channel the two coincide; for one
//     on a prerelease train the window spans the whole train, which is exactly
//     what §11.4 needs to recompute the train's target on every run. Work the
//     train has already published — commits contained in the baseline
//     prerelease tag — still counts toward the bump but is discharged for
//     everything that asks "is this pending?": it cannot re-release the train
//     (NewWork), keep a Release-As in force, or be reached by a cancel.
//
// Propagation itself lives in propagate.go, because §9.2 is a three-phase
// procedure whose phases may not be merged.
package plan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/graph"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// Diagnostic codes emitted by Compute. The numbering is the registry of §16.
//
// Codes the parser owns (E1xx grammar, W1xx authoring) are lifted from ccme
// with their own codes rather than renumbered here; only the codes that
// require a workspace, a graph or tags are defined in this package, because
// those are precisely the ones ccme documents as out of its scope.
const (
	// --- scope resolution (§6.1) ---

	// CodeUnknownInclude rejects an explicit include naming a package that
	// does not exist at HEAD. A typo would silently drop a release.
	CodeUnknownInclude = "E130"
	// CodeUnknownScope marks an exclusion naming an unknown package.
	// Excluding something deleted or renamed is harmless and common.
	CodeUnknownScope = "W130"
	// CodeInertUnit marks a unit that resolved to zero packages.
	CodeInertUnit = "W131"
	// CodeEmptyGlob marks a glob scope term that matched nothing.
	CodeEmptyGlob = "W134"
	// CodeScopeExcludedAll marks a Propagate-Scope that excluded every
	// dependent the unit reached (§8.5).
	CodeScopeExcludedAll = "W135"
	// CodeChannelScopeExcludedAll is its channel-axis counterpart (§8.5a).
	CodeChannelScopeExcludedAll = "W205"

	// --- release control (§8.6) ---

	// CodeReleaseAsConflict reports two package-level Release-As directives in
	// one window; the newest won.
	CodeReleaseAsConflict = "W153"
	// CodeHeldVersion reports the version a held package *would* have been
	// released at, so the value needed to lift the hold does not have to be
	// computed by hand (§13.6a).
	CodeHeldVersion = "W154"
	// CodeAutoNoHold marks a `Release-As: auto` that lifted nothing.
	CodeAutoNoHold = "W158"
	// CodePinNotGreater rejects a `Release-As: <ver>` that does not move the
	// package forward (§8.6).
	CodePinNotGreater = "E153"
	// CodePinBelowBump rejects a `Release-As: <ver>` lower than the version
	// the accumulated bumps require (§8.6).
	CodePinBelowBump = "E156"

	// --- cancellation (§10) ---

	// CodeEmptyCancel marks a `cancel` that discarded nothing — usually the
	// sign that it named the provider when it meant the consumer (§13.7d).
	CodeEmptyCancel = "W170"

	// --- corrections (§7.4, §13.4b) and reverted changelogs (§7.3) ---
	//
	// The specification reserves these for the release engine: it validates the
	// shape of an `Edits` or `Deletes` footer and leaves resolving the target
	// against history, and everything that follows from it, here.

	// CodeCorrectionUnknownTarget marks a correction target that names no
	// commit, names more than one, or names a commit that is not a proper
	// ancestor of the correction's own. Unit-scoped: the correction contributes
	// nothing and its siblings still apply.
	CodeCorrectionUnknownTarget = "E210"
	// CodeCorrectionBadSelector marks a unit selector past the end of the target
	// commit, or a bare sha naming a commit that carries several units and so
	// does not say which record is meant (§7.4.1). Unit-scoped.
	CodeCorrectionBadSelector = "E211"
	// CodeCorrectionControlTarget marks a correction aimed at a `cancel` or
	// `release` unit. Neither carries a record that could be restated or
	// discarded (§7.4.2). Unit-scoped.
	CodeCorrectionControlTarget = "E212"
	// CodeCorrectionWidens marks a correction whose scope-set reaches a package
	// its target's record never claimed. Narrowing a record is how one scoped
	// `(*)` is corrected for some of its packages only; widening would extend
	// someone else's record, so it is refused. Unit-scoped.
	CodeCorrectionWidens = "E213"
	// CodeCorrectionNoop marks a correction that found nothing to act on: the
	// target is already released, already discarded, or a wildcard whose scope
	// holds nothing pending.
	//
	// Non-suppressible, and not by dispat's choice: an operator who writes a
	// correction has to be able to see that it did not take (§17.1). It is
	// dispat's own code rather than the parser's, so `--quiet-parser` cannot
	// reach it either.
	CodeCorrectionNoop = "W209"
	// CodeCorrectionSuperseded marks a correction of a target a newer correction
	// already claimed. The newest wins, by the same rule as §8.6.
	CodeCorrectionSuperseded = "W210"
	// CodeCorrectionIdentical marks an `Edits` restating its target as the same
	// type, breaking marker and description. The correction applies; it just
	// changes nothing.
	CodeCorrectionIdentical = "W211"
	// CodeRevertSuppressed marks a revert and its target leaving the changelog
	// together (§7.3). Both units still count toward the bump: the work happened
	// and was undone, and the version has to carry both halves.
	CodeRevertSuppressed = "W212"
	// CodeRevertNonAncestor marks a `Reverts` value naming a commit that is not
	// an ancestor of the revert. The footer stays informational and the revert
	// releases normally (§7.3).
	CodeRevertNonAncestor = "W213"
	// CodeCorrectionVoid marks a correction whose own record a newer correction
	// discarded for a package. It is void there: none of its effects apply, so
	// whatever it would have discarded stands (§7.4.2).
	CodeCorrectionVoid = "W215"

	// --- propagation and channels (§9, §11, §13.8) ---

	// CodePropagatedChannelConflict reports conflicting propagated channels;
	// the newest won (§9.3).
	CodePropagatedChannelConflict = "W160"
	// CodeGraduateStable marks a graduation of a package already on stable.
	CodeGraduateStable = "W185"
	// CodeChannelConflict reports conflicting direct channel directives; the
	// newest won (§11.6).
	CodeChannelConflict = "W186"
	// CodeChannelRedundant marks a proposed channel equal to the package's
	// current one; nothing is proposed (§9.3). This is what discharges the
	// channel axis (G7).
	CodeChannelRedundant = "W199"
	// CodeChannelNoGraduate marks a propagated `stable` that would have
	// graduated a dependent off a prerelease, suppressed (§9.3).
	CodeChannelNoGraduate = "W200"
	// CodeChannelOnly marks a release whose only cause is a channel change
	// (§13.9). Non-suppressible.
	CodeChannelOnly = "W202"
	// CodeChannelEntryPatch marks the channel-entry patch of §11.4.
	CodeChannelEntryPatch = "W204"
	// CodeTransitionUnmatched marks a channel transition that matched nothing.
	CodeTransitionUnmatched = "W206"
	// CodeTransitionInert marks a transition whose <from> equals its <to>.
	CodeTransitionInert = "W207"
	// CodeBumpSuppressed marks a propagated bump suppressed because no source
	// releases on a channel the dependent can resolve (§9.3a).
	// Non-suppressible.
	CodeBumpSuppressed = "W208"
	// --- versioning groups (see fixedgroup.go) ---
	//
	// W23x, alongside the selection codes below: a versioning group is a
	// dispat configuration rather than a specification concept, and §16's
	// registry runs to W215, so the group's diagnostics carry dispat's own
	// numbers.

	// CodeFixedAlign marks a release whose only cause is fixed versioning:
	// the package has no changes of its own and rides along to keep every
	// member of its versioning group on the shared version.
	// Non-suppressible: like W193 and W202 it explains a presence in the plan
	// that the commit log alone cannot account for.
	CodeFixedAlign = "W234"
	// CodeFixedPinConflict reports two exact Release-As pins competing for
	// one versioning group's shared version; the newest won.
	CodeFixedPinConflict = "W235"
	// CodeFixedChannelConflict reports members of a versioning group
	// resolving to different channels; the group can only move as one, so a
	// deterministic winner is picked.
	CodeFixedChannelConflict = "W236"
	// CodeFixedDepthConflict reports members of a versioning group that share
	// different parts of the version — a `fixed` space joined by a
	// `fixedMajor` package. The group versions on the deepest part any member
	// asks for, which satisfies all of them, and the warning is what explains
	// the sharing none of the shallower members declared.
	CodeFixedDepthConflict = "W237"
	// CodeNonePinned reports a Release-As directive whose scope resolved to a
	// versioning-none package. The directive is inert — a none package is
	// never versioned or released — and the warning is what tells the author
	// the footer they wrote moves nothing.
	CodeNonePinned = "W238"
	// CodeWebhookFailed reports a webhook delivery that did not get through:
	// the endpoint kept refusing, never answered, or the run had more events
	// than the delivery queue could hold. A warning rather than an error by
	// design — like W232, it concerns telling the world about work that
	// already happened, and a listener that missed a notification is never a
	// reason to fail the release it was watching.
	CodeWebhookFailed = "W239"

	// --- release outcomes (§13.7a, §13.9) ---

	// CodeCatchUp marks a release whose entire cause is propagation from a
	// package that is not itself in this run's plan (§13.7a).
	// Non-suppressible.
	CodeCatchUp = "W193"
	// CodeBlocked marks a package that was planned but not attempted because a
	// dependency failed to publish (§19.3). Non-suppressible.
	CodeBlocked = "W194"
	// --- manifests (§9.4, §12.4; emitted by the executor and by compute) ---

	// CodeManifestVersionDrift marks a manifest whose declared own version
	// disagrees with the package's baseline (§12.4): tags are authoritative,
	// the computed version is written over the drifted one.
	CodeManifestVersionDrift = "W192"
	// CodeRangeCatchUp marks a declared range reconciled against a provider
	// that is not releasing this run (§9.4): the manifest had fallen behind an
	// earlier release and auto-versioning caught it up.
	CodeRangeCatchUp = "W197"
	// CodeStableOverPrerelease marks a stable release whose manifest now
	// ranges over a prerelease provider (§9.4): legal, but a stable consumer
	// pinning a moving prerelease is worth an operator's glance.
	CodeStableOverPrerelease = "W203"
	// CodeAmbiguousManifestName marks two workspace packages whose manifests
	// declare the same name: the name-to-package mapping is ambiguous, so no
	// edges are derived from that name — by compute and by the executor's
	// auto-versioning alike.
	CodeAmbiguousManifestName = "W220"
	// CodeAmbiguousManifestVersion marks one package whose manifests declare
	// different versions for it. Which one the package is actually at is a
	// question the files disagree about, so `dispat compute` derives no
	// baseline from them and leaves the answer to the operator.
	CodeAmbiguousManifestVersion = "W225"
	// CodeUnscheduledRewriteEdge marks an auto-versioned manifest dependency
	// with no configured `dependencies` edge behind it: the scheduler cannot
	// order the consumer after this provider or skip it on the provider's
	// failure, so the rewritten range is optimistic about a publish that may
	// still fail. `dispat compute` derives the missing edge.
	CodeUnscheduledRewriteEdge = "W221"
	// CodeReplaceRuleMatchedNothing marks an autoVersion replace rule whose
	// text was found in none of the files it selected. A rule that reconciles
	// nothing is almost always a mistyped template or a stale glob, and it
	// would otherwise fail silently for as many releases as it took someone
	// to notice.
	CodeReplaceRuleMatchedNothing = "W222"

	// --- standalone step commands and re-runs ---

	// CodeChangelogEntryExists marks a changelog write skipped because the
	// file already carries the entry for the planned tag: a `dispat
	// changelog` invocation ran earlier in the flow (or the recorder is
	// re-running), and writing again would duplicate the entry.
	//
	// W226 rather than a number beside the two below it: this is the third of
	// the "already recorded, skip it" family and belongs with them, and the
	// W22x block above it was full.
	CodeChangelogEntryExists = "W226"
	// CodeTagExists marks a tag creation skipped because the release tag
	// already exists at the release's target commit: the flow tagged early
	// (`dispat commit --tag`), and the record the tag exists to be is already
	// durable. A tag at a different commit stays a hard error.
	CodeTagExists = "W223"
	// CodeGitHubReleaseExists marks a GitHub release skipped because the
	// repository already carries one for the planned tag: a `dispat github`
	// invocation ran earlier in the flow, or the run is a re-run after a
	// later stage failed. Creating it again is a 422 from the API, so the
	// skip is what makes both re-runnable.
	CodeGitHubReleaseExists = "W224"
	// CodeCommitIncludeMissing marks a commit.include path that names nothing
	// on disk. The path is simply not staged — `git add` would refuse it — but
	// silently, a typo'd path means an artifact the release commit was
	// supposed to carry never lands in it, so the miss is said out loud.
	CodeCommitIncludeMissing = "W227"
	// CodeStepAligned marks a step command whose own replan disagreed with the
	// release run that invoked it: the run's DISPAT_* environment is the
	// authority, so the step aligned its record to it and says so. The drift
	// itself is ordinary — earlier legs' tags move what a fresh plan computes —
	// which is exactly why the wiring exists (§13.8's records must be the
	// run's own).
	CodeStepAligned = "W228"
	// CodeStepUnalignable marks a step command inside a run whose environment
	// it cannot honor: the named package is not in its plan, the pinned
	// version does not parse, or the aligned version renders a different tag
	// than the run's. Nothing is written — a refusal here is a failed leg the
	// operator re-runs, where a drifted record would be an incident.
	CodeStepUnalignable = "E219"
	// CodeStepBeforeTag marks a wired `dispat github` running before the
	// run's tag exists: asked to release a tag nobody created, GitHub invents
	// it at the default branch head — a plausible-looking release pinned to
	// the wrong commit. The step proceeds (the flow may create the tag
	// another way), but the ordering smell is said out loud: the commit step
	// belongs before the github step.
	CodeStepBeforeTag = "W229"

	// --- releasing part of the graph (see narrow.go) ---
	//
	// W23x, not the W19x the release outcomes above live in: §16's registry
	// reserves W195 and W196 for a staleness audit and an adopted tag, and a
	// selection is dispat's own idea rather than the specification's.

	// CodeSelectionWithheld marks a selected package the release order cannot
	// reach in this run: a provider it depends on is releasing in the same plan
	// and the selection leaves that provider out. Releasing the consumer first
	// is the one staleness case publish order exists to prevent (§19.2,
	// §13.7b), so it stays behind and the next run releases it.
	// Non-suppressible: the selection asked for the package and did not get it.
	CodeSelectionWithheld = "W230"
	// CodeSelectionSplit marks a selection that releases part of a versioning
	// group. The members left behind keep their old version until the next run
	// rides them up to the group's (W234), so the group's shared version is
	// briefly untrue — deliberate, and worth saying out loud.
	CodeSelectionSplit = "W231"
	// CodeAliasTagFailed marks an alias tag that could not be written. The
	// release tag it accompanies is already there, so the release itself is
	// recorded; what is missing is a convenience ref, which is re-pointed by
	// hand or by the next release. A warning rather than a critical for
	// exactly that reason.
	CodeAliasTagFailed = "W232"
	// CodeFixedMajorSpread marks a versioning group whose members sit on
	// different major versions. The group versions from its newest member, so
	// the one furthest ahead decides where every other member lands, and a
	// single mis-tagged package can carry the whole group across a major
	// boundary that §19.1 then forbids undoing. This is E157's hazard without
	// E157's footer to hang an error on: the versions are all legitimately
	// published, so the group is released and the outlier is named.
	//
	// Members with no baseline at all are not a spread — a package joining a
	// group has no major to disagree with, and W234 already reports its ride.
	// Sparse members are exempt too: staying behind until they change is what
	// a sparse mode is for.
	CodeFixedMajorSpread = "W233"

	// --- after the point of no return ---
	//
	// E22x, above the E1xx/E200 range §16's registry defines and clear of the
	// correction errors it ends at (E213), for the same reason the W23x codes
	// sit where they do: these are dispat's own, and the specification has
	// nothing to say about them.
	//
	// Every one of these marks work that failed *after* something irreversible
	// already happened — a package published to its registry, a release commit
	// created. None of them fails a package or stops the run. A release that
	// is already out cannot be un-published by reporting it as failed, and a
	// run that gave up here would leave the rest of what it owed undone: the
	// remaining tags, the push, the GitHub releases. So each is recorded,
	// logged, and the run carries on to the end, where the collected failures
	// make the command exit non-zero.

	// CodeTagFailed marks a release tag that could not be created after its
	// package published. The package stays published — it is — but nothing
	// records the version, so the next run reads the package as never released
	// and would release the same version again. Worth fixing by hand before
	// the next run.
	CodeTagFailed = "E220"
	// CodeTagAtOtherCommit marks a release tag that already exists at a commit
	// that is not this release's. The tag is left exactly where it is: moving
	// it would rewrite a record another run made, and force-pushing the moved
	// tag would spread the mistake to the remote.
	CodeTagAtOtherCommit = "E221"
	// CodeRecordFailed marks a release record — a changelog entry, a GitHub
	// release — that could not be written after its package published. The
	// other recorders still run: a changelog failure is no reason to skip the
	// GitHub release too.
	CodeRecordFailed = "E222"
	// CodeCommitFailed marks a failed release commit. Tagging still follows:
	// the tags then point where the packages' exported commits or HEAD say,
	// which is where they would have pointed anyway.
	CodeCommitFailed = "E223"
	// CodePushFailed marks a failed push. The commit and the tags exist
	// locally, so the release is recorded; what is missing is the copy on the
	// remote, and a later push sends it.
	CodePushFailed = "E224"

	// --- the manifest-command gates ---
	//
	// E215 onward: `dispat scanner`'s verification gates. They continue the
	// range above because they are dispat's own codes too, but they behave
	// differently: a gate exists to stop a pipeline, so each of these fails
	// its command outright.

	// CodeLinkPresent marks a local-link directive --verify-unlinked found
	// still in place: a go.mod filesystem replace, a Cargo [patch.crates-io]
	// or uv [tool.uv.sources] path entry, a pubspec dependency_overrides path
	// or an npm file:/link: override. Exactly the directives --link-local can
	// inject, which is the gate's whole scope.
	CodeLinkPresent = "E215"
	// CodeLinkAbsent marks a selection --verify-linked found no directive in:
	// the link step this gate proves ran either did not run or wrote nothing.
	CodeLinkAbsent = "E216"
	// CodeRangeForbidden marks a declared dependency range --forbid-range
	// matched, `workspace:*` on the way to a registry being the canonical
	// case.
	CodeRangeForbidden = "E217"
	// CodeRangeMissing marks a --require-range pattern no declared dependency
	// range matched: the tree was supposed to be in a state it is not in.
	CodeRangeMissing = "E218"

	// --- release outcomes, repository-scoped (§16) ---

	// CodeBadPrereleaseTag rejects an existing prerelease tag whose counter is
	// not a numeric identifier (§11.3). Repository-scoped.
	CodeBadPrereleaseTag = "E182"
	// CodeGraduateNoIncrease rejects a graduation that would lower the
	// version (§11.5). Repository-scoped; only reachable from edited tags.
	CodeGraduateNoIncrease = "E185"
	// CodeVersionNotGreater rejects a computed version that does not exceed
	// the baseline (§13.9). Repository-scoped.
	CodeVersionNotGreater = "E195"
	// CodePinMultiPackage rejects an exact Release-As whose scope-set resolved
	// to more than one package (§8.6). ccme enforces the cases decidable from
	// the message alone; this is the one that needs the workspace, because a
	// glob's breadth is not visible in the text.
	CodePinMultiPackage = "E154"
	// CodePinMajorJump rejects an exact Release-As raising the major version
	// more than MaxMajorJump above the computed one (§14.1). It is a default,
	// not an opt-in: a fresh repository writing `Release-As: 5.0.0` against a
	// computed 1.5.0 gets it with no configuration involved.
	CodePinMajorJump = "E157"
	// CodeDuplicateVersionTag rejects two reachable tags that parse to the
	// same version of one package but point at different commits (§12.1): the
	// baseline selection is ambiguous, so no correct plan exists.
	// Repository-scoped.
	CodeDuplicateVersionTag = "E191"
	// CodeShallowRepository rejects a shallow or grafted repository (§16): an
	// incomplete history hides tags and commits, and every window computed
	// over it is wrong in ways nothing downstream can detect.
	// Repository-scoped.
	CodeShallowRepository = "E196"
	// CodeDependencyCycle rejects a configured dependency graph with a cycle
	// (§16): no publish order exists. Repository-scoped.
	CodeDependencyCycle = "E200"
)

// MaxMajorJump is the §14.1 default: an exact Release-As may raise the major
// version at most this far above the computed version.
const MaxMajorJump = 1

// repositoryScoped is the §16 bucket whose members mean the run cannot produce
// a correct plan at all.
//
// The distinction is the whole point of §16's blast radius. A unit-scoped
// error is an authoring mistake in one unit: that unit contributes nothing and
// its siblings still apply. A repository-scoped error is an integrity failure
// — a tag that cannot be read, a version that goes backwards, a cycle — with
// no offending unit to invalidate and no resolution available anywhere in the
// commit log. It is resolved by a human correcting the repository, after which
// the run is simply repeated, so no partial release may be emitted meanwhile.
var repositoryScoped = map[string]bool{
	CodeBadPrereleaseTag:    true, // E182
	CodeGraduateNoIncrease:  true, // E185
	CodeDuplicateVersionTag: true, // E191
	CodeVersionNotGreater:   true, // E195
	CodeShallowRepository:   true, // E196
	CodeDependencyCycle:     true, // E200
}

// IsRepositoryScoped reports whether a diagnostic code aborts the run whatever
// the configured error policy (§16).
func IsRepositoryScoped(code string) bool { return repositoryScoped[code] }

// Level separates advisory diagnostics from ones that must fail the run.
type Level int

const (
	LevelWarn Level = iota
	LevelError
)

// Diagnostic is one reportable observation about the plan.
type Diagnostic struct {
	Code    string
	Level   Level
	Pkg     string
	Commit  string
	Message string
}

func (d Diagnostic) String() string {
	if d.Pkg == "" {
		return fmt.Sprintf("%s: %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", d.Code, d.Pkg, d.Message)
}

// StaleSource is one provider contribution a package has not yet released. It
// is the consumer-side view of §9.2 that §13.7b asks implementations to offer:
// "which of my packages are behind their dependencies, and behind which?".
type StaleSource struct {
	Provider string    // the package the contribution came from
	Commit   string    // the commit carrying the unit
	Level    int       // hops from the provider to this package
	Bump     ccme.Bump // the bump the unit propagates
}

// ProviderUpdate is one provider whose version this release picks up, with
// the movement.
type ProviderUpdate struct {
	Name     string
	From, To ccme.Version
}

// Release describes what (if anything) will happen to one package.
type Release struct {
	Pkg *model.Package

	// Current is the stable baseline the next version is computed from: the
	// latest parseable stable tag; otherwise the configured initial version;
	// otherwise 0.0.0. Computing from the *stable* baseline is what makes G3
	// (version stability across re-runs) hold for prerelease trains too, and
	// what lets a breaking change arriving mid-train move the whole train.
	Current ccme.Version
	// Baseline is baseline(P) of §12.3: the newest tag of any kind,
	// prereleases included. It is what a computed version must exceed, and
	// what the package's channel is derived from.
	Baseline ccme.Version
	// HasBaseline reports whether any parseable tag exists.
	HasBaseline bool
	// Tagged reports whether a parseable *stable* release tag exists — the
	// counterpart of HasBaseline for the stable baseline Current comes from.
	Tagged       bool
	StableCommit string // commit of the stable baseline tag; "" when untagged
	// BaselineCommit is the commit of the baseline tag — the newest tag of any
	// kind. On a prerelease train it is ahead of StableCommit, and everything
	// at or behind it has already been published by the train; for a stable
	// package the two coincide.
	BaselineCommit string
	FromInitials   bool // Current came from the config initials

	OwnBump        ccme.Bump // direct(P), §13.6
	PropagatedBump ccme.Bump // propagated(P), §13.7
	Bump           ccme.Bump // effective(P) = max of the two

	// NewWork reports that at least one contributing commit — own or
	// propagated — is NOT already contained in the baseline tag. The window of
	// a package on a prerelease train spans the whole train, because §11.4
	// recomputes the train's target over it on every run; but the commits the
	// train has already published must not *re-release* it, or a train would
	// release beta.1, beta.2, ... forever from the same content. Bump is the
	// max over the whole window; NewWork is what makes it releasable. For a
	// stable package the window already excludes released commits, so any bump
	// implies NewWork.
	NewWork bool

	// Channel is channel(P) as resolved by §13.8; BaselineChannel is the
	// channel derived from the package's own baseline tag (§11.1). A plan
	// MUST show both where they differ — the transition a reader needs to see
	// is "beta -> stable", not the word "stable" alone (§13.10).
	Channel         string
	BaselineChannel string
	// ChannelFrom names the provider a propagated channel came from, empty
	// for a direct directive.
	ChannelFrom string

	Next  ccme.Version // version to release; equals Current when unchanged
	Units []*ccme.Unit // the package's own surviving units
	// FreshUnits are the units of Units whose commits are NOT contained in
	// the baseline tag: the changeset the baseline has not published yet. For
	// a stable package the window already excludes released commits, so the
	// two slices are equal; they differ only on a prerelease train, where
	// Units spans the whole train (§11.4 recomputes the target over it) and
	// FreshUnits is what the next prerelease actually adds.
	//
	// Because the two are equal off the train, a test or fixture that sets
	// only Units proves nothing about which of them a reader uses — every
	// pre-1.0.0 planner bug was a train-wide read passing exactly such
	// tests. A hand-built stable-line Release must set both, as a real plan
	// does, and anything user-facing reads the fresh side (NotesUnits,
	// FreshOwnBump) unless it can say why the train-wide value is the right
	// one.
	FreshUnits []*ccme.Unit

	DueTo   []string      // providers that forced (at least) part of the bump
	Sources []StaleSource // the same, with commit and depth detail
	// Updates is every provider whose version this release picks up:
	//
	//	Updates = DueTo ∪ { configured providers releasing this run }
	//
	// From is what the provider last published, To what it carries at the end
	// of the run; the two are equal on a catch-up, whose provider is already
	// out. It is wider than DueTo on purpose. DueTo answers "why is this
	// package releasing" and only propagation fills it, but propagation depth
	// is 0 by default, so a provider and a consumer that each change for their
	// own reasons propagate nothing to each other and DueTo would answer "no
	// providers moved" on the most ordinary run there is. The consumer's
	// manifests, scripts and changelog still have to learn the new version.
	//
	// A union rather than a replacement: each set carries a case the other
	// misses. A catch-up's provider is in DueTo and is *not* releasing
	// (§13.7a); a non-propagating provider is releasing and is not in DueTo.
	Updates []ProviderUpdate

	// Held is set when the effective `Release-As` directive is `none`
	// (§13.6a). A held package keeps its computed bump and version — they are
	// reported but not released, and recomputed identically by whichever run
	// lifts the hold.
	Held bool
	// Pinned is set when `Release-As` named an exact version.
	Pinned bool
	// CatchUp marks a release caused solely by propagation from packages that
	// are not themselves releasing in this run (§13.7a, W193).
	CatchUp bool
	// ChannelOnly marks a release whose only cause is a channel change
	// (§13.9, W202).
	ChannelOnly bool
	// FixedRide marks a release whose only cause is the space's fixed
	// versioning (W234): the package has no changes of its own and releases
	// solely to stay on the space's shared version. Its changelog receives a
	// single "no changes" entry.
	FixedRide bool
	// absorbed is set only on a fixed group's aggregate, never on a real
	// release: the group baseline's tag already contains some member's
	// pending work, so the aggregate was measured against that tag and the
	// alignment may raise a member's computed version even at the full
	// shared depth — the one case where a releasing member can otherwise
	// land below a version its group has already published.
	absorbed bool

	// Deselected is set by Narrow when the invocation's selection leaves the
	// package out of this run. It is Held's twin: the package keeps its
	// computed bump and version — both are reported — and is neither built,
	// published nor tagged. Nothing in Compute sets it; a plan is narrowed
	// after it is computed, so the versions a filtered run releases are the
	// versions the whole-monorepo run would have released.
	Deselected bool
	// WaitingFor names the releasing providers a *selected* package must
	// follow and this run is not releasing, which is why Narrow deselected it
	// anyway. Empty on a package the selection simply did not name: that one
	// is nobody's surprise, while this one was asked for and could not go.
	WaitingFor []string

	// Outputs are the values the package's scripts exported through their
	// DISPAT_OUTPUT files, in first-export order with later re-exports
	// overriding earlier values. They are produced at run time (by the
	// executor) rather than by planning; each entry reaches every later
	// script and hook of the package as DISPAT_OUTPUT_<NAME>=<value>, with
	// DISPAT_OUTPUT_SOURCE_<NAME> naming the script it came from. The
	// GitHub recorder reads the GitHubExport entry to decide whether to
	// create a release and which files to attach.
	Outputs []Output

	// Corrects names, per unit, the records that unit restates: the short shas
	// of its `Edits` targets, suffixed "#n" where the target commit carried
	// several units. §13.10 requires the plan to mark corrected entries, and
	// the changelog renders the mark beside the restatement.
	Corrects map[*ccme.Unit][]string
	// SuppressedNotes marks units whose changelog entry a revert took with it
	// (§7.3). The unit stays in Units and still counts toward the bump: the
	// work happened and was undone, and the version has to carry both halves.
	// Only the notes omit it, which NotesUnits is where that happens.
	SuppressedNotes map[*ccme.Unit]bool

	// UnitAuthors is who each unit is by: the carrying commit's git author and
	// everyone its Co-authored-by trailers name. Keyed by unit pointer like
	// Corrects, so the attribution travels with the unit to every consumer of
	// the release, and only ever holds units that parsed — an invalid unit is
	// not in Units and has no entry to attribute.
	UnitAuthors map[*ccme.Unit][]Author
	// WindowAuthors is every author of every commit in the package's pending
	// window, deduplicated, newest commit first. It is deliberately wider than
	// the union of UnitAuthors: a commit whose message is not a CCME record at
	// all, or whose units all failed to parse, still changed the package and
	// its author still worked on the release. Only the primary author is taken
	// from such a commit, because a message that did not parse has no footers
	// the planner is willing to read.
	WindowAuthors []Author
	// FreshWindowAuthors is WindowAuthors restricted to the commits an earlier
	// prerelease of the train has not already published, the author-side twin
	// of FreshUnits. AllAuthors picks between the two.
	FreshWindowAuthors []Author

	Diagnostics []Diagnostic
}

// UnitCorrects returns the records the unit restates, empty for an ordinary
// unit (§7.4, §13.10).
func (r *Release) UnitCorrects(u *ccme.Unit) []string { return r.Corrects[u] }

// UnitSuppressed reports a unit whose changelog entry a revert suppressed
// (§7.3).
func (r *Release) UnitSuppressed(u *ccme.Unit) bool { return r.SuppressedNotes[u] }

// AuthorsFor returns who the unit is by, empty for a unit nothing attributed.
func (r *Release) AuthorsFor(u *ccme.Unit) []Author { return r.UnitAuthors[u] }

// AllAuthors returns every author of the release's window, narrowed exactly as
// NotesUnits narrows the units it renders: a prerelease is attributed to the
// people behind its own changeset alone, while a stable release collects the
// whole pending window since the last stable tag. An entry that documents the
// train has to credit the train.
func (r *Release) AllAuthors() []Author {
	if r.Next.IsPrerelease() {
		return r.FreshWindowAuthors
	}
	return r.WindowAuthors
}

// GitHubExport is one of the outputs with a consumer inside dispat: a package
// that exports it gets a GitHub release (when the recorder is enabled), with
// the value read as a whitespace-separated list of files to attach; a package
// that does not is skipped by the recorder. Unlike ordinary outputs it is
// exported under — and travels to later scripts as — this full name.
const GitHubExport = "DISPAT_EXPORT_GITHUB"

// PackageCommitExportPrefix is the other output convention with a consumer
// inside dispat. A release script that exports PACKAGE_<KEY>=<commitHash>
// (reaching later scripts as DISPAT_OUTPUT_PACKAGE_<KEY>), where <KEY> is the
// package's own EnvKey, pins that package's release: the tag is created at
// the exported commit instead of the run's commit (the release commit in
// commit mode), and the package's GitHub release carries the hash as its
// commit and target_commitish. Meant for packages whose release scripts
// produce their own commit (a subtree push, a generated repository) that the
// tag should point at.
const PackageCommitExportPrefix = "PACKAGE_"

// EnvKey turns a package name into the fragment it occupies inside a
// DISPAT_* variable name. Environment variable names admit far less than
// package names do — "@acme/ui" is a fine package and an impossible
// variable — so anything outside [A-Z0-9] becomes "_" and letters are
// uppercased. The key only has to be addressable, not reversible.
func EnvKey(name string) string {
	b := []byte(strings.ToUpper(name))
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			b[i] = '_'
		}
	}
	return string(b)
}

// ExportedCommit returns the commit hash the package's scripts pinned this
// release to via the PACKAGE_<KEY> export, or "" when none was exported.
func (r *Release) ExportedCommit() string {
	if r.Pkg == nil {
		return ""
	}
	if v, ok := r.Output(PackageCommitExportPrefix + EnvKey(r.Pkg.Name)); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Output is one NAME=value pair a script exported through its DISPAT_OUTPUT
// file.
type Output struct {
	Name  string
	Value string
	// Source names the script that exported (or last re-exported) the value,
	// as "<package>:<stage>" — "core:build", "base:run:lint" — or
	// "<space>:login" for the space-level login script.
	Source string
}

// Output returns the value of the named script output, if exported.
func (r *Release) Output(name string) (string, bool) {
	for _, o := range r.Outputs {
		if o.Name == name {
			return o.Value, true
		}
	}
	return "", false
}

// Previous is the version the package last published: baseline(P) of §12.3.
//
// It is deliberately not Current. Current is the *stable* baseline, which is
// what versions are computed from (§11.4) and is a version in the past for any
// package on a prerelease train; Previous is what the package actually shipped
// last, which is what a reader, a changelog and a version script mean by "the
// old version".
func (r *Release) Previous() ccme.Version {
	if r.HasBaseline {
		return r.Baseline
	}
	return r.Current
}

// ChannelChanged reports whether the package is moving between channels.
func (r *Release) ChannelChanged() bool { return r.Channel != r.BaselineChannel }

// IsPrerelease reports whether the version being released carries a
// prerelease component.
func (r *Release) IsPrerelease() bool { return r.Next.IsPrerelease() }

// ChannelTransition renders the channel movement the plan must display.
func (r *Release) ChannelTransition() string {
	if !r.ChannelChanged() {
		return r.Channel
	}
	return r.BaselineChannel + " -> " + r.Channel
}

// Changed reports whether the package has a reason to be released: a bump
// carried by work the baseline has not published (NewWork), a channel change,
// or an exact pin (§13.9). A held package can be Changed and still not be
// released. A package on a prerelease train whose bump comes entirely from
// commits its baseline already contains is NOT changed — that work shipped in
// the baseline prerelease, and re-admitting it would re-release the train on
// every run.
func (r *Release) Changed() bool {
	return (r.Bump != ccme.BumpNone && r.NewWork) || r.ChannelChanged() || r.Pinned || r.FixedRide
}

// NotesUnits returns the units the release's *notes* — the changelog entry,
// the GitHub release body and the DISPAT_BREAKING_CHANGES / DISPAT_FEATURES /
// DISPAT_FIXES variables — are built from.
//
// A prerelease documents only its own changeset: the units its train's
// earlier prereleases have not already published (FreshUnits) — beta.1's
// entry does not repeat beta.0's. A stable release documents the whole
// pending window since the last stable tag (Units) — for a graduation that
// is every prerelease's changes collected into the one entry readers of the
// stable line will actually see. The bump and version are always computed
// over the whole window either way (§11.4); only the notes narrow.
// A revert and the unit it reverted leave the notes together (§7.3): the two
// cancel out, so documenting either would describe work the release does not
// contain. Both still count toward the bump, which is why the filter lives
// here and not in Units.
func (r *Release) NotesUnits() []*ccme.Unit {
	units := r.Units
	if r.Next.IsPrerelease() {
		units = r.FreshUnits
	}
	if len(r.SuppressedNotes) == 0 {
		return units
	}
	out := make([]*ccme.Unit, 0, len(units))
	for _, u := range units {
		if r.SuppressedNotes[u] {
			continue
		}
		out = append(out, u)
	}
	return out
}

// NoChanges reports whether the release carries no content of its own — no
// units, no provider updates — and exists only to keep the space's fixed
// versioning aligned. The changelog and the GitHub release render a single
// "no changes" entry for it.
func (r *Release) NoChanges() bool {
	// NotesUnits rather than Units, mirroring what the entry renders: Units
	// spans the whole prerelease train, so a riding member with any train
	// history would fail this test and render an empty body instead of the
	// ride line. Updates too: a ride that picks up a provider's movement has
	// a dependencies section to show, which is not "no changes".
	return r.FixedRide && len(r.NotesUnits()) == 0 && len(r.DueTo) == 0 && len(r.Updates) == 0
}

// SharedDepth is how many leading version components the package holds in
// common with its versioning group: 3 for a shared whole version, 2 for a
// shared major and minor, 1 for a shared major, 0 for an independent package.
// Records read it to say what a ride's version bump was actually for.
func (r *Release) SharedDepth() int {
	if r.Pkg == nil || r.Pkg.Space == nil {
		return 0
	}
	return r.Pkg.Space.Versioning.SharedDepth()
}

// Releasable reports whether the package takes part in the release flow at
// all: false only under versioning "none", whose packages exist to run
// scripts and are never versioned, tagged or published. Nil-safe like
// SharedDepth, and permanent where Held is per-run: a held package keeps a
// computed version waiting, a none package never has one.
func (r *Release) Releasable() bool {
	return r.Pkg == nil || r.Pkg.Space == nil || r.Pkg.Space.Versioning.Releasable()
}

// Releasing reports whether the package is in this run's plan: it is
// releasable at all, it has a reason, it is not held, and this invocation's
// selection did not leave it out. Every released package is versioned and
// tagged whatever its publish target — an exception there costs convergence
// (§13.7c).
//
// This is the one gate the whole run reads: the executor's task graph, the
// workspace environment, auto-versioning's provider ranges, the finalize phase
// and the summary all ask it, which is what makes narrowing a plan a single
// decision rather than a condition repeated in five places.
func (r *Release) Releasing() bool {
	return r.Changed() && !r.Held && !r.Deselected && r.Releasable()
}

// RunsScripts reports whether the package sits in the default script window:
// releasing, or a changed versioning-none package the selection kept. Run
// scripts are the one thing a none package exists for, so the window that
// would otherwise be exactly the plan admits it too.
func (r *Release) RunsScripts() bool {
	return r.Releasing() || (!r.Releasable() && r.Changed() && !r.Deselected)
}

// TagFormat is the release tag template of the package's space, or the
// repository default when the space names none.
func (r *Release) TagFormat() gitx.TagFormat {
	return TagFormatFor(r.Pkg)
}

// TagFormatFor is the same rule for a package with no release around it — the
// commands that read tags without planning first. It is one function because
// the format is what a run reads a package's baseline from: two callers
// spelling it differently would give the package two histories.
func TagFormatFor(p *model.Package) gitx.TagFormat {
	if p != nil && p.Space != nil {
		return gitx.TagFormat(p.Space.TagFormat).WithDefault()
	}
	return gitx.DefaultTagFormat
}

// TagName is the tag written on a successful release. Everything released is
// tagged, whatever its publish target — an exception here would cost
// convergence, because a package whose window never advances reappears in
// every plan for ever (§13.7c, §13.10a).
//
// This is the single place a release tag name is built. It has to be: the name
// is what the *next* run reads its baseline from, so a caller that renders one
// differently silently gives that package no history at all.
func (r *Release) TagName() string {
	return r.TagFormat().Render(r.Pkg.Name, r.Next)
}

// AliasTag is one alias this release is additionally written under.
type AliasTag struct {
	Name string
	// Force allows the write to replace a ref that already exists, which is
	// what a moving alias needs on every release after its first.
	Force bool
}

// AliasTags renders the aliases that apply to this release: the package's
// configured list, filtered to the ones whose channels admit the channel being
// released on, each rendered from the version being released.
//
// The names come out in configuration order, and a package with no aliases
// gets nothing, which is every package by default.
func (r *Release) AliasTags() []AliasTag {
	if r.Pkg == nil || r.Pkg.Space == nil {
		return nil
	}
	out := make([]AliasTag, 0, len(r.Pkg.Space.AliasTags))
	for _, a := range r.Pkg.Space.AliasTags {
		if !aliasAppliesTo(a, r.Channel) {
			continue
		}
		out = append(out, AliasTag{
			Name:  gitx.AliasFormat(a.Format).Render(r.Pkg.Name, r.Next),
			Force: a.Force,
		})
	}
	return out
}

// aliasAppliesTo reports whether an alias is written for a release on channel.
// An empty channel list means every channel.
func aliasAppliesTo(a model.AliasTag, channel string) bool {
	if len(a.Channels) == 0 {
		return true
	}
	for _, c := range a.Channels {
		if strings.EqualFold(c, channel) {
			return true
		}
	}
	return false
}

// SemverTagName is the same release named under the normative
// "{name}@{version}" format, whatever the space's tagFormat renders. It is
// never written to git — TagName is the single source of real tag names — it
// exists so a script can receive the SemVer spelling alongside the custom one.
func (r *Release) SemverTagName() string {
	return gitx.TagName(r.Pkg.Name, r.Next)
}

// counterOf is the prerelease counter of a version: the identifiers after the
// channel, so "1.3.0-beta.4" reports "4". Usually the bare number §11.3
// prescribes; an exact Release-As may carry more, and they belong to the
// counter rather than being dropped. Empty for a stable version.
func counterOf(v ccme.Version) string {
	if len(v.Prerelease) < 2 {
		return ""
	}
	return strings.Join(v.Prerelease[1:], ".")
}

// Counter is the prerelease counter of the version being released.
func (r *Release) Counter() string { return counterOf(r.Next) }

// PreviousCounter is the prerelease counter of the version last published.
func (r *Release) PreviousCounter() string { return counterOf(r.Previous()) }

// Reason renders the §13.10 explanation of why the package is in the plan.
func (r *Release) Reason() string {
	switch {
	case r.FixedRide:
		return "fixed group versioning"
	case r.CatchUp:
		parts := make([]string, 0, len(r.Sources))
		seen := make(map[string]bool)
		for _, s := range r.Sources {
			if seen[s.Provider] {
				continue
			}
			seen[s.Provider] = true
			parts = append(parts, s.Provider)
		}
		return "catch-up from " + strings.Join(parts, ", ")
	case r.ChannelOnly:
		if r.ChannelFrom != "" {
			return "channel from " + r.ChannelFrom
		}
		return "channel " + r.ChannelTransition()
	case r.FreshOwnBump():
		return "direct"
	case len(r.DueTo) > 0:
		return "propagated from " + strings.Join(r.DueTo, ", ")
	case r.OwnBump != ccme.BumpNone:
		// Own work the train already published: it keeps deciding the
		// train's target, and with no fresh cause above it is also the only
		// explanation left to give.
		return "direct"
	case r.Pinned:
		return "pinned"
	default:
		return "unchanged"
	}
}

// FreshOwnBump reports whether the package's own pending changeset — the
// units its baseline has not published — carries a bump. This is the
// "direct" of a reason: own work the train has already shipped keeps
// counting toward the target (OwnBump spans the train), but it does not
// explain why the package is releasing again. Exported because the
// executor's skip cascade asks the same question: whether the package has a
// reason of its own for *this* release.
func (r *Release) FreshOwnBump() bool {
	for _, u := range r.FreshUnits {
		if u.Bump != ccme.BumpNone {
			return true
		}
	}
	return false
}

// Plan is the full release plan for the repository.
type Plan struct {
	Order       []string            // topological order, providers before consumers
	Releases    map[string]*Release // one entry per package
	Providers   map[string][]string // consumer -> its providers
	Diagnostics []Diagnostic

	// ancestor answers "is a an ancestor-or-self of b" over the commits the
	// plan examined; it backs PossiblyBehind.
	ancestor func(a, b string) bool
}

// HasErrors reports whether any error-severity diagnostic was raised, of any
// blast radius. Whether that stops the run is a policy question the caller
// answers; see Fatal for the errors that stop it regardless.
func (p *Plan) HasErrors() bool {
	for _, d := range p.Diagnostics {
		if d.Level == LevelError {
			return true
		}
	}
	return false
}

// Fatal reports whether any repository-scoped error was raised. These abort
// the run whatever the configured policy: they mean no correct plan exists, so
// emitting a partial release would be releasing something nobody computed
// (§16).
func (p *Plan) Fatal() bool {
	for _, d := range p.Diagnostics {
		if d.Level == LevelError && IsRepositoryScoped(d.Code) {
			return true
		}
	}
	return false
}

// Releasing lists the packages this run will publish, in dependency order.
// Publishing in this order is what prevents the one staleness case no rule
// over tags can detect: a provider and a consumer released at the same commit
// with the consumer published first (§19.2, §13.7b).
func (p *Plan) Releasing() []*Release {
	out := make([]*Release, 0, len(p.Order))
	for _, name := range p.Order {
		if r := p.Releases[name]; r != nil && r.Releasing() {
			out = append(out, r)
		}
	}
	return out
}

// Held lists packages withheld by `Release-As: none`, in dependency order.
// These are the only packages allowed to persist across runs (§13.7c, G6).
func (p *Plan) Held() []string {
	var out []string
	for _, name := range p.Order {
		if r := p.Releases[name]; r != nil && r.Held {
			out = append(out, name)
		}
	}
	return out
}

// Deselected lists the packages Narrow left out of this run, in dependency
// order: they would have released, and this invocation's selection is why they
// are not. Empty on every plan nothing narrowed.
func (p *Plan) Deselected() []string {
	var out []string
	for _, name := range p.Order {
		if r := p.Releases[name]; r != nil && r.Deselected {
			out = append(out, name)
		}
	}
	return out
}

// StaleSources is the walk *up* from a consumer described in §13.7b. It is the
// dual of the downward traversal of §9.2 over the same relation: it is
// non-empty exactly when the consumer was assigned a non-none propagated bump,
// and max() over its rows equals that bump. The two formulations MUST agree;
// disagreement is an implementation bug.
func (p *Plan) StaleSources(pkg string) []StaleSource {
	r := p.Releases[pkg]
	if r == nil {
		return nil
	}
	return r.Sources
}

// PossiblyBehind is the cheap tag-level screen of §13.7b: the consumer may owe
// a release to the provider when the provider's baseline tag is not an
// ancestor-or-self of the consumer's.
//
// It MUST NOT be used to decide releases. It is necessary but not sufficient —
// the units between the two tags may all be `^none`, or `+0`, or scoped away,
// or reach the consumer only beyond their declared depth, or travel only over
// devDependencies edges. Use it to find candidates; use the plan to decide.
func (p *Plan) PossiblyBehind(consumer, provider string) bool {
	c, pr := p.Releases[consumer], p.Releases[provider]
	if c == nil || pr == nil || pr.StableCommit == "" {
		return false
	}
	if c.StableCommit == "" {
		return true // never released while the provider has been
	}
	if p.ancestor == nil {
		return false
	}
	return !p.ancestor(pr.StableCommit, c.StableCommit)
}

// edge is one dependency edge, provider -> consumer.
type edge struct {
	to   string
	kind model.DepKind
}

// commitRec is one commit of the union of all pending windows, parsed once.
type commitRec struct {
	commit gitx.Commit
	key    string
	rank   int // position in history, 0 = newest
	units  []*ccme.Unit
	// unitCount is how many units the message carried, invalid ones included.
	// units holds only those that parsed, and a correction's "#n" selector
	// counts positions in the message (§7.4.1), so the two are different
	// numbers and both are needed.
	unitCount int
	// scope[i] is the resolved scope-set of units[i] (§6), as package names.
	scope []map[string]bool
	// derivedSet memoises derived(commit) (§6.2).
	derivedSet map[string]bool
}

// pin is an exact `Release-As` directive together with the context its guards
// need: where it was written, and how many packages its scope-set resolved to.
type pin struct {
	version  ccme.Version
	commit   string
	packages int
}

// cancelRec is one `cancel` unit with its resolved scope-set and the ancestor
// closure of the commit carrying it.
type cancelRec struct {
	key       string
	scope     map[string]bool
	closure   map[string]bool // commit keys that are ancestors-or-self
	discarded bool            // whether it actually discarded anything
	pkgLabel  string
}

// Options are the inputs to Compute beyond the repository itself.
type Options struct {
	// Packages is the workspace at HEAD (§13.1).
	Packages []*model.Package
	// Dependencies are the graph edges.
	Dependencies []model.Dependency
	// Initials is the baseline for a package whose latest tag is missing or
	// unparseable, keyed by package name.
	Initials map[string]ccme.Version
	// Root is the repository root, against which a commit's changed-file
	// paths are resolved (§6.2).
	Root string
	// NonPackageScopes are scope names that are deliberately not packages, so
	// naming one is not the typo E130 exists to catch. A unit scoping only
	// these resolves to nothing, silently.
	NonPackageScopes []string
	// ParserConfig is the commit-message parser configuration (the config
	// file's `parser` object). The zero value is the specification defaults,
	// exactly as ccme documents it, so a caller with no opinions passes
	// nothing.
	ParserConfig ccme.Config
	// Log traces the computation's phases and what each package resolved to.
	// The zero value discards, so a caller with nothing to say passes nothing
	// and the planner stays silent.
	//
	// A wrong plan is the hardest thing to debug about a release, because the
	// output is a plausible set of versions and the reason lives in an
	// intermediate the plan does not carry: which tag became the baseline, how
	// many commits the window held, what the bump was before propagation
	// touched it. These lines are those intermediates.
	Log zerolog.Logger
	// IgnoredTags are exact tag names left out of baseline resolution: the
	// run's own not-yet-final tags. A step command re-planning from inside a
	// running release must not read the tag its own leg just created back as
	// published history — that would empty the window the record needs — so
	// the environment wiring masks it here. See the app's step wiring.
	IgnoredTags []string
}

type computation struct {
	ctx      context.Context
	git      gitx.Git
	log      zerolog.Logger
	root     string
	initials map[string]ccme.Version
	// nonPackage holds Options.NonPackageScopes as a set.
	nonPackage map[string]bool
	// ignoredTags is Options.IgnoredTags as a set; see that field.
	ignoredTags map[string]bool

	pkgs      []*model.Package
	scopeDirs []scopeDir // prepared once; see prepareScopeDirs
	byName    map[string]*model.Package
	order     []string
	providers map[string][]string
	edges     map[string][]edge

	parser *ccme.Parser

	rel     map[string]*Release
	tags    map[string]gitx.Tags       // package -> its tag listing, newest first
	window  map[string]map[string]bool // package -> commit keys it has not released
	commits []*commitRec               // newest first, deduplicated
	byKey   map[string]*commitRec
	parents map[string][]string
	linked  bool // whether parent pointers are available

	// ownContribs is each package's direct contributions with the commits
	// that carried them: what directBumps folded into OwnBump, kept apart so
	// the fixed-group aggregate can re-measure a member's pending work
	// against the group's published baseline instead of the member's own —
	// the difference between a group whose prefix must move and a member
	// catching up to a version the group has already published.
	ownContribs map[string][]groupContrib

	cancels []*cancelRec
	held    map[string]bool
	pinned  map[string]pin

	// corrections state (§13.4b, §7.3). dropped holds every claimed
	// (package, record) pair and the correction that claimed it; corrects and
	// noteDrops are the plan marking §13.10 requires, per package.
	dropped   map[dropKey]*correctionRec
	corrects  map[string]map[*ccme.Unit][]string
	noteDrops map[string]map[*ccme.Unit]bool
	shaCache  map[string]string

	// unitAuthors is who each parsed unit is by, resolved once in §13.4 and
	// shared by every release the unit reaches. One map for the whole
	// computation rather than one per package: a unit's authors are a property
	// of the commit that carried it, not of the package it resolved onto.
	unitAuthors map[*ccme.Unit][]Author

	// channel axis state, produced by §9.2 phase 1 and settled by §13.8.
	proposed    map[string]channelPick
	channel     map[string]string
	channelFrom map[string]string

	// ancestry state: the memo behind ancestorOrSelf, the flag that the Git
	// implementation answered gitx.ErrNoAncestry (so only the fallbacks are
	// consulted from then on), and the first real git failure — which aborts
	// Compute rather than let the weaker fallbacks decide cancellation and
	// containment.
	ancCache map[[2]string]bool
	ancNoGit bool
	ancErr   error

	diags []Diagnostic
}

// Compute builds the dependency graph, inspects git history and decides every
// package's next version and channel.
//
// The procedure is the one in §13 and is a pure function of (history, graph,
// configuration): it never consults wall-clock time, tag creation dates or the
// outcome of any previous run, which is what makes re-running it after a
// partial publish deterministic (§17.2) and what gives G1-G8.
//
//	§13.1  load the workspace at HEAD
//	§13.2  load tags, resolve baselines
//	§13.3  pending window per package, measured from its last *stable* tag
//	§13.4  parse the union of windows into units, resolve scopes
//	§13.5  cancellation closures
//	§13.4b corrections, applied to the stream the phases below read
//	§13.6a holds, resolved before propagation
//	§13.6  direct bumps
//	§13.7  propagation — channel axis, channel resolution, then the bump axis
//	§13.9  versions
//	§13.10 emit
//
// Graph work is O((V+E) log V) per propagation phase. Git is queried exactly
// twice per package — one tag listing and one bounded log range — and never
// over the whole history.
func Compute(ctx context.Context, git gitx.Git, opts Options) (*Plan, error) {
	pkgs := opts.Packages
	cp := &computation{
		ctx:         ctx,
		git:         git,
		log:         opts.Log,
		root:        opts.Root,
		initials:    opts.Initials,
		nonPackage:  make(map[string]bool, len(opts.NonPackageScopes)),
		ignoredTags: make(map[string]bool, len(opts.IgnoredTags)),
		pkgs:        pkgs,
		byName:      make(map[string]*model.Package, len(pkgs)),
		rel:         make(map[string]*Release, len(pkgs)),
		tags:        make(map[string]gitx.Tags, len(pkgs)),
		window:      make(map[string]map[string]bool, len(pkgs)),
		ownContribs: make(map[string][]groupContrib, len(pkgs)),
		byKey:       make(map[string]*commitRec),
		parents:     make(map[string][]string),
		held:        make(map[string]bool),
		pinned:      make(map[string]pin),
		proposed:    make(map[string]channelPick),
		channel:     make(map[string]string, len(pkgs)),
		channelFrom: make(map[string]string),
		dropped:     make(map[dropKey]*correctionRec),
		corrects:    make(map[string]map[*ccme.Unit][]string),
		noteDrops:   make(map[string]map[*ccme.Unit]bool),
		unitAuthors: make(map[*ccme.Unit][]Author),
	}
	for _, s := range opts.NonPackageScopes {
		cp.nonPackage[s] = true
	}
	for _, t := range opts.IgnoredTags {
		cp.ignoredTags[t] = true
	}

	if err := cp.loadWorkspace(opts.Dependencies); err != nil { // §13.1
		if errors.Is(err, errFatalPlan) {
			return cp.fatalPlan(), nil
		}
		return nil, err
	}

	// §16 E196: a shallow or grafted clone hides commits and tags, so every
	// window and baseline computed over it is silently wrong. Checked before
	// any history is read.
	if shallow, err := git.IsShallow(ctx); err != nil {
		return nil, fmt.Errorf("plan: checking repository completeness: %w", err)
	} else if shallow {
		cp.err(CodeShallowRepository, "", "",
			"the repository is shallow or grafted: history is incomplete, so no correct plan can be computed; run `git fetch --unshallow` first")
		return cp.fatalPlan(), nil
	}
	// The parser options come from the configuration file's `parser` object;
	// a zero Config is the specification defaults, so nothing changes for a
	// repository that configures nothing.
	parser, err := ccme.NewParser(opts.ParserConfig)
	if err != nil {
		return nil, err
	}
	cp.parser = parser

	if err := cp.loadTagsAndWindows(); err != nil { // §13.2, §13.3
		if errors.Is(err, errFatalPlan) {
			return cp.fatalPlan(), nil
		}
		return nil, err
	}
	cp.log.Debug().Int("packages", len(cp.order)).Int("commits", len(cp.commits)).
		Msg("plan: windows loaded")
	if err := cp.parseAndResolve(); err != nil { // §13.4
		return nil, err
	}
	cp.collectCancels()        // §13.5
	cp.applyCorrections()      // §13.4b, on the stream every phase below reads
	cp.suppressRevertedNotes() // §7.3, on the corrected stream
	cp.resolveHolds()          // §13.6a
	cp.directBumps()           // §13.6
	if err := cp.ancestryFailed(); err != nil {
		return nil, err
	}
	cp.log.Debug().Int("held", len(cp.held)).Int("pinned", len(cp.pinned)).
		Msg("plan: direct bumps resolved")

	// §13.7 is §9.2's three phases; §13.8 is invoked from inside it.
	cp.propagateChannels() // phase 1
	cp.resolveChannels()   // phase 2 (§13.8)
	cp.propagateBumps()    // phase 3

	cp.finalise()      // §13.9, §13.10
	cp.reportCancels() // W170
	cp.reportHeld()    // W154
	cp.logReleases()
	if err := cp.ancestryFailed(); err != nil {
		return nil, err
	}

	return &Plan{
		Order:       cp.order,
		Releases:    cp.rel,
		Providers:   cp.providers,
		Diagnostics: cp.diags,
		ancestor:    cp.ancestorOrSelf,
	}, nil
}

// PackagesChangedSince resolves which packages the commits in rev..HEAD
// address, under exactly the scope semantics planning uses (§6): a unit's
// written scope-set is authoritative — globs, exclusions and non-package
// scopes included — and only a unit with no scope-set falls back to the
// commit's changed files (§6.2, longest path prefix). It is the selection
// behind `dispat run --since`: scope-first, files only where scopes are not
// specified. Scope diagnostics are deliberately not raised — this selects
// packages to run a script over, it does not plan a release — and the names
// come back in dependency order.
func PackagesChangedSince(ctx context.Context, git gitx.Git, opts Options, rev string) ([]string, error) {
	cp := &computation{
		ctx:        ctx,
		git:        git,
		root:       opts.Root,
		nonPackage: make(map[string]bool, len(opts.NonPackageScopes)),
		pkgs:       opts.Packages,
		byName:     make(map[string]*model.Package, len(opts.Packages)),
	}
	for _, s := range opts.NonPackageScopes {
		cp.nonPackage[s] = true
	}
	if err := cp.loadWorkspace(opts.Dependencies); err != nil {
		return nil, err
	}
	parser, err := ccme.NewParser(opts.ParserConfig)
	if err != nil {
		return nil, err
	}
	cp.parser = parser

	commits, err := git.Commits(ctx, rev)
	if err != nil {
		return nil, fmt.Errorf("plan: resolving commits since %q: %w", rev, err)
	}
	selected := make(map[string]bool)
	for _, c := range commits {
		rec := &commitRec{commit: c, key: commitKey(c)}
		data, err := cp.parser.Parse(c.Message)
		if data == nil {
			return nil, fmt.Errorf("plan: %s: %w", rec.key, err)
		}
		for _, u := range data.ValidUnits() {
			scopes, written := unitScopes(u)
			for name := range cp.resolveScopeSet(scopes, written, rec).packages {
				selected[name] = true
			}
		}
	}
	out := make([]string, 0, len(selected))
	for _, name := range cp.order {
		if selected[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// §13.1 workspace
// ---------------------------------------------------------------------------

func (cp *computation) loadWorkspace(deps []model.Dependency) error {
	g := graph.New()
	for _, p := range cp.pkgs {
		cp.byName[p.Name] = p
		g.AddNode(p.Name)
	}
	cp.providers = make(map[string][]string)
	cp.edges = make(map[string][]edge)

	seen := make(map[model.Dependency]bool)
	for _, d := range deps {
		if seen[d] { // tolerate duplicate config entries
			continue
		}
		seen[d] = true
		if err := g.AddEdge(d.Provider, d.Consumer); err != nil {
			return err
		}
		cp.providers[d.Consumer] = append(cp.providers[d.Consumer], d.Provider)
		cp.edges[d.Provider] = append(cp.edges[d.Provider], edge{to: d.Consumer, kind: d.Kind})
	}
	// Deterministic traversal order (§17.2): the BFS of §9.2 marks each
	// package seen once, so the order it meets them must not depend on map
	// iteration or on the order of the config file.
	for _, es := range cp.edges {
		sort.Slice(es, func(i, j int) bool { return es[i].to < es[j].to })
	}

	order, err := g.TopoSort()
	if err != nil {
		// §16 E200: a cyclic graph has no publish order — the run cannot
		// produce a correct plan, and the code must surface as a diagnostic
		// (not a bare load failure) so operators and tooling can key off it.
		// §13.1 also requires the cycle report to name the manifest field
		// carrying each edge, so the message lists the edges among the
		// blocked nodes with their kinds.
		msg := err.Error()
		var cyc *graph.CycleError
		if errors.As(err, &cyc) {
			members := make(map[string]bool, len(cyc.Nodes))
			for _, n := range cyc.Nodes {
				members[n] = true
			}
			var edges []string
			for d := range seen {
				if members[d.Consumer] && members[d.Provider] {
					edges = append(edges, fmt.Sprintf("%s -> %s (%s)", d.Consumer, d.Provider, d.Kind))
				}
			}
			sort.Strings(edges)
			msg += "; edges: " + strings.Join(edges, ", ")
		}
		cp.err(CodeDependencyCycle, "", "", msg)
		return errFatalPlan
	}
	cp.order = order
	return nil
}

// errFatalPlan signals that a repository-scoped error was recorded in
// cp.diags: no correct plan exists, and Compute returns the diagnostics as a
// fatal plan instead of an ordinary error, so the §16 code reaches the
// caller's diagnostics stream.
var errFatalPlan = errors.New("plan: repository-scoped error")

// fatalPlan is the plan of a repository where no correct plan exists: the
// recorded diagnostics — at least one of them repository-scoped, making
// Fatal() true — and nothing releasable.
func (cp *computation) fatalPlan() *Plan {
	return &Plan{
		Releases:    map[string]*Release{},
		Providers:   map[string][]string{},
		Diagnostics: cp.diags,
		ancestor:    cp.ancestorOrSelf,
	}
}

// ---------------------------------------------------------------------------
// §13.2 tags and §13.3 pending windows
// ---------------------------------------------------------------------------

func (cp *computation) loadTagsAndWindows() error {
	// Per-package commit lists, kept so the union can be ranked afterwards.
	lists := make([][]gitx.Commit, 0, len(cp.pkgs))

	// The tag queries are independent per-package git reads, so they are
	// fetched concurrently (bounded, for monorepos with hundreds of
	// packages) and then assembled strictly in package order below — the
	// diagnostics, windows and union ranking stay deterministic because
	// nothing after this block runs concurrently.
	tagsFor := make([]gitx.Tags, len(cp.pkgs))
	tagsErr := make([]error, len(cp.pkgs))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, p := range cp.pkgs {
		// A versioning-none package is never tagged, so there is nothing to
		// query: its tags stay empty and its window is the whole history.
		if !(&Release{Pkg: p}).Releasable() {
			continue
		}
		wg.Add(1)
		go func(i int, p *model.Package) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tagsFor[i], tagsErr[i] = cp.git.Tags(cp.ctx, p.Name, (&Release{Pkg: p}).TagFormat())
		}(i, p)
	}
	wg.Wait()

	// Windows are one `git log` per DISTINCT starting tag: packages that
	// share a window origin (every never-stably-released package shares the
	// whole history) share the listing instead of re-reading it.
	commitsBySince := make(map[string][]gitx.Commit)

	for i, p := range cp.pkgs {
		rel := &Release{Pkg: p}

		// One tag query per package. Both baselines of §12.3 are selections
		// over the same list, so asking for them separately would double the
		// tag work for an answer that comes from identical output.
		tags, err := tagsFor[i], tagsErr[i]
		if err != nil {
			return fmt.Errorf("plan: %s: %w", p.Name, err)
		}
		tags = cp.withoutIgnoredTags(tags)
		// Kept for the graduation's dependencies record: reconstructing what a
		// consumer's last stable release shipped against is a question about
		// the provider's tags, and the planner holds no other state between
		// runs (versionAt).
		cp.tags[p.Name] = tags

		// §16 E191: two reachable tags parsing to the same version of this
		// package (build metadata carries no precedence, so "1.2.3" and
		// "1.2.3+b" collide) on different commits make the baseline selection
		// ambiguous — no correct plan exists.
		if a, b, dup := duplicateVersionTags(tags); dup {
			cp.err(CodeDuplicateVersionTag, p.Name, "", fmt.Sprintf(
				"tags %s and %s parse to the same version %s but point at different commits",
				a.Name, b.Name, a.Version.String()))
			return errFatalPlan
		}

		newest, hasNewest := tags.Baseline()
		if hasNewest && newest.Parsed {
			rel.Baseline, rel.HasBaseline = newest.Version, true
			rel.BaselineCommit = newest.Commit
		}
		// §11.1: a package with no baseline is on the stable channel, so a
		// never-released package is graduated by nothing and entered onto a
		// train by any directive naming it.
		rel.BaselineChannel = channelOf(rel.Baseline, rel.HasBaseline)
		rel.Channel = rel.BaselineChannel

		stable, hasStable := tags.StableBaseline()

		// Baseline resolution. The latest parseable *stable* tag wins. When
		// the newest tag exists but cannot be parsed, the pre-last tag is NOT
		// used: the baseline comes from initials (default 0.0.0) while the
		// window is still measured from the unparseable tag, so that already
		// released commits are not counted twice.
		since := ""
		switch {
		case hasStable && stable.Parsed:
			rel.Current, rel.Tagged, since = stable.Version, true, stable.Name
			rel.StableCommit = stable.Commit
		case hasStable:
			since = stable.Name
			rel.StableCommit = stable.Commit
			if init, ok := cp.initials[p.Name]; ok {
				rel.Current, rel.FromInitials = init, true
			}
		default: // never stably released: the window is the whole history (§13.3)
			// Initials seed a first release; a versioning-none package never
			// has one, so a fabricated Current must not appear for it.
			if init, ok := cp.initials[p.Name]; ok && rel.Releasable() {
				rel.Current, rel.FromInitials = init, true
			}
		}
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		cp.rel[p.Name] = rel

		commits, ok := commitsBySince[since]
		if !ok {
			commits, err = cp.git.Commits(cp.ctx, since)
			if err != nil {
				return fmt.Errorf("plan: %s: %w", p.Name, err)
			}
			commitsBySince[since] = commits
		}
		w := make(map[string]bool, len(commits))
		for _, c := range commits {
			w[commitKey(c)] = true
		}
		cp.window[p.Name] = w
		lists = append(lists, commits)
	}

	cp.buildUnion(lists)
	return nil
}

// duplicateVersionTags finds two parsed tags carrying the same version on
// different commits. Version identity ignores build metadata, exactly as
// precedence does.
func duplicateVersionTags(tags gitx.Tags) (a, b gitx.Tag, dup bool) {
	byVersion := make(map[string]gitx.Tag, len(tags))
	for _, t := range tags {
		if !t.Parsed {
			continue
		}
		key := t.Version.String()
		prev, seen := byVersion[key]
		if seen && prev.Commit != t.Commit {
			return prev, t, true
		}
		if !seen {
			byVersion[key] = t
		}
	}
	return gitx.Tag{}, gitx.Tag{}, false
}

// baselineChannel is channelOf(baseline(P)) (§11.1). It reads the package's
// own tag and nothing computed in this run, which is what makes a transition's
// <from> stable across phases and gives the channel axis its convergence
// (§13.7c G7).
func (cp *computation) baselineChannel(pkg string) string {
	if rel := cp.rel[pkg]; rel != nil {
		return rel.BaselineChannel
	}
	return ccme.ChannelStable
}

// containedInBaseline reports whether the commit has already been published by
// the package's baseline tag. It can only be true on a prerelease train: the
// window is measured from the *stable* tag (§13.3), so a train's window spans
// commits its prerelease releases already shipped, and those commits are ahead
// of StableCommit but at-or-behind BaselineCommit. For a stable package the
// two tags coincide and the window already excludes released commits.
//
// Contained work still counts toward the train's bump — §11.4 recomputes the
// target over the whole window — but it is discharged for every purpose that
// asks "is this still pending?": it does not re-release the train (NewWork),
// a Release-As it carries is no longer in force, and a cancel cannot discard
// it (§10.3: cancellation never reaches a published tag, and a prerelease tag
// is a published tag).
func (cp *computation) containedInBaseline(pkg, key string) bool {
	rel := cp.rel[pkg]
	if rel == nil || rel.BaselineCommit == "" || rel.BaselineCommit == rel.StableCommit {
		return false
	}
	return cp.ancestorOrSelf(key, rel.BaselineCommit)
}

// buildUnion merges the per-package windows into one history-ordered,
// deduplicated list. A commit reachable through several windows contributes
// its units exactly once (§13.3).
func (cp *computation) buildUnion(lists [][]gitx.Commit) {
	// Rank against the longest window first, so the widest available view of
	// history defines the ordering and shorter windows (suffixes of it) reuse
	// the ranks they already have.
	idx := make([]int, len(lists))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return len(lists[idx[a]]) > len(lists[idx[b]]) })

	for _, i := range idx {
		for _, c := range lists[i] {
			key := commitKey(c)
			if _, ok := cp.byKey[key]; ok {
				continue
			}
			rec := &commitRec{commit: c, key: key, rank: len(cp.commits)}
			cp.byKey[key] = rec
			cp.commits = append(cp.commits, rec)
			if ps := c.Parents; len(ps) > 0 {
				cp.parents[key] = ps
				cp.linked = true
			}
		}
	}
}

// ancestorOrSelf reports whether a is an ancestor-or-self of b.
//
// Answers are memoised on the computation: the same (commit, baseline) pair
// is asked from several nested phases, and each uncached git answer is a
// subprocess — on a prerelease train the containment checks alone would
// otherwise fork once per commit×package.
func (cp *computation) ancestorOrSelf(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	key := [2]string{a, b}
	if v, ok := cp.ancCache[key]; ok {
		return v
	}
	v := cp.ancestorLookup(a, b)
	if cp.ancCache == nil {
		cp.ancCache = make(map[[2]string]bool)
	}
	cp.ancCache[key] = v
	return v
}

// ancestorLookup answers one uncached ancestry question. Three sources of
// truth, in order of preference: the Git implementation, the parent pointers
// carried by the commits, and — when neither is available — history order,
// which is exact for the linear case and the only thing left otherwise.
// Ancestry rather than commit dates is what keeps cancellation deterministic
// under merges and rebases (§10.4).
//
// A real git failure — as opposed to gitx.ErrNoAncestry's "I have no answer,
// use the fallback" — is recorded once in cp.ancErr and aborts Compute: the
// fallbacks answer a weaker question, and silently degrading to them (on a
// cancelled context, say) would change which releases get cancelled or
// contained.
func (cp *computation) ancestorLookup(a, b string) bool {
	if !cp.ancNoGit && cp.ancErr == nil {
		yes, err := cp.git.IsAncestor(cp.ctx, a, b)
		switch {
		case err == nil:
			return yes
		case errors.Is(err, gitx.ErrNoAncestry):
			cp.ancNoGit = true
		default:
			cp.ancErr = err
		}
	}
	if cp.linked {
		seen := map[string]bool{b: true}
		queue := []string{b}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, p := range cp.parents[cur] {
				if p == a {
					return true
				}
				if seen[p] {
					continue
				}
				seen[p] = true
				queue = append(queue, p)
			}
		}
		return false
	}
	ra, oka := cp.byKey[a]
	rb, okb := cp.byKey[b]
	if !oka || !okb {
		return false
	}
	return ra.rank >= rb.rank // newest first: later in the list is older
}

// ancestryFailed surfaces the first real git failure an ancestry question
// hit. Compute checks it between phases: once git has failed, every fallback
// answer after it is untrustworthy, so no plan may be emitted.
func (cp *computation) ancestryFailed() error {
	if cp.ancErr != nil {
		return fmt.Errorf("plan: ancestry query failed: %w", cp.ancErr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// §13.4 parse and resolve
// ---------------------------------------------------------------------------

func (cp *computation) parseAndResolve() error {
	for _, rec := range cp.commits {
		data, err := cp.parser.Parse(rec.commit.Message)
		if data == nil {
			return fmt.Errorf("plan: %s: %w", rec.key, err)
		}
		// A parse error invalidates only the offending unit (§16); its
		// siblings still apply, so the error itself is reported rather than
		// returned.
		cp.liftDiagnostics(data, rec.key)

		rec.units = data.ValidUnits()
		rec.unitCount = len(data.Units)
		cp.resolveAuthors(rec)
		rec.scope = make([]map[string]bool, len(rec.units))
		for i, u := range rec.units {
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
		}
	}
	return nil
}

// liftDiagnostics carries ccme's diagnostics into the plan's, preserving code
// and severity. Flattening them onto one code would lose exactly the
// information §16 assigns a blast radius to.
func (cp *computation) liftDiagnostics(res *ccme.Result, commit string) {
	for _, d := range res.Diagnostics {
		level := LevelWarn
		if d.Severity == ccme.SeverityError {
			level = LevelError
		}
		cp.diags = append(cp.diags, Diagnostic{
			Code:    d.Code,
			Level:   level,
			Commit:  commit,
			Message: d.Message,
		})
	}
}

// ---------------------------------------------------------------------------
// §13.5 cancellation
// ---------------------------------------------------------------------------

func (cp *computation) collectCancels() {
	for _, rec := range cp.commits {
		for i, u := range rec.units {
			if !u.IsCancel() {
				continue
			}
			c := &cancelRec{
				key:      rec.key,
				scope:    rec.scope[i],
				closure:  cp.ancestorClosure(rec.key),
				pkgLabel: joinSorted(rec.scope[i]),
			}
			cp.cancels = append(cp.cancels, c)
		}
	}
}

// ancestorClosure is the set of examined commits that are ancestors-or-self of
// key. Commits outside every pending window are irrelevant by construction:
// cancellation only ever reaches unreleased work and never a published tag
// (§10.3).
func (cp *computation) ancestorClosure(key string) map[string]bool {
	out := make(map[string]bool)
	for _, rec := range cp.commits {
		if cp.ancestorOrSelf(rec.key, key) {
			out[rec.key] = true
		}
	}
	return out
}

// cancelledFor is cancelledFor(C, X) from §13.4a: C is an ancestor-or-self of
// some `cancel` commit whose resolved scope contains X.
//
// Work the package's baseline tag already contains is beyond cancellation's
// reach: §10.3 says cancellation never retracts a published tag, and a
// prerelease tag is a published tag. Without the guard, a cancel landing after
// beta.0 would discard the train's published units, shrink the effective bump
// below the baseline's core, and abort the run with E195 — for work that is
// already public and cannot be unshipped.
func (cp *computation) cancelledFor(commitKey, pkg string) bool {
	if cp.containedInBaseline(pkg, commitKey) {
		return false
	}
	for _, c := range cp.cancels {
		if !c.scope[pkg] {
			continue
		}
		if c.closure[commitKey] {
			c.discarded = true
			return true
		}
	}
	return false
}

// reportCancels emits W170 for a `cancel` that discarded nothing. §13.7d calls
// this out as the signal that the directive addressed the wrong package: a
// cancel aimed at a provider that has already published cannot retract what
// its consumers are owed, and the right target is the consumer.
//
// The warning is for a *live* cancel only. A cancel whose own commit has been
// discharged for every package it names belongs to history: whatever it had
// to discard, it discarded in an earlier run's window, and that discard is
// invisible now — the discarded units left the window together with the
// cancel. Warning "discarded nothing" about it on every later run (it stays
// in the union window as long as any other package's window spans it) would
// misreport a spent directive as a misaimed one.
func (cp *computation) reportCancels() {
	for _, c := range cp.cancels {
		if c.discarded || cp.cancelSpent(c) {
			continue
		}
		cp.warn(CodeEmptyCancel, c.pkgLabel, c.key,
			"cancel discarded nothing; already released work cannot be retracted (§10.3); to stop a pending catch-up, cancel the consumer (§13.7d)")
	}
}

// cancelSpent reports whether the cancel's own commit is discharged for every
// package its scope names: each has either released past it (the commit left
// its window) or published it inside a prerelease train (contained in the
// baseline). An inert cancel — one whose scope resolved to no package at all —
// is spent trivially; W131 already reports the inert unit.
func (cp *computation) cancelSpent(c *cancelRec) bool {
	for pkg := range c.scope {
		if cp.window[pkg][c.key] && !cp.containedInBaseline(pkg, c.key) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// §13.6a holds
// ---------------------------------------------------------------------------

// resolveHolds resolves each package's effective `Release-As` directive.
//
// Holds are resolved *before* propagation, so that a held package cannot bump
// its dependents with work it has not released. Precedence (§8.6): the newest
// surviving directive in the package's own window wins; a directive discarded
// by cancellation carries no weight, which is how a `cancel` clears a hold.
func (cp *computation) resolveHolds() {
	type directiveRec struct {
		directive releaseAs
		commit    string
		packages  int // how many packages the directive's scope-set addressed
	}

	// Collect every directive still in force, newest commit first. Deciding
	// in a second pass is what lets W158 be accurate: whether an `auto` lifted
	// anything is a question about the *older* directives it outranks, which a
	// single newest-wins pass has already discarded by the time it asks.
	pending := make(map[string][]directiveRec)
	var names []string
	for _, rec := range cp.commits { // newest first
		for i, u := range rec.units {
			directive, ok := unitReleaseAs(u)
			if !ok {
				continue
			}
			for _, name := range sortedKeys(rec.scope[i]) {
				if !cp.window[name][rec.key] {
					continue // already released: no longer in force
				}
				if cp.containedInBaseline(name, rec.key) {
					// Released by a prerelease of the train: consumed exactly
					// as a stable release consumes a directive. Without this a
					// pin published as 2.0.0-rc.0 would raise E153 ("does not
					// move forward") on every later run of the train.
					continue
				}
				if cp.cancelledFor(rec.key, name) {
					continue // a cancel clears a hold by discarding its unit
				}
				if len(pending[name]) == 0 {
					names = append(names, name)
				}
				pending[name] = append(pending[name], directiveRec{
					directive: directive,
					commit:    rec.key,
					packages:  len(rec.scope[i]),
				})
			}
		}
	}
	sort.Strings(names)

	for _, name := range names {
		recs := pending[name]
		winner := recs[0] // newest
		if len(recs) > 1 {
			cp.warn(CodeReleaseAsConflict, name, winner.commit,
				fmt.Sprintf("%d Release-As directives are pending; the newest (%q) wins",
					len(recs), winner.directive.raw))
		}
		switch {
		case winner.directive.isHold():
			cp.held[name] = true
		case winner.directive.isAuto():
			lifted := false
			for _, older := range recs[1:] {
				if older.directive.isHold() {
					lifted = true
					break
				}
			}
			if !lifted {
				cp.warn(CodeAutoNoHold, name, winner.commit,
					"Release-As: auto with no hold in force; this is already the default behaviour")
			}
		case winner.directive.isExact():
			cp.pinned[name] = pin{
				version:  winner.directive.version,
				commit:   winner.commit,
				packages: winner.packages,
			}
		}
		// No default: a Release-As value that is none of the three never
		// reaches here. ccme rejects it at parse time (E151), which
		// invalidates the unit, and an invalid unit is not in ValidUnits.
	}
}

// ---------------------------------------------------------------------------
// §13.6 direct bumps
// ---------------------------------------------------------------------------

// directBumps computes direct(P) = max over surviving tuples for P.
//
// The retention rule here is the *direct* one of §13.4: a tuple counts only
// while the commit is in the unit's own package's window. The propagation pass
// deliberately uses a different rule; see §13.7a for why a single rule serving
// both cannot be correct.
func (cp *computation) directBumps() {
	for _, rec := range cp.commits {
		for i, u := range rec.units {
			if u.IsCancel() { // cancel units are dropped after §13.5
				continue
			}
			// u.Bump is bumpOf(unit) from §13.6: the type mapping and "!"
			// alone. No footer overrides it — Release-As acts on the release,
			// not on the size of the change — and ccme has already applied
			// that rule.
			bump := u.Bump
			if bump == ccme.BumpNone {
				continue
			}
			for _, name := range sortedKeys(rec.scope[i]) {
				if !cp.window[name][rec.key] {
					continue
				}
				if cp.cancelledFor(rec.key, name) {
					continue
				}
				rel := cp.rel[name]
				rel.Units = append(rel.Units, u)
				rel.OwnBump = ccme.MaxBump(rel.OwnBump, bump)
				cp.ownContribs[name] = append(cp.ownContribs[name], groupContrib{key: rec.key, bump: bump})
				if !cp.containedInBaseline(name, rec.key) {
					rel.NewWork = true
					rel.FreshUnits = append(rel.FreshUnits, u)
				}
			}
		}
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
		discharged := !cp.window[name][rec.key] || cp.containedInBaseline(name, rec.key)
		if discharged || !(cp.cancelledFor(rec.key, name) || cp.held[name]) {
			out[name] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// §13.9 versions, §13.10 emit
// ---------------------------------------------------------------------------

func (cp *computation) finalise() {
	// Members of a shared-versioning space version as one group; their
	// per-package version computation is deferred to the group's.
	groups := cp.fixedGroups()
	shared := make(map[string]bool)
	for _, members := range groups {
		for _, m := range members {
			shared[m] = true
		}
	}

	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		rel.Bump = ccme.MaxBump(rel.OwnBump, rel.PropagatedBump)
		// A hold suspends a release a none package was never going to make;
		// leaving Held false keeps it out of the held counts and W154, so the
		// one exclusion the graph reports for it is its versioning.
		rel.Held = cp.held[name] && rel.Releasable()
		// §13.10: the plan marks its corrected and suppressed entries. Both
		// maps are keyed by unit, and rel.Units holds pointers into the same
		// parsed messages, so the marks travel with the units to every
		// consumer of the release.
		rel.Corrects = cp.corrects[name]
		rel.SuppressedNotes = cp.noteDrops[name]
		// The attribution, alongside the other two unit-keyed marks. The map is
		// shared rather than copied per package for the same reason the units
		// themselves are: it is read-only from here on, and a unit reaching two
		// packages is by the same people in both.
		rel.UnitAuthors = cp.unitAuthors
		rel.WindowAuthors, rel.FreshWindowAuthors = cp.collectWindowAuthors(name)
		if len(rel.WindowAuthors) > 0 {
			cp.log.Debug().Str("package", name).Int("window", len(rel.WindowAuthors)).
				Int("fresh", len(rel.FreshWindowAuthors)).Msg("release authors collected")
		}
		rel.Channel = cp.channel[name]
		if rel.Channel == "" {
			rel.Channel = rel.BaselineChannel
		}
		rel.ChannelFrom = cp.channelFrom[name]

		if shared[name] {
			continue // versioned by its group below
		}
		cp.versionOne(name, rel)
	}

	groupNames := make([]string, 0, len(groups))
	for gn := range groups {
		groupNames = append(groupNames, gn)
	}
	sort.Strings(groupNames)
	for _, gn := range groupNames {
		cp.applyFixedGroup(gn, groups[gn])
	}

	// Provider version movements, resolved after every version is final. The
	// topological order already guarantees a provider's Next is computed
	// before its consumers are visited, but a second pass keeps the guarantee
	// independent of the loop above ever changing shape.
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		rel.Updates = cp.providerUpdates(rel, name)
	}

	cp.reportCatchUp()
	cp.reportChannelOnly()
}

// newerCommit reports whether commit a is newer than commit b in the examined
// history ("" counts as infinitely old).
func (cp *computation) newerCommit(a, b string) bool {
	if b == "" {
		return a != ""
	}
	ra, oka := cp.byKey[a]
	rb, okb := cp.byKey[b]
	if !oka || !okb {
		return oka
	}
	return ra.rank < rb.rank // newest first: lower rank is newer
}

// versionOne applies the §13.9 computation to a single package: its pin when
// one is in force, the ordinary computation otherwise. The single call path
// is what keeps the independent loop and the fixed-group fallback agreeing
// about pin precedence.
func (cp *computation) versionOne(name string, rel *Release) {
	if !rel.Releasable() {
		// A none package carries no version: Next mirrors Current (both zero)
		// so nothing downstream reads a fabricated release, and a pin aimed
		// at it is inert rather than an error — the commit may legitimately
		// pin other packages of its scope.
		if _, ok := cp.pinned[name]; ok {
			cp.pkgWarn(rel, CodeNonePinned, "",
				"Release-As names a package with versioning \"none\"; the directive moves nothing")
		}
		rel.Next = rel.Current
		return
	}
	if p, ok := cp.pinned[name]; ok {
		cp.applyPin(rel, p)
		return
	}
	cp.computeVersion(rel)
}

// computeVersion implements §13.9 for a package with no exact Release-As.
func (cp *computation) computeVersion(rel *Release) {
	if !rel.Changed() {
		// Nothing to release. Next stays at the baseline so that reporting
		// shows the package's current position rather than a fabricated one.
		rel.Next = rel.Current
		if rel.HasBaseline {
			rel.Next = rel.Baseline
		}
		return
	}

	if rel.Channel == ccme.ChannelStable {
		// Graduation and the ordinary stable release are the same
		// computation: applyBump over the stable baseline, no suffix (§11.5).
		next := rel.Current.Bumped(rel.Bump)
		if rel.BaselineChannel != ccme.ChannelStable && versionLess(next, rel.Baseline.Core()) {
			// Reachable from hand-edited tags, and from a train an exact
			// Release-As raised above what the window computes (§11.5): the
			// pin's effect lives in the baseline tag, not in the window, so
			// the graduation must be pinned too.
			cp.pkgErr(rel, CodeGraduateNoIncrease,
				fmt.Sprintf("graduating to %s would go backwards from the %s baseline %s",
					next.String(), rel.BaselineChannel, rel.Baseline.String()))
			return
		}
		rel.Next = next
		cp.checkGreater(rel)
		return
	}

	next, ok := nextPrerelease(rel.Current, rel.Baseline, rel.HasBaseline, rel.Channel, rel.Bump)
	if !ok {
		cp.pkgErr(rel, CodeBadPrereleaseTag,
			fmt.Sprintf("baseline %s has no numeric prerelease counter, so the train cannot be continued (§11.3)",
				rel.Baseline.String()))
		return
	}

	// The channel-entry patch (§11.4). Entering a train from a clean stable
	// baseline computes a version SemVer ranks *below* the baseline: from
	// 1.2.0 with no bump, the target is 1.2.0 and the next is 1.2.0-beta.0.
	// One patch is the narrowest possible fix, and it is applied only for a
	// channel-only release, so it can never mask the genuine regression E195
	// exists to catch.
	if rel.Bump == ccme.BumpNone && rel.HasBaseline && !versionLess(rel.Baseline, next) {
		bumped, okPatch := nextPrerelease(rel.Current, rel.Baseline, rel.HasBaseline, rel.Channel, ccme.BumpPatch)
		if okPatch {
			cp.pkgWarn(rel, CodeChannelEntryPatch, "",
				fmt.Sprintf("channel-entry patch applied: %s would not have exceeded the baseline %s, so %s is released instead",
					next.String(), rel.Baseline.String(), bumped.String()))
			next = bumped
		}
	}

	rel.Next = next
	cp.checkGreater(rel)
}

// checkGreater enforces the §13.9 requirement that a computed version be
// strictly greater than the baseline by SemVer precedence.
func (cp *computation) checkGreater(rel *Release) {
	if !rel.HasBaseline {
		return
	}
	if versionLess(rel.Baseline, rel.Next) {
		return
	}
	cp.pkgErr(rel, CodeVersionNotGreater,
		fmt.Sprintf("computed version %s is not greater than the baseline %s",
			rel.Next.String(), rel.Baseline.String()))
}

// applyPin implements `Release-As: <ver>` and its guards (§8.6, §14.1).
//
// The pin replaces the computed version but never the computed bump: how large
// a change is, is a property of the change, and the type already declares it.
// E156 is what keeps that true — a breaking change cannot be shipped as a
// patch by writing a footer.
//
// A rejected pin has §16's unit-scoped blast radius: the offending directive
// contributes nothing, and everything else the window carries still applies.
// So each guard reports its error and then falls back to the ordinary
// computed version, exactly as if the pin had never been written — a sibling
// `feat` in the same commit still releases at its computed bump rather than
// being silently swallowed with the bad footer. (Whether the raised error
// stops the whole run is the caller's `commitErrors` policy, as with any
// other unit-scoped error.)
func (cp *computation) applyPin(rel *Release, p pin) {
	baseline := rel.Previous()
	computed := rel.Current.Bumped(rel.Bump)
	rejected := func() { cp.computeVersion(rel) }

	// E154's decidable cases — two explicit includes, or a term addressing the
	// whole workspace — are caught by the parser. This is the case that needs
	// the workspace: a glob or "." whose breadth is not visible in the text.
	if p.packages > 1 {
		cp.pkgErr(rel, CodePinMultiPackage,
			fmt.Sprintf("Release-As: %s applies to %d packages; an exact version can name only one",
				p.version.String(), p.packages))
		rejected()
		return
	}
	if !versionLess(baseline, p.version) {
		cp.pkgErr(rel, CodePinNotGreater,
			fmt.Sprintf("Release-As: %s does not move %s forward from %s",
				p.version.String(), rel.Pkg.Name, baseline.String()))
		rejected()
		return
	}
	// E156 is about how *large* a release is, so it is measured on the cores
	// alone. A prerelease ranks below its own core by SemVer precedence, and
	// comparing the versions whole would read "Release-As: 1.1.0-rc.0" against
	// a computed 1.1.0 as a downgrade, reject it, and fall back to shipping
	// the stable version the operator was asking to hold back. The core is
	// what carries the bump: 1.1.0-rc.0 is on its way to 1.1.0 and satisfies
	// the minor the commits require, while an rc of 1.1.0 under a computed
	// 2.0.0 still fails, which is the case the guard exists for. E153 above
	// keeps comparing whole versions, because "does this move forward" is
	// exactly the question precedence answers.
	if rel.Bump != ccme.BumpNone && versionLess(p.version.Core(), computed.Core()) {
		cp.pkgErr(rel, CodePinBelowBump,
			fmt.Sprintf("Release-As: %s is below %s, which the pending commits require",
				p.version.String(), computed.String()))
		rejected()
		return
	}
	// E157 is a default, not an opt-in: a typo'd major in a footer is a
	// mistake nothing downstream can undo, since §19.1 forbids moving a tag.
	if jump := int64(p.version.Major) - int64(computed.Major); jump > MaxMajorJump {
		cp.pkgErr(rel, CodePinMajorJump,
			fmt.Sprintf("Release-As: %s raises the major version %d above the computed %s, more than the limit of %d",
				p.version.String(), jump, computed.String(), MaxMajorJump))
		rejected()
		return
	}

	rel.Pinned = true
	rel.Next = p.version
	// A pin states the version, not the channel: the channel it lands on is
	// whatever the version itself says (§11.1), so that a pinned
	// "1.3.0-rc.0" enters the rc line and a pinned "1.3.0" graduates.
	rel.Channel = channelOf(p.version, true)
}

// providerUpdates resolves one release's Updates: every provider whose version
// the package picks up, in DueTo order first and then the remaining configured
// providers that are releasing, so a reader meets the propagated ones where
// they always were.
//
// cp.providers is indexed by edge, not by provider, so one pair declared under
// two dependency kinds appears twice; the seen set is what keeps it one update
// (narrow.go's waitingOn compensates the same way for the same reason).
func (cp *computation) providerUpdates(rel *Release, name string) []ProviderUpdate {
	provs := cp.providers[name]
	direct := make(map[string]bool, len(provs))
	for _, prov := range provs {
		direct[prov] = true
	}
	out := make([]ProviderUpdate, 0, len(rel.DueTo)+len(provs))
	seen := make(map[string]bool, len(rel.DueTo)+len(provs))
	add := func(prov string) {
		if seen[prov] {
			return
		}
		pr := cp.rel[prov]
		if pr == nil {
			return
		}
		seen[prov] = true
		out = append(out, ProviderUpdate{Name: prov, From: pr.Previous(), To: pr.Next})
	}
	for _, prov := range rel.DueTo {
		// A blast origin hops away answers "why is this package releasing";
		// the dependencies section speaks the package's own manifest
		// language instead. The movement arrives through a direct provider,
		// which the releasing half below names, so an indirect origin here
		// would put a package the manifests never mention into the record.
		if !direct[prov] {
			continue
		}
		add(prov)
	}
	for _, prov := range provs {
		// A provider that is not releasing has published nothing new for this
		// run to pick up. It still reaches Updates through DueTo when an
		// earlier run published it and this one is the catch-up (§13.7a).
		if pr := cp.rel[prov]; pr != nil && pr.Releasing() {
			add(prov)
		}
	}
	// A graduation's entry is what readers of the stable line actually see,
	// so its dependencies section spans the same window as its notes:
	// everything since the last stable release. A provider that moved during
	// the train was documented piecewise by the prerelease entries, which
	// those readers skip — without this widening the movement would reach no
	// stable entry at all. From is reconstructed off the provider's tags
	// (versionAt), because the planner keeps no state between runs, and it
	// also rewrites the From of the entries added above: their fresh movement
	// is a tail of the train-long one the graduation reports.
	if rel.HasBaseline && rel.Baseline.IsPrerelease() && !rel.Next.IsPrerelease() {
		for i := range out {
			if from, ok := cp.versionAt(out[i].Name, rel.StableCommit); ok {
				out[i].From = from
			}
		}
		for _, prov := range provs {
			pr := cp.rel[prov]
			if pr == nil || seen[prov] {
				continue
			}
			from, ok := cp.versionAt(prov, rel.StableCommit)
			if !ok || from.String() == pr.Previous().String() {
				continue
			}
			seen[prov] = true
			out = append(out, ProviderUpdate{Name: prov, From: from, To: pr.Previous()})
		}
	}
	return out
}

// versionAt is the package's newest published version as of the given commit:
// the newest parsed tag pointing at an ancestor of it. False when the package
// carried no tag there, or when the commit is unknown (a package never
// released on the stable line has no stable commit to ask about).
func (cp *computation) versionAt(pkg, commit string) (ccme.Version, bool) {
	if commit == "" {
		return ccme.Version{}, false
	}
	for _, t := range cp.tags[pkg] {
		if !t.Parsed || t.Commit == "" {
			continue
		}
		if cp.ancestorOrSelf(t.Commit, commit) {
			return t.Version, true
		}
	}
	return ccme.Version{}, false
}

// withoutIgnoredTags drops the masked tag names from a package's tag listing
// before baselines are read; see Options.IgnoredTags.
func (cp *computation) withoutIgnoredTags(tags gitx.Tags) gitx.Tags {
	if len(cp.ignoredTags) == 0 {
		return tags
	}
	kept := tags[:0:0]
	for _, t := range tags {
		if !cp.ignoredTags[t.Name] {
			kept = append(kept, t)
		}
	}
	return kept
}

// logReleases traces what each package resolved to, once the plan is final.
//
// These are the intermediates a wrong plan is diagnosed from and the emitted
// plan does not otherwise carry: which tag became the baseline, how large the
// window was, and what the bump was. Trace rather than debug because it is one
// line per package of the workspace, releasing or not, and the packages that
// did *not* release are half of what a reader is usually checking.
func (cp *computation) logReleases() {
	if !cp.log.Trace().Enabled() {
		return
	}
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil {
			continue
		}
		ev := cp.log.Trace().
			Str("package", name).
			Str("baseline", rel.Baseline.String()).
			Bool("hasBaseline", rel.HasBaseline).
			// The whole pending window since the stable baseline — on a train
			// it spans commits earlier prereleases already shipped, so it is
			// not "commits this release adds"; the name says so.
			Int("windowSinceStable", len(cp.window[name])).
			Str("bump", rel.Bump.String()).
			Str("channel", rel.Channel).
			Str("next", rel.Next.String()).
			Bool("releasing", rel.Releasing())
		if len(rel.DueTo) > 0 {
			ev = ev.Strs("dueTo", rel.DueTo)
		}
		ev.Msg("plan: package resolved")
	}
}

// reportCatchUp emits W193: a release whose entire cause is propagation from
// packages that are not themselves in this run's plan.
//
// The diagnostic MUST carry the origin's *published* version, so that a
// reviewer can see at a glance that the plan is discharging an earlier run's
// unfinished work rather than releasing something new (§13.10).
func (cp *computation) reportCatchUp() {
	inPlan := make(map[string]bool)
	for _, name := range cp.order {
		if rel := cp.rel[name]; rel != nil && rel.Releasing() {
			inPlan[name] = true
		}
	}

	for _, name := range cp.order {
		rel := cp.rel[name]
		// freshOwnBump, not OwnBump: own work the train already shipped keeps
		// deciding the target, but it does not explain why the package is
		// releasing again — a package whose only fresh cause is propagation
		// from an already-published provider is a catch-up whatever its train
		// history says.
		if rel == nil || !rel.Releasing() || rel.FreshOwnBump() {
			continue
		}
		if len(rel.Sources) == 0 {
			continue
		}
		// A releasing dependency explains the package's presence in the plan,
		// so it is an ordinary propagated release rather than a catch-up.
		explained := false
		for _, s := range rel.Sources {
			if inPlan[s.Provider] {
				explained = true
				break
			}
		}
		if explained {
			continue
		}
		rel.CatchUp = true

		origins := make([]string, 0, len(rel.DueTo))
		for _, provider := range rel.DueTo {
			published := "untagged"
			if pr := cp.rel[provider]; pr != nil && pr.HasBaseline {
				published = pr.Baseline.String()
			}
			origins = append(origins, provider+"@"+published)
		}
		cp.pkgWarn(rel, CodeCatchUp, "",
			fmt.Sprintf("catch-up release at %s: discharging work already published by %s",
				rel.Next.String(), strings.Join(origins, ", ")))
	}
}

// reportChannelOnly emits W202 for a package in the plan solely because its
// channel changed. It exists for the same reason W193 does: such a package
// appears with no commits of its own and no bump, and is otherwise
// unexplainable to whoever reviews the plan.
func (cp *computation) reportChannelOnly() {
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil || !rel.Releasing() {
			continue
		}
		// A pinned release is explained by its footer, not by its channel,
		// even when the pinned version happens to move it between lines; a
		// fixed-versioning ride is already explained by W234.
		//
		// Bump is deliberately train-wide here, unlike the catch-up scan's
		// freshOwnBump: a graduation publishes the train's whole window, so
		// any bump in it explains the release even when nothing is fresh.
		if rel.Bump != ccme.BumpNone || rel.Pinned || rel.FixedRide || !rel.ChannelChanged() {
			continue
		}
		rel.ChannelOnly = true
		cp.pkgWarn(rel, CodeChannelOnly, "",
			fmt.Sprintf("channel-only release at %s: %s",
				rel.Next.String(), rel.ChannelTransition()))
	}
}

// reportHeld emits W154. The engine MUST compute the would-be version for
// every held package anyway and report it, so the value needed to lift the
// hold is available without hand computation (§13.6a). The bump is not lost:
// it is recomputed from the same tuples by whichever run lifts the hold.
func (cp *computation) reportHeld() {
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil || !rel.Held || !rel.Changed() {
			continue
		}
		cp.pkgWarn(rel, CodeHeldVersion, "",
			fmt.Sprintf("held by Release-As: none; would release %s", rel.Next.String()))
	}
}

// ---------------------------------------------------------------------------
// diagnostics
// ---------------------------------------------------------------------------

func (cp *computation) warn(code, pkg, commit, msg string) {
	cp.diags = append(cp.diags, Diagnostic{Code: code, Level: LevelWarn, Pkg: pkg, Commit: commit, Message: msg})
}

func (cp *computation) err(code, pkg, commit, msg string) {
	cp.diags = append(cp.diags, Diagnostic{Code: code, Level: LevelError, Pkg: pkg, Commit: commit, Message: msg})
}

// relWarn attaches a warning to a package's release as well as to the run.
func (cp *computation) relWarn(pkg, code, commit, msg string) {
	d := Diagnostic{Code: code, Level: LevelWarn, Pkg: pkg, Commit: commit, Message: msg}
	if rel := cp.rel[pkg]; rel != nil {
		rel.Diagnostics = append(rel.Diagnostics, d)
	}
	cp.diags = append(cp.diags, d)
}

func (cp *computation) pkgWarn(rel *Release, code, commit, msg string) {
	cp.relWarn(rel.Pkg.Name, code, commit, msg)
}

func (cp *computation) pkgErr(rel *Release, code, msg string) {
	d := Diagnostic{Code: code, Level: LevelError, Pkg: rel.Pkg.Name, Message: msg}
	rel.Diagnostics = append(rel.Diagnostics, d)
	cp.diags = append(cp.diags, d)
}
