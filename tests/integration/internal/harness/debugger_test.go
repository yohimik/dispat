package harness

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunBinCommandUsesDebuggerOnlyForOptedInRelease(t *testing.T) {
	const (
		binary = "/test/dispat"
		root   = "/test/repo"
	)
	releaseArgs := []string{testDebuggerRelease, "--root", root}

	t.Setenv(testDebuggerEnv, "")
	require.Equal(t, append([]string{binary}, releaseArgs...), runBinCommand(binary, releaseArgs).Args)

	t.Setenv(testDebuggerEnv, testDebuggerGDB)
	require.Equal(t, []string{binary, "worker", "--root", root},
		runBinCommand(binary, []string{"worker", "--root", root}).Args)

	debugged := runBinCommand(binary, releaseArgs)
	require.Equal(t, testDebuggerGDB, debugged.Args[testDebuggerFirstArg])
	require.Equal(t, releaseArgs, debugged.Args[len(debugged.Args)-len(releaseArgs):])
	require.Contains(t, debugged.Args, testDebuggerSignalPipe)
	require.Contains(t, debugged.Args, testDebuggerGCSignals)
	require.Contains(t, debugged.Args, fmt.Sprintf(testDebuggerFaultTraceFormat, testDebuggerFaultTrace))
	require.Contains(t, debugged.Args, testDebuggerQuietThreads)

	require.Equal(t, []string{binary}, runBinCommand(binary, nil).Args)
	t.Setenv(testDebuggerEnv, "lldb")
	require.Equal(t, append([]string{binary}, releaseArgs...), runBinCommand(binary, releaseArgs).Args)
}
