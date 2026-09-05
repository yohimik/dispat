// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

// The variables a release carries into everything that reads it: the script
// environment (internal/release composes these with the workspace listing, the
// provider updates and the stage name) and the record text, where a changelog
// or GitHub release interpolates them into its own title, header and footer.
// They live here, next to the release, so the two readers cannot drift: a
// variable added for a script is a variable a footer can name.

import (
	"strconv"
	"strings"
)

// PackageEnvVar names the package a per-package script or hook is running
// for. A nested dispat command reads it to know whose environment it was
// handed, which is how the standalone github step attributes the
// GitHubExport opt-in it finds there.
const PackageEnvVar = "DISPAT_PACKAGE"

// GroupEnvVar names the versioning group the package shares its version with:
// the declared versionGroups entry, or the space's own name where a space
// versions as a group. It is a package's third address beside its name and its
// space, and a script that has to know which packages move together — a
// coordinated deploy, a shared release note — cannot derive it from the other
// two. It is left unset for an independently versioned package; see Vars.
const GroupEnvVar = "DISPAT_GROUP"

// NewVersionEnvVar and TagEnvVar carry the run's planned version and tag for
// the package a script runs for. The step commands' wiring reads them back to
// hold a mid-run replan to the run's own answers; see the app's step wiring.
const (
	NewVersionEnvVar = "DISPAT_NEW_VERSION"
	TagEnvVar        = "DISPAT_TAG"
)

// UpdatedPackagesEnvVar and UpdatedEnvPrefix carry the run's live provider
// updates into a package's scripts (internal/release renders them, one
// DISPAT_UPDATED_<KEY>_* set per provider). The step commands' wiring reads
// them back so a wired record states the run's provider movements rather
// than a mid-run replan's; see the app's step wiring.
const (
	UpdatedPackagesEnvVar = "DISPAT_UPDATED_PACKAGES"
	UpdatedEnvPrefix      = "DISPAT_UPDATED_"
)

// WorkspacePackagesEnvVar and WorkspaceEnvPrefix carry the workspace listing
// the same way (internal/release renders it). The wiring reads the releasing
// entries back to mask every tag the run has already written, so a mid-run
// replan reads the same baselines the run itself started from.
const (
	WorkspacePackagesEnvVar = "DISPAT_WORKSPACE_PACKAGES"
	WorkspaceEnvPrefix      = "DISPAT_WORKSPACE_"
)

// OutputEnvPrefix is what an exported script output is published under;
// scripts may also spell the name with this prefix already attached in their
// export line.
const OutputEnvPrefix = "DISPAT_OUTPUT_"

// OutputSourceEnvPrefix is where an export's provenance is published:
// DISPAT_OUTPUT_SOURCE_<NAME>=<package>:<stage>.
const OutputSourceEnvPrefix = "DISPAT_OUTPUT_SOURCE_"

// ReservedEnvPrefix guards the rest of the script environment: an export whose
// name starts with DISPAT_ but is neither a DISPAT_OUTPUT_-spelled output nor
// GitHubExport is rejected rather than passed through, because it would
// otherwise override a real DISPAT_* variable in every later script.
const ReservedEnvPrefix = "DISPAT_"

// Vars renders everything a release says about itself as NAME=value pairs:
// the package it belongs to, the versions it moves between, the channel it
// moves on and the tag it will carry.
//
// It is the release-derived part of the per-package script environment, and
// the whole of what record text may interpolate. What it deliberately leaves
// out is what a release alone cannot answer: the stage being run, the
// workspace listing, and which provider updates are still live.
func (r *Release) Vars() []string {
	env := []string{
		PackageEnvVar + "=" + r.Pkg.Name,
		"DISPAT_SPACE=" + r.Pkg.Space.Name,
		"DISPAT_OLD_VERSION=" + r.Previous().String(),
		"DISPAT_STABLE_BASELINE=" + r.Current.String(),
		NewVersionEnvVar + "=" + r.Next.String(),
		"DISPAT_BUMP=" + r.Bump.String(),
		TagEnvVar + "=" + r.TagName(),
		// Channel state (§11.1). A publish script needs the channel to choose
		// a dist-tag; the old value is there so that a graduation is
		// distinguishable from an ordinary release.
		"DISPAT_CHANNEL=" + r.Channel,
		"DISPAT_OLD_CHANNEL=" + r.BaselineChannel,
		"DISPAT_IS_PRERELEASE=" + boolVar(r.IsPrerelease()),
		// The same release named under the normative "{name}@{version}"
		// format. DISPAT_TAG follows the space's tagFormat, so a script
		// written against the SemVer spelling keeps a stable input whatever
		// local convention that format encodes.
		"DISPAT_SEMVER_TAG=" + r.SemverTagName(),
		// The version decomposed, so a script never re-parses a tag:
		//
		//	DISPAT_VERSION      the core alone: 1.0.1
		//	DISPAT_MAJOR        the core's three numbers on their own: 1, 0
		//	DISPAT_MINOR        and 1. They are the core split, not the
		//	DISPAT_PATCH        version: a prerelease decomposes to the same
		//	                    three as the stable release it is heading for.
		//	DISPAT_CHANNEL      the channel alone (above)
		//	DISPAT_COUNTER      the counter alone (below; unset when stable)
		//	DISPAT_NEW_VERSION  version+channel+counter, SemVer: 1.0.1-beta.4
		//	DISPAT_TAG_VERSION  version+channel+counter as the space's
		//	                    tagFormat spells it — 1.0.1-beta4 under
		//	                    "{name}@v{version}-{channel}{counter}" — the
		//	                    version section of DISPAT_TAG without the name
		//	                    and its decoration
		//
		// The three numbers are what a script writes a moving series tag with
		// — "image:1", "image:1.0" beside "image:1.0.1" — and they are always
		// set: unlike a counter, every version has all three.
		"DISPAT_VERSION=" + r.Next.Core().String(),
		"DISPAT_MAJOR=" + strconv.FormatUint(r.Next.Major, 10),
		"DISPAT_MINOR=" + strconv.FormatUint(r.Next.Minor, 10),
		"DISPAT_PATCH=" + strconv.FormatUint(r.Next.Patch, 10),
		"DISPAT_TAG_VERSION=" + r.TagFormat().RenderVersion(r.Next),
	}
	// The baseline — the newest tag of any kind, prereleases included — is
	// the counterpart of DISPAT_STABLE_BASELINE: what the computed version
	// must exceed and where the channel is read from. It is left unset when
	// the package has never released, so ${DISPAT_BASELINE+x} detects a first
	// release — DISPAT_OLD_VERSION cannot, because it falls back to the
	// stable baseline (initials or 0.0.0) there. When set, the two are equal.
	if r.HasBaseline {
		env = append(env, "DISPAT_BASELINE="+r.Baseline.String())
	}
	// The versioning group, left unset — not empty — when the package versions
	// on its own, by the same convention the counters below keep: an independent
	// package is not a member of a group called "", and ${DISPAT_GROUP+x} is how
	// a script asks whether this release moves anything else with it.
	if group := r.Pkg.VersionGroupName(); group != "" {
		env = append(env, GroupEnvVar+"="+group)
	}
	// The counters are left unset — not empty — when the version has none, so
	// a shell's ${DISPAT_COUNTER+x} distinguishes "a stable release" from "a
	// prerelease whose counter is empty text", which "" cannot.
	if c := r.Counter(); c != "" {
		env = append(env, "DISPAT_COUNTER="+c)
	}
	if c := r.PreviousCounter(); c != "" {
		env = append(env, "DISPAT_OLD_COUNTER="+c)
	}
	return env
}

// OutputVars renders the release's accumulated script outputs: one
// DISPAT_OUTPUT_<NAME> per export with its DISPAT_OUTPUT_SOURCE_<NAME>
// provenance, plus the DISPAT_OUTPUTS listing, set even when empty so a shell
// loop iterates zero times instead of reading an unset variable. GitHubExport
// travels under its full name (no re-prefixing, no listing entry): it is a
// directive to the GitHub recorder, not an ordinary output.
func (r *Release) OutputVars() []string {
	names := make([]string, 0, len(r.Outputs))
	env := make([]string, 0, len(r.Outputs)*2+1)
	for _, o := range r.Outputs {
		if strings.HasPrefix(o.Name, ReservedEnvPrefix) { // GitHubExport
			env = append(env, o.Name+"="+o.Value)
			continue
		}
		names = append(names, o.Name)
		env = append(env, OutputEnvPrefix+o.Name+"="+o.Value)
		if o.Source != "" {
			env = append(env, OutputSourceEnvPrefix+o.Name+"="+o.Source)
		}
	}
	return append(env, "DISPAT_OUTPUTS="+strings.Join(names, " "))
}

func boolVar(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
