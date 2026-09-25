// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package integration

// Final production-review command failures at the process boundary.
//
// Each scenario keeps a useful invariant visible outside the implementation:
// repository metadata must be readable before commit may mutate Git state,
// one malformed manifest must not erase evidence from a readable sibling,
// and one unsafe manifest edit must not prevent independent files in the same
// writer batch from reaching their requested state. Install also rechecks the
// filesystem after waiting for release metadata, before it downloads a file.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// finalInstallMutationServer publishes one ordinary release and changes the
// destination while answering the listing request. The change is complete
// before the client receives the response, so the scenario is deterministic:
// it recreates another process changing the filesystem between install's
// early replaceability check and its later write without timing a race.
func finalInstallMutationServer(t *testing.T, mutate func() error) (string, <-chan error, *atomic.Int32) {
	t.Helper()
	body := toolScript(toolNew)
	mutated := make(chan error, 1)
	var once sync.Once
	var downloads atomic.Int32
	var base string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/dl/") {
			downloads.Add(1)
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write(body)
			return
		}
		once.Do(func() { mutated <- mutate() })
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "v" + toolNew, "draft": false, "prerelease": false,
			"assets": []map[string]any{{
				"name": "tool-" + platform(), "size": len(body),
				"browser_download_url": base + "/dl/tool",
				"digest":               "sha256:" + strings.Repeat("00", 32),
			}},
		}})
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	t.Cleanup(srv.Close)
	return base, mutated, &downloads
}

func finalExecLayerRepo(t *testing.T) (*harness.Repo, string) {
	t.Helper()
	r := harness.New(t)
	marker := r.Path("script-ran")
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["which"] = models.Script{"touch " + harness.ShQuote(marker)}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	return r, marker
}

// TestFinalCommitRefusesUnreadableRepositoryMetadataBeforeMutation covers the
// three Git reads that construct the temporary validation environment. An
// absent config key is ordinary, but a repository that cannot read the key or
// locate its hooks is broken; commit must preserve HEAD, the index and the
// working tree rather than delegate an unvalidated write to Git.
func TestFinalCommitRefusesUnreadableRepositoryMetadataBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		want    string
	}{
		{
			name:    "commit cleanup configuration",
			pattern: "*config --get commit.cleanup*",
			want:    "read commit.cleanup",
		},
		{
			name:    "comment character configuration",
			pattern: "*config --get core.commentChar*",
			want:    "read core.commentChar",
		},
		{
			name:    "hooks location",
			pattern: "*rev-parse --path-format=absolute --git-path hooks*",
			want:    "resolve git hooks",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := authoringRepo(t)
			beforeHead := r.Git("rev-parse", "HEAD")
			r.WriteFile("tracked.txt", "changed but not committed\n")
			beforeIndex := r.Git("diff", "--cached")
			fault := harness.NewGitFault(t, harness.GitFault{
				Pattern: tc.pattern,
				Code:    128,
			})

			res := r.CommandEnv(fault.Env(), "commit", "-am", "fix(core): guarded")
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			combined := res.Stdout + res.Stderr
			assert.Contains(t, combined, tc.want)
			assert.Equal(t, 1, fault.Matches(), "the selected metadata read was attempted once")
			assert.Equal(t, beforeHead, r.Git("rev-parse", "HEAD"), "no commit was created")
			assert.Equal(t, beforeIndex, r.Git("diff", "--cached"), "-a never reached git commit")
			assert.Equal(t, "changed but not committed\n", readRepoFile(t, r, "tracked.txt"),
				"the working copy remains available to retry")
		})
	}
}

// TestFinalComputeKeepsReadableEvidenceBesideAMalformedManifest exercises the
// scanner's partial-result promise through compute. The readable npm manifest
// declares the same local edge in every supported field while a sibling Cargo
// manifest is malformed. Compute must warn, keep the sound declarations, and
// repair an impossible configured kind to the strongest runtime declaration.
func TestFinalComputeKeepsReadableEvidenceBesideAMalformedManifest(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{
		Consumer: "web",
		Provider: "core",
		Kind:     "mystery",
	}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core"}`)
	r.WriteFile("packages/web/package.json", `{
  "name": "@acme/web",
  "dependencies": {"@acme/core": "workspace:*"},
  "devDependencies": {"@acme/core": "workspace:*"},
  "peerDependencies": {"@acme/core": "workspace:*"},
  "optionalDependencies": {"@acme/core": "workspace:*"}
}`)
	r.WriteFile("packages/web/Cargo.toml", "[package\nname = \"broken\"\n")

	res := r.Command("compute")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	combined := res.Stdout + res.Stderr
	assert.Contains(t, combined, "some manifests failed to parse")
	assert.Contains(t, combined, "~ kind    web -> core (dependencies)")
	assert.Contains(t, combined, `invalid kind "mystery" -> dependencies`)
	assert.NotContains(t, combined, "- remove", "the malformed sibling cannot erase readable evidence")
}

// TestFinalWriterBatchContainsAMalformedOverrideWithoutLosingOtherEdits pins
// both sides of a batch failure. Relinking an npm manifest whose selected
// override container is a scalar is refused byte-for-byte, while a separate
// valid manifest in the same invocation is still updated and reported.
func TestFinalWriterBatchContainsAMalformedOverrideWithoutLosingOtherEdits(t *testing.T) {
	r := harness.New(t)
	const malformed = "{\n  \"name\": \"bad\",\n  \"overrides\": 7\n}\n"
	r.WriteFile("bad/package.json", malformed)
	r.WriteFile("good/package.json", "{\n  \"name\": \"good\"\n}\n")

	res := r.Command("writer", "bad/package.json", "good/package.json", "--link", "core=../core")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "is not an object")
	assert.Equal(t, malformed, readRepoFile(t, r, "bad/package.json"),
		"the refused manifest is byte-for-byte unchanged")
	assert.Contains(t, readRepoFile(t, r, "good/package.json"), `"core": "file:../core"`,
		"the independent manifest still reaches the requested state")
	assert.Contains(t, res.Stdout, "1 applied")
}

// TestFinalInstallRechecksTheFilesystemAfterReleaseDiscovery covers the two
// points that remain able to refuse an install after its initial destination
// check. A concurrent process may create the destination or block creation of
// its parent while the release listing is in flight. Both changes are refused
// before the asset transfer, and the object that appeared is preserved.
func TestFinalInstallRechecksTheFilesystemAfterReleaseDiscovery(t *testing.T) {
	t.Run("the destination became a folder", func(t *testing.T) {
		r := harness.New(t)
		bin := t.TempDir()
		target := filepath.Join(bin, "tool"+exeSuffix())
		base, mutated, downloads := finalInstallMutationServer(t, func() error {
			return os.Mkdir(target, 0o755)
		})

		res := r.Command("install", "acme/tool", "--api-url", base, "--bin-dir", bin,
			"--asset", "tool-{os}-{arch}")
		require.NoError(t, <-mutated)
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "is a folder, not a file dispat can replace")
		assert.Zero(t, downloads.Load(), "the late refusal happens before downloading the asset")
		assert.DirExists(t, target, "the object that appeared is not renamed or replaced")
	})

	t.Run("the install folder became a file", func(t *testing.T) {
		r := harness.New(t)
		parent := t.TempDir()
		bin := filepath.Join(parent, "future", "bin")
		base, mutated, downloads := finalInstallMutationServer(t, func() error {
			if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
				return err
			}
			return os.WriteFile(bin, []byte("belongs to another process\n"), 0o644)
		})

		res := r.Command("install", "acme/tool", "--api-url", base, "--bin-dir", bin,
			"--asset", "tool-{os}-{arch}", "--force")
		require.NoError(t, <-mutated)
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout+res.Stderr, "cannot create "+bin)
		assert.Zero(t, downloads.Load(), "folder creation is checked before downloading the asset")
		blocker, err := os.ReadFile(bin)
		require.NoError(t, err)
		assert.Equal(t, "belongs to another process\n", string(blocker), "the blocker is not replaced")
	})
}

// TestFinalStepCommandsStopWhenPlanningOrSelectionCannotReadGit verifies the
// two read phases before either recursive editor opens a target. A broken plan
// and a broken explicit commit window are both fatal, and neither command may
// weaken the failure into an empty selection or a successful no-op.
func TestFinalStepCommandsStopWhenPlanningOrSelectionCannotReadGit(t *testing.T) {
	for _, command := range []struct {
		name string
		args []string
	}{
		{
			name: "autowriter",
			args: []string{"autowriter", "--since", "HEAD~1", "--set-version", "9.9.9"},
		},
		{
			name: "autoreplacer",
			args: []string{"autoreplacer", "--since", "HEAD~1", "--files", "README.md",
				"--replace", "before=>after"},
		},
	} {
		for _, phase := range []struct {
			name    string
			pattern string
			want    string
		}{
			{
				name:    "plan",
				pattern: "*rev-parse --is-shallow-repository*",
				want:    "checking repository",
			},
			{
				name:    "selection window",
				pattern: "*log --format=* HEAD~1..HEAD*",
				want:    "resolving commits since",
			},
		} {
			t.Run(command.name+"/"+phase.name, func(t *testing.T) {
				r := harness.New(t)
				r.WriteConfigModel(libsConfig(echoBuild, 1))
				r.SeedPackage("packages", "core")
				r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"0.0.0"}`)
				r.WriteFile("packages/core/README.md", "before\n")
				r.Commit("feat(core): bootstrap")
				r.WriteFile("packages/core/change.txt", "pending\n")
				r.Commit("fix(core): establish a finite window")
				beforeManifest := readRepoFile(t, r, "packages/core/package.json")
				beforeReadme := readRepoFile(t, r, "packages/core/README.md")
				fault := harness.NewGitFault(t, harness.GitFault{Pattern: phase.pattern, Code: 128})

				res := r.CommandEnv(fault.Env(), command.args...)
				require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
				assert.Contains(t, res.Stdout+res.Stderr, phase.want)
				assert.Equal(t, 1, fault.Matches(), "the selected Git read was attempted once")
				assert.Equal(t, beforeManifest, readRepoFile(t, r, "packages/core/package.json"))
				assert.Equal(t, beforeReadme, readRepoFile(t, r, "packages/core/README.md"))
			})
		}
	}
}

// TestFinalAutoWriterUsesHealthyManifestBesideBrokenAndDifferentFormats pins the
// recursive writer's partial-scan behavior. A malformed Cargo manifest and a
// Dockerfile carrying no npm dependency are both outside the requested npm
// edit, while the valid package manifest remains usable. A link-unsupported Dockerfile cannot make
// strict mode accept a removal missing from the npm manifest; an unchanged
// link in that npm manifest still counts as a real match on a converged run.
func TestFinalAutoWriterUsesHealthyManifestBesideBrokenAndDifferentFormats(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name":"@acme/web","version":"0.0.0","dependencies":{"@acme/core":"^0.0.0"}}`)
	r.WriteFile("packages/web/Cargo.toml", "[package\nname = \"broken\"\n")
	r.WriteFile("packages/web/Dockerfile", "FROM alpine:3.22\n")
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autowriter", "--since", "all", "--manifests", "all",
		"--set", "@acme/core=^9.9.9", "--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "some manifests failed to parse")
	assert.Contains(t, readRepoFile(t, r, "packages/web/package.json"), `"@acme/core":"^9.9.9"`)
	assert.Equal(t, "FROM alpine:3.22\n", readRepoFile(t, r, "packages/web/Dockerfile"),
		"a readable but unsupported manifest is not treated as a failed write")

	linkArgs := []string{"autowriter", "--since", "all", "--package", "web", "--manifests", "all",
		"--link", "@acme/core=../core"}
	require.Equal(t, 0, r.Command(linkArgs...).Code)
	res = r.Command(append(linkArgs, "--strict")...)
	require.Equal(t, 0, res.Code, "an unchanged link in a supported manifest is a strict match")

	res = r.Command("autowriter", "--since", "all", "--package", "web", "--manifests", "all",
		"--strict", "--link", "absent=")
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "link:absent")
	assert.Contains(t, res.Stdout+res.Stderr, "matched no manifest")
}

// TestFinalAutoReplacerKeepsProviderFactsFromAHealthySibling proves that its
// provider fan-out follows an explicit local path when the dependency's alias
// names no workspace package. A malformed sibling manifest is reported at
// debug level but does not discard that sound local-path evidence.
func TestFinalAutoReplacerKeepsProviderFactsFromAHealthySibling(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.WriteFile("packages/core/package.json", `{"name":"@acme/core","version":"0.0.0"}`)
	r.WriteFile("packages/web/package.json",
		`{"name":"@acme/web","version":"0.0.0","dependencies":{"local-alias":"file:../core"}}`)
	r.WriteFile("packages/web/Cargo.toml", "[package\nname = \"broken\"\n")
	r.WriteFile("packages/web/README.md", "uses core 0.0.0\n")
	r.Commit("feat(core,web): bootstrap")

	res := r.Command("autoreplacer", "--since", "all", "--files", "README.md",
		"--replace", "uses {provider} {providerPrevious}=>uses {provider} {providerVersion}",
		"--log-level", "debug")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "some root manifests failed to parse")
	assert.Contains(t, readRepoFile(t, r, "packages/web/README.md"), "uses core 0.1.0")
}

// TestFinalExecFailsClosedWhenLayeredConfigurationCannotBeDiscovered reaches
// each lazy discovery site after the root config has loaded. Whether the bad
// layer is needed for the subject, the script source, or only the subject's
// environment, exec refuses before starting the root-level script.
func TestFinalExecFailsClosedWhenLayeredConfigurationCannotBeDiscovered(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "package environment",
			args: []string{"exec", "which", "--for", "pkg:core", "--script-from", "root"},
		},
		{
			name: "space environment",
			args: []string{"exec", "which", "--for", "space:libs", "--script-from", "root"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, marker := finalExecLayerRepo(t)
			r.WriteFile("packages/dispat.json", `{"scripts":`)

			res := r.Command(tc.args...)
			require.NotZero(t, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, "packages/dispat.json")
			assert.NoFileExists(t, marker, "the root-level script cannot run with an unreadable subject layer")
		})
	}
}

// TestFinalExecComputedEnvironmentNeedsNoDeclaredPairs covers the empty static
// environment explicitly: --env dispat still supplies the package's computed
// release variables when there are no configured names to remove from them.
func TestFinalExecComputedEnvironmentNeedsNoDeclaredPairs(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Env = nil
	cfg.Scripts["show-package"] = models.Script{`printf 'package=%s\n' "$DISPAT_PACKAGE"`}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Command("exec", "show-package", "--for", "pkg:core", "--script-from", "root", "--env", "dispat")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "package=core")
}

// TestFinalRecursiveEditorsStopBetweenAtomicFileWrites drives Ctrl-C through
// both graph-ordered editors after the first write is visible. A file already
// replaced remains valid, the scheduler stops before all later files are
// touched, and the command reports interruption instead of a successful
// partial sweep.
func TestFinalRecursiveEditorsStopBetweenAtomicFileWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not implemented for child processes on Windows")
	}
	const paddingSize = 8 << 20

	t.Run("autowriter", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		manifest := func(name string, padding int) string {
			return fmt.Sprintf(`{"name":%q,"version":"0.0.0","dependencies":{"dep":"^1.0.0"},"padding":%q}`,
				name, strings.Repeat("x", padding))
		}
		r.WriteFile("packages/core/package.json", manifest("core", 0))
		r.WriteFile("packages/core/fixtures/a/package.json", manifest("fixture-a", paddingSize))
		r.WriteFile("packages/core/fixtures/b/package.json", manifest("fixture-b", paddingSize))
		r.Commit("feat(core): bootstrap")

		proc := r.StartRelease("autowriter", "--since", "all", "--manifests", "all", "--set", "dep=^2.0.0")
		require.Eventually(t, func() bool {
			body, err := os.ReadFile(r.Path("packages/core/fixtures/a/package.json"))
			return err == nil && strings.Contains(string(body), "^2.0.0")
		}, 15*time.Second, time.Millisecond, "the first atomic manifest write never completed")
		proc.Signal(os.Interrupt)
		res := proc.Wait()

		assert.NotZero(t, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "autowriter interrupted")
		later := readRepoFile(t, r, "packages/core/package.json")
		assert.Contains(t, later, "^1.0.0", "interruption stops before every later manifest is rewritten")
	})

	t.Run("autoreplacer", func(t *testing.T) {
		r := harness.New(t)
		cfg := libsConfig(echoBuild, 1)
		cfg.Dependencies = []models.DependencyConfig{{Consumer: "web", Provider: "core"}}
		r.WriteConfigModel(cfg)
		r.SeedPackage("packages", "core")
		r.SeedPackage("packages", "web")
		r.WriteFile("packages/core/00.txt", "before\n")
		r.WriteFile("packages/core/10.txt", strings.Repeat("x", paddingSize)+"before\n")
		r.WriteFile("packages/core/20.txt", strings.Repeat("x", paddingSize)+"before\n")
		r.Commit("feat(core): bootstrap")

		proc := r.StartRelease("autoreplacer", "--since", "all", "--files", "*.txt", "--replace", "before=>after")
		require.Eventually(t, func() bool {
			body, err := os.ReadFile(r.Path("packages/core/00.txt"))
			return err == nil && strings.Contains(string(body), "after")
		}, 15*time.Second, time.Millisecond, "the first atomic replacement never completed")
		proc.Signal(os.Interrupt)
		res := proc.Wait()

		assert.NotZero(t, res.Code)
		assert.Contains(t, res.Stdout+res.Stderr, "autoreplacer interrupted")
		later := readRepoFile(t, r, "packages/core/20.txt")
		assert.Contains(t, later, "before", "interruption stops before every later file is rewritten")
	})
}
