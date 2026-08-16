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
	failures map[string]error
}

func (f *fakeLockGit) record(call string) error {
	f.calls = append(f.calls, call)
	return f.failures[call]
}

func (f *fakeLockGit) CreateTagForce(_ context.Context, _, message, _ string) error {
	f.messages = append(f.messages, message)
	return f.record("create")
}

func (f *fakeLockGit) PushTag(_ context.Context, _, _ string) error { return f.record("push") }

func (f *fakeLockGit) DeleteTag(_ context.Context, _ string) error { return f.record("delete") }

func (f *fakeLockGit) DeleteRemoteTag(_ context.Context, _, _ string) error {
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
	assert.Equal(t, []string{"create", "push"}, git.calls)

	lock.Release(context.Background())
	assert.Equal(t, []string{"create", "push", "deleteRemote", "delete"}, git.calls,
		"the remote copy goes first: it is the one another run is waiting on")
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
	assert.Equal(t, []string{"create", "push", "delete"}, git.calls)

	// And a lock that was never taken is never given back, whatever the caller
	// does next: the tag on the remote is another run's.
	lock.Release(context.Background())
	assert.Equal(t, []string{"create", "push", "delete"}, git.calls,
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

// TestLockMessagesAreUniquePerAttempt: two runs must never produce the same
// tag object. If they did, the second push would be a no-op that succeeds and
// both runs would hold the lock — the one failure mode that would make the
// feature worse than not having it, since it fails silently.
func TestLockMessagesAreUniquePerAttempt(t *testing.T) {
	git := &fakeLockGit{}
	lock := newLock(git, &bytes.Buffer{})
	ctx := context.Background()

	require.NoError(t, lock.Acquire(ctx))
	lock.Release(ctx)
	require.NoError(t, lock.Acquire(ctx))

	require.Len(t, git.messages, 2)
	assert.NotEqual(t, git.messages[0], git.messages[1],
		"back-to-back attempts, the worst case for a timestamp, still differ")
	for _, msg := range git.messages {
		assert.Contains(t, msg, "pid ", "the message says which process holds it")
		assert.Contains(t, msg, "host ")
	}
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
	lock.Release(ctx)

	assert.Equal(t, []string{"create", "push", "deleteRemote", "delete"}, git.calls,
		"the local tag goes even when the remote one would not")
	logged := out.String()
	assert.Contains(t, logged, `"level":"error"`)
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
	lock.Release(ctx)
	lock.Release(ctx)

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
	lock.Release(ctx)

	assert.Contains(t, out.String(), "local release lock tag")
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
	for name, git := range map[string]LockGit{
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
