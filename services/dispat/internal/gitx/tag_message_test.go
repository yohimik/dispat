// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A receipt can be larger than an operating system's per-argument limit. Its
// annotation must reach the tag object intact without travelling in argv.
func TestCreateTagStreamsLargeAnnotation(t *testing.T) {
	root, git := initRepo(t)
	message := "release core@1.0.0\n\n" + strings.Repeat("x", 256<<10) + "\n"
	require.NoError(t, git.CreateTag(t.Context(), "core@1.0.0", message, "HEAD"))

	object := runGit(t, root, "cat-file", "-p", "refs/tags/core@1.0.0")
	_, stored, found := strings.Cut(object, "\n\n")
	require.True(t, found, "an annotated tag has a message after its headers")
	assert.Equal(t, len(message), len(stored), "the annotation was not truncated")
	assert.Equal(t, sha256.Sum256([]byte(message)), sha256.Sum256([]byte(stored)))
}
