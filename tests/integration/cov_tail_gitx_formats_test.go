// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios: the tag names a configuration would have written.
//
// A release tag is rendered at the very end of a run, after the artefact is
// published, so a format that cannot render a legal ref name — or cannot read
// its own output back — fails at the one moment where failing costs the most:
// the package is out and untagged, which the next run reads as "never
// released". Every shape below is therefore refused at load time, and each
// refusal is checked through the binary, because what an operator gets is the
// exit code and the sentence rather than an error value.

import (
	"testing"

	"github.com/yohimik/dispat/pkg/models"
)

// TestCovTailTagFormatStructureIsRefusedWithTheRuleItBroke: the structural
// rules a release tag format is held to. Each of them exists so a rendered tag
// can be read back into exactly one version: a second {version} makes the
// split ambiguous, a {channel} with no {counter} gives every prerelease of one
// train the same name, a {counter} with no {channel} cannot tell alpha.1 from
// beta.1, and the three placeholders out of order (or with something between
// the last two) render a prerelease section that cannot be dropped for a
// stable release.
func TestCovTailTagFormatStructureIsRefusedWithTheRuleItBroke(t *testing.T) {
	r := refusalRepo(t)
	format := func(f string) func(*models.File) {
		return func(c *models.File) { c.TagFormat = f }
	}
	runRefusals(t, r, []refusal{
		{"two version placeholders", format("{name}@{version}-{version}"),
			"contains more than one {version} placeholder"},
		{"channel without counter", format("{name}@{version}-{channel}"),
			"uses {channel} without {counter}"},
		{"counter without channel", format("{name}@{version}-{counter}"),
			"uses {counter} without {channel}"},
		{"two channel placeholders", format("{name}@{version}-{channel}{channel}.{counter}"),
			"contains more than one {channel} placeholder"},
		{"two counter placeholders", format("{name}@{version}-{channel}.{counter}{counter}"),
			"contains more than one {counter} placeholder"},
		{"prerelease section before the version", format("{name}@{channel}.{counter}-{version}"),
			"expects {version}, then {channel}, then {counter}, in that order"},
		{"a placeholder inside the prerelease section",
			format("{name}@{version}-{channel}{name}{counter}"),
			"places another placeholder between {channel} and {counter}"},
	})
}

// TestCovTailTagFormatIsRefusedWhenGitWouldRefuseTheName: the second half of
// the check, and the one that catches the mistakes that read naturally. A
// leading slash, a leading dash, a leading dot, a ".lock" suffix, a doubled
// separator and a shell-glob character are all things a person writes into a
// tag template without thinking, and all things git-check-ref-format refuses.
// The refusal names the rendered sample, not the template, because the sample
// is what git would have been asked to create.
func TestCovTailTagFormatIsRefusedWhenGitWouldRefuseTheName(t *testing.T) {
	r := refusalRepo(t)
	format := func(f string) func(*models.File) {
		return func(c *models.File) { c.TagFormat = f }
	}
	runRefusals(t, r, []refusal{
		{"leading slash", format("/{name}@{version}"),
			"a ref name may not begin or end with '/'"},
		{"leading dash", format("-{name}@{version}"),
			"a ref name may not begin with '-'"},
		{"leading dot", format(".{name}@{version}"),
			"a ref name may not begin or end with '.'"},
		{"lock suffix", format("{name}@{version}.lock"),
			"a ref name may not end with '.lock'"},
		{"doubled slash", format("{name}//{version}"),
			"a ref name may not contain '//', '..' or '@{'"},
		{"an unknown placeholder left as text", format("{name}@{revision}{version}"),
			"a ref name may not contain '//', '..' or '@{'"},
		{"a character git reserves", format("{name}~{version}"),
			`a ref name may not contain "~"`},
	})
}

// TestCovTailAliasFormatKeepsItsOwnStructuralRules: an alias is only ever
// written, never read back, which is what lets it spell a fragment of the
// version. The rules it keeps are the ones that are about rendering rather
// than parsing — one of each placeholder, a channel and a counter together or
// not at all — plus the same ref-name check, since an alias is created by the
// same `git tag` the release tag is.
func TestCovTailAliasFormatKeepsItsOwnStructuralRules(t *testing.T) {
	r := refusalRepo(t)
	alias := func(f string) func(*models.File) {
		return func(c *models.File) {
			c.AliasTags = []models.AliasTagConfig{{Format: f}}
		}
	}
	runRefusals(t, r, []refusal{
		{"two version placeholders", alias("{name}@v{version}-{version}"),
			"contains more than one {version} placeholder"},
		{"channel without counter", alias("{name}@v{major}-{channel}"),
			"uses {channel} without {counter}"},
		{"counter without channel", alias("{name}@v{major}-{counter}"),
			"uses {counter} without {channel}"},
		{"two channel placeholders", alias("{name}@v{major}-{channel}{channel}.{counter}"),
			"contains more than one {channel} placeholder"},
		{"two counter placeholders", alias("{name}@v{major}-{channel}.{counter}{counter}"),
			"contains more than one {counter} placeholder"},
		{"a name git refuses", alias("/{name}@v{major}"),
			"a ref name may not begin or end with '/'"},
	})
}
