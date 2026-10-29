// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

const (
	packBenchmarkNode              = "node-a"
	packBenchmarkTask              = "artifact:run"
	packBenchmarkRoot              = "artifacts"
	packBenchmarkCapacity          = 1
	packBenchmarkAttempt           = 1
	packBenchmarkSmallBytes        = 4096
	packBenchmarkOpaqueFiles       = 6
	packBenchmarkOpaqueBytes int64 = 147422480
	packBenchmarkObjectPage        = 32
)

type packBenchmarkInput struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path,omitempty"`
}

func packBenchmarkInputs(t testing.TB, scenario string) []packBenchmarkInput {
	t.Helper()
	var inputs []packBenchmarkInput
	root := os.Getenv(packBenchmarkPayloadEnv)
	if scenario == packBenchmarkSmall {
		root = t.TempDir()
		body := bytes.Repeat([]byte("small output record\n"), packBenchmarkSmallBytes)
		body = body[:packBenchmarkSmallBytes]
		sum := sha256.Sum256(body)
		require.NoError(t, os.WriteFile(filepath.Join(root, "output.txt"), body, packBenchmarkFileMode))
		inputs = []packBenchmarkInput{{Name: "output.txt", Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}
	} else {
		require.NotEmpty(t, root, "opaque payload must be supplied explicitly; it is never downloaded or executed")
		inventory := os.Getenv(packBenchmarkInventoryEnv)
		require.NotEmpty(t, inventory)
		raw, err := os.ReadFile(inventory)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &inputs))
		require.Len(t, inputs, packBenchmarkOpaqueFiles)
	}
	seen := map[string]bool{}
	var total int64
	for i := range inputs {
		input := &inputs[i]
		require.NotEmpty(t, input.Name)
		require.Equal(t, filepath.Base(input.Name), input.Name)
		require.NotEqual(t, ".", input.Name)
		require.NotEqual(t, "..", input.Name)
		require.False(t, seen[input.Name])
		seen[input.Name] = true
		input.Path = filepath.Join(root, input.Name)
		info, err := os.Lstat(input.Path)
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
		require.Equal(t, input.Bytes, info.Size())
		require.GreaterOrEqual(t, input.Bytes, int64(0))
		file, err := os.Open(input.Path)
		require.NoError(t, err)
		hash := sha256.New()
		n, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		require.NoError(t, readErr)
		require.NoError(t, closeErr)
		require.Equal(t, input.Bytes, n)
		require.Equal(t, input.SHA256, hex.EncodeToString(hash.Sum(nil)))
		total += n
	}
	if scenario == packBenchmarkOpaque {
		require.Equal(t, packBenchmarkOpaqueBytes, total)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return inputs
}

type packBenchmarkFixture struct {
	worker        *Worker
	task          *claimedTask
	report        Result
	canonical     *mailboxFixture
	broker        *FirstNodeBroker
	binding       ChoreographyRunBinding
	bootstrapOID  string
	authorityRoot string
	pending       workerPickupState
	outcomeBranch string
	proposal      ChoreographyResultProposal
	receipt       ChoreographyReceipt
	submissions   int
}

func newPackBenchmarkFixture(t testing.TB, inputs []packBenchmarkInput) *packBenchmarkFixture {
	t.Helper()
	authorityRoot := t.TempDir()
	require.NoError(t, os.Chmod(authorityRoot, packBenchmarkDirectoryMode))
	authority, err := OpenNodeSlotAuthority(authorityRoot, packBenchmarkNode, packBenchmarkCapacity)
	require.NoError(t, err)
	limits := TransferLimits{MaxFiles: models.DefaultExecutionMaxFiles, MaxBytes: models.DefaultExecutionMaxBytes,
		MaxManifestBytes: models.DefaultExecutionMaxManifestBytes, MaxPackBytes: models.DefaultExecutionMaxPackBytes}
	canonical, persist, binding, spec := newChoreographyLedgerFixtureWithSettings(t, choreographyLedgerFixtureSettings{
		AuthorityID: authority.AuthorityID(), AdmissionSeconds: choreographyTestAdmissionSeconds,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Mode:     bootstrapCompilationModeName, Tasks: []ChoreographyTaskSpec{{Task: packBenchmarkTask, Kind: KindRun}},
		OutputRoots: []string{packBenchmarkRoot}, Limits: limits})
	coordinator := &Coordinator{Run: binding.Run, Generation: binding.Generation, PlanDigest: binding.PlanDigest, Limits: limits}
	require.NoError(t, coordinator.RegisterCompiledChoreographyResults(persist.opts.Templates))
	persist.opts.VerifyResult = coordinator.VerifyChoreographyResult
	options := choreographyFixtureBrokerOptions(persist)
	options.Members[0].AuthorityID = authority.AuthorityID()
	options.Members[0].Platform = runtime.GOOS + "/" + runtime.GOARCH
	broker, err := RecoverFirstNodeBroker(t.Context(), options)
	require.NoError(t, err)
	claim := ChoreographyClaimProposal{ChoreographyRunBinding: binding, Task: spec.Task, Attempt: packBenchmarkAttempt,
		Node: packBenchmarkNode, Branch: spec.Branch, ExpectedOID: spec.ReservationOID, InputsDigest: spec.InputsDigest,
		PickupID: NewChoreographyPickupID(), ProposalID: "benchmark-result-claim"}
	completeChoreographyFixtureClaim(t, persist, &claim)
	peer := canonical.second(t)
	worker := &Worker{Node: packBenchmarkNode, Endpoint: peer.endpoint, Mailbox: peer.mailbox, Cache: peer.git,
		NodeSlots: authority, ReturnSigner: peer.signer, StateDir: t.TempDir(), Log: zerolog.Nop(),
		Report: NodeReport{AuthorityID: authority.AuthorityID(), Limits: limits, OS: runtime.GOOS, Arch: runtime.GOARCH, Dispat: "benchmark"}}
	channel := &workerReturnChannel{binding: binding, bootstrapOID: persist.opts.BootstrapOID,
		bootstrap: &RunBootstrap{Protocol: ChoreographyProtocolVersion, Mode: bootstrapCompilationModeName,
			ChoreographyRunBinding: binding, CoordinationEndpoint: peer.endpoint}}
	worker.returnChannels = map[string]*workerReturnChannel{binding.Run: channel}
	proposed := Assignment{Header: Header{Protocol: ChoreographyProtocolVersion, Run: binding.Run, Generation: binding.Generation,
		PlanDigest: binding.PlanDigest, Task: spec.Task, Attempt: claim.Attempt, Node: claim.Node, Branch: spec.Branch, Kind: spec.Kind},
		GraphDigest: binding.GraphDigest, GraphRootOID: persist.opts.GraphRootOID, BootstrapOID: persist.opts.BootstrapOID, Weight: spec.Weight}
	lease, available, err := worker.reserveNodeSlot(t.Context(), proposed, channel, claim)
	require.NoError(t, err)
	require.True(t, available)
	accepted, err := broker.AcceptClaim(t.Context(), claim)
	require.NoError(t, err)
	require.True(t, accepted.Accepted)
	require.NoError(t, peer.git.FetchRefs(t.Context(), peer.endpoint, []string{spec.Branch, FormatChoreographyGraphRootBranch(binding.Run)}))
	view, err := peer.mailbox.OpenCarriedAssignment(t.Context(), accepted.AssignmentOID, limits.MaxManifestBytes)
	require.NoError(t, err)
	task := &claimedTask{assignment: view.Assignment, tip: ChainTip{Branch: spec.Branch, OID: accepted.AssignmentOID}, claimed: accepted.OID, slotLease: lease}
	pending, err := worker.ensurePickupPending(t.Context(), task)
	require.NoError(t, err)
	identity := ChoreographyPickupIdentity{Run: binding.Run, Task: spec.Task, Node: claim.Node, PickupID: claim.PickupID, Mode: bootstrapCompilationModeName}
	outcome, err := FormatChoreographyOutcomeBranch(identity, StatusSucceeded)
	require.NoError(t, err)
	remote, err := worker.pickupRemoteTip(t.Context(), outcome)
	require.NoError(t, err)
	require.Empty(t, remote)
	manifest := packBenchmarkManifest(t, worker, task, inputs)
	report := Result{Header: worker.formatReplyHeader(task.assignment.Header), Assignment: task.claimed, Status: StatusSucceeded,
		Platform: manifest.Platform, Outputs: manifest}
	return &packBenchmarkFixture{worker: worker, task: task, report: report, canonical: canonical, broker: broker, binding: binding,
		bootstrapOID: persist.opts.BootstrapOID, authorityRoot: authorityRoot, pending: pending, outcomeBranch: outcome}
}

func packBenchmarkManifest(t testing.TB, worker *Worker, task *claimedTask, inputs []packBenchmarkInput) *OutputManifest {
	t.Helper()
	manifest := &OutputManifest{Protocol: ProtocolVersion, Run: task.assignment.Run, Generation: task.assignment.Generation,
		PlanDigest: task.assignment.PlanDigest, Task: task.assignment.Task, Attempt: task.assignment.Attempt, Node: worker.Node,
		Package: "artifact", Platform: Platform{OS: worker.Report.OS, Arch: worker.Report.Arch, Dispat: worker.Report.Dispat}, Roots: []string{packBenchmarkRoot}}
	var entries []gitx.TreeEntry
	for _, input := range inputs {
		file, err := os.Open(input.Path)
		require.NoError(t, err)
		command := exec.CommandContext(t.Context(), "git", "-C", worker.Cache.Dir, "hash-object", "-w", "--no-filters", "--stdin")
		command.Stdin = file
		output, hashErr := command.Output()
		closeErr := file.Close()
		require.NoError(t, hashErr)
		require.NoError(t, closeErr)
		oid := strings.TrimSpace(string(output))
		require.Len(t, oid, 40)
		require.FileExists(t, filepath.Join(worker.Cache.Dir, "objects", oid[:2], oid[2:]), "fresh artifact representation is loose")
		entries = append(entries, gitx.TreeEntry{Mode: gitx.TreeModeFile, Type: "blob", OID: oid, Name: input.Name})
		manifest.Entries = append(manifest.Entries, ManifestEntry{Path: packBenchmarkRoot + "/" + input.Name, Type: EntryFile,
			Mode: EntryModeFile, Size: input.Bytes, SHA256: input.SHA256})
		manifest.Bytes += input.Bytes
	}
	plumbing := gitx.NewPlumbing(worker.Cache)
	tree := plumbing.MakeTree(t.Context(), entries)
	manifest.OutputTree = plumbing.MakeTree(t.Context(), []gitx.TreeEntry{{Mode: gitx.TreeModeDir, Type: "tree", OID: tree, Name: packBenchmarkRoot}})
	require.NoError(t, plumbing.Err())
	manifest.Files = len(inputs)
	manifest.Digest = CalculateManifestDigest(manifest)
	_, err := ValidateOutputs(t.Context(), worker.Cache, manifest, packBenchmarkExpectation(manifest, worker.Report.Limits))
	require.NoError(t, err)
	return manifest
}

func packBenchmarkExpectation(m *OutputManifest, limits TransferLimits) OutputExpectation {
	return OutputExpectation{Run: m.Run, Generation: m.Generation, PlanDigest: m.PlanDigest, Task: m.Task,
		Attempt: m.Attempt, Roots: []string{packBenchmarkRoot}, Digest: m.Digest, Limits: limits}
}

func (f *packBenchmarkFixture) attachTransport(t testing.TB, ctx context.Context) {
	t.Helper()
	f.worker.returnChannels[f.binding.Run].client = &fakeWorkerReturn{
		result: func(p ChoreographyResultProposal, payload io.ReadSeeker) (ChoreographyReceipt, error) {
			f.submissions++
			if f.submissions != 1 {
				return ChoreographyReceipt{}, fmt.Errorf("benchmark unexpectedly resubmitted a result")
			}
			f.proposal = p
			file, ok := payload.(*os.File)
			if !ok {
				return ChoreographyReceipt{}, fmt.Errorf("production result payload is not a bounded file")
			}
			receipt, err := f.broker.AcceptResult(ctx, p, file)
			if err != nil {
				return receipt, err
			}
			f.receipt = receipt
			if !receipt.Accepted {
				return receipt, fmt.Errorf("real broker refused benchmark result")
			}
			err = f.broker.DecideAttempt(ctx, ChoreographyDecision{ChoreographyRunBinding: f.binding, Task: p.Task, Branch: p.Branch,
				Attempt: p.Attempt, Node: p.Node, ResultOID: receipt.OID, Succeeded: true})
			return receipt, err
		},
		lookup: func(q ChoreographyReceiptQuery) (ChoreographyReceipt, bool, error) {
			return f.broker.LookupReceipt(ctx, q)
		},
		pollWithContext: func(ctx context.Context, r ChoreographyEventRequest) (ChoreographyEventPage, error) {
			if r.ChoreographyRunBinding != f.binding {
				return ChoreographyEventPage{}, fmt.Errorf("event request changed benchmark binding")
			}
			return f.broker.ReadEvents(ctx, r.Cursor, r.MaxEvents)
		},
	}
}

func (f *packBenchmarkFixture) verify(t testing.TB) {
	t.Helper()
	require.Equal(t, f.outcomeBranch, f.proposal.OutcomeBranch)
	require.Equal(t, f.pending.PendingOID, f.proposal.PendingOID)
	require.Equal(t, f.task.claimed, f.proposal.ClaimOID)
	tree, err := f.canonical.mailbox.readTree(t.Context(), f.proposal.ResultDigest)
	require.NoError(t, err)
	raw, err := f.canonical.mailbox.Read(t.Context(), ChainTip{Kind: tree.kind, document: tree.document, signature: tree.signature}, f.worker.Report.Limits.MaxManifestBytes)
	require.NoError(t, err)
	var result Result
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NoError(t, f.canonical.mailbox.VerifyChoreographyPickupResult(t.Context(), ChoreographyPickupResultProof{
		Proposal: f.proposal, Result: result, Mode: bootstrapCompilationModeName, MaxDocumentBytes: f.worker.Report.Limits.MaxManifestBytes}))
	require.Equal(t, f.report.Outputs, result.Outputs)
	validated, err := ValidateOutputs(t.Context(), f.canonical.git, result.Outputs, packBenchmarkExpectation(f.report.Outputs, f.worker.Report.Limits))
	require.NoError(t, err)
	reader, err := gitx.OpenObjectReader(t.Context(), f.canonical.git)
	require.NoError(t, err)
	for i, entry := range result.Outputs.Entries {
		hash := sha256.New()
		n, readErr := reader.ReadBlob(validated.Objects[i], hash, entry.Size)
		require.NoError(t, readErr)
		require.Equal(t, entry.Size, n)
		require.Equal(t, entry.SHA256, hex.EncodeToString(hash.Sum(nil)))
	}
	require.NoError(t, reader.Close())
	feed, err := OpenRetainedChoreographyFeed(t.Context(), RetainedChoreographyFeedOptions{Mailbox: f.canonical.mailbox, Binding: f.binding,
		BootstrapOID: f.bootstrapOID, MaxDocumentBytes: f.worker.Report.Limits.MaxManifestBytes, MaxTagBytes: f.worker.Report.Limits.MaxPackBytes})
	require.NoError(t, err)
	page, err := feed.ReadEvents(t.Context(), f.receipt.Cursor, packBenchmarkObjectPage)
	require.NoError(t, err)
	terminal := false
	for _, event := range page.Events {
		if event.Kind == ChoreographyTerminal && event.Task == f.task.assignment.Task {
			require.NoError(t, feed.VerifyAttemptFinality(t.Context(), ChoreographyAttemptFinalityProof{Event: event, ReceiptOID: f.receipt.OID,
				ClaimOID: f.task.claimed, AssignmentOID: f.task.tip.OID}))
			terminal = true
		}
	}
	require.True(t, terminal, "exact signed terminal must be independently recoverable")
	reopened, err := OpenNodeSlotAuthority(f.authorityRoot, packBenchmarkNode, packBenchmarkCapacity)
	require.NoError(t, err)
	held, err := reopened.Held(t.Context())
	require.NoError(t, err)
	require.Empty(t, held, "real finality released the durable lease")
}
