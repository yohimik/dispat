// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestPolyrepoInterruptionDuringRecordKeepsNativeRecordsAndSkipsHooks proves
// the post-publication interruption boundary through the real binary. The
// source beforeCommit hook holds the record tail open while the parent gets
// SIGINT. Native source and control records must still finish, but no later
// source hook and no control hook may observe a run the operator interrupted.
func TestPolyrepoInterruptionDuringRecordKeepsNativeRecordsAndSkipsHooks(t *testing.T) {
	source := harness.New(t)
	source.SeedPackage("packages", "lib")
	source.Commit("feat(lib): bootstrap library")

	control := harness.New(t)
	addPolyrepoSource(t, control, "lib-source", "sources/lib", source)
	cfg := polyrepoFile()
	cfg["spaces"] = centralSpaces(map[string]string{"libs": "sources/lib/packages"})
	cfg["changelog"] = map[string]any{"enabled": true}
	cfg["commit"] = map[string]any{
		"enabled": true, "push": true, "remote": "origin", "branch": harness.DefaultBranch,
	}
	cfg["repositoryOverrides"] = map[string]any{
		"lib-source": map[string]any{"commit": map[string]any{
			"enabled": true, "push": true, "remote": "origin", "branch": harness.DefaultBranch,
		}},
	}
	cfg["scripts"] = map[string]any{
		"build":   []string{"echo building"},
		"publish": []string{"echo published > ../../published"},
		"interrupt-record": []string{
			`case "$PWD" in */sources/lib) : > ../../source-before-commit; while [ ! -f ../../continue-record ]; do sleep 0.01; done;; *) : > control-before-commit;; esac`,
		},
		"mark-after-commit": []string{
			`case "$PWD" in */sources/lib) : > ../../source-after-commit;; *) : > control-after-commit;; esac`,
		},
		"mark-post-commit": []string{
			`case "$PWD" in */sources/lib) : > ../../source-post-commit;; *) : > control-post-commit;; esac`,
		},
		"mark-before-push": []string{
			`case "$PWD" in */sources/lib) : > ../../source-before-push;; *) : > control-before-push;; esac`,
		},
		"mark-after-push": []string{
			`case "$PWD" in */sources/lib) : > ../../source-after-push;; *) : > control-after-push;; esac`,
		},
	}
	cfg["run"] = map[string]any{
		"beforeCommit": []string{"interrupt-record"},
		"afterCommit":  []string{"mark-after-commit"},
		"postCommit":   []string{"mark-post-commit"},
		"beforePush":   []string{"mark-before-push"},
		"afterPush":    []string{"mark-after-push"},
	}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure interruptible source records")

	sourceRemote := filepath.Join(t.TempDir(), "source.git")
	control.Git("init", "-q", "--bare", sourceRemote)
	control.Git("-C", sourceRemote, "symbolic-ref", "HEAD", "refs/heads/"+harness.DefaultBranch)
	control.Git("-C", "sources/lib", "remote", "set-url", "origin", sourceRemote)
	control.Git("-C", "sources/lib", "push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	controlRemote := control.AddBareRemote()
	control.Git("push", "-q", "origin", "HEAD:refs/heads/"+harness.DefaultBranch)
	controlBefore := control.Git("rev-parse", "HEAD")
	sourceBefore := control.Git("-C", "sources/lib", "rev-parse", "HEAD")

	proc := control.StartRelease()
	require.Eventually(t, func() bool {
		_, err := os.Stat(control.Path("source-before-commit"))
		return err == nil
	}, 20*time.Second, 20*time.Millisecond, "source beforeCommit did not start")
	proc.Signal(os.Interrupt)
	control.WriteFile("continue-record", "continue\n")
	res := proc.Wait()

	assert.NotZero(t, res.Code, "the interrupted command must report cancellation")
	assert.FileExists(t, control.Path("sources/lib/published"), "publication completed before interruption")
	sourceAfter := control.Git("-C", "sources/lib", "rev-parse", "HEAD")
	controlAfter := control.Git("rev-parse", "HEAD")
	assert.NotEqual(t, sourceBefore, sourceAfter, "the durable source release commit still finishes")
	assert.NotEqual(t, controlBefore, controlAfter, "the durable control checkpoint still finishes")
	assert.Equal(t, sourceAfter, control.Git("-C", "sources/lib", "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, sourceAfter, control.Git("-C", sourceRemote, "rev-parse", "lib@0.1.0^{commit}"))
	assert.Equal(t, sourceAfter, control.Git("-C", sourceRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))
	assert.Equal(t, sourceAfter, control.Git("rev-parse", "HEAD:sources/lib"))
	assert.Equal(t, controlAfter, control.Git("-C", controlRemote, "rev-parse", "refs/heads/"+harness.DefaultBranch))

	assert.FileExists(t, control.Path("source-before-commit"), "the hook that observed cancellation did start")
	for _, marker := range []string{
		"source-after-commit", "source-post-commit", "source-before-push", "source-after-push",
		"control-before-commit", "control-after-commit", "control-post-commit", "control-before-push", "control-after-push",
	} {
		assert.NoFileExists(t, control.Path(marker), "%s must stay silent after interruption", marker)
	}
}
