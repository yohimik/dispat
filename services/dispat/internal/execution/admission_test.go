// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Admitting a set another node reported, against a real repository: what it
// costs in git processes and what the run keeps of it afterwards.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// admissionFixture is a repository holding one captured output set, the
// signed-result encoding of its manifest, and a coordinator that installs
// admitted sets into a checkout of its own.
type admissionFixture struct {
	coordinator *Coordinator
	document    []byte
	git         *gitx.LocalGitx
	destination string
}

// newAdmissionFixture captures a set of the given number of files under
// dist/, spread over folders the way a bundler's output is.
func newAdmissionFixture(tb testing.TB, files int) *admissionFixture {
	tb.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		tb.Skip("git not available")
	}
	dir := tb.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "unit@dispat.test"},
		{"config", "user.name", "dispat unit"}} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(tb, err, "git %v: %s", args, out)
	}
	for i := range files {
		path := filepath.Join(dir, "dist", fmt.Sprintf("chunk-%02d", i%32), fmt.Sprintf("file-%05d.js", i))
		require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(tb, os.WriteFile(path, []byte(fmt.Sprintf("export const value = %d;\n", i)), 0o644))
	}
	git := &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}
	limits := TransferLimits{MaxFiles: files + 1, MaxBytes: 1 << 30, MaxManifestBytes: 1 << 26}
	manifest, err := CaptureOutputs(context.Background(), CaptureRequest{
		Git: git, Dir: dir, PackagePath: ".", Roots: []string{"dist"}, Limits: limits,
		Manifest: OutputManifest{Run: "run-1", PlanDigest: "digest", Task: "core:build", Attempt: 1,
			Generation: "generation", Node: "build-a", Package: "core",
			Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"}},
	})
	require.NoError(tb, err)
	document, err := json.Marshal(manifest)
	require.NoError(tb, err)
	coordinator := &Coordinator{Run: "run-1", PlanDigest: "digest", Generation: "generation",
		Limits: limits, outputs: newOutputRegistry(), dispatch: Dispatch{
			Sources:        func(string) []Source { return []Source{{Name: "app", Path: ".", Dir: dir}} },
			OpenRepository: func(string) *gitx.LocalGitx { return git },
		}}
	return &admissionFixture{coordinator: coordinator, document: document, git: git, destination: tb.TempDir()}
}

// admit decodes the set as a result reply carries it and admits it, as
// another node's build of the named package, into the fixture's checkout.
func (f *admissionFixture) admit(tb testing.TB, packageName string) {
	tb.Helper()
	manifest := &OutputManifest{}
	require.NoError(tb, json.Unmarshal(f.document, manifest))
	request := release.StageRequest{Dir: f.destination, Release: &plan.Release{Pkg: &model.Package{
		Name: packageName, Repository: "app", Space: &model.Space{BuildOutputs: []string{"dist"}},
	}}}
	require.NoError(tb, f.coordinator.admitOutputs(context.Background(), "core:build", producedOutputs{
		node: "build-b", store: f.git, manifest: manifest, attempt: 1,
	}, request))
}

// TestAdmittingAnotherNodesSetListsItsTreeOnce: validation already walks the
// tree an admitted set is read from, so installing it reads the objects that
// walk found instead of listing the same immutable tree a second time. Three
// processes remain: the validating listing, the lookup of the checkout's
// private folder, and the one object reader the bytes stream through.
func TestAdmittingAnotherNodesSetListsItsTreeOnce(t *testing.T) {
	fixture := newAdmissionFixture(t, 8)
	before := gitx.GitInvocations()

	fixture.admit(t, "core")

	assert.Equal(t, uint64(3), gitx.GitInvocations()-before)
	installed, err := os.ReadFile(filepath.Join(fixture.destination, "dist", "chunk-07", "file-00007.js"))
	require.NoError(t, err)
	assert.Equal(t, "export const value = 7;\n", string(installed))
}

// BenchmarkAdmitOutputsFromAnotherNode admits one 1000-file set per
// iteration, each under its own package name, the way a run admits the
// builds of many packages. git-invocations/op counts the processes one
// admission starts; retained-B/op is what the run's record still holds per
// admitted set once the admission has returned.
func BenchmarkAdmitOutputsFromAnotherNode(b *testing.B) {
	fixture := newAdmissionFixture(b, 1000)
	var memory runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memory)
	heapBefore := memory.HeapAlloc
	gitBefore := gitx.GitInvocations()
	admitted := 0
	for b.Loop() {
		fixture.admit(b, fmt.Sprintf("package-%d", admitted))
		admitted++
	}
	b.StopTimer()
	invocations := gitx.GitInvocations() - gitBefore
	runtime.GC()
	runtime.ReadMemStats(&memory)
	runtime.KeepAlive(fixture.coordinator)
	b.ReportMetric(float64(invocations)/float64(admitted), "git-invocations/op")
	b.ReportMetric(float64(int64(memory.HeapAlloc)-int64(heapBefore))/float64(admitted), "retained-B/op")
}
