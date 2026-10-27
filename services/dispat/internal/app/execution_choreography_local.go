package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// bindFirstLocalWorker sends an offered first-node command through the
// same signed claim/result broker as a peer. Each concurrent offer gets its
// own Worker state, while all offers share the protected run directory, the
// canonical object store, and the physical node-slot authority. No local
// task needs to call the advertised HTTPS endpoint or push a Git ref.
func (a *App) bindFirstLocalWorker(c *execution.Coordinator,
	broker *execution.FirstNodeBroker, verifyFirst func(context.Context) error) error {
	if a.returnJournal == nil || a.localNodeSlots == nil || c == nil || broker == nil ||
		verifyFirst == nil || c.Signer == nil || len(c.Links) == choreographyNoEntriesCount {
		return fmt.Errorf(choreographyMissingLocalAuthorityErrorText)
	}
	stateDir := filepath.Join(a.returnJournal.dir, choreographyLocalStateDirectoryName)
	if err := os.MkdirAll(stateDir, choreographyLocalDirectoryMode); err != nil {
		return fmt.Errorf(choreographyLocalStateCreationErrorFormat, err)
	}
	if err := syncRunDirectory(a.returnJournal.dir); err != nil {
		return fmt.Errorf(choreographyLocalStatePersistenceErrorFormat, err)
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return err
	}
	report := execution.FormatNodeReport(choreographyAbsentIdentityText, a.git.GitVersion(context.Background()),
		c.Local.Capacity, c.Limits)
	report.AuthorityID = a.localNodeSlots.AuthorityID()
	slots := a.localNodeSlots
	endpoint := c.Links[choreographyFirstEndpointIndex].Endpoint
	c.LocalChoreographer = func(ctx context.Context, assignmentOID string,
		cursor execution.ChoreographyEventCursor) error {
		key := sha256.Sum256([]byte(assignmentOID))
		seenPath := filepath.Join(stateDir, choreographySeenFilePrefixText+hex.EncodeToString(key[:])+choreographySeenFileExtensionText)
		seen, err := execution.LoadSeenSet(seenPath, time.Now())
		if err != nil {
			return err
		}
		worker := &execution.Worker{
			Node: c.Local.Name, Endpoint: endpoint, StateDir: stateDir,
			Report: report, Mailbox: mailbox, ReturnSigner: c.Signer,
			NodeSlots: slots, Cache: a.git, Seen: seen, Log: a.log,
			PrepareInterpretedRun: a.interpretedRunPlanner(c.Local.Name, c.Signer),
			TransferTimeout:       time.Duration(a.cfg.Execution.ResolveTransfer().Timeout) * time.Second,
			VerifyOwner: func() error {
				checkCtx, cancel := context.WithTimeout(context.Background(), choreographyOwnershipCheckTimeoutSeconds*time.Second)
				defer cancel()
				return verifyFirst(checkCtx)
			},
		}
		err = worker.RunAssignedLocal(ctx, assignmentOID, cursor, broker)
		if err != nil {
			a.log.Error().Err(err).Str(choreographyAssignmentLogKey, assignmentOID).
				Msg(choreographyLocalTaskUnsettledLogText)
		}
		return err
	}
	return nil
}
