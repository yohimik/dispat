// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// The commands other than release, against a choreographed fleet: what they
// see, and what they hand down to the commands a script starts.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestChoreographyRunSweepsEveryRepository: a script sweep is over the whole
// fleet, and each package runs in its own repository's folder.
func TestChoreographyRunSweepsEveryRepository(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["tests"] = models.Script{"printf '%s %s\\n' \"$DISPAT_PACKAGE\" \"$(pwd)\""}
	})
	api.Commit("chore: add a test script")
	fleet.configureIn(api.Repo, "sdk", "chore: add a test script", func(cfg *models.File) {
		cfg.Scripts["tests"] = models.Script{"printf '%s %s\\n' \"$DISPAT_PACKAGE\" \"$(pwd)\""}
	})

	res := api.RunScriptOK("tests", "--since", "all")
	assert.Contains(t, res.Stdout, "api-pkg")
	assert.Contains(t, res.Stdout, "sdk-pkg")
	assert.Contains(t, res.Stdout, ".links/sdk/packages/sdk-pkg",
		"the provider's script ran in the provider's own folder")
}

// TestChoreographyPreviewReadsTheWholeFleet: preview renders notes for a
// package in another repository, which means it composed the fleet too.
func TestChoreographyPreviewReadsTheWholeFleet(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	fleet.workIn(api.Repo, "sdk", "sdk-pkg", "feat(sdk-pkg): a note worth previewing")

	res := api.Command("preview", "--package", "sdk-pkg", "--changelog")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "a note worth previewing")
}

// TestChoreographyNestedCommandComposesTheSameFleet: a command a package
// script starts inherits the entry, the saga and the repositories, so it sees
// the fleet the release around it is holding.
func TestChoreographyNestedCommandComposesTheSameFleet(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")
	marker := api.Path("nested.json")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Dependencies = models.Dependencies{{Consumer: "api-pkg", Provider: "sdk-pkg"}}
		cfg.Scripts["publish"] = models.Script{
			// The glob is quoted: this is a shell, and an unquoted * would be
			// the publish folder's file names rather than every package.
			api.DispatCommand("status", "--package", "'*'", "--log-format", "json") +
				" > " + shellQuote(marker)}
	})
	api.Commit("chore: run a nested status while publishing")

	api.ReleaseOK("--package", "*")
	nested := harness.ParseEvents(readAbs(t, marker))
	var composed []string
	for _, event := range nested {
		if event.Str("message") == "polyrepo workspace composed" {
			assert.Empty(t, event.Str("saga"))
			assert.Equal(t, "api", event.Str("entry"))
			for _, name := range event["repositories"].([]any) {
				composed = append(composed, name.(string))
			}
		}
	}
	assert.ElementsMatch(t, []string{"api", "sdk"}, composed,
		"the nested command composed the fleet the release is holding")
}

// TestChoreographyDiagnosticsReadsAConfigWithoutAFleet: validating a message
// is a parser question, and it must not need a fleet to answer it.
func TestChoreographyDiagnosticsReadsAConfigWithoutAFleet(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")

	res := api.Command("diagnostics", "--config", "dispat.json", "fix(api-pkg): a message")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	bad := api.Command("diagnostics", "--config", "dispat.json", "not a conventional commit")
	assert.Equal(t, 1, bad.Code)
}

// TestChoreographyStatusFromInsideALinkedCheckout: a run started in the copy
// of a peer that lives inside another repository cannot compose the fleet —
// its own links were never materialized — and says which command repairs it.
func TestChoreographyStatusFromInsideALinkedCheckout(t *testing.T) {
	fleet := crossRepositoryFleet(t)
	api := fleet.peer("api")

	res := api.CommandAt(".links/sdk", "status", "--package", "*")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	requireDiagnostic(t, res, "E330")
	assert.True(t, strings.Contains(res.Stdout+res.Stderr, "git submodule update --init"))
}
