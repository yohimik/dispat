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
	r.Git("config", "user.email", "integration@dispat.test")
	r.Git("config", "user.name", "dispat integration")
	return r
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
	r.Git("remote", "add", "origin", bare)
	return bare
}

// Commit stages every change and commits it with msg.
func (r *Repo) Commit(msg string) {
	r.T.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-q", "-m", msg)
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
	full := append(append([]string{}, args...), "--root", root)
	cmd := exec.Command(bin, full...)
	// No test may reach api.github.com. The kill switch goes in before the
	// caller's own pairs, and exec keeps the last value for a repeated key, so
	// a scenario that wants the update check can still ask for it.
	cmd.Env = append(append(os.Environ(), "DISPAT_UPDATE_CHECK=0"), env...)
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
	full := append(append([]string{}, flags...), "--root", r.Root)
	cmd := exec.Command(r.dispatBin, full...)
	if dir := coverDir(); dir != "" {
		cmd.Env = append(os.Environ(), "GOCOVERDIR="+dir)
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
