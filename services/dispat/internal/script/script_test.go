package script

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireShell(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available", name)
	}
}

func TestShellRunnerDefault(t *testing.T) {
	requireShell(t, "/bin/sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{}
	err := r.Run(context.Background(), t.TempDir(), "echo $DISPAT_PACKAGE", []string{"DISPAT_PACKAGE=core"}, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, "core\n", out.String(), "env must reach the script")
}

func TestShellTraceOmitsLiteralCommandCredentials(t *testing.T) {
	requireShell(t, "/bin/sh")
	var logs, out bytes.Buffer
	r := &ShellRunner{Log: zerolog.New(&logs)}
	require.NoError(t, r.Run(context.Background(), t.TempDir(), "SECRET=private-token true", nil, &out, &out))
	assert.Contains(t, logs.String(), "script finished")
	assert.Contains(t, logs.String(), "commandBytes")
	assert.NotContains(t, logs.String(), "private-token")
}

func TestShellRunnerCustomShell(t *testing.T) {
	requireShell(t, "sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{Shell: []string{"sh", "-c"}}
	err := r.Run(context.Background(), t.TempDir(), "echo custom", nil, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, "custom\n", out.String())
}

func TestShellRunnerFailure(t *testing.T) {
	requireShell(t, "/bin/sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{}
	err := r.Run(context.Background(), t.TempDir(), "exit 3", nil, &out, &errb)
	assert.Error(t, err)
}

func TestRunCancelKillsChildren(t *testing.T) {
	// Cancellation must reach the script's children, not just the shell: the
	// `wait` keeps the shell alive with a sleeping child, and only the
	// process-group signal ends both. Without it, the child keeps the output
	// pipes open and Run would sit out the sleep (or at least the WaitDelay).
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a unix mechanism")
	}
	requireShell(t, "/bin/sh")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	var out, errb bytes.Buffer
	r := &ShellRunner{}
	start := time.Now()
	err := r.Run(ctx, t.TempDir(), "sleep 30 & wait", nil, &out, &errb)
	assert.Error(t, err, "a killed script is an error; the caller classifies it via ctx")
	assert.Less(t, time.Since(start), 3*time.Second,
		"the group signal must end the child promptly; only the shell dying would leave the pipes held")
}

func TestShellRunnerCancellationAllowsTermCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group TERM cleanup is a unix mechanism")
	}
	requireShell(t, "/bin/sh")
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cleaned := filepath.Join(dir, "cleaned")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	readySeen := make(chan bool)
	go func() {
		for {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				readySeen <- true
				return
			}
			select {
			case <-ctx.Done():
				readySeen <- false
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	var output bytes.Buffer
	err := (&ShellRunner{}).Run(ctx, dir,
		"trap 'printf cleaned > cleaned; exit 0' TERM; printf ready > ready; while :; do sleep 1; done",
		nil, &output, &output)
	require.True(t, <-readySeen, "script never reported readiness")
	require.Error(t, err)
	contents, readErr := os.ReadFile(cleaned)
	require.NoError(t, readErr)
	assert.Equal(t, "cleaned", string(contents))
}

func TestRunBackgroundChildDoesNotBlockWait(t *testing.T) {
	// A script that legitimately backgrounds a child and exits succeeds: the
	// child holds the output pipes, WaitDelay stops the wait for them, and
	// the forced pipe close (ErrWaitDelay) is not an error of the script.
	if runtime.GOOS == "windows" {
		t.Skip("relies on sh job control")
	}
	requireShell(t, "/bin/sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{WaitDelay: 500 * time.Millisecond}
	start := time.Now()
	err := r.Run(context.Background(), t.TempDir(), "sleep 5 & echo started", nil, &out, &errb)
	require.NoError(t, err, "the script's own process succeeded")
	assert.Contains(t, out.String(), "started")
	assert.Less(t, time.Since(start), 3*time.Second,
		"Wait must give up on the pipes after WaitDelay instead of sitting out the child")
}
