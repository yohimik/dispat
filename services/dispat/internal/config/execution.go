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

	"github.com/spf13/pflag"

	lib "github.com/yohimik/dispat/pkg/config"
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/envname"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
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

// LoadNode reads a configuration as a serving node's rather than as a
// release's.
//
// The difference is one rule: a file that declares no space and no package is
// complete here. A node serving tasks is told what to do by the assignments
// it receives, so a machine that only ever serves has nothing to declare
// beyond what it is, and refusing its file for holding no packages would be
// refusing it for being exactly what it is. Everything else about the load is
// the same, the `execution` object's own validation included.
func LoadNode(path string, flags *pflag.FlagSet) (*File, error) {
	return load(path, flags, true)
}

// DiagnosticExecution reports a configuration no distributed run could be
// executed under. As the configuration is read, that is an `execution` object
// with an unknown role, a capacity that is not a capacity, a worker link that
// names no node or states an endpoint no mailbox could be, or a missing
// signing secret; and it is a `buildOutputs` or `buildPlatforms` list that
// does not describe a place a build product can travel from, two packages that
// claimed one folder included. A run that dispatches reports the same code for
// what only it can decide: before any lock, a lock bypass, a lock it could not
// read back, a missing secret or a release remote a link with no endpoint may
// not reach; and once the plan is fixed, a stage pinned to a worker it has
// none of, a platform no node satisfies or a node that failed preflight. No
// command has run whichever it is.
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
	// Known hazard, left as it stands on purpose: `maxManifestBytes` bounds
	// every document of the coordination protocol and not only an output
	// manifest, so a value below the size of an ordinary assignment makes the
	// profile unusable rather than merely strict. Refusing such a value at
	// load, and bounding protocol documents by a ceiling of their own, are both
	// behaviour changes that existing fences pin the current meaning of, so the
	// choice belongs to the owner rather than to a passing validation change.
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
	if x.SecretEnv != "" && !envname.IsValid(x.SecretEnv) {
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
// node, the names are distinct, and each endpoint a link states is a remote
// git could be pointed at. A link that states none reaches the repository
// being released, whose remote is resolved and held to the same rules when a
// run that dispatches starts.
func validateExecutionWorkers(workers []ExecutionWorkerConfig) error {
	namedBy := map[string]string{}
	for i, worker := range workers {
		if err := validateExecutionWorker(fmt.Sprintf("execution.workers[%d]", i), worker, namedBy); err != nil {
			return err
		}
	}
	return nil
}

// validateExecutionWorker checks one link, under the label that tells the
// reader where it was stated, against the names the links before it took.
func validateExecutionWorker(label string, worker ExecutionWorkerConfig, namedBy map[string]string) error {
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
		return fmt.Errorf("%s: name %q is already used by %s", label, worker.Name, previous)
	}
	namedBy[folded] = label
	if worker.Endpoint == "" {
		return nil
	}
	if err := gitx.RequireTransportEndpoint(worker.Endpoint); err != nil {
		return fmt.Errorf("%s: endpoint: %w", label, err)
	}
	return nil
}

// WorkerFlag is the command-line flag that names a worker node for one
// invocation, `--worker name` or `--worker name=endpoint`, repeatable. It
// exists for the machine a pipeline created a minute before the run: a link
// the committed file cannot know about, stated where the pipeline knows it. A
// name alone reaches the repository being released, as a configured link with
// no endpoint does.
const WorkerFlag = "worker"

// DiagnosticExecutionAuthority reports execution refused because of who asked
// for it. Its one use in this package is a `--worker` given to a node whose
// file calls it a worker: a worker never dispatches to a pool, whether the
// link it would dispatch to is written in a file or on a command line.
const DiagnosticExecutionAuthority = "E226"

// ParseWorkerLink reads one `--worker` value as the link it names: a node name
// alone, or name=endpoint with both halves stated.
//
// The value is never echoed. The half after the separator is an endpoint, and
// a malformed one is exactly the value that may be carrying a credential; a
// value with no separator that is not a node name is refused here for the
// same reason, because it is most likely an endpoint somebody wrote without
// its name, and the rule that would otherwise refuse it quotes what it read.
func ParseWorkerLink(value string) (ExecutionWorkerConfig, error) {
	name, endpoint, isPaired := strings.Cut(value, "=")
	if isPaired && name != "" && endpoint != "" {
		return ExecutionWorkerConfig{Name: name, Endpoint: endpoint}, nil
	}
	if !isPaired && isExecutionNodeName(name) {
		return ExecutionWorkerConfig{Name: name}, nil
	}
	return ExecutionWorkerConfig{}, fmt.Errorf(
		"--%s takes name or name=endpoint: the node's name, alone to reach the repository being released, or with both halves stated to reach another mailbox",
		WorkerFlag)
}

// appendCommandLineWorkers adds the links the invocation named to the entry
// configuration's own, before anything is validated.
//
// Before, so that a link stated on a command line is held to every rule a
// link written in the file is: the name, the endpoint when it states one, the
// folded uniqueness against the file's own links and the signing secret the
// file has to name.
// Each is checked here under the flag's own label first, so that a refusal
// names the value the operator typed rather than an index into a list they
// never wrote, and the whole list is validated again with everything else.
//
// A node whose file calls it a worker is refused outright, with the authority
// code: a worker never dispatches to a pool, which is the rule a `workers`
// list in its file is refused by.
func appendCommandLineWorkers(c *File, flags *pflag.FlagSet) error {
	values := readWorkerFlag(flags)
	if len(values) == 0 {
		return nil
	}
	if c.Execution.IsWorker() {
		return WithDiagnostic(DiagnosticExecutionAuthority, fmt.Errorf(
			"--%s names a node this invocation would dispatch to, and execution.role is %q on this node: "+
				"a worker executes the tasks it is given and dispatches nothing",
			WorkerFlag, c.Execution.ResolveRole()))
	}
	if c.Execution == nil {
		c.Execution = &ExecutionConfig{}
	}
	namedBy := map[string]string{}
	for i, worker := range c.Execution.Workers {
		namedBy[lib.Fold(worker.Name)] = fmt.Sprintf("execution.workers[%d]", i)
	}
	for _, value := range values {
		link, err := ParseWorkerLink(value)
		if err != nil {
			return WithDiagnostic(DiagnosticExecution, err)
		}
		label := fmt.Sprintf("--%s %s", WorkerFlag, link.Name)
		if err := validateExecutionWorker(label, link, namedBy); err != nil {
			return WithDiagnostic(DiagnosticExecution, err)
		}
		c.Execution.Workers = append(c.Execution.Workers, link)
	}
	return nil
}

// readWorkerFlag is every `--worker` value the invocation passed, in order,
// and nothing for a flag set that does not declare the flag or never set it.
func readWorkerFlag(flags *pflag.FlagSet) []string {
	if flags == nil {
		return nil
	}
	flag := flags.Lookup(WorkerFlag)
	if flag == nil || !flag.Changed {
		return nil
	}
	values, isList := flag.Value.(pflag.SliceValue)
	if !isList {
		return nil
	}
	return values.GetSlice()
}
