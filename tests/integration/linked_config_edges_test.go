// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Failure boundaries where repository-owned configuration meets the Git links
// that compose a choreographed fleet. These scenarios stay at the CLI boundary:
// a failed composition must produce a diagnostic and no release plan.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
)

// TestLinkedConfigDisabledPeerNeedsNoReadableCheckout proves exclusion happens
// before the walk inspects a peer. This permits an entry checkout to release
// while a deliberately disabled link has not been initialized in CI.
func TestLinkedConfigDisabledPeerNeedsNoReadableCheckout(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"sdk": {Enabled: models.Bool(false)},
		}
	})
	api.Git("submodule", "deinit", "-f", "--", ".links/sdk")

	res := api.StatusOK("--package", "*")
	assert.Equal(t, []string{"api"}, composedRepositories(res))
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(res))
	requireNoDiagnostic(t, res, "E330")
	requireNoDiagnostic(t, res, "E339")
}

// TestLinkedConfigRejectsControlPolicyOwnedByAPeer proves every configuration
// reached through a link is validated as repository-owned policy. A forbidden
// central commit override cannot hide in a peer that the entry config trusts.
func TestLinkedConfigRejectsControlPolicyOwnedByAPeer(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{
			"api": {Commit: &models.CommitConfig{Enabled: models.Bool(false)}},
		}
	})
	fleet.peer("sdk").Commit("chore: put central policy in a peer")
	fleet.push("sdk")
	fleet.refresh("api", "sdk")

	res := fleet.peer("api").Status("--package", "*")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E332")
	assert.Contains(t, res.Stdout+res.Stderr, "commit policy belongs to repository")
	assert.Empty(t, plannedPackages(res))
}

// TestLinkedConfigRejectsTwoIdentitiesAtOneGitlinkPath proves a roster cannot
// make one checkout stand for two peers. The second identity is checked against
// the configuration actually found at that path before any package is planned.
func TestLinkedConfigRejectsTwoIdentitiesAtOneGitlinkPath(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk", "web")
	fleet.link("api", "sdk")
	api := fleet.peer("api")
	api.WriteFile(".gitmodules", "[submodule \"sdk\"]\n\tpath = .links/sdk\n\turl = "+fleet.peer("sdk").remote+
		"\n[submodule \"web\"]\n\tpath = .links/sdk\n\turl = "+fleet.peer("web").remote+"\n")

	res := api.Status("--package", "*")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E339")
	assert.Contains(t, diagnosticText(res), "calls itself")
	assert.Contains(t, diagnosticText(res), "links it as")
	assert.Contains(t, diagnosticText(res), "sdk")
	assert.Contains(t, diagnosticText(res), "web")
	assert.Empty(t, plannedPackages(res))
}

// TestLinkedConfigRejectsDuplicatePackageOwnershipAcrossPeers proves linked
// repositories keep distinct filesystem scopes without allowing two owners to
// claim the same package identity in the fleet-wide dependency graph.
func TestLinkedConfigRejectsDuplicatePackageOwnershipAcrossPeers(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.Spaces = nil
		cfg.Packages = map[string]models.PackageConfig{
			"api-pkg": {Path: "packages/sdk-pkg"},
		}
	})
	fleet.peer("sdk").Commit("chore: claim the entry package identity")
	fleet.push("sdk")
	fleet.refresh("api", "sdk")

	res := fleet.peer("api").Status("--package", "*")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E332")
	assert.Contains(t, diagnosticText(res), `duplicate package name "api-pkg"`)
	assert.Contains(t, diagnosticText(res), `repositories "api" and "sdk"`)
	assert.Empty(t, plannedPackages(res))
}

// TestLinkedConfigCannotDeclareAPackageInsideItsPeer proves configuration
// ownership follows the deepest Git root. An entry cannot adopt files from a
// linked checkout merely by naming their path in its own package map.
func TestLinkedConfigCannotDeclareAPackageInsideItsPeer(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.link("api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.Spaces = nil
		cfg.Packages = map[string]models.PackageConfig{
			"borrowed-sdk": {Path: ".links/sdk/packages/sdk-pkg"},
		}
	})

	res := fleet.peer("api").Status("--package", "*")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	requireDiagnostic(t, res, "E331")
	assert.Contains(t, diagnosticText(res), "borrowed-sdk")
	assert.Contains(t, diagnosticText(res), "repository")
	assert.Empty(t, plannedPackages(res))
}
