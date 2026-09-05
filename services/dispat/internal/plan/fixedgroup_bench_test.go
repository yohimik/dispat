package plan

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/ccme/v2"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// groupWorkspace builds a workspace of one versioning group with n members,
// all in the given mode, plus a history that moves the group.
func groupWorkspace(n int, mode model.Versioning) ([]*model.Package, *fakeGit) {
	space := &model.Space{Name: "shared", Versioning: mode}
	pkgs := make([]*model.Package, 0, n)
	git := newFakeGit(commit{sha: "c1", message: "feat(p0)!: moves the group"})
	for i := range n {
		name := "p" + strconv.Itoa(i)
		pkgs = append(pkgs, &model.Package{Name: name, Dir: "/r/pkgs/" + name, Space: space})
		git = git.tag(name, "1."+strconv.Itoa(i%10)+".0", "")
	}
	return pkgs, git
}

// BenchmarkComputeGroup measures the group path per mode. The partial modes
// add one pass over the members (groupDepth) and a handful of value
// comparisons to what the full depth already does, so their cost must track
// the full depth's rather than diverging with the group's size.
func BenchmarkComputeGroup(b *testing.B) {
	modes := []model.Versioning{
		model.VersioningFixed,
		model.VersioningFixedSparse,
		model.VersioningFixedMajorMinor,
		model.VersioningFixedMajor,
		model.VersioningFixedMajorSparse,
	}
	for _, mode := range modes {
		b.Run(string(mode), func(b *testing.B) {
			pkgs, git := groupWorkspace(200, mode)
			opts := Options{Packages: pkgs, Root: "/r"}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Compute(context.Background(), git, opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestPrefixHelpersDoNotAllocate fences the helpers the group path calls once
// per member: they read and compare version values, and a version built on
// the heap here would cost an allocation for every package of every group on
// every run.
func TestPrefixHelpersDoNotAllocate(t *testing.T) {
	a, b := v(1, 2, 3), v(1, 9, 0)
	pre := ccme.Version{Major: 2, Prerelease: []string{"beta", "0"}}

	allocs := testing.AllocsPerRun(100, func() {
		for depth := 1; depth <= 3; depth++ {
			_ = samePrefix(a, b, depth)
			_ = samePrefix(a, pre, depth)
			_ = groupTarget(b, depth)
		}
	})
	assert.Zero(t, allocs, "samePrefix and groupTarget must stay allocation free")
}

// TestGroupPathScalesWithTheMembers is the cheap counterpart of the benchmark:
// a large group still produces one release per member and one shared major,
// so nothing in the group path is quadratic in a way a 200-member workspace
// would expose as a timeout.
func TestGroupPathScalesWithTheMembers(t *testing.T) {
	pkgs, git := groupWorkspace(200, model.VersioningFixedMajor)
	p, err := Compute(context.Background(), git, Options{Packages: pkgs, Root: "/r"})
	require.NoError(t, err)

	require.Len(t, p.Releasing(), 200, "every member rides the shared major")
	for _, rel := range p.Releases {
		assertVersion(t, v(2, 0, 0), rel.Next)
	}
	assert.Equal(t, 199, len(p.Diagnostics), "one W234 per rider, none for the package that changed")
}
