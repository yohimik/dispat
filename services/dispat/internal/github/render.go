// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package github

import (
	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// renderReleaseParts is the release body before the size cap: the entry's
// sections, the release section naming the commit and the tag when the
// commit is known, and the note. The release section sits inside the body,
// before the footer, because it belongs to the release the entry describes
// and a footer is the last word.
func renderReleaseParts(rel *plan.Release, format changelog.Format, look changelog.Lookup, commit, note string) changelog.BodyParts {
	var release string
	if commit != "" {
		release = "### Release\n\n- commit: " + commit + "\n- tag: " + rel.TagName() + "\n"
	}
	return changelog.RenderBodyParts(rel, format, look, release, note)
}

// RenderReleaseBody is the body a release would send for its GitHub release,
// before the size cap Record applies: what preview shows for a release.
func RenderReleaseBody(rel *plan.Release, format changelog.Format, look changelog.Lookup, commit, note string) string {
	return renderReleaseParts(rel, format, look, commit, note).String()
}
