// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The first push of an input state to each node, against real mailboxes whose
// receiving side is slow on purpose.
//
// A pre-receive hook is what makes a push take time on a real remote, so the
// fixtures here are bare repositories with one: it runs for every branch a
// push creates, records the push, and holds it for as long as the scenario
// needs. Everything else is the real transport.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// offerFixture is a run with one source repository and one mailbox per node,
// each mailbox holding every creating push in a hook of the scenario's.
type offerFixture struct {
	coordinator *Coordinator
	source      Source
	events      string
}

// newOfferFixture opens a run over nodes whose mailboxes run hook on every
// branch a push creates. The hook is a shell body that sees NODE, the node's
// name, and EVENTS, a folder the test reads.
func newOfferFixture(t testing.TB, nodes []string, hook string) *offerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the slow mailbox is a POSIX shell hook")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	events := filepath.Join(root, "events")
	require.NoError(t, os.MkdirAll(events, 0o755))
	source := filepath.Join(root, "source")
	for _, args := range [][]string{
		{"init", "-q", source},
		{"-C", source, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "source"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	signer, err := NewSigner("hunter2")
	require.NoError(t, err)
	links := make([]Link, 0, len(nodes))
	mailboxes := map[string]*GitMailbox{}
	for _, node := range nodes {
		endpoint := filepath.Join(root, node+".git")
		out, err := exec.Command("git", "init", "-q", "--bare", endpoint).CombinedOutput()
		require.NoError(t, err, "git init --bare: %s", out)
		script := "#!/bin/sh\nNODE=" + node + "\nEVENTS=" + events + "\n" +
			"created=\nwhile read old new ref; do\n  case \"$old\" in *[!0]*) ;; *) created=\"$ref\";; esac\ndone\n" +
			"[ -n \"$created\" ] || exit 0\necho \"$created\" >> \"$EVENTS/pushes-$NODE\"\n" + hook + "\nexit 0\n"
		require.NoError(t, os.WriteFile(filepath.Join(endpoint, "hooks", "pre-receive"), []byte(script), 0o755))
		links = append(links, Link{Name: node, Endpoint: endpoint})
		store := &gitx.LocalGitx{Dir: source, Log: zerolog.Nop()}
		mailboxes[node] = NewGitMailbox(endpoint, store, signer, zerolog.Nop())
	}
	coordinator := NewCoordinator("run-1", "digest", "generation", LocalNode{Name: "here", Capacity: 1},
		links, mailboxes, signer, Timeouts{Cancel: 30}, TransferLimits{}, zerolog.Nop())
	coordinator.Start(context.Background(), Dispatch{Concurrency: 1, OpenRepository: func(dir string) *gitx.LocalGitx {
		return &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}
	}})
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })
	return &offerFixture{coordinator: coordinator, events: events,
		source: Source{Name: "app", Path: ".", Dir: source}}
}

// head is the source repository's current commit, which stands in for a
// prepared state: what is pushed is a commit of the repository it came from.
func (f *offerFixture) head(t testing.TB) string {
	t.Helper()
	out, err := exec.Command("git", "-C", f.source.Dir, "rev-parse", "HEAD").CombinedOutput()
	require.NoError(t, err, "rev-parse: %s", out)
	return strings.TrimSpace(string(out))
}

// advance moves the source on by one commit, which is a new prepared state.
func (f *offerFixture) advance(t testing.TB) {
	t.Helper()
	out, err := exec.Command("git", "-C", f.source.Dir, "-c", "user.name=t", "-c", "user.email=t@t",
		"commit", "-q", "--allow-empty", "-m", "next").CombinedOutput()
	require.NoError(t, err, "commit: %s", out)
}

// offerAtOnce offers one state to each named node from goroutines of its own,
// and answers the branch each offer returned.
func (f *offerFixture) offerAtOnce(t testing.TB, nodes []string, commit string) []string {
	t.Helper()
	branches := make([]string, len(nodes))
	failures := make([]error, len(nodes))
	var offers sync.WaitGroup
	for index, node := range nodes {
		offers.Go(func() {
			branches[index], failures[index] = f.coordinator.offerInput(context.Background(), node, f.source, commit)
		})
	}
	offers.Wait()
	for _, err := range failures {
		require.NoError(t, err)
	}
	return branches
}

// readEvent is one file the hooks wrote, trimmed, and empty when none was.
func (f *offerFixture) readEvent(name string) string {
	content, err := os.ReadFile(filepath.Join(f.events, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// overlapHook holds each creating push until the other node's push has
// started too, for at most ten seconds, and says which it saw. Two pushes that
// run at once both see the other; two that run one after the other leave the
// first to wait alone.
const overlapHook = `touch "$EVENTS/started-$NODE"
other=build-a
[ "$NODE" = build-a ] && other=build-b
i=0
while [ $i -lt 100 ]; do
  if [ -e "$EVENTS/started-$other" ]; then echo overlapped > "$EVENTS/result-$NODE"; exit 0; fi
  sleep 0.1
  i=$((i+1))
done
echo alone > "$EVENTS/result-$NODE"`

// TestFirstSnapshotsOfDifferentNodesPushAtOnce: the first push of a state to
// one node does not hold up the first push of the same state to another, so a
// run's first two dispatches wait for the slower of two transfers rather than
// for both, one after the other.
func TestFirstSnapshotsOfDifferentNodesPushAtOnce(t *testing.T) {
	nodes := []string{"build-a", "build-b"}
	fixture := newOfferFixture(t, nodes, overlapHook)

	branches := fixture.offerAtOnce(t, nodes, fixture.head(t))

	assert.NotEqual(t, branches[0], branches[1], "each node reads its own branch")
	for _, node := range nodes {
		assert.Equal(t, "overlapped", fixture.readEvent("result-"+node),
			"the push to %s ran while the other one did", node)
	}
}

// gateHook holds each creating push until the test lets it go, so that a
// second offer of the same state can arrive while the first is in flight.
const gateHook = `touch "$EVENTS/started-$NODE"
i=0
while [ ! -e "$EVENTS/release" ] && [ $i -lt 100 ]; do sleep 0.1; i=$((i+1)); done`

// TestOneNodeReceivesOneStateOnce: two dispatches to one node that need the
// same state share one push, even when the second asks while the first is
// still pushing, and a new state is pushed again.
func TestOneNodeReceivesOneStateOnce(t *testing.T) {
	fixture := newOfferFixture(t, []string{"build-a"}, gateHook)
	commit := fixture.head(t)

	var offers sync.WaitGroup
	branches := make([]string, 2)
	for index := range branches {
		offers.Go(func() {
			branch, err := fixture.coordinator.offerInput(context.Background(), "build-a", fixture.source, commit)
			assert.NoError(t, err)
			branches[index] = branch
		})
		if index == 0 {
			require.Eventually(t, func() bool { return fixture.isEventWritten("started-build-a") },
				10*time.Second, 10*time.Millisecond, "the first push reaches the mailbox")
		}
	}
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.events, "release"), nil, 0o644))
	offers.Wait()

	assert.Equal(t, branches[0], branches[1], "both dispatches name one branch")
	assert.Len(t, strings.Fields(fixture.readEvent("pushes-build-a")), 1, "and the state travelled once")

	fixture.advance(t)
	fixture.offerAtOnce(t, []string{"build-a"}, fixture.head(t))
	assert.Len(t, strings.Fields(fixture.readEvent("pushes-build-a")), 2, "a new state travels again")
}

// isEventWritten reports whether a hook has written one file yet.
func (f *offerFixture) isEventWritten(name string) bool {
	_, err := os.Stat(filepath.Join(f.events, name))
	return err == nil
}

// refuseOnceHook holds each creating push until the test lets it go, and
// refuses the first one it lets go.
const refuseOnceHook = gateHook + `
if [ ! -e "$EVENTS/refused" ]; then touch "$EVENTS/refused"; exit 1; fi`

// TestADispatchWaitingOnAFailedPushPushesItself: a push that failed is no
// answer for the dispatch that waited for it, which pushes the state itself
// rather than inheriting the failure.
func TestADispatchWaitingOnAFailedPushPushesItself(t *testing.T) {
	fixture := newOfferFixture(t, []string{"build-a"}, refuseOnceHook)
	commit := fixture.head(t)

	var offers sync.WaitGroup
	branches := make([]string, 2)
	failures := make([]error, 2)
	for index := range branches {
		offers.Go(func() {
			branches[index], failures[index] = fixture.coordinator.offerInput(context.Background(),
				"build-a", fixture.source, commit)
		})
		if index == 0 {
			require.Eventually(t, func() bool { return fixture.isEventWritten("started-build-a") },
				10*time.Second, 10*time.Millisecond, "the first push reaches the mailbox")
		}
	}
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(fixture.events, "release"), nil, 0o644))
	offers.Wait()

	require.Error(t, failures[0], "the refused push fails its own dispatch")
	require.NoError(t, failures[1], "and the waiting one pushes again")
	assert.NotEmpty(t, branches[1])
	assert.Len(t, strings.Fields(fixture.readEvent("pushes-build-a")), 2)
}

// BenchmarkFirstSnapshotToTwoNodes measures a run's first dispatch to two
// nodes of one new state, over mailboxes that take 200 ms to accept a push.
// ns/op is the wall time the two dispatches wait; pushes/op is how many
// branches the state was put on, which stays at two.
func BenchmarkFirstSnapshotToTwoNodes(b *testing.B) {
	nodes := []string{"build-a", "build-b"}
	fixture := newOfferFixture(b, nodes, "sleep 0.2")
	offers := 0
	for b.Loop() {
		offers++
		b.StopTimer()
		fixture.advance(b)
		commit := fixture.head(b)
		b.StartTimer()
		fixture.offerAtOnce(b, nodes, commit)
	}
	pushes := len(strings.Fields(fixture.readEvent("pushes-build-a"))) +
		len(strings.Fields(fixture.readEvent("pushes-build-b")))
	b.ReportMetric(float64(pushes)/float64(offers), "pushes/op")
}
