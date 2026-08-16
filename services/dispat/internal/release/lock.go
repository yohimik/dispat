package release

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The release lock: one tag on the remote that says a release is running here.
//
// Two releases of one repository at the same time is not a race dispat can
// win by being careful. Both plan against the same tags, both compute the same
// next versions, both publish, and whichever pushes last decides what the tags
// say. The fix is not to make the race fair but to refuse to enter it: the
// first run to get its tag onto the remote releases, and the second is told to
// come back later.
//
// The remote ref is the whole mechanism. A push that is not forced is the only
// operation git offers whose outcome depends on what another machine already
// did, which makes it the only thing here that can serve as a mutex. Nothing
// about the lock is local state: the tag in this clone is scratch, and every
// question about who holds the lock is answered by the remote.

// LockTagName is the ref every release contends for. It is never read back as
// a release tag: gitx reserves the name and drops it from every package's
// history, however broad the tag format, and an alias format cannot produce
// it, since an alias must carry a version placeholder.
const LockTagName = gitx.LockTagName

// LockRemedy is what to do about a lock that will not budge, carried on both
// the failure to take it and the failure to give it back. Both readers of
// those events are looking at the same tag and have the same one thing to
// decide: whether anything really is releasing.
const LockRemedy = "another release may hold it; if you are sure nothing else is releasing, " +
	"delete the tag on the remote (git push <remote> --delete " + LockTagName + ") and run again"

// LockGit is the slice of git the lock needs. *gitx.CLI satisfies it.
//
// Note what is missing: nothing here can force a push. Taking the lock has to
// be able to fail, so the one operation that would make it always succeed is
// deliberately out of reach.
type LockGit interface {
	CreateTagForce(ctx context.Context, name, message, target string) error
	PushTag(ctx context.Context, remote, name string) error
	DeleteTag(ctx context.Context, name string) error
	DeleteRemoteTag(ctx context.Context, remote, name string) error
}

// Lock is one run's claim on the repository. Acquire it before anything the
// run cannot take back, and release it when the run is over, whatever the
// outcome:
//
//	lock := &release.Lock{Git: git, Remote: remote, Log: log}
//	if err := lock.Acquire(ctx); err != nil {
//		return err
//	}
//	defer lock.Release(context.WithoutCancel(ctx))
//
// The zero value is not usable: Git and Remote are required.
type Lock struct {
	Git    LockGit
	Remote string
	Log    zerolog.Logger

	// held records that this run, and not some earlier one, put the tag on the
	// remote. Release does nothing without it, because deleting a lock nobody
	// here took is deleting somebody else's.
	held bool
}

// Acquire claims the lock, or reports why it could not.
//
// Every failure means the same thing to the caller — do not release — so they
// are not distinguished. A rejected push is a lock somebody else holds; an
// unreachable remote, a missing one, or a ref rule that forbids the tag are
// all a remote this run cannot use to coordinate with the next one, and
// releasing without coordination is the thing being prevented.
func (l *Lock) Acquire(ctx context.Context) error {
	// Force locally, and only locally. The tag in this clone may be a leftover
	// from a run that was killed before it could clean up, and that leftover
	// says nothing about who holds the lock; the push below is what asks.
	if err := l.Git.CreateTagForce(ctx, LockTagName, lockMessage(), "HEAD"); err != nil {
		return fmt.Errorf("creating the release lock tag: %w", err)
	}
	if err := l.Git.PushTag(ctx, l.Remote, LockTagName); err != nil {
		// The local tag was this attempt's, so it goes with the attempt. Left
		// behind it would be read as a lock this clone holds by anyone looking
		// at `git tag`, which is exactly the wrong thing to suggest.
		if derr := l.Git.DeleteTag(ctx, LockTagName); derr != nil {
			l.Log.Debug().Err(derr).Str("tag", LockTagName).
				Msg("could not remove the local lock tag after a failed push")
		}
		if holder := l.describeHolder(ctx); holder != "" {
			return fmt.Errorf("pushing the release lock tag to %s (%s): %w", l.Remote, holder, err)
		}
		return fmt.Errorf("pushing the release lock tag to %s: %w", l.Remote, err)
	}
	l.held = true
	l.Log.Debug().Str("tag", LockTagName).Str("remote", l.Remote).Msg("release lock acquired")
	return nil
}

// Release gives the lock back: the tag goes from the remote first, then from
// here.
//
// It reports failures and returns nothing, because it runs when the release is
// over and there is no decision left for a caller to make. A tag left on the
// remote costs the *next* run, which refuses and says why, and this is the
// event that explains to whoever reads the log how that happened.
//
// Call it with a context detached from cancellation (context.WithoutCancel):
// an interrupted run has as much reason to unlock as a finished one, and more,
// since nobody is watching to do it by hand.
func (l *Lock) Release(ctx context.Context) {
	if !l.held {
		return
	}
	// Whatever happens below, this run has stopped claiming the lock: a second
	// call must not try again, and a failure here is not retried.
	l.held = false
	if err := l.Git.DeleteRemoteTag(ctx, l.Remote, LockTagName); err != nil {
		l.Log.Error().Err(err).Str("tag", LockTagName).Str("remote", l.Remote).
			Str("remedy", LockRemedy).Msg("could not remove the release lock tag from the remote")
	}
	if err := l.Git.DeleteTag(ctx, LockTagName); err != nil {
		l.Log.Error().Err(err).Str("tag", LockTagName).
			Msg("could not remove the local release lock tag")
	}
}

// lockInspector is the optional capability behind the holder line in a
// refusal: reading the remote lock tag's message. *gitx.CLI has it; the
// narrower fakes in tests do not, and the refusal reads the same without it.
type lockInspector interface {
	RemoteTagMessage(ctx context.Context, remote, name string) (string, error)
}

// describeHolder turns the remote lock tag's message into "held for 3h12m by
// host ci-7 pid 4242", or nothing when the message cannot be read or parsed.
// The refusal is already correct without it; this is the difference between
// "somebody holds the lock" and knowing whether that somebody is still alive.
func (l *Lock) describeHolder(ctx context.Context) string {
	insp, ok := l.Git.(lockInspector)
	if !ok {
		return ""
	}
	msg, err := insp.RemoteTagMessage(ctx, l.Remote, LockTagName)
	if err != nil || msg == "" {
		return ""
	}
	var host, pid string
	var at time.Time
	for _, line := range strings.Split(msg, "\n") {
		switch {
		case strings.HasPrefix(line, "host "):
			host = strings.TrimPrefix(line, "host ")
		case strings.HasPrefix(line, "pid "):
			pid = strings.TrimPrefix(line, "pid ")
		case strings.HasPrefix(line, "at "):
			at, _ = time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "at "))
		}
	}
	if host == "" || at.IsZero() {
		return ""
	}
	return fmt.Sprintf("held for %s by host %s pid %s", time.Since(at).Round(time.Second), host, pid)
}

// lockMessage is the body of the lock tag, and the reason two runs can never
// write the same tag object.
//
// A git tag object is identified by its target, its tagger, its date to the
// second and its message. Two runs tagging the same commit in the same second
// under the same identity with the same message would produce one object, and
// pushing an object the remote already has under a ref it already has is not a
// rejection — it is a no-op that succeeds. Both runs would then believe they
// held the lock. The host, the process id and a nanosecond timestamp are here
// to make that impossible; they are worth reading in `git show` besides.
func lockMessage() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("dispat release lock\n\nhost %s\npid %d\nat %s\n",
		host, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
}
