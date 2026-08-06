package harness

import (
	"encoding/json"

	"github.com/stretchr/testify/require"
	models "github.com/yohimik/dispat/pkg/models"
)

// Bool returns a pointer to b, for the enable/disable *bool config fields
// (GitHub.Enabled, Changelog.Enabled, Commit.Enabled).
func Bool(b bool) *bool { return &b }

// BaseFile returns the config every test starts from: the given concurrency
// (one shared value or a [build, publish] pair), JSON logging so the run
// parses into Events, and GitHub disabled so a run never reaches the real
// GitHub API even when GITHUB_TOKEN / GITHUB_REPOSITORY happen to be set in
// the environment (e.g. a CI job running this very suite). Callers fill in
// Scripts, Spaces and Dependencies and write the result with WriteConfigModel;
// a test needing GitHub (the recorder tests) overrides the GitHub field.
func BaseFile(concurrency ...int) models.File {
	return models.File{
		Concurrency: concurrency,
		LogLevel:    "info",
		LogFormat:   "json",
		GitHub:      models.GitHubConfig{Enabled: Bool(false)},
	}
}

// WriteConfigModel marshals cfg — a config.File, or a map[string]any when the
// test needs a shape the model cannot express (an unknown key, a legacy
// schema) — to JSON and writes it as the repository's dispat.json.
func (r *Repo) WriteConfigModel(cfg any) {
	r.T.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(r.T, err)
	r.WriteConfig(string(data))
}
