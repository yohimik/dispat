// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestLinkedGroupsMergeEverySharedModeFromEitherEntry proves version policies
// combine across identities, not just packages discovered in one repository.
func TestLinkedGroupsMergeEverySharedModeFromEitherEntry(t *testing.T) {
	for _, mode := range []string{"fixed", "fixedSparse", "fixedMajorMinor", "fixedMajorMinorSparse", "fixedMajor", "fixedMajorSparse"} {
		t.Run(mode, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			for _, name := range fleet.names {
				fleet.writeConfig(name, func(cfg *models.File) {
					cfg.VersionGroups = map[string]models.VersionGroupConfig{"Train": {Versioning: mode}}
					space := cfg.Spaces[name]
					space.VersionGroup = "TRAIN"
					cfg.Spaces[name] = space
				})
				fleet.peer(name).Commit("chore: join the fleet train")
				fleet.push(name)
			}
			fleet.peer("sdk").CommitEmpty("feat(sdk-pkg)!: change the shared contract")
			fleet.push("sdk")
			fleet.link("api", "sdk")
			for _, entry := range fleet.names {
				result := fleet.enter(entry).StatusOK("--package", "*")
				for _, pkg := range []string{"api-pkg", "sdk-pkg"} {
					assert.Equal(t, "major", harness.GraphLine(result.Events, pkg).Str("bump"), "%s through %s", pkg, entry)
				}
			}
		})
	}
}

// TestLinkedGroupsResolvePeerDeclarationsAndKeepOwnerSettings releases a group
// declared by only one peer; scripts with the same name and environment key
// still use each owner's values and package directory.
func TestLinkedGroupsResolvePeerDeclarationsAndKeepOwnerSettings(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			if name == "sdk" {
				cfg.VersionGroups = map[string]models.VersionGroupConfig{"Train": {Versioning: "fixed"}}
			}
			space := cfg.Spaces[name]
			space.VersionGroup = "train"
			cfg.Spaces[name] = space
			cfg.Env = map[string]string{"OWNER": name}
			cfg.Scripts["publish"] = models.Script{"printf '%s' \"$OWNER\" > owner.txt"}
		})
		fleet.peer(name).Commit("chore: join the peer's train")
		fleet.push(name)
	}
	fleet.peer("sdk").CommitEmpty("feat(sdk-pkg)!: change contract")
	fleet.push("sdk")
	fleet.link("api", "sdk")
	entry := fleet.enter("api")
	entry.ReleaseOK("--package", "*")
	assert.Equal(t, []string{"api-pkg@1.0.0"}, entry.TagList())
	assert.Equal(t, []string{"sdk-pkg@1.0.0"}, tagsIn(entry, ".links/sdk"))
	for _, tc := range []struct{ path, owner string }{{"packages/api-pkg/owner.txt", "api"}, {".links/sdk/packages/sdk-pkg/owner.txt", "sdk"}} {
		data, err := os.ReadFile(entry.Path(tc.path))
		require.NoError(t, err)
		assert.Equal(t, tc.owner, string(data))
	}
	settled := entry.StatusOK("--package", "*")
	for _, pkg := range []string{"api-pkg", "sdk-pkg"} {
		assert.Empty(t, harness.GraphLine(settled.Events, pkg).Str("bump"))
	}
}

// TestLinkedGroupsResolveImplicitFolderPolicies verifies that folder overrides
// define the implicit group, and peers can reference that effective rule.
func TestLinkedGroupsResolveImplicitFolderPolicies(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("api", func(cfg *models.File) {
		space := cfg.Spaces["api"]
		space.VersionGroup = "SDK"
		cfg.Spaces["api"] = space
	})
	fleet.peer("api").Commit("chore: join the sdk space group")
	fleet.push("api")
	fleet.peer("sdk").WriteFile("packages/dispat.json", `{"versioning":"fixed"}`)
	fleet.peer("sdk").Commit("feat(sdk-pkg)!: define folder-level shared policy")
	fleet.push("sdk")
	fleet.link("api", "sdk")
	result := fleet.peer("api").StatusOK("--package", "*")
	assert.Equal(t, "major", harness.GraphLine(result.Events, "api-pkg").Str("bump"))
}

// TestLinkedGroupsRefuseConflictingAxes checks all three semantic axes; entry
// order cannot choose a winner or permit release side effects.
func TestLinkedGroupsRefuseConflictingAxes(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule models.VersionGroupConfig
	}{
		{"semver", models.VersionGroupConfig{Versioning: "fixedMajor"}},
		{"counter", models.VersionGroupConfig{Versioning: "fixed", Counter: "independent"}},
		{"channels", models.VersionGroupConfig{Versioning: "fixed", Counter: "independent", Channels: "independent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			for _, name := range fleet.names {
				fleet.writeConfig(name, func(cfg *models.File) {
					rule := models.VersionGroupConfig{Versioning: "fixed"}
					if tc.name == "channels" {
						rule.Counter = "independent"
					}
					if name == "sdk" {
						rule = tc.rule
					}
					key := "Train"
					if name == "sdk" {
						key = "TRAIN"
					}
					cfg.VersionGroups = map[string]models.VersionGroupConfig{key: rule}
					space := cfg.Spaces[name]
					space.VersionGroup = "train"
					cfg.Spaces[name] = space
				})
				fleet.peer(name).Commit("chore: configure train policy")
				fleet.push(name)
			}
			fleet.link("api", "sdk")
			for _, name := range fleet.names {
				entry := fleet.enter(name)
				result := entry.Release("--package", "*")
				require.NotZero(t, result.Code)
				requireDiagnostic(t, result, "E332")
				assert.Contains(t, result.Stdout+result.Stderr, "conflicting policies")
				assert.Empty(t, entry.TagList())
			}
		})
	}
}

// TestLinkedGroupsExcludeDisabledDeclarations proves a disabled peer neither
// supplies a group nor vetoes the remaining fleet's policy.
func TestLinkedGroupsExcludeDisabledDeclarations(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			mode := "fixed"
			if name == "sdk" {
				mode = "fixedMajor"
			}
			cfg.VersionGroups = map[string]models.VersionGroupConfig{"train": {Versioning: mode}}
			space := cfg.Spaces[name]
			space.VersionGroup = "train"
			cfg.Spaces[name] = space
			if name == "api" {
				cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{"sdk": {Enabled: models.Bool(false)}}
			}
		})
		fleet.peer(name).Commit("chore: configure participation")
		fleet.push(name)
	}
	fleet.link("api", "sdk")
	result := fleet.peer("api").StatusOK("--package", "*")
	assert.Equal(t, []string{"api-pkg"}, plannedPackages(result))
	fleet.writeConfig("api", func(cfg *models.File) {
		cfg.RepositoryOverrides = map[string]models.RepositoryOverrideConfig{"sdk": {Enabled: models.Bool(false)}}
		space := cfg.Spaces["api"]
		space.VersionGroup = "train"
		cfg.Spaces["api"] = space
	})
	refused := fleet.peer("api").Status("--package", "*")
	assert.NotZero(t, refused.Code)
	assert.Contains(t, refused.Stdout+refused.Stderr, "matches no versionGroups")
}

// TestLinkedGroupsKeepIndependentChannelsAcrossPeerReleases verifies shared
// prefixes and independent graduation remain compatible across repositories.
func TestLinkedGroupsKeepIndependentChannelsAcrossPeerReleases(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			cfg.VersionGroups = map[string]models.VersionGroupConfig{"Train": {Versioning: "fixedMajorMinor", Counter: "independent", Channels: "independent"}}
			space := cfg.Spaces[name]
			space.VersionGroup = "train"
			cfg.Spaces[name] = space
		})
		fleet.peer(name).Commit("chore: share the prefix with separate channels")
		fleet.peer(name).CommitEmpty("feat(" + name + "-pkg)%beta: start the train")
		fleet.push(name)
	}
	fleet.link("api", "sdk")
	entry := fleet.enter("api")
	entry.ReleaseOK("--package", "*")
	assert.Contains(t, entry.TagList(), "api-pkg@0.1.0-beta.0")
	assert.Contains(t, tagsIn(entry, ".links/sdk"), "sdk-pkg@0.1.0-beta.0")
	entry.CommitEmpty("release(api-pkg)%beta>stable: graduate this peer")
	entry.ReleaseOK("--package", "*")
	assert.Contains(t, entry.TagList(), "api-pkg@0.1.0")
	assert.NotContains(t, tagsIn(entry, ".links/sdk"), "sdk-pkg@0.1.0")
	fleet.workIn(entry, "sdk", "sdk-pkg", "feat(sdk-pkg): move the shared prefix")
	entry.ReleaseOK("--package", "*")
	assert.Contains(t, entry.TagList(), "api-pkg@0.2.0")
	assert.Contains(t, tagsIn(entry, ".links/sdk"), "sdk-pkg@0.2.0-beta.0")
}

// TestLinkedGroupsMergeImplicitNamesAndPreserveMemberOverrides checks the
// group's deepest-member convergence without mixing owner configuration.
func TestLinkedGroupsMergeImplicitNamesAndPreserveMemberOverrides(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	for _, name := range fleet.names {
		fleet.writeConfig(name, func(cfg *models.File) {
			group := "Libraries"
			if name == "sdk" {
				group = "LIBRARIES"
			}
			cfg.Versioning = "fixedMajor"
			cfg.Spaces = map[string]models.SpaceConfig{group: {Path: models.PathList{"packages"}}}
			if name == "sdk" {
				cfg.Packages = map[string]models.PackageConfig{"sdk-pkg": {Versioning: "fixed"}}
			}
		})
		fleet.peer(name).Commit("chore: configure shared spaces")
		fleet.push(name)
	}
	fleet.link("api", "sdk")
	entry := fleet.enter("api")
	first := entry.ReleaseOK("--package", "*")
	assert.True(t, harness.IsCodePresent(first.Events, "W237"))
	fleet.workIn(entry, "sdk", "sdk-pkg", "fix(sdk-pkg): advance the deeper shared policy")
	entry.ReleaseOK("--package", "*")
	assert.Contains(t, entry.TagList(), "api-pkg@0.1.1")
	assert.Contains(t, tagsIn(entry, ".links/sdk"), "sdk-pkg@0.1.1")
}

// TestLinkedGroupsKeepIndependentSpacesOutsideTheNamespace checks an unrelated
// owner-local space does not join or veto another peer's explicit group.
func TestLinkedGroupsKeepIndependentSpacesOutsideTheNamespace(t *testing.T) {
	fleet := newChoreographyFleet(t, "api", "sdk")
	fleet.writeConfig("sdk", func(cfg *models.File) {
		cfg.VersionGroups = map[string]models.VersionGroupConfig{"API": {Versioning: "fixed"}}
	})
	fleet.peer("sdk").Commit("chore: declare an ambiguous name")
	fleet.push("sdk")
	fleet.link("api", "sdk")
	result := fleet.peer("api").StatusOK("--package", "*")
	assert.Equal(t, "minor", harness.GraphLine(result.Events, "api-pkg").Str("bump"))
}

// TestLinkedGroupsUseTheReferenceUnicodeEquivalence proves EqualFold-equivalent
// names cannot select a policy by map order or split one release group.
func TestLinkedGroupsUseTheReferenceUnicodeEquivalence(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			fleet := newChoreographyFleet(t, "api", "sdk")
			for _, name := range fleet.names {
				fleet.writeConfig(name, func(cfg *models.File) {
					key, mode := "Σ", "fixed"
					if name == "sdk" {
						key = "ς"
						if conflict {
							mode = "fixedMajor"
						}
					}
					cfg.VersionGroups = map[string]models.VersionGroupConfig{key: {Versioning: mode}}
					space := cfg.Spaces[name]
					space.VersionGroup = key
					cfg.Spaces[name] = space
				})
				fleet.peer(name).Commit("chore: configure Unicode group")
				fleet.push(name)
			}
			fleet.peer("sdk").CommitEmpty("feat(sdk-pkg)!: move the shared major")
			fleet.push("sdk")
			fleet.link("api", "sdk")
			for _, name := range fleet.names {
				entry := fleet.enter(name)
				result := entry.Status("--package", "*")
				if conflict {
					require.NotZero(t, result.Code)
					requireDiagnostic(t, result, "E332")
					assert.Contains(t, result.Stdout+result.Stderr, "conflicting policies")
				} else {
					require.Zero(t, result.Code, "%s\n%s", result.Stdout, result.Stderr)
					assert.Equal(t, "major", harness.GraphLine(result.Events, "api-pkg").Str("bump"))
					for _, spelling := range []string{"Σ", "σ", "ς"} {
						selected := entry.Status("--group", spelling)
						require.Zero(t, selected.Code, "group %q: %s\n%s", spelling, selected.Stdout, selected.Stderr)
						assert.Equal(t, "major", harness.GraphLine(selected.Events, "api-pkg").Str("bump"))
						loop := entry.Command("for", "-g", spelling, "--do", `echo "GROUP:$DISPAT_GROUP"`)
						require.Zero(t, loop.Code, "group %q: %s\n%s", spelling, loop.Stdout, loop.Stderr)
						assert.Equal(t, 1, strings.Count(loop.Stdout, "GROUP:"))
					}
				}
			}
		})
	}
}
