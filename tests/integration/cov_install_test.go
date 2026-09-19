// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Coverage scenarios for `dispat install`: the folder rule, a destination that
// is a link to something no download may replace, and the spellings a
// repository reaches the command line under. All three are decided before any
// request, so each one is a whole invocation of the compiled binary that costs
// the fake nothing.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// covWritableDir answers the question install's own folder rule asks of
// /usr/local/bin — can a file be created here — the way the rule answers it,
// by creating one. Asking the mode bits instead answers for the wrong user
// under sudo and for no user at all on a read-only mount.
func covWritableDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(dir, ".dispat-it-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

// covInstallCheck runs `dispat install <ref> --check` against the fixture's
// fake with extra environment pairs, which is how the folder rule is driven:
// the rule reads the environment, and only a whole process has one.
func covInstallCheck(r *toolRepo, env []string, ref string, args ...string) harness.RunResult {
	r.T.Helper()
	full := append([]string{"install", ref, "--api-url", r.api,
		"--asset", "tool-{os}-{arch}", "--check"}, args...)
	return r.CommandEnv(env, full...)
}

// TestInstallFolderRuleFallsThroughUntilSomethingAnswers: where a download
// goes when no flag says. The variable answers first, a relative answer is
// made absolute before it is reported — the report and the write must name the
// same folder however the process moves — and with nothing naming one the rule
// falls through the shared folder to the user's own.
func TestInstallFolderRuleFallsThroughUntilSomethingAnswers(t *testing.T) {
	const ref = "https://github.com/acme/tool"
	name := "tool" + exeSuffix()

	t.Run("the variable names the folder when no flag does", func(t *testing.T) {
		r := newToolRepo(t)
		dir := t.TempDir()
		res := covInstallCheck(r, []string{"DISPAT_BIN_DIR=" + dir}, ref)
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "install to "+filepath.Join(dir, name))
	})

	t.Run("a relative folder is resolved before it is reported", func(t *testing.T) {
		r := newToolRepo(t)
		r.WorkFrom()
		// The folder is resolved against the process's own working directory,
		// which a temp folder reaches through a link on macOS, so the
		// expectation follows the same links the binary's answer did.
		root, err := filepath.EvalSymlinks(r.Root)
		require.NoError(t, err)
		res := covInstallCheck(r, []string{"DISPAT_BIN_DIR=vendor-bin"}, ref)
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "install to "+filepath.Join(root, "vendor-bin", name),
			`a report naming "vendor-bin" would say nothing about which one`)
	})

	t.Run("nothing names one and the rule falls through", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the shared folder of the rule is a unix path")
		}
		r := newToolRepo(t)
		home := t.TempDir()

		res := covInstallCheck(r, []string{"HOME=" + home}, ref)
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		if covWritableDir("/usr/local/bin") {
			assert.Contains(t, res.Stdout, "install to /usr/local/bin/"+name,
				"the shared folder is first whenever it can be written to")
			return
		}
		assert.Contains(t, res.Stdout, "install to "+filepath.Join(home, ".local", "bin", name),
			"and the user's own folder is where it goes when it cannot")

		// The two rungs below that one only exist under an unwritable shared
		// folder, so they are asserted where that is what the machine has.
		res = covInstallCheck(r, []string{"HOME=", "USERPROFILE=" + home}, ref)
		require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "install to "+filepath.Join(home, ".local", "bin", name),
			"windows names the same folder differently and the rule reads both")

		res = covInstallCheck(r, []string{"HOME=", "USERPROFILE="}, ref)
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "nowhere to install",
			"a machine with neither is one that has to be told")
		assert.Contains(t, res.Stdout+res.Stderr, "--bin-dir")
	})
}

// TestInstallRefusesALinkToSomethingThatIsNotAFile: a link on PATH pointing at
// a binary is an ordinary way to install one, and replacing the link is what
// was asked for. A link to anything else is that thing, and an install is two
// renames: the first would move somebody's folder out of the way to stand a
// binary where it stood.
func TestInstallRefusesALinkToSomethingThatIsNotAFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a link needs a privilege the test runner may not have")
	}
	r := newToolRepo(t)
	folder := filepath.Join(r.bin, "a-folder-somebody-owns")
	require.NoError(t, os.Mkdir(folder, 0o755))
	require.NoError(t, os.Symlink(folder, r.installed()))

	before := len(r.requests())
	res := r.install()
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "link to something that is not a file")
	assert.Equal(t, before, len(r.requests()), "the refusal is decided on disk and costs no request")
	assert.DirExists(t, folder, "and what the link pointed at is still there")

	// The same link pointed at an ordinary file is exactly what an install
	// replaces, so the rule is about what the link resolves to rather than
	// about links.
	require.NoError(t, os.Remove(r.installed()))
	target := filepath.Join(r.bin, "the-real-file")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\necho \"tool 0.0.1\"\n"), 0o755))
	require.NoError(t, os.Symlink(target, r.installed()))

	res = r.install()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, toolNew, r.version(r.installed()))
}

// TestInstallReadsARepositoryHoweverItIsSpelled: naming a repository by URL is
// worth doing because the URL is what the browser is showing, so every
// spelling a reader has at hand reaches the same two path segments — and every
// spelling that names no repository is refused with the spelling that would,
// before a single request.
func TestInstallReadsARepositoryHoweverItIsSpelled(t *testing.T) {
	r := newToolRepo(t)

	for name, tc := range map[string]struct{ ref, want string }{
		"the shorthand":                {ref: "acme/tool", want: "repository acme/tool"},
		"an ssh remote":                {ref: "git@github.com:acme/tool.git", want: "repository acme/tool"},
		"a clone URL":                  {ref: "https://github.com/acme/tool.git", want: "repository acme/tool"},
		"a page inside the repository": {ref: "https://github.com/acme/tool/releases/tag/v1.1.0", want: "repository acme/tool"},
		"a trailing slash":             {ref: "https://github.com/acme/tool/", want: "repository acme/tool"},
		"an enterprise host":           {ref: "https://ghe.example.com/acme/tool", want: "repository ghe.example.com/acme/tool"},
		"an enterprise host with no scheme": {ref: "ghe.example.com/acme/tool",
			want: "repository ghe.example.com/acme/tool"},
	} {
		t.Run(name, func(t *testing.T) {
			res := covInstallCheck(r, nil, tc.ref, "--bin-dir", r.bin)
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout, tc.want)
		})
	}

	before := len(r.requests())
	for name, tc := range map[string]struct{ ref, want string }{
		"a host and nothing else":      {ref: "https://github.com", want: "names a host but no repository"},
		"an owner and nothing else":    {ref: "acme", want: "names no repository"},
		"an owner no GitHub name is":   {ref: "ac me/tool", want: "which no GitHub name may"},
		"a repository no name is":      {ref: "acme/to ol", want: "which no GitHub name may"},
		"a dot standing for the owner": {ref: "./tool", want: "is not a name for the owner"},
		"a host no host name is":       {ref: "https://exa mple.com/acme/tool", want: "which no host name may"},
	} {
		t.Run(name, func(t *testing.T) {
			res := covInstallCheck(r, nil, tc.ref, "--bin-dir", r.bin)
			assert.NotEqual(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
		})
	}
	assert.Equal(t, before, len(r.requests()), "no refusal here costs a request")
}

// TestInstallReadsAPortFromASchemeQualifiedURL: the enterprise clone URL a
// browser or a git remote actually hands somebody — a scheme, a credential and
// an explicit port together — reaches the repository it names.
//
// It used to reach another one. The scp-like "host:path" split that reads
// git@host:owner/repo ran even after a scheme had been consumed, so the port
// became the owner and the owner became the repository: a reference spelled
// `ssh://git@host:22/acme/tool` was read as owner "22" and repository "acme",
// and the command queried a repository nobody named. Dropping either the
// credential or the port hid it, which is why it survived, so both are present
// in each row and the whole host, port included, is asserted rather than only
// the two names.
func TestInstallReadsAPortFromASchemeQualifiedURL(t *testing.T) {
	r := newToolRepo(t)
	for name, tc := range map[string]struct{ ref, want string }{
		"an ssh remote with a port": {ref: "ssh://git@ghe.example.com:22/acme/tool",
			want: "repository ghe.example.com:22/acme/tool"},
		"an https clone URL with a credential and a port": {ref: "https://ci-bot@ghe.example.com:8443/acme/tool",
			want: "repository ghe.example.com:8443/acme/tool"},
	} {
		t.Run(name, func(t *testing.T) {
			res := covInstallCheck(r, nil, tc.ref, "--bin-dir", r.bin)
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout, tc.want,
				"%s names the repository acme/tool on that host, whatever else it carries", tc.ref)
		})
	}
}
