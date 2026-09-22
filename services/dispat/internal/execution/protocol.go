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
// protocol's own files. One message's document is `<kind>.json` inside it and
// the signature over its exact bytes is `<kind>.sig` beside it.
const messageDir = "dispat"

// outputsDir is the folder a result's captured build outputs travel in, beside
// the message folder rather than inside it. A reader never looks anything up
// by that name: the signed result names the tree by object id, and the folder
// exists so that a push of the commit sends the blobs.
const outputsDir = "outputs"

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

// ResolveBranchKindHint is the kind segment of a coordination branch name, and
// the empty string for a name that is not shaped like one.
//
// It is a hint and says so: the name is written by whoever created the branch,
// so nothing a node acts on may be decided by it. What it is good for is the
// opposite decision, refusing to act: a branch whose name says it carries a
// prepared input state or a relayed result is not an assignment addressed to
// anybody, so a node skips it instead of reading a repository tree and
// reporting that it holds no message. The signed message is still the whole
// authority for every branch that is not skipped.
//
// The segments are read from the right because a node name may itself hold
// hyphens: the last three fields of `dispat-worker-<id>-<date>-<kind>-<hex>`
// are the date, the kind and the randomness, whatever the id spelled.
func ResolveBranchKindHint(branch string) string {
	fields := strings.Split(branch, "-")
	if len(fields) < 5 || !strings.HasPrefix(branch, branchPrefix) {
		return ""
	}
	return fields[len(fields)-2]
}

// IsBranchCarryingWork reports whether a branch name could name work a node is
// asked to perform, which every name that is not a prepared input state or a
// relayed result could.
//
// A name this function rejects is skipped without being read, and a name it
// accepts is read and then believed only as far as its signature goes. It is
// stated as a positive question about work rather than as a list of the two
// transport kinds so that a kind added later is read rather than silently
// skipped: the safe default for an unknown name is to authenticate it.
func IsBranchCarryingWork(branch string) bool {
	switch ResolveBranchKindHint(branch) {
	case KindSnapshot, KindRelay:
		return false
	default:
		return true
	}
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
	// DeadlineSeconds is how long the run will wait for this attempt, so that
	// the node bounds the work itself rather than being abandoned while it is
	// still running: the orchestrator's wait and the node's own are then the
	// same number instead of two machines disagreeing about when an attempt is
	// over. Zero is no bound of the node's own.
	DeadlineSeconds int `json:"deadlineSeconds,omitempty"`
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
// in the plan, the version this run is releasing it as, the repository it
// lives in and its folder inside it.
//
// The version is carried so that the manifest of what the task produces can
// name it. A node could read the same number out of the computed environment,
// but reading policy out of an environment variable is what §28.3 forbids: a
// task executes the input it was given.
type AssignmentPackage struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Repository string `json:"repository,omitempty"`
	Dir        string `json:"dir,omitempty"`
}

// AssignmentFrame is the script frame of one stage, in execution order. It is
// the frame the orchestrator resolved, so that a node runs exactly the
// commands the run's configuration produced and resolves nothing of its own.
//
// There is no login here and there never will be. A space that authenticates
// publishes on the orchestrator, in the checkout its own credentials are on,
// so no login command, no login export and no login state ever enters a
// mailbox: giving a space a `flow.login` is what pins its publishes to the
// machine the release was started on.
type AssignmentFrame struct {
	Before   []string `json:"before,omitempty"`
	Commands []string `json:"commands,omitempty"`
	After    []string `json:"after,omitempty"`
}

// AssignmentInput is one provider's admitted output set as the consuming node
// has to reach it: which task produced it, the branch of this node's own
// endpoint the bytes are fetchable from, the exact object they are read at,
// the manifest the orchestrator admitted, and where the provider's folder sits
// in the checkout the consumer materializes.
//
// The branch is endpoint-local because the consumer talks to one mailbox and
// to nothing else: when the producer answered somewhere else, the orchestrator
// relays the object onto this endpoint and names the relay branch here, so a
// node never has to be told about a machine it cannot reach (§28.5).
//
// The digest is what makes the reference a transfer rather than a promise. A
// node verifies the manifest it finds at Commit against this value before a
// single file is installed, so an output set somebody swapped on the mailbox
// is refused by the consumer as well as by the orchestrator that admitted it.
type AssignmentInput struct {
	Task    string `json:"task"`
	Package string `json:"package,omitempty"`
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	Digest  string `json:"manifestDigest"`
	Path    string `json:"path,omitempty"`
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
//
// The hook's exports travel with it rather than waiting for the result,
// because they are what a beforePublish hook is for: the orchestrator merges
// them onto the release at the moment the local path would have, so the values
// a hook exported are on the release whether or not the publication that
// follows succeeds.
type Ready struct {
	Header
	Assignment string          `json:"assignment"`
	Claim      string          `json:"claim"`
	Exports    []ExportedValue `json:"hookExports,omitempty"`
}

// Go is the orchestrator's single-use publication authorization. It names the
// exact ready commit it answers, which is what makes it single use: an
// authorization for one state of one branch is not an authorization for the
// next one, so it cannot be replayed onto a later attempt or a later ready.
//
// NotAfter is the instant the authorization stops meaning anything. It is
// stated rather than implied because the publisher is the party that acts on
// it: an authorization that reached a node minutes late is an authorization
// taken under checks that are no longer current, and a node that started
// publishing on it would be publishing under ownership nobody re-verified.
type Go struct {
	Header
	Assignment string `json:"assignment"`
	Ready      string `json:"ready"`
	NotAfter   string `json:"notAfter,omitempty"`
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
	// Reason is the stable word for a failure that was not a command exiting
	// non-zero: the rule an input or output set broke. It is an enum so that
	// the orchestrator's log says which rule without echoing anything the
	// task produced.
	Reason string `json:"reason,omitempty"`
	// Exit is the exit status of the failed command.
	Exit int `json:"exit,omitempty"`
	// Platform is what the work actually ran on, which is how a run's records
	// can say where an artefact was built rather than where it was planned.
	Platform Platform `json:"platform"`
	// Exports are what the stage's scripts wrote to their DISPAT_OUTPUT files.
	Exports []ExportedValue `json:"exports,omitempty"`
	// Outputs is the verified description of what this attempt produced, nil
	// when it declared none. It travels inside the result rather than beside
	// it so that the signature over the result is the signature over the
	// manifest: a blob added to the same tree under a name of its own would be
	// trusted for being called what it is called.
	Outputs *OutputManifest `json:"outputs,omitempty"`
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

// The two kinds of thing a build output may be. A declared root travels as
// files and links and as nothing else: a device node, a socket or a submodule
// is not a build product, and a node that installed one would be reproducing
// something about the machine that built it rather than something it made.
const (
	EntryFile    = "file"
	EntrySymlink = "symlink"
)

// The two file modes an output entry may carry, in git's own spelling minus
// the object-type digits. Everything a build writes is either readable or
// runnable; anything else is a permission bit that does not survive the
// journey between two operating systems and must therefore not be promised.
const (
	EntryModeFile       = "0644"
	EntryModeExecutable = "0755"
)

// ManifestEntry is one file of a task's captured outputs, bound to the digest
// that makes it verifiable on the consuming node.
//
// The path is slash-separated and relative to the package folder, because that
// is the one spelling two machines can agree on; the digest is over the
// content for a file and over the link target for a symlink, so that every
// entry of a manifest is something the consumer can check rather than
// something it has to believe.
type ManifestEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

// OutputManifest is what one successful build says it produced, bound to
// everything that decides whether a consumer may use it (§28.5).
//
// Two halves travel together. The header binds the bytes to the run, the plan,
// the task attempt and the ownership that authorized it, to the exact source
// states the build consumed, to the package and version it was for, to the
// platform it ran on and to the output sets that went into it; the entries
// describe the bytes themselves. Neither half is useful alone: a description
// of files nobody can place in a run is a description of files nobody may
// install, which is exactly the substitution §28.5 forbids.
//
// The whole document travels inside the signed result, so nothing here is
// trusted for being found somewhere. Digest is the manifest's own name, taken
// over the document with that field empty, so that an assignment can name one
// admitted manifest and a consumer can prove the one it fetched is that one.
type OutputManifest struct {
	// Protocol is ProtocolVersion, matched exactly, as on every message.
	Protocol int `json:"protocol"`
	// The work this output set belongs to, in the same vocabulary the
	// protocol's headers use.
	Run        string `json:"run"`
	PlanDigest string `json:"planDigest"`
	Task       string `json:"task"`
	Attempt    int    `json:"attempt"`
	Generation string `json:"generation"`
	Node       string `json:"node"`
	// Package and Version are what was being built, so that an output set can
	// be reported and refused by the name a person reads in a plan.
	Package string `json:"package"`
	Version string `json:"version,omitempty"`
	// Repositories are the prepared input states the build consumed, one per
	// repository of its input closure, repository-qualified so that a
	// composed workspace's states are told apart.
	Repositories []ManifestRepository `json:"repositories,omitempty"`
	// Platform is the machine the build ran on, which is what a consumer
	// compares against the platforms its own package may build on.
	Platform Platform `json:"platform"`
	// Roots are the declared build output roots the entries lie under,
	// exactly as the configuration ladder resolved them.
	Roots []string `json:"roots,omitempty"`
	// Inputs are the output sets installed for this build, so that what a set
	// was made from is as inspectable as what it is.
	Inputs []ManifestInput `json:"inputs,omitempty"`
	// Entries are the files themselves, sorted by path.
	Entries []ManifestEntry `json:"entries,omitempty"`
	// Files and Bytes are the totals the transfer ceilings are applied to.
	// They are stated rather than recomputed so that a reader can refuse a
	// set before it walks it, and they are checked against the entries so
	// that stating them is not a way of lying about them.
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// OutputTree is the git tree the entries are read from.
	OutputTree string `json:"outputTree"`
	// Digest names this manifest: the SHA-256 of the document with this field
	// empty.
	Digest string `json:"manifestDigest"`
}

// ManifestRepository is one prepared input state a build consumed: the
// repository's identity in the run and the exact commit that was materialized.
type ManifestRepository struct {
	Name     string `json:"name,omitempty"`
	Snapshot string `json:"snapshot"`
}

// ManifestInput is one provider output set that was installed before a build
// ran: whose it was, the tree it came from and the manifest that described it.
type ManifestInput struct {
	Package    string `json:"package"`
	OutputTree string `json:"outputTree"`
	Digest     string `json:"manifestDigest"`
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
