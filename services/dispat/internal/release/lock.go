package release

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// LockGitx is the slice of git the lock needs. *gitx.LocalGitx satisfies it.
//
// Note what is missing: nothing here can force a push. Taking the lock has to
// be able to fail, so the one operation that would make it always succeed is
// deliberately out of reach.
type LockGitx interface {
	CreateTag(ctx context.Context, name, message, target string) error
	TagObject(ctx context.Context, name string) (string, error)
	PushObjectToTag(ctx context.Context, remote, oid, name string) error
	DeleteTag(ctx context.Context, name string) error
	TagExists(ctx context.Context, name string) (bool, error)
	DeleteRemoteTagLease(ctx context.Context, remote, name, expectedOID string) error
}

// Lock is one run's claim on the repository. Acquire it before anything the
// run cannot take back, and release it when the run is over, whatever the
// outcome:
//
//	lock := &release.Lock{Git: git, Remote: remote, Log: log}
//	if err := lock.Acquire(ctx); err != nil {
//		return err
//	}
//	defer func() { _ = lock.Release(context.WithoutCancel(ctx)) }()
//
// The zero value is not usable: Git and Remote are required.
type Lock struct {
	Git    LockGitx
	Remote string
	Log    zerolog.Logger

	// held records that this run, and not some earlier one, put the tag on the
	// remote. Release does nothing without it, because deleting a lock nobody
	// here took is deleting somebody else's.
	held     bool
	localTag string
	oid      string
}

// Acquire claims the lock, or reports why it could not.
//
// Every failure means the same thing to the caller — do not release — so they
// are not distinguished. A rejected push is a lock somebody else holds; an
// unreachable remote, a missing one, or a ref rule that forbids the tag are
// all a remote this run cannot use to coordinate with the next one, and
// releasing without coordination is the thing being prevented.
func (l *Lock) Acquire(ctx context.Context) error {
	// Each attempt gets its own local ref. A shared checkout may have two
	// processes acquiring at once; neither may be able to retarget the source
	// the other is about to push.
	var err error
	l.localTag, err = localLockTag()
	if err != nil {
		return fmt.Errorf("creating a release lock identity: %w", err)
	}
	if err := l.Git.CreateTag(ctx, l.localTag, lockMessage(l.localTag), "HEAD"); err != nil {
		return fmt.Errorf("creating the release lock tag: %w", err)
	}
	oid, err := l.Git.TagObject(ctx, l.localTag)
	if err != nil {
		// Resolving the object may fail because the run was cancelled. The
		// attempt ref is still ours and must be cleaned with a live, bounded
		// context; otherwise a harmless failed acquisition leaves a local tag
		// that looks like durable lock state. If Git cannot remove it, say so:
		// this is the only evidence an operator has for the stranded ref.
		localCtx, cancelLocal := detachedDeadline(ctx, time.Second)
		if derr := l.Git.DeleteTag(localCtx, l.localTag); derr != nil {
			l.Log.Warn().Err(derr).Str("tag", l.localTag).
				Msg("could not remove the local lock tag after resolving its object failed")
		}
		cancelLocal()
		return fmt.Errorf("resolving the release lock object: %w", err)
	}
	l.oid = oid
	l.Log.Trace().Str("tag", LockTagName).Str("attempt", l.localTag).
		Str("remote", gitx.RedactURL(l.Remote)).Msg("pushing the release lock tag")
	if err := l.Git.PushObjectToTag(ctx, l.Remote, oid, LockTagName); err != nil {
		// Read the remote's lock once, and read it on a context this run's
		// cancellation cannot take away: the two questions that follow — "did
		// the push land after all" and "who holds it instead" — are exactly
		// the questions an interrupted acquisition has to answer, and a probe
		// that inherits the interrupt answers neither.
		probeCtx, cancelProbe := detachedDeadline(ctx, 10*time.Second)
		message := l.remoteLockMessage(probeCtx)
		cancelProbe()
		// A push whose response was lost still landed. The attempt id in the
		// tag message is unique to this Acquire call, so finding it on the
		// remote proves this run owns the lock rather than that somebody else
		// does — and owning it is what makes Release able to give it back
		// instead of stranding it for the next run to clear by hand.
		if carriesAttempt(message, l.localTag) {
			l.held = true
			l.Log.Warn().Err(err).Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
				Msg("the release lock push reported a failure but landed; this run owns the lock")
			return nil
		}
		// The local tag was this attempt's, so it goes with the attempt. Left
		// behind it would be read as a lock this clone holds by anyone looking
		// at `git tag`, which is exactly the wrong thing to suggest — and an
		// interrupted attempt has to clean it up on a live context of its own.
		localCtx, cancelLocal := detachedDeadline(ctx, time.Second)
		if derr := l.Git.DeleteTag(localCtx, l.localTag); derr != nil {
			l.Log.Debug().Err(derr).Str("tag", l.localTag).
				Msg("could not remove the local lock tag after a failed push")
		}
		cancelLocal()
		if holder := describeHolder(message); holder != "" {
			return fmt.Errorf("pushing the release lock tag to %s (%s): %w", gitx.RedactURL(l.Remote), holder, err)
		}
		return fmt.Errorf("pushing the release lock tag to %s: %w", gitx.RedactURL(l.Remote), err)
	}
	l.held = true
	// Info, not debug: "who holds the lock" is the first question of a stuck
	// pipeline, and this is the line that answers it at the default level.
	l.Log.Info().Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).Msg("release lock acquired")
	return nil
}

// Release gives the lock back: the tag goes from the remote first, then from
// here.
//
// It reports every failure and returns their joined error. Publication cannot
// be taken back at this point, but a tag left on the remote makes the release
// incomplete: the caller must retain the published outcome while failing the
// run so a green exit never conceals the lock the next run will meet.
//
// Call it with a context detached from cancellation (context.WithoutCancel):
// an interrupted run has as much reason to unlock as a finished one, and more,
// since nobody is watching to do it by hand.
func (l *Lock) Release(ctx context.Context) error {
	if !l.held {
		return nil
	}
	// Whatever happens below, this run has stopped claiming the lock: a second
	// call must not try again, and a failure here is not retried.
	l.held = false
	// Reserve the final second for local cleanup, so a remote that consumes
	// its entire allowance cannot strand the attempt ref in this checkout.
	var cleanupErrs []error
	remoteCtx, remoteCancel := detachedDeadline(ctx, 29*time.Second)
	if err := l.Git.DeleteRemoteTagLease(remoteCtx, l.Remote, LockTagName, l.oid); err != nil {
		l.Log.Error().Err(err).Str("code", "E336").Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
			Str("remedy", LockRemedy).Msg("could not remove the release lock tag from the remote")
		cleanupErrs = append(cleanupErrs, fmt.Errorf("removing the release lock tag from the remote: %w", err))
	}
	remoteCancel()
	localCtx, localCancel := detachedDeadline(ctx, time.Second)
	defer localCancel()
	if err := l.Git.DeleteTag(localCtx, l.localTag); err != nil {
		l.Log.Error().Err(err).Str("code", "E336").Str("tag", l.localTag).
			Msg("could not remove the local release lock tag")
		cleanupErrs = append(cleanupErrs, fmt.Errorf("removing the local release lock tag: %w", err))
	}
	// Older versions used the remote lock name as a local scratch tag.
	// Acquiring the remote lock proved no owner still relies on that tag.
	if exists, err := l.Git.TagExists(localCtx, LockTagName); err == nil && exists {
		if err := l.Git.DeleteTag(localCtx, LockTagName); err != nil {
			l.Log.Debug().Err(err).Msg("could not remove the legacy local lock tag")
		}
	}
	return errors.Join(cleanupErrs...)
}

func detachedDeadline(ctx context.Context, maximum time.Duration) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < maximum {
		return context.WithDeadline(base, deadline)
	}
	return context.WithTimeout(base, maximum)
}

func localLockTag() (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	return gitx.LockAttemptTagPrefix + hex.EncodeToString(identity[:]), nil
}

// lockInspector is the optional capability behind the holder line in a
// refusal: reading the remote lock tag's message. *gitx.LocalGitx has it; the
// narrower fakes in tests do not, and the refusal reads the same without it.
type lockInspector interface {
	RemoteTagMessage(ctx context.Context, remote, name string) (string, error)
}

// remoteLockMessage reads the remote lock tag's own message, or nothing when
// the capability, the remote or the tag is unavailable. The narrower fakes in
// tests do not implement the inspector, and every caller reads the same
// without it.
func (l *Lock) remoteLockMessage(ctx context.Context) string {
	insp, ok := l.Git.(lockInspector)
	if !ok {
		return ""
	}
	msg, err := insp.RemoteTagMessage(ctx, l.Remote, LockTagName)
	if err != nil {
		return ""
	}
	return msg
}

// carriesAttempt reports whether a remote lock message is the one this attempt
// wrote. It compares the attempt id rather than object ids, so it answers the
// same question a person would ask of `git show`.
//
// The attempt id is random per Acquire call and appears in no other message,
// which is what makes a match proof of ownership rather than of coincidence.
func carriesAttempt(message, attempt string) bool {
	if message == "" || attempt == "" {
		return false
	}
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimSpace(line) == "attempt "+attempt {
			return true
		}
	}
	return false
}

// describeHolder turns the remote lock tag's message into "held for 3h12m by
// host ci-7 pid 4242", or nothing when the message is absent or unparsed.
// The refusal is already correct without it; this is the difference between
// "somebody holds the lock" and knowing whether that somebody is still alive.
func describeHolder(msg string) string {
	if msg == "" {
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
func lockMessage(attempt string) string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("dispat release lock\n\nhost %s\npid %d\nat %s\nattempt %s\n",
		host, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano), attempt)
}
