// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPinnedInvocationCarriesSemanticOverlayWithoutNodeRoutes(t *testing.T) {
	root := t.TempDir()
	imported := filepath.Join(root, "source.json")
	require.NoError(t, os.WriteFile(imported, []byte(`{}`), 0o600))
	a := &App{root: root, ignoreTags: []string{"core/v0"}, planningInvocation: PlanningConfigInvocation{
		Concurrency: []int{2, 1}, ConcurrencySet: true,
		Polyrepo: true, PolyrepoSet: true,
		ImportedConfigs: []string{imported},
		Workers:         []string{"peer@https://secret.example.invalid/token"},
	}}
	document, err := a.pinnedInvocation()
	require.NoError(t, err)
	text := string(document)
	require.Contains(t, text, `"concurrency":[2,1]`)
	require.Contains(t, text, `"imports":[{"repository":"","path":"source.json"}]`)
	require.Contains(t, text, `"ignoredTags":["core/v0"]`)
	require.False(t, strings.Contains(text, "secret.example.invalid"))
	require.False(t, strings.Contains(text, "workers"))
	decoded, err := parsePinnedInvocation(document)
	require.NoError(t, err)
	require.Equal(t, []int{2, 1}, decoded.Concurrency)
	_, err = parsePinnedInvocation([]byte(`{"imports":[{"repository":"","path":"../other"}]}`))
	require.ErrorContains(t, err, "escapes")
	_, err = parsePinnedInvocation([]byte(`{"returnEndpoint":"https://foreign"}`))
	require.ErrorContains(t, err, "unknown field")
}
