package app

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/script"
)

// `dispat for`'s iteration and exit-code handling, against the same fake runner
// `dispat if` is tested with, so "which scripts ran, in which order, with which
// environment" is asked directly. That the strings then reach a real shell is
// the integration suite's claim.

// items is the literal list a scenario iterates over, in one line.
func items(values ...string) []ForItem {
	out := make([]ForItem, 0, len(values))
	for _, v := range values {
		out = append(out, ForItem{Value: v})
	}
	return out
}

// runFor drives RunFor with a fake runner and requires it not to fail for a
// reason of dispat's own.
func runFor(t *testing.T, f *fakeRunner, opts ForOptions) int {
	t.Helper()
	opts.Runner = f
	opts.Log = zerolog.Nop()
	code, err := RunFor(context.Background(), opts)
	require.NoError(t, err)
	return code
}

// envOf indexes one iteration's NAME=value pairs.
func envOf(t *testing.T, pairs []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		name, value, found := splitPair(p)
		require.True(t, found, "not a NAME=value pair: %q", p)
		out[name] = value
	}
	return out
}

func splitPair(pair string) (string, string, bool) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == '=' {
			return pair[:i], pair[i+1:], true
		}
	}
	return "", "", false
}

func TestRunForVisitsEveryItemInOrder(t *testing.T) {
	// The list is the list, in the order it was given: a loop that reordered
	// its items would break every script whose steps depend on each other.
	f := &fakeRunner{}
	code := runFor(t, f, ForOptions{Items: items("a", "b", "c"), Scripts: []string{"work"}})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"work", "work", "work"}, f.ran)

	var seen []string
	for _, env := range f.envs {
		seen = append(seen, envOf(t, env)[ItemEnvVar])
	}
	assert.Equal(t, []string{"a", "b", "c"}, seen)
}

func TestRunForExportsTheIteratorVariables(t *testing.T) {
	// The three every mode carries, plus the item's own, and the rule that
	// makes them dependable: the iterator's win a name clash, so an item
	// describing itself as DISPAT_ITEM cannot lie about which item it is.
	f := &fakeRunner{}
	runFor(t, f, ForOptions{
		Items: []ForItem{
			{Value: "core", Env: []string{"DISPAT_PACKAGE=core", ItemEnvVar + "=impostor"}},
			{Value: "web", Env: []string{"DISPAT_PACKAGE=web"}},
		},
		Scripts: []string{"work"},
	})
	require.Len(t, f.envs, 2)

	first := envOf(t, f.envs[0])
	assert.Equal(t, "core", first[ItemEnvVar], "the iterator's value is the last one written, so it wins")
	assert.Equal(t, "core", first["DISPAT_PACKAGE"])
	assert.Equal(t, "0", first[IndexEnvVar])
	assert.Equal(t, "2", first[TotalEnvVar])

	second := envOf(t, f.envs[1])
	assert.Equal(t, "web", second[ItemEnvVar])
	assert.Equal(t, "1", second[IndexEnvVar])
	assert.Equal(t, "2", second[TotalEnvVar], "the total is the list's length, not the position")
}

func TestRunForRunsEveryScriptPerItemAndStopsAtTheFirstFailure(t *testing.T) {
	// Several --do scripts are one item's sequence: they run in order and gate
	// their own remainder, the same fail-fast a release stage's sequence gets.
	f := &fakeRunner{outcomes: map[string]error{"second": exitErr(t, 4)}}
	code := runFor(t, f, ForOptions{
		Items:   items("a", "b"),
		Scripts: []string{"first", "second", "third"},
	})
	assert.Equal(t, 4, code)
	assert.Equal(t, []string{"first", "second"}, f.ran,
		"the item stopped inside its sequence, and the failing item stopped the loop")
}

// itemRunner is the fake for the scenarios about *which* item failed. The
// per-command fake cannot express those: every iteration runs the same script
// text, and what differs is the environment it runs under, so this one answers
// by the item instead.
type itemRunner struct {
	ran      []string       // the item each run was for, in order
	outcomes map[string]int // item -> exit code, absent meaning success
	t        *testing.T
}

var _ script.Runner = (*itemRunner)(nil)

func (r *itemRunner) Run(_ context.Context, _, _ string, env []string, _, _ io.Writer) error {
	item := envOf(r.t, env)[ItemEnvVar]
	r.ran = append(r.ran, item)
	if code, failing := r.outcomes[item]; failing {
		return exitErr(r.t, code)
	}
	return nil
}

func TestRunForStopsAtTheFirstFailingItem(t *testing.T) {
	// The default: a failing item ends the loop where it stands and its code
	// becomes the command's, so a loop stays transparent in a pipeline.
	f := &itemRunner{t: t, outcomes: map[string]int{"b": 7, "c": 9}}
	code, err := RunFor(context.Background(), ForOptions{
		Items: items("a", "b", "c"), Scripts: []string{"work"},
		Runner: f, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	assert.Equal(t, 7, code, "the first failure's code, not the last one's")
	assert.Equal(t, []string{"a", "b"}, f.ran, "the items after the failure never start")
}

func TestRunForKeepGoingRunsTheRestAndKeepsTheFirstCode(t *testing.T) {
	// --keep-going is about the remaining work, not about the verdict: every
	// item still runs, and the command still reports the first failure, because
	// a later item succeeding says nothing about the one that did not.
	f := &itemRunner{t: t, outcomes: map[string]int{"b": 7, "c": 9}}
	code, err := RunFor(context.Background(), ForOptions{
		Items: items("a", "b", "c"), Scripts: []string{"work"}, KeepGoing: true,
		Runner: f, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	assert.Equal(t, 7, code, "the first failure still decides, however many followed it")
	assert.Equal(t, []string{"a", "b", "c"}, f.ran, "every item runs")
}

func TestRunForOnFailureRunsOnceAndDecidesTheCode(t *testing.T) {
	// The failure script reacts to the loop having failed, so it runs once
	// however many items failed, and its code replaces theirs.
	f := &fakeRunner{outcomes: map[string]error{"work": exitErr(t, 7), "notify": exitErr(t, 3)}}
	code := runFor(t, f, ForOptions{
		Items: items("a", "b"), Scripts: []string{"work"},
		KeepGoing: true, OnFailure: "notify",
	})
	assert.Equal(t, 3, code)
	assert.Equal(t, []string{"work", "work", "notify"}, f.ran,
		"one cleanup for the loop, not one per failing item")
}

func TestRunForOnFailureIsSkippedOnSuccess(t *testing.T) {
	f := &fakeRunner{}
	code := runFor(t, f, ForOptions{
		Items: items("a"), Scripts: []string{"work"}, OnFailure: "notify",
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, []string{"work"}, f.ran, "nothing failed, so nothing reacts to a failure")
}

func TestRunForOnFailureSurvivesACancelledContext(t *testing.T) {
	// The same treatment every other failure script gets: a Ctrl-C that ended
	// the loop must not also kill the cleanup reacting to it. The cancellation
	// therefore breaks the loop rather than returning from it, which is the
	// only way an interrupted loop can reach its --on-failure at all.
	f := &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := RunFor(ctx, ForOptions{
		Items: items("a"), Scripts: []string{"work"}, OnFailure: "cleanup",
		Runner: f, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	assert.Equal(t, 0, code, "the cleanup succeeded, so its code is the command's")
	assert.Equal(t, []string{"cleanup"}, f.ran,
		"no item ran, and the cleanup still did")
}

func TestRunForStopsBetweenItemsWhenCancelled(t *testing.T) {
	// A cancelled context ends the loop where it stands rather than starting
	// the next item, which is what makes Ctrl-C stop a long list. The exit code
	// is dispat's own 1: no item said anything, the run was taken away.
	f := &fakeRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := RunFor(ctx, ForOptions{
		Items: items("a", "b"), Scripts: []string{"work"}, Runner: f, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, f.ran, "the check happens before the item, so nothing started")
}

func TestRunForOverNothingIsASuccessThatRunsNothing(t *testing.T) {
	// Shell fidelity: `for x in $EMPTY` runs the loop zero times and succeeds.
	f := &fakeRunner{}
	code := runFor(t, f, ForOptions{Scripts: []string{"work"}})
	assert.Equal(t, 0, code)
	assert.Empty(t, f.ran)
}

func TestRunForRequireItemsRefusesAnEmptyIteration(t *testing.T) {
	// ...unless the caller says the list mattering is the point, which is the
	// CI gate --require-items exists for.
	f := &fakeRunner{}
	code, err := RunFor(context.Background(), ForOptions{
		Scripts: []string{"work"}, RequireItems: true, Runner: f, Log: zerolog.Nop(),
	})
	require.Error(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, f.ran)

	// A non-empty iteration is unaffected by the flag.
	f = &fakeRunner{}
	assert.Equal(t, 0, runFor(t, f, ForOptions{
		Items: items("a"), Scripts: []string{"work"}, RequireItems: true}))
	assert.Equal(t, []string{"work"}, f.ran)
}

func TestRunForRunsEveryItemInOneFolder(t *testing.T) {
	// No item is cd'ed into: every iteration runs where the invocation stands,
	// so a relative path in the script means one thing throughout.
	f := &fakeRunner{}
	runFor(t, f, ForOptions{
		Items: []ForItem{
			{Value: "core", Env: []string{DirEnvVar + "=/repo/packages/core"}},
			{Value: "web", Env: []string{DirEnvVar + "=/repo/packages/web"}},
		},
		Scripts: []string{"pwd"},
		Dir:     "/repo",
	})
	assert.Equal(t, []string{"/repo", "/repo"}, f.dirs,
		"the item's own folder is exported, never entered")
}

func TestRunForReportsARunnerFailureAsDispatsOwn(t *testing.T) {
	// A missing shell is not an item saying something, so it becomes dispat's
	// own failure rather than an exit code passed along.
	f := &fakeRunner{outcomes: map[string]error{"work": errors.New("no such shell")}}
	code, err := RunFor(context.Background(), ForOptions{
		Items: items("a", "b"), Scripts: []string{"work"}, Runner: f, Log: zerolog.Nop(),
	})
	require.Error(t, err)
	assert.Equal(t, 1, code)
	assert.Len(t, f.ran, 1, "the loop stops at the failure rather than repeating it per item")
}
