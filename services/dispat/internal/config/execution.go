package config

// The `execution` key: this node's role, its capacity, and the worker nodes
// it may delegate to.
//
// Two things make the key unlike every other object in the language, and both
// are enforced here. It is a node-startup setting, so its line lives in
// fileFields alone: writing it on a space, a package or in a folder file is
// an unknown key, because a checkout that travels to another machine must not
// be able to tell that machine what role it plays. And it is read from the
// entry configuration alone, so an imported or linked peer's own `execution`
// object is legal to state and is simply never consulted; it is still
// validated when that file is loaded, because a file that cannot say what it
// means is a file nobody should be running from either.
//
// Every refusal below carries DiagnosticExecution, so a configuration a
// distributed run could not be started under is one machine-readable code in
// the log rather than a sentence a CI job has to match on.

import (
	"fmt"
	"regexp"
	"strings"

	lib "github.com/yohimik/dispat/pkg/config"
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// The execution models under this package's own names, beside the tables that
// decode them, for the reason the other aliases sit beside theirs: the tables
// read as the config language rather than as a tour of another package.
type (
	ExecutionConfig         = public.ExecutionConfig
	ExecutionWorkerConfig   = public.ExecutionWorkerConfig
	ExecutionTimeoutsConfig = public.ExecutionTimeoutsConfig
	ExecutionTransferConfig = public.ExecutionTransferConfig
)

// DiagnosticExecution reports an `execution` object no distributed run could
// be started under: an unknown role, a capacity that is not a capacity, a
// worker link that names no node or no reachable mailbox, or a missing
// signing secret. It is a load-time refusal, so it fires before any lock,
// plan or command.
const DiagnosticExecution = "E225"

// executionNodeName is the whole vocabulary of a node name. It is the fleet
// identity's alphabet and deliberately a separate rule: a node names an
// authenticated execution endpoint, a repository identity names a history,
// and the two are free to diverge.
var executionNodeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// isExecutionNodeName reports whether name can identify a node. The alphabet
// is not the whole rule: a node's name is written into the coordination
// branches addressed to it, and git refuses a ref name holding two dots in a
// row, so such a name would pass the load and then fail the first push a run
// makes to that node.
func isExecutionNodeName(name string) bool {
	return executionNodeName.MatchString(name) && !strings.Contains(name, "..")
}

// executionFields is the top-level `execution` object.
func executionFields(dst *ExecutionConfig) fields {
	return fields{
		"role":        str(&dst.Role),
		"concurrency": numPtr(&dst.Concurrency),
		"name":        str(&dst.Name),
		"endpoint":    str(&dst.Endpoint),
		"secretenv":   str(&dst.SecretEnv),
		"workers":     objList(&dst.Workers, executionWorkerFields),
		"timeouts":    obj(&dst.Timeouts, executionTimeoutsFields),
		"transfer":    obj(&dst.Transfer, executionTransferFields),
	}
}

// executionWorkerFields is one entry of `execution.workers`. It is a list of
// objects rather than a map for the reason a webhook's headers are: node
// names keep the case the file wrote them in, which a folded map key could
// not.
func executionWorkerFields(dst *ExecutionWorkerConfig) fields {
	return fields{
		"name":     str(&dst.Name),
		"endpoint": str(&dst.Endpoint),
	}
}

// executionTimeoutsFields is `execution.timeouts`: the bounded waits.
func executionTimeoutsFields(dst *ExecutionTimeoutsConfig) fields {
	return fields{
		"preflight": num(&dst.Preflight),
		"task":      num(&dst.Task),
		"cancel":    num(&dst.Cancel),
	}
}

// executionTransferFields is `execution.transfer`: the output ceilings. The
// two byte counts are 64-bit because a build output set is measured in
// gigabytes and a 32-bit ceiling would be a bound nobody chose.
func executionTransferFields(dst *ExecutionTransferConfig) fields {
	return fields{
		"maxfiles":         num(&dst.MaxFiles),
		"maxbytes":         num64(&dst.MaxBytes),
		"maxmanifestbytes": num64(&dst.MaxManifestBytes),
		"timeout":          num(&dst.Timeout),
	}
}

// num64 fills a 64-bit whole number, through the same weak reader every other
// number goes through so that a file may write the value as a number or as a
// string exactly as it may everywhere else.
func num64(dst *int64) setter {
	return func(val any, at string) error {
		n, err := weakInt(val, at)
		if err != nil {
			return err
		}
		*dst = int64(n)
		return nil
	}
}

// validateExecution checks the `execution` object.
//
// The bounds are checked through the nil-safe resolvers rather than against
// the raw fields, which is what makes the defaults and the stated values one
// rule: a configuration that states nothing travels the same sentences and
// proves that what an absent key means is also what the loader accepts.
func validateExecution(c *File) error {
	if err := validateExecutionBounds(c.Execution); err != nil {
		return WithDiagnostic(DiagnosticExecution, err)
	}
	if c.Execution == nil {
		return nil
	}
	if err := validateExecutionNode(c.Execution); err != nil {
		return WithDiagnostic(DiagnosticExecution, err)
	}
	return nil
}

// validateExecutionBounds checks the role and the numbers: what this node is,
// how much it takes on, how long it waits and how much it moves.
func validateExecutionBounds(x *ExecutionConfig) error {
	if role := x.ResolveRole(); role != public.ExecutionRoleOrchestrator && role != public.ExecutionRoleWorker {
		return fmt.Errorf("execution.role %q is invalid (want %q or %q)",
			role, public.ExecutionRoleOrchestrator, public.ExecutionRoleWorker)
	}
	if capacity := x.ResolveConcurrency(); capacity < 1 {
		return fmt.Errorf("execution.concurrency must be >= 1, got %d: a node that accepts no task is a node to remove rather than to configure", capacity)
	}
	timeouts := x.ResolveTimeouts()
	transfer := x.ResolveTransfer()
	for _, bound := range []struct {
		key   string
		value int64
	}{
		{"execution.timeouts.preflight", int64(timeouts.Preflight)},
		{"execution.timeouts.task", int64(timeouts.Task)},
		{"execution.timeouts.cancel", int64(timeouts.Cancel)},
		{"execution.transfer.maxFiles", int64(transfer.MaxFiles)},
		{"execution.transfer.maxBytes", transfer.MaxBytes},
		{"execution.transfer.maxManifestBytes", transfer.MaxManifestBytes},
		{"execution.transfer.timeout", int64(transfer.Timeout)},
	} {
		if bound.value < 0 {
			return fmt.Errorf("%s must be >= 0, got %d", bound.key, bound.value)
		}
	}
	return nil
}

// validateExecutionNode checks what the node says about itself and about the
// nodes it delegates to: the names, the mailboxes and the secret that signs
// what travels between them.
func validateExecutionNode(x *ExecutionConfig) error {
	if x.Name != "" && !isExecutionNodeName(x.Name) {
		return fmt.Errorf("execution.name %q is not a node name (letters, digits, dot, underscore and hyphen, with no two dots in a row)", x.Name)
	}
	if x.Endpoint != "" {
		if err := gitx.RequireTransportEndpoint(x.Endpoint); err != nil {
			return fmt.Errorf("execution.endpoint: %w", err)
		}
	}
	if x.SecretEnv != "" && !release.IsValidEnvName(x.SecretEnv) {
		return fmt.Errorf("execution.secretEnv %q is not an environment variable name: it names the variable holding the signing secret, never the secret itself", x.SecretEnv)
	}
	if x.IsWorker() && x.IsDistributed() {
		return fmt.Errorf("execution.workers is not a worker's to state: a worker executes the tasks it is given and delegates nothing")
	}
	if err := validateExecutionWorkers(x.Workers); err != nil {
		return err
	}
	// A run that dispatches has to sign what it sends, and the secret is the
	// one part of the configuration that cannot be defaulted. It is required
	// here rather than at dispatch so the reader hears about it before the
	// locks rather than after them; whether the variable is actually set is a
	// question for the run.
	if x.IsDistributed() && x.SecretEnv == "" {
		return fmt.Errorf("execution.secretEnv is required with execution.workers: every message a mailbox carries is signed with the secret it names")
	}
	return nil
}

// validateExecutionWorkers checks the worker links on their own: each names a
// node, the names are distinct, and each mailbox is a remote git could be
// pointed at.
func validateExecutionWorkers(workers []ExecutionWorkerConfig) error {
	namedBy := map[string]int{}
	for i, worker := range workers {
		label := fmt.Sprintf("execution.workers[%d]", i)
		if worker.Name == "" {
			return fmt.Errorf("%s: name is required: it is how a node recognises the work addressed to it", label)
		}
		if !isExecutionNodeName(worker.Name) {
			return fmt.Errorf("%s: name %q is not a node name (letters, digits, dot, underscore and hyphen, with no two dots in a row)", label, worker.Name)
		}
		// Folded, because two spellings of one name would be two links to one
		// node, and the second would silently take the first one's work.
		folded := lib.Fold(worker.Name)
		if previous, isDuplicate := namedBy[folded]; isDuplicate {
			return fmt.Errorf("%s: name %q is already used by execution.workers[%d]", label, worker.Name, previous)
		}
		namedBy[folded] = i
		if worker.Endpoint == "" {
			return fmt.Errorf("%s: endpoint is required: it is the mailbox this node's work is left in", label)
		}
		if err := gitx.RequireTransportEndpoint(worker.Endpoint); err != nil {
			return fmt.Errorf("%s: endpoint: %w", label, err)
		}
	}
	return nil
}
