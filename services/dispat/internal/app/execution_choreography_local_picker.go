package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

const (
	compiledLocalPickerStateError     = "execution: first-node compiled picker has no protected state"
	compiledLocalPickerSeenPrefix     = "seen-compiled-"
	compiledLocalPickerStateFolder    = "first-local-compiled"
	compiledLocalPickerSeenFileSuffix = ".json"
	compiledLocalPickerAutoDetectedOS = ""
	compiledLocalPickerStateDirMode   = 0o700
	compiledLocalPickerDoneBufferSize = 1
	compiledLocalFinalityGracePeriod  = 30 * time.Second
	compiledLocalGraphIncompleteError = "execution: first-node compiled graph has nonterminal executable tasks at picker shutdown"
)

// startCompiledLocalPicker begins first-node self-pick after run gating hooks.
// It shares the durable NodeSlot authority with executor-owned native work.
func (a *App) startCompiledLocalPicker(ctx context.Context, c *execution.Coordinator,
	broker *execution.FirstNodeBroker) (context.Context, func() error, error) {
	if a == nil || a.returnJournal == nil || a.localNodeSlots == nil ||
		c == nil || broker == nil || c.Signer == nil || c.CanonicalEndpoint == choreographyAbsentIdentityText {
		return nil, nil, fmt.Errorf(compiledLocalPickerStateError)
	}
	stateDir := filepath.Join(a.returnJournal.dir, compiledLocalPickerStateFolder)
	if err := os.MkdirAll(stateDir, compiledLocalPickerStateDirMode); err != nil {
		return nil, nil, err
	}
	if err := syncRunDirectory(a.returnJournal.dir); err != nil {
		return nil, nil, err
	}
	key := sha256.Sum256([]byte(c.Run))
	seenPath := filepath.Join(stateDir, compiledLocalPickerSeenPrefix+hex.EncodeToString(key[:])+compiledLocalPickerSeenFileSuffix)
	seen, err := execution.LoadSeenSet(seenPath, time.Now())
	if err != nil {
		return nil, nil, err
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return nil, nil, err
	}
	report := execution.FormatNodeReport(compiledLocalPickerAutoDetectedOS, a.git.GitVersion(ctx), c.Local.Capacity, c.Limits)
	report.AuthorityID = a.localNodeSlots.AuthorityID()
	worker := &execution.Worker{Node: c.Local.Name, Endpoint: c.CanonicalEndpoint,
		StateDir: stateDir, Report: report, Mailbox: mailbox, Cache: a.git,
		ReturnSigner: c.Signer, NodeSlots: a.localNodeSlots, Seen: seen, Log: a.log,
		PrepareInterpretedRun: a.interpretedRunPlanner(c.Local.Name, c.Signer),
		TransferTimeout:       time.Duration(a.cfg.Execution.ResolveTransfer().Timeout) * time.Second}
	pickerCtx, cancel := context.WithCancelCause(ctx)
	done := make(chan error, compiledLocalPickerDoneBufferSize)
	go func() {
		err := worker.RunCompiledLocal(pickerCtx, broker, c.BootstrapOID, c.GraphRootOID)
		if err != nil {
			cancel(err)
		}
		done <- err
	}()
	var stopOnce sync.Once
	var stopErr error
	stop := func() error {
		stopOnce.Do(func() {
			if ctx.Err() == nil && pickerCtx.Err() == nil && broker.IsCompiledGraphTerminal() {
				bound := max(c.Timeouts.Cancel, worker.TransferTimeout) + compiledLocalFinalityGracePeriod
				settleCtx, end := context.WithTimeout(ctx, bound)
				stopErr = worker.WaitCompiledLocalFinality(settleCtx, c.Run)
				end()
			} else if ctx.Err() == nil && pickerCtx.Err() == nil {
				stopErr = fmt.Errorf(compiledLocalGraphIncompleteError)
			}
			cancel(nil)
			stopErr = errors.Join(stopErr, <-done)
		})
		return stopErr
	}
	return pickerCtx, stop, nil
}
