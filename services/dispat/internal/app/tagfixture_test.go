package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
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
