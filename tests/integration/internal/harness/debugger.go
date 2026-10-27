package harness

import (
	"os"
	"os/exec"
)

const (
	testDebuggerEnv         = "DISPAT_TEST_DEBUGGER"
	testDebuggerGDB         = "gdb"
	testDebuggerRelease     = "release"
	testDebuggerFirstArg    = 0
	testDebuggerNoArgs      = 0
	testDebuggerBatch       = "--batch"
	testDebuggerChildResult = "--return-child-result"
	testDebuggerQuiet       = "-q"
	testDebuggerExecute     = "-ex"
	testDebuggerLogToStderr = "set logging file /dev/stderr"
	testDebuggerRedirectLog = "set logging redirect on"
	testDebuggerEnableLog   = "set logging enabled on"
	testDebuggerSignalPipe  = "handle SIGPIPE nostop noprint pass"
	testDebuggerGCSignals   = "handle SIG40 SIG41 nostop noprint pass"
	testDebuggerRun         = "run"
	testDebuggerBacktrace   = "thread apply all bt"
	testDebuggerSignalInfo  = "p $_siginfo"
	testDebuggerChildArgs   = "--args"
)

// runBinCommand traces only synchronous release calls when explicitly enabled
// by the test process. The pinned TinyGo Boehm GC uses SIG40 and SIG41 to
// coordinate threads; passing them through lets gdb stop on an actual fault.
// GDB writes its diagnostics to stderr so the child's event stream on stdout
// remains exact. Workers and interruptible Proc calls retain their usual
// signal semantics.
func runBinCommand(bin string, args []string) *exec.Cmd {
	if os.Getenv(testDebuggerEnv) != testDebuggerGDB ||
		len(args) == testDebuggerNoArgs || args[testDebuggerFirstArg] != testDebuggerRelease {
		return exec.Command(bin, args...)
	}
	debuggerArgs := []string{
		testDebuggerBatch, testDebuggerChildResult, testDebuggerQuiet,
		testDebuggerExecute, testDebuggerLogToStderr,
		testDebuggerExecute, testDebuggerRedirectLog,
		testDebuggerExecute, testDebuggerEnableLog,
		testDebuggerExecute, testDebuggerSignalPipe,
		testDebuggerExecute, testDebuggerGCSignals,
		testDebuggerExecute, testDebuggerRun,
		testDebuggerExecute, testDebuggerBacktrace,
		testDebuggerExecute, testDebuggerSignalInfo,
		testDebuggerChildArgs, bin,
	}
	return exec.Command(testDebuggerGDB, append(debuggerArgs, args...)...)
}
