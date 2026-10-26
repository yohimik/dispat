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
		verifyFirst == nil || c.Signer == nil || len(c.Links) == 0 {
		return fmt.Errorf("execution: first-local choreography cannot establish its authority")
	}
	stateDir := filepath.Join(a.returnJournal.dir, "first-local")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("execution: preparing first-local task state: %w", err)
	}
	if err := syncRunDirectory(a.returnJournal.dir); err != nil {
		return fmt.Errorf("execution: persisting first-local task state: %w", err)
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return err
	}
	report := execution.FormatNodeReport("", a.git.GitVersion(context.Background()),
		c.Local.Capacity, c.Limits)
	report.AuthorityID = a.localNodeSlots.AuthorityID()
	slots := a.localNodeSlots
	endpoint := c.Links[0].Endpoint
	c.LocalChoreographer = func(ctx context.Context, assignmentOID string,
		cursor execution.ChoreographyEventCursor) error {
		key := sha256.Sum256([]byte(assignmentOID))
		seenPath := filepath.Join(stateDir, "seen-"+hex.EncodeToString(key[:])+".json")
		seen, err := execution.LoadSeenSet(seenPath, time.Now())
		if err != nil {
			return err
		}
		worker := &execution.Worker{
			Node: c.Local.Name, Endpoint: endpoint, StateDir: stateDir,
			Report: report, Mailbox: mailbox, ReturnSigner: c.Signer,
			NodeSlots: slots, Cache: a.git, Seen: seen, Log: a.log,
			TransferTimeout: time.Duration(a.cfg.Execution.ResolveTransfer().Timeout) * time.Second,
			VerifyOwner: func() error {
				checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return verifyFirst(checkCtx)
			},
		}
		err = worker.RunAssignedLocal(ctx, assignmentOID, cursor, broker)
		if err != nil {
			a.log.Error().Err(err).Str("assignment", assignmentOID).
				Msg("first-local worker could not settle its offered task")
		}
		return err
	}
	return nil
}
