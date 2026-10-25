package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// tagFixture is a committed one-space repository whose packages carry the
// release tags a test names, with every package a compute candidate.
type tagFixture struct {
	app        *App
	candidates []manifestBaseline
}

func fixtureGit(t testing.TB, root string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// newTagFixture lays out one package folder per entry of tags under a space
// whose tag format is format, commits them, and writes each package's tags in
// the listed order, one commit apart so creation order is the listed order.
func newTagFixture(t testing.TB, format string, tags map[string][]string, order []string) tagFixture {
	t.Helper()
	root := t.TempDir()
	cfg := config.File{
		Scripts: map[string]config.Script{"build": {"true"}},
		Spaces: map[string]config.SpaceConfig{
			"libs": {Path: config.PathList{"packages"}, TagFormat: format, Flow: &config.SpaceFlowConfig{Build: []string{"build"}}},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "dispat.json")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range order {
		dir := filepath.Join(root, "packages", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf(`{"name": %q, "version": "1.0.0"}`, name)
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-q", "-m", "packages")
	for step, name := range order {
		for _, tag := range tags[name] {
			fixtureGit(t, root, "commit", "-q", "--allow-empty", "-m", tag)
			stamp := fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", 1700000000+step)
			cmd := exec.Command("git", "-C", root, "tag", "-a", "-m", tag, tag)
			cmd.Env = append(os.Environ(), stamp)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git tag %s: %v: %s", tag, err, out)
			}
		}
	}
	loaded, err := config.Load(cfgPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(root, loaded, zerolog.Nop())
	pkgs, err := a.packages()
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]manifestBaseline, 0, len(pkgs))
	for _, p := range pkgs {
		candidates = append(candidates, manifestBaseline{pkg: p, version: ccme.Version{Major: 1}, manifest: p.Name + "/package.json"})
	}
	return tagFixture{app: a, candidates: candidates}
}

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
