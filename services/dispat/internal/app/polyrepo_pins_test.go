// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

type pinCaptureRunner struct {
	env []string
}

func (r *pinCaptureRunner) Run(_ context.Context, _, _ string, env []string, _, _ io.Writer) error {
	r.env = append([]string(nil), env...)
	return nil
}

func lastPinValue(env []string, key string) string {
	prefix := key + "="
	var value string
	for _, pair := range env {
		if len(pair) >= len(prefix) && pair[:len(prefix)] == prefix {
			value = pair[len(prefix):]
		}
	}
	return value
}

func TestWorkspacePinsCarryNativeRecordToLaterRunner(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	rel.Pkg.Changelog.Enabled = true
	require.NoError(t, w.Record(t.Context(), rel))

	pin := recordGit(t, w.byName["source"].repo.Root, "rev-parse", "HEAD")
	capture := &pinCaptureRunner{}
	require.NoError(t, w.pins.runner(capture).Run(t.Context(), rel.Pkg.Dir, "later-independent-script", nil, io.Discard, io.Discard))
	assert.Equal(t, pin, lastPinValue(capture.env, "DISPAT_OUTPUT_PACKAGE_LIB"))
}

func TestWorkspacePinsAreVisibleToSourceAfterCommitHook(t *testing.T) {
	w, rel := recordFixture(t, true, false)
	source := w.byName["source"]
	rel.Pkg.Changelog.Enabled = true
	source.repo.Config.Scripts = map[string]config.Script{
		"capture-source-pin": {`printf '%s\n' "$DISPAT_OUTPUT_PACKAGE_LIB" > after-commit-pin`},
	}
	source.repo.Config.Run.AfterCommit = []string{"capture-source-pin"}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	require.NoError(t, w.Record(ctx, rel), "the hook must not deadlock while reading the pin recorded before it")

	pin := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	got, err := os.ReadFile(filepath.Join(source.repo.Root, "after-commit-pin"))
	require.NoError(t, err)
	assert.Equal(t, pin+"\n", string(got))
}

func TestWorkspacePinsRejectUnverifiedOrWrongOwnerEntries(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name  string
		owner string
		rel   *plan.Release
		pin   string
	}{
		{name: "nil release", owner: "source", pin: valid},
		{name: "nil package", owner: "source", rel: &plan.Release{}, pin: valid},
		{name: "control repository", owner: config.ControlRepository, rel: &plan.Release{Pkg: &model.Package{Name: "lib", Repository: config.ControlRepository}}, pin: valid},
		{name: "foreign owner", owner: "other", rel: &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}, pin: valid},
		{name: "abbreviated object", owner: "source", rel: &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}, pin: valid[:12]},
		{name: "non hexadecimal object", owner: "source", rel: &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}, pin: "z123456789abcdef0123456789abcdef01234567"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pins := &workspacePins{}
			require.NoError(t, pins.remember(tc.owner, tc.rel, tc.pin))
			assert.Equal(t, []string{"BASE=kept"}, pins.environment([]string{"BASE=kept"}))
		})
	}
}

func TestWorkspacePinsSnapshotDoesNotMutateInputAndCurrentExportWins(t *testing.T) {
	stored := "0123456789abcdef0123456789abcdef01234567"
	current := "89abcdef0123456789abcdef0123456789abcdef"
	pins := &workspacePins{}
	require.NoError(t, pins.remember("source", &plan.Release{Pkg: &model.Package{Name: "lib", Repository: "source"}}, stored))

	backing := []string{"KEEP=present", "unused-capacity-sentinel"}
	caller := backing[:1:2]
	got := pins.environment(caller)

	assert.Equal(t, []string{"KEEP=present"}, caller)
	assert.Equal(t, "unused-capacity-sentinel", backing[1], "building a snapshot must not append through caller storage")
	assert.Equal(t, stored, lastPinValue(got, "DISPAT_OUTPUT_PACKAGE_LIB"))
	withCurrent := pins.environment([]string{"KEEP=present", "DISPAT_OUTPUT_PACKAGE_LIB=" + current})
	assert.Equal(t, current, lastPinValue(withCurrent, "DISPAT_OUTPUT_PACKAGE_LIB"), "the active script sequence owns its explicit current export")
	assert.Equal(t, stored, lastPinValue(pins.environment(nil), "DISPAT_OUTPUT_PACKAGE_LIB"), "taking a snapshot must not replace the registry value")
}
