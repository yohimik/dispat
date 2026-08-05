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

// GithubDisabled is the `"github": {"enabled": false}` config fragment nearly
// every test config includes, so a run never reaches the real GitHub API even
// when GITHUB_TOKEN / GITHUB_REPOSITORY happen to be set in the environment
// (e.g. a CI job running this very suite).
const GithubDisabled = `"github": {"enabled": false}`

// Base returns the config tail shared by nearly every test: the given
// concurrency (raw JSON — "1" or "[4, 2]"), JSON logging so the run parses
// into Events, and GitHub disabled. Callers concatenate it after their
// scripts/spaces/dependencies keys; a test overriding any of these writes
// its config in full instead.
func Base(concurrency string) string {
	return `"concurrency": ` + concurrency + `,
  "logLevel": "info",
  "logFormat": "json",
  ` + GithubDisabled
}

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

// run executes the dispat binary against this repository, always appending
// --root. It fails the test only when the binary could not be launched at
// all — a non-zero exit is a normal outcome most scenarios assert on.
func (r *Repo) run(args ...string) RunResult {
	r.T.Helper()
	full := append(append([]string{}, args...), "--root", r.Root)
	cmd := exec.Command(r.dispatBin, full...)
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

// shQuote single-quotes s for safe interpolation into a /bin/sh -c command
// line (the shell every script in these tests runs through by default).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
