// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// Who a run says it is, on the events it sends.
//
// Two claims are tested here, and they are the two halves of the same rule. A
// process that takes part in distributed execution names itself on every
// event, and a process that does not names nothing, so a repository with no
// execution object sends the payloads it always sent. Beside them, the third
// field: an event about work another machine did names that machine, and an
// event about work this one did names nobody.

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSenderNamesEveryEventItStamps: the sender travels onto the event and is
// absent from the payload of a run that states no execution object.
func TestSenderNamesEveryEventItStamps(t *testing.T) {
	for name, tc := range map[string]struct {
		sender     Sender
		isStated   bool
		wantFields map[string]any
	}{
		"a process that states nothing": {
			sender: Sender{}, wantFields: map[string]any{}},
		"an orchestrator": {
			sender: Sender{Role: "orchestrator", Node: "ci-1"}, isStated: true,
			wantFields: map[string]any{"role": "orchestrator", "node": "ci-1"}},
		"a worker": {
			sender: Sender{Role: "worker", Node: "build-a"}, isStated: true,
			wantFields: map[string]any{"role": "worker", "node": "build-a"}},
	} {
		t.Run(name, func(t *testing.T) {
			stamped := tc.sender.Stamp(Event{Name: EventReleaseStarted, Root: "/repo"})

			assert.Equal(t, tc.isStated, tc.sender.IsStated())
			assert.Equal(t, EventReleaseStarted, stamped.Name, "stamping changes nothing else")
			document, err := json.Marshal(stamped)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(document, &payload))
			for _, field := range []string{"role", "node", "worker"} {
				want, isWanted := tc.wantFields[field]
				if !isWanted {
					assert.NotContains(t, payload, field, "an unstated field is absent, not empty")
					continue
				}
				assert.Equal(t, want, payload[field])
			}
		})
	}
}

// TestSenderNamesEveryLineItAttaches: the same decision, on the logger. A
// stated sender puts both fields on every line written through the child
// logger; an unstated one hands the logger back as it was.
func TestSenderNamesEveryLineItAttaches(t *testing.T) {
	for name, tc := range map[string]struct {
		sender Sender
		want   map[string]any
	}{
		"a stated sender": {
			sender: Sender{Role: "worker", Node: "build-a"},
			want:   map[string]any{"role": "worker", "node": "build-a", "message": "polling the mailbox"}},
		"an unstated sender": {
			sender: Sender{},
			want:   map[string]any{"message": "polling the mailbox"}},
	} {
		t.Run(name, func(t *testing.T) {
			var written bytes.Buffer
			log := tc.sender.Attach(zerolog.New(&written))

			log.Info().Msg("polling the mailbox")

			var line map[string]any
			require.NoError(t, json.Unmarshal(written.Bytes(), &line))
			delete(line, "level")
			assert.Equal(t, tc.want, line)
		})
	}
}

// TestDelegatedStagesNameTheNodeTheyRanOn: a build executed somewhere else is
// reported with that node on the events that can know it, and the stages this
// run kept for itself name nobody. The package's own outcome carries the
// placement too, because "where was this version built" is a question about
// the release rather than about the stage that was heard from last.
func TestDelegatedStagesNameTheNodeTheyRanOn(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	observer := &fakeObserver{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = &fakeRemote{}
	executor.Observer = observer

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusPublished, results["a"].Status, "%v", results["a"].Err)
	assert.Equal(t, "build-a", results["a"].Worker, "the package remembers where its work was placed")
	assert.Equal(t, "build-a", observer.workerOf(t, EventStageSucceeded, "build"),
		"the stage that was delegated names the node that ran it")
	assert.Empty(t, observer.workerOf(t, EventStageStarted, "build"),
		"and the event that opened it could not, because nothing was placed yet")
	assert.Empty(t, observer.workerOf(t, EventStageSucceeded, "publish"),
		"a stage this run kept names no worker")
	assert.Equal(t, "build-a", observer.workerOf(t, EventPackagePublished, ""))
}

// TestAFailedDelegatedStageNamesItsNode: the failure a package is reported
// with names the machine to go and look at.
func TestAFailedDelegatedStageNamesItsNode(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	observer := &fakeObserver{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Remote = &fakeRemote{failBuild: "a", failPart: PartCommands}
	executor.Observer = observer

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusFailed, results["a"].Status)
	assert.Equal(t, "build-a", results["a"].Worker)
	assert.Equal(t, "build-a", observer.workerOf(t, EventPackageFailed, ""))
}

// TestALocalRunNamesNoWorker: with no remote behind the seam every event is
// the event it always was, the third field included.
func TestALocalRunNamesNoWorker(t *testing.T) {
	p := mkPlan(planSpec{Names: []string{"a"}})
	observer := &fakeObserver{}
	executor := newExecutor(execSpec{Runner: &fakeRunner{}, Tagger: &fakeTagger{}, Build: 1, Publish: 1})
	executor.Observer = observer

	results := executor.Run(context.Background(), p)

	require.Equal(t, StatusPublished, results["a"].Status)
	assert.Empty(t, results["a"].Worker)
	assert.Empty(t, observer.workerOf(t, EventStageSucceeded, "build"))
	assert.Empty(t, observer.workerOf(t, EventPackagePublished, ""))
}

// workerOf is the worker field of the first recorded event of one name and
// stage, and fails the test when no such event was observed: a claim about a
// field of an event that never arrived would pass by accident.
func (f *fakeObserver) workerOf(t *testing.T, name, stage string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ev := range f.events {
		if ev.Name == name && ev.Stage == stage {
			return ev.Worker
		}
	}
	t.Fatalf("no %s event of the %q stage was observed", name, stage)
	return ""
}
