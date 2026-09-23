package release

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// The lock's contract with git is small enough to state exactly, so these
// tests state it: which calls it makes, in which order, and what it does when
// one of them fails. What the calls mean to a real remote — that a second push
// of the same ref is refused — is git's own behaviour and is pinned in the
// gitx suite; that the two together stop a second release is pinned by the
// black-box suite in tests/integration.

// fakeLockGit records every call and fails the ones it is told to.
type fakeLockGit struct {
	calls    []string
	messages []string
	// failures are keyed by call name ("push", "create", "delete",
	// "deleteRemote").
	failures       map[string]error
	deleteOID      string
	deleteDeadline bool
	cancelResolve  context.CancelFunc
	deleteLive     bool
}

func (f *fakeLockGit) record(call string) error {
	f.calls = append(f.calls, call)
	return f.failures[call]
}

func (f *fakeLockGit) CreateTag(_ context.Context, _, message, _ string) error {
	f.messages = append(f.messages, message)
	return f.record("create")
}

func (f *fakeLockGit) TagObject(_ context.Context, _ string) (string, error) {
	if f.cancelResolve != nil {
		f.cancelResolve()
	}
	err := f.record("resolve")
	return "object-id", err
}

func (f *fakeLockGit) PushObjectToTag(_ context.Context, _, _, _ string) error {
	return f.record("push")
}

func (f *fakeLockGit) DeleteTag(ctx context.Context, _ string) error {
	f.deleteLive = ctx.Err() == nil
	return f.record("delete")
}

func (f *fakeLockGit) TagExists(context.Context, string) (bool, error) { return false, nil }

func (f *fakeLockGit) DeleteRemoteTagLease(ctx context.Context, _, _, oid string) error {
	f.deleteOID = oid
	_, f.deleteDeadline = ctx.Deadline()
	return f.record("deleteRemote")
}

// newLock builds a lock over a fake, with its log captured for the tests that
// assert on what it said.
func newLock(git *fakeLockGit, out *bytes.Buffer) *Lock {
	return &Lock{Git: git, Remote: "origin", Log: zerolog.New(out)}
}

// TestLockRoundTrip: the whole life of a lock in call order. The tag is
// written here, offered to the remote, and given back remote-first.
func TestLockRoundTrip(t *testing.T) {
	git := &fakeLockGit{}
	lock := newLock(git, &bytes.Buffer{})

	require.NoError(t, lock.Acquire(context.Background()))
	assert.Equal(t, []string{"create", "resolve", "push"}, git.calls)

	require.NoError(t, lock.Release(context.Background()))
	assert.Equal(t, []string{"create", "resolve", "push", "deleteRemote", "delete"}, git.calls,
		"the remote copy goes first: it is the one another run is waiting on")
	assert.Equal(t, "object-id", git.deleteOID, "unlock carries the immutable acquisition object")
	assert.True(t, git.deleteDeadline, "unlock is bounded even with an unbounded caller context")
}

// TestLockRejectedPushLeavesNothingBehind: the case the whole feature exists
// for. A refused push is a lock somebody else holds, and this run has to end
// up owning nothing — including the local tag it just wrote, which anyone
// reading `git tag` would take for a lock this clone holds.
func TestLockRejectedPushLeavesNothingBehind(t *testing.T) {
	git := &fakeLockGit{failures: map[string]error{"push": errors.New("already exists")}}
	lock := newLock(git, &bytes.Buffer{})

	err := lock.Acquire(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists", "the git rejection survives the wrapping")
	assert.Equal(t, []string{"create", "resolve", "push", "delete"}, git.calls)

	// And a lock that was never taken is never given back, whatever the caller
	// does next: the tag on the remote is another run's.
	lock.Release(context.Background())
	assert.Equal(t, []string{"create", "resolve", "push", "delete"}, git.calls,
		"releasing an unacquired lock must touch nothing")
}

// TestLockRejectedPushWithAStuckLocalTag: the double failure. The push was
// refused and the local tag will not go either, which changes nothing the
// caller can act on — the answer is still "somebody else is releasing" — so
// the tidying failure is noted quietly and the rejection is what comes back.
func TestLockRejectedPushWithAStuckLocalTag(t *testing.T) {
	var out bytes.Buffer
	git := &fakeLockGit{failures: map[string]error{
		"push":   errors.New("already exists"),
		"delete": errors.New("still there"),
	}}
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&out).Level(zerolog.DebugLevel)}

	err := lock.Acquire(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists", "the rejection is still what is reported")
	assert.Contains(t, out.String(), `"level":"debug"`, "the tidying failure is a footnote, not the news")
}

// TestLockCreateFailureIsReported: nothing was pushed, so there is nothing to
// clean up and nothing to release.
func TestLockCreateFailureIsReported(t *testing.T) {
	git := &fakeLockGit{failures: map[string]error{"create": errors.New("ref rejected")}}
	lock := newLock(git, &bytes.Buffer{})

	require.Error(t, lock.Acquire(context.Background()))
	assert.Equal(t, []string{"create"}, git.calls)
}

// TestLockResolveFailureCleansWithALiveContext: resolving the private attempt
// tag can be the Git call that observes cancellation. The tag still belongs
// to this acquisition, so its cleanup must outlive that cancellation and stay
// bounded rather than silently leaving a ref that looks like lock state.
func TestLockResolveFailureCleansWithALiveContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	git := &fakeLockGit{
		failures:      map[string]error{"resolve": context.Canceled},
		cancelResolve: cancel,
	}
	lock := newLock(git, &bytes.Buffer{})

	err := lock.Acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []string{"create", "resolve", "delete"}, git.calls)
	assert.True(t, git.deleteLive, "attempt cleanup must not inherit the failed run's cancellation")
}

// TestLockMessagesAreUniquePerAttempt: two runs must never produce the same
// tag object. If they did, the second push would be a no-op that succeeds and
// both runs would hold the lock — the one failure mode that would make the
// feature worse than not having it, since it fails silently.
func TestLockMessagesAreUniquePerAttempt(t *testing.T) {
	git := &fakeLockGit{}
	lock := newLock(git, &bytes.Buffer{})
	ctx := context.Background()

	require.NoError(t, lock.Acquire(ctx))
	require.NoError(t, lock.Release(ctx))
	require.NoError(t, lock.Acquire(ctx))

	require.Len(t, git.messages, 2)
	assert.NotEqual(t, git.messages[0], git.messages[1],
		"back-to-back attempts, the worst case for a timestamp, still differ")
	for _, msg := range git.messages {
		assert.Contains(t, msg, "pid ", "the message says which process holds it")
		assert.Contains(t, msg, "host ")
		assert.Contains(t, msg, "attempt "+gitx.LockAttemptTagPrefix)
	}
}

// TestLockMessageNamesADistributedRun: a run that delegates work writes its run
// id into the tag, because a run that ends holding its lock has to be findable
// from the lock (CCME §28.6, vector 29): the run id is what its coordination
// branches carry. A run with no run id writes the message it always wrote,
// line for line.
func TestLockMessageNamesADistributedRun(t *testing.T) {
	git := &fakeLockGit{}
	distributed := newLock(git, &bytes.Buffer{})
	distributed.Run = "6f1a9f0d2b90c8f96f1a9f0d2b90c8f9"
	require.NoError(t, distributed.Acquire(context.Background()))
	local := newLock(git, &bytes.Buffer{})
	require.NoError(t, local.Acquire(context.Background()))

	require.Len(t, git.messages, 2)
	assert.True(t, strings.HasSuffix(git.messages[0], "\nrun 6f1a9f0d2b90c8f96f1a9f0d2b90c8f9\n"),
		"the run is the last line of a distributed run's lock: %q", git.messages[0])
	assert.NotContains(t, git.messages[1], "\nrun ", "a run with no run id names none")
	lines := strings.Split(strings.TrimSuffix(git.messages[1], "\n"), "\n")
	require.Len(t, lines, 6, "the local message keeps its shape: %q", git.messages[1])
	assert.Equal(t, "dispat release lock", lines[0])
	assert.Empty(t, lines[1])
	for index, prefix := range []string{"host ", "pid ", "at ", "attempt "} {
		assert.True(t, strings.HasPrefix(lines[2+index], prefix), "line %d is %q", 2+index, lines[2+index])
	}
}

// TestLockRefusalNamesTheHoldingRun: a refusal met by a distributed run's lock
// names that run, which is the one fact an operator needs to find the
// coordination branches it left before the lock may be removed.
func TestLockRefusalNamesTheHoldingRun(t *testing.T) {
	git := &inspectingLockGit{
		fakeLockGit: fakeLockGit{failures: map[string]error{"push": errors.New("already exists")}},
		message: "dispat release lock\n\nhost ci-7\npid 4242\nat " +
			time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano) +
			"\nattempt dispat-release-lock-attempt-someone-else\nrun 0123456789abcdef0123456789abcdef\n",
	}
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}

	err := lock.Acquire(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host ci-7 pid 4242 run 0123456789abcdef0123456789abcdef")
}

// TestLockReleaseReportsFailuresAndCarriesOn: the end of a run is no place to
// give up. A remote that will not take the delete is said out loud, with the
// remedy the next run's refusal will echo, and the local half is still cleaned
// up afterwards.
func TestLockReleaseReportsFailuresAndCarriesOn(t *testing.T) {
	var out bytes.Buffer
	git := &fakeLockGit{failures: map[string]error{"deleteRemote": errors.New("no such remote")}}
	lock := newLock(git, &out)
	ctx := context.Background()

	require.NoError(t, lock.Acquire(ctx))
	err := lock.Release(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such remote")

	assert.Equal(t, []string{"create", "resolve", "push", "deleteRemote", "delete"}, git.calls,
		"the local tag goes even when the remote one would not")
	logged := out.String()
	assert.Contains(t, logged, `"level":"error"`)
	assert.Contains(t, logged, `"code":"E336"`)
	assert.Contains(t, logged, "no such remote")
	assert.Contains(t, logged, LockTagName)
	assert.Contains(t, logged, "delete the tag on the remote",
		"the log carries the remedy: this is what stranded the next run")
}

// TestLockReleaseIsIdempotent: the bracket is a defer, and a caller that also
// releases explicitly must not double-delete — by then the tag may belong to
// the next run.
func TestLockReleaseIsIdempotent(t *testing.T) {
	git := &fakeLockGit{}
	lock := newLock(git, &bytes.Buffer{})
	ctx := context.Background()

	require.NoError(t, lock.Acquire(ctx))
	require.NoError(t, lock.Release(ctx))
	require.NoError(t, lock.Release(ctx))

	assert.Equal(t, 1, strings.Count(strings.Join(git.calls, " "), "deleteRemote"))
}

// TestLockLocalDeleteFailureIsReported: the local half is scratch, so its
// failure is worth a line and nothing more.
func TestLockLocalDeleteFailureIsReported(t *testing.T) {
	var out bytes.Buffer
	git := &fakeLockGit{failures: map[string]error{"delete": errors.New("not found")}}
	lock := newLock(git, &out)
	ctx := context.Background()

	require.NoError(t, lock.Acquire(ctx))
	err := lock.Release(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	assert.Contains(t, out.String(), "local release lock tag")
	assert.Contains(t, out.String(), `"code":"E336"`)
}

// inspectingLockGit is fakeLockGit plus the optional capability the holder
// line rides on: reading the remote lock tag's message.
type inspectingLockGit struct {
	fakeLockGit
	message string
	msgErr  error
}

func (f *inspectingLockGit) RemoteTagMessage(context.Context, string, string) (string, error) {
	f.record("readMessage")
	return f.message, f.msgErr
}

// TestLockRefusalNamesTheHolder: with the remote tag's message readable, the
// refusal says who holds the lock and for how long — the difference between
// "come back later" and knowing the holder died an hour ago.
func TestLockRefusalNamesTheHolder(t *testing.T) {
	git := &inspectingLockGit{
		fakeLockGit: fakeLockGit{failures: map[string]error{"push": errors.New("already exists")}},
		message: "dispat release lock\n\nhost ci-7\npid 4242\nat " +
			time.Now().UTC().Add(-3*time.Hour).Format(time.RFC3339Nano) + "\n",
	}
	lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}

	err := lock.Acquire(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "held for 3h0m", "the age comes from the tag's own timestamp")
	assert.Contains(t, err.Error(), "host ci-7")
	assert.Contains(t, err.Error(), "pid 4242")
	assert.Contains(t, err.Error(), "already exists", "the git rejection still comes through")
}

// TestLockRefusalDegradesWithoutAMessage: an unreadable or unparseable tag
// message costs nothing but the holder line.
func TestLockRefusalDegradesWithoutAMessage(t *testing.T) {
	for name, git := range map[string]LockGitx{
		"no capability": &fakeLockGit{failures: map[string]error{"push": errors.New("refused")}},
		"read fails":    &inspectingLockGit{fakeLockGit: fakeLockGit{failures: map[string]error{"push": errors.New("refused")}}, msgErr: errors.New("no fetch")},
		"not dispat's":  &inspectingLockGit{fakeLockGit: fakeLockGit{failures: map[string]error{"push": errors.New("refused")}}, message: "some other tag"},
	} {
		t.Run(name, func(t *testing.T) {
			lock := &Lock{Git: git, Remote: "origin", Log: zerolog.New(&bytes.Buffer{})}
			err := lock.Acquire(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "refused")
			assert.NotContains(t, err.Error(), "held for")
		})
	}
}

// inspectableLockGit is a fake that can also answer what the remote's lock tag
// says, which is how an interrupted or unanswered push is told apart from a
// lock somebody else holds.
type inspectableLockGit struct {
	*fakeLockGit
	remoteMessage func() string
	probeLive     bool
}

func (f *inspectableLockGit) RemoteTagMessage(ctx context.Context, _, _ string) (string, error) {
	f.probeLive = ctx.Err() == nil
	if f.remoteMessage == nil {
		return "", errors.New("no remote tag")
	}
	return f.remoteMessage(), nil
}

// TestLockAcquireRecoversALostPushResponse: a push whose answer never came
// back still put the tag on the remote. The attempt id in the tag's message is
// this call's alone, so finding it there proves this run owns the lock — and
// owning it is what lets Release give it back instead of stranding it.
//
// The probe runs on a context of its own, so the one case that produces a lost
// response most often — a cancelled run — is also the one it can answer.
func TestLockAcquireRecoversALostPushResponse(t *testing.T) {
	var out bytes.Buffer
	git := &inspectableLockGit{fakeLockGit: &fakeLockGit{
		failures: map[string]error{"push": errors.New("connection reset")},
	}}
	lock := newLock(git.fakeLockGit, &out)
	lock.Git = git
	git.remoteMessage = func() string { return git.messages[0] }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, lock.Acquire(ctx), "the tag is on the remote and carries this attempt")
	assert.True(t, git.probeLive, "the ownership probe must outlive the run's cancellation")
	assert.NotContains(t, git.calls, "delete", "an owned attempt keeps its local tag")
	assert.Contains(t, out.String(), "this run owns the lock")

	lock.Release(context.Background())
	assert.Contains(t, git.calls, "deleteRemote", "the recovered lock is given back")
	assert.Equal(t, "object-id", git.deleteOID, "and only while it still names this run's object")
}

// TestLockAcquireKeepsAnotherRunsLock: the same failed push against a remote
// whose lock belongs to somebody else is a refusal, and the refusal names the
// holder rather than adopting the tag.
func TestLockAcquireKeepsAnotherRunsLock(t *testing.T) {
	var out bytes.Buffer
	git := &inspectableLockGit{fakeLockGit: &fakeLockGit{
		failures: map[string]error{"push": errors.New("rejected")},
	}}
	lock := newLock(git.fakeLockGit, &out)
	lock.Git = git
	git.remoteMessage = func() string {
		return "dispat release lock\n\nhost ci-7\npid 4242\nat " +
			time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano) +
			"\nattempt dispat-release-lock-attempt-someone-else\n"
	}

	err := lock.Acquire(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host ci-7")
	assert.Contains(t, git.calls, "delete", "a refused attempt removes its own local tag")

	lock.Release(context.Background())
	assert.NotContains(t, git.calls, "deleteRemote", "another run's lock is never deleted")
}
