package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

func TestPinnedPlanningConfigResolvesRelativeProvenanceFromAppRoot(t *testing.T) {
	root := t.TempDir()
	repositoryConfig := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(repositoryConfig, []byte("{}"), 0o600))
	a := New(root, &config.File{SourceFiles: []string{"dispat.json"}}, zerolog.Nop())
	files, err := a.pinnedConfigFiles()
	require.NoError(t, err)
	require.Equal(t, []execution.PinnedConfigFile{{Path: "dispat.json"}}, files)
	private := &planningMaterialization{repositories: map[string]string{"": t.TempDir()}}
	mapped, err := a.pinnedConfigPath(private, "dispat.json")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(private.repositories[""], "dispat.json"), mapped)
	current, err := os.Getwd()
	require.NoError(t, err)
	relativeRoot, err := filepath.Rel(current, root)
	require.NoError(t, err)
	relativeApp := New(relativeRoot, &config.File{SourceFiles: []string{"dispat.json"}}, zerolog.Nop())
	relativeFiles, err := relativeApp.pinnedConfigFiles()
	require.NoError(t, err)
	require.Equal(t, files, relativeFiles)
}

func TestPinnedPlanningMaterializationKeepsSnapshotBytesAndOriginalHistory(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(root, "input"), []byte("committed\n"), 0o644))
	fixtureGit(t, root, "add", "input")
	fixtureGit(t, root, "commit", "-qm", "feat(core): baseline")
	fixtureGit(t, root, "tag", "-a", "-m", "release", "core/v1.0.0")
	head := recordGit(t, root, "rev-parse", "HEAD")
	tagObject := recordGit(t, root, "rev-parse", "refs/tags/core/v1.0.0")
	require.NoError(t, os.WriteFile(filepath.Join(root, "input"), []byte("pinned edit\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "new-input"), []byte("pinned new\n"), 0o644))

	git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: head}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	materialized, err := materializePlanningInputs(t.Context(), planningMaterializationOptions{
		repositories: pinned.Repositories, sourceDirs: map[string]string{"": root},
		refs: []execution.PinnedNativeRef{{Name: "core/v1.0.0", Object: tagObject, Peeled: head}},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, materialized.close()) }()
	private := materialized.repositories[""]

	// Neither a later checkout edit nor a tag replacement changes the plan
	// input. The transport commit is present only as a fetched object; HEAD
	// still names the semantic history commit.
	require.NoError(t, os.WriteFile(filepath.Join(root, "input"), []byte("later edit\n"), 0o644))
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "feat(core): later")
	fixtureGit(t, root, "tag", "-fa", "-m", "replacement", "core/v1.0.0")
	require.Equal(t, head, recordGit(t, private, "rev-parse", "HEAD"))
	require.Equal(t, tagObject, recordGit(t, private, "rev-parse", "refs/tags/core/v1.0.0"))
	require.Equal(t, head, recordGit(t, private, "rev-parse", "core/v1.0.0^{}"))
	require.Equal(t, "feat(core): baseline", recordGit(t, private, "log", "-1", "--format=%s"))
	input, err := os.ReadFile(filepath.Join(private, "input"))
	require.NoError(t, err)
	require.Equal(t, "pinned edit\n", string(input))
	newInput, err := os.ReadFile(filepath.Join(private, "new-input"))
	require.NoError(t, err)
	require.Equal(t, "pinned new\n", string(newInput))
	require.True(t, strings.Contains(recordGit(t, private, "status", "--porcelain"), "input"))
}

func TestPinnedPlanningMaterializationCarriesNonCommitTags(t *testing.T) {
	root := t.TempDir()
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(root, "input"), []byte("data\n"), 0o644))
	fixtureGit(t, root, "add", "input")
	fixtureGit(t, root, "commit", "-qm", "fix(core): baseline")
	head := recordGit(t, root, "rev-parse", "HEAD")
	tree := recordGit(t, root, "rev-parse", "HEAD^{tree}")
	blob := recordGit(t, root, "rev-parse", "HEAD:input")
	fixtureGit(t, root, "tag", "tree-ref", tree)
	fixtureGit(t, root, "tag", "blob-ref", blob)
	store := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: head}},
		OpenRepository: func(string) *gitx.LocalGitx { return store }, Store: store, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	private, err := materializePlanningInputs(t.Context(), planningMaterializationOptions{
		repositories: pinned.Repositories, sourceDirs: map[string]string{"": root},
		refs: []execution.PinnedNativeRef{
			{Name: "tree-ref", Object: tree}, {Name: "blob-ref", Object: blob},
		}, maxObjectBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, private.close()) }()
	require.Equal(t, tree, recordGit(t, private.repositories[""], "rev-parse", "refs/tags/tree-ref"))
	require.Equal(t, blob, recordGit(t, private.repositories[""], "rev-parse", "refs/tags/blob-ref"))
}

func TestPinnedPlanningMaterializationRefusesAmbientSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "ambient.json")
	require.NoError(t, os.WriteFile(outside, []byte(`{"spaces":{}}`), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dispat.json")))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): config link")
	git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: recordGit(t, root, "rev-parse", "HEAD")}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	_, err = materializePlanningInputs(t.Context(), planningMaterializationOptions{
		repositories: pinned.Repositories, sourceDirs: map[string]string{"": root},
	})
	require.ErrorContains(t, err, "escapes captured sources")
}

func TestPlanFromMaterializationUsesPinnedHistoryAndRehomesExecutionPaths(t *testing.T) {
	root := t.TempDir()
	cfg := config.File{Spaces: map[string]config.SpaceConfig{
		"libs": {Path: config.PathList{"packages"}},
	}}
	encoded, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"), encoded, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "package.json"),
		[]byte(`{"name":"core","version":"0.0.0"}`), 0o644))
	fixtureGit(t, root, "init", "-q")
	fixtureGit(t, root, "config", "user.name", "Test")
	fixtureGit(t, root, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "add", "-A")
	fixtureGit(t, root, "commit", "-qm", "fix(core): pinned patch")
	head := recordGit(t, root, "rev-parse", "HEAD")
	flags := pflag.NewFlagSet("planning-overrides", pflag.ContinueOnError)
	flags.IntSlice("concurrency", nil, "")
	require.NoError(t, flags.Set("concurrency", "2,1"))
	loaded, err := config.Load(filepath.Join(root, "dispat.json"), flags)
	require.NoError(t, err)
	git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: head}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	materialized, err := materializePlanningInputs(t.Context(), planningMaterializationOptions{
		repositories: pinned.Repositories, sourceDirs: map[string]string{"": root},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, materialized.close()) }()

	fixtureGit(t, root, "commit", "-qm", "feat(core): later minor", "--allow-empty")
	a := New(root, loaded, zerolog.Nop())
	a.SetPlanningConfigInvocation(PlanningConfigInvocation{Concurrency: []int{2, 1}, ConcurrencySet: true})
	planFromPinned, err := a.planFromMaterialization(t.Context(), materialized, nil)
	require.NoError(t, err)
	require.Equal(t, head, a.plannedHeads[""])
	firstDigest, err := a.calculatePlanDigest(t.Context(), planFromPinned, ReleaseOptions{})
	require.NoError(t, err)
	fixtureGit(t, root, "commit", "-qm", "feat(core): later again", "--allow-empty")
	secondDigest, err := a.calculatePlanDigest(t.Context(), planFromPinned, ReleaseOptions{})
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest, "live HEAD cannot rename a plan made from the captured history")
	files, err := a.pinnedConfigFiles()
	require.NoError(t, err)
	require.Equal(t, []execution.PinnedConfigFile{{Path: "dispat.json"}}, files)
	require.Equal(t, "0.0.1", planFromPinned.Releases["core"].Next.String())
	require.Equal(t, filepath.Join(root, "packages", "core"), planFromPinned.Releases["core"].Pkg.Dir)
	require.Equal(t, root, a.plannedOptions.Root)
	live, err := New(root, loaded, zerolog.Nop()).plan(t.Context())
	require.NoError(t, err)
	require.Equal(t, "0.1.0", live.Releases["core"].Next.String())

	// A config edit between the original load and source capture cannot be
	// silently combined with the preloaded policy in one distributed plan.
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"spaces":{"libs":{"path":["packages"]}},"commitErrors":"error"}`), 0o644))
	later, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources:        []execution.Source{{Path: ".", Dir: root, Head: recordGit(t, root, "rev-parse", "HEAD")}},
		OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer later.Close()
	laterPrivate, err := materializePlanningInputs(t.Context(), planningMaterializationOptions{
		repositories: later.Repositories, sourceDirs: map[string]string{"": root},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, laterPrivate.close()) }()
	stale := New(root, loaded, zerolog.Nop())
	stale.SetPlanningConfigInvocation(PlanningConfigInvocation{Concurrency: []int{2, 1}, ConcurrencySet: true})
	_, err = stale.planFromMaterialization(t.Context(), laterPrivate, nil)
	require.ErrorContains(t, err, "control config changed between load and captured planning snapshot")
}

func TestPinnedFleetPlanKeepsRepositoryIdentityAndOriginalExecutionPaths(t *testing.T) {
	control, source, path := compositionReleaseFixture(t)
	loaded, err := config.Load(path, nil)
	require.NoError(t, err)
	workspace, err := config.ComposeWorkspaceWithPinResolver(t.Context(), loaded, path, control, nil, nil, nil)
	require.NoError(t, err)
	sources := []execution.Source{
		{Name: config.ControlRepository, Path: ".", Dir: control, Head: recordGit(t, control, "rev-parse", "HEAD")},
		{Name: "source", Path: "source", Dir: source, Head: recordGit(t, source, "rev-parse", "HEAD")},
	}
	open := func(dir string) *gitx.LocalGitx { return &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()} }
	pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
		Sources: sources, OpenRepository: open, Store: open(control), Log: zerolog.Nop(),
	})
	require.NoError(t, err)
	defer pinned.Close()
	a := NewWorkspace(control, loaded, workspace, zerolog.Nop())
	private, err := a.openPinnedPlanning(t.Context(), pinned, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, private.close()) }()
	planFromPinned, err := a.planFromMaterialization(context.Background(), private, nil)
	require.NoError(t, err)
	files, err := a.pinnedConfigFiles()
	require.NoError(t, err)
	require.Equal(t, []execution.PinnedConfigFile{{Repository: config.ControlRepository, Path: "dispat.json"}}, files)
	canonicalSource, err := filepath.EvalSymlinks(source)
	require.NoError(t, err)
	require.Equal(t, sources[0].Head, planFromPinned.RepositoryHeads[config.ControlRepository])
	require.Equal(t, sources[1].Head, planFromPinned.RepositoryHeads["source"])
	require.Equal(t, canonicalSource, planFromPinned.Releases["lib"].Pkg.RepoRoot)
	require.Equal(t, filepath.Join(canonicalSource, "pkg"), planFromPinned.Releases["lib"].Pkg.Dir)
	require.Equal(t, control, a.plannedOptions.Root)
	require.Equal(t, canonicalSource, a.plannedOptions.Repositories["source"].Root)
}
