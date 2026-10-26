package integration

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// A fixed-group rider consults the other owner's history even without a
// dependency edge. The source record is native work in this run, not drift.
func fixedGroupNativeOverlap(t *testing.T) (*harness.Repo, map[string]any) {
	t.Helper()
	aSource := harness.New(t)
	aSource.SeedPackage("packages", "a")
	aSource.Commit("feat(a): initial package A")
	aSource.Git("tag", "-a", "a@1.0.0", "-m", "initial release")
	aSource.WriteFile("packages/a/fix.txt", "one\n")
	aSource.Commit("fix(a): source-A-only patch")
	bSource := harness.New(t)
	bSource.SeedPackage("packages", "b")
	bSource.Commit("feat(b): initial package B")
	bSource.Git("tag", "-a", "b@1.0.0", "-m", "initial release")

	control := harness.New(t)
	addPolyrepoSource(t, control, "source-a", "sources/a", aSource)
	addPolyrepoSource(t, control, "source-b", "sources/b", bSource)
	commitA := control.DispatCommand("commit", "--tag")
	waitForA := `attempts=0; while [ ! -f ../../../../a-nested-recorded ] && [ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; test -f ../../../../a-nested-recorded`
	cfg := polyrepoFile()
	cfg["concurrency"] = []int{2}
	cfg["versionGroups"] = map[string]any{"fleet": map[string]any{"versioning": "fixed"}}
	cfg["spaces"] = map[string]any{
		"first":  map[string]any{"path": []string{"sources/a/packages"}, "versionGroup": "fleet"},
		"second": map[string]any{"path": []string{"sources/b/packages"}, "versionGroup": "fleet"},
	}
	cfg["scripts"] = map[string]any{
		"build": []string{"echo building"},
		"wait-for-native-a": []string{`if [ "$DISPAT_PACKAGE" = b ]; then ` + waitForA + `; ` +
			`: > ../../../../b-before-publish; fi`},
		"publish": []string{`case "$DISPAT_PACKAGE" in ` +
			`a) printf 'a\n' > nested-release.txt; ` + commitA + `; ` +
			`: > ../../../../a-nested-recorded; ` +
			`attempts=0; while [ ! -f ../../../../b-published ] && [ "$attempts" -lt 300 ]; do ` +
			`attempts=$((attempts + 1)); sleep 0.01; done;; ` +
			`b) : > ../../../../b-published;; esac`},
	}
	cfg["flow"] = map[string]any{
		"build": []string{"build"}, "beforePublish": []string{"wait-for-native-a"},
		"publish": []string{"publish"},
	}
	return control, cfg
}

func TestPolyrepoFixedGroupNativeRecordOverlapsRiderPublish(t *testing.T) {
	control, cfg := fixedGroupNativeOverlap(t)
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure cross-source fixed ride overlap")
	status := control.StatusOK()
	assert.Equal(t, "fixed group versioning", harness.GraphLine(status.Events, "b").Str("reason"))

	res := control.Release()
	assert.FileExists(t, control.Path("a-nested-recorded"), "A's native record completed before B's hook")
	assert.FileExists(t, control.Path("b-before-publish"), "B's hook saw that completed native record")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("b-published"))
	assert.Contains(t, polyrepoTags(control, "sources/a"), "a@1.0.1")
	assert.Contains(t, polyrepoTags(control, "sources/b"), "b@1.0.1")
}

func TestPolyrepoFixedGroupWaitsForNestedTagAfterHeadMoves(t *testing.T) {
	control, cfg := fixedGroupNativeOverlap(t)
	scripts := cfg["scripts"].(map[string]any)
	scripts["wait-for-native-a"] = []string{`if [ "$DISPAT_PACKAGE" = b ]; then ` +
		`attempts=0; while [ ! -f ../../../../a-head-written ] && [ "$attempts" -lt 300 ]; do ` +
		`attempts=$((attempts + 1)); sleep 0.01; done; test -f ../../../../a-head-written; ` +
		`: > ../../../../b-before-publish; fi`}
	scripts["hold-after-commit"] = []string{`case "$PWD" in */sources/a) ` +
		`: > ../../a-head-written; attempts=0; while [ ! -f ../../allow-native-tag ] && ` +
		`[ "$attempts" -lt 300 ]; do attempts=$((attempts + 1)); sleep 0.01; done; ` +
		`test -f ../../allow-native-tag;; esac`}
	cfg["run"] = map[string]any{"afterCommit": []string{"hold-after-commit"}}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure native commit-to-tag barrier")

	proc := control.StartRelease()
	if !assert.Eventually(t, func() bool {
		_, err := os.Stat(control.Path("a-head-written"))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond, "A's native commit did not reach its afterCommit hook") {
		control.WriteFile("allow-native-tag", "continue\n")
		res := proc.Wait()
		t.Fatalf("release before native hook marker: code %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	require.Eventually(t, func() bool {
		_, err := os.Stat(control.Path("b-before-publish"))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond, "B did not reach its pre-publish guard")
	assert.NoFileExists(t, control.Path("b-published"), "B cannot publish against an unfinished native record")
	control.WriteFile("allow-native-tag", "continue\n")
	res := proc.Wait()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("b-published"))
}

func TestPolyrepoExcludedPublishOrderStillAdmitsCompletedProviderRecord(t *testing.T) {
	control, cfg := fixedGroupNativeOverlap(t)
	delete(cfg, "versionGroups")
	for _, value := range cfg["spaces"].(map[string]any) {
		delete(value.(map[string]any), "versionGroup")
	}
	cfg["dependencies"] = map[string]any{"b": []any{"a"}}
	cfg["publish"] = map[string]any{"orderKinds": []any{}, "blockingKinds": []any{}}
	cfg["repositoryBaselines"] = []any{map[string]any{
		"consumer": "b", "releaseTag": "b@1.0.0", "repository": "source-a", "revision": "a@1.0.0",
	}}
	control.WriteFile("sources/b/packages/b/own.txt", "own fix\n")
	commitPolyrepoSource(t, control, "sources/b", "fix(b): independent own cause")
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure unordered provider publication")

	status := control.StatusOK()
	assert.Equal(t, "direct", harness.GraphLine(status.Events, "b").Str("reason"))
	res := control.Release()
	assert.FileExists(t, control.Path("a-nested-recorded"))
	assert.FileExists(t, control.Path("b-before-publish"))
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.FileExists(t, control.Path("b-published"))
	assert.Contains(t, polyrepoTags(control, "sources/a"), "a@1.0.1")
	assert.Contains(t, polyrepoTags(control, "sources/b"), "b@1.0.1")
}

func TestPolyrepoPeerRejectsBareExportAndDirectGitTag(t *testing.T) {
	control, cfg := fixedGroupNativeOverlap(t)
	scripts := cfg["scripts"].(map[string]any)
	scripts["wait-for-native-a"] = []string{`if [ "$DISPAT_PACKAGE" = b ]; then ` +
		`attempts=0; while [ ! -f ../../../../a-direct-mutated ] && [ "$attempts" -lt 300 ]; do ` +
		`attempts=$((attempts + 1)); sleep 0.01; done; test -f ../../../../a-direct-mutated; fi`}
	scripts["publish"] = []string{`case "$DISPAT_PACKAGE" in a) ` +
		`printf 'direct\n' > direct.txt; git -C ../.. add packages/a/direct.txt; ` +
		`git -C ../.. commit -qm 'fix(a): direct hook mutation'; ` +
		`git -C ../.. tag -a a@1.0.1 -m 'direct hook tag'; ` +
		`printf 'PACKAGE_A=%s\n' "$(git -C ../.. rev-parse HEAD)" >> "$DISPAT_OUTPUT"; ` +
		`: > ../../../../a-direct-mutated; ` +
		`attempts=0; while [ ! -f ../../../../b-published ] && [ "$attempts" -lt 300 ]; do ` +
		`attempts=$((attempts + 1)); sleep 0.01; done;; ` +
		`b) : > ../../../../b-published;; esac`}
	writePolyrepoJSON(t, control, "dispat.json", cfg)
	control.Commit("chore: configure direct Git drift with a bare export")

	res := control.Release()
	assert.FileExists(t, control.Path("a-direct-mutated"))
	assert.NotZero(t, res.Code)
	assert.True(t, harness.IsCodePresent(res.Events, "E330"), "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.NoFileExists(t, control.Path("b-published"), "a peer cannot adopt a bare export from another owner")
	assert.NotContains(t, polyrepoTags(control, "sources/b"), "b@1.0.1")
}
