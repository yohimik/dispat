// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestExecutionMalformedObjectStreamCannotPublish checks the framing boundary
// after a real worker build: a successful Git process with an invalid batch
// reply must not become an admitted output or a published release.
func TestExecutionMalformedObjectStreamCannotPublish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git protocol fixture uses a POSIX shell")
	}
	for _, tc := range []struct{ name, reply, want string }{
		{"wrong object", `printf '%040d blob 0\n\n' 0`, "instead of"},
		{"missing object", `printf '%s missing\n' "$oid"`, "does not hold the object"},
		{"wrong type", `printf '%s tree 0\n\n' "$oid"`, "not a blob header"},
		{"invalid size", `printf '%s blob unknown\n' "$oid"`, "not a number"},
		{"negative size", `printf '%s blob -1\n' "$oid"`, "is negative"},
		{"oversized object", `printf '%s blob 9223372036854775807\n' "$oid"`, "too-many-bytes"},
		{"long header", `printf '%s blob ' "$oid"; head -c 2048 /dev/zero | tr '\000' x; printf '\n'`, "too-many-bytes"},
		{"truncated header", `printf '%s blob' "$oid"`, "did not answer"},
		{"truncated contents", `printf '%s blob 10\na' "$oid"`, "reading the 10 bytes"},
		{"missing terminator", `printf '%s blob 0\n' "$oid"`, "reading the end"},
		{"wrong terminator", `printf '%s blob 0\nx' "$oid"`, "no terminating newline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newExecutionFaultOutputs(t)
			// Capturing outputs reads the task checkout. Mailbox verification
			// reads its bare cache, which must remain intact until this build.
			outputOID := gitIn(t, rig.repo.Root, "assets 0.1.0 "+executionNode+"\n", "hash-object", "--stdin")
			env, marker := executionBatchReply(t, "*-C */tasks/*/checkout cat-file --batch", outputOID, tc.reply)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, env...)
			res := rig.release()
			reply := stopAll(t, []*executionWorker{worker})[0]
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			require.FileExists(t, marker, "a completed build reached the object protocol")
			assert.Equal(t, 1, executionBuildCount(rig, "assets"))
			assert.Contains(t, reply.Stdout+reply.Stderr, tc.want)
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode))
			assert.Empty(t, executionReleasedPackages(res))
			assert.Empty(t, executionReleaseTags(rig))
			assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
				"the rejected output remains in a signed run history for recovery")
			for _, run := range rig.runs() {
				assert.False(t, strings.HasPrefix(run.Node, "probe-publish"))
			}
		})
	}
}

func executionBatchReply(t *testing.T, pattern, firstObject, reply string) ([]string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Git protocol fixture uses a POSIX shell")
	}
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	shim := t.TempDir()
	marker := filepath.Join(shim, "matched")
	script := `#!/bin/sh
case "$*" in
$DISPAT_IT_BATCH_PATTERN)
IFS= read -r oid || exit 1
selected=0
if [ -n "$DISPAT_IT_BATCH_OBJECT" ]; then
  [ "$oid" = "$DISPAT_IT_BATCH_OBJECT" ] && selected=1
else
  previous=
  repository=.
  for argument do
    if [ "$previous" = -C ]; then repository=$argument; fi
    previous=$argument
  done
  object_type=$(GIT_NO_REPLACE_OBJECTS=1 "$DISPAT_IT_BATCH_GIT" -C "$repository" cat-file -t "$oid") || exit 1
  [ "$object_type" = blob ] && selected=1
fi
if [ "$selected" -eq 0 ]; then
  { printf '%s\n' "$oid"; cat; } | "$DISPAT_IT_BATCH_GIT" "$@"
  exit "$?"
fi
printf hit >> "$DISPAT_IT_BATCH_MARKER"
` + reply + `
exit 0;;
esac
exec "$DISPAT_IT_BATCH_GIT" "$@"
`
	require.NoError(t, os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755))
	return []string{
		"PATH=" + shim + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DISPAT_IT_BATCH_GIT=" + realGit, "DISPAT_IT_BATCH_MARKER=" + marker,
		"DISPAT_IT_BATCH_PATTERN=" + pattern,
		"DISPAT_IT_BATCH_OBJECT=" + firstObject,
	}, marker
}

// TestExecutionMalformedMailboxListingCannotStartRelease checks that the
// mailbox cannot turn malformed or unbounded remote refs into an empty pool
// or trusted assignment inventory.
func TestExecutionMalformedMailboxListingCannotStartRelease(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, tc := range []struct{ name, output, want string }{
		{"missing separator", oid + " refs/heads/dispat-worker-a-x\n", "malformed remote ref entry"},
		{"invalid object", "no-object\trefs/heads/dispat-worker-a-x\n", "malformed remote ref entry"},
		{"foreign namespace", oid + "\trefs/tags/dispat-worker-a-x\n", "malformed remote ref entry"},
		{"invalid ref", oid + "\trefs/heads/dispat-worker-a..x\n", "malformed remote ref entry"},
		{"partial listing", oid + "\trefs/heads/dispat-worker-a-x", "ended mid-entry"},
		{"unbounded ref", oid + "\trefs/heads/" + strings.Repeat("x", 1<<17) + "\n\x00", "entry exceeds"},
		{"too many refs", strings.Repeat(oid+"\trefs/heads/dispat-worker-a-x\n", 10001) + "\x00", "more than 10000 refs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 2}
			})
			worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*ls-remote --heads -- *dispat-worker-*", Output: tc.output})
			res := rig.release(fault.Env()...)
			stopAll(t, []*executionWorker{worker})
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches())
			assert.Contains(t, res.Stdout+res.Stderr, tc.want)
			assert.Zero(t, buildRuns(rig.repo))
			assert.Empty(t, rig.repo.TagList())
		})
	}
}

// TestExecutionMalformedTreeStreamCannotPublish verifies the tree metadata
// boundary separately from blob framing. The worker reads the bootstrap tree
// during preflight, so a corrupt listing is refused before any task starts.
func TestExecutionMalformedTreeStreamCannotPublish(t *testing.T) {
	for _, tc := range []struct{ name, output, want string }{
		{"missing path", "100644 blob deadbeef 0\x00", "has no path"},
		{"missing field", "100644 blob deadbeef\tdist/file\x00", "rather than four"},
		{"truncated record", "100644 blob deadbeef 0\tdist/file", "ended mid-entry"},
		{"oversized terminated record", "100644 blob deadbeef 0\t" + strings.Repeat("x", 1<<17) + "\x00", "exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newExecutionFaultOutputs(t)
			fault := harness.NewGitFault(t, harness.GitFault{Pattern: "*ls-tree -r -l -z*", Output: tc.output})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox), 0, fault.Env()...)
			res := rig.release()
			reply := stopAll(t, []*executionWorker{worker})[0]
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches())
			assert.Contains(t, reply.Stdout+reply.Stderr, tc.want)
			assert.Empty(t, executionReleasedPackages(res))
			assert.Empty(t, executionReleaseTags(rig))
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode),
				"the invalid bootstrap tree prevents authenticated preflight\nstdout:\n%s", res.Stdout)
			assert.Zero(t, buildRuns(rig.repo), "no frame ran after a corrupt bootstrap tree")
			assert.Empty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
				"the run did not publish a runtime ledger after failed preflight")
		})
	}
}

// TestExecutionInvalidMessageSizeCannotStartRelease refuses corrupt size
// metadata before a mailbox document can be trusted. The batch header must
// bound and frame the exact object bytes that follow it.
func TestExecutionInvalidMessageSizeCannotStartRelease(t *testing.T) {
	for _, tc := range []struct{ name, size string }{
		{"not numeric", "unknown"},
		{"negative", "-1"},
		{"above ceiling", "9223372036854775807"},
		{"shorter than content", "0"},
		{"longer than content", "1000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A corrupt probe read cannot produce an authenticated node
			// report. The first node refuses at the bounded preflight gate.
			rig := newExecutionRig(t, func(cfg *models.File) {
				cfg.Execution.Timeouts = &models.ExecutionTimeoutsConfig{Preflight: 5}
			})
			worker := startWorker(t, rig.repo, executionWorkerConfig(rig.mailbox), 0)
			env, marker := executionBatchReply(t, executionOutputBatchPattern, "",
				`printf '%s blob `+tc.size+`\n{' "$oid"`)
			res := rig.release(env...)
			stopAll(t, []*executionWorker{worker})
			require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			require.FileExists(t, marker, "a signed-message read reached the corrupt batch header")
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionRefusalCode))
			assert.Zero(t, buildRuns(rig.repo))
			assert.Empty(t, rig.repo.TagList())
		})
	}
}
