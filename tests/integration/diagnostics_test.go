package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestDiagnosticsDefaultsNeedNeitherGitNorConfig(t *testing.T) {
	r := harness.New(t)
	r.WorkFrom()
	require.NoError(t, os.RemoveAll(r.Path(".git")))
	r.WriteConfig("not valid json")
	res := r.CommandEnv([]string{"PATH=" + t.TempDir()}, "diagnostics", "feat(core): accepted")
	require.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
	data, err := os.ReadFile(r.Path("dispat.json"))
	require.NoError(t, err)
	assert.Equal(t, "not valid json", string(data))
	_, err = os.Stat(r.Path(".git"))
	assert.True(t, os.IsNotExist(err))
}

func TestDiagnosticsExplicitConfigResolvesParserReferences(t *testing.T) {
	r := harness.New(t)
	r.WorkFrom()
	require.NoError(t, os.RemoveAll(r.Path(".git")))
	r.WriteFile("settings/packages/core/placeholder", "")
	r.WriteFile("settings/dispat.yaml", "packages:\n  core:\n    path: packages/core\nparser:\n  $ref: ./parser.yaml\n")
	r.WriteFile("settings/parser.yaml", "types:\n  add: minor\nstrictTypes: true\n")
	for _, tc := range []struct {
		message string
		code    int
	}{
		{"add(core): accepted", 0},
		{"unlisted(core): refused", 1},
	} {
		res := r.CommandEnv([]string{"PATH=" + t.TempDir()}, "diagnostics", "--config", "settings/dispat.yaml", tc.message)
		assert.Equal(t, tc.code, res.Code, "%s%s", res.Stdout, res.Stderr)
		if tc.code != 0 {
			assert.Contains(t, res.Stdout+res.Stderr, "E140")
		}
	}
}

func TestDiagnosticsWarningsRemainVisibleAsJSON(t *testing.T) {
	r := harness.New(t)
	r.WriteFile("packages/core/placeholder", "")
	r.WriteConfig(`{"packages":{"core":{"path":"packages/core"}},"parser":{"quiet":true},"logLevel":"error"}`)
	res := r.Command("diagnostics", "--config", "dispat.json", "--log-format", "json", "--log-level", "error", "--quiet-parser", "unlisted(core): warning")
	require.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
	found := false
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout+res.Stderr), "\n") {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), "%s", line)
		if event["code"] == "W140" {
			found = true
			assert.Equal(t, "warn", event["level"])
			assert.NotNil(t, event["line"])
			assert.NotNil(t, event["column"])
		}
	}
	assert.True(t, found, "expected parser warning: %s%s", res.Stdout, res.Stderr)
}

func TestDiagnosticsChecksLiteralWholeMessage(t *testing.T) {
	r := harness.New(t)
	for _, message := range []string{"", "hello world", "# comment\nfeat(core): no Git cleanup", "feat(core): accepted\n\n---\nnot conventional", "--not-a-flag"} {
		res := r.Command("diagnostics", "--", message)
		assert.Equal(t, 1, res.Code, "message %q: %s%s", message, res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "commit message refused")
	}
	res := r.Command("diagnostics", "feat(core): accepted\n\n---\nfix(api): another unit")
	assert.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
}

func TestDiagnosticsSeparatesUsageAndConfigErrors(t *testing.T) {
	r := harness.New(t)
	r.WriteConfig("{")
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"diagnostics"}, 2},
		{[]string{"diagnostics", "feat: one", "fix: two"}, 2},
		{[]string{"diagnostics", "--tag", "feat: one"}, 2},
		{[]string{"diagnostics", "--config", "missing.json", "feat: one"}, 1},
		{[]string{"diagnostics", "--config", "dispat.json", "feat: one"}, 1},
	} {
		res := r.Command(tc.args...)
		assert.Equal(t, tc.code, res.Code, "%v: %s%s", tc.args, res.Stdout, res.Stderr)
	}
}
