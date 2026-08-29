package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

var fields = Fields{OS: "linux", Arch: "amd64", Version: "1.4.0", Tag: "v1.4.0", Name: "tool"}

func release(names ...string) selfupdate.Release {
	rel := selfupdate.Release{Tag: "v1.4.0"}
	for _, n := range names {
		rel.Assets = append(rel.Assets, selfupdate.Asset{Name: n, URL: "http://example.invalid/" + n})
	}
	return rel
}

// TestExpandRendersWhatDiffersBetweenReleases: a literal asset name stops
// working at the next release, which is the whole reason a pattern exists. All
// five fields render, and text around them survives untouched.
func TestExpandRendersWhatDiffersBetweenReleases(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"nothing to expand":  {"tool-linux-amd64", "tool-linux-amd64"},
		"the platform":       {"tool-{os}-{arch}", "tool-linux-amd64"},
		"the version":        {"tool_{version}_{os}_{arch}.tar.gz", "tool_1.4.0_linux_amd64.tar.gz"},
		"the tag":            {"{tag}/{name}", "v1.4.0/tool"},
		"the same one twice": {"{os}-{os}", "linux-linux"},
		"beside a glob":      {"*{arch}*", "*amd64*"},
		"an empty pattern":   {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Expand(tc.in, fields)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestExpandRefusesAPlaceholderNobodyDefines: leaving an unknown one in place
// would put it in a filename the release never carried, and the failure would
// then read as a missing asset rather than as the typo it is.
func TestExpandRefusesAPlaceholderNobodyDefines(t *testing.T) {
	_, err := Expand("tool-{arch64}", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{arch64}")
	assert.Contains(t, err.Error(), "{arch}", "and it says what it does know")
	assert.Contains(t, err.Error(), "{version}")

	_, err = Expand("tool-{os", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never closed")
}

// TestSelectAssetNeedsNoPatternForAnUnambiguousRelease: a release carrying one
// file has one answer, and asking for a pattern to state the obvious would be
// ceremony.
func TestSelectAssetNeedsNoPatternForAnUnambiguousRelease(t *testing.T) {
	got, err := SelectAsset(release("tool"), "", fields)
	require.NoError(t, err)
	assert.Equal(t, "tool", got.Name)
}

// TestSelectAssetRefusesToGuess: which of nine files is the binary is exactly
// the question that must not be answered by inference, because the wrong
// answer is installed globally and run. The refusal lists them, so the next
// invocation can name one.
func TestSelectAssetRefusesToGuess(t *testing.T) {
	rel := release("tool-linux-amd64", "tool-darwin-arm64", "checksums.txt")
	_, err := SelectAsset(rel, "", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--asset")
	for _, name := range rel.AssetNames() {
		assert.Contains(t, err.Error(), name, "the refusal names what is there")
	}

	_, err = SelectAsset(selfupdate.Release{Tag: "v1.4.0"}, "", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carries no files")
}

// TestSelectAssetMatchesByNameThenByGlob: an exact name wins outright, so an
// asset whose name happens to be a prefix of another is still reachable; a
// glob is what reaches one nobody wants to type in full.
func TestSelectAssetMatchesByNameThenByGlob(t *testing.T) {
	rel := release("tool", "tool-linux-amd64", "tool-linux-amd64.sha256")

	got, err := SelectAsset(rel, "tool", fields)
	require.NoError(t, err)
	assert.Equal(t, "tool", got.Name, "an exact name is never a glob's runner-up")

	got, err = SelectAsset(rel, "tool-{os}-{arch}", fields)
	require.NoError(t, err)
	assert.Equal(t, "tool-linux-amd64", got.Name)

	got, err = SelectAsset(rel, "*.sha256", fields)
	require.NoError(t, err)
	assert.Equal(t, "tool-linux-amd64.sha256", got.Name)
}

// TestSelectAssetRefusesAnAmbiguousGlob: a pattern reaching two files is a
// pattern that has not chosen, and installing either of them would be a coin
// toss the reader never asked for.
func TestSelectAssetRefusesAnAmbiguousGlob(t *testing.T) {
	rel := release("tool-linux-amd64", "tool-linux-amd64.sha256")
	_, err := SelectAsset(rel, "tool-linux-*", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2")
	assert.Contains(t, err.Error(), "tool-linux-amd64.sha256")
}

// TestSelectAssetReportsAPlatformTheReleaseSkipped: a release cut before a
// platform joined the build matrix has nothing for it, and the refusal owes
// the reader the name it wanted and the names it found.
func TestSelectAssetReportsAPlatformTheReleaseSkipped(t *testing.T) {
	_, err := SelectAsset(release("tool-darwin-arm64"), "tool-{os}-{arch}", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool-linux-amd64", "the one it wanted")
	assert.Contains(t, err.Error(), "tool-darwin-arm64", "and the one there is")

	_, err = SelectAsset(release("tool"), "tool-{nonsense}", fields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{nonsense}", "a bad pattern is reported as one, not as a missing file")
}
