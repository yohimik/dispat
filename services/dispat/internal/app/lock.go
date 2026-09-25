package app

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/config"
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
func warnLockDisabled(log zerolog.Logger, repositories []string, isByConfig bool) {
	log.Warn().Str("code", codeLockDisabled).Strs("repositories", repositories).
		Strs("setting", lockBypassSettings(isByConfig)).
		Msg("UNSAFE: releasing without the release lock; a concurrent release of these repositories cannot be prevented")
}

// lockBypassSettings names the settings that switched the lock off, which is
// the half of the bypass report that is the same wherever it is made: the
// warning a bypassed release prints, and the refusal a run that would
// dispatch work to other machines makes instead of printing it.
func lockBypassSettings(isByConfig bool) []string {
	var settings []string
	if isByConfig {
		settings = append(settings, "unsafeDisableLock")
	}
	if lockDisabledByEnv() {
		settings = append(settings, lockDisableEnv)
	}
	return settings
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

// lockBypassOf decides whether one repository of a composed workspace
// releases without its remote lock, and whether a configuration said so.
//
// An orchestrated fleet releases under one repository's policy: the control
// configuration is the run's configuration, and its unsafeDisableLock speaks
// for every source. A source's own configuration does not, even when it is
// imported and sets the key: an imported configuration contributes that
// repository's packages and records, not the run's lock policy, and a source
// that could switch its own lock off would take the fleet's exclusion apart
// one repository at a time. What it states is reported as ignored (see
// isLockBypassIgnored). A choreographed peer owns its policy as it owns
// everything else, so the entry's setting speaks for the entry alone and one
// peer cannot unlock another. The environment kill switch is the
// invocation's, and applies to whatever that invocation releases.
//
// It is an App method rather than the lock acquisition's own so that the
// question can be asked before any lock is taken, which is where a run that
// delegates work has to ask it.
func (a *App) lockBypassOf(repository *config.Repository) (isBypassed, isByConfig bool) {
	if !a.workspace.IsLinked() {
		if a.cfg.UnsafeDisableLock {
			return true, true
		}
		return lockDisabledByEnv(), false
	}
	if repository.Config != nil && repository.Config.UnsafeDisableLock {
		return true, true
	}
	return lockDisabledByEnv(), false
}

// isLockBypassIgnored reports whether a repository's own configuration asks
// for the unsafe lock bypass and this run takes its lock anyway: a source of an
// orchestrated fleet whose own configuration sets unsafeDisableLock, where only
// the control configuration and the environment decide.
func (a *App) isLockBypassIgnored(repository *config.Repository) bool {
	if a.workspace.IsLinked() || repository.Control || repository.Config == nil || repository.Config == a.cfg {
		return false
	}
	if !repository.Config.UnsafeDisableLock {
		return false
	}
	isBypassed, _ := a.lockBypassOf(repository)
	return !isBypassed
}

// warnLockBypassIgnored reports, in one line, the sources of an orchestrated
// fleet whose own unsafeDisableLock this run did not honour, so a source that
// expected to release unlocked learns that its lock was taken. It carries no
// W331: that code names repositories releasing WITHOUT their lock, and these
// release with it.
func warnLockBypassIgnored(log zerolog.Logger, repositories []string) {
	log.Warn().Strs("repositories", repositories).
		Strs("setting", []string{"unsafeDisableLock"}).
		Msg("ignored: a source's own unsafeDisableLock does not switch its lock off in an orchestrated fleet; " +
			"only the control configuration or " + lockDisableEnv + " can, so these repositories are locked")
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
// release commit and tags are pushed to, what the release lock is taken on,
// and what a worker link with no endpoint reaches.
func (a *App) pushRemote() string {
	return commitRemote(a.cfg.Commit)
}

// commitRemote is the remote one repository's commit policy names:
// commit.remote, and git's own default name when it names none. It is the one
// answer for a single history and for every repository of a composed
// workspace, so the lock, the records and the coordination branches of one
// repository cannot end up on two different remotes.
//
// Nil-safe, because the lock is taken whether or not the release commit is
// configured at all.
func commitRemote(commit *config.CommitConfig) string {
	if commit != nil && commit.Remote != "" {
		return commit.Remote
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
