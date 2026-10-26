package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type heldSweepTask struct {
	started chan struct{}
	gate    chan struct{}
}

func TestSweepRefusesSuccessorWhenSignedFinalityCannotSettle(t *testing.T) {
	a, pl := sweepApp(t, 1)
	work := &fakeWork{}
	_, err := a.runSweep(t.Context(), pl, []string{"a", "b"}, work, sweepOptions{
		TaskOutcomeSettled: func(_ context.Context, pkg string, _ release.PackageOutcome) error {
			if pkg == "a" {
				return errors.New("ledger refused terminal decision")
			}
			return nil
		},
	})
	require.ErrorContains(t, err, "ledger refused terminal decision")
	require.Equal(t, []string{"a"}, work.order(),
		"consumer work must not run after provider finality failed to persist")
}

func TestSweepRefusesSuccessorWhenBlockedFinalityCannotSettle(t *testing.T) {
	a, pl := sweepApp(t, 1)
	work := &fakeWork{fail: map[string]bool{"a": true}}
	var settled []string
	var statuses []release.PackageOutcome
	var blockers []string
	_, err := a.runSweep(t.Context(), pl, []string{"a", "b", "c"}, work, sweepOptions{
		TaskOutcomeSettled: func(_ context.Context, pkg string, status release.PackageOutcome) error {
			settled = append(settled, pkg)
			statuses = append(statuses, status)
			return nil
		},
		TaskBlocked: func(_ context.Context, pkg, blocker string) error {
			settled = append(settled, pkg)
			blockers = append(blockers, blocker)
			return errors.New("ledger refused blocked decision")
		},
	})
	require.ErrorContains(t, err, "ledger refused blocked decision")
	require.Equal(t, []string{"a", "b"}, settled)
	require.Equal(t, []release.PackageOutcome{release.OutcomeFailed}, statuses)
	require.Equal(t, []string{"a"}, blockers)
	require.Equal(t, []string{"a"}, work.order(),
		"the dependent command must not start after blocked finality failed to persist")
}

func (w heldSweepTask) stage() string { return "run:test" }

func (w heldSweepTask) resolve(context.Context, *plan.Release) (task, error) {
	return func(ctx context.Context) error {
		close(w.started)
		select {
		case <-w.gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil
}

func TestSeparateLocalSweepsShareExplicitNodeCapacity(t *testing.T) {
	firstApp, firstPlan := sweepApp(t, 2)
	secondApp, secondPlan := sweepApp(t, 2)
	firstPlan.Releases["a"].Pkg.BuildWeight = 1
	secondPlan.Releases["a"].Pkg.BuildWeight = 1
	slots, err := execution.OpenNodeSlotAuthority(filepath.Join(t.TempDir(), "node-state"), "same-node", 1)
	require.NoError(t, err)
	first := heldSweepTask{started: make(chan struct{}), gate: make(chan struct{})}
	second := heldSweepTask{started: make(chan struct{}), gate: make(chan struct{})}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, runErr := firstApp.runSweep(t.Context(), firstPlan, []string{"a"}, first,
			sweepOptions{LocalSlots: slots, LocalRun: "first-run", SlotWait: 2 * time.Second})
		firstDone <- runErr
	}()
	select {
	case <-first.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first local sweep did not start")
	}
	go func() {
		_, runErr := secondApp.runSweep(t.Context(), secondPlan, []string{"a"}, second,
			sweepOptions{LocalSlots: slots, LocalRun: "second-run", SlotWait: 2 * time.Second})
		secondDone <- runErr
	}()
	select {
	case <-second.started:
		t.Fatal("a second local run started while the first held the shared node slot")
	case <-time.After(150 * time.Millisecond):
	}
	close(first.gate)
	require.NoError(t, <-firstDone)
	select {
	case <-second.started:
	case <-time.After(2 * time.Second):
		t.Fatal("second local run did not start after settled completion")
	}
	close(second.gate)
	require.NoError(t, <-secondDone)
	held, err := slots.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
}
