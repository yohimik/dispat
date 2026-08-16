package integration

// Goal 39: a record entry is never empty. Three release shapes are admitted
// to the plan with nothing for their notes to group — an exact pin with no
// pending work, a channel transition carrying none, and pending work its own
// reverts cancel out — and before 1.0.0 each of them rendered a header-only
// changelog entry and an empty GitHub release body. Every shape must instead
// state its cause, in the file and in the API payload alike.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// bodiesRepo is a one-package repository whose GitHub recorder points at the
// given fake, with one stable release already recorded so every scenario
// starts from published history.
func bodiesRepo(t *testing.T, apiURL string) *harness.Repo {
	t.Helper()
	r := harness.New(t)
	r.WriteConfigModel(githubConfig(apiURL))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@0.1.0"), "tags: %v", r.TagList())
	return r
}

// lastBody returns the most recently recorded GitHub release body.
func lastBody(t *testing.T, bodies func() [][]byte) string {
	t.Helper()
	type ghRelease struct {
		Body string `json:"body"`
	}
	all := decodeAll[ghRelease](t, bodies())
	require.NotEmpty(t, all, "a release must have been recorded")
	return all[len(all)-1].Body
}

// TestEntryBodyOfAPinOnlyRelease: an exact Release-As with no pending bump
// releases a version nothing else asked for; the entry says so.
func TestEntryBodyOfAPinOnlyRelease(t *testing.T) {
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r := bodiesRepo(t, srv.URL)

	r.CommitEmpty("release(core): cut it exactly here\n\nRelease-As: 1.0.0")
	r.ReleaseOK()
	require.True(t, r.HasTag("core@1.0.0"), "tags: %v", r.TagList())

	line := "No changes: a version set by Release-As."
	assert.Contains(t, entryOf(t, changelogOf(t, r, "core"), "core@1.0.0"), line)
	assert.Contains(t, lastBody(t, bodies), line, "the GitHub body must not be empty")
}

// TestEntryBodyOfAChannelOnlyRelease: a package moved between lines with no
// new work (W202) still writes records; they name the transition.
func TestEntryBodyOfAChannelOnlyRelease(t *testing.T) {
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r := bodiesRepo(t, srv.URL)

	r.CommitEmpty("release(core)%rc: enter the rc line")
	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W202", "core"),
		"a channel-only release is said out loud: %s", res.Stdout)
	require.True(t, r.HasTag("core@0.1.1-rc.0"),
		"the channel-entry patch applies (W204); tags: %v", r.TagList())

	line := "No changes: a channel transition, stable -> rc."
	assert.Contains(t, entryOf(t, changelogOf(t, r, "core"), "core@0.1.1-rc.0"), line)
	assert.Contains(t, lastBody(t, bodies), line, "the GitHub body must not be empty")
}

// TestEntryBodyOfACancelledOutRelease: a feature and its revert in one window
// release the owed bump (§7.3) with both entries suppressed (W212); the body
// says the work cancelled out instead of rendering empty.
func TestEntryBodyOfACancelledOutRelease(t *testing.T) {
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r := bodiesRepo(t, srv.URL)

	r.WriteFile("packages/core/main.txt", "a bad idea\n")
	r.Commit("feat(core)!: a bad idea")
	bad := r.Git("rev-parse", "HEAD")
	r.WriteFile("packages/core/main.txt", "")
	r.Commit("revert(core): a bad idea\n\nReverts: " + bad)
	res := r.ReleaseOK()
	assert.True(t, harness.HasCodeForPackage(res.Events, "W212", "core"), "out: %s", res.Stdout)
	require.True(t, r.HasTag("core@1.0.0"), "the major is still owed; tags: %v", r.TagList())

	line := "No changes: the pending work and its reverts cancel out."
	entry := entryOf(t, changelogOf(t, r, "core"), "core@1.0.0")
	assert.NotContains(t, entry, "a bad idea")
	assert.Contains(t, entry, line)
	assert.Contains(t, lastBody(t, bodies), line, "the GitHub body must not be empty")
}
