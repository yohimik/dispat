// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// TestAncestryIndexAgreesWithAWalk checks the marker pass against the
// definition, a walk per question, over random DAGs. Rank and topology are
// drawn independently, because a log lists by date and a skewed clock puts a
// parent before its child; the pass has to order the visit itself. The large
// round carries more markers than one pass does, and more than one word.
func TestAncestryIndexAgreesWithAWalk(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 60; round++ {
		n := 1 + rng.Intn(90)
		if round == 0 {
			n = ancestryBatch + 150
		}
		// topo[i] may only have parents among topo[i+1:], so the graph is a
		// DAG whatever the ranks say.
		topo := rng.Perm(n)
		parents := make([][]int32, n)
		for i, c := range topo {
			for k := rng.Intn(3); k > 0 && i+1 < n; k-- {
				parents[c] = append(parents[c], int32(topo[i+1+rng.Intn(n-i-1)]))
			}
		}
		index := newAncestryIndex(n, func(rank int) []int32 { return parents[rank] })
		require.NotNil(t, index)

		reaches := func(from int32) []bool {
			seen := make([]bool, n)
			for stack := []int32{from}; len(stack) > 0; {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if !seen[c] {
					seen[c] = true
					stack = append(stack, parents[c]...)
				}
			}
			return seen
		}
		var markers []int32
		for m := 0; m < n; m++ {
			if round == 0 || rng.Intn(3) == 0 {
				markers = append(markers, int32(m))
			}
		}
		// Registered in two batches, the second repeating part of the first:
		// the phases do exactly that.
		index.mark(markers[:len(markers)/2])
		index.mark(markers)
		for _, m := range markers {
			want, got := reaches(m), index.ancestors(m)
			require.NotNil(t, got)
			size := 0
			for c := 0; c < n; c++ {
				require.Equalf(t, want[c], got.has(c), "round %d: is %d an ancestor-or-self of %d", round, c, m)
				if want[c] {
					size++
				}
			}
			assert.Equal(t, size, got.len())
		}
		assert.Nil(t, index.ancestors(int32(n)), "a commit never marked has no set")
	}

	assert.Nil(t, newAncestryIndex(2, func(rank int) []int32 { return []int32{int32(1 - rank)} }),
		"two commits naming each other as parent are not a history")
}

// perBoundaryGit is the real Git implementation with the union read hidden,
// so the planner reads one window per boundary and asks git every ancestry
// question, as it did before the marker pass existed. Every other capability
// the planner probes for is forwarded, so the two runs differ in nothing else.
type perBoundaryGit struct{ inner *gitx.LocalGitx }

func (g perBoundaryGit) Tags(ctx context.Context, pkg string, f gitx.TagFormat) (gitx.Tags, error) {
	return g.inner.Tags(ctx, pkg, f)
}
func (g perBoundaryGit) TagsForPackages(ctx context.Context, f map[string]gitx.TagFormat) (map[string]gitx.Tags, error) {
	return g.inner.TagsForPackages(ctx, f)
}
func (g perBoundaryGit) Commits(ctx context.Context, since string) ([]gitx.Commit, error) {
	return g.inner.Commits(ctx, since)
}
func (g perBoundaryGit) CreateTag(ctx context.Context, name, message, target string) error {
	return g.inner.CreateTag(ctx, name, message, target)
}
func (g perBoundaryGit) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	return g.inner.IsAncestor(ctx, a, b)
}
func (g perBoundaryGit) IsShallow(ctx context.Context) (bool, error) { return g.inner.IsShallow(ctx) }
func (g perBoundaryGit) ResolveCommit(ctx context.Context, rev string) (string, error) {
	return g.inner.ResolveCommit(ctx, rev)
}

// randomRepository grows a history with side branches and merges, every
// commit a CCME message drawn from the directives whose meaning rests on
// ancestry or on window membership, and tags three packages into it: some
// stable, some on a train, some never released, on boundaries that nest and
// boundaries that do not.
func randomRepository(t *testing.T, seed int64) (*gitx.LocalGitx, []*model.Package, []model.Dependency) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	rng := rand.New(rand.NewSource(seed))
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")

	names := []string{"a", "b", "c"}
	var shas []string
	serial := 0
	commit := func() {
		serial++
		pkg := names[rng.Intn(len(names))]
		dir := filepath.Join(root, "pkgs", pkg)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", serial)), []byte("x"), 0o644))
		message := []string{
			"fix(" + pkg + "): change", "fix: derived from the files", "feat(a)^: reaches the consumers",
			"feat(" + pkg + ")%beta: onto the train", "fix(a,b,c): everywhere", "fix(" + pkg + ")!: breaking",
			"cancel(" + pkg + "): reset release state", "cancel(*): reset release state",
			"chore(" + pkg + "): restate\n\nDeletes: *", "release(" + pkg + ")%beta>stable: graduate",
			"release(" + pkg + "): hold\n\nRelease-As: none", "release(" + pkg + "): resume\n\nRelease-As: auto",
		}[rng.Intn(12)]
		if len(shas) > 0 {
			switch rng.Intn(8) {
			case 0:
				message = "fix(" + pkg + "): restated\n\nEdits: " + shas[rng.Intn(len(shas))]
			case 1:
				message = "revert(" + pkg + "): undo\n\nReverts: " + shas[rng.Intn(len(shas))]
			}
		}
		git("add", ".")
		git("commit", "-qm", message)
		shas = append(shas, git("rev-parse", "HEAD"))
	}
	commit()
	branches := []string{"main"}
	for step := 0; step < 26; step++ {
		switch r := rng.Intn(10); {
		case r < 6:
			git("checkout", "-q", branches[rng.Intn(len(branches))])
			commit()
		case r < 8:
			name := fmt.Sprintf("side%d", step)
			git("checkout", "-q", "-b", name, shas[rng.Intn(len(shas))])
			branches = append(branches, name)
			commit()
		default:
			git("checkout", "-q", "main")
			if other := branches[rng.Intn(len(branches))]; other != "main" {
				git("merge", "-q", "--no-ff", "-m", "chore: merge "+other, other)
			}
		}
	}
	git("checkout", "-q", "main")
	for _, b := range branches[1:] {
		if rng.Intn(3) > 0 {
			git("merge", "-q", "--no-ff", "-m", "chore: merge "+b, b)
		}
	}
	commit()

	// Tags on commits HEAD reaches, in history order so a prerelease sits
	// after the stable tag it trains from.
	reachable := strings.Fields(git("rev-list", "--reverse", "--topo-order", "HEAD"))
	for _, pkg := range names {
		if rng.Intn(5) == 0 {
			continue // never released: its window is the whole history
		}
		at := rng.Intn(len(reachable))
		git("tag", pkg+"@1.0.0", reachable[at])
		if rng.Intn(2) == 0 && at+1 < len(reachable) {
			git("tag", pkg+"@1.1.0-beta.0", reachable[at+1+rng.Intn(len(reachable)-at-1)])
		}
	}

	pkgs := make([]*model.Package, len(names))
	for i, name := range names {
		pkgs[i] = &model.Package{Name: name, Dir: filepath.Join(root, "pkgs", name)}
	}
	deps := []model.Dependency{{Consumer: "b", Provider: "a"}, {Consumer: "c", Provider: "a"}}
	return &gitx.LocalGitx{Dir: root}, pkgs, deps
}

// TestUnionReadPlansExactlyAsPerBoundaryReads is the byte-identity claim of
// CCME §17.2 for the single read and the marker pass: over histories with
// merges, the plan computed from one union walk and bit tests equals the plan
// computed from a walk per boundary and git's own answers, in every release,
// every diagnostic and their order. It also checks the index against
// gitx.IsAncestor directly, for every pair of commits the union holds.
func TestUnionReadPlansExactlyAsPerBoundaryReads(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real repositories")
	}
	ctx := context.Background()
	unionReads := 0
	for seed := int64(1); seed <= 10; seed++ {
		git, pkgs, deps := randomRepository(t, seed)
		opts := Options{Packages: pkgs, Dependencies: deps, Root: git.Dir}

		want, err := Compute(ctx, perBoundaryGit{inner: git}, opts)
		require.NoError(t, err)
		got, err := Compute(ctx, git, opts)
		require.NoError(t, err)

		assert.Equalf(t, want.Order, got.Order, "seed %d", seed)
		assert.Equalf(t, want.Diagnostics, got.Diagnostics, "seed %d", seed)
		require.Equalf(t, len(want.Releases), len(got.Releases), "seed %d", seed)
		for name, w := range want.Releases {
			assert.Equalf(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])),
				"seed %d: release of %s", seed, name)
		}

		// The index itself, against git, over every pair of the union.
		cp := &computation{ctx: ctx, git: git, pkgs: pkgs, log: zerolog.Nop(),
			rel: map[string]*Release{}, tags: map[string]gitx.Tags{}, window: map[string]*commitSet{},
			windowKey: map[string]string{}, byKey: map[string]*commitRec{}, parents: map[string][]string{}}
		require.NoError(t, cp.loadLegacyTagsAndWindows())
		if cp.anc == nil {
			continue
		}
		unionReads++
		for _, a := range cp.commits {
			for _, b := range cp.commits {
				yes, known := cp.markedAncestor(a.key, b.key)
				require.True(t, known)
				truth, err := git.IsAncestor(ctx, a.key, b.key)
				require.NoError(t, err)
				require.Equalf(t, truth, yes, "seed %d: is %s an ancestor-or-self of %s", seed, a.key, b.key)
			}
		}
		// And every window, against the listing git gives for its boundary.
		for _, p := range pkgs {
			since := ""
			if stable, ok := cp.tags[p.Name].StableBaseline(); ok {
				since = stable.Name
			}
			listed, err := git.Commits(ctx, since)
			require.NoError(t, err)
			assert.Equalf(t, len(listed), cp.windowSize(p.Name), "seed %d: window of %s", seed, p.Name)
			for _, c := range listed {
				assert.Truef(t, cp.inWindow(p.Name, c.SHA), "seed %d: %s is pending for %s", seed, c.SHA, p.Name)
			}
		}
	}
	assert.Positive(t, unionReads, "no seed exercised the index at all")
}

// TestComposedUnionReadPlansExactlyAsPerBoundaryReads is the same claim for a
// composed workspace, where each repository is read through its own
// boundaries: one repository history holding the three packages, planned once
// through the single read per repository and once through a read per boundary.
func TestComposedUnionReadPlansExactlyAsPerBoundaryReads(t *testing.T) {
	if testing.Short() {
		t.Skip("builds real repositories")
	}
	ctx := context.Background()
	for seed := int64(11); seed <= 16; seed++ {
		git, pkgs, deps := randomRepository(t, seed)
		for _, p := range pkgs {
			p.Repository, p.RepoRoot = "source", git.Dir
		}
		plan := func(history gitx.Gitx, stats *HistoryStats) *Plan {
			p, err := Compute(ctx, history, Options{Packages: pkgs, Dependencies: deps, Root: git.Dir,
				HistoryStats: stats,
				Repositories: map[string]RepositoryHistory{"source": {Name: "source", Root: git.Dir, Git: history}}})
			require.NoError(t, err)
			return p
		}
		perBoundary, single := &HistoryStats{}, &HistoryStats{}
		want, got := plan(perBoundaryGit{inner: git}, perBoundary), plan(git, single)

		assert.Equalf(t, want.Order, got.Order, "seed %d", seed)
		assert.Equalf(t, want.Diagnostics, got.Diagnostics, "seed %d", seed)
		for name, w := range want.Releases {
			assert.Equalf(t, canonical(reflect.ValueOf(w)), canonical(reflect.ValueOf(got.Releases[name])),
				"seed %d: release of %s", seed, name)
		}
		assert.Equalf(t, perBoundary.CommitWindows.Load(), single.CommitWindows.Load(), "seed %d: the same windows", seed)
		assert.Equalf(t, perBoundary.WindowCommitRefs.Load(), single.WindowCommitRefs.Load(), "seed %d: of the same sizes", seed)
		assert.Equalf(t, perBoundary.UniqueCommits.Load(), single.UniqueCommits.Load(), "seed %d: over the same union", seed)
		assert.LessOrEqualf(t, single.AncestryLookups.Load(), perBoundary.AncestryLookups.Load(),
			"seed %d: the index asks git no more than a read per boundary does", seed)
	}
}

// canonical renders a value with every pointer followed and every map sorted
// by its rendered keys. A Release keys several maps by *ccme.Unit, and two runs
// parse two sets of units, so the releases of two runs are never DeepEqual
// however identical; what they say is, and this is what they say.
func canonical(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Invalid:
		return "nil"
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return "nil"
		}
		return canonical(v.Elem())
	case reflect.Struct:
		var b strings.Builder
		b.WriteString(v.Type().Name() + "{")
		for i := 0; i < v.NumField(); i++ {
			b.WriteString(v.Type().Field(i).Name + ":" + canonical(v.Field(i)) + " ")
		}
		return b.String() + "}"
	case reflect.Map:
		entries := make([]string, 0, v.Len())
		for it := v.MapRange(); it.Next(); {
			entries = append(entries, canonical(it.Key())+"=>"+canonical(it.Value()))
		}
		sort.Strings(entries)
		return "map[" + strings.Join(entries, " ") + "]"
	case reflect.Slice, reflect.Array:
		var b strings.Builder
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			b.WriteString(canonical(v.Index(i)) + " ")
		}
		return b.String() + "]"
	case reflect.Func:
		return "func"
	case reflect.String:
		return fmt.Sprintf("%q", v.String())
	case reflect.Bool:
		return fmt.Sprint(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprint(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprint(v.Uint())
	default:
		return fmt.Sprintf("<%s>", v.Kind())
	}
}
