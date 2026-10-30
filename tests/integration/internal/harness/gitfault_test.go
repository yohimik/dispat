package harness

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitFaultChangedFilesReplyConsumesItsRequest(t *testing.T) {
	// Larger than the pipe buffer: a successful early reply cannot hide the
	// writer's broken pipe behind scheduling that happens to accept a tiny query.
	input := strings.Repeat(strings.Repeat("a", 40)+"\n", 32768)
	for name, tc := range map[string]struct{ output, firstInputLine string }{
		"text":                {output: " "},
		"framed bytes":        {output: "\x1einvalid\x1f\x00"},
		"selected first line": {output: " ", firstInputLine: strings.Repeat("a", 40)},
	} {
		t.Run(name, func(t *testing.T) {
			fault := NewGitFault(t, GitFault{Pattern: "*log --no-walk=unsorted --stdin*", Output: tc.output,
				FirstInputLine: tc.firstInputLine})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", `exec git "$@"`, "sh",
				"-c", "maintenance.autoDetach=false", "-C", t.TempDir(), "log", "--no-walk=unsorted", "--stdin")
			cmd.WaitDelay = time.Second
			cmd.Env = append(os.Environ(), fault.Env()...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			stdin, err := cmd.StdinPipe()
			require.NoError(t, err)
			require.NoError(t, cmd.Start())
			// Observe the write itself: os/exec may ignore an EPIPE from its
			// own stdin copier, whereas LocalGitx retains that causal error.
			written, writeErr := io.WriteString(stdin, input)
			closeErr := stdin.Close()
			waitErr := cmd.Wait()
			assert.NoError(t, writeErr, "the canned reply must consume the request before exiting")
			assert.Equal(t, len(input), written)
			assert.NoError(t, closeErr)
			require.NoError(t, waitErr)
			assert.Equal(t, tc.output, stdout.String(), "the caller receives the exact malformed reply")
			assert.Empty(t, stderr.String())
			assert.Equal(t, 1, fault.Matches())
		})
	}
}

func TestGitFaultRepliesDoNotWaitForUnrelatedInput(t *testing.T) {
	for name, tc := range map[string]struct {
		args      []string
		isFailure bool
	}{
		"no stdin":             {args: []string{"--version"}},
		"interactive batch":    {args: []string{"cat-file", "--batch"}},
		"path names an option": {args: []string{"log", "--", "--stdin"}},
		"directory names log":  {args: []string{"-C", "log", "cat-file", "--batch"}},
		"config names log":     {args: []string{"-c", "log", "cat-file", "--batch"}},
		"failed listing":       {args: []string{"log", "--no-walk=unsorted", "--stdin"}, isFailure: true},
	} {
		t.Run(name, func(t *testing.T) {
			injected := GitFault{Pattern: "*", Output: "replacement"}
			if tc.isFailure {
				injected.Output, injected.Code = "", 7
			}
			fault := NewGitFault(t, injected)
			read, write, err := os.Pipe()
			require.NoError(t, err)
			defer read.Close()
			defer write.Close() // Stay open until after the command answers.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", `exec git "$@"`, "sh")
			cmd.WaitDelay = time.Second
			cmd.Args = append(cmd.Args, tc.args...)
			cmd.Env = append(os.Environ(), fault.Env()...)
			cmd.Stdin = read
			output, err := cmd.CombinedOutput()
			if tc.isFailure {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, injected.Code, exitErr.ExitCode(), "a failed read must retain its immediate failure")
				assert.Contains(t, string(output), GitFaultMarker)
				return
			}
			require.NoError(t, err, "the replacement must not wait for EOF: %s", output)
			assert.Equal(t, "replacement", string(output))
		})
	}
}

// runThroughFault invokes `git` the way the process under test does: by name,
// through a shell that resolves it from the PATH the fault hands out. Naming
// the script's path directly would prove the script works and say nothing
// about whether it is the one a run would find.
func runThroughFault(t testing.TB, fault *GitFault, args ...string) (code int, stdout, stderr string) {
	return runInputThroughFault(t, fault, "", args...)
}

func runInputThroughFault(t testing.TB, fault *GitFault, input string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `exec git "$@"`, "sh")
	cmd.Args = append(cmd.Args, args...)
	cmd.Env = append(os.Environ(), fault.Env()...)
	cmd.Stdin = strings.NewReader(input)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr), "launching the stand-in: %v", err)
		code = exitErr.ExitCode()
	}
	return code, out.String(), errOut.String()
}

func TestGitFaultScopesBatchReadsToTheFirstObject(t *testing.T) {
	fault := NewGitFault(t, GitFault{Pattern: "*cat-file --batch", Nth: 2, Onward: true})
	root := t.TempDir()
	git := func(input string, args ...string) string {
		cmd := exec.Command(fault.realGit, append([]string{"-C", root}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return string(out)
	}
	git("", "init", "--bare", "-q")
	messageOID := strings.TrimSpace(git("signed message", "hash-object", "-w", "--stdin"))
	outputOID := strings.TrimSpace(git("build output", "hash-object", "-w", "--stdin"))
	fault.FirstInputLine = outputOID
	args := []string{"-C", root, "cat-file", "--batch"}
	for _, input := range []string{messageOID + "\n" + outputOID + "\n", messageOID, ""} {
		code, stdout, stderr := runInputThroughFault(t, fault, input, args...)
		assert.Zero(t, code)
		assert.Equal(t, git(input, "cat-file", "--batch"), stdout,
			"a nonmatch replays the entire request stream byte for byte")
		assert.Empty(t, stderr)
		assert.Zero(t, fault.Matches(), "an unrelated first object consumes no ordinal")
	}
	input := outputOID + "\n" + messageOID + "\n"
	code, stdout, stderr := runInputThroughFault(t, fault, input, args...)
	assert.Zero(t, code)
	assert.Equal(t, git(input, "cat-file", "--batch"), stdout,
		"an unselected matching ordinal also preserves both requests")
	assert.Empty(t, stderr)
	for ordinal := 2; ordinal <= 3; ordinal++ {
		code, stdout, stderr = runInputThroughFault(t, fault, input, args...)
		assert.Equal(t, 1, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, GitFaultMarker)
		assert.Equal(t, ordinal, fault.Matches(), "Onward counts only scoped object matches")
	}
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
		{name: "from the first match through the second",
			fault:  GitFault{Pattern: "*--version*", Nth: 1, Onward: true, Through: 2},
			failed: []int{1, 2}, code: 1},
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

	t.Run("a chain hands what its first fault runs to its second", func(t *testing.T) {
		first, second := NewGitFaultChain(t,
			GitFault{Pattern: "*--version*", Nth: 1, After: true, Output: "lost\n"},
			GitFault{Pattern: "*--version*", Nth: 2, Code: 9})
		code, stdout, stderr := runThroughFault(t, first, "--version")
		assert.Equal(t, 0, code, "the first fault ran the command through the second and lost its answer")
		assert.Equal(t, "lost\n", stdout)
		assert.NotContains(t, stderr, GitFaultMarker)

		code, _, stderr = runThroughFault(t, first, "--version")
		assert.Equal(t, 9, code, "the second fault fails the second match it sees")
		assert.Contains(t, stderr, GitFaultMarker)

		code, stdout, _ = runThroughFault(t, first, "--exec-path")
		assert.Equal(t, 0, code, "an invocation neither names reaches the real Git")
		assert.NotEmpty(t, strings.TrimSpace(stdout))
		assert.Equal(t, 2, first.Matches())
		assert.Equal(t, 2, second.Matches(), "each fault counts what it saw")
	})
}

func TestGitFaultReservesOrdinalsAfterALongPrefix(t *testing.T) {
	const priorMatches = 512
	fault := NewGitFault(t, GitFault{Pattern: "*--version*", Nth: priorMatches + 1, Code: 128})
	for ordinal := 1; ordinal <= priorMatches; ordinal++ {
		require.NoError(t, os.Mkdir(filepath.Join(fault.matches, strconv.Itoa(ordinal)), 0o700))
	}

	code, _, stderr := runThroughFault(t, fault, "--version")
	assert.Equal(t, 128, code, "the first unclaimed ordinal is selected")
	assert.Contains(t, stderr, GitFaultMarker)
	assert.Equal(t, priorMatches+1, fault.Matches())
}

func TestGitFaultConcurrentMatchesReserveDistinctOrdinals(t *testing.T) {
	const invocations = 32
	const selectedOrdinal = 17
	fault := NewGitFault(t, GitFault{Pattern: "*--version*", Nth: selectedOrdinal, Code: 128})
	type outcome struct {
		code   int
		output string
		err    error
	}
	finished := make(chan outcome, invocations)
	faultEnv := fault.Env()
	for range invocations {
		go func() {
			cmd := exec.Command("sh", "-c", `exec git "$@"`, "sh", "--version")
			cmd.Env = append(os.Environ(), faultEnv...)
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				}
			}
			finished <- outcome{code: code, output: string(output), err: err}
		}()
	}

	selected := 0
	for range invocations {
		select {
		case result := <-finished:
			if result.code == 128 {
				selected++
				assert.Contains(t, result.output, GitFaultMarker)
				continue
			}
			require.NoError(t, result.err)
			assert.Contains(t, result.output, "git version")
		case <-time.After(20 * time.Second):
			t.Fatal("concurrent fault invocations did not finish")
		}
	}
	assert.Equal(t, 1, selected, "exactly one process claims the requested ordinal")
	assert.Equal(t, invocations, fault.Matches(), "all concurrent matches reserve one ordinal")
}
