// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// credentialRemote is a remote spelled as a URL carrying a password, which is
// how a CI checkout configured with a token remote looks from inside dispat.
const credentialRemote = "https://ci-bot:s3cr3t-token@git.example.com/acme/app.git"

// TestMergeRecoveryTextsRedactTheRemote: the merge recovery writes the remote
// into places that outlive the run — a merge commit message and a changelog
// note — and into errors that reach hook scripts through DISPAT_ERROR. A
// remote may be a URL with credentials in it, so every one of those goes
// through the same redaction the log lines use.
func TestMergeRecoveryTextsRedactTheRemote(t *testing.T) {
	message := mergeMessage(credentialRemote, "main", "0123456789abcdef0123456789abcdef01234567", []string{"core@1.0.0"})
	assert.NotContains(t, message, "s3cr3t-token", "a merge commit message is durable repository state")
	assert.Contains(t, message, "REDACTED")
	assert.Contains(t, message, "git.example.com/acme/app.git", "the destination still has to be readable")

	note := conflictNote(credentialRemote, "dispat/conflict-core-1.0.0", []string{"packages/core/main.go"})
	assert.NotContains(t, note, "s3cr3t-token", "the note is committed into the changelog")
	assert.Contains(t, note, "REDACTED")

	// A named remote is not a URL and must survive untouched: redaction that
	// rewrote "origin" would make every message unreadable to buy nothing.
	plain := mergeMessage("origin", "main", "0123456789abcdef0123456789abcdef01234567", nil)
	assert.Contains(t, plain, "origin/main")
	assert.NotContains(t, plain, "REDACTED")

	// A filesystem remote, which is what every fixture in this repository
	// uses, is likewise not a URL.
	local := conflictNote("/srv/git/app.git", "dispat/conflict", []string{"a.txt"})
	assert.Contains(t, local, "/srv/git/app.git")
	assert.False(t, strings.Contains(local, "REDACTED"))
}
