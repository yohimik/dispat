package harness

import (
	"fmt"
	"os"
	"os/exec"
)

const (
	testDebuggerEnv              = "DISPAT_TEST_DEBUGGER"
	testDebuggerGDB              = "gdb"
	testDebuggerRelease          = "release"
	testDebuggerFirstArg         = 0
	testDebuggerNoArgs           = 0
	testDebuggerBatch            = "--batch"
	testDebuggerChildResult      = "--return-child-result"
	testDebuggerQuiet            = "-q"
	testDebuggerExecute          = "-ex"
	testDebuggerLogToStderr      = "set logging file /dev/stderr"
	testDebuggerRedirectLog      = "set logging redirect on"
	testDebuggerEnableLog        = "set logging enabled on"
	testDebuggerQuietThreads     = "set print thread-events off"
	testDebuggerSignalPipe       = "handle SIGPIPE nostop noprint pass"
	testDebuggerGCSignals        = "handle SIG40 SIG41 nostop noprint pass"
	testDebuggerRun              = "run"
	testDebuggerFaultTraceFormat = "python exec(%q)"
	testDebuggerFaultTrace       = `for fault in range(4):
 if not gdb.selected_inferior().threads():
  break
 for command in ('info registers', 'x/8i $pc', 'info auxv', 'info proc mappings', 'info proc status', 'thread apply all bt', 'p $_siginfo'):
  try:
   gdb.execute(command)
  except gdb.error as error:
   gdb.write(str(error) + '\n', gdb.STDERR)
 gdb.execute('continue')
`
	testDebuggerChildArgs = "--args"
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
		testDebuggerExecute, testDebuggerQuietThreads,
		testDebuggerExecute, testDebuggerSignalPipe,
		testDebuggerExecute, testDebuggerGCSignals,
		testDebuggerExecute, testDebuggerRun,
		testDebuggerExecute, fmt.Sprintf(testDebuggerFaultTraceFormat, testDebuggerFaultTrace),
		testDebuggerChildArgs, bin,
	}
	return exec.Command(testDebuggerGDB, append(debuggerArgs, args...)...)
}
