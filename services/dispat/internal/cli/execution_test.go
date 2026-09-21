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
// release ref or announces one, with every helper a build legitimately uses
// left alone.
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
		"a webhook event":         {command: cmdTrigger, isRefused: true},
		"the plan":                {command: cmdStatus},
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
