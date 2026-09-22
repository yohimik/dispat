package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// What a release refuses before it takes a lock is decided here; what the
// refusal costs a real run is in tests/integration/execution_authority_test.go.

// executionSecretEnv is the variable the distributed rows name. It is the
// suite's own namespace so that nothing outside these tests is read.
const executionSecretEnv = "DISPAT_IT_EXECUTION_SECRET"

// executionEntry builds a node with the given execution settings and a logger
// to read its refusals out of.
func executionEntry(t *testing.T, cfg *config.File) (*App, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	return New(t.TempDir(), cfg, zerolog.New(&logs).Level(zerolog.DebugLevel)), &logs
}

// executionWorkers is the one-link configuration the distributed rules are
// varied through.
func executionWorkers() *public.ExecutionConfig {
	return &public.ExecutionConfig{
		SecretEnv: executionSecretEnv,
		Workers:   []public.ExecutionWorkerConfig{{Name: "build-a", Endpoint: "/srv/mailboxes/a.git"}},
	}
}

// TestCheckExecutionEntryRefusals: every state a release may not be started
// in, and every state it may. The environment is set per row because two of
// the four rules read it, and a node that says nothing about execution is the
// row every other one is measured against.
func TestCheckExecutionEntryRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *public.ExecutionConfig
		bypass    bool
		authority string
		secret    string
		noSecret  bool
		code      string
		category  string
		want      string
	}{
		"no execution settings at all": {},
		"an orchestrator with no workers": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleOrchestrator},
		},
		"a node with no workers releasing without the lock": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleOrchestrator},
			bypass:    true,
		},
		"a task running under worker authority": {
			authority: "worker",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "worker authority",
		},
		"a task under worker authority on a node that delegates": {
			execution: executionWorkers(),
			authority: "worker",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "worker authority",
		},
		"a worker node": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "execution.role is \"worker\"",
		},
		"a worker node whose authority says nothing": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			authority: "orchestrator",
			code:      execution.CodeAuthority, category: execution.CategoryAuthority,
			want: "execution.role is \"worker\"",
		},
		"workers and the configured bypass": {
			execution: executionWorkers(),
			bypass:    true,
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: "unsafeDisableLock",
		},
		"workers and an unset secret": {
			execution: executionWorkers(),
			noSecret:  true,
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: executionSecretEnv,
		},
		"workers and an empty secret": {
			execution: executionWorkers(),
			secret:    "",
			code:      execution.CodeConfiguration, category: execution.CategoryConfiguration,
			want: executionSecretEnv,
		},
		"workers, the lock and the secret": {
			execution: executionWorkers(),
			secret:    "hunter2",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(execution.AuthorityEnv, tc.authority)
			if tc.authority == "" {
				require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
			}
			t.Setenv(executionSecretEnv, tc.secret)
			if tc.noSecret {
				require.NoError(t, os.Unsetenv(executionSecretEnv))
			}
			// The bypass rows state it in the configuration, so the
			// invocation's own switch is taken out of every row: what a test
			// of the lock refusal must not depend on is the environment it
			// happened to be started in.
			t.Setenv(lockDisableEnv, "")
			require.NoError(t, os.Unsetenv(lockDisableEnv))
			a, logs := executionEntry(t, &config.File{
				Execution: tc.execution, UnsafeDisableLock: tc.bypass})

			err := a.checkExecutionEntry(runRelease)

			if tc.code == "" {
				require.NoError(t, err)
				assert.Empty(t, logs.String(), "a release nothing refuses says nothing about execution")
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, tc.code, config.DiagnosticCode(err))
			assert.Equal(t, tc.category, execution.DiagnosticCategory(err))
			assert.Contains(t, logs.String(), `"code":"`+tc.code+`"`)
			assert.Contains(t, logs.String(), `"category":"`+tc.category+`"`)
		})
	}
}

// TestCheckExecutionEntryNamesTheBypassedRepositories: the refusal replaces
// the warning a bypassed release would have printed, so it has to carry what
// that warning carried: which repositories release unlocked and which setting
// asked for it.
func TestCheckExecutionEntryNamesTheBypassedRepositories(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	t.Setenv(executionSecretEnv, "hunter2")
	t.Setenv(lockDisableEnv, "true")
	entry := &config.File{Execution: executionWorkers()}
	a, logs := executionEntry(t, entry)
	a.workspace = &config.Workspace{Repositories: []config.Repository{
		{Name: "sdk", Config: entry, Entry: true},
		{Name: "api", Config: &config.File{UnsafeDisableLock: true}},
		{Name: "web", Config: &config.File{}},
	}}

	err := a.checkExecutionEntry(runRelease)

	require.Error(t, err)
	assert.Equal(t, execution.CodeConfiguration, config.DiagnosticCode(err))
	// Every repository is bypassed here, because the environment switch is the
	// invocation's and reaches all of them; the configured one is named as the
	// second setting because api states it for itself.
	assert.Contains(t, logs.String(), `"repositories":["api","sdk","web"]`)
	assert.Contains(t, logs.String(), `"setting":["unsafeDisableLock","`+lockDisableEnv+`"]`)
}

// TestCheckExecutionEntryReportsIgnoredPeerSettings: a peer may carry its own
// execution object, because any peer may be another run's entry. It is never
// consulted, and the one line saying so is what tells a reader that a peer
// calling itself a worker changed nothing here. The entry states its own
// settings by definition, and a source that declares no configuration of its
// own is recorded against the entry's file, so neither is reported.
func TestCheckExecutionEntryReportsIgnoredPeerSettings(t *testing.T) {
	t.Setenv(execution.AuthorityEnv, "")
	require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
	entry := &config.File{Execution: &public.ExecutionConfig{Name: "control"}}
	a, logs := executionEntry(t, entry)
	a.workspace = &config.Workspace{Repositories: []config.Repository{
		{Name: "control", Config: entry, Entry: true},
		{Name: "sdk", Config: &config.File{Execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker}}},
		{Name: "ui", Config: entry},
		{Name: "web", Config: &config.File{}},
		{Name: "docs"},
	}}

	require.NoError(t, a.checkExecutionEntry(runRelease))

	assert.Contains(t, logs.String(),
		`"repository":"sdk","message":"execution settings ignored outside the entry configuration"`)
	for _, quiet := range []string{"control", "ui", "web", "docs"} {
		assert.NotContains(t, logs.String(), `"repository":"`+quiet+`"`,
			"%s states no execution settings of its own", quiet)
	}
}

// TestCheckExecutionEntryRefusesASweepByTheSameRules: a sweep with worker
// links is held to the rules a release is (§28.10), and each refusal says it
// was a sweep that was refused. The bypass is refused although a sweep takes
// no lock, because it states that the repository has no remote to coordinate
// through, and a sweep that dispatches has one.
func TestCheckExecutionEntryRefusesASweepByTheSameRules(t *testing.T) {
	for name, tc := range map[string]struct {
		execution *public.ExecutionConfig
		bypass    bool
		authority string
		want      string
		code      string
	}{
		"a task under worker authority": {
			execution: executionWorkers(), authority: "worker",
			want: "cannot start a sweep", code: execution.CodeAuthority},
		"a worker node": {
			execution: &public.ExecutionConfig{Role: public.ExecutionRoleWorker},
			want:      "a worker cannot start a sweep", code: execution.CodeAuthority},
		"workers and the configured bypass": {
			execution: executionWorkers(), bypass: true,
			want: "none to dispatch a sweep through", code: execution.CodeConfiguration},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(execution.AuthorityEnv, tc.authority)
			if tc.authority == "" {
				require.NoError(t, os.Unsetenv(execution.AuthorityEnv))
			}
			t.Setenv(executionSecretEnv, "hunter2")
			t.Setenv(lockDisableEnv, "")
			require.NoError(t, os.Unsetenv(lockDisableEnv))
			a, logs := executionEntry(t, &config.File{Execution: tc.execution, UnsafeDisableLock: tc.bypass})

			err := a.checkExecutionEntry(runSweep)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, tc.code, config.DiagnosticCode(err))
			assert.Contains(t, logs.String(), `"message":"cannot start sweep"`)
		})
	}
}
