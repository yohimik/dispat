// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

func TestGoInstallBuildUsesTheGoToolchainForUpdates(t *testing.T) {
	r := newSURepo(t)
	copyFile(t, harness.BuildGoInstalled(t, suOld), r.exe)

	version := r.CommandBin(r.exe, "--version")
	require.Equal(t, 0, version.Code, "stderr:\n%s", version.Stderr)
	assert.Contains(t, version.Stdout, "dispat "+suOld)
	assert.Contains(t, version.Stdout, "go install",
		"the executable's module build metadata determines its origin")

	check := r.CommandBin(r.exe, "self-update", "--check", "--api-url", r.api, "--owner", "o", "--repo", "r")
	require.Equal(t, 1, check.Code, "stdout:\n%s\nstderr:\n%s", check.Stdout, check.Stderr)
	assert.Contains(t, check.Stdout,
		"update it with: go install github.com/yohimik/dispat/services/dispat@latest")
	assert.NotContains(t, check.Stdout, "install it with: dispat self-update")

	before, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	update := r.CommandBin(r.exe, "self-update", "--api-url", r.api, "--owner", "o", "--repo", "r")
	require.NotZero(t, update.Code, "stdout:\n%s\nstderr:\n%s", update.Stdout, update.Stderr)
	updateOutput := update.Stdout + update.Stderr
	assert.Contains(t, updateOutput, "this dispat was installed with go install")
	assert.Contains(t, updateOutput,
		"go install github.com/yohimik/dispat/services/dispat@latest")
	after, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(after, before), "the refused update changed the Go-installed executable")
	assert.NoFileExists(t, r.backup, "a refusal before installation must not create a backup")
	for _, request := range r.requests() {
		assert.False(t, strings.Contains(request, "/assets/"),
			"the refusal must happen before downloading an asset: %s", request)
	}

	notice := r.CommandBinEnv(r.exe, []string{"DISPAT_UPDATE_CHECK=1"},
		"--version", "--api-url", r.api, "--owner", "o", "--repo", "r")
	require.Equal(t, 0, notice.Code, "stdout:\n%s\nstderr:\n%s", notice.Stdout, notice.Stderr)
	assert.Contains(t, notice.Stdout, "a newer stable release is available: "+suNew)
	assert.NotContains(t, notice.Stdout, "dispat self-update",
		"an ambient notice must not recommend replacing a Go-managed executable")
}
