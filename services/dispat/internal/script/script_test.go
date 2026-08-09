package script

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"

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
