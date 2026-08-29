package harness

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Repo is a disposable git monorepo driven through the real dispat and
// tsmark binaries. It mirrors the fixture pattern of the cli package's own
// end-to-end tests (initRepo + a "git" closure), promoted to a reusable type
// because this module cannot — by design — reach into that internal package.
type Repo struct {
	T    testing.TB
	Root string

	dispatBin, tsmarkBin string
	// workDir is the folder invocations run *from*, empty for the test
	// process's own. See WorkFrom.
	workDir string
}

// New creates an empty git repository in a fresh temp directory. Tests still
// need to write a config and seed at least one package before a run does
// anything.
func New(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dispatBin, tsmarkBin := Build(t)
	r := &Repo{T: t, Root: t.TempDir(), dispatBin: dispatBin, tsmarkBin: tsmarkBin}
	r.Git("init", "-q")
	// Pin the branch name instead of inheriting the host's init.defaultBranch,
	// which differs between developer machines and CI runners. Anything that
	// pushes, clones or names a branch would otherwise pass in one environment
	// and quietly prove nothing in the other.
	r.Git("symbolic-ref", "HEAD", "refs/heads/"+DefaultBranch)
	r.Git("config", "user.email", "integration@dispat.test")
	r.Git("config", "user.name", "dispat integration")
	return r
}

// DefaultBranch is the branch every harness repository and bare remote starts
// on.
const DefaultBranch = "main"

// WorkFrom makes every later invocation run *from* this folder of the
// repository, the way a shell in that folder would. Only what dispat reads
// from the current directory notices — the `.env` file — since the harness
// passes --root either way.
func (r *Repo) WorkFrom(relPath ...string) {
	r.workDir = r.Path(relPath...)
}

// Path joins parts onto the repository root.
func (r *Repo) Path(parts ...string) string {
	return filepath.Join(append([]string{r.Root}, parts...)...)
}

// Git runs `git -C <root> <args...>`, failing the test on error, and returns
// trimmed combined output.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput()
	require.NoError(r.T, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// WriteFile writes a file relative to the repository root, creating parent
// directories as needed. It does not stage or commit anything.
func (r *Repo) WriteFile(relPath, contents string) {
	r.T.Helper()
	full := r.Path(relPath)
	require.NoError(r.T, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(r.T, os.WriteFile(full, []byte(contents), 0o644))
}

// Remove deletes a file relative to the repository root — typically an
// untracked failure marker a scenario plants and later lifts, without
// needing a commit either way.
func (r *Repo) Remove(relPath string) {
	r.T.Helper()
	require.NoError(r.T, os.Remove(r.Path(relPath)))
}

// WriteConfig writes the repository's dispat.json (the --config default).
func (r *Repo) WriteConfig(cfg string) { r.WriteFile("dispat.json", cfg) }

// SeedPackage creates a package folder with one tracked file whose content
// is the package name plus a newline, so the package exists as a
// discoverable direct sub-folder of spacePath (relative to the root, e.g.
// "packages/libs") before any commit.
func (r *Repo) SeedPackage(spacePath, name string) {
	r.WriteFile(filepath.Join(spacePath, name, "main.txt"), name+"\n")
}

// AddBareRemote creates a bare repository in a fresh temp directory and
// registers it as "origin", returning its path — the fixture of every push
// scenario.
func (r *Repo) AddBareRemote() string {
	r.T.Helper()
	bare := r.T.TempDir()
	r.Git("init", "-q", "--bare", bare)
	// Same reason as New: a bare repository's HEAD decides what a later clone
	// checks out, and a clone landing on an unborn branch pushes somewhere the
	// original never tracks.
	out, err := exec.Command("git", "-C", bare, "symbolic-ref", "HEAD", "refs/heads/"+DefaultBranch).CombinedOutput()
	require.NoError(r.T, err, "git symbolic-ref: %s", out)
	r.Git("remote", "add", "origin", bare)
	return bare
}

// Commit stages every change and commits it with msg.
func (r *Repo) Commit(msg string) {
	r.T.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", msg)
}

// CommitAs stages every change and commits it under a named git identity.
//
// New puts one fixed identity on the repository, which is the right default:
// almost nothing a release does depends on who wrote a commit. Attribution
// does, and it cannot be tested at all until two commits are by two different
// people, so this is the one helper that steps around the fixture identity.
// The identity is passed per invocation rather than configured, so a test can
// alternate authors without leaving the repository in a changed state.
func (r *Repo) CommitAs(name, email, msg string) {
	r.T.Helper()
	r.Git("add", "-A")
	r.Git("-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "-m", msg)
}

// CommitEmpty commits msg with no file changes — a directive-only unit that
// needs no content of its own (a hold, a resume, a cancel, a graduation).
func (r *Repo) CommitEmpty(msg string) {
	r.T.Helper()
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
}

// TagList returns every tag in the repository.
func (r *Repo) TagList() []string {
	out := r.Git("tag")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// HasTag reports whether the exact tag name exists. For "was this package
// tagged at all", use TagCount with the "pkg@" prefix — an exact match
// against a bare prefix is always false and would pass vacuously.
func (r *Repo) HasTag(name string) bool {
	for _, t := range r.TagList() {
		if t == name {
			return true
		}
	}
	return false
}

// TagCount returns how many tags start with prefix — the idiom for "how many
// releases has pkg had" (prefix "pkg@"), which is how the catch-up and
// convergence scenarios assert that a package was, or was not, re-released.
func (r *Repo) TagCount(prefix string) int {
	n := 0
	for _, t := range r.TagList() {
		if strings.HasPrefix(t, prefix) {
			n++
		}
	}
	return n
}

// TsmarkScript returns a shell command usable as any script or hook entry:
// it appends "<label> start <ns>" / "<label> end <ns>" lines to logFile
// (relative to the repository root), sleeping in between. label is
// interpolated into the command line unquoted, so a caller may pass a shell
// expression such as "$DISPAT_PACKAGE" to get one shared script definition
// that labels itself per invocation, or a plain literal for a fixed label.
func (r *Repo) TsmarkScript(logFile, label string, sleep time.Duration) string {
	return fmt.Sprintf("%s %s %s %d",
		shQuote(r.tsmarkBin), shQuote(r.Path(logFile)), label, sleep.Milliseconds())
}

// Timeline parses logFile (relative to the repository root) as a tsmark log.
func (r *Repo) Timeline(logFile string) []Interval {
	return ParseTimeline(r.T, r.Path(logFile))
}

// RunResult is the outcome of one dispat invocation.
type RunResult struct {
	Code           int
	Stdout, Stderr string
	Events         []Event
}

// Status runs `dispat status`.
func (r *Repo) Status(flags ...string) RunResult {
	return r.run(append([]string{"status"}, flags...)...)
}

// StatusOK runs `dispat status` and requires exit code 0.
func (r *Repo) StatusOK(flags ...string) RunResult {
	r.T.Helper()
	return r.requireOK(r.Status(flags...))
}

// Release runs `dispat release` (also the default command with no verb).
func (r *Repo) Release(flags ...string) RunResult {
	return r.run(flags...)
}

// RunScript runs `dispat run <name>`.
func (r *Repo) RunScript(name string, flags ...string) RunResult {
	return r.run(append([]string{"run", name}, flags...)...)
}

// RunScriptOK runs `dispat run <name>` and requires exit code 0.
func (r *Repo) RunScriptOK(name string, flags ...string) RunResult {
	r.T.Helper()
	return r.requireOK(r.RunScript(name, flags...))
}

// Command runs an arbitrary dispat invocation — "init", "test", "preview", a
// bare run-script word — for the commands the named helpers above do not
// cover. --root is appended like everywhere else.
func (r *Repo) Command(args ...string) RunResult {
	return r.run(args...)
}

// CommandAt runs an arbitrary dispat invocation with --root pointing at a
// folder *inside* the repository (relPath, relative to its root) instead of
// the root itself — how a user standing in a package folder invokes the CLI,
// which is what the config ascent and the run shorthand's package narrowing
// exist for.
func (r *Repo) CommandAt(relPath string, args ...string) RunResult {
	return r.runAt(r.Path(relPath), args...)
}

// ReleaseOK runs `dispat release` and requires exit code 0, with the run's
// output in the failure message. Scenarios expecting a non-zero exit — a
// failing package, a refused release — use plain Release and assert the code
// themselves.
func (r *Repo) ReleaseOK(flags ...string) RunResult {
	r.T.Helper()
	return r.requireOK(r.Release(flags...))
}

func (r *Repo) requireOK(res RunResult) RunResult {
	r.T.Helper()
	require.Equal(r.T, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	return res
}

// CommandEnv runs an arbitrary dispat invocation with extra environment
// pairs appended — how a scenario hands the binary a stage-script
// environment (DISPAT_OUTPUT and friends) it would otherwise only see inside
// a release run.
func (r *Repo) CommandEnv(env []string, args ...string) RunResult {
	r.T.Helper()
	return r.runAtEnv(r.Root, env, "", args...)
}

// CommandInput runs an arbitrary dispat invocation with stdin already holding
// the answers — how a scenario drives an interactive prompt (`compute
// --interactive`) through the process boundary rather than through the option
// struct.
func (r *Repo) CommandInput(stdin string, args ...string) RunResult {
	r.T.Helper()
	return r.runAtEnv(r.Root, nil, stdin, args...)
}

// DispatCommand renders a shell command invoking the dispat binary under
// test with the given arguments — what a scenario writes into a config
// script slot so a release stage runs a nested dispat command, exactly as a
// real flow would.
func (r *Repo) DispatCommand(args ...string) string {
	return shQuote(r.dispatBin) + " " + strings.Join(args, " ")
}

// Shell runs a shell script with the binary under test first on PATH as
// `dispat`, which is what a provisioning script or an install manifest actually
// is: a file of ordinary command lines, run by sh rather than by a test. The
// script is written exactly as its author would write it, and the symlink is
// what makes the name resolve to the binary under test.
//
// The environment is every other invocation's, coverage wiring included, so the
// dispat processes the script spawns contribute their counters like any other
// run's. A script writes its own `set -e` when it wants one: whether a manifest
// stops at the first failure is exactly what some of these scenarios are about.
func (r *Repo) Shell(script string) RunResult {
	r.T.Helper()
	binDir := r.T.TempDir()
	require.NoError(r.T, os.Symlink(r.dispatBin, filepath.Join(binDir, "dispat")))
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = r.Root
	cmd.Env = append(append(baseEnv(), defaultEnv...),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if dir := coverDir(); dir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			r.T.Fatalf("launching the shell: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return RunResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String(), Events: ParseEvents(stdout.String())}
}

// CommandBin runs an arbitrary invocation of a *different* dispat binary —
// one BuildVersioned stamped with a version, or one a self-update just put in
// place. Everything else is the ordinary path, so the counters of the binary
// being driven land in the coverage profile like any other run's.
func (r *Repo) CommandBin(bin string, args ...string) RunResult {
	r.T.Helper()
	return r.runBin(bin, r.Root, nil, "", args...)
}

// CommandBinEnv is CommandBin with extra environment pairs, which is how the
// self-update scenarios turn the update check back on for one invocation.
func (r *Repo) CommandBinEnv(bin string, env []string, args ...string) RunResult {
	r.T.Helper()
	return r.runBin(bin, r.Root, env, "", args...)
}

// run executes the dispat binary against this repository, always appending
// --root. It fails the test only when the binary could not be launched at
// all — a non-zero exit is a normal outcome most scenarios assert on.
func (r *Repo) run(args ...string) RunResult {
	r.T.Helper()
	return r.runAt(r.Root, args...)
}

// runAt is run with an explicit --root value.
func (r *Repo) runAt(root string, args ...string) RunResult {
	r.T.Helper()
	return r.runAtEnv(root, nil, "", args...)
}

// runAtEnv is runAt with extra environment pairs appended and, for the
// commands that ask questions, stdin already holding the answers.
func (r *Repo) runAtEnv(root string, env []string, stdin string, args ...string) RunResult {
	r.T.Helper()
	return r.runBin(r.dispatBin, root, env, stdin, args...)
}

// runBin is the single choke point every invocation goes through, whichever
// binary is being driven.
func (r *Repo) runBin(bin, root string, env []string, stdin string, args ...string) RunResult {
	r.T.Helper()
	cmd := exec.Command(bin, withRoot(args, root)...)
	cmd.Dir = r.workDir // empty inherits the test process's folder
	// No test may reach api.github.com, and most fixtures have no remote to
	// take the release lock on. Both kill switches go in before the caller's
	// own pairs, and exec keeps the last value for a repeated key, so a
	// scenario that wants the update check — or the lock — asks for it back by
	// appending its own value.
	cmd.Env = append(append(baseEnv(), defaultEnv...), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if dir := coverDir(); dir != "" {
		// The instrumented binary (see binary.go) writes its counters here.
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			r.T.Fatalf("launching dispat: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return RunResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String(), Events: ParseEvents(stdout.String())}
}

// withRoot appends dispat's own --root to an invocation, ahead of any `--`
// the scenario typed.
//
// `--root` is dispat's flag, not the script's, and everything after `--`
// belongs to whatever `dispat run` or `dispat exec` is about to execute. A
// scenario forwarding arguments would otherwise hand the script the harness's
// plumbing and leave the binary pointed at the test process's own working
// directory. Without a `--`, this is the append it always was.
func withRoot(args []string, root string) []string {
	at := len(args)
	for i, a := range args {
		if a == "--" {
			at = i
			break
		}
	}
	full := make([]string, 0, len(args)+2)
	full = append(full, args[:at]...)
	full = append(full, "--root", root)
	return append(full, args[at:]...)
}

// defaultEnv is what every invocation starts from unless the scenario says
// otherwise. See runBin for why each entry is here.
var defaultEnv = []string{"DISPAT_UPDATE_CHECK=0", "DISPAT_UNSAFE_DISABLE_LOCK=true"}

// itEnvPrefix is the suite's own namespace in the environment: the fixtures
// name it in their configs (token-env, static env) and it must reach the
// binary, unlike everything else dispat spells DISPAT_.
const itEnvPrefix = "DISPAT_IT_"

// baseEnv is the parent environment with dispat's own variables taken out.
//
// The binary reads DISPAT_* from its environment as a matter of design:
// DISPAT_PACKAGE tells a nested step whose environment it was handed, and the
// release variables are exactly what `dispat exec --env` is asked to produce
// or not produce. Inheriting them means the fixture is no longer the only
// thing deciding what the binary sees.
//
// That is not hypothetical. This suite runs as the `dispat` package's
// tests:integration script, so its own parent is a `dispat run` sweep that
// exports a full release environment — and a test asserting a version is
// absent outside a release would read the sweep's instead, and pass or fail on
// how it was invoked rather than on what it tests.
//
// Everything else is inherited: PATH, HOME and the Go toolchain variables are
// what let the binary run at all. defaultEnv is appended afterwards, so the
// two kill switches survive being stripped here.
func baseEnv() []string {
	all := os.Environ()
	out := make([]string, 0, len(all))
	for _, kv := range all {
		if strings.HasPrefix(kv, "DISPAT_") && !strings.HasPrefix(kv, itEnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// LockEnabled is what a lock scenario appends to bring the release lock back
// for one run, on top of a repository that has a remote to take it on.
var LockEnabled = []string{"DISPAT_UNSAFE_DISABLE_LOCK=false"}

// Proc is a dispat invocation started but not waited for — the fixture of the
// interruption scenarios, which signal the process mid-run and then assert on
// its outcome.
type Proc struct {
	repo           *Repo
	cmd            *exec.Cmd
	stdout, stderr *bytes.Buffer
}

// StartRelease launches `dispat release` (with --root appended, coverage
// wiring included) and returns without waiting. The caller drives the process
// through Signal and collects the outcome with Wait.
func (r *Repo) StartRelease(flags ...string) *Proc {
	r.T.Helper()
	return r.StartReleaseEnv(nil, flags...)
}

// StartReleaseEnv is StartRelease with extra environment pairs appended, for
// the scenarios that interrupt a run the release lock is guarding.
func (r *Repo) StartReleaseEnv(env []string, flags ...string) *Proc {
	r.T.Helper()
	full := append(append([]string{}, flags...), "--root", r.Root)
	cmd := exec.Command(r.dispatBin, full...)
	cmd.Env = append(append(baseEnv(), defaultEnv...), env...)
	if dir := coverDir(); dir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+dir)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(r.T, cmd.Start(), "launching dispat")
	return &Proc{repo: r, cmd: cmd, stdout: &stdout, stderr: &stderr}
}

// Signal delivers sig to the running dispat process (os.Interrupt is what a
// Ctrl-C or a CI cancellation sends).
func (p *Proc) Signal(sig os.Signal) {
	p.repo.T.Helper()
	require.NoError(p.repo.T, p.cmd.Process.Signal(sig))
}

// Wait blocks until the process exits and returns the run's outcome. The
// output buffers are only read here, after the process is gone, so the
// harness needs no synchronisation around them.
func (p *Proc) Wait() RunResult {
	p.repo.T.Helper()
	err := p.cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			p.repo.T.Fatalf("waiting for dispat: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return RunResult{Code: code, Stdout: p.stdout.String(), Stderr: p.stderr.String(), Events: ParseEvents(p.stdout.String())}
}

// shQuote single-quotes s for safe interpolation into a /bin/sh -c command
// line (the shell every script in these tests runs through by default).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
