package harness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runThroughFault invokes `git` the way the process under test does: by name,
// through a shell that resolves it from the PATH the fault hands out. Naming
// the script's path directly would prove the script works and say nothing
// about whether it is the one a run would find.
func runThroughFault(t testing.TB, fault *GitFault, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `exec git "$@"`, "sh")
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), fault.Env()...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr), "launching the stand-in: %v", err)
		code = exitErr.ExitCode()
	}
	return code, out.String(), errOut.String()
}

// TestGitFaultSelectsMatchesAndPassesTheRestThrough: the stand-in is the real
// Git for everything the scenario did not ask about, fails exactly the
// selected matches with the code it was given, counts every match whether it
// failed it or not, and can answer with a reply of its own instead.
func TestGitFaultSelectsMatchesAndPassesTheRestThrough(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  GitFault
		failed []int
		code   int
	}{
		{name: "every match", fault: GitFault{Pattern: "*--version*", Code: 7},
			failed: []int{1, 2, 3}, code: 7},
		{name: "only the second match", fault: GitFault{Pattern: "*--version*", Code: 128, Nth: 2},
			failed: []int{2}, code: 128},
		{name: "from the second match onward", fault: GitFault{Pattern: "*--version*", Nth: 2, Onward: true},
			failed: []int{2, 3}, code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fault := NewGitFault(t, tc.fault)
			var failed []int
			for attempt := 1; attempt <= 3; attempt++ {
				code, stdout, stderr := runThroughFault(t, fault, "--version")
				if code == 0 {
					assert.Contains(t, stdout, "git version", "the real git answered attempt %d", attempt)
					assert.NotContains(t, stderr, GitFaultMarker)
					continue
				}
				failed = append(failed, attempt)
				assert.Equal(t, tc.code, code, "attempt %d", attempt)
				assert.Contains(t, stderr, GitFaultMarker, "attempt %d is recognisably the stand-in", attempt)
			}
			assert.Equal(t, tc.failed, failed)
			assert.Equal(t, 3, fault.Matches(), "every matching invocation is counted")

			// An invocation the glob does not name is the real Git's, and is
			// not counted at all.
			code, stdout, _ := runThroughFault(t, fault, "--exec-path")
			assert.Equal(t, 0, code)
			assert.NotEmpty(t, strings.TrimSpace(stdout))
			assert.Equal(t, 3, fault.Matches())
		})
	}

	t.Run("a reply in place of git", func(t *testing.T) {
		fault := NewGitFault(t, GitFault{Pattern: "*--version*", Output: "not a version at all"})
		code, stdout, stderr := runThroughFault(t, fault, "--version")
		assert.Equal(t, 0, code)
		assert.Equal(t, "not a version at all", stdout)
		assert.NotContains(t, stderr, GitFaultMarker)
		assert.Equal(t, 1, fault.Matches())
	})

	t.Run("nothing counts before the arming invocation", func(t *testing.T) {
		fault := NewGitFault(t, GitFault{Pattern: "*--version*", Nth: 1, ArmAfter: "*--exec-path*"})
		code, stdout, stderr := runThroughFault(t, fault, "--version")
		assert.Equal(t, 0, code, "a match before the arming invocation is the real git's")
		assert.Contains(t, stdout, "git version")
		assert.NotContains(t, stderr, GitFaultMarker)
		assert.Equal(t, 0, fault.Matches(), "and is not counted")

		code, _, _ = runThroughFault(t, fault, "--exec-path")
		assert.Equal(t, 0, code, "the arming invocation itself runs")

		code, _, stderr = runThroughFault(t, fault, "--version")
		assert.Equal(t, 1, code, "the first match after arming is the one selected")
		assert.Contains(t, stderr, GitFaultMarker)
		code, _, _ = runThroughFault(t, fault, "--version")
		assert.Equal(t, 0, code, "and only that one")
		assert.Equal(t, 2, fault.Matches())
	})

	t.Run("a held command waits for Resume and then runs", func(t *testing.T) {
		fault := NewGitFault(t, GitFault{Pattern: "*--version*", Hold: true})
		type outcome struct {
			code   int
			stdout string
		}
		done := make(chan outcome, 1)
		go func() {
			code, stdout, _ := runThroughFault(t, fault, "--version")
			done <- outcome{code: code, stdout: stdout}
		}()
		require.Eventually(t, fault.IsHeld, 10*time.Second, 10*time.Millisecond, "the invocation never reached the hold")
		select {
		case <-done:
			t.Fatal("a held invocation finished before Resume")
		case <-time.After(300 * time.Millisecond):
		}
		fault.Resume()
		select {
		case finished := <-done:
			assert.Equal(t, 0, finished.code)
			assert.Contains(t, finished.stdout, "git version", "the real command ran once resumed")
		case <-time.After(10 * time.Second):
			t.Fatal("a resumed invocation never finished")
		}
		assert.Equal(t, 1, fault.Matches())
	})

	t.Run("a successful command loses its response", func(t *testing.T) {
		fault := NewGitFault(t, GitFault{Pattern: "*--version*", After: true, Code: 128})
		code, stdout, stderr := runThroughFault(t, fault, "--version")
		assert.Equal(t, 128, code)
		assert.Contains(t, stdout, "git version", "the real command ran before the injected failure")
		assert.Contains(t, stderr, GitFaultMarker)
		assert.Equal(t, 1, fault.Matches())
	})
}
