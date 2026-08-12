package app

import (
	"os"
	"strconv"
	"strings"
)

// lockDisableEnv turns the release lock off for one invocation, as the
// config's own unsafeDisableLock does for the repository. It is spelled unsafe
// because it is: a run without the lock is a run that can be started beside
// another one, which is the situation the lock exists to prevent. Both exist
// for the repositories that have no remote to coordinate through at all — a
// scratch clone, a test fixture — where the alternative is not an unsafe
// release but no release.
const lockDisableEnv = "DISPAT_UNSAFE_DISABLE_LOCK"

// lockDisabled reports whether this run releases without the lock. Two places
// can say so, the config file and the environment, and either is enough: the
// file states the repository's situation (it has no remote to coordinate
// through at all), the variable states one invocation's.
func (a *App) lockDisabled() bool {
	return a.cfg.UnsafeDisableLock || lockDisabledByEnv()
}

// lockDisabledByEnv reads the environment kill switch. Only a value that
// plainly parses as true turns the lock off; unset, empty and anything that
// makes no sense leave it on, because a typo in an environment variable is not
// consent to release unguarded.
func lockDisabledByEnv() bool {
	raw, ok := os.LookupEnv(lockDisableEnv)
	if !ok {
		return false
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return value
}

// pushRemote is the remote this repository coordinates through: what the
// release commit and tags are pushed to, and what the release lock is taken
// on. commit.remote names it; unset it is git's own default name.
//
// Nil-safe, because the lock is taken whether or not the release commit is
// configured at all.
func (a *App) pushRemote() string {
	if a.cfg.Commit != nil && a.cfg.Commit.Remote != "" {
		return a.cfg.Commit.Remote
	}
	return "origin"
}
