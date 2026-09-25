// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

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
