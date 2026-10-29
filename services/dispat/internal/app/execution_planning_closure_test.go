package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/execution"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// A folder reference is part of the operation policy even when it does not
// change the semantic task graph. Both the first planner and peer/retained
// reconstruction must reject an ambient source before producing that plan.
func TestPinnedPlanningConfigurationReferenceClosure(t *testing.T) {
	for _, level := range []string{"root", "space", "package"} {
		for _, route := range []string{"direct", "transitive"} {
			for _, location := range []string{"ambient", "captured"} {
				t.Run(level+"/"+route+"/"+location, func(t *testing.T) {
					root := t.TempDir()
					folder := filepath.Join(root, "packages")
					if level == "root" {
						folder = root
					}
					if level == "package" {
						folder = filepath.Join(folder, "core")
					}
					require.NoError(t, os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755))
					writePlanningClosureFile(t, filepath.Join(root, "dispat.json"),
						`{"spaces":{"libs":{"path":["packages"]}}}`)
					writePlanningClosureFile(t, filepath.Join(root, "packages", "core", "package.json"),
						`{"name":"core","version":"0.0.0"}`)
					policyDir := t.TempDir()
					if location == "captured" {
						policyDir = filepath.Join(root, "config")
						require.NoError(t, os.MkdirAll(policyDir, 0o755))
					}
					policyPath := filepath.Join(policyDir, "policy.json")
					policyFor := func(command string) string {
						document := map[string]any{"flow": map[string]string{"publish": "release"},
							"scripts": map[string]string{"release": command}}
						if level == "root" {
							document["spaces"] = map[string]any{"libs": map[string]any{"path": []string{"packages"}}}
						}
						encoded, err := json.Marshal(document)
						require.NoError(t, err)
						return string(encoded)
					}
					writePlanningClosureFile(t, policyPath, policyFor("echo captured"))
					target := policyPath
					if location == "captured" {
						var err error
						target, err = filepath.Rel(folder, policyPath)
						require.NoError(t, err)
					}
					if route == "transitive" {
						writePlanningClosureRef(t, filepath.Join(folder, "intermediate.json"), target)
						target = "intermediate.json"
					}
					writePlanningClosureRef(t, filepath.Join(folder, "dispat.json"), target)
					fixtureGit(t, root, "init", "-q")
					fixtureGit(t, root, "config", "user.name", "Test")
					fixtureGit(t, root, "config", "user.email", "test@example.com")
					fixtureGit(t, root, "add", "-A")
					fixtureGit(t, root, "commit", "-qm", "fix(core): capture native policy")
					loaded, err := config.Load(filepath.Join(root, "dispat.json"), nil)
					require.NoError(t, err)
					first := New(root, loaded, zerolog.Nop())
					original, err := first.selectedPlan(t.Context(), ReleaseOptions{})
					require.NoError(t, err)
					require.Equal(t, []string{"echo captured"}, original.Releases["core"].Pkg.Space.PublishScript)
					planDigest, err := first.calculatePlanDigest(t.Context(), original, ReleaseOptions{})
					require.NoError(t, err)
					graph, err := (&release.Executor{PublishGroup: publishByRepository}).CompileTaskGraph(original)
					require.NoError(t, err)
					selection, policy, err := first.pinnedReleaseDocuments(ReleaseOptions{})
					require.NoError(t, err)
					configFiles, err := first.pinnedConfigFiles()
					require.NoError(t, err)
					invocation, err := first.pinnedInvocation()
					require.NoError(t, err)
					git := &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()}
					pinned, err := execution.CapturePinnedRunSources(t.Context(), execution.PinnedSourceCaptureOptions{
						Sources:        []execution.Source{{Path: ".", Dir: root, Head: recordGit(t, root, "rev-parse", "HEAD")}},
						OpenRepository: func(string) *gitx.LocalGitx { return git }, Store: git, Log: zerolog.Nop(),
					})
					require.NoError(t, err)
					defer pinned.Close()
					private, err := materializePlanningInputs(t.Context(), planningMaterializationOptions{
						repositories: pinned.Repositories, sourceDirs: map[string]string{"": root},
					})
					require.NoError(t, err)
					defer func() { require.NoError(t, private.close()) }()
					writePlanningClosureFile(t, policyPath, policyFor("echo changed after capture"))
					// Local planning still accepts absolute refs and reads their
					// current bytes; only a captured planner applies this boundary.
					liveConfig, err := config.Load(filepath.Join(root, "dispat.json"), nil)
					require.NoError(t, err)
					local, err := New(root, liveConfig, zerolog.Nop()).selectedPlan(t.Context(), ReleaseOptions{})
					require.NoError(t, err)
					require.Equal(t, []string{"echo changed after capture"}, local.Releases["core"].Pkg.Space.PublishScript)
					captured, err := New(root, loaded, zerolog.Nop()).planFromMaterialization(t.Context(), private, nil)
					if location == "ambient" {
						require.ErrorContains(t, err, "escapes captured sources")
						require.Nil(t, captured)
					} else {
						require.NoError(t, err)
						require.Equal(t, []string{"echo captured"}, captured.Releases["core"].Pkg.Space.PublishScript)
					}
					peer, err := recomputePeerReleasePlan(t.Context(), peerReleasePlanningOptions{
						Inputs: execution.PinnedRunInputs{Repositories: pinned.Repositories, ConfigFiles: configFiles,
							Invocation: invocation, Selection: selection, Options: policy},
						Binding: execution.ChoreographyRunBinding{Run: "closure", Generation: "one",
							PlanDigest: planDigest, GraphDigest: graph.Digest()},
						SemanticGraphDigest: graph.Digest(), SourceDirs: map[string]string{"": root},
						MaxPackBytes: 1 << 20, Log: zerolog.Nop(),
					})
					if location == "ambient" {
						require.ErrorContains(t, err, "escapes captured sources")
						require.Nil(t, peer)
					} else {
						require.NoError(t, err)
						defer func() { require.NoError(t, peer.close()) }()
						require.Equal(t, []string{"echo captured"}, peer.plan.Releases["core"].Pkg.Space.PublishScript)
					}
				})
			}
		}
	}
}

func writePlanningClosureRef(t *testing.T, path, target string) {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"$ref": target})
	require.NoError(t, err)
	writePlanningClosureFile(t, path, string(encoded))
}

func writePlanningClosureFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
}
