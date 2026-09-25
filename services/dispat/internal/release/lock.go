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

// LockLostRemedy is what to do when the lock this run took is no longer its
// own: the remote carries another run's lock under the name, or none. It is
// the opposite advice to LockRemedy on purpose. The tag on the remote belongs
// to whoever wrote it, and deleting it would hand the repository to a third
// run while the second is still releasing.
const LockLostRemedy = "the release lock on the remote is no longer this run's; another run may hold it now, " +
	"so do not delete it: check what this run published and run again once that run has finished"

// The bounds of the remote calls around taking and giving back the lock.
// Giving it back fits a fleet's 30 seconds per repository: the delete, one
// read when the delete reports a failure, and the second the local cleanup
// keeps for itself.
const (
	// lockDeleteTimeout bounds one lease delete of the remote lock.
	lockDeleteTimeout = 18 * time.Second
	// lockReleaseReadTimeout bounds the one read that settles a delete
	// whose answer was a failure.
	lockReleaseReadTimeout = 10 * time.Second
	// lockPushReadTimeout bounds the reads that settle a push whose answer
	// was a failure: the verification's own retries, cut off at this bound.
	lockPushReadTimeout = 30 * time.Second
	// lockMessageReadTimeout bounds the read of the holder's tag message.
	lockMessageReadTimeout = 10 * time.Second
	// lockLocalTimeout bounds removing this attempt's local tag.
	lockLocalTimeout = time.Second
)

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
	// Run names the distributed run taking the lock, and is empty for a run
	// that delegates nothing. It is written into the tag message, because a
	// run that ends holding its lock has to be findable from it (CCME §28.6):
	// the run id is what every coordination branch of that run carries, and an
	// operator reading the lock has no other way to learn which branches to
	// settle before removing it.
	Run string

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
//
// A push that reports a failure is not taken at its word, because a lost
// response looks exactly like a refusal: the remote is read back and decides
// (see settleFailedPush). The lock is held only when a read shows this
// attempt's object on the remote.
func (l *Lock) Acquire(ctx context.Context) error {
	// Each attempt gets its own local ref. A shared checkout may have two
	// processes acquiring at once; neither may be able to retarget the source
	// the other is about to push.
	var err error
	l.localTag, err = localLockTag()
	if err != nil {
		return fmt.Errorf("creating a release lock identity: %w", err)
	}
	if err := l.Git.CreateTag(ctx, l.localTag, lockMessage(l.localTag, l.Run), "HEAD"); err != nil {
		return fmt.Errorf("creating the release lock tag: %w", err)
	}
	oid, err := l.Git.TagObject(ctx, l.localTag)
	if err != nil {
		// Resolving the object may fail because the run was cancelled. The
		// attempt ref is still ours and must be cleaned with a live, bounded
		// context; otherwise a harmless failed acquisition leaves a local tag
		// that looks like durable lock state. If Git cannot remove it, say so:
		// this is the only evidence an operator has for the stranded ref.
		localCtx, cancelLocal := detachedDeadline(ctx, lockLocalTimeout)
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
		return l.settleFailedPush(ctx, err)
	}
	l.held = true
	// Info, not debug: "who holds the lock" is the first question of a stuck
	// pipeline, and this is the line that answers it at the default level.
	l.Log.Info().Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).Msg("release lock acquired")
	return nil
}

// settleFailedPush decides what a lock push that reported a failure did,
// from what the remote carries now:
//
//   - this attempt's object: the push landed and only its answer was lost, so
//     this run owns the lock and says so;
//   - another object: another run holds the lock, and the refusal names it;
//   - no lock at all: the push did not land;
//   - nothing, because every read failed: the push may have landed, so it is
//     removed under a lease on this attempt's object, and the refusal names
//     the attempt and the object so a lock stranded anyway can be recognised.
//
// Every read runs on a context this run's cancellation cannot take away: "did
// the push land" is exactly the question an interrupted acquisition has to
// answer, and a read that inherits the interrupt answers nothing. A git that
// cannot read a remote tag object falls back to the tag message, which is
// what settled the question before objects could be read.
func (l *Lock) settleFailedPush(ctx context.Context, pushErr error) error {
	if _, isReadable := l.Git.(lockReader); !isReadable {
		return l.settleFailedPushByMessage(ctx, pushErr)
	}
	readCtx, cancelRead := detachedDeadline(ctx, lockPushReadTimeout)
	object, readErr := l.readRemoteObject(readCtx)
	cancelRead()
	switch {
	case readErr != nil:
		return l.abandonUnknownPush(ctx, pushErr, readErr)
	case object == l.oid:
		l.adoptLandedPush(pushErr)
		return nil
	}
	l.dropAttempt(ctx)
	if object == "" {
		return fmt.Errorf("pushing the release lock tag to %s: %w", gitx.RedactURL(l.Remote), pushErr)
	}
	return l.formatRefusal(ctx, pushErr)
}

// settleFailedPushByMessage is settleFailedPush for a git that can only read
// the remote tag's message. The attempt id in the message is unique to this
// Acquire call, so finding it there proves this run owns the lock.
func (l *Lock) settleFailedPushByMessage(ctx context.Context, pushErr error) error {
	probeCtx, cancelProbe := detachedDeadline(ctx, lockMessageReadTimeout)
	message := l.remoteLockMessage(probeCtx)
	cancelProbe()
	if carriesAttempt(message, l.localTag) {
		l.adoptLandedPush(pushErr)
		return nil
	}
	l.dropAttempt(ctx)
	if holder := describeHolder(message); holder != "" {
		return fmt.Errorf("pushing the release lock tag to %s (%s): %w", gitx.RedactURL(l.Remote), holder, pushErr)
	}
	return fmt.Errorf("pushing the release lock tag to %s: %w", gitx.RedactURL(l.Remote), pushErr)
}

// adoptLandedPush takes ownership of a lock whose push reported a failure and
// landed all the same. Owning it is what makes Release able to give it back
// instead of stranding it for the next run to clear by hand.
func (l *Lock) adoptLandedPush(pushErr error) {
	l.held = true
	l.Log.Warn().Err(pushErr).Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
		Msg("the release lock push reported a failure but landed; this run owns the lock")
}

// formatRefusal is the refusal of a lock another run holds, naming that run
// when the remote tag's message says who it is.
func (l *Lock) formatRefusal(ctx context.Context, pushErr error) error {
	probeCtx, cancelProbe := detachedDeadline(ctx, lockMessageReadTimeout)
	message := l.remoteLockMessage(probeCtx)
	cancelProbe()
	if holder := describeHolder(message); holder != "" {
		return fmt.Errorf("pushing the release lock tag to %s (%s): %w", gitx.RedactURL(l.Remote), holder, pushErr)
	}
	return fmt.Errorf("pushing the release lock tag to %s: %w", gitx.RedactURL(l.Remote), pushErr)
}

// abandonUnknownPush gives up an acquisition whose push reported a failure and
// whose outcome no read could establish. The push may have landed, and a lock
// nobody owns strands every later run, so it is deleted under a lease on this
// attempt's object: the lease can only remove this attempt's own lock, never
// another run's. The refusal names the attempt and the object either way,
// which is how an operator recognises a lock the delete could not reach.
func (l *Lock) abandonUnknownPush(ctx context.Context, pushErr, readErr error) error {
	deleteCtx, cancelDelete := detachedDeadline(ctx, lockDeleteTimeout)
	deleteErr := l.Git.DeleteRemoteTagLease(deleteCtx, l.Remote, LockTagName, l.oid)
	cancelDelete()
	outcome := "a push that landed was removed again"
	if deleteErr != nil {
		outcome = "removing a push that may have landed also failed"
		l.Log.Warn().Err(deleteErr).Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
			Str("attempt", l.localTag).Msg("could not remove a release lock push whose outcome is unknown")
	}
	l.dropAttempt(ctx)
	return fmt.Errorf("pushing the release lock tag to %s reported a failure and the lock could not be read "+
		"back (%s; %v): a %s tag on the remote whose object is %s and whose message names attempt %s is this "+
		"run's stranded lock and may be deleted, and any other is another run's: %w",
		gitx.RedactURL(l.Remote), outcome, readErr, LockTagName, l.oid, l.localTag, pushErr)
}

// dropAttempt removes this attempt's local tag after an acquisition that did
// not take the lock. Left behind it would be read as a lock this clone holds
// by anyone looking at `git tag`, which is exactly the wrong thing to suggest,
// and an interrupted attempt has to clean it up on a live context of its own.
func (l *Lock) dropAttempt(ctx context.Context) {
	localCtx, cancelLocal := detachedDeadline(ctx, lockLocalTimeout)
	defer cancelLocal()
	if err := l.Git.DeleteTag(localCtx, l.localTag); err != nil {
		l.Log.Debug().Err(err).Str("tag", l.localTag).
			Msg("could not remove the local lock tag after a failed push")
	}
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
	var cleanupErrs []error
	if err := l.deleteRemoteLock(ctx); err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	localCtx, localCancel := detachedDeadline(ctx, lockLocalTimeout)
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

// deleteRemoteLock removes this run's lock from the remote under a lease on
// its object, which is what keeps it from ever deleting another run's lock.
//
// A delete that reports a failure is read back once before it is believed,
// because a lost response looks exactly like a refused delete:
//
//   - no lock on the remote: the delete landed, or the lock was already gone,
//     and either way this run holds nothing any more;
//   - another object: another run holds the lock now, so this run cannot show
//     that it held the exclusion to its end (§27.7). That lock is the other
//     run's to keep, so it is left alone and the run fails with E336 and the
//     remedy that says not to delete it;
//   - this run's object, or a read that failed: the lock is stranded, which
//     is E336 with the remedy that tells an operator how to clear it.
func (l *Lock) deleteRemoteLock(ctx context.Context) error {
	remoteCtx, remoteCancel := detachedDeadline(ctx, lockDeleteTimeout)
	err := l.Git.DeleteRemoteTagLease(remoteCtx, l.Remote, LockTagName, l.oid)
	remoteCancel()
	if err == nil {
		return nil
	}
	object, readErr := l.readRemoteObjectOnce(ctx)
	switch {
	case readErr == nil && object == "":
		l.Log.Debug().Err(err).Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
			Msg("the release lock delete reported a failure and the remote holds no lock; it is released")
		return nil
	case readErr == nil && object != l.oid:
		l.Log.Error().Err(err).Str("code", "E336").Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
			Str("object", object).Str("remedy", LockLostRemedy).
			Msg("another run holds the release lock now; this run's lock was replaced before it ended, and that one is left alone")
		return fmt.Errorf("the release lock on %s was replaced by another run's before this run gave it back: %w",
			gitx.RedactURL(l.Remote), err)
	}
	l.Log.Error().Err(err).Str("code", "E336").Str("tag", LockTagName).Str("remote", gitx.RedactURL(l.Remote)).
		Str("remedy", LockRemedy).Msg("could not remove the release lock tag from the remote")
	return fmt.Errorf("removing the release lock tag from the remote: %w", err)
}

// readRemoteObjectOnce reads the remote lock's object once, on a bounded
// context of its own. A git that cannot read one answers ErrLockUnreadable,
// which the caller treats as a read that failed.
func (l *Lock) readRemoteObjectOnce(ctx context.Context) (string, error) {
	reader, isReadable := l.Git.(lockReader)
	if !isReadable {
		return "", ErrLockUnreadable
	}
	readCtx, cancelRead := detachedDeadline(ctx, lockReleaseReadTimeout)
	defer cancelRead()
	return reader.RemoteTagObject(readCtx, l.Remote, LockTagName)
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
// host ci-7 pid 4242", followed by "run <id>" when a distributed run holds it,
// or nothing when the message is absent or unparsed. The refusal is already
// correct without it; this is the difference between "somebody holds the
// lock" and knowing whether that somebody is still alive, and the run id is
// what finds the coordination branches a distributed holder left behind.
func describeHolder(msg string) string {
	if msg == "" {
		return ""
	}
	var host, pid, run string
	var at time.Time
	for _, line := range strings.Split(msg, "\n") {
		switch {
		case strings.HasPrefix(line, "host "):
			host = strings.TrimPrefix(line, "host ")
		case strings.HasPrefix(line, "pid "):
			pid = strings.TrimPrefix(line, "pid ")
		case strings.HasPrefix(line, "at "):
			at, _ = time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "at "))
		case strings.HasPrefix(line, "run "):
			run = strings.TrimPrefix(line, "run ")
		}
	}
	if host == "" || at.IsZero() {
		return ""
	}
	holder := fmt.Sprintf("held for %s by host %s pid %s", time.Since(at).Round(time.Second), host, pid)
	if run != "" {
		holder += " run " + run
	}
	return holder
}

// lockMessage is the body of the lock tag, and the reason two runs can never
// write the same tag object.
//
// A git tag object is identified by its target, its tagger, its date to the
// second and its message. Two runs tagging the same commit in the same second
// under the same identity with the same message would produce one object, and
// pushing an object the remote already has under a ref it already has is not a
// rejection: it is a no-op that succeeds. Both runs would then believe they
// held the lock. The host, the process id and a nanosecond timestamp are here
// to make that impossible; they are worth reading in `git show` besides.
//
// A distributed run adds a `run` line naming itself, which is the documented
// place CCME §28.6 asks for: the run id is what the coordination branches of
// an abandoned run are found by. A run that delegates nothing has no run id,
// and its message is the one it has always been.
func lockMessage(attempt, run string) string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	message := fmt.Sprintf("dispat release lock\n\nhost %s\npid %d\nat %s\nattempt %s\n",
		host, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano), attempt)
	if run != "" {
		message += "run " + run + "\n"
	}
	return message
}
