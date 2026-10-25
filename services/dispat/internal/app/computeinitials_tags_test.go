package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// TestBaselineReasonsReadOneInventoryPerRepository: compute answers every
// candidate of a repository from one tag inventory, and each package's list,
// order included, is the one its own `git tag --list` returns. core's format
// prefix also matches core-next's tags, which is the overlap the per-package
// matcher has to keep apart.
func TestBaselineReasonsReadOneInventoryPerRepository(t *testing.T) {
	f := newTagFixture(t, "{name}-{version}", map[string][]string{
		"core":      {"core-1.0.0", "core-1.1.0"},
		"core-next": {"core-next-1.0.0"},
		"cli":       {"cli-1.0.0", "cli-banana"},
		"web":       {"web-2.0.0-beta.1"},
	}, []string{"core", "core-next", "cli", "web", "docs"})
	ctx := t.Context()

	before := gitx.GitInvocations()
	reasons, errs := f.app.baselineReasons(ctx, f.candidates)
	assert.Equal(t, uint64(2), gitx.GitInvocations()-before, "one HEAD check and one tag inventory for the repository")

	repositories := f.app.baselineRepositories(ctx, f.candidates)
	got := make(map[string]string)
	for i, c := range f.candidates {
		require.NoError(t, errs[i])
		perPackage, err := f.app.git.Tags(ctx, c.pkg.Name, plan.TagFormatFor(c.pkg))
		require.NoError(t, err)
		repo := repositories[c.pkg.Repository]
		assert.Equal(t, perPackage, repo.tags[c.pkg.Name], "%s: the inventory gives the per-package listing", c.pkg.Name)
		assert.Equal(t, baselineReason(repo.aliases.Without(perPackage, c.pkg.Name, zerolog.Nop())), reasons[i], c.pkg.Name)
		got[c.pkg.Name] = reasons[i]
	}
	assert.Equal(t, map[string]string{
		"core":      "newest tag core-next-1.0.0 is not a version",
		"core-next": "",
		"cli":       "newest tag cli-banana is not a version",
		"web":       "no release tag yet",
		"docs":      "no release tag yet",
	}, got)
}

// BenchmarkBaselineReasons measures compute's tag reading for a repository of
// 64 released packages: the git processes it starts are the claim.
func BenchmarkBaselineReasons(b *testing.B) {
	const packages = 64
	tags := make(map[string][]string, packages)
	order := make([]string, packages)
	for i := range packages {
		name := fmt.Sprintf("pkg%02d", i)
		order[i] = name
		tags[name] = []string{name + "@1.0.0", name + "@1.1.0"}
	}
	f := newTagFixture(b, "", tags, order)
	ctx := context.Background()
	before := gitx.GitInvocations()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reasons, errs := f.app.baselineReasons(ctx, f.candidates)
		for j := range reasons {
			if errs[j] != nil || reasons[j] != "" {
				b.Fatalf("%s is released: %q, %v", f.candidates[j].pkg.Name, reasons[j], errs[j])
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(b.N), "GitInvocations/op")
}
