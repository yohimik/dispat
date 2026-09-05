// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// The DISPAT_* environment renderers: the workspace listing, the per-task
// provider updates, the per-package script environment and the run-level
// outcome variables. They are pure functions of the plan and the results —
// nothing here schedules or executes — which is why they live apart from the
// executor's pump in executor.go.

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// StaticEnv places the configuration's static env pairs before the computed
// ones. exec's last-wins duplicate rule then keeps a static key from ever
// shadowing a computed DISPAT_* variable, which is what makes the computed
// namespace dependable: a script reads DISPAT_VERSION knowing no configuration
// could have redefined it.
//
// Static values are expanded first. $NAME and ${NAME} resolve against the
// computed set, then the process environment, because cmd.Env values are never
// shell-expanded and `CUSTOM_TAG: custom_$DISPAT_VERSION` has to mean what it
// reads. $$ is a literal dollar and an unknown name expands to nothing, as in
// the shell.
//
// A configuration with no static env pays nothing: computed is returned as it
// arrived, with no map built and no allocation made.
func StaticEnv(static, computed []string) []string {
	if len(static) == 0 {
		return computed
	}
	vars := make(map[string]string, len(computed))
	for _, kv := range computed {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	lookup := func(name string) string {
		if name == "$" {
			return "$"
		}
		if v, ok := vars[name]; ok {
			return v
		}
		return os.Getenv(name)
	}
	out := make([]string, 0, len(static)+len(computed))
	for _, kv := range static {
		k, v, _ := strings.Cut(kv, "=")
		out = append(out, k+"="+os.Expand(v, lookup))
	}
	return append(out, computed...)
}

// providerUpdate is one live provider update, flattened into the
// DISPAT_UPDATED_* variables of a consumer's scripts.
type providerUpdate struct {
	Package    string
	Space      string
	OldVersion string
	NewVersion string
	// Channel is the channel the provider is releasing on, so that a version
	// script can tell a prerelease dependency from a stable one — the case
	// §9.4 reports as W203 and cannot make safe by itself.
	Channel string
	// Tag is the release tag the new version was published under, rendered
	// through the *provider's* own tagFormat. A consumer's script cannot
	// derive it: the format belongs to the provider's configuration, and a
	// consumer guessing at it would fetch a tag nobody created. It is also
	// what a record's dependency line links to, which is why a nested step
	// command reads it back out of this listing.
	Tag string
}

// workspaceVersion is one entry of the workspace listing: a package and the
// version it will carry at the end of the run.
//
// dispat has no manifest model — reconciling declared ranges is the version
// script's job (§9.4) — so this is the input that job needs. The breadth
// matters: §9.4 requires reconciliation against *every* workspace dependency,
// not only those released in the same run, because a dependency may have been
// published by an earlier run whose dependent leg failed (§13.7a). Restricting
// it to this run's releases reopens exactly that hole.
type workspaceVersion struct {
	Package string
	Version string
	Channel string
	// Releasing reports whether the version is this run's plan (true) or the
	// package's existing baseline (false). A false here with a version newer
	// than the consumer's declared range is the W197 case.
	Releasing bool
}

// liveProviderUpdates returns — with mu held — the provider versions this
// package has to pick up, excluding providers that failed or were skipped:
// their new versions were never released, so manifests must not point at them.
// Providers whose publish is still pending (possible for the version/build
// stages when isBuildWaitingPublish is false) are included.
//
// The set is plan.Release.Updates, which is every provider that moved rather
// than only the ones that propagated a bump (see its doc comment). A provider
// releasing beside its consumer with no caret between them is a version the
// consumer's scripts have to see, and DueTo does not contain it.
func liveProviderUpdates(pkg string, p *plan.Plan, results map[string]*Result) []providerUpdate {
	rel := p.Releases[pkg]
	updates := make([]providerUpdate, 0, len(rel.Updates))
	for _, u := range rel.Updates {
		if r, ok := results[u.Name]; ok && (r.Status == StatusFailed || r.Status == StatusSkipped) {
			continue
		}
		pr := p.Releases[u.Name]
		updates = append(updates, providerUpdate{
			Package:    u.Name,
			Space:      pr.Pkg.Space.Name,
			OldVersion: u.From.String(),
			NewVersion: u.To.String(),
			Channel:    pr.Channel,
			Tag:        u.Tag,
		})
	}
	return updates
}

// WorkspaceEnv renders the workspace listing as plain variables, one set per
// package, readable from any shell without a parser:
//
//	DISPAT_WORKSPACE_PACKAGES            space-separated keys, iteration order
//	DISPAT_WORKSPACE_<KEY>_NAME          the raw package name
//	DISPAT_WORKSPACE_<KEY>_VERSION       end-of-run version
//	DISPAT_WORKSPACE_<KEY>_CHANNEL       end-of-run channel
//	DISPAT_WORKSPACE_<KEY>_RELEASING     true / false
//
// Two names may sanitise to one key ("core-utils", "core.utils"); the first
// in plan order keeps it and the loser is omitted from the listing with a
// warning, rather than silently overwriting fields one by one. A workspace
// hitting this renames one of the pair.
func WorkspaceEnv(p *plan.Plan, log zerolog.Logger) []string {
	entries := workspaceVersions(p)
	keys := make([]string, 0, len(entries))
	taken := make(map[string]string, len(entries))
	out := make([]string, 0, len(entries)*4+1)
	for _, e := range entries {
		k := plan.EnvKey(e.Package)
		if prev, dup := taken[k]; dup {
			log.Warn().Str("package", e.Package).Str("key", k).
				Msgf("workspace env: key collides with %q, package omitted from DISPAT_WORKSPACE_* variables", prev)
			continue
		}
		taken[k] = e.Package
		keys = append(keys, k)
		pre := plan.WorkspaceEnvPrefix + k
		out = append(out,
			pre+"_NAME="+e.Package,
			pre+"_VERSION="+e.Version,
			pre+"_CHANNEL="+e.Channel,
			pre+"_RELEASING="+boolEnv(e.Releasing))
	}
	return append(out, plan.WorkspacePackagesEnvVar+"="+strings.Join(keys, " "))
}

// updatedEnv renders the live provider updates the same way, under
// DISPAT_UPDATED_*:
//
//	DISPAT_UPDATED_<KEY>_NAME            the raw provider name
//	DISPAT_UPDATED_<KEY>_SPACE           the space the provider belongs to
//	DISPAT_UPDATED_<KEY>_OLD_VERSION     version the consumer was on
//	DISPAT_UPDATED_<KEY>_NEW_VERSION     version the consumer picks up
//	DISPAT_UPDATED_<KEY>_CHANNEL         channel the provider releases on
//	DISPAT_UPDATED_<KEY>_TAG             tag the new version is published under
//
// It is built per task because the update list is: which providers are still
// live differs between a package's build and its publish.
//
// Every field of a listed key is written even when its value is empty, the
// same contract the listings themselves keep: a script addressing a key that
// the listing names reads a variable rather than an unset name. A plan always
// renders a tag for an update it carries, so an empty _TAG means the update
// reached this renderer from somewhere that could not state one, and saying so
// with an empty value is honest where omitting the variable would be
// indistinguishable from a typo in the key.
func updatedEnv(updates []providerUpdate) []string {
	keys := make([]string, 0, len(updates))
	taken := make(map[string]bool, len(updates))
	out := make([]string, 0, len(updates)*6+1)
	for _, u := range updates {
		k := plan.EnvKey(u.Package)
		if taken[k] {
			continue // same first-come rule as WorkspaceEnv, already warned there
		}
		taken[k] = true
		keys = append(keys, k)
		pre := plan.UpdatedEnvPrefix + k
		out = append(out,
			pre+"_NAME="+u.Package,
			pre+"_SPACE="+u.Space,
			pre+"_OLD_VERSION="+u.OldVersion,
			pre+"_NEW_VERSION="+u.NewVersion,
			pre+"_CHANNEL="+u.Channel,
			pre+"_TAG="+u.Tag)
	}
	// Set even when empty: `for k in $DISPAT_UPDATED_PACKAGES` should iterate
	// zero times, not read an unset variable.
	return append(out, plan.UpdatedPackagesEnvVar+"="+strings.Join(keys, " "))
}

// RunEnv builds the environment of the run-level hooks (postAll and the
// commit/push hooks): the workspace listing plus the run outcome, rendered the
// same way as the per-task listings so a script reads both with one idiom:
//
//	DISPAT_PUBLISHED_PACKAGES            space-separated keys of published packages
//	DISPAT_FAILED_PACKAGES               keys of failed packages
//	DISPAT_SKIPPED_PACKAGES              keys of skipped (blocked) packages
//	DISPAT_CANCELLED_PACKAGES            keys of packages an interrupted run never ran
//	DISPAT_UNPLANNED_PACKAGES            keys of packages the plan did not release
//	                                     (unchanged, or held by Release-As: none)
//	DISPAT_RESULT_<KEY>_NAME             the raw package name
//	DISPAT_RESULT_<KEY>_STATUS           published / failed / skipped / cancelled
//	DISPAT_RESULT_<KEY>_OLD_VERSION      version before the run
//	DISPAT_RESULT_<KEY>_NEW_VERSION      version the run planned
//	DISPAT_RESULT_<KEY>_CHANNEL          release channel
//	DISPAT_RESULT_<KEY>_FAILED_STAGE     stage that failed (failed packages only)
//	DISPAT_RESULT_<KEY>_BLOCKED_BY       blocking provider (blocked packages only)
//
// Keys collide under the same first-in-plan-order rule as WorkspaceEnv; the
// list variables are set even when empty so a shell for-loop iterates zero
// times instead of reading an unset variable.
func RunEnv(p *plan.Plan, results map[string]*Result, log zerolog.Logger) []string {
	env := WorkspaceEnv(p, log)
	var published, failed, skipped, cancelled, unplanned []string
	taken := make(map[string]bool, len(p.Order))
	for _, name := range p.Order {
		k := plan.EnvKey(name)
		if taken[k] {
			continue // collision already warned about by WorkspaceEnv
		}
		taken[k] = true
		res, ok := results[name]
		if !ok {
			unplanned = append(unplanned, k)
			continue
		}
		switch res.Status {
		case StatusPublished:
			published = append(published, k)
		case StatusFailed:
			failed = append(failed, k)
		case StatusCancelled:
			cancelled = append(cancelled, k)
		default:
			skipped = append(skipped, k)
		}
		pre := "DISPAT_RESULT_" + k
		env = append(env,
			pre+"_NAME="+name,
			pre+"_STATUS="+res.Status.String(),
			pre+"_OLD_VERSION="+res.From.String(),
			pre+"_NEW_VERSION="+res.To.String(),
			pre+"_CHANNEL="+res.Channel)
		if res.FailedStage != "" {
			env = append(env, pre+"_FAILED_STAGE="+res.FailedStage)
		}
		if res.BlockedBy != "" {
			env = append(env, pre+"_BLOCKED_BY="+res.BlockedBy)
		}
	}
	return append(env,
		"DISPAT_PUBLISHED_PACKAGES="+strings.Join(published, " "),
		"DISPAT_FAILED_PACKAGES="+strings.Join(failed, " "),
		"DISPAT_SKIPPED_PACKAGES="+strings.Join(skipped, " "),
		"DISPAT_CANCELLED_PACKAGES="+strings.Join(cancelled, " "),
		"DISPAT_UNPLANNED_PACKAGES="+strings.Join(unplanned, " "))
}

// workspaceVersions lists every workspace package with the version it will
// carry at the end of the run: its planned version where it is releasing, its
// baseline otherwise (§9.4).
func workspaceVersions(p *plan.Plan) []workspaceVersion {
	out := make([]workspaceVersion, 0, len(p.Order))
	for _, name := range p.Order {
		rel := p.Releases[name]
		if rel == nil {
			continue
		}
		entry := workspaceVersion{Package: name, Channel: rel.Channel}
		if rel.Releasing() {
			entry.Version, entry.Releasing = rel.Next.String(), true
		} else if rel.HasBaseline {
			entry.Version = rel.Baseline.String()
		} else {
			entry.Version = rel.Current.String()
		}
		out = append(out, entry)
	}
	return out
}

// CommandEnv builds the full per-package DISPAT_* environment outside a
// release run: the package variables, the workspace listing (wsVars, built
// once per run with WorkspaceEnv and shared across packages) and the
// package's provider updates, all considered live since no run is deciding
// otherwise. It is what `dispat run <script>` hands a space's run scripts, so
// a script is movable between a stage and a run script without changing what
// it reads.
func CommandEnv(p *plan.Plan, pkg, stage string, wsVars []string) []string {
	return packageEnv(p, pkg, wsVars, liveProviderUpdates(pkg, p, nil), stage)
}

// packageEnv builds the DISPAT_* environment of one package's script or hook.
// stage is what DISPAT_STAGE carries: the stage name for a stage script, the
// hook name ("beforeBuild", "postPublish", ...) for a hook — every hook gets
// the same full environment as the stage scripts, distinguished only there.
//
// What the release says about itself comes from plan.Release.Vars, which the
// record text interpolates as well; everything below it is what a release
// alone cannot answer — the stage, the workspace, the live provider updates.
func packageEnv(p *plan.Plan, pkg string, wsVars []string, updates []providerUpdate, stage string) []string {
	rel := p.Releases[pkg]
	env := append(rel.Vars(), "DISPAT_STAGE="+stage)
	// The release notes, grouped exactly as the changelog and the GitHub
	// release group their sections: units bumping major are breaking changes,
	// minor are features, patch are fixes. One headline per line — bodies are
	// multiline prose that would destroy the line-per-entry contract, and they
	// stay in the changelog. Empty (not unset) when a group has no entries, so
	// a line-wise loop iterates zero times. The announce stage is the audience,
	// but like every listing they go to every stage, keeping scripts movable.
	env = append(env,
		"DISPAT_BREAKING_CHANGES="+unitLines(rel, ccme.BumpMajor),
		"DISPAT_FEATURES="+unitLines(rel, ccme.BumpMinor),
		"DISPAT_FIXES="+unitLines(rel, ccme.BumpPatch),
		// The dependencies section of the same notes: one "name: old -> new"
		// line per live provider update, matching what the changelog and the
		// GitHub release render — the DISPAT_UPDATED_* listing carries the
		// same data field by field for scripts that want it addressable.
		"DISPAT_DEPENDENCIES="+dependencyLines(updates))
	// Both listings go to every stage. The version stage is where manifests
	// are reconciled (§9.4), but a build baking versions into artefacts and a
	// publish choosing dist-tags read the same state, and giving each stage
	// the same environment keeps a script movable between them.
	env = append(env, wsVars...)
	env = append(env, updatedEnv(updates)...)
	// The accumulated script outputs: everything earlier scripts of the
	// package exported through their DISPAT_OUTPUT files, as
	// DISPAT_OUTPUT_<NAME> variables plus the DISPAT_OUTPUTS listing.
	env = append(env, rel.OutputVars()...)
	// The configuration's static env, already resolved for this package (the
	// top-level, space and package layers merged at load time), goes in front
	// so every computed variable above wins a name clash.
	return StaticEnv(rel.Pkg.Space.Env, env)
}

// dependencyLines renders the live provider updates the way the changelog's
// dependencies section does: "core: 1.2.3 -> 1.3.0", one per line.
func dependencyLines(updates []providerUpdate) string {
	var lines []string
	for _, u := range updates {
		lines = append(lines, u.Package+": "+u.OldVersion+" -> "+u.NewVersion)
	}
	return strings.Join(lines, "\n")
}

// unitLines returns the descriptions of the release's notes units carrying
// the given bump, newline-separated — the grouping changelog.RenderSections
// uses for its breaking/features/fixes sections. NotesUnits keeps the
// variables aligned with the changelog entry: a prerelease reports only its
// own changeset, a stable release the whole pending window.
//
// Deliberately description-only, and deliberately unaffected by the authors
// entry format. DISPAT_BREAKING_CHANGES, DISPAT_FEATURES and DISPAT_FIXES are
// a contract with scripts that already parse them line by line, so appending
// an attribution here would change what an existing announce or release-notes
// script reads without that script asking for anything. The attribution
// belongs to the rendered record, which is where the configuration puts it.
func unitLines(rel *plan.Release, kind ccme.Bump) string {
	var lines []string
	for _, c := range rel.NotesUnits() {
		if c.Bump == kind {
			lines = append(lines, c.Header.Description)
		}
	}
	return strings.Join(lines, "\n")
}

func boolEnv(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
