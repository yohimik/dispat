package main

// The sharded half of `testreport test`: one suite split over several go test
// processes, kept as the one log a single process would have written.
//
// A suite that spends its time waiting (polls, preflights, sleeps between the
// steps of a scenario) is bound by the clock rather than by the processor, so
// a slower machine stretches it and more cores do not shorten it. Several
// processes wait at once without a test being rewritten for it, which is what
// a shard is.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// testListPattern is the -list pattern that finds every function -run
// selects: tests, fuzz targets (which run their seed corpus under a plain go
// test) and examples. Benchmarks are left out because -run never selects one.
const testListPattern = "^(Test|Fuzz|Example)"

// testNamePrefixes are the prefixes testListPattern admits, which is how a
// name is told from the rest of the listing's output.
var testNamePrefixes = []string{"Test", "Fuzz", "Example"}

// unshardableFlags are the go test flags a sharded run refuses, each with the
// reason it gives. The shards choose their tests with -run, so a selection of
// the caller's own would be overridden without a word; every flag naming an
// output file would have each shard write that one file over the others'.
// -coverprofile is the exception the run knows how to split and merge. -json
// is the run's own: a listing written as events would hide every test name.
var unshardableFlags = map[string]string{
	"run":          "the shards choose their tests with -run",
	"list":         "the shards choose their tests with -run",
	"json":         "testreport adds -json to every shard itself",
	"bench":        "every shard would run the same benchmarks",
	"fuzz":         "fuzzing runs one target in one process",
	"outputdir":    "every shard would write its files into the one folder",
	"cpuprofile":   "every shard would write the one profile",
	"memprofile":   "every shard would write the one profile",
	"blockprofile": "every shard would write the one profile",
	"mutexprofile": "every shard would write the one profile",
	"trace":        "every shard would write the one trace",
	"o":            "every shard would write the one test binary",
}

// runShardedGoTest is `testreport test --shards N`.
//
// The tests are listed with the caller's own arguments, so a test file behind
// a build constraint those arguments select (-race, -tags) is listed like any
// other, and the listing compiles the packages the shards then only link. The
// names are dealt round robin into at most N shares and each share runs as its
// own `go test <args> -run '^(names)$' -json`, all of them at once. Every
// listed test runs in exactly one shard, and a name two packages share runs in
// both of them in that one shard.
//
// The shards' streams become the one log `readLog` reads (see shardedLog), the
// summary is read back out of it, and the exit code is the first failing
// shard's. A shard that could not start or was killed fails the command with
// an error naming it. A -coverprofile is written per shard and merged into the
// file the caller named once every shard has finished.
func runShardedGoTest(invocation testInvocation, w io.Writer) (int, error) {
	names, err := listTestNames(invocation.goArgs)
	if err != nil {
		return 1, err
	}
	shares := splitRoundRobin(names, invocation.shardCount)
	if len(shares) < 2 {
		// One test, or none, is nothing to share: the single process runs it
		// exactly as an unsharded command line would.
		return runGoTestProcess(invocation, w)
	}
	fmt.Fprintf(w, "%s: go test %s -json, %d listed tests over %d shards\n",
		invocation.logName, strings.Join(invocation.goArgs, " "), len(names), len(shares))

	logFile, err := os.Create(invocation.logPath)
	if err != nil {
		return 1, err
	}
	sharedLog := newShardedLog(logFile)
	shards := newShardedRun(invocation.goArgs, shares, sharedLog)
	if err := shards.start(); err != nil {
		return 1, errors.Join(err, logFile.Close())
	}
	code, waitErr := shards.wait()
	if err := errors.Join(sharedLog.writeResults(), logFile.Close()); err != nil {
		return 1, errors.Join(waitErr, err)
	}

	if err := summarise(invocation.logPath, w); err != nil {
		logf(levelWarn, "could not summarise %s: %v; the tests themselves exited %d", invocation.logPath, err, code)
	}
	if waitErr != nil {
		return 1, waitErr
	}
	if err := shards.mergeCoverProfiles(); err != nil {
		return 1, err
	}
	return code, nil
}

// verifyShardableArgs refuses a go test command line a sharded run cannot
// honour: a flag in unshardableFlags, or a relative -coverprofile. go test
// resolves a relative profile against its own -C folder, while the shards'
// profiles are merged from here, so the path has to mean one file to both.
func verifyShardableArgs(goArgs []string) error {
	for _, arg := range goArgs {
		reason, isRefused := unshardableFlags[parseGoTestFlagName(arg)]
		if isRefused {
			return fmt.Errorf("--shards cannot pass %s to go test: %s", arg, reason)
		}
	}
	profile := findCoverProfile(goArgs)
	if profile != "" && !filepath.IsAbs(profile) {
		return fmt.Errorf("--shards needs an absolute -coverprofile path, got %q: go test resolves a relative one against its -C folder", profile)
	}
	return nil
}

// parseGoTestFlagName reads the flag a go test argument sets, spelled the way
// go test reads it whatever form the argument took: `-coverprofile=x`,
// `--coverprofile x` and `-test.coverprofile=x` all name `coverprofile`. It is
// empty for an argument that is not a flag.
func parseGoTestFlagName(arg string) string {
	if !strings.HasPrefix(arg, "-") {
		return ""
	}
	name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
	name = strings.TrimPrefix(name, "test.")
	name, _, _ = strings.Cut(name, "=")
	return name
}

// rewriteCoverProfile returns the command line with every coverage profile
// flag, in any spelling go test accepts, replaced by the arguments replace
// returns for the path it named. A trailing flag with no path is left for go
// test to report.
func rewriteCoverProfile(goArgs []string, replace func(profile string) []string) []string {
	rewritten := make([]string, 0, len(goArgs))
	for i := 0; i < len(goArgs); i++ {
		arg := goArgs[i]
		if parseGoTestFlagName(arg) != "coverprofile" {
			rewritten = append(rewritten, arg)
			continue
		}
		if _, profile, isJoined := strings.Cut(arg, "="); isJoined {
			rewritten = append(rewritten, replace(profile)...)
			continue
		}
		if i+1 == len(goArgs) {
			rewritten = append(rewritten, arg)
			continue
		}
		i++
		rewritten = append(rewritten, replace(goArgs[i])...)
	}
	return rewritten
}

// findCoverProfile returns the coverage profile the command line names, the
// last one when it names several, as go test takes it. Empty when there is
// none.
func findCoverProfile(goArgs []string) string {
	found := ""
	rewriteCoverProfile(goArgs, func(profile string) []string {
		found = profile
		return nil
	})
	return found
}

// formatShardCoverProfile names the profile one shard writes in place of the
// caller's.
func formatShardCoverProfile(profile string, shardNumber int) string {
	return profile + ".shard-" + strconv.Itoa(shardNumber)
}

// listTestNames asks go test which tests the command line would run.
//
// The listing keeps every argument but the coverage profile, which becomes a
// bare -cover: the build is the same instrumented one the shards need, and
// the listing writes no profile of its own over the caller's. A listing that
// fails is the run failing, because a package whose tests could not be listed
// would otherwise run none of them in every shard and pass.
func listTestNames(goArgs []string) ([]string, error) {
	listArgs := rewriteCoverProfile(goArgs, func(string) []string { return []string{"-cover"} })
	listArgs = append(append([]string{"test"}, listArgs...), "-list", testListPattern)
	listing, err := exec.Command("go", listArgs...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing the tests to shard: go %s: %w\n%s", strings.Join(listArgs, " "), err, listing)
	}
	return parseTestNames(string(listing)), nil
}

// parseTestNames reads the names out of a `go test -list` listing, in the
// order they were listed and each once. The listing interleaves them with the
// per-package `ok` lines and whatever a TestMain printed, and a name is the
// one line that is a lone identifier carrying a test prefix.
func parseTestNames(listing string) []string {
	seen := map[string]bool{}
	var names []string
	for _, line := range strings.Split(listing, "\n") {
		name := strings.TrimSpace(line)
		if !isTestName(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// isTestName reports whether a listing line names a test, fuzz target or
// example.
func isTestName(line string) bool {
	isPrefixed := slices.ContainsFunc(testNamePrefixes, func(prefix string) bool {
		return strings.HasPrefix(line, prefix)
	})
	if !isPrefixed {
		return false
	}
	for _, character := range line {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' {
			return false
		}
	}
	return true
}

// splitRoundRobin deals the names into at most shardCount shares, the i-th
// name to share i mod the share count. Neighbouring tests in a file tend to
// cost alike, so dealing them apart balances the shares better than cutting
// the list into runs would. There are never more shares than names, so no
// share is empty.
func splitRoundRobin(names []string, shardCount int) [][]string {
	shares := make([][]string, min(shardCount, len(names)))
	for i, name := range names {
		share := i % len(shares)
		shares[share] = append(shares[share], name)
	}
	return shares
}

// formatRunPattern is the -run pattern that selects exactly the names given,
// as whole top-level names.
func formatRunPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// shardedRun is one suite split over several go test processes that write
// into one log.
type shardedRun struct {
	// coverProfile is the profile the caller named, which the shards'
	// profiles are merged into. Empty when the command line names none.
	coverProfile string
	shards       []*testShard
}

// testShard is one of the processes a sharded run is split into.
type testShard struct {
	// number counts from 1, as the messages and the profile names give it.
	number     int
	shardCount int
	stream     *shardStream
	cmd        *exec.Cmd
}

// String names the shard in a message: `shard 2 of 6`.
func (s *testShard) String() string {
	return fmt.Sprintf("shard %d of %d", s.number, s.shardCount)
}

// newShardedRun prepares one go test process per share, each writing its
// stream into the shared log and its coverage profile beside the caller's.
func newShardedRun(goArgs []string, shares [][]string, sharedLog *shardedLog) *shardedRun {
	run := &shardedRun{coverProfile: findCoverProfile(goArgs)}
	for i, names := range shares {
		number := i + 1
		shardArgs := rewriteCoverProfile(goArgs, func(profile string) []string {
			return []string{"-coverprofile=" + formatShardCoverProfile(profile, number)}
		})
		shardArgs = append(append([]string{"test"}, shardArgs...), "-run", formatRunPattern(names), "-json")
		stream := newShardStream(sharedLog)
		cmd := exec.Command("go", shardArgs...)
		cmd.Stdout = stream
		cmd.Stderr = os.Stderr
		run.shards = append(run.shards, &testShard{number: number, shardCount: len(shares), stream: stream, cmd: cmd})
	}
	return run
}

// start starts every shard. A shard that cannot start stops the ones already
// running, so a failed start leaves no process behind.
func (r *shardedRun) start() error {
	for i, shard := range r.shards {
		if err := shard.cmd.Start(); err != nil {
			stopShards(r.shards[:i])
			return fmt.Errorf("%s could not start: %w", shard, err)
		}
	}
	return nil
}

// stopShards kills shards that are running and waits for them to go. The
// errors are the kill's own and say nothing the caller does not already know.
func stopShards(shards []*testShard) {
	for _, shard := range shards {
		_ = shard.cmd.Process.Kill()
		_ = shard.cmd.Wait()
	}
}

// wait waits for every shard and returns the code of the first one, in shard
// order, that failed. The error collects every shard that was killed or whose
// stream could not be kept, and waiting goes on past them, because the shards
// still running are still writing into the log.
func (r *shardedRun) wait() (int, error) {
	code := 0
	var problems []error
	for _, shard := range r.shards {
		shardCode, err := shard.wait()
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if code == 0 {
			code = shardCode
		}
	}
	return code, errors.Join(problems...)
}

// wait waits for the shard's process and closes its stream, and returns the
// code go test exited with.
func (s *testShard) wait() (int, error) {
	runErr := s.cmd.Wait()
	if err := s.stream.close(); err != nil {
		return 1, fmt.Errorf("%s: keeping its log: %w", s, err)
	}
	if runErr == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) {
		return 1, fmt.Errorf("%s: %w", s, runErr)
	}
	if !exit.Exited() {
		return 1, fmt.Errorf("%s was killed: %v", s, exit)
	}
	return exit.ExitCode(), nil
}

// mergeCoverProfiles writes the shards' coverage profiles into the one the
// caller named: the mode header once, then every shard's blocks, which is the
// shape the other merges of this repository produce and the coverage readers
// fold by block. The shards' own files are removed once the merge is written.
func (r *shardedRun) mergeCoverProfiles() error {
	if r.coverProfile == "" {
		return nil
	}
	var merged bytes.Buffer
	header := ""
	for _, shard := range r.shards {
		body, err := os.ReadFile(formatShardCoverProfile(r.coverProfile, shard.number))
		if err != nil {
			return fmt.Errorf("%s left no coverage profile: %w", shard, err)
		}
		shardHeader, blocks, _ := strings.Cut(string(body), "\n")
		if header == "" {
			header = shardHeader
			merged.WriteString(header + "\n")
		}
		if shardHeader != header {
			return fmt.Errorf("%s wrote a profile headed %q where the first shard wrote %q", shard, shardHeader, header)
		}
		merged.WriteString(blocks)
	}
	if err := os.WriteFile(r.coverProfile, merged.Bytes(), 0o644); err != nil {
		return err
	}
	var problems []error
	for _, shard := range r.shards {
		problems = append(problems, os.Remove(formatShardCoverProfile(r.coverProfile, shard.number)))
	}
	return errors.Join(problems...)
}

// packageEvent is the part of a `go test -json` event the sharded log reads:
// enough to tell a package's own result from everything else and to fold the
// shards' results into one. The fields are test2json's own, in its order, so
// a folded result is written the way go test writes one.
type packageEvent struct {
	Time        time.Time `json:"Time"`
	Action      string    `json:"Action"`
	Package     string    `json:"Package"`
	Test        string    `json:"Test,omitempty"`
	Elapsed     float64   `json:"Elapsed"`
	FailedBuild string    `json:"FailedBuild,omitempty"`
}

// isPackageResult reports whether the event is a package's own pass, fail or
// skip, rather than a test's result or progress.
func (e packageEvent) isPackageResult() bool {
	return e.Test == "" && e.Package != "" && event{Action: e.Action}.terminal()
}

// rankPackageResult orders the three results a package can end in, so folding
// keeps the worst: a failure anywhere is the package's failure.
func rankPackageResult(action string) int {
	switch action {
	case "fail":
		return 2
	case "pass":
		return 1
	default:
		return 0
	}
}

// foldPackageResults is one package's result across two shards: the worse
// result, the later time, and the longer elapsed time, since the shards ran
// the package side by side rather than one after the other.
func foldPackageResults(folded, next packageEvent) packageEvent {
	if rankPackageResult(next.Action) > rankPackageResult(folded.Action) {
		folded.Action = next.Action
	}
	if next.Time.After(folded.Time) {
		folded.Time = next.Time
	}
	folded.Elapsed = max(folded.Elapsed, next.Elapsed)
	if folded.FailedBuild == "" {
		folded.FailedBuild = next.FailedBuild
	}
	return folded
}

// shardedLog is the one `go test -json` log every shard of a run writes into.
//
// The reader keys every event by its package and test, so the shards' lines
// may interleave in any order as long as each line arrives whole. The one
// thing a single process writes once and the shards write once each is a
// package's own result, and `readLog` counts a package, and its elapsed time,
// per result. Those lines are therefore held back and folded, and written
// once per package after every shard has finished, which leaves the log
// saying what one process would have said about the same tests.
type shardedLog struct {
	mu   sync.Mutex
	file io.Writer
	// packages keeps the order the packages' first results arrived in, so the
	// folded results are written in a stable order.
	packages []string
	results  map[string]packageEvent
}

func newShardedLog(file io.Writer) *shardedLog {
	return &shardedLog{file: file, results: map[string]packageEvent{}}
}

// appendLine writes one whole line of a shard's stream.
func (l *shardedLog) appendLine(line []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.file.Write(line)
	return err
}

// foldResult holds one shard's result for a package until every shard is done.
func (l *shardedLog) foldResult(result packageEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	folded, isSeen := l.results[result.Package]
	if !isSeen {
		l.packages = append(l.packages, result.Package)
		l.results[result.Package] = result
		return
	}
	l.results[result.Package] = foldPackageResults(folded, result)
}

// writeResults writes each package's folded result, once.
func (l *shardedLog) writeResults() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, pkg := range l.packages {
		line, err := json.Marshal(l.results[pkg])
		if err != nil {
			return err
		}
		if _, err := l.file.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// shardStream is one shard's stdout. It cuts the stream into whole lines for
// the shared log, and remembers which packages the shard started and which it
// reported a result for.
//
// A write to the log that fails is kept rather than returned: returning it
// would close the shard's pipe under a go test that is still running tests,
// and the shard's end is where the failure is reported.
type shardStream struct {
	sharedLog *shardedLog
	pending   []byte
	started   map[string]bool
	reported  map[string]bool
	writeErr  error
}

func newShardStream(sharedLog *shardedLog) *shardStream {
	return &shardStream{sharedLog: sharedLog, started: map[string]bool{}, reported: map[string]bool{}}
}

// Write takes whatever the pipe delivered and passes on every line it
// completes, keeping the unfinished tail for the next write.
func (s *shardStream) Write(chunk []byte) (int, error) {
	s.pending = append(s.pending, chunk...)
	consumed := 0
	for {
		end := bytes.IndexByte(s.pending[consumed:], '\n')
		if end < 0 {
			break
		}
		s.passLine(s.pending[consumed : consumed+end+1])
		consumed += end + 1
	}
	s.pending = append(s.pending[:0], s.pending[consumed:]...)
	return len(chunk), nil
}

// passLine sends one whole line on: a package's result to be folded, anything
// else into the log as it is. A line that is not an event is kept verbatim,
// so the reader fails on it exactly as it would on the same line from one
// process.
func (s *shardStream) passLine(line []byte) {
	var ev packageEvent
	isEvent := json.Unmarshal(line, &ev) == nil
	if isEvent && ev.isPackageResult() {
		s.reported[ev.Package] = true
		s.sharedLog.foldResult(ev)
		return
	}
	if isEvent && ev.Test == "" && ev.Action == "start" {
		s.started[ev.Package] = true
	}
	if s.writeErr != nil {
		return
	}
	s.writeErr = s.sharedLog.appendLine(line)
}

// close ends the stream once the shard's process has exited: a last line the
// shard left unterminated is written as a whole one, and a package the shard
// started and never reported a result for fails, which is what a shard killed
// in the middle of a package leaves behind.
func (s *shardStream) close() error {
	if len(s.pending) > 0 {
		s.passLine(append(s.pending, '\n'))
		s.pending = nil
	}
	var unreported []string
	for pkg := range s.started {
		if !s.reported[pkg] {
			unreported = append(unreported, pkg)
		}
	}
	sort.Strings(unreported)
	for _, pkg := range unreported {
		s.sharedLog.foldResult(packageEvent{Time: time.Now(), Action: "fail", Package: pkg})
	}
	return s.writeErr
}
