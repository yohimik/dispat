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
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// Link is one configured worker node: what it is called, and the mailbox it
// reads its work from.
type Link struct {
	Name              string
	Endpoint          string
	LateJoin          bool
	IdentityPublicKey string
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
	// Set before preflight when remote workers participate. These
	// fields enter the signed probe and bind its HTTPS return destination.
	ReturnEndpoint    string
	ReturnCertSHA256  string
	GraphDigest       string
	Mode              string
	AlgorithmVersion  string
	JoinPolicy        ChoreographyJoinPolicy
	GraphRootOID      string
	CanonicalEndpoint string
	canonicalMailbox  *GitMailbox
	// PinnedSources are captured before signed preflight. PinnedInputs adds
	// exact ref and planner-option evidence to those source commitments.
	PinnedSources *PinnedRunSources
	PinnedInputs  *PinnedRunInputs
	// BootstrapOID is retained by v3 task and ledger roots after probe cleanup.
	BootstrapOID string
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
	// reservations are created after preflight and before any task frame.
	reservations *reservationState
	// choreographyInitialized fences a second v3 initial batch after a
	// partial or ambiguous create. Close settles any refs the run owns.
	choreographyInitialized bool
	choreographySlots       *reservationState
	choreographyBroker      *FirstNodeBroker
	choreographyPersist     *GitChoreographyPersist
	choreographyRequests    sync.Map // task name -> choreographyResultExpectation
	choreographyPending     sync.Map // task name -> *choreographyPendingDecision
	choreographySummary     sync.Map // task name -> choreographyTaskSummary
	choreographyReadyFrames sync.Map // task name -> first-node compiled ready frame
	nativeWriterLanes       sync.Map // repository name -> *sync.Mutex
	// LocalChoreographer executes one already durable v3 assignment through
	// the same signed claim/result return path as a remote peer.
	LocalChoreographer func(context.Context, string, ChoreographyEventCursor) error

	// mailboxes is one open mailbox per link, opened by the caller so that
	// this type never decides what a local object store is.
	mailboxes map[string]*GitMailbox

	// owned are the refs this run created, with the value it last wrote, and
	// the mutex that lets several probes record theirs at once. One owner:
	// nothing else in the process knows what this run put on a mailbox.
	mu           sync.Mutex
	probeReplies sync.Mutex
	owned        map[string][]gitx.BranchLease
	// ownership is what this run asks before every new assignment, and the
	// cancellations a lost lock has to reach. It is the release's own gate,
	// borrowed through UseOwnership, and nil for a run that holds no lock.
	ownership *OwnershipGate
	// taskRecords and timings are what the run summary is printed from: what
	// every placed task came to, and what each of them spent queueing and
	// working. They are accumulated here because the coordinator is the one
	// party that sees every task of the run.
	taskRecords []TaskRecord
	timings     map[string]taskTiming

	// What Start assembles and Close takes down. They are nil on a coordinator
	// that only ever preflighted, which is what a refused run is.
	dispatch Dispatch
	// guard is the snapshot guard: shared by the frames that write the working
	// tree, exclusive while one is captured.
	guard sync.RWMutex
	// local is this node's own capacity, as a slot per frame it keeps.
	local chan struct{}
	// snapshots is what this run has already captured for assignment carriers.
	snapshots *snapshots
	// outputs is what every package of this run produced, as it was admitted.
	outputs *outputRegistry
	// sweepOutputs is what the tasks of a command sweep reported under the
	// sweep's own roots, held until every task has answered (§28.10).
	sweepOutputs *sweepOutputs
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

// SetCanonicalChoreographyMailbox fixes the one Git CAS authority independently
// of the initially admitted peers. A run with only late joiners still needs
// this store before it creates its signed bootstrap and task roots.
func (c *Coordinator) SetCanonicalChoreographyMailbox(endpoint string, mailbox *GitMailbox) error {
	if c == nil || mailbox == nil || c.BootstrapOID != "" || c.choreographyInitialized ||
		endpoint == "" || mailbox.endpoint != endpoint {
		return fmt.Errorf("execution: canonical choreography mailbox is unavailable or was set after bootstrap")
	}
	for _, link := range c.Links {
		if link.Endpoint != endpoint {
			return fmt.Errorf("execution: worker %q names a different coordination endpoint", link.Name)
		}
	}
	c.CanonicalEndpoint, c.canonicalMailbox = endpoint, mailbox
	if c.mailboxes == nil {
		c.mailboxes = make(map[string]*GitMailbox)
	}
	if existing := c.mailboxes[c.Local.Name]; existing != nil && existing != mailbox {
		return fmt.Errorf("execution: first-node mailbox identity collides with a peer link")
	}
	c.mailboxes[c.Local.Name] = mailbox
	return nil
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
//
// Before any of that, the run asks whether it still owns what it locked
// (§28.3): a probe is the first thing of this run that exists on another
// machine, and a run that lost its lock while it planned must not reach one.
// A sweep holds no lock and no gate, so for it the question is no question.
func (c *Coordinator) Preflight(ctx context.Context, packages []PackagePlatforms) error {
	if err := c.checkOwnership(ctx); err != nil {
		return err
	}
	activeLinks := make([]Link, 0, len(c.Links))
	for _, link := range c.Links {
		if !link.LateJoin {
			activeLinks = append(activeLinks, link)
		}
	}
	reports := make([]*NodeReport, len(activeLinks))
	failures := make([]error, len(activeLinks))
	var waiting sync.WaitGroup
	for index, link := range activeLinks {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			// A panic reading what a node wrote is that node failing
			// preflight, and never the process: the mailbox is written to by
			// other machines.
			defer func() {
				if recovered := recover(); recovered != nil {
					c.Log.Error().Str("worker", link.Name).Str("panic", fmt.Sprint(recovered)).
						Msg("probing the node panicked")
					failures[index] = fmt.Errorf("probing the node failed unexpectedly: %v", recovered)
				}
			}()
			probeCtx, done := context.WithTimeout(ctx, c.Timeouts.Preflight)
			defer done()
			report, err := c.probe(probeCtx, link)
			reports[index], failures[index] = report, err
		}()
	}
	waiting.Wait()
	for index, link := range activeLinks {
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
	c.Pool = NewPool(activeLinks, reports, c.Local, c.Log)
	return nil
}

// probe offers one node a probe and waits for its report.
func (c *Coordinator) probe(ctx context.Context, link Link) (*NodeReport, error) {
	mailbox := c.mailboxes[link.Name]
	branch := FormatBranch(link.Name, KindProbe, time.Now())
	assignment := &Assignment{
		Header: Header{
			Protocol: c.probeProtocol(), Kind: KindProbe, Run: c.Run,
			PlanDigest: c.PlanDigest, Task: PreflightTask, Attempt: 1,
			Generation: c.Generation, Node: link.Name, Branch: branch,
			IssuedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Limits: c.Limits, ReturnEndpoint: c.ReturnEndpoint,
		ReturnCertSHA256: c.ReturnCertSHA256, GraphDigest: c.GraphDigest,
		DistributionMode: c.Mode,
		BootstrapOID:     c.BootstrapOID,
		PinnedInputs:     c.PinnedInputs,
	}
	var offered string
	var err error
	if c.PinnedSources != nil {
		assignment.Repositories = append([]AssignmentRepository(nil), c.PinnedSources.Repositories...)
		parents := c.PinnedSources.CommitOIDs()
		if c.BootstrapOID != "" {
			parents = append(parents, c.BootstrapOID)
		}
		offered, err = mailbox.PreparePinnedProbe(ctx, assignment, parents)
	} else {
		offered, err = mailbox.PrepareAssignment(ctx, assignment)
	}
	if err != nil {
		return nil, err
	}
	// Owned before it is pushed, so a probe whose push has no known outcome
	// still leaves a branch this run closes.
	c.recordOwnedRef(ctx, ownedRefStep{node: link.Name, branch: branch, oid: offered})
	resolution, err := mailbox.Offer(ctx, branch, offered)
	if resolution == pushNotLanded {
		c.forgetOwnedRef(link.Name, branch)
		return nil, err
	}
	// Landed, or not known to have: a probe that reached the node is answered
	// within the preflight wait like any other, and one that did not costs
	// that wait and refuses the node.
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
	// The authenticated HTTPS probe return handler may already have reread
	// this tip to settle an unknown reply. Observe would suppress that same
	// unchanged OID from the preflight waiter, so use an exact fresh read.
	head, err := mailbox.Reread(ctx, branch)
	if err != nil {
		return nil, err
	}
	heads := []gitx.RemoteHead{head}
	for _, head := range heads {
		if head.OID == "" {
			continue
		}
		tip, err := mailbox.Inspect(ctx, head)
		if err != nil {
			return nil, err
		}
		if tip.Kind == MessageClaim {
			// A node can claim the probe and then fail before writing its
			// report. Its signed claim is still this run's cleanup boundary;
			// an unverified branch movement is not.
			waiting := &attemptState{offered: offered, assignment: &Assignment{Header: Header{
				Kind: KindProbe, Run: c.Run, PlanDigest: c.PlanDigest,
				Task: PreflightTask, Attempt: 1, Generation: c.Generation,
			}}}
			observer := watcher{coordinator: c, link: link, mailbox: mailbox}
			if reason := observer.readClaim(ctx, tip, waiting); reason == "" {
				c.recordOwnedRef(ctx, ownedRefStep{node: link.Name, branch: branch,
					oid: tip.OID, parent: tip.PreviousOID})
			} else {
				observer.reportRejectedReply(tip, reason)
			}
			continue
		}
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
		c.recordOwnedRef(ctx, ownedRefStep{node: link.Name, branch: branch,
			oid: tip.OID, parent: tip.PreviousOID})
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
	if reason := CheckHeaderVersion(HeaderVersionCheck{Header: result.Header,
		Binding: Binding{Node: link.Name, Branch: tip.Branch}, Now: time.Now(), Version: c.probeProtocol()}); reason != "" {
		return reason
	}
	if tip.Previous != MessageClaim {
		return ReasonChain
	}
	if result.Assignment != offered || result.Kind != KindProbe ||
		result.Run != c.Run || result.PlanDigest != c.PlanDigest ||
		result.Task != PreflightTask || result.Attempt != 1 ||
		result.Generation != c.Generation {
		return ReasonReplay
	}
	if result.Report == nil {
		return ReasonUnreadable
	}
	return ""
}

// checkReport is what a node has to be for this run to place work on it.
func (c *Coordinator) checkReport(report *NodeReport) error {
	if report.Protocol != c.probeProtocol() {
		return fmt.Errorf("the node speaks protocol %d and this run speaks %d: a node that does not implement the same protocol cannot be sent work",
			report.Protocol, c.probeProtocol())
	}
	if report.Capacity < 1 {
		return fmt.Errorf("the node reports a capacity of %d: a node that accepts no task cannot be sent work", report.Capacity)
	}
	if c.Mode != "" && !validSlotHex(report.AuthorityID, 32) {
		return fmt.Errorf("the worker has no persistent shared slot authority identity")
	}
	for _, ceiling := range []struct {
		key     string
		node    int64
		orderer int64
	}{
		{"transfer.maxFiles", int64(report.Limits.MaxFiles), int64(c.Limits.MaxFiles)},
		{"transfer.maxBytes", report.Limits.MaxBytes, c.Limits.MaxBytes},
		{"transfer.maxManifestBytes", report.Limits.MaxManifestBytes, c.Limits.MaxManifestBytes},
		{"transfer.maxPackBytes", report.Limits.MaxPackBytes, c.Limits.MaxPackBytes},
	} {
		if ceiling.node < ceiling.orderer {
			return fmt.Errorf("the node's %s is %d and this run's is %d: a node that would refuse what this run transfers cannot be sent work",
				ceiling.key, ceiling.node, ceiling.orderer)
		}
	}
	return nil
}

// CheckChoreographyJoinReport applies the same capability floor as eager
// preflight before a late node may enter the signed membership ledger.
func (c *Coordinator) CheckChoreographyJoinReport(report *NodeReport) error {
	if c == nil || report == nil || report.OS == "" || report.Arch == "" {
		return fmt.Errorf("execution: joining node has no toolchain or platform identity")
	}
	if err := c.checkReport(report); err != nil {
		return err
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
		if wanted.Placement == PlacementAnyNode &&
			isPlatformCompatible(runtime.GOOS+nodePlatformSeparator+runtime.GOARCH, wanted.Platforms) {
			continue
		}
		// A choreographer-only task may remain inert until an authorized
		// future node proves its platform at signed admission.
		if wanted.Placement == PlacementWorker {
			lateAllowed := false
			for _, link := range c.Links {
				lateAllowed = lateAllowed || link.LateJoin
			}
			if lateAllowed {
				continue
			}
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

// ownedRefStep is a branch value this run created or an authenticated worker
// message it accepted. parent is the message's first parent when known.
type ownedRefStep struct {
	node, branch, oid, parent string
}

// recordOwnedRef advances a cleanup lease only along the branch's first-parent
// chain. A delayed poll may arrive after a cancel or acknowledgement was
// recorded; it must not replace the newer lease with the older observed tip.
func (c *Coordinator) recordOwnedRef(ctx context.Context, step ownedRefStep) {
	step.node = c.ownerNode(step.node, step.branch)
	for retry := 0; retry <= maxChainDepth; retry++ {
		c.mu.Lock()
		index := -1
		for candidate, lease := range c.owned[step.node] {
			if lease.Branch == step.branch {
				index = candidate
				break
			}
		}
		if index < 0 {
			if retry == 0 {
				c.owned[step.node] = append(c.owned[step.node], gitx.BranchLease{
					Branch: step.branch, ExpectedOld: step.oid,
				})
			}
			c.mu.Unlock()
			return
		}
		current := c.owned[step.node][index].ExpectedOld
		if current == step.oid {
			c.mu.Unlock()
			return
		}
		if current == step.parent {
			c.owned[step.node][index].ExpectedOld = step.oid
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		// A skipped protocol message needs a bounded first-parent proof. Do the
		// Git reads outside the coordinator lock: another node's bookkeeping
		// must not wait for this node's object store.
		isLater, err := c.isFirstParentSuccessor(ctx, step.node, current, step.oid)
		c.mu.Lock()
		index = -1
		for candidate, lease := range c.owned[step.node] {
			if lease.Branch == step.branch {
				index = candidate
				break
			}
		}
		if index < 0 {
			c.mu.Unlock()
			return // Close already took the owned refs.
		}
		if c.owned[step.node][index].ExpectedOld != current {
			c.mu.Unlock()
			continue
		}
		if err == nil && isLater {
			c.owned[step.node][index].ExpectedOld = step.oid
		}
		c.mu.Unlock()
		if err != nil {
			c.Log.Warn().Err(err).Str("branch", step.branch).
				Msg("the cleanup lease could not be advanced")
		} else if !isLater {
			c.Log.Debug().Str("branch", step.branch).Str("commit", step.oid).
				Msg("an older coordination tip cannot replace the cleanup lease")
		}
		return
	}
	c.Log.Warn().Str("branch", step.branch).
		Msg("the cleanup lease changed too often to record this coordination tip")
}

// ownerNode gives v2 refs one cleanup owner per exact endpoint string.
func (c *Coordinator) ownerNode(node, branch string) string {
	if !strings.HasPrefix(branch, reservationBranchPrefix) &&
		!strings.HasPrefix(branch, slotBranchPrefix) {
		return node
	}
	endpoint := c.endpointOf(node)
	for _, link := range c.Links {
		if link.Endpoint == endpoint {
			return link.Name
		}
	}
	return node
}

// isFirstParentSuccessor checks only the bounded coordination chain, not a
// merge parent: cleanup ownership follows the exact branch history.
func (c *Coordinator) isFirstParentSuccessor(ctx context.Context, node, earlier, candidate string) (bool, error) {
	for depth := 0; depth < maxChainDepth; depth++ {
		if candidate == earlier {
			return true, nil
		}
		parent, err := c.mailboxes[node].plumbing.ResolveCommit(ctx, candidate+"^")
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// An existing root commit has no first parent. Check that the
			// candidate itself is readable before treating this as a normal
			// stale observation rather than an object-store failure.
			if _, inspectErr := c.mailboxes[node].plumbing.ResolveCommit(ctx, candidate); inspectErr != nil {
				return false, inspectErr
			}
			return false, nil
		}
		candidate = parent
	}
	return candidate == earlier, nil
}

// Close deletes settled refs this run created and reports any ref still present.
func (c *Coordinator) Close(ctx context.Context) error {
	if c.snapshots != nil {
		defer c.snapshots.closeBaselines()
	}
	// Bounded here whatever the caller handed in: close runs detached from the
	// run's cancellation, and a mailbox that hangs must not keep a finished
	// run, and the locks it gives back afterwards, waiting for ever. Every
	// batch still gets its attempt within the bound, and the fetched refs are
	// removed on a bound of their own.
	ctx, done := context.WithTimeout(ctx, c.resolveCloseBound())
	defer done()
	if c.choreographyBroker != nil {
		return c.closeChoreography(ctx)
	}
	if c.choreographyInitialized {
		// The v3 roots may already be visible remotely. Without an attached
		// broker there is no signed stop/seal decision, so v2 cleanup cannot
		// certify that deleting those refs is safe.
		return fmt.Errorf("execution: initialized choreography has no attached broker; canonical refs are retained")
	}
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
	type retainedRef struct{ node, branch string }
	var retained []retainedRef
	var failures []error
	for _, node := range sortedNodes(owned) {
		outcomes, err := c.mailboxes[node].Close(ctx, owned[node])
		if err != nil {
			failures = append(failures, err)
		}
		for _, outcome := range outcomes {
			if outcome.Result != gitx.BranchDeleted {
				retained = append(retained, retainedRef{node: node, branch: outcome.Branch})
			}
		}
	}
	// Distinct URLs may address the same Git repository. An earlier endpoint
	// can report a stale lease before a later endpoint removes that exact ref.
	// Only an absent final ref settles the alias without claiming it was ours.
	remaining := retained[:0]
	for _, ref := range retained {
		heads, err := c.mailboxes[ref.node].remote.ListRemoteHeads(ctx,
			c.endpointOf(ref.node), "refs/heads/"+ref.branch)
		if err != nil {
			remaining = append(remaining, ref)
			continue
		}
		present := false
		for _, head := range heads {
			present = present || head.Name == ref.branch
		}
		if present {
			remaining = append(remaining, ref)
		}
	}
	retained = remaining
	if len(retained) == 0 && len(failures) == 0 {
		return nil
	}
	names := make([]string, 0, len(retained)+len(failures))
	for _, ref := range retained {
		names = append(names, ref.branch)
	}
	names = append(names, formatFailures(failures)...)
	return NewIdentifiedDiagnostic(Identity{Run: c.Run}, CodeTransportRetained, CategoryTransportCleanup,
		"%d coordination branches of this run could not be closed (%v): they carry no release record; inspect each current tip and ownership before deletion",
		len(retained)+len(failures), names)
}

// minimumCloseBound is the least a run's close is given, for a run whose cancel
// wait is shorter than one round trip to a slow remote. A variable so that a
// test can wait out a hanging mailbox in less than that.
var minimumCloseBound = 30 * time.Second

// resolveCloseBound is how long a run's close may take: the run's own cancel
// wait, which is the bound it already holds every other closing write to, and
// never less than one slow round trip.
func (c *Coordinator) resolveCloseBound() time.Duration {
	return max(c.Timeouts.Cancel, minimumCloseBound)
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
