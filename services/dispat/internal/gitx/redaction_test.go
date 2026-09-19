// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemotePushURLRefusalNamesTheRemoteSafely: the ambiguity refusal names
// the remote it refused, through the same redaction every other remote in this
// package goes through.
//
// A remote reaching this particular refusal is always a configured name —
// `git remote get-url` answers about names, and a URL spelled as the remote
// fails one line earlier — so the redaction here buys no secrecy today. It is
// written anyway because a release error reaches hook scripts through
// DISPAT_ERROR, and "this value happens to be safe" is the kind of premise
// that stops being true quietly. What the test holds down is the other half:
// a name must come out of it unchanged.
func TestRemotePushURLRefusalNamesTheRemoteSafely(t *testing.T) {
	root, cli := initRepo(t)
	runGit(t, root, "remote", "add", "ambiguous", "https://git.example.com/acme/app.git")
	runGit(t, root, "config", "--add", "remote.ambiguous.pushurl", "https://git.example.com/acme/app.git")
	runGit(t, root, "config", "--add", "remote.ambiguous.pushurl", "https://mirror.example.com/acme/app.git")

	_, err := cli.RemotePushURL(t.Context(), "ambiguous")
	require.ErrorIs(t, err, ErrAmbiguousPushDestination)
	assert.Contains(t, err.Error(), `"ambiguous"`, "the operator has to know which remote was refused")
}

// TestRedactURLKeepsTheDestinationAndDropsTheSecret pins the contract the
// error and message paths rely on: a credential-bearing URL comes out
// readable and harmless, and anything that is not a URL comes out untouched.
func TestRedactURLKeepsTheDestinationAndDropsTheSecret(t *testing.T) {
	safe := RedactURL("https://ci-bot:s3cr3t-token@git.example.com/acme/app.git?token=abc#frag")
	assert.NotContains(t, safe, "s3cr3t-token")
	assert.NotContains(t, safe, "abc")
	assert.Contains(t, safe, "REDACTED")
	assert.Contains(t, safe, "git.example.com/acme/app.git", "the destination stays legible")

	// Named and filesystem remotes are not URLs and must survive unchanged:
	// a redaction that rewrote "origin" would cost every message its meaning
	// and buy nothing.
	assert.Equal(t, "origin", RedactURL("origin"))
	assert.Equal(t, "/srv/git/app.git", RedactURL("/srv/git/app.git"))
	assert.Equal(t, "git@github.com:acme/app.git", RedactURL("git@github.com:acme/app.git"))
}
