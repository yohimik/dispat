// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

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
