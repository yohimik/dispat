//go:build linux || darwin

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNodeSlotsReserveFullWeightAcrossAuthorities(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	first, err := OpenNodeSlotAuthority(root, "same-node", 3)
	require.NoError(t, err)
	other, err := OpenNodeSlotAuthority(root, "same-node", 3)
	require.NoError(t, err)
	ctx := t.Context()
	a, ok, err := first.TryReserve(ctx, NodeSlotIntent{Run: "run-a", Task: "build", Attempt: 1, Weight: 2})
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = other.TryReserve(ctx, NodeSlotIntent{Run: "run-b", Task: "build", Attempt: 1, Weight: 2})
	require.NoError(t, err)
	require.False(t, ok, "a weighted task cannot take one of two needed slots")
	c, ok, err := other.TryReserve(ctx, NodeSlotIntent{Run: "run-c", Task: "test", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
	_, _, err = first.TryReserve(ctx, a.Intent)
	require.ErrorContains(t, err, "unresolved slot lease")
	require.NoError(t, other.ReleaseSettled(ctx, a.ID, strings.Repeat("a", 40)))
	b, ok, err := first.TryReserve(ctx, NodeSlotIntent{Run: "run-b", Task: "build", Attempt: 1, Weight: 2})
	require.NoError(t, err)
	require.True(t, ok)
	held, err := first.Held(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []NodeSlotLease{c, b}, held)
	_, err = OpenNodeSlotAuthority(root, "same-node", 2)
	require.ErrorContains(t, err, "capacity changed with unresolved attempts")
}

func TestNodeSlotRecoveryLocatorRoundTripsJournalAndRejectsConflictingWAL(t *testing.T) {
	makeIntent := func() NodeSlotIntent {
		binding := ChoreographyRunBinding{Run: "run", Generation: "generation",
			PlanDigest: "plan", GraphDigest: "graph"}
		return NodeSlotIntent{Run: binding.Run, Task: "pkg:build", Attempt: 1, Weight: 1,
			Recovery: &NodeSlotRecoveryLocator{Binding: binding,
				CanonicalEndpoint: "file:///canonical", BootstrapOID: strings.Repeat("a", 40),
				TaskBranch: FormatChoreographyTaskBranch(binding.Run, "pkg:build"),
				Kind:       KindBuild, AssignmentOID: strings.Repeat("b", 40),
				ExpectedLedgerOID: strings.Repeat("c", 40),
				ProposalDigest:    strings.Repeat("d", 64), Node: "node"}}
	}
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	lease, acquired, err := a.TryReserve(t.Context(), makeIntent())
	require.NoError(t, err)
	require.True(t, acquired)
	reopened, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
	require.NoError(t, reopened.ReleaseSettled(t.Context(), lease.ID, strings.Repeat("e", 40)))

	changed := makeIntent()
	changed.Recovery.CanonicalEndpoint = "file:///other"
	second, acquired, err := reopened.TryReserve(t.Context(), makeIntent())
	require.NoError(t, err)
	require.True(t, acquired)
	// A stale reserve WAL must not accept a lease whose retained locator was
	// changed independently, even though Run/Task/Attempt and weight match.
	require.NoError(t, reopened.writeDocument(filepath.Join(reopened.dir, "pending.json"),
		nodeSlotTransaction{Kind: "reserve", Lease: second, Before: 0, After: 1}))
	altered := second
	altered.Intent = changed
	require.NoError(t, reopened.writeDocument(reopened.leasePath(second.ID), altered))
	_, err = OpenNodeSlotAuthority(root, "node", 1)
	require.ErrorContains(t, err, "conflicting node slot lease")
}

func TestNativeNodeSlotLocatorIsDurableAndExclusive(t *testing.T) {
	binding := ChoreographyRunBinding{Run: "native-run", Generation: "generation",
		PlanDigest: "plan", GraphDigest: "graph"}
	task := "pkg:version"
	locator := &NodeSlotNativeRecoveryLocator{Binding: binding,
		CanonicalEndpoint: "file:///canonical", BootstrapOID: strings.Repeat("a", 40),
		LedgerRootOID: strings.Repeat("b", 40), TaskBranch: FormatChoreographyTaskBranch(binding.Run, task),
		ReservationOID: strings.Repeat("c", 40), Kind: "version", Node: "node"}
	intent := NodeSlotIntent{Run: binding.Run, Task: task, Attempt: 1, Weight: 1,
		NativeRecovery: locator}
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	lease, acquired, err := a.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, acquired)
	reopened, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
	require.NoError(t, reopened.ReleaseSettled(t.Context(), lease.ID, strings.Repeat("d", 40)))

	foreign := intent
	changed := *locator
	changed.TaskBranch = FormatChoreographyTaskBranch(binding.Run, "other")
	foreign.NativeRecovery = &changed
	_, _, err = reopened.TryReserve(t.Context(), foreign)
	require.ErrorContains(t, err, "invalid node slot intent")
	foreign = intent
	foreign.Attempt = 2
	_, _, err = reopened.TryReserve(t.Context(), foreign)
	require.ErrorContains(t, err, "invalid node slot intent")
	foreign = intent
	foreign.Recovery = &NodeSlotRecoveryLocator{}
	_, _, err = reopened.TryReserve(t.Context(), foreign)
	require.ErrorContains(t, err, "invalid node slot intent")

	local := NodeSlotIntent{Run: binding.Run, Task: "pkg:publish", Attempt: 1, Weight: 1,
		NativeRecovery: &NodeSlotNativeRecoveryLocator{Binding: binding,
			CanonicalEndpoint: locator.CanonicalEndpoint, BootstrapOID: locator.BootstrapOID,
			LedgerRootOID: locator.LedgerRootOID, Kind: KindPublish, Node: "node"}}
	_, acquired, err = reopened.TryReserve(t.Context(), local)
	require.NoError(t, err)
	require.True(t, acquired, "branchless local authority has a distinct signed recovery form")
}

func TestNodeSlotLeaseSurvivesOwningProcessExit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	cmd := exec.Command(os.Args[0], "-test.run=^TestNodeSlotCrashHelper$")
	cmd.Env = append(os.Environ(), "DISPAT_NODE_SLOT_CRASH_ROOT="+root)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	held, err := a.Held(t.Context())
	require.NoError(t, err)
	require.Len(t, held, 1)
	_, ok, err := a.TryReserve(t.Context(), NodeSlotIntent{Run: "second", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.False(t, ok, "a dead parent cannot free capacity while a command may survive")
	require.NoError(t, a.ReleaseSettled(t.Context(), held[0].ID, strings.Repeat("b", 40)))
	_, ok, err = a.TryReserve(t.Context(), NodeSlotIntent{Run: "second", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestNodeSlotCrashHelper(t *testing.T) {
	root := os.Getenv("DISPAT_NODE_SLOT_CRASH_ROOT")
	if root == "" {
		return
	}
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	_, ok, err := a.TryReserve(context.Background(),
		NodeSlotIntent{Run: "crashed", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
	os.Exit(0)
}

func TestNodeSlotsRefuseMissingOrMutatedJournal(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	require.NoError(t, os.Remove(a.dir+string(os.PathSeparator)+"slots.json"))
	_, _, err = a.TryReserve(t.Context(), NodeSlotIntent{Run: "run", Task: "task", Attempt: 1, Weight: 1})
	require.ErrorContains(t, err, "disappeared")
	intent := NodeSlotIntent{Run: "run", Task: "task", Attempt: 1, Weight: 1}
	require.NoError(t, a.writeDocument(filepath.Join(a.dir, "pending.json"),
		nodeSlotTransaction{Kind: "reserve", Lease: NodeSlotLease{
			ID: nodeSlotLeaseID(intent), Intent: intent}, Before: 0, After: 1}))
	_, err = OpenNodeSlotAuthority(root, "same-node", 1)
	require.ErrorContains(t, err, "disappeared beside a pending mutation")
}

func TestNodeSlotWriteAheadRecoveryKeepsCapacityExact(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "run", Task: "task", Attempt: 1, Weight: 1}
	lease := NodeSlotLease{ID: nodeSlotLeaseID(intent), Intent: intent}
	require.NoError(t, a.writeDocument(filepath.Join(a.dir, "pending.json"),
		nodeSlotTransaction{Kind: "reserve", Lease: lease, Before: 0, After: 1}))
	reopened, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
	_, ok, err := reopened.TryReserve(t.Context(), NodeSlotIntent{Run: "other", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, reopened.writeDocument(filepath.Join(a.dir, "pending.json"),
		nodeSlotTransaction{Kind: "release", Lease: lease, Before: 1, After: 0,
			Evidence: strings.Repeat("a", 40)}))
	require.NoError(t, reopened.removeDocument(reopened.leasePath(lease.ID)))
	reopened, err = OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	held, err = reopened.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
	_, ok, err = reopened.TryReserve(t.Context(), NodeSlotIntent{Run: "other", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestNodeSlotLocalSettlementRequiresExactStoppedAttempt(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "local-run", Task: "pkg:build", Attempt: 1, Weight: 1}
	lease, ok, err := a.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, ok)
	wrong := NodeSlotLocalCompletion{Run: intent.Run, Task: intent.Task, Attempt: 2, Status: "succeeded"}
	require.ErrorContains(t, a.ReleaseLocalSettled(t.Context(), lease.ID, wrong), "another node-slot attempt")
	held, err := a.Held(t.Context())
	require.NoError(t, err)
	require.Equal(t, []NodeSlotLease{lease}, held)
	completion := NodeSlotLocalCompletion{Run: intent.Run, Task: intent.Task, Attempt: 1, Status: "failed"}
	require.NoError(t, a.ReleaseLocalSettled(t.Context(), lease.ID, completion))
	held, err = a.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held)
	entries, err := os.ReadDir(a.settlementDir())
	require.NoError(t, err)
	require.Empty(t, entries, "consumed local evidence does not grow with the task count")
}

func TestNodeSlotReopensCreateOnlyLocalTerminalBeforeCapacityRelease(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	intent := NodeSlotIntent{Run: "local-run", Task: "pkg:test", Attempt: 1, Weight: 1}
	lease, ok, err := a.TryReserve(t.Context(), intent)
	require.NoError(t, err)
	require.True(t, ok)
	terminal := nodeSlotLocalTerminal{Lease: lease, Completion: NodeSlotLocalCompletion{
		Run: intent.Run, Task: intent.Task, Attempt: intent.Attempt, Status: "confirmed-stop"}}
	require.NoError(t, a.writeNewDocument(a.settlementPath(lease.ID), terminal))
	_, ok, err = a.TryReserve(t.Context(), NodeSlotIntent{Run: "other", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.False(t, ok, "a local completion record alone does not release capacity")
	reopened, err := OpenNodeSlotAuthority(root, "node", 1)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held, "startup replays only the exact fsynced local terminal")
	_, ok, err = reopened.TryReserve(t.Context(), NodeSlotIntent{Run: "other", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestNodeSlotOSLockExcludesAnotherProcess(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Chmod(root, 0o700))
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	cmd := exec.Command(os.Args[0], "-test.run=^TestNodeSlotOSLockHelper$")
	cmd.Env = append(os.Environ(), "DISPAT_NODE_SLOT_LOCK_ROOT="+root)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	require.True(t, scanner.Scan())
	require.Equal(t, "locked", scanner.Text())
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, _, err = a.TryReserve(ctx, NodeSlotIntent{Run: "run", Task: "task", Attempt: 1, Weight: 1})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	_, ok, err := a.TryReserve(t.Context(), NodeSlotIntent{Run: "run", Task: "task", Attempt: 1, Weight: 1})
	require.NoError(t, err)
	require.True(t, ok)
}

func TestNodeSlotOSLockHelper(t *testing.T) {
	root := os.Getenv("DISPAT_NODE_SLOT_LOCK_ROOT")
	if root == "" {
		return
	}
	a, err := OpenNodeSlotAuthority(root, "same-node", 1)
	require.NoError(t, err)
	file, err := os.OpenFile(filepath.Join(a.dir, "slots.lock"), os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, lockNodeSlotFile(context.Background(), file))
	fmt.Println("locked")
	time.Sleep(5 * time.Second)
}
