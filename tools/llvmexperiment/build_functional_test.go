package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The compiler and source commands are explicit test doubles. This validates the
// complete task/manifest path without executing or claiming an LLVM benchmark.
func TestBuildTaskRetainsCompletedOperationAndVerifiedManifest(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "test-tools")
	if err := os.Mkdir(tools, directoryMode); err != nil {
		t.Fatal(err)
	}
	git := "#!/bin/sh\ncase \"$*\" in *rev-parse*) printf '%s\\n' '" + llvmRevision + "';; esac\n"
	cmake := "#!/bin/sh\nset -eu\nif test \"$1\" != --build; then exit 0; fi\nmkdir -p build/bin\nfor name in llvm-cxxfilt llvm-size; do\ncat > build/bin/\"$name\" <<'TOOL'\n#!/bin/sh\nif test \"$1\" = --version; then echo 'test fixture LLVM 20.1.8'; else echo 'foo()'; fi\nTOOL\nchmod +x build/bin/\"$name\"\ndone\n"
	for name, content := range map[string]string{"git": git, "cmake": cmake} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), executableFileMode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(taskEvidenceEnvironment, filepath.Join(root, "evidence"))
	t.Setenv("DISPAT_EXECUTION_NODE", "functional-node")
	settings, _ := parseOptions([]string{"--profile", smokeProfile, "--source", filepath.Join(root, "fixture-source")})
	item := taskForBackend(settings, "functional-task-fixture", "X86")
	working := filepath.Join(root, "repo", "packages", item.Name)
	if err := os.MkdirAll(working, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(working, taskFileName), item); err != nil {
		t.Fatal(err)
	}
	t.Chdir(working)
	if err := buildTask(context.Background()); err != nil {
		t.Fatal(err)
	}
	returned := filepath.Join(root, "repo", artifactsDirectory, item.Name, manifestFileName)
	content, err := os.ReadFile(returned)
	if err != nil {
		t.Fatal(err)
	}
	var manifest taskManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Status != completedStatus || manifest.Interval.DurationSeconds == nil || manifest.Interval.ClockKind != monotonicClockKind || len(manifest.Artifacts) != 2 || manifest.Node != "functional-node" {
		t.Fatalf("task output lost measured evidence: %s", content)
	}
	terminal := filepath.Join(root, "evidence", manifest.AttemptID, taskFinishedFile)
	raw, err := os.ReadFile(terminal)
	if err != nil || string(raw) != string(content) {
		t.Fatal("returned manifest differs from retained physical attempt")
	}
	if err := buildTask(context.Background()); err == nil {
		t.Fatal("warm build directory was accepted")
	}
	entries, err := os.ReadDir(filepath.Join(root, "evidence"))
	if err != nil || len(entries) != 2 {
		t.Fatal("rejected warm attempt was not preserved")
	}
}

func TestDispatSourceIdentityDoesNotSubstituteLLVMRevision(t *testing.T) {
	source := strings.Repeat("a", 40)
	t.Setenv(dispatSourceEnvironment, "")
	if actual := dispatSourceRevision("dispat 1.11.0-rc.13-candidate."+source+" (linux_amd64)", ""); actual != source {
		t.Fatal("candidate source identity was lost")
	}
	if actual := dispatSourceRevision("dispat 1.11.0", "candidate:"+source); actual != source {
		t.Fatal("binary source identity was lost")
	}
	if actual := dispatSourceRevision("dispat 1.11.0", "release"); actual != "" {
		t.Fatal("unknown Dispat source was fabricated")
	}
	explicit := strings.Repeat("b", 40)
	t.Setenv(dispatSourceEnvironment, explicit)
	if actual := dispatSourceRevision("dispat 1.11.0", "candidate:"+source); actual != explicit {
		t.Fatal("explicit source provenance was discarded")
	}
}
