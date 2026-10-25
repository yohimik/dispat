// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// bodyUnder is the record body below the "## <tag> (...)" header in text: a
// changelog file's entry or a preview's output.
func bodyUnder(t *testing.T, text, tag string) string {
	t.Helper()
	start := strings.Index(text, "## "+tag+" (")
	require.NotEqual(t, -1, start, "no header for %s in:\n%s", tag, text)
	rest := text[start:]
	blank := strings.Index(rest, "\n\n")
	require.NotEqual(t, -1, blank, "no body under %s in:\n%s", tag, text)
	return rest[blank+2:]
}

// githubBodyOf is the body the fake GitHub server received for one tag.
func githubBodyOf(t *testing.T, bodies [][]byte, tag string) string {
	t.Helper()
	type ghRelease struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	for _, release := range decodeAll[ghRelease](t, bodies) {
		if release.TagName == tag {
			return release.Body
		}
	}
	require.Failf(t, "no GitHub release", "for %s", tag)
	return ""
}

// previewVersionRepo releases core twice beside its provider utils, which
// releases in core's first run only. Each run's records are kept: the first
// changelog entry as the file held it then, and every GitHub body the fake
// received. With isReleaseCommitted the runs make release commits, and the
// GitHub bodies carry the release section naming them.
func previewVersionRepo(t *testing.T, isReleaseCommitted bool) (r *harness.Repo, firstEntry string, bodies func() [][]byte) {
	t.Helper()
	srv, bodies := githubFake(t)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r = harness.New(t)
	cfg := githubConfig(srv.URL)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "core", Provider: "utils"}}
	if isReleaseCommitted {
		cfg.Commit = &models.CommitConfig{Enabled: models.Bool(true)}
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "utils")
	r.Commit("feat(core): first feature\n\n---\n\nfix(utils): first fix")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.0"), "tags: %v", r.TagList())
	require.True(t, r.IsTagged("utils@0.0.1"), "tags: %v", r.TagList())
	firstEntry = bodyUnder(t, changelogOf(t, r, "core"), "core@0.1.0")
	require.Contains(t, firstEntry, "utils: 0.0.0 -> 0.0.1",
		"the provider released in the same run is part of the record")

	if !isReleaseCommitted {
		r.Commit("chore(release): record the changelog")
	}
	r.WriteFile("packages/core/main.txt", "second\n")
	r.Commit("fix(core): second fix")
	r.ReleaseOK()
	require.True(t, r.IsTagged("core@0.1.1"), "tags: %v", r.TagList())
	return r, firstEntry, bodies
}

// TestPreviewVersionRendersWhatAPastReleaseWrote: after two releases nothing
// is pending, and --version renders the first release's changelog entry and
// GitHub body byte for byte as that release wrote them, with no trace of the
// second. The bare flag renders the latest release. Nothing is written. Both
// with and without release commits, since only a release commit puts the
// release section into the GitHub body.
func TestPreviewVersionRendersWhatAPastReleaseWrote(t *testing.T) {
	for name, isReleaseCommitted := range map[string]bool{"without release commits": false, "with release commits": true} {
		t.Run(name, func(t *testing.T) {
			r, firstEntry, bodies := previewVersionRepo(t, isReleaseCommitted)
			before := r.Git("status", "--porcelain")
			if isReleaseCommitted {
				require.Contains(t, githubBodyOf(t, bodies(), "core@0.1.0"),
					"- commit: "+r.Git("rev-parse", "core@0.1.0^{commit}"))
			}

			changelog := r.Command("preview", "-p", "core", "--version", "0.1.0", "--changelog")
			require.Equal(t, 0, changelog.Code, "stdout:\n%s\nstderr:\n%s", changelog.Stdout, changelog.Stderr)
			assert.Equal(t, firstEntry, bodyUnder(t, changelog.Stdout, "core@0.1.0"))
			assert.NotContains(t, changelog.Stdout, "second fix")

			gh := r.Command("preview", "--version", "core@0.1.0", "--github")
			require.Equal(t, 0, gh.Code, "stdout:\n%s\nstderr:\n%s", gh.Stdout, gh.Stderr)
			assert.Equal(t, githubBodyOf(t, bodies(), "core@0.1.0"), bodyUnder(t, gh.Stdout, "core@0.1.0"),
				"a full tag identifies its package without a selection")

			latest := r.Command("preview", "-p", "core", "--version", "--github")
			require.Equal(t, 0, latest.Code, "stdout:\n%s\nstderr:\n%s", latest.Stdout, latest.Stderr)
			assert.Equal(t, githubBodyOf(t, bodies(), "core@0.1.1"), bodyUnder(t, latest.Stdout, "core@0.1.1"))

			pending := r.Command("preview", "-p", "core")
			require.Equal(t, 0, pending.Code, "stderr:\n%s", pending.Stderr)
			assert.Contains(t, pending.Stdout, "no pending changes for core", "without the flag the preview is unchanged")

			assert.Equal(t, before, r.Git("status", "--porcelain"), "a preview writes nothing")
			assert.Len(t, strings.Split(strings.TrimSpace(r.Git("worktree", "list")), "\n"), 1,
				"the history the plan was read from is gone again")
		})
	}
}

// TestPreviewVersionRefusals: a version the package never released, a version
// beside a selection of several packages, and another package's tag are each
// refused with a sentence naming the problem and no notes.
func TestPreviewVersionRefusals(t *testing.T) {
	r, _, _ := previewVersionRepo(t, false)
	tests := map[string]struct {
		args    []string
		message string
	}{
		"an unknown version": {
			args:    []string{"preview", "-p", "core", "--version", "9.9.9"},
			message: "core has no release 9.9.9",
		},
		"a version beside several packages": {
			args:    []string{"preview", "--version", "0.1.0"},
			message: "--version 0.1.0 needs one package, and the selection covers",
		},
		"latest beside several packages": {
			args:    []string{"preview", "-p", "*", "--version"},
			message: "--version latest needs one package",
		},
		"another package's tag": {
			args:    []string{"preview", "-p", "core", "--version", "utils@0.0.1"},
			message: "utils@0.0.1 is a release of utils, which the selection (core) does not cover",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := r.Command(tc.args...)
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.message)
			assert.NotContains(t, res.Stdout, "## core@", "a refusal renders no notes")
		})
	}
}
