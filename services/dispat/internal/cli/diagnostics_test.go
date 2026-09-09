package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runDiagnosticsCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv(updateCheckEnv, "0")
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeDiagnosticsConfig(t *testing.T, root, name, parser string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
	body := `{"packages":{"core":{"path":"pkgs/core"}},"parser":` + parser + `}`
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o600))
}

func TestDiagnosticsUsesDefaultsWithoutDiscoveringConfig(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsConfig(t, root, "dispat.json", `{"strictTypes":true}`)

	code, stdout, stderr := runDiagnosticsCLI(t,
		"diagnostics", "unknown(core): accepted with a warning", "--root", root)

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "W140")
	assert.Empty(t, stderr)
}

func TestDiagnosticsLoadsOnlyAnExplicitConfigRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsConfig(t, root, "strict.json", `{"strictTypes":true}`)

	code, stdout, stderr := runDiagnosticsCLI(t,
		"diagnostics", "unknown(core): rejected", "--root", root, "--config", "strict.json")

	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "E140")
	assert.Contains(t, stdout, "commit message refused")
	assert.Empty(t, stderr)
}

func TestDiagnosticsExplicitConfigResolvesRefs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "parser.json"),
		[]byte(`{"strictTypes":true}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"packages":{"core":{"path":"pkgs/core"}},"parser":{"$ref":"parser.json"}}`), 0o600))

	code, stdout, stderr := runDiagnosticsCLI(t,
		"diagnostics", "unknown(core): rejected", "--root", root, "--config", "dispat.json")

	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "E140")
	assert.Empty(t, stderr)
}

func TestDiagnosticsPreservesEmptyAndMultilineMessages(t *testing.T) {
	code, stdout, stderr := runDiagnosticsCLI(t, "diagnostics", "")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "E002")
	assert.Empty(t, stderr)

	code, stdout, stderr = runDiagnosticsCLI(t,
		"diagnostics", "feat(core): first\n---\nfix(core): second", "--log-format", "json")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout, "a valid message has no warnings or errors at warn level")
	assert.Empty(t, stderr)
}

func TestDiagnosticsAlwaysShowsRequestedWarnings(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsConfig(t, root, "quiet.json", `{"quiet":true,"maxDescriptionLength":10}`)

	code, stdout, stderr := runDiagnosticsCLI(t,
		"diagnostics", "feat(core): description longer than ten", "--root", root,
		"--config", "quiet.json", "--log-level", "error", "--log-format", "json")

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	line := strings.TrimSpace(stdout)
	var event map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &event))
	assert.Equal(t, "warn", event["level"])
	assert.Equal(t, "W120", event["code"])
}

func TestDiagnosticsArityAndFlags(t *testing.T) {
	for name, args := range map[string][]string{
		"missing": {"diagnostics"},
		"extra":   {"diagnostics", "feat: one", "fix: two"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runDiagnosticsCLI(t, args...)
			assert.Equal(t, 2, code)
			assert.Contains(t, stderr, "requires exactly one commit-message argument")
			assert.Contains(t, stderr, "usage: dispat diagnostics <message>")
		})
	}

	code, _, stderr := runDiagnosticsCLI(t, "diagnostics", "feat: okay", "--package", "core")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "--package is not a diagnostics flag")

	code, stdout, stderr := runDiagnosticsCLI(t, "diagnostics", "--", "--not-a-flag")
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "commit message refused")
	assert.Empty(t, stderr)

	for _, args := range [][]string{
		{"diagnostics", "feat: okay", "--log-level", "verbose"},
		{"diagnostics", "feat: okay", "--log-level", "fatal"},
		{"diagnostics", "feat: okay", "--log-level", "disabled"},
		{"diagnostics", "feat: okay", "--log-format", "xml"},
	} {
		code, _, _ = runDiagnosticsCLI(t, args...)
		assert.Equal(t, 2, code)
	}
}
