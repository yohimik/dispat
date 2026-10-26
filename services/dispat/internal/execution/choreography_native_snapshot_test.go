// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestCompiledNativeSnapshotDetectsUncommittedSharedInputOnly(t *testing.T) {
	fixture := newSnapshotFixture(t)
	fixture.write(t, "shared.lock", "before\n")
	coordinator := &Coordinator{Log: zerolog.Nop(), snapshots: newSnapshots()}
	coordinator.dispatch = Dispatch{
		Sources:        func(string) []Source { return []Source{fixture.source()} },
		OpenRepository: func(string) *gitx.LocalGitx { return fixture.git },
	}
	before, err := coordinator.CaptureCurrentPreparedSnapshot(t.Context(), "app", "")
	require.NoError(t, err)
	fixture.write(t, "shared.lock", "after\n")
	after, err := coordinator.CaptureCurrentPreparedSnapshot(t.Context(), "app", "")
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	changed, err := fixture.git.ChangedPathsBetween(context.Background(), before, after,
		[]string{"shared.lock"})
	require.NoError(t, err)
	require.Equal(t, []string{"shared.lock"}, changed)
	require.Equal(t, fixture.head, strings.TrimSpace(fixture.run(t, "rev-parse", "HEAD")))

	fixture.write(t, "unrelated.txt", "other\n")
	unrelated, err := coordinator.CaptureCurrentPreparedSnapshot(t.Context(), "app", "")
	require.NoError(t, err)
	changed, err = fixture.git.ChangedPathsBetween(context.Background(), after, unrelated,
		[]string{"shared.lock"})
	require.NoError(t, err)
	require.Empty(t, changed)
}
