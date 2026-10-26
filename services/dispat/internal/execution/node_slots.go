// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/fsx"
)

const nodeSlotVersion = 1
const nodeSlotDocumentLimitBytes = 4 << 10

// NodeSlotIntent identifies one attempt across processes and restarts. Its
// identity does not include Weight: a retry with a changed weight is still
// the same unresolved attempt, never a second slot reservation.
type NodeSlotIntent struct {
	Run      string `json:"run"`
	Task     string `json:"task"`
	Attempt  int    `json:"attempt"`
	Weight   int    `json:"weight"`
	PickupID string `json:"pickupId,omitempty"`
	// Recovery is the exact signed peer claim identity captured before a
	// distributed proposal leaves this process. NativeRecovery is the signed
	// first-node task root captured before its local effect. Exactly one is
	// present on a v3 lease; no-links local leases omit both.
	Recovery       *NodeSlotRecoveryLocator       `json:"recovery,omitempty"`
	NativeRecovery *NodeSlotNativeRecoveryLocator `json:"nativeRecovery,omitempty"`
}

type NodeSlotNativeRecoveryLocator struct {
	Binding           ChoreographyRunBinding `json:"binding"`
	CanonicalEndpoint string                 `json:"canonicalEndpoint"`
	BootstrapOID      string                 `json:"bootstrapOid"`
	LedgerRootOID     string                 `json:"ledgerRootOid"`
	TaskBranch        string                 `json:"taskBranch,omitempty"`
	ReservationOID    string                 `json:"reservationOid,omitempty"`
	Kind              string                 `json:"kind"`
	Node              string                 `json:"node"`
}

type NodeSlotRecoveryLocator struct {
	Binding           ChoreographyRunBinding `json:"binding"`
	CanonicalEndpoint string                 `json:"canonicalEndpoint"`
	BootstrapOID      string                 `json:"bootstrapOid"`
	TaskBranch        string                 `json:"taskBranch"`
	Kind              string                 `json:"kind"`
	AssignmentOID     string                 `json:"assignmentOid,omitempty"`
	ExpectedTaskOID   string                 `json:"expectedTaskOid,omitempty"`
	GraphRootOID      string                 `json:"graphRootOid,omitempty"`
	TemplateDigest    string                 `json:"templateDigest,omitempty"`
	ExpectedLedgerOID string                 `json:"expectedLedgerOid"`
	ProposalDigest    string                 `json:"proposalDigest"`
	Node              string                 `json:"node"`
}

type NodeSlotLease struct {
	ID     string         `json:"id"`
	Intent NodeSlotIntent `json:"intent"`
}

// NodeSlotLocalCompletion is written only after the local command returned
// and its process group is known stopped. It is distinct from a Git receipt:
// an uncertain child or a parent PID exit cannot create this evidence.
type NodeSlotLocalCompletion struct {
	Run     string `json:"run"`
	Task    string `json:"task"`
	Attempt int    `json:"attempt"`
	Status  string `json:"status"`
}

type nodeSlotLocalTerminal struct {
	Lease      NodeSlotLease           `json:"lease"`
	Completion NodeSlotLocalCompletion `json:"completion"`
}

type nodeSlotSummary struct {
	Version     int    `json:"version"`
	Node        string `json:"node"`
	AuthorityID string `json:"authorityId"`
	Capacity    int    `json:"capacity"`
	Used        int    `json:"used"`
}

type nodeSlotTransaction struct {
	Kind     string        `json:"kind"`
	Lease    NodeSlotLease `json:"lease"`
	Before   int           `json:"before"`
	After    int           `json:"after"`
	Evidence string        `json:"evidence,omitempty"`
}

// NodeSlotAuthority is one physical node's durable weighted capacity. The
// caller supplies the same stable private root and node name to every first
// and serving process on that OS-user/shared-filesystem scope.
type NodeSlotAuthority struct {
	dir         string
	node        string
	capacity    int
	authorityID string
}

// This complements the OS file lock for two authority objects in one process.
var nodeSlotProcessMu sync.Mutex

func OpenNodeSlotAuthority(root, node string, capacity int) (*NodeSlotAuthority, error) {
	if runtime.GOOS != nodeSlotLinuxPlatformName && runtime.GOOS != nodeSlotMacOSPlatformName {
		return nil, fmt.Errorf(nodeSlotUnsupportedPlatformErrorFormat, runtime.GOOS)
	}
	if !filepath.IsAbs(root) || node == nodeSlotAbsentIdentityText || len(node) > nodeSlotMaximumNodeNameBytes || capacity <= nodeSlotNoOccupiedWeight || capacity > nodeSlotMaximumCapacityWeight {
		return nil, fmt.Errorf(nodeSlotInvalidIdentityCapacityErrorText)
	}
	if err := os.MkdirAll(root, nodeSlotPrivateDirectoryMode); err != nil {
		return nil, fmt.Errorf(nodeSlotRootCreationErrorFormat, err)
	}
	if err := requirePrivateSlotDir(root); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(node))
	dir := filepath.Join(root, hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(dir, nodeSlotPrivateDirectoryMode); err != nil {
		return nil, fmt.Errorf(nodeSlotDirectoryCreationErrorFormat, err)
	}
	if err := requirePrivateSlotDir(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, nodeSlotLeaseDirectoryName), nodeSlotPrivateDirectoryMode); err != nil {
		return nil, err
	}
	if err := requirePrivateSlotDir(filepath.Join(dir, nodeSlotLeaseDirectoryName)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, nodeSlotSettlementDirectoryName), nodeSlotPrivateDirectoryMode); err != nil {
		return nil, err
	}
	if err := requirePrivateSlotDir(filepath.Join(dir, nodeSlotSettlementDirectoryName)); err != nil {
		return nil, err
	}
	a := &NodeSlotAuthority{dir: dir, node: node, capacity: capacity}
	err := a.withLock(context.Background(), func() error {
		summary, err := a.readSummary()
		if err != nil {
			return err
		}
		if summary.Version == nodeSlotUninitializedVersion {
			if _, err := os.Lstat(filepath.Join(a.dir, nodeSlotPendingTransactionFileName)); err == nil {
				return fmt.Errorf(nodeSlotPendingMutationMissingJournalErrorText)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			entries, err := os.ReadDir(a.leaseDir())
			if err != nil {
				return fmt.Errorf(nodeSlotRetainedLeasesUnreadableErrorFormat, err)
			}
			if len(entries) != nodeSlotNoEntriesCount {
				return fmt.Errorf(nodeSlotRetainedLeaseMissingJournalErrorText)
			}
			entries, err = os.ReadDir(a.settlementDir())
			if err != nil {
				return fmt.Errorf(nodeSlotLocalSettlementsUnreadableErrorFormat, err)
			}
			if len(entries) != nodeSlotNoEntriesCount {
				return fmt.Errorf(nodeSlotLocalSettlementMissingJournalErrorText)
			}
			id, err := newNodeSlotAuthorityID()
			if err != nil {
				return err
			}
			summary = nodeSlotSummary{Version: nodeSlotVersion, Node: node,
				AuthorityID: id, Capacity: capacity}
			if err := a.writeSummary(summary); err != nil {
				return err
			}
		} else {
			if err := a.recoverTransaction(&summary); err != nil {
				return err
			}
			if err := a.recoverLocalSettlements(&summary); err != nil {
				return err
			}
			if summary.Node != node {
				return fmt.Errorf(nodeSlotIdentityChangedErrorText)
			}
			if summary.Capacity != capacity {
				if summary.Used != nodeSlotNoOccupiedWeight {
					return fmt.Errorf(nodeSlotCapacityChangedWhileOccupiedErrorText)
				}
				summary.Capacity = capacity
				if err := a.writeSummary(summary); err != nil {
					return err
				}
			}
		}
		if err := a.auditLeases(summary); err != nil {
			return err
		}
		a.authorityID = summary.AuthorityID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// AuthorityID is a nonsecret persistent identity for preflight: two
// processes advertising one node but using different state roots differ.
func (a *NodeSlotAuthority) AuthorityID() string {
	if a == nil {
		return nodeSlotAbsentIdentityText
	}
	return a.authorityID
}

// TryReserve atomically takes all Weight slots or none. Normal transitions
// read only the fixed-size summary and this attempt's own lease file.
func (a *NodeSlotAuthority) TryReserve(ctx context.Context, intent NodeSlotIntent) (NodeSlotLease, bool, error) {
	if a == nil || intent.Run == nodeSlotAbsentIdentityText || intent.Task == nodeSlotAbsentIdentityText || len(intent.Run)+len(intent.Task) > nodeSlotMaximumIntentIdentityBytes ||
		intent.Attempt < nodeSlotMinimumAttemptCount || intent.Weight < nodeSlotMinimumWeight || intent.Weight > a.capacity ||
		!validNodeSlotPickup(intent) ||
		(intent.Recovery != nil && intent.NativeRecovery != nil) ||
		(intent.Recovery != nil && !validNodeSlotRecovery(intent)) ||
		(intent.NativeRecovery != nil && !validNodeSlotNativeRecovery(intent)) {
		return NodeSlotLease{}, nodeSlotLeaseNotAcquiredState, fmt.Errorf(nodeSlotInvalidIntentErrorText)
	}
	lease := NodeSlotLease{ID: nodeSlotLeaseID(intent), Intent: intent}
	var acquired bool
	err := a.withLock(ctx, func() error {
		summary, err := a.currentSummary()
		if err != nil {
			return err
		}
		if _, err := os.Lstat(a.leasePath(lease.ID)); err == nil {
			return fmt.Errorf(nodeSlotAttemptLeaseUnresolvedErrorText)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if intent.Weight > summary.Capacity-summary.Used {
			return nil
		}
		txn := nodeSlotTransaction{Kind: nodeSlotReserveTransactionKind, Lease: lease,
			Before: summary.Used, After: summary.Used + intent.Weight}
		if err := a.commitTransaction(&summary, txn); err != nil {
			return err
		}
		acquired = nodeSlotLeaseAcquiredState
		return nil
	})
	if err != nil {
		return NodeSlotLease{}, nodeSlotLeaseNotAcquiredState, err
	}
	if !acquired {
		return NodeSlotLease{}, nodeSlotLeaseNotAcquiredState, nil
	}
	return lease, nodeSlotLeaseAcquiredState, nil
}

// ReleaseSettled is called only after the caller verified a durable terminal
// or acknowledged stop. A PID exit is never settlement. An ambiguous write
// can be recovered from the one-entry write-ahead transaction on reopen.
func (a *NodeSlotAuthority) ReleaseSettled(ctx context.Context, leaseID, evidenceOID string) error {
	if a == nil || !validSlotHex(leaseID, nodeSlotSHA256HexCharacters) ||
		!validSlotHex(evidenceOID, nodeSlotSHA1HexCharacters) && !validSlotHex(evidenceOID, nodeSlotSHA256HexCharacters) {
		return fmt.Errorf(nodeSlotSettlementEvidenceRequiredErrorText)
	}
	return a.withLock(ctx, func() error {
		summary, err := a.currentSummary()
		if err != nil {
			return err
		}
		lease, err := a.readLease(leaseID)
		if err != nil {
			return err
		}
		return a.releaseLease(&summary, lease, evidenceOID)
	})
}

// ReleaseLocalSettled first fsyncs create-only evidence for an exact local
// attempt, then releases its weighted slot through the ordinary write-ahead
// transaction. A crash before evidence leaves the lease held; a crash after
// evidence lets Open complete the exact release without guessing from a PID.
func (a *NodeSlotAuthority) ReleaseLocalSettled(ctx context.Context,
	leaseID string, completion NodeSlotLocalCompletion) error {
	if a == nil || !validSlotHex(leaseID, nodeSlotSHA256HexCharacters) || !validLocalCompletion(completion) {
		return fmt.Errorf(nodeSlotInvalidLocalCompletionErrorText)
	}
	return a.withLock(ctx, func() error {
		summary, err := a.currentSummary()
		if err != nil {
			return err
		}
		lease, err := a.readLease(leaseID)
		if err != nil {
			return err
		}
		if completion.Run != lease.Intent.Run || completion.Task != lease.Intent.Task ||
			completion.Attempt != lease.Intent.Attempt {
			return fmt.Errorf(nodeSlotLocalCompletionAttemptMismatchErrorText)
		}
		terminal := nodeSlotLocalTerminal{Lease: lease, Completion: completion}
		if err := a.writeNewDocument(a.settlementPath(leaseID), terminal); err != nil {
			return err
		}
		return a.finishLocalSettlement(&summary, terminal)
	})
}

func validLocalCompletion(completion NodeSlotLocalCompletion) bool {
	if completion.Run == nodeSlotAbsentIdentityText || completion.Task == nodeSlotAbsentIdentityText || completion.Attempt < nodeSlotMinimumAttemptCount {
		return nodeSlotIdentityInvalidState
	}
	switch completion.Status {
	case nodeSlotSucceededStatus, nodeSlotFailedStatus, nodeSlotConfirmedStopStatus:
		return nodeSlotIdentityValidState
	default:
		return nodeSlotIdentityInvalidState
	}
}

func (a *NodeSlotAuthority) releaseLease(summary *nodeSlotSummary,
	lease NodeSlotLease, evidence string) error {
	txn := nodeSlotTransaction{Kind: nodeSlotReleaseTransactionKind, Lease: lease, Evidence: evidence,
		Before: summary.Used, After: summary.Used - lease.Intent.Weight}
	if txn.After < nodeSlotNoOccupiedWeight {
		return fmt.Errorf(nodeSlotReleaseExceedsUsageErrorText)
	}
	return a.commitTransaction(summary, txn)
}

// Held is a recovery/audit query, charged O(active leases) separately from
// the O(1) metadata touched by normal reserve/release transitions.
func (a *NodeSlotAuthority) Held(ctx context.Context) ([]NodeSlotLease, error) {
	if a == nil {
		return nil, fmt.Errorf(nodeSlotMissingAuthorityErrorText)
	}
	var leases []NodeSlotLease
	err := a.withLock(ctx, func() error {
		summary, err := a.currentSummary()
		if err != nil {
			return err
		}
		leases, err = a.listLeases()
		if err != nil {
			return err
		}
		used := nodeSlotNoOccupiedWeight
		for _, lease := range leases {
			used += lease.Intent.Weight
		}
		if used != summary.Used {
			return fmt.Errorf(nodeSlotSummaryLeaseMismatchErrorText)
		}
		return nil
	})
	return leases, err
}

func (a *NodeSlotAuthority) withLock(ctx context.Context, action func() error) error {
	nodeSlotProcessMu.Lock()
	defer nodeSlotProcessMu.Unlock()
	lockPath := filepath.Join(a.dir, nodeSlotLockFileName)
	if info, err := os.Lstat(lockPath); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&nodeSlotOtherUsersPermissionMask != nodeSlotNoPermissionBits) {
		return fmt.Errorf(nodeSlotUnsafeLockErrorText)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, nodeSlotPrivateFileMode)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := lockNodeSlotFile(ctx, file); err != nil {
		return err
	}
	defer unlockNodeSlotFile(file)
	return action()
}

func (a *NodeSlotAuthority) currentSummary() (nodeSlotSummary, error) {
	summary, err := a.readSummary()
	if err != nil {
		return nodeSlotSummary{}, fmt.Errorf(nodeSlotJournalUnreadableErrorFormat, err)
	}
	if summary.Version != nodeSlotVersion || summary.Node != a.node ||
		summary.AuthorityID != a.authorityID || summary.Capacity != a.capacity {
		return nodeSlotSummary{}, fmt.Errorf(nodeSlotJournalChangedErrorText)
	}
	if err := a.recoverTransaction(&summary); err != nil {
		return nodeSlotSummary{}, err
	}
	return summary, nil
}

func (a *NodeSlotAuthority) commitTransaction(summary *nodeSlotSummary, txn nodeSlotTransaction) error {
	if err := a.writeDocument(filepath.Join(a.dir, nodeSlotPendingTransactionFileName), txn); err != nil {
		return err
	}
	return a.applyTransaction(summary, txn)
}

func (a *NodeSlotAuthority) recoverTransaction(summary *nodeSlotSummary) error {
	var txn nodeSlotTransaction
	exists, err := readSlotDocument(filepath.Join(a.dir, nodeSlotPendingTransactionFileName), &txn)
	if err != nil || !exists {
		return err
	}
	return a.applyTransaction(summary, txn)
}

func (a *NodeSlotAuthority) recoverLocalSettlements(summary *nodeSlotSummary) error {
	entries, err := os.ReadDir(a.settlementDir())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !validSlotHex(strings.TrimSuffix(entry.Name(), nodeSlotDocumentExtensionText), nodeSlotSHA256HexCharacters) ||
			!strings.HasSuffix(entry.Name(), nodeSlotDocumentExtensionText) || entry.IsDir() {
			return fmt.Errorf(nodeSlotUnexpectedLocalSettlementErrorText)
		}
		var terminal nodeSlotLocalTerminal
		exists, err := readSlotDocument(filepath.Join(a.settlementDir(), entry.Name()), &terminal)
		if err != nil {
			return fmt.Errorf(nodeSlotLocalSettlementUnreadableErrorFormat, err)
		}
		if !exists || !validLocalTerminal(terminal) ||
			entry.Name() != terminal.Lease.ID+nodeSlotDocumentExtensionText {
			return fmt.Errorf(nodeSlotInvalidLocalSettlementErrorText)
		}
		lease, err := a.readLease(terminal.Lease.ID)
		if errors.Is(err, os.ErrNotExist) {
			// A completed WAL may leave its consumed terminal record after a
			// crash. The full startup audit below checks the summary first.
			if err := a.auditLeases(*summary); err != nil {
				return err
			}
			if err := a.removeDocument(a.settlementPath(terminal.Lease.ID)); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf(nodeSlotRetainedLeaseUnreadableErrorFormat, err)
		}
		if !equalNodeSlotLease(lease, terminal.Lease) {
			return fmt.Errorf(nodeSlotLocalSettlementLeaseMismatchErrorText)
		}
		if err := a.finishLocalSettlement(summary, terminal); err != nil {
			return err
		}
	}
	return nil
}

func validLocalTerminal(terminal nodeSlotLocalTerminal) bool {
	completion, intent := terminal.Completion, terminal.Lease.Intent
	return terminal.Lease.ID == nodeSlotLeaseID(intent) && validLocalCompletion(completion) &&
		completion.Run == intent.Run && completion.Task == intent.Task && completion.Attempt == intent.Attempt
}

func (a *NodeSlotAuthority) finishLocalSettlement(summary *nodeSlotSummary,
	terminal nodeSlotLocalTerminal) error {
	if !validLocalTerminal(terminal) {
		return fmt.Errorf(nodeSlotLocalSettlementAttemptMismatchErrorText)
	}
	if err := a.releaseLease(summary, terminal.Lease,
		nodeSlotLocalEvidencePrefixText+terminal.Completion.Status+nodeSlotLocalEvidenceSeparatorText+terminal.Lease.ID); err != nil {
		return err
	}
	return a.removeDocument(a.settlementPath(terminal.Lease.ID))
}

func (a *NodeSlotAuthority) applyTransaction(summary *nodeSlotSummary, txn nodeSlotTransaction) error {
	if txn.Lease.ID != nodeSlotLeaseID(txn.Lease.Intent) || txn.Before < nodeSlotNoOccupiedWeight || txn.After < nodeSlotNoOccupiedWeight ||
		txn.Before > summary.Capacity || txn.After > summary.Capacity ||
		summary.Used != txn.Before && summary.Used != txn.After {
		return fmt.Errorf(nodeSlotInvalidTransactionErrorText)
	}
	switch txn.Kind {
	case nodeSlotReserveTransactionKind:
		if txn.After-txn.Before != txn.Lease.Intent.Weight || txn.Evidence != nodeSlotAbsentIdentityText {
			return fmt.Errorf(nodeSlotInvalidReserveTransactionErrorText)
		}
		if lease, err := a.readLease(txn.Lease.ID); errors.Is(err, os.ErrNotExist) {
			if err := a.writeDocument(a.leasePath(txn.Lease.ID), txn.Lease); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf(nodeSlotRetainedLeaseUnreadableErrorFormat, err)
		} else if !equalNodeSlotLease(lease, txn.Lease) {
			return fmt.Errorf(nodeSlotLeaseConflictErrorText)
		}
	case nodeSlotReleaseTransactionKind:
		if txn.Before-txn.After != txn.Lease.Intent.Weight || txn.Evidence == nodeSlotAbsentIdentityText {
			return fmt.Errorf(nodeSlotInvalidReleaseTransactionErrorText)
		}
		if lease, err := a.readLease(txn.Lease.ID); err == nil && !equalNodeSlotLease(lease, txn.Lease) {
			return fmt.Errorf(nodeSlotReleaseConflictErrorText)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := a.removeDocument(a.leasePath(txn.Lease.ID)); err != nil {
			return err
		}
	default:
		return fmt.Errorf(nodeSlotUnknownTransactionErrorText)
	}
	if summary.Used != txn.After {
		summary.Used = txn.After
		if err := a.writeSummary(*summary); err != nil {
			return err
		}
	}
	return a.removeDocument(filepath.Join(a.dir, nodeSlotPendingTransactionFileName))
}

func (a *NodeSlotAuthority) auditLeases(summary nodeSlotSummary) error {
	leases, err := a.listLeases()
	if err != nil {
		return err
	}
	used := nodeSlotNoOccupiedWeight
	for _, lease := range leases {
		used += lease.Intent.Weight
	}
	if used != summary.Used || used > summary.Capacity {
		return fmt.Errorf(nodeSlotSummaryRetainedLeaseMismatchErrorText)
	}
	return nil
}

func (a *NodeSlotAuthority) listLeases() ([]NodeSlotLease, error) {
	entries, err := os.ReadDir(a.leaseDir())
	if err != nil {
		return nil, err
	}
	leases := make([]NodeSlotLease, nodeSlotInitialLeaseCount, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), nodeSlotDocumentExtensionText) ||
			!validSlotHex(strings.TrimSuffix(entry.Name(), nodeSlotDocumentExtensionText), nodeSlotSHA256HexCharacters) {
			return nil, fmt.Errorf(nodeSlotUnexpectedLeaseEntryErrorText)
		}
		lease, err := a.readLease(strings.TrimSuffix(entry.Name(), nodeSlotDocumentExtensionText))
		if err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

func (a *NodeSlotAuthority) readLease(id string) (NodeSlotLease, error) {
	var lease NodeSlotLease
	exists, err := readSlotDocument(a.leasePath(id), &lease)
	if err != nil {
		return NodeSlotLease{}, err
	}
	if !exists {
		return NodeSlotLease{}, os.ErrNotExist
	}
	if lease.ID != id || id != nodeSlotLeaseID(lease.Intent) || lease.Intent.Weight <= nodeSlotNoOccupiedWeight ||
		lease.Intent.Weight > a.capacity ||
		!validNodeSlotPickup(lease.Intent) ||
		(lease.Intent.Recovery != nil && lease.Intent.NativeRecovery != nil) ||
		(lease.Intent.Recovery != nil && !validNodeSlotRecovery(lease.Intent)) ||
		(lease.Intent.NativeRecovery != nil && !validNodeSlotNativeRecovery(lease.Intent)) {
		return NodeSlotLease{}, fmt.Errorf(nodeSlotInvalidLeaseErrorText)
	}
	return lease, nil
}

func validNodeSlotPickup(intent NodeSlotIntent) bool {
	return intent.PickupID == nodeSlotAbsentIdentityText ||
		validSlotHex(intent.PickupID, choreographyPickupIDHexChars)
}

func validNodeSlotRecovery(intent NodeSlotIntent) bool {
	r := intent.Recovery
	if r == nil {
		return nodeSlotIdentityInvalidState
	}
	expected := r.AssignmentOID
	if r.ExpectedTaskOID != nodeSlotAbsentIdentityText {
		if r.AssignmentOID != nodeSlotAbsentIdentityText ||
			!(validSlotHex(r.GraphRootOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.GraphRootOID, nodeSlotSHA256HexCharacters)) ||
			!validSlotHex(r.TemplateDigest, nodeSlotSHA256HexCharacters) {
			return nodeSlotIdentityInvalidState
		}
		expected = r.ExpectedTaskOID
	}
	return r != nil && r.Binding.valid() && r.Binding.Run == intent.Run && r.Node != nodeSlotAbsentIdentityText &&
		r.CanonicalEndpoint != nodeSlotAbsentIdentityText && r.Kind != nodeSlotAbsentIdentityText &&
		r.TaskBranch == FormatChoreographyTaskBranch(intent.Run, intent.Task) &&
		(validSlotHex(r.BootstrapOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.BootstrapOID, nodeSlotSHA256HexCharacters)) &&
		(validSlotHex(expected, nodeSlotSHA1HexCharacters) || validSlotHex(expected, nodeSlotSHA256HexCharacters)) &&
		(validSlotHex(r.ExpectedLedgerOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.ExpectedLedgerOID, nodeSlotSHA256HexCharacters)) &&
		validSlotHex(r.ProposalDigest, nodeSlotSHA256HexCharacters)
}

func validNodeSlotNativeRecovery(intent NodeSlotIntent) bool {
	r := intent.NativeRecovery
	if r == nil || intent.Attempt != nodeSlotMinimumAttemptCount || !r.Binding.valid() || r.Binding.Run != intent.Run || r.Node == nodeSlotAbsentIdentityText ||
		r.CanonicalEndpoint == nodeSlotAbsentIdentityText || r.Kind == nodeSlotAbsentIdentityText ||
		!(validSlotHex(r.BootstrapOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.BootstrapOID, nodeSlotSHA256HexCharacters)) ||
		!(validSlotHex(r.LedgerRootOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.LedgerRootOID, nodeSlotSHA256HexCharacters)) {
		return nodeSlotIdentityInvalidState
	}
	if r.TaskBranch == nodeSlotAbsentIdentityText {
		return r.ReservationOID == nodeSlotAbsentIdentityText && (r.Kind == KindPublish || r.Kind == nodeSlotNativeRecordKind)
	}
	return r.TaskBranch == FormatChoreographyTaskBranch(intent.Run, intent.Task) &&
		(validSlotHex(r.ReservationOID, nodeSlotSHA1HexCharacters) || validSlotHex(r.ReservationOID, nodeSlotSHA256HexCharacters))
}

func equalNodeSlotLease(a, b NodeSlotLease) bool {
	if a.ID != b.ID || a.Intent.Run != b.Intent.Run || a.Intent.Task != b.Intent.Task ||
		a.Intent.Attempt != b.Intent.Attempt || a.Intent.Weight != b.Intent.Weight ||
		a.Intent.PickupID != b.Intent.PickupID {
		return nodeSlotIdentityInvalidState
	}
	if (a.Intent.Recovery == nil) != (b.Intent.Recovery == nil) ||
		(a.Intent.NativeRecovery == nil) != (b.Intent.NativeRecovery == nil) {
		return nodeSlotIdentityInvalidState
	}
	if a.Intent.Recovery != nil && *a.Intent.Recovery != *b.Intent.Recovery {
		return nodeSlotIdentityInvalidState
	}
	return a.Intent.NativeRecovery == nil ||
		*a.Intent.NativeRecovery == *b.Intent.NativeRecovery
}

func (a *NodeSlotAuthority) readSummary() (nodeSlotSummary, error) {
	var summary nodeSlotSummary
	exists, err := readSlotDocument(filepath.Join(a.dir, nodeSlotSummaryFileName), &summary)
	if err != nil || !exists {
		return summary, err
	}
	if summary.Version != nodeSlotVersion || summary.Node == nodeSlotAbsentIdentityText ||
		!validSlotHex(summary.AuthorityID, nodeSlotAuthorityIDHexCharacters) || summary.Capacity < nodeSlotMinimumWeight ||
		summary.Used < nodeSlotNoOccupiedWeight || summary.Used > summary.Capacity {
		return nodeSlotSummary{}, fmt.Errorf(nodeSlotInvalidSummaryErrorText)
	}
	return summary, nil
}

func (a *NodeSlotAuthority) writeSummary(summary nodeSlotSummary) error {
	return a.writeDocument(filepath.Join(a.dir, nodeSlotSummaryFileName), summary)
}

func (a *NodeSlotAuthority) writeDocument(path string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf(nodeSlotDocumentEncodingErrorFormat, err)
	}
	if len(document) > nodeSlotDocumentLimitBytes {
		return fmt.Errorf(nodeSlotDocumentLimitBytesErrorText)
	}
	if err := fsx.WriteFileAtomic(path, document, nodeSlotPrivateFileMode); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncNodeSlotDir(filepath.Dir(path))
}

func (a *NodeSlotAuthority) writeNewDocument(path string, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf(nodeSlotLocalEvidenceEncodingErrorFormat, err)
	}
	if len(document) > nodeSlotDocumentLimitBytes {
		return fmt.Errorf(nodeSlotLocalEvidenceLimitErrorText)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, nodeSlotPrivateFileMode)
	if err != nil {
		return err
	}
	if _, err := file.Write(document); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncNodeSlotDir(filepath.Dir(path))
}

func (a *NodeSlotAuthority) removeDocument(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncNodeSlotDir(filepath.Dir(path))
}

func readSlotDocument(path string, value any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nodeSlotDocumentUnavailableState, nil
	}
	if err != nil {
		return nodeSlotDocumentUnavailableState, fmt.Errorf(nodeSlotDocumentMetadataUnavailableErrorFormat, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&nodeSlotOtherUsersPermissionMask != nodeSlotNoPermissionBits ||
		info.Size() > nodeSlotDocumentLimitBytes {
		return nodeSlotDocumentUnavailableState, fmt.Errorf(nodeSlotUnsafeDocumentErrorText)
	}
	file, err := os.Open(path)
	if err != nil {
		return nodeSlotDocumentUnavailableState, err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, nodeSlotDocumentLimitBytes+nodeSlotDocumentProbeBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return nodeSlotDocumentUnavailableState, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nodeSlotDocumentUnavailableState, fmt.Errorf(nodeSlotDocumentTrailingDataErrorText)
	}
	return nodeSlotDocumentPresentState, nil
}

func nodeSlotLeaseID(intent NodeSlotIntent) string {
	identity, _ := json.Marshal(struct {
		Run      string `json:"run"`
		Task     string `json:"task"`
		Attempt  int    `json:"attempt"`
		PickupID string `json:"pickupId,omitempty"`
	}{Run: intent.Run, Task: intent.Task, Attempt: intent.Attempt, PickupID: intent.PickupID})
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:])
}

func newNodeSlotAuthorityID() (string, error) {
	var bytes [nodeSlotAuthorityIDBytes]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return nodeSlotAbsentIdentityText, err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validSlotHex(value string, length int) bool {
	if len(value) != length {
		return nodeSlotIdentityInvalidState
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func (a *NodeSlotAuthority) leaseDir() string {
	return filepath.Join(a.dir, nodeSlotLeaseDirectoryName)
}
func (a *NodeSlotAuthority) settlementDir() string {
	return filepath.Join(a.dir, nodeSlotSettlementDirectoryName)
}
func (a *NodeSlotAuthority) leasePath(id string) string {
	return filepath.Join(a.leaseDir(), id+nodeSlotDocumentExtensionText)
}
func (a *NodeSlotAuthority) settlementPath(id string) string {
	return filepath.Join(a.settlementDir(), id+nodeSlotDocumentExtensionText)
}

func requirePrivateSlotDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf(nodeSlotDirectoryMetadataUnavailableErrorFormat, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != nodeSlotNoPermissionBits || info.Mode().Perm()&nodeSlotOtherUsersPermissionMask != nodeSlotNoPermissionBits {
		return fmt.Errorf(nodeSlotDirectoryNotPrivateErrorText)
	}
	return nil
}
