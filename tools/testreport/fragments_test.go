package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fragmentTestProfile = "mode: atomic\ngithub.com/yohimik/dispat/pkg/ccme/x.go:1.1,2.2 1 1\n"
	fragmentTestLog     = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme"}
`
	fragmentFailedLog = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"fail","Package":"github.com/yohimik/dispat/pkg/ccme"}
`
	fragmentIncompletePackageLog = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
`
	fragmentIncompleteTestLog = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme"}
`
	fragmentMalformedLog = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"`
	fragmentShardedLog = `{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"start","Package":"github.com/yohimik/dispat/pkg/ccme"}
{"Action":"run","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme","Test":"TestParse"}
{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme"}
`
)

func fragmentTestOptions(t *testing.T, lane string) fragmentOptions {
	t.Helper()
	return fragmentOptions{
		Mode: fragmentCollectMode, Lane: lane, Commit: gateCommit,
		Coverage: filepath.Join(t.TempDir(), "coverage"),
		Out:      filepath.Join(t.TempDir(), "fragment"),
	}
}

func seedFragmentSuite(t *testing.T, options fragmentOptions, suite string) {
	t.Helper()
	write(t, filepath.Join(options.Coverage, suite+fragmentProfileSuffix), fragmentTestProfile)
	write(t, filepath.Join(options.Coverage, suite+fragmentStampSuffix), gateCommit+"\n")
	write(t, filepath.Join(options.Coverage, fragmentTestLogName, suite+fragmentLogSuffix),
		fragmentSuiteLog(suite))
}

func fragmentSuiteLog(suite string) string {
	return strings.ReplaceAll(fragmentTestLog, fragmentExpectedPackage("ccme.json"),
		fragmentExpectedPackage(suite+fragmentLogSuffix))
}

func TestFragmentsCommandCollectsEmptyChecksLane(t *testing.T) {
	output := filepath.Join(t.TempDir(), "checks")
	code := run([]string{fragmentVerb, "-mode=collect", "-lane=checks", "-commit=" + gateCommit,
		"-coverage=" + filepath.Join(t.TempDir(), "absent"), "-out=" + output, "-full=true"},
		&strings.Builder{}, &strings.Builder{})
	if code != 0 {
		t.Fatalf("checks lane collection exited %d", code)
	}
	manifest, err := os.ReadFile(filepath.Join(output, fragmentManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"lane": "checks"`) ||
		!strings.Contains(string(manifest), `"full": true`) {
		t.Fatalf("checks manifest has wrong lane or full flag: %s", manifest)
	}
}

func TestFragmentsCollectAndMergeCompleteEvidence(t *testing.T) {
	input := filepath.Join(t.TempDir(), "fragments")
	if err := os.Mkdir(input, fragmentDirectoryMode); err != nil {
		t.Fatal(err)
	}
	for _, lane := range fragmentLanes {
		options := fragmentTestOptions(t, lane)
		options.Full = true
		options.Out = filepath.Join(input, fragmentArtifactPrefix+lane)
		switch lane {
		case fragmentUnitLane:
			for _, suite := range fragmentUnitSuites {
				seedFragmentSuite(t, options, suite)
			}
		case fragmentIntegrationLane:
			seedFragmentSuite(t, options, fragmentIntegrationLane)
		case fragmentRaceLane:
			for _, suite := range fragmentRaceSuites {
				write(t, filepath.Join(options.Coverage, fragmentTestLogName,
					suite+raceSuffix+fragmentLogSuffix), fragmentSuiteLog(suite))
			}
		}
		if err := collectFragment(options); err != nil {
			t.Fatalf("collecting %s: %v", lane, err)
		}
		manifestData, err := os.ReadFile(filepath.Join(options.Out, fragmentManifestName))
		if err != nil {
			t.Fatal(err)
		}
		var manifest fragmentManifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Source != gateCommit || manifest.Lane != lane || !manifest.Full ||
			len(manifest.Files) != len(fragmentInventory(lane)) {
			t.Fatalf("%s manifest does not bind full inventory: %+v", lane, manifest)
		}
		if lane == fragmentChecksLane {
			if err := os.Remove(filepath.Join(options.Out, fragmentCoverageName)); err != nil {
				t.Fatal(err)
			}
		}
	}
	out := filepath.Join(t.TempDir(), "merged")
	if err := mergeFragments(fragmentOptions{Mode: fragmentMergeMode, Commit: gateCommit,
		Input: input, Out: out, Full: true}); err != nil {
		t.Fatal(err)
	}
	files, err := fragmentWalk(out, false)
	if err != nil {
		t.Fatal(err)
	}
	want := len(fragmentInventory(fragmentUnitLane)) + len(fragmentInventory(fragmentIntegrationLane)) +
		len(fragmentInventory(fragmentRaceLane))
	if len(files) != want {
		t.Fatalf("merged %d files, want %d", len(files), want)
	}
	if _, err := os.Stat(filepath.Join(out, fragmentManifestName)); !os.IsNotExist(err) {
		t.Fatalf("merged coverage must contain no manifest: %v", err)
	}
}

func TestFragmentsAcceptAggregateTerminalForShardedStarts(t *testing.T) {
	if err := validateFragmentLog("testlog/ccme.json", []byte(fragmentShardedLog)); err != nil {
		t.Fatalf("one aggregate package terminal must close both sharded starts: %v", err)
	}
	unfinished := strings.TrimSuffix(fragmentShardedLog,
		`{"Action":"pass","Package":"github.com/yohimik/dispat/pkg/ccme"}`+"\n")
	if err := validateFragmentLog("testlog/ccme.json", []byte(unfinished)); err == nil {
		t.Fatal("sharded log without final package terminal was accepted")
	}
}

func TestFragmentsRejectPartialStaleAndUnsafeEvidence(t *testing.T) {
	for _, example := range []struct {
		name   string
		change func(*testing.T, fragmentOptions)
	}{
		{"missing profile", func(t *testing.T, options fragmentOptions) {
			if err := os.Remove(filepath.Join(options.Coverage, "ccme.out")); err != nil {
				t.Fatal(err)
			}
		}},
		{"stale stamp", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, "ccme.commit"), "older\n")
		}},
		{"failed log", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, fragmentTestLogName, "ccme.json"), fragmentFailedLog)
		}},
		{"unfinished test", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, fragmentTestLogName, "ccme.json"), fragmentIncompleteTestLog)
		}},
		{"unfinished package", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, fragmentTestLogName, "ccme.json"), fragmentIncompletePackageLog)
		}},
		{"malformed log", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, fragmentTestLogName, "ccme.json"), fragmentMalformedLog)
		}},
		{"malformed profile", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, "ccme.out"), "mode: not-go-coverage\n")
		}},
		{"unrelated package", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, fragmentTestLogName, "ccme.json"),
				strings.ReplaceAll(fragmentTestLog, fragmentExpectedPackage("ccme.json"),
					fragmentExpectedPackage("models.json")))
		}},
		{"unexpected file", func(t *testing.T, options fragmentOptions) {
			write(t, filepath.Join(options.Coverage, "stale.out"), fragmentTestProfile)
		}},
		{"symlink", func(t *testing.T, options fragmentOptions) {
			if err := os.Symlink(filepath.Join(options.Coverage, "ccme.out"),
				filepath.Join(options.Coverage, "linked.out")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(example.name, func(t *testing.T) {
			options := fragmentTestOptions(t, fragmentUnitLane)
			seedFragmentSuite(t, options, "ccme")
			example.change(t, options)
			if err := collectFragment(options); err == nil {
				t.Fatal("invalid fragment was accepted")
			}
			if _, err := os.Stat(options.Out); !os.IsNotExist(err) {
				t.Fatalf("failed validation wrote output: %v", err)
			}
		})
	}
}

func TestFragmentsMergeRequiresFourBoundLanes(t *testing.T) {
	input := filepath.Join(t.TempDir(), "fragments")
	if err := os.Mkdir(input, fragmentDirectoryMode); err != nil {
		t.Fatal(err)
	}
	for _, lane := range fragmentLanes {
		options := fragmentTestOptions(t, lane)
		options.Out = filepath.Join(input, fragmentArtifactPrefix+lane)
		if err := collectFragment(options); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(options.Out, fragmentCoverageName)); err != nil {
			t.Fatal(err)
		}
	}
	merge := func() error {
		return mergeFragments(fragmentOptions{Commit: gateCommit, Input: input,
			Out: filepath.Join(t.TempDir(), "merged")})
	}
	if err := merge(); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(input, fragmentArtifactPrefix+fragmentUnitLane, fragmentManifestName)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		name string
		old  string
		new  string
	}{
		{"lane", `"lane": "unit"`, `"lane": "race"`},
		{"source", gateCommit, strings.Repeat("f", fragmentCommitHexLength)},
	} {
		write(t, manifestPath, strings.Replace(string(manifest), mutation.old, mutation.new, 1))
		if err := merge(); err == nil {
			t.Fatalf("wrong manifest %s was accepted", mutation.name)
		}
	}
	write(t, manifestPath, string(manifest))
	if err := os.Mkdir(filepath.Join(input, "unrecognized"), fragmentDirectoryMode); err != nil {
		t.Fatal(err)
	}
	if err := merge(); err == nil {
		t.Fatal("unexpected fifth child was accepted")
	}
}

func TestFragmentsPartialSuitesAndMergeTamper(t *testing.T) {
	input := filepath.Join(t.TempDir(), "fragments")
	if err := os.Mkdir(input, fragmentDirectoryMode); err != nil {
		t.Fatal(err)
	}
	for _, lane := range fragmentLanes {
		options := fragmentTestOptions(t, lane)
		options.Out = filepath.Join(input, fragmentArtifactPrefix+lane)
		if lane == fragmentUnitLane {
			seedFragmentSuite(t, options, "ccme")
		}
		if err := collectFragment(options); err != nil {
			t.Fatal(err)
		}
		if lane != fragmentUnitLane {
			if err := os.Remove(filepath.Join(options.Out, fragmentCoverageName)); err != nil {
				t.Fatal(err)
			}
		}
	}
	valid := fragmentOptions{Mode: fragmentMergeMode, Commit: gateCommit,
		Input: input, Out: filepath.Join(t.TempDir(), "merged")}
	if err := mergeFragments(valid); err != nil {
		t.Fatalf("complete subset and empty lanes should merge: %v", err)
	}
	if err := mergeFragments(valid); err == nil {
		t.Fatal("merge overwrote an existing output directory")
	}
	invalid := valid
	invalid.Out = filepath.Join(t.TempDir(), "full")
	invalid.Full = true
	if err := mergeFragments(invalid); err == nil {
		t.Fatal("partial fragments passed full merge")
	}
	tampered := filepath.Join(input, fragmentArtifactPrefix+fragmentUnitLane,
		fragmentCoverageName, "ccme.out")
	write(t, tampered, strings.Replace(fragmentTestProfile, " 1 1", " 1 0", 1))
	invalid.Out = filepath.Join(t.TempDir(), "tampered")
	invalid.Full = false
	if err := mergeFragments(invalid); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered profile was accepted: %v", err)
	}
	if _, err := os.Stat(invalid.Out); !os.IsNotExist(err) {
		t.Fatalf("tampered merge wrote output: %v", err)
	}
	write(t, tampered, fragmentTestProfile)
	if err := os.RemoveAll(filepath.Join(input, fragmentArtifactPrefix+fragmentUnitLane,
		fragmentCoverageName)); err != nil {
		t.Fatal(err)
	}
	invalid.Out = filepath.Join(t.TempDir(), "missing-coverage")
	if err := mergeFragments(invalid); err == nil {
		t.Fatal("nonempty manifest without coverage directory was accepted")
	}
}
