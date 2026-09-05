package plan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// varsMap indexes rendered NAME=value pairs, so a test asks about one
// variable without depending on where in the slice it sits.
func varsMap(t *testing.T, pairs []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		name, value, ok := strings.Cut(p, "=")
		require.True(t, ok, "not a NAME=value pair: %q", p)
		out[name] = value
	}
	return out
}

func TestReleaseVars(t *testing.T) {
	rel := &Release{
		Pkg:             &model.Package{Name: "core", Space: &model.Space{Name: "libs"}},
		Next:            v(1, 3, 0),
		Current:         v(1, 2, 0),
		Bump:            ccme.BumpMinor,
		Channel:         "stable",
		BaselineChannel: "stable",
	}
	vars := varsMap(t, rel.Vars())

	assert.Equal(t, "core", vars[PackageEnvVar])
	assert.Equal(t, "libs", vars["DISPAT_SPACE"])
	assert.Equal(t, "1.3.0", vars["DISPAT_NEW_VERSION"])
	assert.Equal(t, "1.3.0", vars["DISPAT_VERSION"])
	assert.Equal(t, "1.2.0", vars["DISPAT_STABLE_BASELINE"])
	assert.Equal(t, "core@1.3.0", vars["DISPAT_TAG"])
	assert.Equal(t, "core@1.3.0", vars["DISPAT_SEMVER_TAG"])
	assert.Equal(t, "1.3.0", vars["DISPAT_TAG_VERSION"])
	assert.Equal(t, "stable", vars["DISPAT_CHANNEL"])
	assert.Equal(t, "false", vars["DISPAT_IS_PRERELEASE"])
	assert.Equal(t, "minor", vars["DISPAT_BUMP"])
	assert.Equal(t, "1", vars["DISPAT_MAJOR"])
	assert.Equal(t, "3", vars["DISPAT_MINOR"])
	assert.Equal(t, "0", vars["DISPAT_PATCH"], "zero is a value, not an absence")

	// Unset, not empty: a shell tells "never released" from "released 0.0.0"
	// by whether the variable exists at all.
	assert.NotContains(t, vars, "DISPAT_BASELINE", "a package that never released has no baseline")
	assert.NotContains(t, vars, "DISPAT_COUNTER", "a stable version has no counter")
	assert.NotContains(t, vars, "DISPAT_OLD_COUNTER")
	assert.NotContains(t, vars, GroupEnvVar, "an independently versioned package is in no group")
}

// TestReleaseVarsCarriesTheVersioningGroup: DISPAT_GROUP names the group whose
// versions move together, so a script can tell which other packages this
// release takes with it — something neither the package name nor the space name
// can answer, since a group may span spaces and a space may version
// independently.
func TestReleaseVarsCarriesTheVersioningGroup(t *testing.T) {
	release := func(space *model.Space) *Release {
		return &Release{
			Pkg:     &model.Package{Name: "core", Space: space},
			Next:    v(1, 0, 0),
			Current: v(1, 0, 0),
			Channel: "stable", BaselineChannel: "stable",
		}
	}

	shared := varsMap(t, release(&model.Space{Name: "libs", Versioning: model.VersioningFixed}).Vars())
	assert.Equal(t, "libs", shared[GroupEnvVar],
		"a space that versions as a group declares nothing, so its members carry its name")

	declared := varsMap(t, release(&model.Space{
		Name: "libs", Versioning: model.VersioningFixed, VersionGroup: "gang"}).Vars())
	assert.Equal(t, "gang", declared[GroupEnvVar], "a declared group outranks the space's own name")

	// Unset rather than empty, by the same rule the counters keep: an
	// independent package is not a member of a group called "", and
	// ${DISPAT_GROUP+x} is what tells the two apart.
	independent := varsMap(t, release(&model.Space{Name: "libs"}).Vars())
	assert.NotContains(t, independent, GroupEnvVar)
}

func TestReleaseVarsOnAPrereleaseTrain(t *testing.T) {
	rel := &Release{
		Pkg:             &model.Package{Name: "core", Space: &model.Space{Name: "libs"}},
		Next:            ccme.Version{Major: 1, Minor: 3, Prerelease: []string{"beta", "4"}},
		Baseline:        ccme.Version{Major: 1, Minor: 3, Prerelease: []string{"beta", "3"}},
		HasBaseline:     true,
		Current:         v(1, 2, 0),
		Channel:         "beta",
		BaselineChannel: "beta",
	}
	vars := varsMap(t, rel.Vars())

	assert.Equal(t, "true", vars["DISPAT_IS_PRERELEASE"])
	assert.Equal(t, "1.3.0-beta.3", vars["DISPAT_BASELINE"])
	assert.Equal(t, "4", vars["DISPAT_COUNTER"])
	assert.Equal(t, "3", vars["DISPAT_OLD_COUNTER"])
	assert.Equal(t, "1.3.0", vars["DISPAT_VERSION"], "the core alone, without the prerelease")
	// The three numbers split DISPAT_VERSION, so a prerelease decomposes to the
	// stable release it is heading for rather than to anything of its own.
	assert.Equal(t, "1", vars["DISPAT_MAJOR"])
	assert.Equal(t, "3", vars["DISPAT_MINOR"])
	assert.Equal(t, "0", vars["DISPAT_PATCH"])
}

func TestReleaseVarsAreIndependentSlices(t *testing.T) {
	rel := &Release{Pkg: &model.Package{Name: "core", Space: &model.Space{Name: "libs"}}}
	first := rel.Vars()
	appended := append(rel.Vars(), "EXTRA=1")

	assert.NotContains(t, first, "EXTRA=1", "appending to one rendering must not reach another")
	assert.Contains(t, appended, "EXTRA=1")
}

func TestReleaseOutputVars(t *testing.T) {
	rel := &Release{Outputs: []Output{
		{Name: "IMAGE", Value: "acme/core:1.3.0", Source: "core:build"},
		{Name: "DIGEST", Value: "sha256:abc"},
		{Name: GitHubExport, Value: "/dist/app.tgz", Source: "core:build"},
	}}
	vars := varsMap(t, rel.OutputVars())

	assert.Equal(t, "acme/core:1.3.0", vars["DISPAT_OUTPUT_IMAGE"])
	assert.Equal(t, "core:build", vars["DISPAT_OUTPUT_SOURCE_IMAGE"])
	assert.Equal(t, "sha256:abc", vars["DISPAT_OUTPUT_DIGEST"])
	assert.NotContains(t, vars, "DISPAT_OUTPUT_SOURCE_DIGEST", "no source, no provenance variable")
	assert.Equal(t, "/dist/app.tgz", vars[GitHubExport], "the export travels under its full name")
	assert.Equal(t, "IMAGE DIGEST", vars["DISPAT_OUTPUTS"], "and stays out of the listing")
}

func TestReleaseOutputVarsWithoutOutputs(t *testing.T) {
	vars := varsMap(t, (&Release{}).OutputVars())
	require.Contains(t, vars, "DISPAT_OUTPUTS", "set even when empty, so a shell loop iterates zero times")
	assert.Empty(t, vars["DISPAT_OUTPUTS"])
}
