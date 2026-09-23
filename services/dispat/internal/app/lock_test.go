package app

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// What the lock does around a release is a black-box claim and lives in
// tests/integration/lock_test.go; what a release decides before taking one is
// here.

// TestLockKillSwitchDefaultsToLocked: the variable has to be spelled out to be
// believed. Everything else — unset, empty, a typo, a value that means nothing
// — leaves the lock on, because releasing unguarded is not a state to fall
// into by accident.
func TestLockKillSwitchDefaultsToLocked(t *testing.T) {
	for name, tc := range map[string]struct {
		value    string
		unset    bool
		disabled bool
	}{
		"unset":                     {unset: true},
		"true disables it":          {value: "true", disabled: true},
		"TRUE, however it is typed": {value: "TRUE", disabled: true},
		"1 disables it":             {value: "1", disabled: true},
		"padded, as a YAML job is":  {value: " true ", disabled: true},
		"false keeps it":            {value: "false"},
		"0 keeps it":                {value: "0"},
		"empty keeps it":            {value: ""},
		"a typo keeps it":           {value: "ture"},
		"a sentence keeps it":       {value: "yes please"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(lockDisableEnv, tc.value) // registers the restore either way
			if tc.unset {
				require.NoError(t, os.Unsetenv(lockDisableEnv))
			}
			assert.Equal(t, tc.disabled, lockDisabledByEnv())
		})
	}
}

// TestLockIsOffWhenEitherSwitchSaysSo: the config states the repository's
// situation and the variable states this invocation's, so neither can be
// overridden by the other being quiet. Only both saying nothing keeps the
// lock on, which is the default a repository gets without ever mentioning it.
func TestLockIsOffWhenEitherSwitchSaysSo(t *testing.T) {
	for name, tc := range map[string]struct {
		config   bool
		env      string
		disabled bool
	}{
		"neither":              {},
		"the config alone":     {config: true, disabled: true},
		"the variable alone":   {env: "true", disabled: true},
		"both":                 {config: true, env: "true", disabled: true},
		"config on, env false": {config: true, env: "false", disabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(lockDisableEnv, tc.env)
			if tc.env == "" {
				require.NoError(t, os.Unsetenv(lockDisableEnv))
			}
			a := &App{cfg: &config.File{UnsafeDisableLock: tc.config}}
			assert.Equal(t, tc.disabled, a.lockDisabled())
		})
	}
}

// TestPushRemoteResolution: one remote answers both questions — where the
// release commit goes and where the lock is taken — so it is resolved in one
// place. The nil case is not hypothetical: the lock is taken by a repository
// that configures no release commit at all.
func TestPushRemoteResolution(t *testing.T) {
	for name, tc := range map[string]struct {
		commit *config.CommitConfig
		want   string
	}{
		"no commit configured": {nil, "origin"},
		"no remote named":      {&config.CommitConfig{}, "origin"},
		"a named remote":       {&config.CommitConfig{Remote: "upstream"}, "upstream"},
	} {
		t.Run(name, func(t *testing.T) {
			a := &App{cfg: &config.File{Commit: tc.commit}}
			assert.Equal(t, tc.want, a.pushRemote())
		})
	}
}

// TestReleaseLocksNameTheDistributedRun: the run id a distributed run was
// named with before it locked anything reaches the lock tag on both
// acquisition paths, a single history's and a fleet's, because the lock is
// the documented place an abandoned run is found from (CCME §28.6). A run
// that delegates nothing has no run id and writes no run line.
func TestReleaseLocksNameTheDistributedRun(t *testing.T) {
	t.Setenv(lockDisableEnv, "")
	require.NoError(t, os.Unsetenv(lockDisableEnv))
	const run = "6f1a9f0d2b90c8f96f1a9f0d2b90c8f9"

	t.Run("a single history", func(t *testing.T) {
		for name, runID := range map[string]string{"distributed": run, "local": ""} {
			t.Run(name, func(t *testing.T) {
				root, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
				origin := t.TempDir()
				out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput()
				require.NoError(t, err, "%s", out)
				recordGit(t, root, "remote", "add", "origin", origin)
				a.runID = runID

				_, unlock, err := a.acquireReleaseLocks(t.Context())
				require.NoError(t, err)
				message, err := a.git.RemoteTagMessage(t.Context(), "origin", gitx.LockTagName)
				require.NoError(t, err)
				require.NoError(t, unlock())

				if runID == "" {
					assert.NotContains(t, message, "\nrun ")
					return
				}
				assert.Contains(t, message, "\nrun "+run+"\n")
			})
		}
	})

	t.Run("a fleet", func(t *testing.T) {
		w, _ := recordFixture(t, false, false)
		source := w.byName["source"]
		w.app.cfg.UnsafeDisableLock = false
		source.repo.Config.UnsafeDisableLock = false
		w.ordered = []*repositoryRecord{source}
		w.app.runID = run

		unlock, err := w.acquire(t.Context())
		require.NoError(t, err)
		message, err := source.git.RemoteTagMessage(t.Context(), "origin", gitx.LockTagName)
		require.NoError(t, err)
		require.NoError(t, unlock())
		assert.Contains(t, message, "\nrun "+run+"\n")
	})
}
