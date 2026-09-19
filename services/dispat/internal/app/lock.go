package app

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// codeLockDisabled reports the explicit unsafe lock bypass.
//
// It is a warning of the polyrepository family, beside W330, rather than the
// E336 it used to borrow: E336 is the failure to coordinate, and a bypass
// coordinates nothing on purpose. A nonfatal condition keeps a W code so a CI
// filter acting on errors does not have to special-case it.
const codeLockDisabled = "W331"

// warnLockDisabled reports the effective scope of the bypass: every repository
// releasing without its remote lock in this run, and which setting asked for
// it. One line for the whole run, because the scope is the thing being
// reported and a per-repository line hides how far it reaches.
//
// A composed workspace names its repositories by their `.gitmodules`
// identities. A single repository has no such identity, so it names itself by
// the root the run was given, which is what a reader has to recognise it by.
func warnLockDisabled(log zerolog.Logger, repositories []string, byConfig bool) {
	var settings []string
	if byConfig {
		settings = append(settings, "unsafeDisableLock")
	}
	if lockDisabledByEnv() {
		settings = append(settings, lockDisableEnv)
	}
	log.Warn().Str("code", codeLockDisabled).Strs("repositories", repositories).
		Strs("setting", settings).
		Msg("UNSAFE: releasing without the release lock; a concurrent release of these repositories cannot be prevented")
}

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

// lockDestinationResolver is the slice of git the lock destination needs.
type lockDestinationResolver interface {
	RemotePushURL(ctx context.Context, remote string) (string, error)
}

// lockDestination resolves the exact URL the release lock is taken on, so a
// cleanup after the run cannot follow a remote somebody re-pointed mid-release
// and delete a lock at a different destination.
//
// A remote Git cannot resolve by name is used exactly as configured: an
// unconfigured remote and a URL written straight into commit.remote both land
// here, and the push is then the authority — its refusal is the message the
// operator reads, rather than a resolution failure that never mentions the
// lock. The one answer that is refused is an ambiguous remote: a lock pushed
// to two destinations exists in both and coordinates neither.
func lockDestination(ctx context.Context, git lockDestinationResolver, remote string, log zerolog.Logger) (string, error) {
	resolved, err := git.RemotePushURL(ctx, remote)
	if err == nil {
		return resolved, nil
	}
	if errors.Is(err, gitx.ErrAmbiguousPushDestination) {
		return "", err
	}
	log.Debug().Err(err).Str("remote", gitx.RedactURL(remote)).
		Msg("the release lock uses the configured remote as written")
	return remote, nil
}
