package cli

// Controller units for `dispat download`: the arity and the flag combinations,
// all of which are refused before a single request is made. What the command
// actually does with a release lives in the black-box suite, which drives the
// compiled binary against a fake releases API.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDownloadUsageIsRefusedBeforeAnyRequest: every one of these is a mistake
// the flags alone can see, so none of them may cost a network call, a config
// file or a git repository. Exit 2 is the usage exit.
func TestDownloadUsageIsRefusedBeforeAnyRequest(t *testing.T) {
	root := t.TempDir()
	for name, args := range map[string][]string{
		"no repository at all":       {"download"},
		"two repositories":           {"download", "acme/tool", "acme/other"},
		"a repository that is not":   {"download", "https://github.com/onlyowner"},
		"a repository with a space":  {"download", "acme/my tool"},
		"a name that is a path":      {"download", "acme/tool", "--as", "../evil"},
		"a rollback that downloads":  {"download", "acme/tool", "--rollback", "--release", "1.0.0"},
		"a rollback with a pipe":     {"download", "acme/tool", "--rollback", "--pipe", "sh"},
		"a rollback with an asset":   {"download", "acme/tool", "--rollback", "--asset", "x"},
		"a rollback with a prefix":   {"download", "acme/tool", "--rollback", "--tag-prefix", "release-"},
		"a rollback that forces":     {"download", "acme/tool", "--rollback", "--force"},
		"a rollback that prereleses": {"download", "acme/tool", "--rollback", "--prerelease"},
		"a rollback naming nothing":  {"download", "--rollback"},
		"arguments after a dash":     {"download", "acme/tool", "--", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(append(args, "--root", root), &stdout, &stderr)
			assert.Equal(t, 2, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		})
	}
}

// TestDownloadRollbackNeedsOnlyAName: a rollback restores what is already
// installed, so it has no releases to read and no repository to read them
// from. Naming the file is enough, and the refusal below is the one the
// missing backup earns rather than a usage error.
func TestDownloadRollbackNeedsOnlyAName(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"download", "--rollback", "--as", "tool", "--bin-dir", dir,
		"--root", dir}, &stdout, &stderr)
	assert.Equal(t, 1, code, "the line parses; only the absent backup stops it")
	assert.Contains(t, stdout.String()+stderr.String(), "no backup")
}

// TestDownloadRollbackCheckIsAGate: --check answers whether there is anything
// to restore and changes nothing either way, so a script can ask before it
// commits to asking for one.
func TestDownloadRollbackCheckIsAGate(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"download", "acme/tool", "--rollback", "--check",
		"--bin-dir", dir, "--root", dir}, &stdout, &stderr)
	assert.Equal(t, 0, code, "nothing to restore is not a failure")
	assert.Contains(t, stdout.String(), "no backup")
}

// TestDownloadHelpNamesItsOwnFlags: the command table is the one place the
// mapping lives, so a flag documented for download and nowhere else has to
// render here and must not leak into another command's page.
func TestDownloadHelpNamesItsOwnFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 0, Run([]string{"download", "--help"}, &stdout, &stderr))
	out := stderr.String()
	assert.Contains(t, out, "usage: dispat download <repo> [flags]")
	for _, flag := range []string{"--asset", "--bin-dir", "--as ", "--pipe", "--tag-prefix",
		"--check", "--force", "--prerelease", "--release", "--rollback", "--api-url", "--token-env"} {
		assert.Contains(t, out, flag)
	}
	for _, unwanted := range []string{"--package", "--tag ", "--set-version", "--then"} {
		assert.NotContains(t, out, unwanted, "another command's flag leaked in")
	}

	stderr.Reset()
	assert.Equal(t, 0, Run([]string{"self-update", "--help"}, &stdout, &stderr))
	assert.NotContains(t, stderr.String(), "--asset", "and download's own do not leak the other way")
	assert.NotContains(t, stderr.String(), "--pipe")
}

// TestDownloadCommandWordShadowsAScript: every command word permanently
// shadows a run script of the same name, and "download" is a name a repository
// might well have given one. That has to be a deliberate, tested fact.
func TestDownloadCommandWordShadowsAScript(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	// No config file here, so a script lookup would exit 1 on the missing
	// config. Exit 2 proves the word was read as the command it is.
	code := Run([]string{"download", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "download requires a repository")
	assert.NotContains(t, strings.ToLower(stderr.String()), "config file not found")
}
