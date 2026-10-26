//go:build unix

package script

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	strictScriptTestChildFile = "child.pid"
	strictScriptTestReadyFile = "child.ready"
	strictScriptTestGroupFile = "group.pid"
	strictScriptTestBound     = 4 * time.Second
)

func TestStrictShellRunnerStopsTermIgnoringBackgroundChildBeforeReturn(t *testing.T) {
	requireShell(t, "/bin/sh")
	dir := t.TempDir()
	var output bytes.Buffer
	runner := &ShellRunner{RequireStoppedGroup: true}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), strictScriptTestBound)
	defer cancel()
	err := runner.Run(ctx, dir,
		"echo $$ > "+strictScriptTestGroupFile+"; sh -c 'trap \"\" TERM; echo ready > "+strictScriptTestReadyFile+
			"; while :; do sleep 1; done' >/dev/null 2>&1 & echo $! > "+strictScriptTestChildFile+
			"; while test ! -e "+strictScriptTestReadyFile+"; do sleep 0.01; done",
		nil, &output, &output)
	require.Error(t, err, "a shell that leaves a running child cannot complete a strict task")
	require.Less(t, time.Since(started), strictScriptTestBound)
	groupData, readErr := os.ReadFile(filepath.Join(dir, strictScriptTestGroupFile))
	require.NoError(t, readErr)
	groupID, parseErr := strconv.Atoi(strings.TrimSpace(string(groupData)))
	require.NoError(t, parseErr)
	active, checkErr := processGroupActive(groupID)
	if checkErr != nil {
		require.ErrorIs(t, err, ErrProcessGroupUnsettled,
			"an unprovable group state must retain the physical lease")
		return
	}
	if active {
		require.ErrorIs(t, err, ErrProcessGroupUnsettled,
			"a remaining group must keep the task's physical lease")
	} else {
		require.True(t, errors.Is(err, ErrProcessGroupStopped) || errors.Is(err, ErrProcessGroupUnsettled))
	}
}

func TestStrictShellRunnerOrdinaryCommandCompletes(t *testing.T) {
	requireShell(t, "/bin/sh")
	var output bytes.Buffer
	runner := &ShellRunner{RequireStoppedGroup: true}
	require.NoError(t, runner.Run(context.Background(), t.TempDir(), "echo complete", nil, &output, &output))
	require.Equal(t, "complete\n", output.String())
}
