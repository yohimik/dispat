package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

func TestPlanningRefBracketDetectsSameTargetTagReplacement(t *testing.T) {
	root, a := guardRepo(t, &config.File{})
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, strings.TrimSpace(string(out)), err)
		}
	}
	git("tag", "core@1.0.0")
	baseline, err := a.git.AllTagSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	captured := []planningRefCapture{{git: a.git, expected: baseline}}
	if err := a.finishPlanningRefs(context.Background(), captured); err != nil {
		t.Fatal(err)
	}
	if len(a.pinnedNativeRefs) != 1 || a.pinnedNativeRefs[0].Object != baseline["core@1.0.0"].Object {
		t.Fatalf("wrong pinned planning ref: %+v", a.pinnedNativeRefs)
	}
	git("tag", "-fa", "core@1.0.0", "-m", "same target, different tag object")
	if err := a.finishPlanningRefs(context.Background(), captured); err == nil {
		t.Fatal("planning accepted a tag object replacement at the same commit")
	}
}

func TestPlanningSnapshotBracketPreservesTrackedRootsButExcludesStaleProducts(t *testing.T) {
	root, a := guardRepo(t, &config.File{})
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "seed"), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, strings.TrimSpace(string(out)), err)
		}
	}
	git("add", ".gitignore")
	git("add", "-f", "dist/seed")
	git("commit", "-qm", "track source seed")
	if err := os.WriteFile(filepath.Join(root, "dist", "stale"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, err := a.git.HeadSHA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source := execution.Source{Path: ".", Dir: root, Head: head}
	base := execution.PinnedSourceCaptureOptions{Sources: []execution.Source{source},
		OpenRepository: a.openRepository, Store: a.git, Log: a.log}
	before, err := execution.CapturePinnedRunSources(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	base.OutputRoots = map[string][]string{root: {"dist"}}
	after, err := execution.CapturePinnedRunSources(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if before.Trees[""] == "" || before.Trees[""] != after.Trees[""] {
		t.Fatalf("identical source bytes produced different captured trees: before=%v after=%v",
			before.Trees, after.Trees)
	}
	// Transport commits include their creation time. A second capture may
	// therefore have another OID even when the exact source tree is unchanged.
	before.Repositories[0].Snapshot = strings.Repeat("a", len(after.Repositories[0].Snapshot))
	if !samePinnedPlanningSources(before, after) {
		t.Fatalf("root ownership changed an unchanged planning snapshot: before=%+v after=%+v",
			before.Repositories, after.Repositories)
	}
	after.Trees[""] = strings.Repeat("b", len(before.Trees[""]))
	if samePinnedPlanningSources(before, after) {
		t.Fatal("planning accepted a changed source tree")
	}
}
