// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestPreviewGitHubBodyMatchesPublishedLimitForPendingAndPastReleases(t *testing.T) {
	for _, sample := range []struct {
		name string
		line string
	}{
		{name: "ASCII", line: "The walk reads each commit once and reuses its answer.\n"},
		{name: "Unicode", line: "変更後の処理は各コミットを一度だけ読みます。\n"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			srv, bodies := githubFake(t)
			r := harness.New(t)
			cfg := libsConfig(echoBuild, 1)
			cfg.GitHub = &models.GitHubConfig{
				Enabled: models.Bool(true), AllPackages: models.Bool(true),
				Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
				EntryFormatConfig: models.EntryFormatConfig{Footer: []models.EntryLine{{Line: []string{"End of release."}}}},
			}
			r.WriteConfigModel(cfg)
			t.Setenv("DISPAT_IT_TOKEN", "tkn")
			paragraph := strings.Repeat(sample.line, 130_000/(30*utf8.RuneCountInString(sample.line))+1)
			require.Greater(t, utf8.RuneCountInString(paragraph)*30, 125_000)
			for index := range 30 {
				r.WriteFile(filepath.Join("packages", "core", fmt.Sprintf("step%d.txt", index)), "step\n")
				r.Commit(fmt.Sprintf("fix(core): speed up step %d\n\n%s", index, paragraph))
			}

			pending := r.Command("preview", "--github", "--package", "core")
			require.Equal(t, 0, pending.Code, "stdout:\n%s\nstderr:\n%s", pending.Stdout, pending.Stderr)
			pendingBody := bodyUnder(t, pending.Stdout, "core@0.0.1")
			assert.True(t, harness.IsCodePresentForPackage(pending.Events, "W245", "core"))
			assert.LessOrEqual(t, utf8.RuneCountInString(pendingBody), 120_000)
			assert.Contains(t, pendingBody, "End of release.")
			assert.Contains(t, pendingBody, "The release notes continue in")

			r.ReleaseOK()
			publishedBody := githubBodyOf(t, bodies(), "core@0.0.1")
			assert.Equal(t, publishedBody, pendingBody)

			past := r.Command("preview", "--version", "core@0.0.1", "--github")
			require.Equal(t, 0, past.Code, "stdout:\n%s\nstderr:\n%s", past.Stdout, past.Stderr)
			assert.Equal(t, publishedBody, bodyUnder(t, past.Stdout, "core@0.0.1"))
			assert.True(t, harness.IsCodePresentForPackage(past.Events, "W245", "core"))

			changelog := r.Command("preview", "--version", "core@0.0.1", "--changelog")
			require.Equal(t, 0, changelog.Code, "stdout:\n%s\nstderr:\n%s", changelog.Stdout, changelog.Stderr)
			assert.Greater(t, utf8.RuneCountInString(bodyUnder(t, changelog.Stdout, "core@0.0.1")), 120_000,
				"the GitHub cap does not cut changelog entries")
		})
	}
}
