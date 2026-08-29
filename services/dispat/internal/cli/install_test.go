package cli

// Controller units for `dispat install`: the arity and the flag combinations,
// all of which are refused before a single request is made. What the command
// actually does with a release lives in the black-box suite, which drives the
// compiled binary against a fake releases API.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInstallUsageIsRefusedBeforeAnyRequest: every one of these is a mistake
// the flags alone can see, so none of them may cost a network call, a config
// file or a git repository. Exit 2 is the usage exit.
func TestInstallUsageIsRefusedBeforeAnyRequest(t *testing.T) {
	root := t.TempDir()
	for name, args := range map[string][]string{
		"no repository at all":       {"install"},
		"two repositories":           {"install", "acme/tool", "acme/other"},
		"a repository that is not":   {"install", "https://github.com/onlyowner"},
		"a repository with a space":  {"install", "acme/my tool"},
		"a name that is a path":      {"install", "acme/tool", "--as", "../evil"},
		"a rollback that downloads":  {"install", "acme/tool", "--rollback", "--release", "1.0.0"},
		"a rollback with a pipe":     {"install", "acme/tool", "--rollback", "--pipe", "sh"},
		"a rollback with an asset":   {"install", "acme/tool", "--rollback", "--asset", "x"},
		"a rollback with a prefix":   {"install", "acme/tool", "--rollback", "--tag-prefix", "release-"},
		"a rollback that forces":     {"install", "acme/tool", "--rollback", "--force"},
		"a rollback that prereleses": {"install", "acme/tool", "--rollback", "--prerelease"},
		"a rollback naming nothing":  {"install", "--rollback"},
		"arguments after a dash":     {"install", "acme/tool", "--", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(append(args, "--root", root), &stdout, &stderr)
			assert.Equal(t, 2, code, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		})
	}
}

// TestInstallRollbackNeedsOnlyAName: a rollback restores what is already
// installed, so it has no releases to read and no repository to read them
// from. Naming the file is enough, and the refusal below is the one the
// missing backup earns rather than a usage error.
func TestInstallRollbackNeedsOnlyAName(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"install", "--rollback", "--as", "tool", "--bin-dir", dir,
		"--root", dir}, &stdout, &stderr)
	assert.Equal(t, 1, code, "the line parses; only the absent backup stops it")
	assert.Contains(t, stdout.String()+stderr.String(), "no backup")
}

// TestInstallRollbackCheckIsAGate: --check answers whether there is anything
// to restore and changes nothing either way, so a script can ask before it
// commits to asking for one.
func TestInstallRollbackCheckIsAGate(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"install", "acme/tool", "--rollback", "--check",
		"--bin-dir", dir, "--root", dir}, &stdout, &stderr)
	assert.Equal(t, 0, code, "nothing to restore is not a failure")
	assert.Contains(t, stdout.String(), "no backup")
}

// TestInstallHelpNamesItsOwnFlags: the command table is the one place the
// mapping lives, so a flag documented for install and nowhere else has to
// render here and must not leak into another command's page.
func TestInstallHelpNamesItsOwnFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 0, Run([]string{"install", "--help"}, &stdout, &stderr))
	out := stderr.String()
	assert.Contains(t, out, "usage: dispat install <repo> [flags]")
	for _, flag := range []string{"--asset", "--bin-dir", "--as ", "--pipe", "--tag-prefix",
		"--check", "--force", "--prerelease", "--release", "--rollback", "--api-url", "--token-env"} {
		assert.Contains(t, out, flag)
	}
	for _, unwanted := range []string{"--package", "--tag ", "--set-version", "--then"} {
		assert.NotContains(t, out, unwanted, "another command's flag leaked in")
	}

	stderr.Reset()
	assert.Equal(t, 0, Run([]string{"self-update", "--help"}, &stdout, &stderr))
	assert.NotContains(t, stderr.String(), "--asset", "and install's own do not leak the other way")
	assert.NotContains(t, stderr.String(), "--pipe")
}

// TestInstallCommandWordShadowsAScript: every command word permanently
// shadows a run script of the same name, and "install" is a name a repository
// might well have given one. That has to be a deliberate, tested fact.
func TestInstallCommandWordShadowsAScript(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	// No config file here, so a script lookup would exit 1 on the missing
	// config. Exit 2 proves the word was read as the command it is.
	code := Run([]string{"install", "--root", root}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "install requires a repository")
	assert.NotContains(t, strings.ToLower(stderr.String()), "config file not found")
}
