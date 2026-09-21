// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What travels through a mailbox, and what each message is called.
//
// A coordination branch is the whole transport (CCME §28.4). One branch
// carries one attempt, as a first-parent chain of commits whose trees hold
// nothing but `dispat/<message>.json` and the detached signature beside it.
// The branch name is a routing hint and never an authority: it lets a node
// list only what is addressed to it, and the signed message inside decides
// what the node may act on.
//
// Everything here is data and pure functions over it, so both parties read
// one description of the protocol rather than two agreeing ones.

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

// ProtocolVersion is the version every message states and every reader
// requires exactly. There is no negotiation: two nodes of one run execute one
// engine's plan, and a message a reader does not fully understand is a
// message it must not act on.
const ProtocolVersion = 1

// The kinds of work an assignment may carry. The kind is a diagnostic label
// in the branch name and a decision in the signed message: it is what a
// serving node reads to know whether the work is something it performs.
const (
	// KindProbe asks a node what it is. It runs no command, consumes no
	// capacity and is what preflight dispatches to every configured worker
	// before anything else is sent anywhere.
	KindProbe = "probe"
	// KindBuild is one package's build stage.
	KindBuild = "build"
	// KindPublish is one package's publish stage, which is the only kind that
	// waits for an explicit authorization before its command runs.
	KindPublish = "publish"
	// KindPrepare is a build of a provider that this run does not release,
	// performed so that its outputs can reach a consumer.
	KindPrepare = "prepare"
	// KindSnapshot carries a prepared input state.
	KindSnapshot = "snapshot"
	// KindRelay is one node's result copied onto another node's endpoint,
	// which is how outputs cross two mailboxes.
	KindRelay = "relay"
)

// PreflightTask is the task name a probe is issued under, and the one task
// name that is not a package's: a probe is not part of the plan. It is the
// same on every node, because the run and the node already tell two probes
// apart, and it is stated here rather than at the orchestrator because a
// serving node remembers the work it answered by that name.
const PreflightTask = "preflight"

// MessageKind is one message of the protocol and, at the same time, the name
// its blob takes in the commit tree and the state the branch is in once that
// commit is on it. The three are deliberately one word: a reader that has
// fetched a tip knows the state of the branch from the file it found, without
// a second field anybody could disagree with.
type MessageKind string

// The messages, in the order one attempt produces them.
const (
	// MessageAssignment is the orchestrator's create-only first commit: what
	// is to be done, for which run and plan, by which node.
	MessageAssignment MessageKind = "assignment"
	// MessageClaim is the worker taking the assignment, which it does only
	// when it has a free slot for it.
	MessageClaim MessageKind = "claim"
	// MessageReady is a publisher reporting that everything before the
	// irreversible command is done and it is waiting to be authorized.
	MessageReady MessageKind = "ready"
	// MessageGo is the orchestrator's single-use publication authorization,
	// written after the ownership and the inputs have been revalidated.
	MessageGo MessageKind = "go"
	// MessageResult is the terminal report of one attempt.
	MessageResult MessageKind = "result"
	// MessageCancel is the orchestrator withdrawing an attempt.
	MessageCancel MessageKind = "cancel"
	// MessageAck is the worker confirming that the cancelled attempt has
	// stopped, which is what lets its capacity be reused.
	MessageAck MessageKind = "ack"
)

// The three statuses a result reports. They are distinct from the run's own
// package outcomes: a task says what became of the work it was given, and
// what that means for a release is the orchestrator's to decide.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// messageDir is the one folder every transport tree puts its message in, so
// that build outputs travelling in the same tree can never collide with the
// protocol's own files.
const messageDir = "dispat"

// FormatMessagePath is where one message's document sits in a transport tree,
// and FormatSignaturePath is where the signature over its exact bytes sits
// beside it.
func FormatMessagePath(kind MessageKind) string {
	return messageDir + "/" + string(kind) + ".json"
}

// FormatSignaturePath is the detached signature of the document at
// FormatMessagePath.
func FormatSignaturePath(kind MessageKind) string {
	return messageDir + "/" + string(kind) + ".sig"
}

// branchPrefix is what every branch addressed to one node begins with. The
// node name is written as the file spelled it, because that is what the node
// itself lists on.
const branchPrefix = "dispat-worker-"

// FormatBranchPrefix is the beginning of every coordination branch addressed
// to one node. A worker named `build` also matches `build-a`'s branches, and
// that is harmless: the name routes, the signed message decides.
func FormatBranchPrefix(node string) string { return branchPrefix + node + "-" }

// FormatBranchPattern is the single ref pattern one node polls its mailbox
// with, which is what keeps one node's poll independent of how much work
// every other node has.
func FormatBranchPattern(node string) string { return "refs/heads/" + FormatBranchPrefix(node) + "*" }

// FormatBranch names one attempt's coordination branch:
// `dispat-worker-<id>-<YYYYMMDD>-<kind>-<32 hex>` (§28.4).
//
// The date and the kind are labels for a person reading `git branch` on a
// mailbox, and nothing ever parses them back: a node that trusted the date
// would be trusting the clock of whoever created the branch, and a node that
// trusted the kind would be executing work the signature never covered. The
// randomness is what makes the name unique, and it is cryptographic because
// guessing a name must not let anybody address a node.
func FormatBranch(node, kind string, at time.Time) string {
	return FormatBranchPrefix(node) + at.UTC().Format("20060102") + "-" + kind + "-" + formatRandomHex(16)
}

// FormatRunID names one run: 128 bits of randomness, which is what binds
// every assignment, claim and result of one release together without anything
// derived from a clock or a hostname.
func FormatRunID() string { return formatRandomHex(16) }

// formatRandomHex is n cryptographically random bytes as lowercase hex.
//
// crypto/rand.Read is documented never to fail and always to fill its buffer
// (it stops the program if the system source is broken), so the error it
// returns for compatibility is deliberately not turned into an arm nothing
// could ever take.
func formatRandomHex(n int) string {
	value := make([]byte, n)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

// IsAddressedTo reports whether a branch name routes to this node, which is
// the cheapest half of the acceptance rules and the only one that can be
// answered before anything is fetched.
func IsAddressedTo(branch, node string) bool {
	return strings.HasPrefix(branch, FormatBranchPrefix(node))
}

// Header is what every message of the protocol carries, whatever it is.
//
// It is one struct embedded in each message rather than repeated fields,
// because the acceptance rules are asked of the header alone: a node decides
// whether a message is authentically this run's, addressed to it, and on the
// branch it was found on, before it looks at anything the message is about.
type Header struct {
	// Protocol is ProtocolVersion, matched exactly by every reader.
	Protocol int `json:"protocol"`
	// Kind is the work the branch carries: one of the Kind constants above.
	Kind string `json:"kind"`
	// Run is the run this message belongs to (FormatRunID).
	Run string `json:"run"`
	// PlanDigest names the fixed plan the run executes (§28.3), so a node can
	// refuse work belonging to another planning of the same repository.
	PlanDigest string `json:"planDigest"`
	// Task is the unit of work inside the run, stable across its attempts.
	Task string `json:"task"`
	// Attempt counts from one and distinguishes two attempts of one task,
	// which is what makes (run, task, attempt) the triple a replay is
	// recognised by.
	Attempt int `json:"attempt"`
	// Generation names the ownership the run holds, derived from the release
	// lock objects (§28.3), so an authorization from an earlier acquisition
	// is recognisably not this one's.
	Generation string `json:"generation"`
	// Node is the name of the node the work is addressed to, compared byte
	// for byte with the serving node's own name.
	Node string `json:"node"`
	// Branch is the coordination branch this message may appear on, compared
	// with the ref it was actually found on so that a message cannot be
	// replayed onto a branch of another attempt.
	Branch string `json:"branch"`
	// IssuedAt is when the message was written, in RFC3339. It bounds replay
	// and nothing else: no scheduling decision is made from it, because it is
	// the writer's clock rather than the reader's.
	IssuedAt string `json:"issuedAt"`
}

// Assignment is one unit of work offered to one node.
//
// Everything a task needs travels here, because a worker never rediscovers
// policy or history from a transported checkout (§28.3): the configuration,
// the commands, the platforms and the permitted writes are the orchestrator's
// resolved input, and the fields below are that input rather than a summary
// of it. The fields the later gates fill are declared now and left empty, so
// that the document one gate writes is the document every gate reads.
type Assignment struct {
	Header
	// Repositories are the checkouts the task needs, each with the exact
	// object id of the prepared input state it is to be materialized at.
	Repositories []AssignmentRepository `json:"repositories,omitempty"`
	// Package names the package the task belongs to and the folder its
	// commands run in, relative to its repository.
	Package *AssignmentPackage `json:"package,omitempty"`
	// Frame is the stage's script frame: the login that precedes it, the
	// hooks before and after, and the stage commands themselves.
	Frame *AssignmentFrame `json:"frame,omitempty"`
	// Env are the computed DISPAT_* pairs of the stage, which are public
	// metadata of the run and carry no secret.
	Env []string `json:"env,omitempty"`
	// StaticEnv are the declared environment pairs exactly as the
	// configuration wrote them, unresolved. A pair naming a secret travels as
	// that reference and is expanded on the executing node from its own
	// environment, so a resolved secret never reaches a branch (§28.3).
	StaticEnv []string `json:"staticEnv,omitempty"`
	// Exports are the values the run's earlier stages of this package already
	// exported, so that the scripts of this frame read the same accumulated
	// state they would read at home. What this frame exports travels back in
	// the result rather than being echoed here.
	Exports []ExportedValue `json:"exports,omitempty"`
	// Shell is the interpreter the commands run through.
	Shell []string `json:"shell,omitempty"`
	// Platforms are the node platforms the task may run on, in GOOS/GOARCH
	// spelling. Empty means any node.
	Platforms []string `json:"platforms,omitempty"`
	// Inputs are the results of other tasks this one consumes, each named by
	// the exact object its outputs were captured into.
	Inputs []AssignmentInput `json:"inputs,omitempty"`
	// Outputs are the declared build output roots the task is expected to
	// produce, relative to the package folder.
	Outputs []string `json:"outputs,omitempty"`
	// Permits is what this assignment authorizes beyond running its commands.
	Permits AssignmentPermits `json:"permits"`
	// Limits are the transfer ceilings the orchestrator holds this task to,
	// which a node compares against its own before it accepts any work.
	Limits TransferLimits `json:"limits"`
}

// AssignmentRepository is one checkout a task needs: the repository's
// identity in the run, where it sits relative to the task folder, the exact
// commit the node materializes, and the immutable branch that commit is
// reachable from.
//
// The branch is named because a node fetches objects by ref and then reads the
// exact object: the name is how the state gets there, and the object id is
// what decides what is checked out, so a branch somebody moved cannot change
// what a task builds.
type AssignmentRepository struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Snapshot string `json:"snapshot"`
	Branch   string `json:"branch,omitempty"`
}

// AssignmentPackage names the package a task belongs to: the package's name
// in the plan, the repository it lives in and its folder inside it.
type AssignmentPackage struct {
	Name       string `json:"name"`
	Repository string `json:"repository,omitempty"`
	Dir        string `json:"dir,omitempty"`
}

// AssignmentFrame is the script frame of one stage, in execution order. It is
// the frame the orchestrator resolved, so that a node runs exactly the
// commands the run's configuration produced and resolves nothing of its own.
type AssignmentFrame struct {
	Login    []string `json:"login,omitempty"`
	Before   []string `json:"before,omitempty"`
	Commands []string `json:"commands,omitempty"`
	After    []string `json:"after,omitempty"`
}

// AssignmentInput is one provider result a task consumes: which task produced
// it, and the exact object its verified outputs are read from.
type AssignmentInput struct {
	Task    string `json:"task"`
	Package string `json:"package,omitempty"`
	Commit  string `json:"commit"`
}

// AssignmentPermits is what one assignment authorizes beyond running
// commands. Both default to false, so an assignment that says nothing
// authorizes nothing.
type AssignmentPermits struct {
	// Publish is the right to run the publish stage's own command, and is
	// never enough on its own: the single-use MessageGo is what authorizes
	// the irreversible effect at the moment it happens.
	Publish bool `json:"publish"`
	// NativeRefs is the right to write a release ref, and is never granted to
	// a worker: transport credentials do not authorize native records
	// (§28.4).
	NativeRefs bool `json:"nativeRefs"`
}

// TransferLimits are the ceilings one task's outputs are held to. They travel
// in both directions: down in the assignment, so the node knows what it is
// held to, and up in a probe report, so the orchestrator can refuse a node
// that would silently truncate what it is asked to move.
type TransferLimits struct {
	MaxFiles         int   `json:"maxFiles"`
	MaxBytes         int64 `json:"maxBytes"`
	MaxManifestBytes int64 `json:"maxManifestBytes"`
}

// Every message after the first names the exact object it answers, and that
// is a rule rather than a convenience. A signature says who wrote a document
// and what kind of message it is; the object id is what says where on the
// chain it belongs. Without it an authentic message of the right kind could
// be replayed at another position of the same branch, which is the same
// attack as relabelling one kind as another, one step further in.

// Claim is the worker taking one assignment. It names the exact assignment
// object it read, so that a claim of an assignment somebody rewrote is
// recognisable as one.
type Claim struct {
	Header
	Assignment string `json:"assignment"`
}

// Ready is a publisher reporting that everything before the irreversible
// command is done. It names both the assignment it belongs to and the claim
// commit it follows, so that the orchestrator authorizing it knows exactly
// which state of the branch it is authorizing.
type Ready struct {
	Header
	Assignment string `json:"assignment"`
	Claim      string `json:"claim"`
}

// Go is the orchestrator's single-use publication authorization. It names the
// exact ready commit it answers, which is what makes it single use: an
// authorization for one state of one branch is not an authorization for the
// next one, so it cannot be replayed onto a later attempt or a later ready.
type Go struct {
	Header
	Assignment string `json:"assignment"`
	Ready      string `json:"ready"`
}

// Withdrawal is the orchestrator's cancel message: it withdraws an attempt. It
// names the tip it withdraws, so that a cancellation cannot be replayed onto
// work that started after it was written.
type Withdrawal struct {
	Header
	Assignment string `json:"assignment"`
	Tip        string `json:"tip"`
}

// Ack is the worker confirming that a cancelled attempt has stopped. It names
// the cancel commit it answers, which is what lets the orchestrator tell an
// acknowledgement of this withdrawal from one of an earlier attempt's.
type Ack struct {
	Header
	Assignment string `json:"assignment"`
	Cancel     string `json:"cancel"`
}

// Result is the terminal report of one attempt: what the node did, what came
// of it, and what it produced.
type Result struct {
	Header
	// Assignment is the object id of the assignment this result answers.
	Assignment string `json:"assignment"`
	// Status is one of StatusSucceeded, StatusFailed or StatusCancelled.
	Status string `json:"status"`
	// FailedPart names the part of the frame that failed, empty for a
	// successful attempt.
	FailedPart string `json:"failedPart,omitempty"`
	// Exit is the exit status of the failed command.
	Exit int `json:"exit,omitempty"`
	// Platform is what the work actually ran on, which is how a run's records
	// can say where an artefact was built rather than where it was planned.
	Platform Platform `json:"platform"`
	// Exports are what the stage's scripts wrote to their DISPAT_OUTPUT files.
	Exports []ExportedValue `json:"exports,omitempty"`
	// Manifest is the verified description of the outputs this attempt
	// captured, empty when it produced none.
	Manifest []ManifestEntry `json:"manifest,omitempty"`
	// OutputTree is the object the captured outputs are read from.
	OutputTree string `json:"outputTree,omitempty"`
	// StrayWrites counts the tracked files the task wrote outside what it
	// declared. They are never admitted; the count is what makes a build that
	// writes where nobody expected it visible.
	StrayWrites int `json:"strayWrites,omitempty"`
	// Report is the node's description of itself, carried by the result of a
	// probe and by nothing else.
	Report *NodeReport `json:"report,omitempty"`
}

// ExportedValue is one value a task's scripts exported through their
// DISPAT_OUTPUT file, on its way back to the run that dispatched the task.
//
// The exporting sequence travels with the value rather than being guessed at
// the other end: an export written by a stage's own script and one written by
// the hook that preceded it reach every later script as the same variable, and
// the provenance variable beside it is the only thing that tells them apart.
type ExportedValue struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"`
}

// Platform is where work ran: the operating system and architecture in Go's
// own spelling, and the dispat build that ran it.
type Platform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Dispat string `json:"dispat"`
}

// ManifestEntry is one file of a task's captured outputs, bound to the digest
// that makes it verifiable on the consuming node.
type ManifestEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

// NodeReport is what a node answers a probe with: everything preflight has to
// know before any work is placed anywhere (§28.2).
type NodeReport struct {
	// Protocol is the node's own ProtocolVersion, compared with the
	// orchestrator's rather than assumed from the message it answered.
	Protocol int `json:"protocol"`
	// Dispat is the node's build, which is what a report of "the versions
	// differ" names.
	Dispat string `json:"dispat"`
	// OS and Arch are the platform the node can run work on.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Capacity is how many assigned command tasks the node runs at once.
	Capacity int `json:"capacity"`
	// GitVersion is the git the node would move objects with.
	GitVersion string `json:"gitVersion,omitempty"`
	// Limits are the node's own transfer ceilings, which must not be below
	// the ones the run holds its tasks to.
	Limits TransferLimits `json:"limits"`
}

// IsPlatformSatisfied reports whether this report's platform is one of the
// platforms a package's build may run on. An empty list is every platform, so
// a workspace that never says otherwise places its work anywhere.
func (r *NodeReport) IsPlatformSatisfied(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	own := r.OS + "/" + r.Arch
	for _, platform := range platforms {
		if platform == own {
			return true
		}
	}
	return false
}
