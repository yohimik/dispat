package models

// This file is the `execution` key: which role a node plays when a release is
// executed across several machines, how much work the node accepts at once,
// and where the worker nodes an orchestrator may delegate to are reached.
//
// These are node-startup settings and they are read from the entry
// configuration alone. A space, a package, a package folder's own file, an
// imported configuration and a linked peer never state them, because a
// checkout that travels to another machine would otherwise carry that
// machine's role, capacity and worker list with it.
//
// The transport is Git alone: there is no listener and no second protocol.
// The mailbox is the repository being released, which a worker link with no
// `endpoint` reaches at the push URL of the orchestrator's release remote; an
// `endpoint` is a credential-free Git URL naming another mailbox repository.
// The secret that signs what travels through a mailbox is named by
// `secretEnv` and read from the environment at run time, so the secret itself
// never goes in the file, exactly as a webhook's signing secret does not.

// The two roles a node may play. Orchestrator is the default, so a
// configuration that says nothing about execution is the node a release is
// started on: it owns the locks, the plan and the finalization. A worker
// executes the tasks an orchestrator authorized and initiates nothing of its
// own.
const (
	ExecutionRoleOrchestrator = "orchestrator"
	ExecutionRoleWorker       = "worker"
)

// What an unstated execution setting means. They are written down rather than
// left to the zero value because a node that says nothing must still have one
// documented capacity and one documented bound on every wait: the defaults
// are part of the configuration contract, not an implementation detail of
// whichever gate first reads them.
const (
	// DefaultExecutionConcurrency is the capacity of a node that states none:
	// one task at a time, which is the only bound that is safe without
	// knowing what the node runs.
	DefaultExecutionConcurrency = 1
	// DefaultExecutionPreflightTimeout bounds the probe that asks a worker
	// what it is, in seconds. A node that cannot answer a question this small
	// in a minute is not one to dispatch work to.
	DefaultExecutionPreflightTimeout = 60
	// DefaultExecutionTaskTimeout bounds one assigned task, in seconds. An
	// hour is long enough for a real build and short enough that a lost node
	// does not hold a release open for a shift.
	DefaultExecutionTaskTimeout = 3600
	// DefaultExecutionCancelTimeout bounds the wait for a cancelled task to
	// acknowledge, in seconds. Capacity is held until it does, so the wait is
	// bounded rather than optimistic.
	DefaultExecutionCancelTimeout = 60
	// DefaultExecutionTransferTimeout bounds one output transfer, in seconds.
	DefaultExecutionTransferTimeout = 1800
)

// The default transfer ceilings: what one task's declared build outputs may
// weigh before the transfer is refused rather than attempted. They are
// generous enough for an ordinary `dist` folder and small enough that a
// runaway build is refused before it fills a mailbox.
const (
	// DefaultExecutionMaxFiles is the largest number of files one task's
	// declared outputs may hold.
	DefaultExecutionMaxFiles = 20000
	// DefaultExecutionMaxBytes is the largest total size of one task's
	// declared outputs, in bytes (2 GiB).
	DefaultExecutionMaxBytes int64 = 2147483648
	// DefaultExecutionMaxManifestBytes is the largest output manifest one
	// task may produce, in bytes (8 MiB). It bounds what a consumer reads
	// into memory before it has verified anything.
	DefaultExecutionMaxManifestBytes int64 = 8388608
)

// ExecutionConfig is the top-level `execution` object. It is a pointer on
// File so that an absent key stays absent: with no execution object at all a
// release is planned and executed exactly as it always was, and that is what
// makes distributed execution an addition rather than a new code path every
// existing repository travels.
type ExecutionConfig struct {
	// Role governs release initiation on this node: "orchestrator" (the
	// default) or "worker". The value is matched exactly, as logLevel and
	// commitErrors are, so a misspelling is refused rather than guessed at.
	Role string `json:"role,omitempty"`
	// Concurrency bounds the assigned command tasks this node runs at once,
	// across runs. It is a pointer because a stated 0 is a mistake worth
	// refusing while an absent key means DefaultExecutionConcurrency, and a
	// plain int could not tell the two apart. It bounds this node only: the
	// run's own stage budgets still apply and are never multiplied by the
	// number of workers.
	Concurrency *int `json:"concurrency,omitempty"`
	// Name is this node's identity when it serves tasks, written as
	// [A-Za-z0-9._-]+ so it can be read out of a coordination branch name. It
	// names an execution endpoint and never a repository peer.
	Name string `json:"name,omitempty"`
	// Endpoint is this node's own mailbox when it serves tasks: the
	// credential-free Git URL an orchestrator pushes assignments to, which is
	// the repository being released unless the orchestrator's link names
	// another. https, ssh and file URLs, an absolute path and the scp-like
	// host:path form are accepted; http and git are refused because they
	// carry no authentication.
	Endpoint string `json:"endpoint,omitempty"`
	// SecretEnv names the environment variable holding the shared secret
	// every mailbox message is signed with. Required of an orchestrator that
	// states workers, and of a node serving tasks. The variable's name goes
	// in the file, never the secret itself.
	SecretEnv string `json:"secretEnv,omitempty"`
	// Workers are this orchestrator's execution links, each a node name and,
	// optionally, the mailbox it is reached at; a link that states none
	// reaches the repository being released. A list of objects rather than a
	// map so that node names keep the case the file wrote them in: dispat
	// folds every map key it decodes. An empty or absent list preserves local
	// execution. A worker states none, because a worker delegates nothing.
	//
	// A present but empty list is the one state this field cannot round-trip:
	// omitempty drops it on the way out, and it means what an absent list
	// means anyway.
	Workers []ExecutionWorkerConfig `json:"workers,omitempty"`
	// Timeouts bounds the waits a distributed run makes. nil means every
	// default; see ExecutionTimeoutsConfig.
	Timeouts *ExecutionTimeoutsConfig `json:"timeouts,omitempty"`
	// Transfer bounds what one task's build outputs may weigh. nil means
	// every default; see ExecutionTransferConfig.
	Transfer *ExecutionTransferConfig `json:"transfer,omitempty"`
}

// ExecutionWorkerConfig is one entry of `execution.workers`: a worker node an
// orchestrator may delegate tasks to, and the mailbox repository it reads
// them from. Name is required; an empty Endpoint reaches the orchestrator's
// release remote (the push URL its lock is taken on; the entry repository's
// in a composed workspace).
type ExecutionWorkerConfig struct {
	// Name is the node's identity, unique across the list after case folding
	// and written as [A-Za-z0-9._-]+. It is a routing hint rather than an
	// authority: what a node may act on is decided by the signature on the
	// message, not by the name on the branch.
	Name string `json:"name,omitempty"`
	// Endpoint is the node's mailbox when it is not the repository being
	// released, under the same rules as ExecutionConfig.Endpoint. Empty
	// reaches the release remote's push URL, which is held to those rules
	// when a run that dispatches starts.
	Endpoint string `json:"endpoint,omitempty"`
}

// ExecutionTimeoutsConfig is the `execution.timeouts` object: how long a
// distributed run waits at each of the three points where a remote node can
// simply stop answering. Every value is in seconds, and 0 keeps the default,
// so a file may state one bound without restating the others.
type ExecutionTimeoutsConfig struct {
	// Preflight bounds the probe of one worker before dispatch. Default
	// DefaultExecutionPreflightTimeout.
	Preflight int `json:"preflight,omitempty"`
	// Task bounds one assigned task. Default DefaultExecutionTaskTimeout.
	Task int `json:"task,omitempty"`
	// Cancel bounds the wait for a cancelled task to acknowledge, after which
	// its capacity is reported leaked rather than quietly reused. Default
	// DefaultExecutionCancelTimeout.
	Cancel int `json:"cancel,omitempty"`
}

// ExecutionTransferConfig is the `execution.transfer` object: the ceilings a
// task's declared build outputs are checked against before they are captured
// and before they are installed. They exist so that an oversized or runaway
// output set is refused at a stated boundary instead of being discovered as a
// full disk on the node that consumes it. 0 keeps the default.
type ExecutionTransferConfig struct {
	// MaxFiles is the largest number of files one task's outputs may hold.
	// Default DefaultExecutionMaxFiles.
	MaxFiles int `json:"maxFiles,omitempty"`
	// MaxBytes is the largest total size of one task's outputs, in bytes.
	// Default DefaultExecutionMaxBytes.
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// MaxManifestBytes is the largest output manifest one task may produce,
	// in bytes. Default DefaultExecutionMaxManifestBytes.
	MaxManifestBytes int64 `json:"maxManifestBytes,omitempty"`
	// Timeout bounds one transfer, in seconds. Default
	// DefaultExecutionTransferTimeout.
	Timeout int `json:"timeout,omitempty"`
}

// ResolveRole returns the role this configuration puts the node in. It is
// nil-safe and treats an empty role as the default, so every caller asks one
// question and no caller has to know that an absent key means orchestrator.
func (c *ExecutionConfig) ResolveRole() string {
	if c == nil || c.Role == "" {
		return ExecutionRoleOrchestrator
	}
	return c.Role
}

// IsWorker reports whether this node is configured as a worker, which is the
// state that refuses release initiation. Nil-safe.
func (c *ExecutionConfig) IsWorker() bool { return c.ResolveRole() == ExecutionRoleWorker }

// IsDistributed reports whether this configuration delegates work at all: an
// orchestrator with at least one worker link. It is the one question the
// release path asks before anything about execution changes, so that a
// configuration with no workers keeps the local behaviour byte for byte.
// Nil-safe.
func (c *ExecutionConfig) IsDistributed() bool { return c != nil && len(c.Workers) > 0 }

// ResolveConcurrency returns the number of assigned tasks this node runs at
// once. Nil-safe, and an unstated value is DefaultExecutionConcurrency. A
// stated value is returned as written, including a nonpositive one, because
// the loader is what refuses it and a resolver that quietly repaired the
// value would hide the mistake from the error message.
func (c *ExecutionConfig) ResolveConcurrency() int {
	if c == nil || c.Concurrency == nil {
		return DefaultExecutionConcurrency
	}
	return *c.Concurrency
}

// ResolveTimeouts returns the effective waits, every unstated one filled with
// its default. Nil-safe.
func (c *ExecutionConfig) ResolveTimeouts() ExecutionTimeoutsConfig {
	var stated ExecutionTimeoutsConfig
	if c != nil && c.Timeouts != nil {
		stated = *c.Timeouts
	}
	return ExecutionTimeoutsConfig{
		Preflight: resolveStatedLimit(stated.Preflight, DefaultExecutionPreflightTimeout),
		Task:      resolveStatedLimit(stated.Task, DefaultExecutionTaskTimeout),
		Cancel:    resolveStatedLimit(stated.Cancel, DefaultExecutionCancelTimeout),
	}
}

// ResolveTransfer returns the effective output ceilings, every unstated one
// filled with its default. Nil-safe.
func (c *ExecutionConfig) ResolveTransfer() ExecutionTransferConfig {
	var stated ExecutionTransferConfig
	if c != nil && c.Transfer != nil {
		stated = *c.Transfer
	}
	return ExecutionTransferConfig{
		MaxFiles:         resolveStatedLimit(stated.MaxFiles, DefaultExecutionMaxFiles),
		MaxBytes:         resolveStatedLimit(stated.MaxBytes, DefaultExecutionMaxBytes),
		MaxManifestBytes: resolveStatedLimit(stated.MaxManifestBytes, DefaultExecutionMaxManifestBytes),
		Timeout:          resolveStatedLimit(stated.Timeout, DefaultExecutionTransferTimeout),
	}
}

// resolveStatedLimit is the one rule the timeout and transfer numbers share:
// 0 is the key not being stated and takes the default, and anything else is
// returned as written so the loader can refuse a negative value by name.
func resolveStatedLimit[T int | int64](stated, fallback T) T {
	if stated == 0 {
		return fallback
	}
	return stated
}
