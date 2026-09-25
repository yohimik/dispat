// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package plan reads git history and computes the release plan: which
// packages changed, what their next versions and channels are, and in which
// order they must be processed.
//
// The implementation follows §13 of the release specification. Three
// properties of that text drive the whole design and are worth stating up
// front:
//
//   - Every question of the form "does this commit still count?" is answered
//     from tags and ancestry, and which package's release is consulted
//     depends on the purpose. A unit bumps its own package while that
//     package's window holds the commit (§13.6). It bumps a dependent until a
//     release of the unit's source that carries the commit is one the
//     dependent's own release reached (§13.4a): the dependent's window is the
//     cheap pending case, and a dependent that got ahead of its source is
//     owed the source's release all the same. Conflating the two silently
//     orphans consumers after a partial publish, the failure §13.7a exists
//     to prevent.
//
//   - Catch-up is therefore not a repair pass. There is no second traversal
//     and no timestamp comparison anywhere in this package. A consumer that
//     is behind is a consumer some source still owes, found by the ordinary
//     rule; the owed windows of §13.3 keep the commit it is owed in the
//     union once the source has released it in a run the consumer sat out.
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

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
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
	// CodeCommitRefUnavailable reports entry lines a configured commitRefs
	// policy could not reference: the planner has no sha for the commit that
	// carried them, which happens only where the Git implementation reports
	// none and the window key stands in for it. The lines render without a
	// reference rather than with one that resolves nowhere.
	//
	// W240 continues dispat's own range past W239 rather than starting a new
	// one. §16's registry runs to W215 and reserves nothing above it, and the
	// numbers a reader looks up are dispat's from W220 on; a code out of a
	// fresh hundred would say a boundary exists where none does.
	CodeCommitRefUnavailable = "W240"

	// CodeRepositoryBoundary reports a missing, ambiguous, conflicting or
	// unreachable cross-repository consumer baseline (§27.6). The same code
	// covers the other way a control position can fail to line up with a
	// source: an applicable control unit whose own gitlink snapshot pins a
	// revision the active source checkout does not contain, so the intent
	// would be projected onto code that never carried the snapshot it was
	// written against (validateControlProjectionHeads). Both are the one
	// question "where does this source sit relative to that control
	// position", and both are unanswerable rather than merely inconvenient,
	// which is why they share a repository-scoped code instead of splitting
	// into two an operator would have to learn separately.
	CodeRepositoryBoundary = "E333"
	// CodeRepositoryPrecedence reports incomparable source revisions where a
	// semantic rule requires one winner and no causal control directive does.
	CodeRepositoryPrecedence = "E334"
	// CodeExternalProviderAbsent reports an explicitly external provider whose
	// repository is not part of the current workspace snapshot.
	CodeExternalProviderAbsent = "W330"

	// CodeNoChangesTextEmpty marks a configured noChangesText that expanded to
	// nothing, or to whitespace alone: the names it interpolates are unset, so
	// the entry carries the built-in line naming the release's cause rather
	// than the sentence the configuration asked for. The record is written and
	// what it says is true, which is exactly why the substitution is invisible
	// in the file and has to be said in the log.
	CodeNoChangesTextEmpty = "W241"

	// CodePushMerged marks a release whose push was refused because commits
	// landed on the branch while the run was working, and that recovered by
	// pulling them and merging them with its release commit. Nothing the run
	// made is rewritten, so the tag still names the tree the release recorded;
	// the commits that arrived were not in the plan, sit outside that tag's
	// ancestry and belong to the next window. It is a warning rather than an
	// ordinary line because the branch the release went out on is not the one
	// the run was planned against.
	CodePushMerged = "W242"

	// CodePushConflicted marks a release whose mid-release merge could not be
	// joined: what landed changed the same content the release did. The
	// release still completes, because it has already published. This side of
	// every conflicting path is what the branch keeps, the other side is
	// pushed to a branch of its own so nothing is lost, and both records name
	// the files and that branch. It is a warning rather than a failure and
	// louder than W242 in what it asks for: somebody has to reconcile the two
	// sides, and only a person can.
	CodePushConflicted = "W243"

	// --- release outcomes (§13.7a, §13.9) ---

	// CodeCatchUp marks a release whose entire cause is propagation from a
	// package that is not itself in this run's plan (§13.7a).
	// Non-suppressible.
	CodeCatchUp = "W193"
	// CodeBlocked marks a package that was planned but not attempted because a
	// dependency failed to publish (§19.3). Non-suppressible.
	CodeBlocked = "W194"

	// --- delivery across one commit (§13.4a, §19.3) ---

	// CodeOwedAtBaseline marks a provider released, or about to be released,
	// at the baseline commit of a consumer it still owes, without that
	// consumer releasing after it in the same run. Two releases on one commit
	// have no ancestry order, so the consumer would read as served and stay on
	// the provider's old version with nothing left to detect it (§19.3). A run
	// that would do it is refused before anything publishes; a consumer that
	// failed after its provider published there is reported with the remedy
	// no later plan can compute.
	//
	// Run-scoped and not repository-scoped: it names one pair, and the plan is
	// correct. It is never one of a plan's Diagnostics, so the plan digest and
	// the commitErrors policy read the plan unchanged.
	CodeOwedAtBaseline = "E201"
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
	// baseline selection is ambiguous, so no correct plan exists. It also
	// rejects the same version recorded at two commits across the two stores
	// a write-capable run reads, its checkout and the remote it records to
	// (§13.2). Repository-scoped.
	CodeDuplicateVersionTag = "E191"
	// CodeShallowRepository rejects a shallow or grafted repository (§16): an
	// incomplete history hides tags and commits, and every window computed
	// over it is wrong in ways nothing downstream can detect. A checkout whose
	// history is complete and whose release records are not is the same
	// failure and the same code: a write-capable run whose remote records a
	// release on a commit its head reaches, and which it does not hold, plans
	// that version again (§13.2). Repository-scoped.
	CodeShallowRepository = "E196"
	// CodeDependencyCycle rejects a configured dependency graph with a cycle
	// (§16): no publish order exists. Repository-scoped.
	CodeDependencyCycle = "E200"
)

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
	CodeBadPrereleaseTag:     true, // E182
	CodeGraduateNoIncrease:   true, // E185
	CodeDuplicateVersionTag:  true, // E191
	CodeVersionNotGreater:    true, // E195
	CodeShallowRepository:    true, // E196
	CodeDependencyCycle:      true, // E200
	CodeRepositoryBoundary:   true, // E333
	CodeRepositoryPrecedence: true, // E334
}

// IsRepositoryScoped reports whether a diagnostic code aborts the run whatever
// the configured error policy (§16).
func IsRepositoryScoped(code string) bool { return repositoryScoped[code] }

// StaleSource is one provider contribution a package has not yet released. It
// is the consumer-side view of §9.2 that §13.7b asks implementations to offer:
// "which of my packages are behind their dependencies, and behind which?".
type StaleSource struct {
	Provider  string // the package the contribution came from
	Commit    string // the commit carrying the unit
	commitKey string // repository-qualified identity used only inside planning
	// Level is the number of hops to this package, measured from the unit's
	// source set as a whole the way §9.2 measures depth. A unit written over
	// several packages records one contribution per source within the unit's
	// depth of this package, all at the package's own level.
	Level int
	Bump  ccme.Bump // the bump the unit propagates
}

// ProviderUpdate is one provider whose version this release picks up, with
// the movement.
type ProviderUpdate struct {
	Name     string
	From, To ccme.Version
	// Tag is the release tag the provider's To version was published under,
	// rendered through the provider's own tag format. It is what a record
	// links a dependency line to; a name and a version alone would leave the
	// renderer to guess a format only the provider's configuration knows.
	Tag string
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
	Tagged          bool
	StableCommit    string // commit of the stable baseline tag; "" when untagged
	stableCommitKey string
	// BaselineCommit is the commit of the baseline tag — the newest tag of any
	// kind. On a prerelease train it is ahead of StableCommit, and everything
	// at or behind it has already been published by the train; for a stable
	// package the two coincide.
	BaselineCommit    string
	baselineCommitKey string
	FromInitials      bool // Current came from the config initials

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
	// owedBoundaries is, per provider in Sources, the raw commit this
	// package's newest release sits on in that provider's repository: the
	// boundary delivery is measured against (§13.4a). A provider released on
	// it, or behind it, would read as having delivered what it still owes, so
	// E201 compares the provider's release commit with it (§19.3). Absent for
	// a package that has never released.
	owedBoundaries map[string]string
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
	// versionFloor is the lowest core the package's own version computation
	// may land on: the core of its versioning group's line, written by the
	// group before the member is versioned. It is the zero version for every
	// package that versions on its own, and the zero version is below every
	// other, so nothing outside a group ever notices it.
	//
	// A member's window need not contain the work that set its group's core.
	// A ride carries none of it, and a leg that failed after its neighbours
	// published carries only part of it, so the member's own §11.4 target and
	// §11.5 graduation version can land below a position the group already
	// holds. The floor is applied before the E185 and E195 guards read the
	// result, which leaves both guards their meaning: a version below a
	// position the group never reached is still an error.
	versionFloor ccme.Version

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

	// UnitCommits is the commit each unit was written in, keyed by unit
	// pointer alongside the other unit-keyed marks and shared, unchanged, by
	// every release the unit reaches. A record uses it to point a line at the
	// change behind it; read it through UnitCommit, which is what decides
	// whether the key is a commit id at all.
	UnitCommits map[*ccme.Unit]string

	Diagnostics []Diagnostic
}

// UnitCorrects returns the records the unit restates, empty for an ordinary
// unit (§7.4, §13.10).
func (r *Release) UnitCorrects(u *ccme.Unit) []string { return r.Corrects[u] }

// IsUnitSuppressed reports a unit whose changelog entry a revert suppressed
// (§7.3).
func (r *Release) IsUnitSuppressed(u *ccme.Unit) bool { return r.SuppressedNotes[u] }

// AuthorsFor returns who the unit is by, empty for a unit nothing attributed.
func (r *Release) AuthorsFor(u *ccme.Unit) []Author { return r.UnitAuthors[u] }

// UnitCommit is the sha of the commit the unit was written in, empty when the
// planner has none to offer.
//
// A window key is a sha except where the Git implementation reports none, and
// what stands in for it there identifies an entry in this run's window rather
// than an object anything else could resolve. Answering empty is what keeps a
// synthetic key out of a published record, where it would render as a
// reference that leads nowhere.
func (r *Release) UnitCommit(u *ccme.Unit) string {
	key := r.UnitCommits[u]
	if strings.HasPrefix(rawHistoryKey(key), syntheticKeyPrefix) {
		return ""
	}
	return rawHistoryKey(key)
}

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

// IsChannelChanged reports whether the package is moving between channels.
func (r *Release) IsChannelChanged() bool { return r.Channel != r.BaselineChannel }

// IsPrerelease reports whether the version being released carries a
// prerelease component.
func (r *Release) IsPrerelease() bool { return r.Next.IsPrerelease() }

// ChannelTransition renders the channel movement the plan must display.
func (r *Release) ChannelTransition() string {
	if !r.IsChannelChanged() {
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
func (r *Release) IsChanged() bool {
	return (r.Bump != ccme.BumpNone && r.NewWork) || r.IsChannelChanged() || r.Pinned || r.FixedRide
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

// IsWithoutChanges reports whether the release carries no content of its own — no
// units, no provider updates — and exists only to keep the space's fixed
// versioning aligned. The changelog and the GitHub release render a single
// "no changes" entry for it.
func (r *Release) IsWithoutChanges() bool {
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

// IsReleasable reports whether the package takes part in the release flow at
// all: false only under versioning "none", whose packages exist to run
// scripts and are never versioned, tagged or published. Nil-safe like
// SharedDepth, and permanent where Held is per-run: a held package keeps a
// computed version waiting, a none package never has one.
func (r *Release) IsReleasable() bool {
	return r.Pkg == nil || r.Pkg.Space == nil || r.Pkg.Space.Versioning.IsReleasable()
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
func (r *Release) IsReleasing() bool {
	return r.IsChanged() && !r.Held && !r.Deselected && r.IsReleasable()
}

// IsInScriptWindow reports whether the package sits in the default script window:
// releasing, or a changed versioning-none package the selection kept. Run
// scripts are the one thing a none package exists for, so the window that
// would otherwise be exactly the plan admits it too.
func (r *Release) IsInScriptWindow() bool {
	return r.IsReleasing() || (!r.IsReleasable() && r.IsChanged() && !r.Deselected)
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
	case r.IsFreshOwnBump():
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
func (r *Release) IsFreshOwnBump() bool {
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
	// RepositoryHeads is the immutable source snapshot planning read, keyed by
	// stable repository identity. The release recorder uses it to detect an
	// intervening checkout mutation before any script or tag-only record can
	// accidentally publish the new HEAD under the old plan.
	RepositoryHeads map[string]string
	// RepositoryInputOrder and RepositoryInputs compactly encode the exact
	// repository history closure consulted for each package's plan: its owner,
	// transitive providers, shared-version group inputs, and applicable control
	// intent. RepositoryInputs values are immutable bitsets indexed by the
	// sorted RepositoryInputOrder and are interned when packages share a set.
	// They are internal execution metadata rather than part of JSON plan output.
	RepositoryInputOrder []string            `json:"-"`
	RepositoryInputs     map[string][]uint64 `json:"-"`
}

// IsInvalid reports whether any error-severity diagnostic was raised, of any
// blast radius. Whether that stops the run is a policy question the caller
// answers; see Fatal for the errors that stop it regardless.
func (p *Plan) IsInvalid() bool {
	for _, d := range p.Diagnostics {
		if d.Level == LevelError {
			return true
		}
	}
	return false
}

// IsFatal reports whether any repository-scoped error was raised. These abort
// the run whatever the configured policy: they mean no correct plan exists, so
// emitting a partial release would be releasing something nobody computed
// (§16).
func (p *Plan) IsFatal() bool {
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
		if r := p.Releases[name]; r != nil && r.IsReleasing() {
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

// edge is one dependency edge, provider -> consumer.
type edge struct {
	to   string
	kind model.DepKind
}

// commitRec is one commit of the union of all pending windows, parsed once.
type commitRec struct {
	commit gitx.Commit
	key    string
	// repository/root identify the history carrying commit. Empty repository
	// is the legacy single history.
	repository string
	root       string
	rank       int // position in history, 0 = newest
	units      []*ccme.Unit
	// unitCount is how many units the message carried, invalid ones included.
	// units holds only those that parsed, and a correction's "#n" selector
	// counts positions in the message (§7.4.1), so the two are different
	// numbers and both are needed.
	unitCount int
	// scope[i] is the resolved scope-set of units[i] (§6), as package names.
	scope []map[string]bool
	// derivedSet memoises derived(commit) (§6.2).
	derivedSet          map[string]bool
	propagations        []propagation
	channelPropagations []channelPropagation
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
	closure   func(commitKey string) bool // is it an ancestor-or-self of key
	discarded bool                        // whether it actually discarded anything
	pkgLabel  string
}

// Options are the inputs to Compute beyond the repository itself.
type Options struct {
	// Packages is the workspace at HEAD (§13.1).
	Packages []*model.Package
	// Dependencies are the graph edges.
	Dependencies []model.Dependency
	// InactiveExternalDependencies are syntactically valid external edges
	// whose providers are absent from the current workspace snapshot.
	InactiveExternalDependencies []model.Dependency
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
	// IgnoredTagsByRepository masks exact tag names only in the named
	// repository history. Step commands in a composed workspace use this to
	// avoid hiding an equal tag name belonging to another source. IgnoredTags
	// remains the legacy workspace-wide API.
	IgnoredTagsByRepository map[string][]string
	// Repositories enables composed history. Keys and Name are stable
	// .gitmodules identities; one entry has Control true. Empty preserves the
	// legacy single-history Git argument exactly.
	Repositories map[string]RepositoryHistory
	// RepositoryBaselines are explicit cross-repository release boundaries.
	RepositoryBaselines []RepositoryBaseline
	// LinkEvidence reads cross-repository boundaries from the fleet links
	// rather than from a control repository's checkpoints. It is what a
	// choreographed fleet sets, and it is false for every other plan, which
	// keeps the control checkpoint the only evidence they have ever used.
	LinkEvidence bool
	// HistoryStats optionally receives operation counts for scale tests.
	HistoryStats *HistoryStats
	// withoutDelivery computes the plan under the bump-axis admission that
	// preceded §13.4a's delivery test: a unit is admitted for a dependent only
	// while the dependent's own pending window holds its commit.
	//
	// It is unexported because no caller may ask for a plan the specification
	// no longer describes. It exists for the differential test in this package,
	// which asserts that the two rules agree release for release and
	// diagnostic for diagnostic on every history where nothing overtook a
	// commit, the claim that makes the delivery test a strict addition rather
	// than a change to planning at large.
	withoutDelivery bool
}

type computation struct {
	ctx      context.Context
	git      TagInventoryGitx
	log      zerolog.Logger
	root     string
	initials map[string]ccme.Version
	// nonPackage holds Options.NonPackageScopes as a set.
	nonPackage       map[string]bool
	nonPackageByRepo map[string]map[string]bool
	// ignoredTags is Options.IgnoredTags as a set; see that field.
	ignoredTags             map[string]bool
	ignoredTagsByRepository map[string]map[string]bool
	histories               map[string]RepositoryHistory
	controlRepo             string
	baselines               map[baselineKey]string
	baselineSpecs           []RepositoryBaseline
	stats                   *HistoryStats
	parsers                 map[string]*ccme.Parser
	repositoryHeads         map[string]string
	controlHistory          []gitx.ControlHistoryCommit
	// controlParents is controlHistory's parent graph by position, built
	// once with the history so that every control window is a walk over it
	// rather than a rebuilt map (controlCommitsAfter).
	controlParents   [][]int32
	controlPosition  map[string]int32
	controlIndexed   bool
	controlStates    map[string]*controlGitlinkState
	controlPathIndex map[string]int
	controlPathCount int
	// evidence is how this computation proves a cross-repository boundary:
	// the control checkpoints, or the fleet links. Chosen once at setup.
	evidence boundaryEvidence
	// linkPaths is each repository's fleet link paths, so a commit that only
	// moved a link is not read as a change to a package.
	linkPaths map[string]map[string]bool
	// linkedFleet records that this computation reads a choreographed fleet,
	// which is what makes a remedy naming a control repository wrong advice.
	linkedFleet bool

	pkgs       []*model.Package
	scopeDirs  []scopeDir       // prepared once; see prepareScopeDirs
	scopeByDir map[string][]int // scope folder -> indices into scopeDirs, in order
	byName     map[string]*model.Package
	byFold     map[string]string
	order      []string
	providers  map[string][]string
	edges      map[string][]edge

	parser *ccme.Parser

	rel    map[string]*Release
	tags   map[string]gitx.Tags  // package -> its tag listing, newest first
	window map[string]*commitSet // package -> the commits it has not released
	// anc answers ancestry among the union's commits by the marker pass, for
	// the repositories whose Git implementation lets Parents be trusted
	// (gitx.UnionHistoryx). behindUnion holds the tag commits the union does
	// not contain: a tag is reachable from HEAD (Gitx.Tags), a window is closed
	// under descendants, so such a commit has no ancestor inside the union.
	anc         *ancestryIndex
	ancTrusted  map[string]bool // folded repository name -> Parents are ancestry
	behindUnion map[string]bool
	// releasedCommits memoises, per package, the commits of its release tags
	// that the union holds: the candidate deliveries §13.4a's delivery test
	// walks. Built on first use and marked in one ancestry pass, because the
	// test is asked only about a target that got ahead of a commit and a plan
	// where nothing did must not pay for the markers (see admission.go).
	releasedCommits map[string][]string
	// withoutDelivery is Options.withoutDelivery; see that field.
	withoutDelivery bool
	closures        map[string]func(string) bool // cancel commit -> its closure, built once
	walks           *walkCache                   // §9.2 traversals, shared between units
	globs           *globIndex                   // §6.1 glob terms, resolved once each
	windowRefs      map[string][]map[string]bool // composed package -> shared repository windows
	// windowKeys names the history views a composed package's window was
	// assembled from, in the order they were attached; windowKey is the
	// single-history equivalent. Both are the cache keys the loaders already
	// compute, and packages released at the same boundaries share them, which
	// is what windowIdentity reads.
	windowKeys map[string][]string
	windowKey  map[string]string
	// windowAuthors memoises collectWindowAuthors by window identity. The
	// author collection reads every commit of the union per package, so
	// without this it is quadratic in a workspace whose packages share one
	// history and in a fleet whose repositories all contribute to it.
	windowAuthors       map[windowIdentity]windowAuthorSet
	repositoryReach     map[string][]string
	controlInputs       map[string]bool
	stableBoundaries    map[string]map[string]string // package -> repository -> qualified stable boundary
	publishedBoundaries map[string]map[string]string // package -> repository -> qualified latest-tag boundary
	stableTags          map[string]gitx.Tag
	latestTags          map[string]gitx.Tag
	controlSnapshots    map[string]controlSnapshot
	controlAmbiguous    map[string]bool
	commits             []*commitRec // newest first, deduplicated
	byKey               map[string]*commitRec
	parents             map[string][]string
	linked              bool // whether parent pointers are available

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

	// unitCommits is the commit each parsed unit was written in, filled
	// alongside the authors and shared the same way: the commit behind a unit
	// is a property of the message that carried it, not of the package it
	// resolved onto.
	unitCommits map[*ccme.Unit]string

	// channel axis state, produced by §9.2 phase 1 and settled by §13.8.
	proposed    map[string]channelPick
	proposedAll map[string][]channelPick
	channel     map[string]string
	channelFrom map[string]string

	// ancestry state: the memo behind ancestorOrSelf, the flag that the Git
	// implementation answered gitx.ErrNoAncestry (so only the fallbacks are
	// consulted from then on), and the first real git failure — which aborts
	// Compute rather than let the weaker fallbacks decide cancellation and
	// containment.
	ancCache map[[2]string]bool
	ancNoGit map[string]bool
	ancErr   error
	// filesErr is the first failure to read a commit's deferred changed
	// files outside the pre-pass (readUnforeseenFiles). It aborts Compute:
	// a scope resolved without the files it derives from is wrong.
	filesErr error
	// pinPresent memoises the commit-presence probe behind sourceContainsPin,
	// keyed by qualified revision. The guard asks the same question once per
	// applicable control unit, and a fleet catching up has many of those over
	// the same few gitlink pins; without the memo each one is a subprocess,
	// which is the cost ancCache exists to avoid on the ancestry side.
	pinPresent map[string]bool

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
// Graph work is one walk of the graph, O(V+E), per distinct (source package,
// edge kinds) a propagating unit names, and a prefix of it or a composition of
// several for every unit after the first (propagate.go, walk); the literal
// reading is a walk per propagating unit per phase. The CLI inventories
// reachable tags once for the workspace, reads the union of the pending
// windows in one bounded log walk and recovers each window from it by the
// marker pass (ancestry.go). The log range per distinct window origin remains
// the fallback when a history cannot provide a union walk.
func Compute(ctx context.Context, git TagInventoryGitx, opts Options) (*Plan, error) {
	pkgs := opts.Packages
	cp := &computation{
		ctx:              ctx,
		git:              git,
		log:              opts.Log,
		root:             opts.Root,
		initials:         opts.Initials,
		nonPackage:       make(map[string]bool, len(opts.NonPackageScopes)),
		nonPackageByRepo: make(map[string]map[string]bool, len(opts.Repositories)),
		ignoredTags:      make(map[string]bool, len(opts.IgnoredTags)),
		ignoredTagsByRepository: make(map[string]map[string]bool,
			len(opts.IgnoredTagsByRepository)),
		pkgs:                pkgs,
		byName:              make(map[string]*model.Package, len(pkgs)),
		byFold:              make(map[string]string, len(pkgs)),
		rel:                 make(map[string]*Release, len(pkgs)),
		tags:                make(map[string]gitx.Tags, len(pkgs)),
		window:              make(map[string]*commitSet, len(pkgs)),
		windowRefs:          make(map[string][]map[string]bool, len(pkgs)),
		windowKeys:          make(map[string][]string, len(pkgs)),
		windowKey:           make(map[string]string, len(pkgs)),
		windowAuthors:       make(map[windowIdentity]windowAuthorSet),
		repositoryReach:     make(map[string][]string, len(pkgs)),
		controlInputs:       make(map[string]bool, len(pkgs)),
		stableBoundaries:    make(map[string]map[string]string, len(pkgs)),
		publishedBoundaries: make(map[string]map[string]string, len(pkgs)),
		ownContribs:         make(map[string][]groupContrib, len(pkgs)),
		byKey:               make(map[string]*commitRec),
		parents:             make(map[string][]string),
		held:                make(map[string]bool),
		pinned:              make(map[string]pin),
		proposed:            make(map[string]channelPick),
		proposedAll:         make(map[string][]channelPick),
		channel:             make(map[string]string, len(pkgs)),
		channelFrom:         make(map[string]string),
		dropped:             make(map[dropKey]*correctionRec),
		corrects:            make(map[string]map[*ccme.Unit][]string),
		noteDrops:           make(map[string]map[*ccme.Unit]bool),
		unitAuthors:         make(map[*ccme.Unit][]Author),
		unitCommits:         make(map[*ccme.Unit]string),
		ancNoGit:            make(map[string]bool),
		histories:           make(map[string]RepositoryHistory, len(opts.Repositories)),
		baselines:           make(map[baselineKey]string, len(opts.RepositoryBaselines)),
		baselineSpecs:       append([]RepositoryBaseline(nil), opts.RepositoryBaselines...),
		stats:               opts.HistoryStats,
		parsers:             make(map[string]*ccme.Parser),
		repositoryHeads:     make(map[string]string),
		withoutDelivery:     opts.withoutDelivery,
	}
	for key, history := range opts.Repositories {
		if history.Name == "" {
			history.Name = key
		}
		cp.histories[globx.Fold(history.Name)] = history
		scopes := make(map[string]bool, len(history.NonPackageScopes))
		for _, scope := range history.NonPackageScopes {
			scopes[scope] = true
		}
		cp.nonPackageByRepo[globx.Fold(history.Name)] = scopes
		if history.Control {
			cp.controlRepo = history.Name
		}
	}
	cp.prepareLinkPaths()
	cp.linkedFleet = opts.LinkEvidence
	if opts.LinkEvidence {
		cp.evidence = newLinkEvidence(cp)
	} else {
		cp.evidence = checkpointEvidence{cp: cp}
	}
	for _, baseline := range opts.RepositoryBaselines {
		repository := baseline.Repository
		if history, ok := cp.histories[globx.Fold(repository)]; ok {
			repository = history.Name
		}
		key := baselineKey{consumer: globx.Fold(baseline.Consumer), tag: baseline.ReleaseTag,
			repository: globx.Fold(repository)}
		if _, duplicate := cp.baselines[key]; duplicate {
			cp.err(CodeRepositoryBoundary, baseline.Consumer, "", fmt.Sprintf(
				"duplicate repository baseline for release %s and repository %s", baseline.ReleaseTag, repository))
			return cp.fatalPlan(), nil
		}
		cp.baselines[key] = historyKey(repository, baseline.Revision)
	}
	for _, s := range opts.NonPackageScopes {
		cp.nonPackage[s] = true
	}
	for _, t := range opts.IgnoredTags {
		cp.ignoredTags[t] = true
	}
	for repository, tags := range opts.IgnoredTagsByRepository {
		set := make(map[string]bool, len(tags))
		for _, tag := range tags {
			set[tag] = true
		}
		cp.ignoredTagsByRepository[repository] = set
	}

	if err := cp.loadWorkspace(opts.Dependencies); err != nil { // §13.1
		if errors.Is(err, errFatalPlan) {
			return cp.fatalPlan(), nil
		}
		return nil, err
	}
	for _, dependency := range opts.InactiveExternalDependencies {
		cp.warn(CodeExternalProviderAbsent, dependency.Consumer, "", fmt.Sprintf(
			"external provider %q is absent; the %s edge is inactive for this workspace snapshot",
			dependency.Provider, dependency.Kind.String()))
	}
	if len(cp.histories) > 0 {
		cp.prepareRepositoryReach()
	}

	// §16 E196: a shallow or grafted clone hides commits and tags, so every
	// window and baseline computed over it is silently wrong. Checked before
	// any history is read.
	checkHistories := []RepositoryHistory{{Git: git}}
	if len(cp.histories) > 0 {
		checkHistories = checkHistories[:0]
		for _, history := range cp.histories {
			checkHistories = append(checkHistories, history)
		}
	}
	for _, history := range checkHistories {
		if shallow, err := history.Git.IsShallow(ctx); err != nil {
			return nil, fmt.Errorf("plan: checking repository %s completeness: %w", history.Name, err)
		} else if shallow {
			cp.err(CodeShallowRepository, "", "",
				fmt.Sprintf("repository %s is shallow or grafted: history is incomplete, so no correct plan can be computed; run `git fetch --unshallow` first", history.Name))
			return cp.fatalPlan(), nil
		}
		if len(cp.histories) > 0 {
			head, ok := history.Git.(interface {
				HeadSHA(context.Context) (string, error)
			})
			if !ok {
				return nil, fmt.Errorf("plan: repository %s cannot report its HEAD", history.Name)
			}
			sha, err := head.HeadSHA(ctx)
			if err != nil {
				return nil, fmt.Errorf("plan: reading repository %s HEAD: %w", history.Name, err)
			}
			cp.repositoryHeads[history.Name] = sha
		}
	}
	// The parser options come from the configuration file's `parser` object;
	// a zero Config is the specification defaults, so nothing changes for a
	// repository that configures nothing.
	parser, err := ccme.NewParser(opts.ParserConfig)
	if err != nil {
		return nil, err
	}
	cp.parser = parser
	cp.parsers[""] = parser
	for _, history := range cp.histories {
		configured, err := ccme.NewParser(history.ParserConfig)
		if err != nil {
			return nil, fmt.Errorf("plan: repository %s parser: %w", history.Name, err)
		}
		cp.parsers[globx.Fold(history.Name)] = configured
	}

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
	cp.log.Debug().Int("commits", len(cp.commits)).Msg("plan: window units parsed and scoped")
	if err := cp.resolveApplicableControlBoundaries(); err != nil {
		if errors.Is(err, errFatalPlan) {
			return cp.fatalPlan(), nil
		}
		return nil, err
	}
	cp.markDirectiveCommits()  // §13.11: one marker pass for the phases below
	cp.collectCancels()        // §13.5
	cp.applyCorrections()      // §13.4b, on the stream every phase below reads
	cp.suppressRevertedNotes() // §7.3, on the corrected stream
	cp.resolveHolds()          // §13.6a
	if err := cp.validateControlProjectionHeads(); err != nil {
		if errors.Is(err, errFatalPlan) {
			return cp.fatalPlan(), nil
		}
		return nil, err
	}
	cp.directBumps() // §13.6
	if err := cp.ancestryFailed(); err != nil {
		return nil, err
	}
	cp.log.Debug().Int("held", len(cp.held)).Int("pinned", len(cp.pinned)).
		Msg("plan: direct bumps resolved")

	// §13.7 is §9.2's three phases; §13.8 is invoked from inside it.
	cp.propagateChannels() // phase 1
	cp.log.Debug().Int("proposals", len(cp.proposed)).Msg("plan: channel proposals propagated")
	cp.resolveChannels() // phase 2 (§13.8)
	cp.log.Debug().Int("channels", len(cp.channel)).Msg("plan: channels resolved")
	cp.propagateBumps() // phase 3
	cp.log.Debug().Msg("plan: bumps propagated")

	cp.finalise()      // §13.9, §13.10
	cp.reportCancels() // W170
	cp.reportHeld()    // W154
	cp.logReleases()
	releasing := 0
	for _, rel := range cp.rel {
		if rel.IsReleasing() {
			releasing++
		}
	}
	cp.log.Debug().Int("packages", len(cp.order)).Int("releasing", releasing).
		Int("diagnostics", len(cp.diags)).Msg("plan: versions computed and ordered")
	if err := cp.ancestryFailed(); err != nil {
		return nil, err
	}

	repositoryInputOrder, repositoryInputs := cp.releaseRepositoryInputs()
	cp.releaseWorkspaceScratch()
	return &Plan{
		Order:                cp.order,
		Releases:             cp.rel,
		Providers:            cp.providers,
		Diagnostics:          cp.diags,
		RepositoryHeads:      cp.repositoryHeads,
		RepositoryInputOrder: repositoryInputOrder,
		RepositoryInputs:     repositoryInputs,
	}, nil
}

// These composition indexes have no role after the release plan is built;
// dropping them lets shared window memberships and parser/precedence indexes
// be collected before publication.
func (cp *computation) releaseWorkspaceScratch() {
	cp.repositoryReach = nil
	cp.controlInputs = nil
	cp.window = nil
	cp.windowRefs = nil
	cp.closures = nil
	// The attribution is on the releases now, and the keys that indexed it
	// answer no question the plan can still be asked.
	cp.windowAuthors = nil
	cp.windowKeys = nil
	cp.windowKey = nil
	cp.pinPresent = nil
	cp.publishedBoundaries = nil
	cp.stableTags = nil
	cp.latestTags = nil
	cp.controlSnapshots = nil
	cp.controlAmbiguous = nil
	if cp.evidence != nil {
		cp.evidence.drop()
	}
	cp.linkPaths = nil
	cp.baselines = nil
	cp.baselineSpecs = nil
	cp.parsers = nil
	cp.nonPackageByRepo = nil
	cp.byFold = nil
	cp.proposedAll = nil
	cp.ignoredTagsByRepository = nil
	// The delivery candidates are propagation's, and propagation is over.
	cp.releasedCommits = nil
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
func PackagesChangedSince(ctx context.Context, git TagInventoryGitx, opts Options, rev string) ([]string, error) {
	cp := &computation{
		ctx:              ctx,
		git:              git,
		root:             opts.Root,
		nonPackage:       make(map[string]bool, len(opts.NonPackageScopes)),
		nonPackageByRepo: make(map[string]map[string]bool, len(opts.Repositories)),
		pkgs:             opts.Packages,
		byName:           make(map[string]*model.Package, len(opts.Packages)),
		byFold:           make(map[string]string, len(opts.Packages)),
		histories:        make(map[string]RepositoryHistory, len(opts.Repositories)),
		parsers:          make(map[string]*ccme.Parser),
	}
	for key, history := range opts.Repositories {
		if history.Name == "" {
			history.Name = key
		}
		cp.histories[globx.Fold(history.Name)] = history
		scopes := make(map[string]bool, len(history.NonPackageScopes))
		for _, scope := range history.NonPackageScopes {
			scopes[scope] = true
		}
		cp.nonPackageByRepo[globx.Fold(history.Name)] = scopes
		if history.Control {
			cp.controlRepo = history.Name
		}
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
	cp.parsers[""] = parser
	for _, history := range cp.histories {
		configured, err := ccme.NewParser(history.ParserConfig)
		if err != nil {
			return nil, fmt.Errorf("plan: repository %s parser: %w", history.Name, err)
		}
		cp.parsers[globx.Fold(history.Name)] = configured
	}

	var records []*commitRec
	if len(cp.histories) == 0 {
		commits, err := git.Commits(ctx, rev)
		if err != nil {
			return nil, fmt.Errorf("plan: resolving commits since %q: %w", rev, err)
		}
		for _, commit := range commits {
			records = append(records, &commitRec{commit: commit, key: commitKey(commit), root: opts.Root})
		}
	} else {
		project, err := cp.projectSince(rev, opts.LinkEvidence)
		if err != nil {
			return nil, err
		}
		for _, history := range cp.histories {
			since := project(history)
			commits, err := history.Git.Commits(ctx, since)
			if err != nil {
				return nil, fmt.Errorf("plan: resolving repository %s commits since %q: %w", history.Name, since, err)
			}
			for _, commit := range commits {
				records = append(records, &commitRec{commit: commit, key: historyKey(history.Name, commitKey(commit)), repository: history.Name, root: history.Root})
			}
		}
	}
	units := make([][]*ccme.Unit, len(records))
	var needFiles []*commitRec
	for i, rec := range records {
		data, err := cp.parserFor(rec).Parse(rec.commit.Message)
		if data == nil {
			return nil, fmt.Errorf("plan: %s: %w", rec.key, err)
		}
		units[i] = data.ValidUnits()
		if rec.commit.AreFilesDeferred && areFilesNeededBy(units[i]) {
			needFiles = append(needFiles, rec)
		}
	}
	if err := cp.readDeferredFiles(needFiles); err != nil {
		return nil, err
	}
	selected := make(map[string]bool)
	for i, rec := range records {
		for _, u := range units[i] {
			scopes, written := unitScopes(u)
			for name := range cp.resolveScopeSet(scopes, written, rec).packages {
				selected[name] = true
			}
		}
	}
	if cp.filesErr != nil {
		return nil, cp.filesErr
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
		rel.Held = cp.held[name] && rel.IsReleasable()
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
		rel.UnitCommits = cp.unitCommits
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
	// After every version, fixed rides included: whether a release has an
	// entry to attribute is decided by then.
	cp.collectReleaseAuthors()

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
		rel.owedBoundaries = cp.collectOwedBoundaries(rel)
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

// AliasFilter recognises every alias tag the workspace's packages write, so a
// tag listing can be read without one of them being mistaken for a release.
//
// Every package's aliases, not only the listing's own. An alias is legal as
// long as no version can be read out of it, which says nothing about whose
// tagFormat it happens to share a shape with: A's "v1" is a perfectly legal
// alias beside B's "v{version}" release tags, and it lands in B's listing
// looking exactly like a release of B that nobody can parse. Filtering only
// the listing's own aliases left B's baseline collapsing to its initials from
// A's first release onwards.
//
// The formats are compiled once, when the filter is made, rather than once per
// tag each listing holds.
//
// The formats are indexed by the literal text they open with
// (gitx.AliasIndex), so an unparsed tag is tried against the aliases that
// could have written it rather than against every package's.
type AliasFilter struct{ aliases gitx.AliasIndex }

// NewAliasFilter compiles the alias formats of every package in the workspace.
// The zero AliasFilter matches nothing, which is what a workspace declaring no
// alias needs and what a caller with no package list can safely fall back to.
func NewAliasFilter(pkgs []*model.Package) AliasFilter {
	var matchers []gitx.AliasMatcher
	for _, p := range pkgs {
		if p == nil || p.Space == nil {
			continue
		}
		for _, a := range p.Space.AliasTags {
			matchers = append(matchers, gitx.AliasFormat(a.Format).Matcher(p.Name))
		}
	}
	return AliasFilter{aliases: gitx.NewAliasIndex(matchers)}
}

// Without drops the workspace's alias tags from one package's listing.
//
// An alias is written on every release, under a name of its own, and is never
// a release: "v1" from a "v{major}" alias beside a "v{version}" tagFormat has
// the tagFormat's shape, becomes the newest tag by creation date the moment it
// is written, and carries nothing a version can be read out of. Left in, it
// would be selected as the baseline and the package would look unreleased from
// that release onwards, which is the single-repository convention GitHub
// composites are published under failing on its second run.
//
// Only names that carry no version are dropped, and only when some package's
// alias format could have written them. A tag whose version does parse is a
// release whatever its shape, and one that parses no better but matches no
// alias is a malformed release tag: that is the case the initials fallback is
// for, and it still stops the baseline being read from an older tag.
//
// Every reader of a baseline goes through this, which is what stops the two
// answers drifting: the planner, and the compute command's manifest baselines.
func (f AliasFilter) Without(tags gitx.Tags, pkg string, log zerolog.Logger) gitx.Tags {
	if f.aliases.Len() == 0 {
		return tags
	}
	kept := tags[:0:0]
	for _, t := range tags {
		if t.Parsed || !f.aliases.IsMatch(t.Name) {
			kept = append(kept, t)
			continue
		}
		log.Debug().Str("package", pkg).Str("tag", t.Name).
			Msg("tag is one of the workspace's moving aliases, not a release")
	}
	return kept
}

// withoutIgnoredTags drops the masked tag names from a package's tag listing
// before baselines are read; see Options.IgnoredTags.
func (cp *computation) withoutIgnoredTags(repository string, tags gitx.Tags) gitx.Tags {
	qualified := cp.ignoredTagsByRepository[repository]
	if len(cp.ignoredTags) == 0 && len(qualified) == 0 {
		return tags
	}
	kept := tags[:0:0]
	for _, t := range tags {
		if !cp.ignoredTags[t.Name] && !qualified[t.Name] {
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
			Int("windowSinceStable", cp.windowSize(name)).
			Str("bump", rel.Bump.String()).
			Str("channel", rel.Channel).
			Str("next", rel.Next.String()).
			Bool("releasing", rel.IsReleasing())
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
		if rel := cp.rel[name]; rel != nil && rel.IsReleasing() {
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
		if rel == nil || !rel.IsReleasing() || rel.IsFreshOwnBump() {
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
		if rel == nil || !rel.IsReleasing() {
			continue
		}
		// A pinned release is explained by its footer, not by its channel,
		// even when the pinned version happens to move it between lines; a
		// fixed-versioning ride is already explained by W234.
		//
		// Bump is deliberately train-wide here, unlike the catch-up scan's
		// freshOwnBump: a graduation publishes the train's whole window, so
		// any bump in it explains the release even when nothing is fresh.
		if rel.Bump != ccme.BumpNone || rel.Pinned || rel.FixedRide || !rel.IsChannelChanged() {
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
		if rel == nil || !rel.Held || !rel.IsChanged() {
			continue
		}
		cp.pkgWarn(rel, CodeHeldVersion, "",
			fmt.Sprintf("held by Release-As: none; would release %s", rel.Next.String()))
	}
}
