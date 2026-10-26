// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestPinnedTagSourcesCarryLightweightAndAnnotatedHistoryTargets(t *testing.T) {
	root := t.TempDir()
	a := &App{root: root, pinnedNativeRefs: []execution.PinnedNativeRef{
		{Name: "core/v1", Object: "commit-one"},
		{Name: "core/v2", Object: "tag-object", Peeled: "commit-two"},
	}}
	sources, err := a.pinnedTagSources()
	require.NoError(t, err)
	require.Equal(t, []execution.ChoreographyTagSource{
		{Name: "core/v1", Object: "commit-one", SourceDir: root},
		{Name: "core/v2", Object: "tag-object", Peeled: "commit-two", SourceDir: root},
	}, sources)
}
