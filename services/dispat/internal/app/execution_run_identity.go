package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// reopenActiveFirstRunIdentity takes the same OS ownership on a retained run
// and authenticates its protected certificate. It makes no remote claim:
// callers must separately verify every prepared lock, bootstrap and ledger
// before binding the return handler or starting another effect.
func reopenActiveFirstRunIdentity(ctx context.Context,
	git *gitx.LocalGitx) (*firstRunJournal, firstRunIdentity, error) {
	if git == nil {
		return nil, firstRunIdentity{}, fmt.Errorf("execution: first-node Git store is absent")
	}
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return nil, firstRunIdentity{}, err
	}
	run, err := readActiveRun(filepath.Join(gitDir, "dispat", "return-runs", "active-run"))
	if err != nil || run == "" {
		return nil, firstRunIdentity{}, err
	}
	journal, err := openFirstRunJournal(gitDir, run)
	if err != nil {
		return nil, firstRunIdentity{}, err
	}
	identity, err := journal.read()
	if err != nil {
		_ = journal.close()
		return nil, firstRunIdentity{}, err
	}
	return journal, identity, nil
}

// prepareFirstRunIdentity is before release-lock acquisition and any sweep
// preflight. The listener binds later, using this exact durable TLS identity.
func (a *App) prepareFirstRunIdentity(ctx context.Context, endpoint, listen string) error {
	if !a.cfg.Execution.IsDistributed() {
		return nil
	}
	if endpoint == "" || a.runID == "" || a.returnJournal != nil {
		return execution.NewDiagnostic(execution.CodeConfiguration, execution.CategoryConfiguration,
			"distributed first-node run has no return endpoint or a repeated identity")
	}
	bind, err := ValidateReturnAddress(endpoint, listen)
	if err != nil || bind == "" {
		return fmt.Errorf("execution: first-node return listen address is invalid: %w", err)
	}
	gitDir, err := a.git.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("execution: resolving private Git directory for first-node identity: %w", err)
	}
	journal, err := openFirstRunJournal(gitDir, a.runID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(journal.dir, "identity.json")); err == nil {
		_ = journal.close()
		return fmt.Errorf("execution: first-node run %s already has an identity; resume or reconcile it", a.runID)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = journal.close()
		return err
	}
	certificate, pin, err := newRunReturnCertificate(endpoint)
	if err != nil {
		_ = journal.abandonUnwritten()
		_ = journal.close()
		return fmt.Errorf("execution: creating first-node return identity: %w", err)
	}
	leaf, key, err := encodeFirstRunCertificate(certificate)
	if err != nil {
		_ = journal.abandonUnwritten()
		_ = journal.close()
		return err
	}
	if err := journal.write(firstRunIdentity{Run: a.runID, Endpoint: endpoint, Listen: bind,
		CertPin: pin, CertificatePEM: leaf, PrivateKeyPEM: key}); err != nil {
		_ = journal.abandonUnwritten()
		_ = journal.close()
		return err
	}
	a.returnJournal = journal
	return nil
}

// finishFirstRunIdentity closes the owner lock only after an exact cleanup
// decision. An unresolved ref or lock leaves the active locator for recovery.
func (a *App) finishFirstRunIdentity(safe bool) error {
	journal := a.returnJournal
	if journal == nil {
		return nil
	}
	var err error
	if safe && a.localNodeSlots != nil {
		checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		held, readErr := a.localNodeSlots.Held(checkCtx)
		cancel()
		if readErr != nil {
			safe = false
			err = fmt.Errorf("execution: first-node capacity cannot be inspected before retirement: %w", readErr)
		} else {
			for _, lease := range held {
				if lease.Intent.Run == journal.run {
					safe = false
					err = fmt.Errorf("execution: first-node run %s retains task %s capacity without settled evidence",
						journal.run, lease.Intent.Task)
					break
				}
			}
		}
	}
	if safe {
		err = errors.Join(err, journal.markCompleted())
	} else {
		err = errors.Join(err, fmt.Errorf("execution: first-node run %s remains active for exact reconciliation", journal.run))
	}
	err = errors.Join(err, journal.close())
	a.returnJournal = nil
	return err
}
