package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// choreographyFirstCheck is passed only to the first node's Git persistence.
// The protected local locator must still identify this exact bootstrap and
// ledger, and a release must still hold its verified remote lock generation.
// A sweep has no release lock; its exclusive local run gate and remote CAS
// generation are its authority boundary.
func (a *App) choreographyFirstCheck(coordinator *execution.Coordinator,
	ledgerRootOID string) (func(context.Context) error, error) {
	journal := a.returnJournal
	if coordinator == nil || journal == nil || ledgerRootOID == choreographyAbsentIdentityText {
		return nil, fmt.Errorf(choreographyIncompleteFirstOwnershipErrorText)
	}
	if err := checkChoreographyJournal(journal, coordinator, ledgerRootOID); err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkChoreographyJournal(journal, coordinator, ledgerRootOID); err != nil {
			return err
		}
		if a.ownership != nil {
			return a.ownership.Check(ctx)
		}
		return nil
	}, nil
}

func checkChoreographyJournal(journal *firstRunJournal,
	coordinator *execution.Coordinator, ledgerRootOID string) error {
	if journal == nil || journal.lock == nil || journal.gate == nil ||
		coordinator == nil || journal.run != coordinator.Run {
		return fmt.Errorf(choreographyMissingJournalOwnerErrorText)
	}
	identity, err := journal.read()
	if err != nil {
		return fmt.Errorf(choreographyUnverifiedFirstIdentityErrorFormat, err)
	}
	if identity.Run != coordinator.Run || identity.Generation != coordinator.Generation ||
		identity.BootstrapOID != coordinator.BootstrapOID ||
		identity.LedgerRootOID != ledgerRootOID ||
		identity.Endpoint != coordinator.ReturnEndpoint ||
		identity.CertPin != coordinator.ReturnCertSHA256 {
		return fmt.Errorf(choreographyRetainedJournalMismatchErrorText)
	}
	return nil
}
