package app

import (
	"context"
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

const fatalChoreographyBindingLogMessage = "signed compiled task binding failed"

type choreographyBindOptions struct {
	Coordinator         *execution.Coordinator
	ReturnChannel       *runReturnChannel
	Tasks               []execution.ChoreographyTaskSpec
	Templates           []execution.ChoreographyCompiledTemplate
	PackagePolicies     []execution.ChoreographyPackagePolicy
	SemanticGraphDigest string
	Budgets             []execution.ChoreographyRuntimeBudget
	OutcomePolicy       execution.ChoreographyOutcomePolicyx
	// ResolveBindingsFactory starts from the exact signed ledger root after
	// complete graph publication. It may replay accepted results on demand.
	ResolveBindingsFactory func(*execution.GitChoreographyPersist, string) (
		func(context.Context, string, string) (execution.ChoreographyTaskBindings, error), error)
}

// initializeAndBindChoreography is the first-node handoff after preflight.
// It publishes the one canonical task/ledger batch, settles exact readback,
// then gives the HTTPS return handler a broker. Before the last step every
// task request receives a bounded refusal, so no frame can race an inert ref.
func (a *App) initializeAndBindChoreography(ctx context.Context,
	opts choreographyBindOptions) (*execution.FirstNodeBroker, error) {
	c := opts.Coordinator
	if c == nil || opts.ReturnChannel == nil || a.returnJournal == nil ||
		len(opts.Tasks) == 0 || len(opts.Budgets) == 0 {
		return nil, fmt.Errorf("execution: choreography runtime is incomplete after preflight")
	}
	// Before broker attachment, a failed initial batch can have an ambiguous
	// remote effect. The active run locator stays until exact reconciliation or
	// a bound broker has durably stopped and sealed all accepted attempts.
	a.choreographyRetained = true
	initialized, err := c.InitializeChoreographyTasks(ctx, execution.ChoreographyInitialization{
		BootstrapOID: c.BootstrapOID, Tasks: opts.Tasks, Templates: opts.Templates,
		PackagePolicies: opts.PackagePolicies, SemanticGraphDigest: opts.SemanticGraphDigest,
		Budgets: opts.Budgets,
		OnPreparedLedger: func(ledger execution.ChoreographyLedgerReservation) error {
			return a.returnJournal.recordLedgerRoot(ledger.OID)
		},
	})
	if err != nil {
		return nil, taskInitializationFailure(err)
	}
	if err := c.RegisterCompiledChoreographyResults(initialized.Templates); err != nil {
		return nil, err
	}
	mailbox, err := c.ChoreographyMailbox()
	if err != nil {
		return nil, err
	}
	verifyFirst, err := a.choreographyFirstCheck(c, initialized.Ledger.OID)
	if err != nil {
		return nil, err
	}
	persist, err := execution.NewGitChoreographyPersist(execution.GitChoreographyOptions{
		Binding: execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
			PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest},
		Mailbox: mailbox, VerifyFirst: verifyFirst,
		VerifyResult: c.VerifyChoreographyResult, MaxPackBytes: c.Limits.MaxPackBytes,
		Ledger: initialized.Ledger, BootstrapOID: c.BootstrapOID,
		InitializationCompleteOID: initialized.InitializationCompleteOID,
		GraphRootOID:              c.GraphRootOID, Templates: initialized.Templates,
		ReturnEndpoint: c.ReturnEndpoint, ReturnCertSHA256: c.ReturnCertSHA256,
		JoinPolicy: c.JoinPolicy, AlgorithmVersion: c.AlgorithmVersion,
		Tasks: initialized.Tasks, OnRefAdvance: c.ChoreographyRefAdvanced,
	})
	if err != nil {
		return nil, err
	}
	if opts.ResolveBindingsFactory == nil {
		return nil, fmt.Errorf("execution: complete compiled graph has no signed effective-input resolver")
	}
	resolveBindings, err := opts.ResolveBindingsFactory(persist, initialized.Ledger.OID)
	if err != nil {
		return nil, err
	}
	nodeCapacity := make(map[string]int)
	for _, node := range c.ChoreographyNodes() {
		if node.Name == "" || node.Capacity <= 0 || node.AuthorityID == "" {
			return nil, fmt.Errorf("execution: preflighted node has no persistent slot authority")
		}
		nodeCapacity[node.Name] = node.Capacity
	}
	budgets := make(map[string]int, len(opts.Budgets))
	for _, budget := range opts.Budgets {
		if budget.Name == "" || budget.Capacity <= 0 {
			return nil, fmt.Errorf("execution: invalid retained choreography budget")
		}
		budgets[budget.Name] = budget.Capacity
	}
	repositoryByTask := make(map[string]string, len(opts.Templates))
	for _, template := range opts.Templates {
		if template.Package != nil {
			repositoryByTask[template.Task] = template.Package.Repository
		}
	}
	broker, err := execution.NewFirstNodeBroker(execution.FirstNodeBrokerOptions{
		Binding: execution.ChoreographyRunBinding{Run: c.Run, Generation: c.Generation,
			PlanDigest: c.PlanDigest, GraphDigest: c.GraphDigest},
		GraphRootOID: c.GraphRootOID, InitialMembershipEpochOID: initialized.InitializationCompleteOID,
		JoinPolicy: c.JoinPolicy,
		FirstNode:  c.Local.Name,
		Tasks:      initialized.Tasks, Templates: initialized.Templates,
		Members: c.ChoreographyNodes(), NodeCapacity: nodeCapacity, Budgets: budgets,
		ResolveBindings: resolveBindings,
		OnFatalBinding: func(bindingErr error) {
			c.Log.Error().Err(bindingErr).Msg(fatalChoreographyBindingLogMessage)
		},
		OnClaimAccepted: func(spec execution.ChoreographyTaskSpec,
			bindings execution.ChoreographyTaskBindings) {
			c.CacheAcceptedChoreographyBuildSnapshot(spec, bindings, repositoryByTask[spec.Task])
		},
		ValidateJoinReport: c.CheckChoreographyJoinReport,
		OutcomePolicy:      opts.OutcomePolicy, Persist: persist,
	})
	if err != nil {
		return nil, err
	}
	if err := c.AttachChoreography(persist, broker); err != nil {
		return nil, err
	}
	a.choreographyBound = true
	if err := a.bindFirstLocalWorker(c, broker, verifyFirst); err != nil {
		return nil, err
	}
	if err := a.bindTaskReturnHandler(c, broker, opts.ReturnChannel); err != nil {
		return nil, err
	}
	return broker, nil
}
