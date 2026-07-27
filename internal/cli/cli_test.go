package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConfig = `
scripts:
  build: "echo building"
  publish: "echo publishing"
spaces:
  libs:
    path: packages
    buildScript: build
    publishScript: publish
concurrency: 1
logLevel: info
`

// initRepo creates a git monorepo with one package and one feat commit.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "monorel.yaml"), []byte(testConfig), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("hi"), 0o644))

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("add", ".")
	git("commit", "-qm", "feat(core): first release")
	return root
}

func TestStatusCommand(t *testing.T) {
	root := initRepo(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())

	out := stdout.String()
	assert.Contains(t, out, "core", "graph must list the package")
	assert.Contains(t, out, "0.1.0", "first feat release is 0.1.0")
	// Status must not build, publish or tag anything.
	assert.NotContains(t, out, "publish")
	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Empty(t, bytes.TrimSpace(tags), "status must not create tags")
}

func TestReleaseCommand(t *testing.T) {
	root := initRepo(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--root", root}, &stdout, &stderr)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr.String(), stdout.String())

	tags, err := exec.Command("git", "-C", root, "tag").Output()
	require.NoError(t, err)
	assert.Contains(t, string(tags), "core@0.1.0")
	assert.FileExists(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"bogus"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
}

func TestInvalidConfig(t *testing.T) {
	root := t.TempDir() // no monorel.yaml
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}
