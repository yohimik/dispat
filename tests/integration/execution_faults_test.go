// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Goal 57: the git invocations a distributed run is made of, failed one at a
// time, on whichever of the two machines issues them.
//
// The per-feature scenarios fault the calls their own claim is about. These
// two tables sweep what is left: the calls that prepare an input state, reserve
// task refs, activate an assignment, read what came back and tidy up. They were found
// by running one distributed release at trace level on both sides and reading
// the invocations off the log, so the rows are the shapes the code really
// issues rather than the shapes it looks as though it might.
//
// Every row states the same two things in different words. A call that
// prepares or authorizes work is a call whose failure must stop that work:
// the package fails with the integrity code and nothing of it is published. A
// call that reads what a node said, or that tidies up after it, is a call
// whose failure must cost a poll or a warning and nothing more, because the
// work it is about has already happened somewhere else.
//
// The assertions are codes, exit statuses and git state. No row reads a git
// message: the stand-in reports failures in this suite's own words, which is
// what keeps the tables independent of the git and the operating system they
// run against.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// executionFaultTaskTimeout bounds one task in these fixtures. It is short
// because two of the rows below are about a message that never arrives: the
// run has to wait that bound out before it can report anything, and the
// default is an hour.
const executionFaultTaskTimeout = 25

// A worker that cannot read any canonical task ref can only retire through
// signed Drain and the run's finite admission window, not a task timeout.
const executionFaultUnclaimedAdmissionSeconds = 8

const executionFaultNoClaimExpiredEventKind = "no-claim-expired"
const executionFaultNoClaimSettledEventKind = "no-claim-package-settled"

// newExecutionFaultWorkspace is the fixture the fault tables are driven
// through: the four-package workspace, one node, every build pinned to it.
//
// One node rather than two, because half of these rows count invocations: the
// preflight of a second node would put another probe between the run's start
// and the first call a row is about.
func newExecutionFaultWorkspace(t *testing.T) *executionRig {
	t.Helper()
	return newExecutionWorkspace(t, recordingBuild, executionOneWorker, func(cfg *models.File) {
		cfg.Execution.Timeouts.Task = executionFaultTaskTimeout
		cfg.Execution.Timeouts.Cancel = 20
	})
}

// newExecutionFaultPreparation is the same fixture with a provider this run
// does not release, for the rows about the branch a preparation travels on.
func newExecutionFaultPreparation(t *testing.T) *executionRig {
	t.Helper()
	rig := newExecutionPrepareWorkspace(t, func(_ *harness.Repo, cfg *models.File) {
		cfg.Execution.Workers = cfg.Execution.Workers[:1]
		cfg.Execution.Timeouts.Admission = executionFaultUnclaimedAdmissionSeconds
		cfg.Execution.Timeouts.Task = executionFaultTaskTimeout
		cfg.Execution.Timeouts.Cancel = 20
		cfg.RunOnly = &models.RunOnly{Build: models.RunOnlyWorker, Publish: models.RunOnlyBoth}
	})
	rig.commitTo("the page and the manual use the new asset", "docs", "ui")
	return rig
}

// newExecutionFaultOutputs is the fixture whose packages declare build
// outputs, for the rows about the calls only a capture makes.
func newExecutionFaultOutputs(t *testing.T) *executionRig {
	t.Helper()
	return newExecutionOutputWorkspace(t, executionOneWorker, func(cfg *models.File) {
		cfg.Execution.Timeouts.Task = executionFaultTaskTimeout
		cfg.Execution.Timeouts.Cancel = 20
	})
}

// executionGitFault is one row of either table: which invocation fails, which
// of the matching ones, and what the run is then obliged to do.
type executionGitFault struct {
	// pattern selects the invocation by its arguments.
	pattern string
	// nth is the matching invocation this row fails, and isOnward extends it
	// to every later one. A row about a call that is retried fails exactly one
	// of them; a row about a call the run depends on fails it for good.
	nth      int
	isOnward bool
	// code is the exit status the release must report, which is the whole
	// difference between the halves of each table.
	code int
	// diagnostic is the code the refusal carries: the configuration code for
	// a pool that could not be reached at all, and the integrity code for a
	// task that could not be prepared, offered or executed.
	diagnostic string
	// errorText identifies an early refusal before a task status exists.
	errorText string
	// isRetained expects the transport-cleanup warning rather than a failure.
	isRetained bool
	// noClaim expects signed terminal refusal when no task ref could be read.
	noClaim bool
	// unknownOutcome retains coordination identity when the Git client cannot
	// prove whether an atomic task-and-ledger push was accepted remotely.
	unknownOutcome bool
	// newRig is the fixture this row needs, for the calls a workspace only
	// makes when it prepares a provider or captures a declared output.
	newRig func(*testing.T) *executionRig
	// armAfter names the invocation after which the row's ordinal starts to
	// count, for a call whose earlier matches vary with timing.
	armAfter string
}

// TestExecutionOrchestratorGitFaults: the orchestrator's own git failing
// around the mailbox, in the three ways that matter.
//
// A read that can never succeed is a pool this machine cannot talk to at all,
// so the release refuses before a stage runs anywhere. An input state that
// could not be prepared, and an assignment or a preparation that could not
// be described or activated, are work that was never authorized, so
// the package fails with the integrity code and nothing is published. A fetch
// or a chain walk of what a node already answered is a report about work that
// has already happened, so failing one of them costs a tick and the release
// still ends as a release.
func TestExecutionOrchestratorGitFaults(t *testing.T) {
	for name, row := range map[string]executionGitFault{
		"no message can be resolved at all": {
			pattern: "*cat-file -e*", nth: 1, isOnward: true,
			code: 1, diagnostic: executionRefusalCode,
		},
		"no message can be measured at all": {
			pattern: "*cat-file -s*", nth: 1, isOnward: true,
			code: 1, diagnostic: executionRefusalCode,
		},
		"no message can be read at all": {
			pattern: "*cat-file blob*", nth: 1, isOnward: true,
			code: 1, diagnostic: executionRefusalCode,
		},
		"the input state cannot be committed": {
			pattern: "*commit-tree*dispat transport snapshot*", nth: 1, isOnward: true,
			code: 1, errorText: "the release has no authenticated planning-source snapshot",
		},
		"the assignment cannot be described": {
			// The probe of preflight wrote the first one, so the second is the
			// first assignment this run dispatches work with.
			pattern: "*commit-tree*dispat transport assignment*", nth: 2, isOnward: true,
			code: 1, diagnostic: executionIntegrityCode,
		},
		"the assignment cannot be offered": {
			pattern: "*push*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*",
			nth:     1, isOnward: true,
			code: 1, diagnostic: executionIntegrityCode,
		},
		"the accepted claim cannot be committed": {
			pattern: "*commit-tree*dispat transport choreography-claim*",
			nth:     1, isOnward: true,
			code: 1, errorText: "has no durable receipt after persistence error",
		},
		"a preparation cannot be offered": {
			pattern: "*push*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*",
			nth:     1, isOnward: true,
			code: 1, unknownOutcome: true, newRig: newExecutionFaultPreparation,
		},
		"the answer cannot be retrieved once": {
			pattern: "*fetch*dispat-task-*", nth: 1,
			armAfter: "*push*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*",
		},
		"a node's answer cannot be read once": {
			// Counted from the push that offers the build: how many blobs the
			// preflight probe reads before that depends on when its claim and
			// its result are observed, so the first blob read after the offer
			// is the first blob of a node's answer to the build.
			pattern: "*cat-file blob*", nth: 1,
			armAfter: "*push*--force-with-lease=refs/heads/dispat-task-*:[0-9a-f]*",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := row.rig(t)
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }), 0)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: row.pattern, Nth: row.nth, Onward: row.isOnward, ArmAfter: row.armAfter})

			res := rig.repo.StartReleaseEnv(rig.env(fault.Env()...), "release",
				"--return-endpoint", rig.returnURL).WaitForDiagnostic(90*time.Second, func() {
				t.Logf("first-node Git fault signed ledger: %+v", executionLateJoinLedgerEvents(t, rig.mailbox))
			})
			reply := stopAll(t, []*executionWorker{worker})[0]

			require.Equal(t, row.code, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			if row.unknownOutcome {
				assert.True(t, harness.IsCodePresent(executionEvents(res), executionRetainedCode),
					"an unknown task-and-ledger push retains its run for exact reconciliation\nstdout:\n%s", res.Stdout)
				assert.True(t, remoteHoldsLock(t, rig.origin), "the unresolved run keeps its owned release lock")
				assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-task-"),
					"canonical task refs remain inspectable")
				assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
					"the signed ledger remains inspectable")
				assert.Empty(t, executionReleasedPackages(res), "no package was published")
				return
			}
			assert.Equal(t, 0, reply.Code, "the node carried on serving")
			if row.code == 0 {
				assert.NotEmpty(t, rig.repo.TagList(), "a call that is retried costs a tick and nothing else")
				assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
					"the completed run retains its signed ledger for recovery")
				return
			}
			if row.diagnostic != "" {
				assert.True(t, harness.IsCodePresent(executionEvents(res), row.diagnostic),
					"the run is refused with %s\nstdout:\n%s", row.diagnostic, res.Stdout)
			}
			if row.errorText != "" {
				assert.Contains(t, res.Stdout, row.errorText)
			}
			assert.Empty(t, executionReleasedPackages(res), "nothing was published")
			if row.diagnostic == executionRefusalCode {
				assert.Empty(t, rig.runs(), "and no stage of the release ran anywhere")
			}
		})
	}
}

// rig is the fixture one row runs against, which is the four-package
// workspace unless the row asked for another.
func (f executionGitFault) rig(t *testing.T) *executionRig {
	t.Helper()
	if f.newRig != nil {
		return f.newRig(t)
	}
	return newExecutionFaultWorkspace(t)
}

// executionReleasedPackages are the packages a run published, read off the
// summary lines rather than off a tag list: a fixture whose packages carry a
// baseline tag already has tags before the run starts.
func executionReleasedPackages(res harness.RunResult) []string {
	var published []string
	for _, event := range executionEvents(res) {
		if event.Str("message") == "summary" && event.Str("status") == "published" {
			published = append(published, event.Package())
		}
	}
	return published
}

// TestExecutionWorkerTransportGitFaults: a node's own git failing around the
// task it was given, past the calls its poll loop and its checkout are made of.
//
// The halves are the same two obligations seen from the other machine. An
// assigned input that could not be fetched and a package folder that could not
// be resolved are work that never started,
// so the run is told and the package fails; the administrative record of a
// checkout that could not be tidied away is disk rather than correctness, so
// the release is still a release. Either way the node stays up: a failure of
// one task is not a failure of the machine.
func TestExecutionWorkerTransportGitFaults(t *testing.T) {
	for name, row := range map[string]executionGitFault{
		"the assignment input cannot be fetched": {
			pattern: "*fetch*dispat-task-*", nth: 1, isOnward: true, code: 1, noClaim: true,
			newRig: func(t *testing.T) *executionRig {
				return newExecutionPushOutcomeRig(t, func(cfg *models.File) {
					cfg.Execution.Timeouts.Task = executionFaultUnclaimedAdmissionSeconds
					cfg.Execution.Timeouts.Admission = executionFaultUnclaimedAdmissionSeconds
				})
			},
		},
		"the package folder cannot be resolved": {
			pattern: "*rev-parse --verify --end-of-options*", nth: 1, isOnward: true, code: 1,
			newRig: newExecutionFaultOutputs,
		},
		"a message cannot be read once": {
			pattern: "*cat-file blob*", nth: 1,
		},
		"the checkout records cannot be pruned": {
			pattern: "*worktree prune*", nth: 1, isOnward: true, isRetained: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := row.rig(t)
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: row.pattern, Nth: row.nth, Onward: row.isOnward})
			worker := rig.startWorker(executionWorkerConfig(rig.mailbox,
				func(settings *models.ExecutionConfig) { settings.Concurrency = models.Int(4) }),
				0, fault.Env()...)

			res := rig.release()
			reply := stopAll(t, []*executionWorker{worker})[0]

			require.Equal(t, row.code, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Positive(t, fault.Matches(), "the fault reached the invocation it names")
			assert.Equal(t, 0, reply.Code, "the node carried on serving after its git failed")
			if row.code == 0 && !row.isRetained {
				assert.NotEmpty(t, rig.repo.TagList(),
					"a read the node makes again costs a tick and nothing else")
				assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
					"the completed run retains its signed ledger for recovery")
				return
			}
			if row.isRetained {
				assert.True(t, harness.IsCodePresent(executionEvents(reply), executionRetainedCode),
					"the node reported what it could not tidy away\nstdout:\n%s", reply.Stdout)
				assert.NotEmpty(t, rig.repo.TagList(), "and the release is still a release")
				return
			}
			if row.noClaim {
				var expired, settled bool
				for _, event := range executionLateJoinLedgerEvents(t, rig.mailbox) {
					if event.Kind == executionFaultNoClaimExpiredEventKind {
						expired = true
						assert.Empty(t, event.PackageOutcome, "task refusal precedes package settlement")
					}
					if event.Kind == executionFaultNoClaimSettledEventKind && expired {
						settled = true
					}
				}
				assert.True(t, expired && settled,
					"unreadable canonical task refs end with a signed no-claim and package settlement")
				assert.Empty(t, executionReleasedPackages(res), "nothing was published")
				return
			}
			assert.True(t, harness.IsCodePresent(executionEvents(res), executionIntegrityCode),
				"the affected package is refused with the integrity code\nstdout:\n%s", res.Stdout)
			assert.Empty(t, executionReleasedPackages(res), "nothing was published")
			assert.NotEmpty(t, executionProtocolRefs(t, rig.mailbox, "dispat-ledger-"),
				"the failed run retains its signed ledger for recovery")
		})
	}
}
