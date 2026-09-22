package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// writeShardedWorkspace writes workspace's module with more files beside its
// own test, named relative to the module folder, for a run that spans
// packages.
func writeShardedWorkspace(t *testing.T, testBody string, files map[string]string) string {
	t.Helper()
	root := workspace(t, testBody)
	for name, body := range files {
		write(t, filepath.Join(root, "mod", name), body)
	}
	return root
}

// twoPackageSuite is a module whose tests are worth sharding: five tests in
// the root package, and a second package that shares one of their names and
// adds a test, a fuzz target with a seed and an example, so every kind -run
// selects is in the listing and one name belongs to two packages. A third
// package has no test files, which every shard reports as a skip.
const twoPackageSuite = `package mod

import "testing"

func TestAlpha(t *testing.T)   {}
func TestBravo(t *testing.T)   { t.Run("inner", func(t *testing.T) {}) }
func TestCharlie(t *testing.T) {}
func TestDelta(t *testing.T)   {}
func TestEcho(t *testing.T)    { Covered() }
`

var twoPackageFiles = map[string]string{
	"mod.go":         "package mod\n\nfunc Covered() int { return 1 }\n\nfunc Uncovered() int { return 2 }\n",
	"sub/sub.go":     "package sub\n\n// Hotel is what ExampleHotel shows.\nfunc Hotel() {}\n",
	"empty/empty.go": "package empty\n",
	"sub/sub_test.go": `package sub

import (
	"fmt"
	"testing"
)

func TestAlpha(t *testing.T)   {}
func TestFoxtrot(t *testing.T) {}
func FuzzGolf(f *testing.F) {
	f.Add(1)
	f.Fuzz(func(t *testing.T, n int) {})
}

func ExampleHotel() {
	fmt.Println("hotel")
	// Output: hotel
}
`,
}

// TestRewriteCoverProfileReadsEverySpelling: go test takes the profile flag
// with one dash or two, with or without the test. prefix, and with its path
// joined or as the next argument. Every spelling is found and renamed, the
// last one is the profile go test writes, and a flag with no path is left for
// go test to refuse.
func TestRewriteCoverProfileReadsEverySpelling(t *testing.T) {
	goArgs := []string{"-C", "sub", "./...", "-coverprofile", "/a.out", "--coverprofile=/b.out",
		"-test.coverprofile=/c.out", "-covermode=atomic", "--test.coverprofile", "/d.out"}
	renamed := rewriteCoverProfile(goArgs, func(profile string) []string {
		return []string{"-coverprofile=" + formatShardCoverProfile(profile, 2)}
	})
	want := []string{"-C", "sub", "./...", "-coverprofile=/a.out.shard-2", "-coverprofile=/b.out.shard-2",
		"-coverprofile=/c.out.shard-2", "-covermode=atomic", "-coverprofile=/d.out.shard-2"}
	if !slices.Equal(renamed, want) {
		t.Fatalf("rewriteCoverProfile = %q, want %q", renamed, want)
	}
	if got := findCoverProfile(goArgs); got != "/d.out" {
		t.Fatalf("findCoverProfile = %q, want the last one, /d.out", got)
	}
	trailing := []string{"./...", "-coverprofile"}
	if got := rewriteCoverProfile(trailing, func(string) []string { return []string{"-cover"} }); !slices.Equal(got, trailing) {
		t.Fatalf("a flag with no path became %q, want it left for go test", got)
	}
}

// TestSplitRoundRobinDealsEveryNameOnce: the split is an exact deal, the i-th
// name to share i mod the share count, and whatever order the names arrive in
// the shares hold every name once and nothing else. Fewer names than shards
// makes fewer shares rather than empty ones.
func TestSplitRoundRobinDealsEveryNameOnce(t *testing.T) {
	names := []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG"}
	want := [][]string{{"TestA", "TestD", "TestG"}, {"TestB", "TestE"}, {"TestC", "TestF"}}
	if got := splitRoundRobin(names, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("splitRoundRobin = %q, want the exact deal %q", got, want)
	}

	shuffled := slices.Clone(names)
	rand.New(rand.NewSource(7)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	for shardCount := 1; shardCount <= len(names)+2; shardCount++ {
		var dealt []string
		for _, share := range splitRoundRobin(shuffled, shardCount) {
			if len(share) == 0 {
				t.Fatalf("%d shards: an empty share", shardCount)
			}
			dealt = append(dealt, share...)
		}
		slices.Sort(dealt)
		if !slices.Equal(dealt, names) {
			t.Fatalf("%d shards dealt %q, want every name once: %q", shardCount, dealt, names)
		}
	}
	if got := len(splitRoundRobin(names[:2], 6)); got != 2 {
		t.Fatalf("two names over six shards made %d shares, want 2", got)
	}
	if got := len(splitRoundRobin(nil, 6)); got != 0 {
		t.Fatalf("no names made %d shares, want none", got)
	}
}

// TestParseTestNamesReadsOnlyNames: a listing interleaves the names with the
// per-package lines and whatever a TestMain printed. Only lone identifiers
// with a test prefix are names, in listing order, and a name two packages
// share is dealt once, since -run selects it in both.
func TestParseTestNamesReadsOnlyNames(t *testing.T) {
	listing := "building the fixture binary\nTestAlpha\nTestBravo\nFuzzGolf\n" +
		"ok  \texample.test/mod\t0.012s\tcoverage: 0.0% of statements\n" +
		"TestAlpha\nExampleHotel\nTesting the listing\n" +
		"?   \texample.test/mod/empty\t[no test files]\nFAIL\nBenchmarkIndia\n"
	want := []string{"TestAlpha", "TestBravo", "FuzzGolf", "ExampleHotel"}
	if got := parseTestNames(listing); !slices.Equal(got, want) {
		t.Fatalf("parseTestNames = %q, want %q", got, want)
	}
}

// TestFormatRunPatternSelectsWholeNames: the pattern matches exactly the
// names it was given and no name that merely starts with one of them.
func TestFormatRunPatternSelectsWholeNames(t *testing.T) {
	pattern := regexp.MustCompile(formatRunPattern([]string{"TestAlpha", "TestBravo"}))
	for name, isSelected := range map[string]bool{
		"TestAlpha": true, "TestBravo": true, "TestAlphaBeta": false, "XTestAlpha": false,
	} {
		if pattern.MatchString(name) != isSelected {
			t.Errorf("%s selects %s: %v, want %v", pattern, name, !isSelected, isSelected)
		}
	}
}

// TestShardedLogKeepsInterleavedLinesWhole: two shards write into one log at
// once, each cutting its lines wherever its pipe delivered them. Every line
// arrives whole, the reader sees both shards' tests, and the package both
// shards ran is one package with one result: the worse of the two, timed by
// the longer shard, since they ran side by side.
func TestShardedLogKeepsInterleavedLinesWhole(t *testing.T) {
	const pkg = "example.test/mod"
	first := []string{
		`{"Action":"start","Package":"` + pkg + `"}`,
		`{"Action":"run","Package":"` + pkg + `","Test":"TestAlpha"}`,
		`{"Action":"output","Package":"` + pkg + `","Test":"TestAlpha","Output":"=== RUN   TestAlpha\n"}`,
		`{"Action":"pass","Package":"` + pkg + `","Test":"TestAlpha","Elapsed":0.5}`,
		`{"Action":"pass","Package":"` + pkg + `","Elapsed":2}`,
	}
	second := []string{
		`{"Action":"start","Package":"` + pkg + `"}`,
		`{"Action":"run","Package":"` + pkg + `","Test":"TestBravo"}`,
		`{"Action":"output","Package":"` + pkg + `","Test":"TestBravo","Output":"    bravo_test.go:9: no\n"}`,
		`{"Action":"fail","Package":"` + pkg + `","Test":"TestBravo","Elapsed":0.25}`,
		`{"Action":"fail","Package":"` + pkg + `","Elapsed":3}`,
	}
	var file bytes.Buffer
	sharedLog := newShardedLog(&file)
	streams := []*shardStream{newShardStream(sharedLog), newShardStream(sharedLog)}
	// The first shard's every line is delivered in two pieces, and the second
	// shard writes a whole line of its own between them.
	for i := range first {
		line := first[i] + "\n"
		for _, delivery := range []struct {
			stream *shardStream
			chunk  string
		}{
			{streams[0], line[:len(line)/2]},
			{streams[1], second[i] + "\n"},
			{streams[0], line[len(line)/2:]},
		} {
			if _, err := delivery.stream.Write([]byte(delivery.chunk)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, stream := range streams {
		if err := stream.close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sharedLog.writeResults(); err != nil {
		t.Fatal(err)
	}

	results := 0
	for _, line := range strings.Split(strings.TrimSuffix(file.String(), "\n"), "\n") {
		var ev packageEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("a line arrived cut: %q: %v", line, err)
		}
		if ev.isPackageResult() {
			results++
		}
	}
	if results != 1 {
		t.Fatalf("the log holds %d results for the one package, want 1:\n%s", results, file.String())
	}
	log, err := readLog("unit", strings.NewReader(file.String()))
	if err != nil {
		t.Fatal(err)
	}
	if log.Tests != 2 || log.Passed != 1 || log.Failed != 1 || log.Packages != 1 || log.Elapsed != 3 {
		t.Fatalf("the reader saw %+v; want both shards' tests, one package, the longer shard's time", log.Counts)
	}
	if !log.failedPackages[pkg] || !log.failedTests[failedKey(pkg, "TestBravo")] {
		t.Fatalf("the failure did not survive the fold: packages %v, tests %v", log.failedPackages, log.failedTests)
	}
}

// TestShardStreamFailsWhatAKilledShardLeftUnreported: a shard killed in the
// middle of a package reports no result for it, and another shard's pass must
// not stand for the package. A last line without its newline still arrives as
// a whole line.
func TestShardStreamFailsWhatAKilledShardLeftUnreported(t *testing.T) {
	const pkg = "example.test/mod"
	var file bytes.Buffer
	sharedLog := newShardedLog(&file)
	finished, killed := newShardStream(sharedLog), newShardStream(sharedLog)
	for _, line := range []string{
		`{"Action":"start","Package":"` + pkg + `"}`,
		`{"Action":"run","Package":"` + pkg + `","Test":"TestAlpha"}`,
		`{"Action":"pass","Package":"` + pkg + `","Test":"TestAlpha","Elapsed":0.5}`,
		`{"Action":"pass","Package":"` + pkg + `","Elapsed":1}`,
	} {
		if _, err := finished.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := killed.Write([]byte(`{"Action":"start","Package":"` + pkg + `"}` + "\n" +
		`{"Action":"run","Package":"` + pkg + `","Test":"TestBravo"}`)); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []*shardStream{finished, killed} {
		if err := stream.close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sharedLog.writeResults(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(file.String(), "\n") || !strings.Contains(file.String(), `"Test":"TestBravo"}`+"\n") {
		t.Fatalf("the unterminated line did not arrive whole:\n%s", file.String())
	}
	log, err := readLog("unit", strings.NewReader(file.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !log.failedPackages[pkg] {
		t.Fatalf("the package a killed shard never finished reads as %+v, want it failed", log.Counts)
	}
}

// TestMergeCoverProfilesKeepsOneHeader: the merged profile carries the mode
// header once and every shard's blocks, the coverage reader takes it, and the
// shards' own files are gone. A shard that left no profile, or one headed
// differently, is an error naming the shard rather than a smaller merge.
func TestMergeCoverProfilesKeepsOneHeader(t *testing.T) {
	newRun := func(profile string, shardCount int) *shardedRun {
		run := &shardedRun{coverProfile: profile}
		for number := 1; number <= shardCount; number++ {
			run.shards = append(run.shards, &testShard{number: number, shardCount: shardCount})
		}
		return run
	}
	blocks := []string{
		"example.test/mod/mod.go:3.24,3.34 1 1\n",
		"example.test/mod/mod.go:5.26,5.36 1 0\n",
		"example.test/mod/mod.go:3.24,3.34 1 0\n",
	}

	t.Run("three shards", func(t *testing.T) {
		profile := filepath.Join(t.TempDir(), "unit.out")
		for i, block := range blocks {
			write(t, formatShardCoverProfile(profile, i+1), "mode: atomic\n"+block)
		}
		if err := newRun(profile, 3).mergeCoverProfiles(); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(profile)
		if err != nil {
			t.Fatal(err)
		}
		if want := "mode: atomic\n" + strings.Join(blocks, ""); string(body) != want {
			t.Fatalf("merged profile =\n%s\nwant\n%s", body, want)
		}
		merged := newCoverage()
		if err := merged.addFile(profile); err != nil {
			t.Fatalf("the coverage reader refuses the merge: %v", err)
		}
		if stats := merged.stats(); stats.Statements != 2 || stats.Covered != 1 {
			t.Fatalf("merged stats = %+v, want 2 statements with 1 covered", stats)
		}
		leftovers, err := filepath.Glob(profile + ".shard-*")
		if err != nil || len(leftovers) != 0 {
			t.Fatalf("the shards' profiles were left behind: %q, %v", leftovers, err)
		}
	})
	t.Run("a shard with no profile", func(t *testing.T) {
		profile := filepath.Join(t.TempDir(), "unit.out")
		write(t, formatShardCoverProfile(profile, 1), "mode: atomic\n"+blocks[0])
		err := newRun(profile, 2).mergeCoverProfiles()
		if err == nil || !strings.Contains(err.Error(), "shard 2 of 2") {
			t.Fatalf("merge = %v, want an error naming shard 2 of 2", err)
		}
	})
	t.Run("a shard with another mode", func(t *testing.T) {
		profile := filepath.Join(t.TempDir(), "unit.out")
		write(t, formatShardCoverProfile(profile, 1), "mode: atomic\n"+blocks[0])
		write(t, formatShardCoverProfile(profile, 2), "mode: set\n"+blocks[1])
		err := newRun(profile, 2).mergeCoverProfiles()
		if err == nil || !strings.Contains(err.Error(), "shard 2 of 2") {
			t.Fatalf("merge = %v, want an error naming shard 2 of 2", err)
		}
	})
}

// TestGoTestShardsRefuseWhatTheyCannotSplit: a flag that chooses the tests or
// names an output file, a relative coverage profile and a malformed shard
// count are usage errors before anything happens, so the earlier pass's stamp
// is still there. With one shard the same command lines are go test's own
// business, exactly as without the flag.
func TestGoTestShardsRefuseWhatTheyCannotSplit(t *testing.T) {
	root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	stamp := filepath.Join(root, "coverage", "unit.commit")
	write(t, stamp, "old-commit\n")
	for name, args := range map[string][]string{
		"an output folder":    {"--shards", "2", "--", "./...", "-outputdir", "/tmp"},
		"a cpu profile":       {"--shards", "2", "--", "./...", "-cpuprofile=/tmp/cpu.out"},
		"a prefixed profile":  {"--shards", "2", "--", "./...", "--test.memprofile", "/tmp/mem.out"},
		"a trace":             {"--shards", "2", "--", "./...", "-trace=/tmp/trace.out"},
		"a test binary":       {"--shards", "2", "--", "./...", "-o", "/tmp/mod.test"},
		"a selection":         {"--shards", "2", "--", "./...", "-run", "TestOK"},
		"a listing":           {"--shards", "2", "--", "./...", "-list=."},
		"a stream of its own": {"--shards", "2", "--", "./...", "-json"},
		"a relative profile":  {"--shards", "2", "--", "./...", "-coverprofile=unit.out"},
		"no shard count":      {"--shards", "--", "./..."},
		"a zero shard count":  {"--shards", "0", "--", "./..."},
		"a word":              {"--shards", "six", "--", "./..."},
	} {
		t.Run(name, func(t *testing.T) {
			code, err := goTest(append([]string{"unit"}, args...), io.Discard)
			if code != 2 || err == nil {
				t.Fatalf("goTest(%q) = %d, %v; want 2 and the refusal", args, code, err)
			}
			if _, statErr := os.Stat(stamp); statErr != nil {
				t.Fatalf("a refused command line touched the stamp: %v", statErr)
			}
		})
	}
	if _, err := parseTestInvocation([]string{"unit", "--shards", "1", "--", "./...", "-run", "TestOK", "-json"}); err != nil {
		t.Fatalf("one shard refused a command line a single process takes: %v", err)
	}
}

// durations are the parts of a run's output that differ between two runs of
// the same tests.
var durations = regexp.MustCompile(`[0-9]+(\.[0-9]+)?s\b`)

// readComparableLog reads a -json log with the times taken out: what two runs
// of the same tests agree on.
func readComparableLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s: %q: %v", path, line, err)
		}
		delete(ev, "Time")
		delete(ev, "Elapsed")
		if output, isText := ev["Output"].(string); isText {
			ev["Output"] = durations.ReplaceAllString(output, "Ns")
		}
		events = append(events, ev)
	}
	return events
}

// TestGoTestOneShardIsTheSingleProcess: `--shards 1` is the command without
// the flag. It prints the same line, runs one go test and keeps the same log.
func TestGoTestOneShardIsTheSingleProcess(t *testing.T) {
	root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\nfunc TestAlso(t *testing.T) {}\n")
	logPath := filepath.Join(root, "coverage", "testlog", "unit.json")
	var runs [2]struct {
		output string
		log    []map[string]any
	}
	for i, args := range [][]string{{"unit", "--", "./...", "-count=1"}, {"unit", "--shards", "1", "--", "./...", "-count=1"}} {
		var output bytes.Buffer
		code, err := goTest(args, &output)
		if code != 0 || err != nil {
			t.Fatalf("goTest(%q) = %d, %v", args, code, err)
		}
		runs[i].output = durations.ReplaceAllString(output.String(), "Ns")
		runs[i].log = readComparableLog(t, logPath)
	}
	if runs[0].output != runs[1].output {
		t.Fatalf("one shard printed\n%s\nwithout the flag\n%s", runs[1].output, runs[0].output)
	}
	if !reflect.DeepEqual(runs[0].log, runs[1].log) {
		t.Fatalf("one shard logged\n%v\nwithout the flag\n%v", runs[1].log, runs[0].log)
	}
}

// TestGoTestShardedRunReadsAsOneProcess: the same suite run whole and run in
// three shards leaves a log the reader counts identically, down to the
// packages and the subtests, and a coverage profile with one header that
// measures the same statements. The shards' profiles are merged away.
func TestGoTestShardedRunReadsAsOneProcess(t *testing.T) {
	root := writeShardedWorkspace(t, twoPackageSuite, twoPackageFiles)
	t.Setenv("TESTREPORT_COMMIT", "new-commit")
	logPath := filepath.Join(root, "coverage", "testlog", "unit.json")
	profile := filepath.Join(root, "coverage", "unit.out")
	args := []string{"--", "./...", "-count=1", "-covermode=atomic", "-coverprofile=" + profile}

	type measurement struct {
		counts Counts
		cover  Stats
	}
	measure := func(args []string) (measurement, string) {
		t.Helper()
		var output bytes.Buffer
		code, err := goTest(append([]string{"unit"}, args...), &output)
		if code != 0 || err != nil {
			t.Fatalf("goTest(%q) = %d, %v\n%s", args, code, err, output.String())
		}
		log, err := readLogFile("unit", logPath)
		if err != nil {
			t.Fatal(err)
		}
		counts := log.Counts
		counts.Elapsed = 0
		covered := newCoverage()
		if err := covered.addFile(profile); err != nil {
			t.Fatal(err)
		}
		return measurement{counts: counts, cover: covered.stats()}, output.String()
	}

	whole, _ := measure(args)
	sharded, output := measure(append([]string{"--shards", "3"}, args...))
	if !strings.Contains(output, "8 listed tests over 3 shards") {
		t.Fatalf("the sharded run did not say how it split the listing:\n%s", output)
	}
	if whole != sharded {
		t.Fatalf("sharded run measured %+v, the whole run %+v", sharded, whole)
	}
	if whole.counts.Packages != 2 || whole.counts.Tests != 8 || whole.counts.Fuzz != 1 || whole.counts.Subtests != 2 {
		t.Fatalf("the fixture measured %+v; want 2 packages, 8 tests (TestAlpha twice, one example), 1 fuzz target, 2 subtests", whole.counts)
	}
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if headers := strings.Count(string(body), "mode:"); headers != 1 {
		t.Fatalf("the merged profile carries %d mode headers, want 1:\n%s", headers, body)
	}
	if leftovers, _ := filepath.Glob(profile + ".shard-*"); len(leftovers) != 0 {
		t.Fatalf("the shards' profiles were left behind: %q", leftovers)
	}
	if stamp, err := os.ReadFile(filepath.Join(root, "coverage", "unit.commit")); err != nil || string(stamp) != "new-commit\n" {
		t.Fatalf("a passing sharded run stamped %q, %v", stamp, err)
	}
}

// TestGoTestShardedFailurePropagates: one failing test in one shard is the
// run's failure. The exit code is go test's own, the summary counts it and
// prints its output, and no stamp certifies the profile.
func TestGoTestShardedFailurePropagates(t *testing.T) {
	root := writeShardedWorkspace(t, twoPackageSuite+"\nfunc TestJuliet(t *testing.T) { t.Fatal(\"juliet broke\") }\n", twoPackageFiles)
	t.Setenv("TESTREPORT_COMMIT", "new-commit")
	write(t, filepath.Join(root, "coverage", "unit.commit"), "old-commit\n")

	var output bytes.Buffer
	code, err := goTest([]string{"unit", "--shards", "4", "--", "./...", "-count=1"}, &output)
	if err != nil {
		t.Fatalf("goTest error = %v; a test failure is a code, not an error", err)
	}
	if code == 0 {
		t.Fatalf("goTest = 0; want the failing shard's code\n%s", output.String())
	}
	if !strings.Contains(output.String(), "juliet broke") || !strings.Contains(output.String(), "1 FAILED") {
		t.Fatalf("the summary lost the failure:\n%s", output.String())
	}
	log, err := readLogFile("unit", filepath.Join(root, "coverage", "testlog", "unit.json"))
	if err != nil {
		t.Fatal(err)
	}
	if log.Failed != 1 || log.Passed != 9 || log.Packages != 2 {
		t.Fatalf("log counts %+v; want 1 failed and 9 passed in 2 packages", log.Counts)
	}
	if _, err := os.Stat(filepath.Join(root, "coverage", "unit.commit")); !os.IsNotExist(err) {
		t.Fatalf("a failed sharded run kept a coverage stamp: %v", err)
	}
}

// TestGoTestShardedRunNamesAKilledShard: a shard whose go test dies of a
// signal did not fail a test, it stopped reporting. The command fails with the
// shard named, and the package it left unfinished reads as failed.
func TestGoTestShardedRunNamesAKilledShard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture signals its go test parent")
	}
	root := writeShardedWorkspace(t, `package mod

import (
	"os"
	"syscall"
	"testing"
)

func TestAlpha(t *testing.T) {}
func TestBravo(t *testing.T) {}
func TestCharlie(t *testing.T) {}

// The parent of a test binary is the go test that built and ran it.
func TestKillsItsShard(t *testing.T) {
	if err := syscall.Kill(os.Getppid(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}
`, nil)

	var output bytes.Buffer
	code, err := goTest([]string{"unit", "--shards", "2", "--", "./...", "-count=1"}, &output)
	if code != 1 || err == nil || !regexp.MustCompile(`shard [12] of 2 was killed`).MatchString(err.Error()) {
		t.Fatalf("goTest = %d, %v; want 1 and the killed shard named\n%s", code, err, output.String())
	}
	log, err := readLogFile("unit", filepath.Join(root, "coverage", "testlog", "unit.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !log.failedPackages["example.test/mod"] {
		t.Fatalf("the package the killed shard left unfinished reads as %+v, want it failed", log.Counts)
	}
}

// TestGoTestShardedRunFailsAListingThatFails: tests that cannot be listed
// cannot be dealt, and running none of them in every shard would pass. The
// failed listing is the run's failure, with go test's own output in it.
func TestGoTestShardedRunFailsAListingThatFails(t *testing.T) {
	workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) { undefined() }\n")
	code, err := goTest([]string{"unit", "--shards", "2", "--", "./..."}, io.Discard)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "listing the tests to shard") ||
		!strings.Contains(err.Error(), "undefined") {
		t.Fatalf("goTest = %d, %v; want 1 and the listing's own failure", code, err)
	}
}

// TestGoTestShardedRunOfOneTestIsOneProcess: a listing with fewer tests than
// two has nothing to share, and the command runs it as one process would.
func TestGoTestShardedRunOfOneTestIsOneProcess(t *testing.T) {
	root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	var output bytes.Buffer
	code, err := goTest([]string{"unit", "--shards", "6", "--", "./...", "-count=1"}, &output)
	if code != 0 || err != nil {
		t.Fatalf("goTest = %d, %v", code, err)
	}
	if strings.Contains(output.String(), "shards") {
		t.Fatalf("one test was sharded:\n%s", output.String())
	}
	log, err := readLogFile("unit", filepath.Join(root, "coverage", "testlog", "unit.json"))
	if err != nil || log.Tests != 1 || log.Passed != 1 {
		t.Fatalf("the single process logged %+v, %v", log, err)
	}
}

// TestShardedRunStopsWhatStartedWhenAShardCannotStart: a shard that cannot
// start fails the run with its name, and the shards already running are
// stopped rather than left to finish a run nobody is waiting for.
func TestShardedRunStopsWhatStartedWhenAShardCannotStart(t *testing.T) {
	sleeper, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on this machine")
	}
	sharedLog := newShardedLog(io.Discard)
	commands := []*exec.Cmd{
		exec.Command(sleeper, "60"),
		exec.Command(filepath.Join(t.TempDir(), "no-go-here")),
		exec.Command(sleeper, "60"),
	}
	run := &shardedRun{}
	for i, cmd := range commands {
		stream := newShardStream(sharedLog)
		cmd.Stdout = stream
		run.shards = append(run.shards, &testShard{number: i + 1, shardCount: len(commands), stream: stream, cmd: cmd})
	}
	err = run.start()
	if err == nil || !strings.Contains(err.Error(), "shard 2 of 3 could not start") {
		t.Fatalf("start = %v; want the shard that could not start named", err)
	}
	if state := commands[0].ProcessState; state == nil || state.Success() {
		t.Fatalf("the shard already running was not stopped: %v", state)
	}
	if commands[2].Process != nil {
		t.Fatal("a shard after the one that could not start was started anyway")
	}
}
