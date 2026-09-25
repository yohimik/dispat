// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestFinalSelfUpdateRefusesIncompleteHTTPResponses distinguishes a broken
// transport from a complete response carrying invalid JSON: a release listing
// that stops before its body ends is refused as the transport failure it is,
// and the binary is left alone. The same cut in the asset response is a row of
// TestSelfUpdateRefusesWhatItCannotTrust.
func TestFinalSelfUpdateRefusesIncompleteHTTPResponses(t *testing.T) {
	api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", "4096")
		_, _ = fmt.Fprint(w, "partial response")
	})
	r := harness.New(t)
	exe := covSUExe(t, suOld)
	before, err := os.ReadFile(exe)
	require.NoError(t, err)
	res := r.CommandBin(exe, "self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
	require.NotZero(t, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "unexpected EOF")
	after, err := os.ReadFile(exe)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	entries, err := os.ReadDir(filepath.Dir(exe))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "a partial response leaves no backup or staging file")
}

// TestFinalSelfUpdateCannotInstallANamedDraftOrFailedLookup ensures an
// explicitly named version does not bypass publication or transport checks.
func TestFinalSelfUpdateCannotInstallANamedDraftOrFailedLookup(t *testing.T) {
	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprint("draft=", draft), func(t *testing.T) {
			api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
				if !draft {
					http.Error(w, "release lookup unavailable", http.StatusServiceUnavailable)
					return
				}
				release := covSUReleaseJSON(a.base, suNew, []byte("not published"))
				release["draft"] = true
				_ = json.NewEncoder(w).Encode(release)
			})
			r := harness.New(t)
			exe := covSUExe(t, suOld)
			res := r.CommandBin(exe, "self-update", "--release", suNew,
				"--api-url", api.base, "--owner", "o", "--repo", "r")
			require.NotZero(t, res.Code)
			want := "503"
			if draft {
				want = "is a draft"
			}
			assert.Contains(t, res.Stdout+res.Stderr, want)
			assert.Equal(t, suOld, covVersionOf(t, r, exe))
			assert.NoFileExists(t, backupPath(exe))
			assert.Len(t, api.requests(), 1, "a refused named lookup never downloads an asset")
		})
	}
}

// TestFinalSelfUpdateReportsBothDownloadFailures retains both endpoint
// failures when the authenticated asset and its public fallback are unavailable.
func TestFinalSelfUpdateReportsBothDownloadFailures(t *testing.T) {
	api := covSUServe(t, func(a *covSUAPI, w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			http.Error(w, "private endpoint refused", http.StatusForbidden)
		case strings.HasPrefix(req.URL.Path, "/dl/"):
			assert.Empty(t, req.Header.Get("Authorization"), "the public fallback receives no token")
			http.Error(w, "public endpoint unavailable", http.StatusServiceUnavailable)
		default:
			_ = json.NewEncoder(w).Encode([]any{covSUReleaseJSON(a.base, suNew, []byte("binary"))})
		}
	})
	r := harness.New(t)
	exe := covSUExe(t, suOld)
	res := r.CommandBinEnv(exe, []string{"GITHUB_TOKEN=private-example-token"},
		"self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
	require.NotZero(t, res.Code)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "403")
	assert.Contains(t, combined, "503")
	assert.Contains(t, combined, "public download URL then failed too")
	assert.NotContains(t, combined, "private-example-token")
	assert.Equal(t, suOld, covVersionOf(t, r, exe))
	assert.NoFileExists(t, backupPath(exe))
}

// TestFinalSelfUpdateNotesPreserveUnicodeAndSkipBothFenceStyles keeps release
// examples out of the terminal summary, even when their contents resemble
// headings or another fence, and clips long text at a complete UTF-8 rune.
func TestFinalSelfUpdateNotesPreserveUnicodeAndSkipBothFenceStyles(t *testing.T) {
	r := newSURepo(t)
	r.body = "### Changes\n\n```sh\n### hidden command\n~~~\n```\n" +
		"~~~text\n### hidden example\n```\n~~~\n" +
		"### ###\n- " + strings.Repeat("界", 100) + "\n- visible change\n"
	r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})
	res := r.update("--check")
	require.Equal(t, 1, res.Code, "a newer release is available")
	assert.Contains(t, res.Stdout, "visible change")
	assert.Contains(t, res.Stdout, "界")
	assert.Contains(t, res.Stdout, "notes go on")
	assert.NotContains(t, res.Stdout, "hidden command")
	assert.NotContains(t, res.Stdout, "hidden example")
	assert.True(t, utf8.ValidString(res.Stdout))
	assert.NotContains(t, res.Stdout, "�")
	assert.Equal(t, suOld, r.version(r.exe))
}
