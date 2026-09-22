package cli

// What a process serving somebody else's task may run. The refusal itself is
// driven through the binary in tests/integration/execution_authority_test.go;
// the table of words is decided here.

import (
	"bytes"
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// TestWorkerAuthorityRefusesOnlyTheReleaseWords: a worker's task runs real
// build scripts, and those scripts run dispat. The list is therefore the
// smallest one that holds: the release and every command that writes a native
// release ref, with every helper a build legitimately uses left alone. A
// webhook event is one of those helpers: a build that reports its progress
// reports it from whichever machine runs it.
func TestWorkerAuthorityRefusesOnlyTheReleaseWords(t *testing.T) {
	for name, tc := range map[string]struct {
		command   string
		isRefused bool
	}{
		"the release":             {command: cmdRelease, isRefused: true},
		"a release commit":        {command: cmdCommit, isRefused: true},
		"a GitHub release":        {command: cmdGithub, isRefused: true},
		"a changelog entry":       {command: cmdChangelog, isRefused: true},
		"a version write":         {command: cmdAutoversion, isRefused: true},
		"a computed config":       {command: cmdCompute, isRefused: true},
		"the plan":                {command: cmdStatus},
		"a webhook event":         {command: cmdTrigger},
		"a declared script":       {command: cmdRun},
		"one declared script":     {command: cmdExec},
		"a condition":             {command: cmdIf},
		"a loop":                  {command: cmdFor},
		"the notes preview":       {command: cmdPreview},
		"a manifest read":         {command: cmdScanner},
		"a manifest write":        {command: cmdWriter},
		"a literal replacement":   {command: cmdReplacer},
		"a manifest sweep":        {command: cmdAutowriter},
		"a replacement sweep":     {command: cmdAutoreplacer},
		"a tool install":          {command: cmdInstall},
		"a starter config":        {command: cmdInit},
		"a message diagnosis":     {command: cmdDiagnostics},
		"an update of the binary": {command: cmdSelfUpdate},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.isRefused, isRefusedUnderWorkerAuthority(tc.command))
		})
	}
}

// TestWorkerAuthorityGuardReportsTheRefusal: the guard is silent for every
// invocation that is not one, and the invocation it stops exits non-zero with
// both names for the refusal on one line.
func TestWorkerAuthorityGuardReportsTheRefusal(t *testing.T) {
	guard := func(t *testing.T, authority, command string) (int, bool, string) {
		t.Helper()
		t.Setenv(execution.AuthorityEnv, authority)
		if authority == "" {
			require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
		}
		var logs bytes.Buffer
		r := &runner{inv: invocation{cmd: command}}
		code, isRefused := r.refuseWorkerAuthority(zerolog.New(&logs))
		return code, isRefused, logs.String()
	}

	t.Run("no authority at all", func(t *testing.T) {
		code, isRefused, logs := guard(t, "", cmdRelease)
		assert.Equal(t, 0, code)
		assert.False(t, isRefused)
		assert.Empty(t, logs)
	})

	t.Run("worker authority and an allowed command", func(t *testing.T) {
		code, isRefused, logs := guard(t, execution.WorkerAuthority, cmdStatus)
		assert.Equal(t, 0, code)
		assert.False(t, isRefused)
		assert.Empty(t, logs)
	})

	t.Run("worker authority and the release", func(t *testing.T) {
		code, isRefused, logs := guard(t, execution.WorkerAuthority, cmdRelease)
		assert.Equal(t, 1, code)
		assert.True(t, isRefused)
		assert.Contains(t, logs, `"code":"`+execution.CodeAuthority+`"`)
		assert.Contains(t, logs, `"category":"`+execution.CategoryAuthority+`"`)
		assert.Contains(t, logs, `"command":"release"`)
	})
}

// TestWorkerFlagShapeIsAUsageError: a `--worker` value is name=endpoint with
// both halves stated, and anything else is refused before any file is read,
// without echoing the value, whose second half may be carrying a credential.
func TestWorkerFlagShapeIsAUsageError(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		"no separator":     "build-a",
		"an empty name":    "=file:///srv/mailbox",
		"an empty mailbox": "build-a=",
		"nothing at all":   "",
		"a bare separator": "=",
	} {
		for _, command := range []string{cmdRelease, cmdRun, cmdStatus} {
			t.Run(name+" on "+command, func(t *testing.T) {
				args := []string{command, "--root", root, "--worker", value}
				if command == cmdRun {
					args = append(args, "tests")
				}
				var stdout, stderr bytes.Buffer
				code := Run(args, &stdout, &stderr)
				assert.Equal(t, 2, code, "stderr:\n%s", stderr.String())
				assert.Contains(t, stderr.String(), "name=endpoint")
			})
		}
	}
}

// TestWorkerFlagEchoesNoEndpoint: a malformed value is refused by its shape
// alone, so a mistyped endpoint carrying a credential never reaches a log.
func TestWorkerFlagEchoesNoEndpoint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"status", "--root", t.TempDir(), "--worker", "https://user:hunter2@example.com/m.git"},
		&stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.NotContains(t, stderr.String(), "hunter2")
}

// TestWorkerFlagBelongsToTheDispatchingCommands: the flag names a node an
// invocation may dispatch to, so it is a flag of release, run and status and
// of nothing else. A command that dispatches nothing refuses it as another
// command's flag.
func TestWorkerFlagBelongsToTheDispatchingCommands(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"preview", "--worker", "a=file:///m"},
		{"worker", "--worker", "a=file:///m"},
		{"exec", "build", "--worker", "a=file:///m"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(append(args, "--root", root), &stdout, &stderr)
		assert.Equal(t, 2, code, "args: %v\nstderr:\n%s", args, stderr.String())
		assert.Contains(t, stderr.String(), "--worker", "args: %v", args)
	}
}

// TestWorkerFlagIsRefusedUnderWorkerAuthority: a task executes what its
// assignment authorized, so a build script that named a pool of its own is
// refused with the authority code before any file is read, whichever of the
// three commands it tried.
func TestWorkerFlagIsRefusedUnderWorkerAuthority(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, execution.WorkerAuthority)
	root := t.TempDir()
	for _, command := range []string{cmdRun, cmdStatus} {
		t.Run(command, func(t *testing.T) {
			args := []string{command, "--root", root, "--worker", "a=file:///srv/mailbox", "--log-format", "json"}
			if command == cmdRun {
				args = append(args, "tests")
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, &stdout, &stderr)
			assert.Equal(t, 1, code, "stderr:\n%s", stderr.String())
			assert.Contains(t, stderr.String(), `"code":"`+execution.CodeAuthority+`"`)
			assert.Contains(t, stderr.String(), `"category":"`+execution.CategoryAuthority+`"`)
		})
	}
}
