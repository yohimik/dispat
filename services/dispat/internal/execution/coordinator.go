// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// The orchestrator's side of one distributed run.
//
// This build does one thing with it, and it is the thing §28.2 requires
// before anything else: validate the effective configuration, the protocol
// compatibility and the transfer capability of every configured node before
// dispatch. A node that cannot answer, answers a version this run does not
// speak, cannot run what this run would place on it, or would move less than
// this run's tasks are held to, fails the release before a single stage has
// run, rather than being dropped silently or discovered halfway through.
//
// The coordinator is also the one owner of the refs this run creates. Every
// branch it offers is remembered here and nowhere else, so closing the run is
// a question with one answer.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Link is one configured worker node: what it is called, and the mailbox it
// reads its work from.
type Link struct {
	Name     string
	Endpoint string
}

// PackagePlatforms is one releasing package, the node platforms its build may
// run on and where the run is allowed to place it. An empty platform list is
// every platform.
type PackagePlatforms struct {
	Package   string
	Platforms []string
	Placement Placement
}

// Coordinator runs one release's side of the protocol: it names the run,
// offers work to the configured nodes and owns every ref it created.
type Coordinator struct {
	// Run names this run, and is what every message of it carries.
	Run string
	// PlanDigest names the fixed plan (§28.3).
	PlanDigest string
	// Generation names the ownership the run holds, derived from the release
	// locks it acquired.
	Generation string
	// Links are the configured worker nodes.
	Links []Link
	// Local is this machine's own place in the pool: what it calls itself,
	// and how much of its own run it may execute (§28.1). It is carried
	// rather than derived because the name is the one every log line and
	// every manifest of this node already uses.
	Local LocalNode
	// Signer signs what this run writes into a mailbox, and what it writes
	// into its own object store for a worker to read: a build placed here
	// produces a result a consuming node verifies exactly as it verifies a
	// worker's, so the orchestrator signs one too.
	Signer *Signer
	// Timeouts and Limits are the run's own bounds, as configured.
	Timeouts Timeouts
	Limits   TransferLimits
	// Log is the run logger.
	Log zerolog.Logger
	// Pool is where this run places its work, assembled by Preflight from what
	// the nodes reported about themselves.
	Pool *Pool

	// mailboxes is one open mailbox per link, opened by the caller so that
	// this type never decides what a local object store is.
	mailboxes map[string]*GitMailbox

	// owned are the refs this run created, with the value it last wrote, and
	// the mutex that lets several probes record theirs at once. One owner:
	// nothing else in the process knows what this run put on a mailbox.
	mu    sync.Mutex
	owned map[string][]gitx.BranchLease

	// What Start assembles and Close takes down. They are nil on a coordinator
	// that only ever preflighted, which is what a refused run is.
	dispatch Dispatch
	// guard is the snapshot guard: shared by the frames that write the working
	// tree, exclusive while one is captured.
	guard sync.RWMutex
	// local is this node's own capacity, as a slot per frame it keeps.
	local chan struct{}
	// snapshots is what this run has already captured, and offered is what it
	// has already pushed to which node, with the mutex that lets several
	// dispatches share both.
	snapshots *snapshots
	offers    sync.Mutex
	offered   map[string]offeredState
	// outputs is what every package of this run produced, as it was admitted,
	// and what has already been relayed to which endpoint.
	outputs *outputRegistry
	// preparing guards both fields below it: the providers this run builds
	// without releasing them, and what became of each. One owner, because
	// "has anybody started this provider" and "start it" have to be one
	// decision however many consumers ask at once.
	preparing       sync.Mutex
	preparations    map[string]*preparation
	preparedRecords []PreparedRecord
	// watchers is one poller per endpoint, with the cancellation and the wait
	// that stop them.
	watchers     map[string]*watcher
	stopWatching context.CancelFunc
	watching     sync.WaitGroup
}

// offeredState is one prepared input state as one node has already been given
// it: which commit, and the immutable branch it was put on.
type offeredState struct {
	commit string
	branch string
}

// Timeouts are the bounded waits a distributed run makes, in the shape this
// package uses them. They are a type of this package rather than the
// configuration's own so that a caller assembling a coordinator states the
// waits it means rather than passing a settings object through.
type Timeouts struct {
	Preflight time.Duration
	Task      time.Duration
	Cancel    time.Duration
}

// NewCoordinator assembles one run's coordinator over already-open mailboxes,
// one per link name.
//
// The mailboxes are handed in rather than opened here because what a local
// object store is depends on who is asking: a release's own repository for an
// orchestrator, a bare cache for a serving node. Keeping that decision at the
// caller is what lets this type be exercised over any transport.
func NewCoordinator(run, planDigest, generation string, local LocalNode, links []Link,
	mailboxes map[string]*GitMailbox, signer *Signer, timeouts Timeouts, limits TransferLimits,
	log zerolog.Logger) *Coordinator {
	return &Coordinator{
		Run: run, PlanDigest: planDigest, Generation: generation, Local: local, Links: links,
		Timeouts: timeouts, Limits: limits, Log: log, Signer: signer,
		mailboxes: mailboxes, owned: map[string][]gitx.BranchLease{},
	}
}

// Preflight asks every configured node what it is, and refuses the run when
// any of them cannot take the work this plan would place on it.
//
// Every node is asked at once, because the point of the check is to be paid
// once rather than once per node, and each answer is bounded by its own
// deadline so that one unreachable machine costs the configured preflight
// wait and not the sum of them. The platform question is asked afterwards,
// across the whole set: whether a package can be built is a property of the
// pool rather than of any one node.
func (c *Coordinator) Preflight(ctx context.Context, packages []PackagePlatforms) error {
	reports := make([]*NodeReport, len(c.Links))
	failures := make([]error, len(c.Links))
	var waiting sync.WaitGroup
	for index, link := range c.Links {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			probeCtx, done := context.WithTimeout(ctx, c.Timeouts.Preflight)
			defer done()
			report, err := c.probe(probeCtx, link)
			reports[index], failures[index] = report, err
		}()
	}
	waiting.Wait()
	for index, link := range c.Links {
		if failures[index] != nil {
			return c.refuse(link.Name, failures[index])
		}
		if err := c.checkReport(reports[index]); err != nil {
			return c.refuse(link.Name, err)
		}
		c.Log.Info().Str("worker", link.Name).Str("os", reports[index].OS).
			Str("arch", reports[index].Arch).Int("capacity", reports[index].Capacity).
			Str("dispat", reports[index].Dispat).Str("run", c.Run).Msg("worker ready")
	}
	if err := c.checkPlatforms(packages, reports); err != nil {
		return err
	}
	// The pool is what the reports were collected for: a node's capacity and
	// its platform are the node's own statements about itself, and the only
	// moment this run hears them is here.
	c.Pool = NewPool(c.Links, reports, c.Local, c.Log)
	return nil
}

// probe offers one node a probe and waits for its report.
func (c *Coordinator) probe(ctx context.Context, link Link) (*NodeReport, error) {
	mailbox := c.mailboxes[link.Name]
	branch := FormatBranch(link.Name, KindProbe, time.Now())
	assignment := &Assignment{
		Header: Header{
			Protocol: ProtocolVersion, Kind: KindProbe, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: PreflightTask, Attempt: 1,
			Generation: c.Generation, Node: link.Name, Branch: branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Limits: c.Limits,
	}
	offered, err := mailbox.Assign(ctx, assignment)
	if err != nil {
		return nil, err
	}
	c.recordOwnedRef(link.Name, branch, offered)
	return c.awaitReport(ctx, link, branch, offered)
}

// awaitReport polls one branch until the node has reported, the deadline has
// passed or the run was interrupted.
//
// Anything on the branch that is not this node's authentic result leaves the
// wait exactly as it was. That is deliberate: a reply nobody signed, a reply
// bound to another attempt and a node that has not answered yet are one
// situation from here, which is "no report", and the deadline is what decides
// how long that is tolerated.
func (c *Coordinator) awaitReport(ctx context.Context, link Link, branch, offered string) (*NodeReport, error) {
	interval := minimumPollInterval
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the node to report: %w", ctx.Err())
		case <-next.C:
		}
		interval = min(interval*2, maximumPollInterval)
		next.Reset(interval)
		report, err := c.readReport(ctx, link, branch, offered)
		if err != nil {
			return nil, err
		}
		if report != nil {
			return report, nil
		}
	}
}

// readReport looks once at what a probe branch now carries, and answers the
// node's report when it is there.
func (c *Coordinator) readReport(ctx context.Context, link Link, branch, offered string) (*NodeReport, error) {
	mailbox := c.mailboxes[link.Name]
	heads, err := mailbox.Observe(ctx, "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	for _, head := range heads {
		tip, err := mailbox.Inspect(ctx, head)
		if err != nil {
			return nil, err
		}
		c.recordOwnedRef(link.Name, branch, head.OID)
		if tip.Kind != MessageResult {
			continue
		}
		report, reason, err := c.readResult(ctx, link, tip, offered)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			// A reply nobody signed, or one bound to another attempt, is
			// logged and left where it is: from here it is indistinguishable
			// from a node that has not answered, and the deadline is what
			// decides how long that is tolerated.
			c.Log.Warn().Str("worker", link.Name).Str("branch", tip.Branch).Str("commit", tip.OID).
				Str("reason", string(reason)).Str("code", CodeAuthority).
				Str("category", CategoryAuthority).Msg("result rejected")
			continue
		}
		return report, nil
	}
	return nil, nil
}

// readResult verifies one reply and answers the report it carries, the reason
// it is not this run's, or the transport failure that stopped the read.
func (c *Coordinator) readResult(ctx context.Context, link Link, tip ChainTip, offered string) (*NodeReport, RejectReason, error) {
	document, err := c.mailboxes[link.Name].Read(ctx, tip, c.Limits.MaxManifestBytes)
	if err != nil {
		if reason := RejectionReason(err); reason != "" {
			return nil, reason, nil
		}
		return nil, "", err
	}
	var result Result
	if err := json.Unmarshal(document, &result); err != nil {
		return nil, ReasonUnreadable, nil
	}
	if reason := c.checkResult(result, link, tip, offered); reason != "" {
		return nil, reason, nil
	}
	return result.Report, "", nil
}

// checkResult holds a node's reply to the same rules the node holds an
// assignment to, plus the two the orchestrator alone can ask: that the reply
// answers the assignment this run offered, and that it describes the node at
// all.
func (c *Coordinator) checkResult(result Result, link Link, tip ChainTip, offered string) RejectReason {
	if reason := CheckHeader(result.Header,
		Binding{Node: link.Name, Branch: tip.Branch}, time.Now()); reason != "" {
		return reason
	}
	if result.Assignment != offered || result.Run != c.Run || result.Task != PreflightTask {
		return ReasonReplay
	}
	if result.Report == nil {
		return ReasonUnreadable
	}
	return ""
}

// checkReport is what a node has to be for this run to place work on it.
func (c *Coordinator) checkReport(report *NodeReport) error {
	if report.Protocol != ProtocolVersion {
		return fmt.Errorf("the node speaks protocol %d and this run speaks %d: a node that does not implement the same protocol cannot be sent work",
			report.Protocol, ProtocolVersion)
	}
	if report.Capacity < 1 {
		return fmt.Errorf("the node reports a capacity of %d: a node that accepts no task cannot be sent work", report.Capacity)
	}
	for _, ceiling := range []struct {
		key     string
		node    int64
		orderer int64
	}{
		{"transfer.maxFiles", int64(report.Limits.MaxFiles), int64(c.Limits.MaxFiles)},
		{"transfer.maxBytes", report.Limits.MaxBytes, c.Limits.MaxBytes},
		{"transfer.maxManifestBytes", report.Limits.MaxManifestBytes, c.Limits.MaxManifestBytes},
	} {
		if ceiling.node < ceiling.orderer {
			return fmt.Errorf("the node's %s is %d and this run's is %d: a node that would refuse what this run transfers cannot be sent work",
				ceiling.key, ceiling.node, ceiling.orderer)
		}
	}
	return nil
}

// checkPlatforms asks whether every package this run would build has a node
// that could build it. It is asked of the pool rather than of one node,
// because a package is placed on whichever compatible node is free.
func (c *Coordinator) checkPlatforms(packages []PackagePlatforms, reports []*NodeReport) error {
	for _, wanted := range packages {
		if wanted.Placement == PlacementOrchestrator {
			// A package pinned to this machine is never offered to a worker,
			// so what the workers run says nothing about whether this run can
			// build it. Whether this machine satisfies it is the pool's
			// question, asked where the frame is placed.
			continue
		}
		if c.isPlacementPossible(wanted.Platforms, reports) {
			continue
		}
		return NewIdentifiedDiagnostic(Identity{Run: c.Run, Task: wanted.Package},
			CodeConfiguration, CategoryConfiguration,
			"%s declares buildPlatforms %v and no configured worker node runs one of them: the run would have nowhere to build it",
			wanted.Package, wanted.Platforms)
	}
	return nil
}

// isPlacementPossible reports whether at least one node satisfies a package's
// declared platforms.
func (c *Coordinator) isPlacementPossible(platforms []string, reports []*NodeReport) bool {
	for _, report := range reports {
		if report.IsPlatformSatisfied(platforms) {
			return true
		}
	}
	return false
}

// refuse is the one diagnostic a failed preflight produces: the node it is
// about, and what it could not be.
func (c *Coordinator) refuse(node string, err error) error {
	return NewIdentifiedDiagnostic(Identity{Run: c.Run, Worker: node, Task: PreflightTask, Attempt: 1},
		CodeConfiguration, CategoryConfiguration,
		"worker node %s did not pass preflight: %w", node, err)
}

// recordOwnedRef remembers a ref this run created, and the value it was last
// seen at, so that closing the run deletes exactly what it put there and
// under a lease that is current.
func (c *Coordinator) recordOwnedRef(node, branch, oid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, lease := range c.owned[node] {
		if lease.Branch == branch {
			c.owned[node][index].ExpectedOld = oid
			return
		}
	}
	c.owned[node] = append(c.owned[node], gitx.BranchLease{Branch: branch, ExpectedOld: oid})
}

// Close deletes every ref this run created, in one push per node, and
// answers what could not be removed.
//
// A ref left behind is untidy rather than unsafe: the branches this run owns
// carry coordination messages and nothing a release depends on, so a failed
// close is a warning with the retained code and never a failed release. The
// run's own error, when it has one, stays the run's error.
func (c *Coordinator) Close(ctx context.Context) error {
	if c.stopWatching != nil {
		// The pollers go first: a poll that ran while the refs were being
		// deleted would be fetching objects nobody owns any more.
		c.stopWatching()
		c.watching.Wait()
	}
	c.mu.Lock()
	owned := c.owned
	c.owned = map[string][]gitx.BranchLease{}
	c.mu.Unlock()
	var retained []string
	var failures []error
	for _, node := range sortedNodes(owned) {
		outcomes, err := c.mailboxes[node].Close(ctx, owned[node])
		if err != nil {
			failures = append(failures, err)
		}
		for _, outcome := range outcomes {
			if !outcome.IsDeleted {
				retained = append(retained, outcome.Branch)
			}
		}
	}
	if len(retained) == 0 && len(failures) == 0 {
		return nil
	}
	return NewIdentifiedDiagnostic(Identity{Run: c.Run}, CodeTransportRetained, CategoryTransportCleanup,
		"%d coordination branches of this run could not be closed (%v): they carry no release record and can be deleted at any time",
		len(retained)+len(failures), append(retained, formatFailures(failures)...))
}

// formatFailures is what a batch that could not be pushed at all contributes
// to the retained report: the failure's own sentence, which already names the
// endpoint redacted.
func formatFailures(failures []error) []string {
	texts := make([]string, 0, len(failures))
	for _, failure := range failures {
		texts = append(texts, failure.Error())
	}
	return texts
}

// sortedNodes is the node names of a map in a stable order, so that a run
// closing several mailboxes does it the same way twice.
func sortedNodes(owned map[string][]gitx.BranchLease) []string {
	names := make([]string, 0, len(owned))
	for name := range owned {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
