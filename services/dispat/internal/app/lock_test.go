package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
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
