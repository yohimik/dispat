package cli

// What an environment file adds to a run, and what it must never do: override
// a variable the environment already carries, or print a value.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inDir runs the test in dir, which is what "the current directory" means to
// the default file.
func inDir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(previous)) })
}

// writeEnvFile writes one environment file into dir.
func writeEnvFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestEnvFileAddsVariables(t *testing.T) {
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env", "# a comment\nADD_TOKEN=secret\nexport ADD_QUOTED=\"two words\"\n")
	inDir(t, dir)

	require.NoError(t, loadEnvFiles(nil, zerolog.Nop()))
	assert.Equal(t, "secret", os.Getenv("ADD_TOKEN"))
	assert.Equal(t, "two words", os.Getenv("ADD_QUOTED"))
}

func TestEnvFileKeepsWhatTheEnvironmentSets(t *testing.T) {
	// The rule that makes it safe for dispat to read these files itself: a
	// value the caller exported, in CI or in a shell, is never replaced by one
	// committed to the repository.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env", "KEEP_PRESET=from-file\nKEEP_FRESH=from-file\n")
	inDir(t, dir)
	t.Setenv("KEEP_PRESET", "from-the-environment")

	require.NoError(t, loadEnvFiles(nil, zerolog.Nop()))
	assert.Equal(t, "from-the-environment", os.Getenv("KEEP_PRESET"))
	assert.Equal(t, "from-file", os.Getenv("KEEP_FRESH"))
}

func TestEnvFileMissingDefaultIsNothing(t *testing.T) {
	// Most repositories have no .env at all, and that is not an error.
	inDir(t, t.TempDir())
	require.NoError(t, loadEnvFiles(nil, zerolog.Nop()))
}

func TestEnvFileFlagReplacesTheDefault(t *testing.T) {
	// A named file is read instead of ./.env, and several are read in order
	// with the later one winning, which is what reading them as written means.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env", "FLAG_SOURCE=default\n")
	base := writeEnvFile(t, dir, "base.env", "FLAG_SOURCE=base\nFLAG_ONLY_BASE=yes\n")
	ci := writeEnvFile(t, dir, "ci.env", "FLAG_SOURCE=ci\n")
	inDir(t, dir)

	require.NoError(t, loadEnvFiles([]string{base, ci}, zerolog.Nop()))
	assert.Equal(t, "ci", os.Getenv("FLAG_SOURCE"), "the later file wins")
	assert.Equal(t, "yes", os.Getenv("FLAG_ONLY_BASE"), "the earlier file still contributes")
}

func TestEnvFileFailures(t *testing.T) {
	dir := t.TempDir()
	inDir(t, dir)

	t.Run("a named file that is not there", func(t *testing.T) {
		err := loadEnvFiles([]string{filepath.Join(dir, "absent.env")}, zerolog.Nop())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot read the environment file")
	})

	t.Run("a line that is not an assignment", func(t *testing.T) {
		path := writeEnvFile(t, dir, "broken.env", "BROKEN_TOKEN=fine\nthis is not a variable\n")
		err := loadEnvFiles([]string{path}, zerolog.Nop())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot read the environment file")
	})
}

func TestEnvFileNeverLogsAValue(t *testing.T) {
	// An environment file is exactly where the secrets are, so the logs name
	// the keys and nothing else, at every level.
	dir := t.TempDir()
	writeEnvFile(t, dir, ".env", "LOG_TOKEN=super-secret\nLOG_PRESET=also-secret\n")
	inDir(t, dir)
	t.Setenv("LOG_PRESET", "kept")

	var logged bytes.Buffer
	log := zerolog.New(&logged).Level(zerolog.TraceLevel)
	require.NoError(t, loadEnvFiles(nil, log))

	out := logged.String()
	assert.Contains(t, out, "LOG_TOKEN", "the key is worth logging")
	assert.NotContains(t, out, "super-secret")
	assert.NotContains(t, out, "also-secret")

	// The debug line reports what happened without naming anything sensitive.
	var summary map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if event["message"] == "environment files read" {
			summary = event
		}
	}
	require.NotNil(t, summary)
	assert.Equal(t, float64(1), summary["added"])
	assert.Equal(t, float64(1), summary["kept"])
}
