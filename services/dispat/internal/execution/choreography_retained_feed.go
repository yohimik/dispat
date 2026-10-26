// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	retainedFeedReadOnlyErrorMessage              = "execution: retained choreography feed cannot mutate signed state"
	retainedFeedInitializationErrorMessage        = "execution: retained choreography feed lacks exact complete initialization"
	retainedFeedIdentityErrorMessage              = "execution: incomplete retained choreography feed identity"
	retainedFeedBootstrapMismatchError            = "execution: retained feed bootstrap belongs to another run"
	retainedFeedUnavailableErrorMessage           = "execution: retained choreography feed is unavailable"
	retainedFeedEpochUnavailableError             = "execution: retained choreography epoch is unavailable"
	retainedFeedMissingOID                        = ""
	retainedFeedMinimumHistoryEntries             = 1
	retainedFeedInitializationHistoryIndex        = 0
	retainedFeedMinimumDocumentBytes       int64  = 1
	retainedFeedMinimumTagBytes            int64  = 1
	retainedFeedUnknownEpochSequence       uint64 = 0
	retainedFeedEpochNotFound              bool   = false
)

// RetainedChoreographyFeed reads signed canonical finality after the first
// node's HTTPS service has closed. It exposes no Git mutation capability.
// One opener replays the run once and then pages every held local lease from
// the same indexed history.
type RetainedChoreographyFeed struct {
	reader           *GitChoreographyPersist
	maxDocumentBytes int64
}

type RetainedChoreographyFeedOptions struct {
	Mailbox          *GitMailbox
	Binding          ChoreographyRunBinding
	BootstrapOID     string
	MaxDocumentBytes int64
	MaxTagBytes      int64
}

// RetainedChoreographyIdentity is a cold, authenticated snapshot of the
// bootstrap, complete graph, initial ledger event, and roster. Callers may
// reconstruct state from it without replanning task placement or templates.
type RetainedChoreographyIdentity struct {
	Bootstrap                 RunBootstrap
	Carrier                   ChoreographyGraphCarrier
	GraphRootOID              string
	InitializationCompleteOID string
	Ledger                    ChoreographyLedgerReservation
	Roster                    ChoreographyRuntimeRoster
}

// ReadRetainedChoreographyIdentity verifies the same complete graph and
// canonical ref inventory as compiled pickup before a cold reader replays
// any accepted task finality.
func ReadRetainedChoreographyIdentity(ctx context.Context,
	opts RetainedChoreographyFeedOptions) (RetainedChoreographyIdentity, error) {
	if opts.Mailbox == nil || !opts.Binding.valid() || opts.BootstrapOID == retainedFeedMissingOID ||
		opts.MaxDocumentBytes < retainedFeedMinimumDocumentBytes ||
		opts.MaxTagBytes < retainedFeedMinimumTagBytes {
		return RetainedChoreographyIdentity{}, fmt.Errorf(retainedFeedIdentityErrorMessage)
	}
	ledger, roster, err := ReadChoreographyLedgerRoot(ctx, opts.Mailbox, opts.Binding, opts.BootstrapOID)
	if err != nil {
		return RetainedChoreographyIdentity{}, err
	}
	bootstrap, err := opts.Mailbox.ReadChoreographyBootstrap(ctx, opts.BootstrapOID,
		opts.MaxDocumentBytes, opts.MaxTagBytes)
	if err != nil {
		return RetainedChoreographyIdentity{}, err
	}
	if bootstrap.ChoreographyRunBinding != opts.Binding ||
		bootstrap.CoordinationEndpoint != opts.Mailbox.endpoint {
		return RetainedChoreographyIdentity{}, fmt.Errorf(retainedFeedBootstrapMismatchError)
	}
	carrier, graphRootOID, err := opts.Mailbox.ReadChoreographyGraphCarrier(ctx,
		opts.Binding, opts.BootstrapOID, opts.MaxDocumentBytes)
	if err != nil {
		return RetainedChoreographyIdentity{}, err
	}
	if carrier.AlgorithmVersion != bootstrap.AlgorithmVersion {
		return RetainedChoreographyIdentity{}, fmt.Errorf(retainedFeedInitializationErrorMessage)
	}
	ledgerTip, err := opts.Mailbox.readRemoteTip(ctx, ledger.Branch)
	if err != nil {
		return RetainedChoreographyIdentity{}, err
	}
	history, err := opts.Mailbox.plumbing.FirstParentOIDs(ctx, ledgerTip, ledger.OID)
	if err != nil {
		return RetainedChoreographyIdentity{}, err
	}
	if len(history) < retainedFeedMinimumHistoryEntries {
		return RetainedChoreographyIdentity{}, fmt.Errorf(retainedFeedInitializationErrorMessage)
	}
	return RetainedChoreographyIdentity{Bootstrap: bootstrap, Carrier: carrier,
		GraphRootOID: graphRootOID, InitializationCompleteOID: history[retainedFeedInitializationHistoryIndex],
		Ledger: ledger, Roster: roster}, nil
}

func OpenRetainedChoreographyFeed(ctx context.Context,
	opts RetainedChoreographyFeedOptions) (*RetainedChoreographyFeed, error) {
	identity, err := ReadRetainedChoreographyIdentity(ctx, opts)
	if err != nil {
		return nil, err
	}
	readOnly := func(context.Context) error { return fmt.Errorf(retainedFeedReadOnlyErrorMessage) }
	reader, err := NewGitChoreographyPersist(GitChoreographyOptions{
		Binding: opts.Binding, Mailbox: opts.Mailbox, Ledger: identity.Ledger,
		BootstrapOID: opts.BootstrapOID, GraphRootOID: identity.GraphRootOID,
		InitializationCompleteOID: identity.InitializationCompleteOID,
		ReturnEndpoint:            identity.Bootstrap.ReturnEndpoint,
		ReturnCertSHA256:          identity.Bootstrap.ReturnCertSHA256,
		JoinPolicy:                identity.Bootstrap.JoinPolicy,
		AlgorithmVersion:          identity.Bootstrap.AlgorithmVersion,
		Tasks:                     identity.Carrier.Tasks, Templates: identity.Carrier.Templates,
		MaxPackBytes: opts.MaxTagBytes, VerifyFirst: readOnly,
		VerifyResult: func(context.Context, ChoreographyResultProposal, Result, *gitx.LocalGitx) error {
			return fmt.Errorf(retainedFeedReadOnlyErrorMessage)
		},
	})
	if err != nil {
		return nil, err
	}
	if _, err := reader.LedgerHead(ctx); err != nil {
		return nil, err
	}
	return &RetainedChoreographyFeed{reader: reader,
		maxDocumentBytes: opts.MaxDocumentBytes}, nil
}

func (f *RetainedChoreographyFeed) ReadEvents(ctx context.Context,
	cursor ChoreographyEventCursor, maxEvents int) (ChoreographyEventPage, error) {
	if f == nil || f.reader == nil {
		return ChoreographyEventPage{}, fmt.Errorf(retainedFeedUnavailableErrorMessage)
	}
	return f.reader.ReadEvents(ctx, cursor, maxEvents)
}

func (f *RetainedChoreographyFeed) Head(ctx context.Context) (ChoreographyEventCursor, error) {
	if f == nil || f.reader == nil {
		return ChoreographyEventCursor{}, fmt.Errorf(retainedFeedUnavailableErrorMessage)
	}
	return f.reader.LedgerHead(ctx)
}

// SequenceForOID answers whether an epoch is in this run's authenticated
// first-parent ledger. The initial opener has already replayed that history;
// later calls extend the same index once and never rescan it per held lease.
func (f *RetainedChoreographyFeed) SequenceForOID(ctx context.Context,
	oid string) (uint64, bool, error) {
	if f == nil || f.reader == nil || oid == retainedFeedMissingOID {
		return retainedFeedUnknownEpochSequence, retainedFeedEpochNotFound,
			fmt.Errorf(retainedFeedEpochUnavailableError)
	}
	g := f.reader
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.syncLedgerLocked(ctx); err != nil {
		return retainedFeedUnknownEpochSequence, retainedFeedEpochNotFound, err
	}
	sequence, found := g.eventIndex[oid]
	return sequence, found, nil
}
