package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func nativeAdmissionFixture(t *testing.T) (*workspaceRecorder, *plan.Release, workspaceenv.NativeTransitionScope) {
	t.Helper()
	w, rel := recordFixture(t, false, false)
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{rel.Pkg}))
	pl := snapshotPlan(t, w, rel)
	w.setLinkPlan(pl)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	return w, rel, attachNativeAdmissionStore(t, w)
}

func attachNativeAdmissionStore(t *testing.T, w *workspaceRecorder) workspaceenv.NativeTransitionScope {
	t.Helper()
	root := w.app.workspace.ControlRoot
	configPath := filepath.Join(root, "dispat.json")
	require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))
	store, err := workspaceenv.NewLivePins(root, configPath,
		map[string]string{"PACKAGE_LIB": "source"}, []string{config.ControlRepository, "source"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	w.pins.live = store
	scope := workspaceenv.NativeTransitionScope{
		Root: root, ConfigPath: configPath,
		Environment: []string{
			workspaceenv.Root + "=" + root,
			workspaceenv.Config + "=dispat.json",
			workspaceenv.Imports + "=[]",
			workspaceenv.Owners + `={"PACKAGE_LIB":"source"}`,
			workspaceenv.Repositories + `=["control","source"]`,
			store.Environment(),
		},
	}
	return scope
}

func TestNativeAdmissionKeepsHeadPinnedUntilExactTagObjectPasses(t *testing.T) {
	w, rel, scope := nativeAdmissionFixture(t)
	source := w.byName["source"]
	prior := source.expectedHead
	proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: "source", priorHead: prior, tagRequested: true})
	proof.Kind = workspaceenv.NativeTransitionSourceRecord
	token, err := workspaceenv.BeginNativeTransition(scope, proof)
	require.NoError(t, err)
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix(lib): nested native source record")
	proof.NewHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	recordGit(t, source.repo.Root, "tag", "-a", rel.TagName(), "-m", "native release")
	object := recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName())
	proof.Tags = []workspaceenv.NativeTagRef{{Name: rel.TagName(), Object: object, Commit: proof.NewHead}}
	require.NoError(t, workspaceenv.CompleteNativeTransition(scope, token, proof))

	recordGit(t, source.repo.Root, "tag", "-f", "-a", rel.TagName(), "-m", "foreign replacement")
	err = w.verifySnapshot(t.Context(), rel)
	require.ErrorContains(t, err, "E330")
	assert.Equal(t, prior, source.expectedHead, "failed tag validation cannot widen the accepted HEAD")

	recordGit(t, source.repo.Root, "update-ref", "refs/tags/"+rel.TagName(), object)
	require.NoError(t, w.verifySnapshot(t.Context(), rel))
	assert.Equal(t, proof.NewHead, source.expectedHead)
}

func TestNativeAdmissionRejectsFailedAndUnsettledSourceRecords(t *testing.T) {
	for _, state := range []string{"failed", "pending"} {
		t.Run(state, func(t *testing.T) {
			w, rel, scope := nativeAdmissionFixture(t)
			source := w.byName["source"]
			prior := source.expectedHead
			proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: "source", priorHead: prior, tagRequested: true})
			proof.Kind = workspaceenv.NativeTransitionSourceRecord
			token, err := workspaceenv.BeginNativeTransition(scope, proof)
			require.NoError(t, err)
			recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix(lib): incomplete native record")
			if state == "failed" {
				require.NoError(t, workspaceenv.FailNativeTransition(scope, token, "source"))
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err = w.verifySnapshot(ctx, rel)
			require.Error(t, err)
			assert.Equal(t, prior, source.expectedHead, "unfinished native work cannot advance the accepted HEAD")
			assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
		})
	}
}

func TestNativeAdmissionAcceptsExactSameHeadTagAndExplicitTaglessCommit(t *testing.T) {
	t.Run("same-head tag", func(t *testing.T) {
		w, rel, scope := nativeAdmissionFixture(t)
		source := w.byName["source"]
		proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: "source", priorHead: source.expectedHead, tagRequested: true})
		proof.Kind = workspaceenv.NativeTransitionSourceRecord
		token, err := workspaceenv.BeginNativeTransition(scope, proof)
		require.NoError(t, err)
		recordGit(t, source.repo.Root, "tag", "-a", rel.TagName(), "-m", "same-head native tag")
		proof.NewHead = proof.PriorHead
		proof.Tags = []workspaceenv.NativeTagRef{{
			Name: rel.TagName(), Object: recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName()),
			Commit: proof.NewHead,
		}}
		require.NoError(t, workspaceenv.CompleteNativeTransition(scope, token, proof))
		require.NoError(t, w.verifySnapshot(t.Context(), rel))
		assert.Equal(t, proof.PriorHead, source.expectedHead)
	})
	t.Run("tagless source commit", func(t *testing.T) {
		w, rel, scope := nativeAdmissionFixture(t)
		source := w.byName["source"]
		proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: "source", priorHead: source.expectedHead})
		proof.Kind = workspaceenv.NativeTransitionSourceRecord
		token, err := workspaceenv.BeginNativeTransition(scope, proof)
		require.NoError(t, err)
		recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix(lib): native tagless commit")
		proof.NewHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		require.NoError(t, workspaceenv.CompleteNativeTransition(scope, token, proof))
		require.NoError(t, w.verifySnapshot(t.Context(), rel))
		assert.Equal(t, proof.NewHead, source.expectedHead)
		assert.Empty(t, recordGit(t, source.repo.Root, "tag", "--list", rel.TagName()))
	})
}

func TestNativeAdmissionAllowsOnlyPlannedTaglessSettlementOwner(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			w, rel, scope := nativeAdmissionFixture(t)
			control := w.byName[config.ControlRepository]
			if allowed {
				w.snapshot.nativeOwners[rel.Pkg.Name][config.ControlRepository] = true
				w.snapshot.byPackage[rel.Pkg.Name].words[w.snapshot.repoIndex[config.ControlRepository]/64] |=
					uint64(1) << uint(w.snapshot.repoIndex[config.ControlRepository]%64)
			}
			proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: config.ControlRepository, priorHead: control.expectedHead})
			proof.Kind = workspaceenv.NativeTransitionLinkSettlement
			token, err := workspaceenv.BeginNativeTransition(scope, proof)
			require.NoError(t, err)
			recordGit(t, control.repo.Root, "commit", "--allow-empty", "-qm", "chore: native fleet link settlement")
			proof.NewHead = recordGit(t, control.repo.Root, "rev-parse", "HEAD")
			require.NoError(t, workspaceenv.CompleteNativeTransition(scope, token, proof))
			if allowed {
				require.NoError(t, w.verifySnapshot(t.Context(), rel))
				assert.Equal(t, proof.NewHead, control.expectedHead)
			} else {
				// Selecting the owner without granting its planned route must not
				// turn an unrelated native write into an accepted input.
				w.snapshot.byPackage[rel.Pkg.Name].words[w.snapshot.repoIndex[config.ControlRepository]/64] |=
					uint64(1) << uint(w.snapshot.repoIndex[config.ControlRepository]%64)
				err := w.verifySnapshot(t.Context(), rel)
				require.ErrorContains(t, err, "E330")
			}
		})
	}
}

func TestNativeAdmissionUsesTheLastCausalMovingAliasObject(t *testing.T) {
	w, lib := recordFixture(t, false, false)
	lib.Pkg.Space.AliasTags = []model.AliasTag{{Format: "latest-v{major}", Moving: true, Force: true}}
	toolPkg := *lib.Pkg
	toolPkg.Name = "tool"
	toolPkg.Space = &model.Space{Name: "tools", AliasTags: []model.AliasTag{{Format: "latest-v{major}", Moving: true, Force: true}}}
	tool := *lib
	tool.Pkg = &toolPkg
	require.NoError(t, w.captureSnapshot(t.Context(), []*model.Package{lib.Pkg, tool.Pkg}))
	pl := snapshotPlan(t, w, lib, &tool)
	w.setLinkPlan(pl)
	w.setSnapshotPlan(pl)
	require.NoError(t, w.prepare(t.Context(), pl))
	scope := attachNativeAdmissionStore(t, w)
	source := w.byName["source"]

	write := func(rel *plan.Release, prior, message string) (string, string) {
		t.Helper()
		proof := nativeTransitionFor(nativeTransitionSpec{release: rel, owner: "source", priorHead: prior, tagRequested: true})
		proof.Kind = workspaceenv.NativeTransitionSourceRecord
		token, err := workspaceenv.BeginNativeTransition(scope, proof)
		require.NoError(t, err)
		recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", message)
		proof.NewHead = recordGit(t, source.repo.Root, "rev-parse", "HEAD")
		recordGit(t, source.repo.Root, "tag", "-a", rel.TagName(), "-m", message)
		recordGit(t, source.repo.Root, "tag", "-f", "-a", "latest-v1", "-m", message)
		aliasObject := recordGit(t, source.repo.Root, "rev-parse", "refs/tags/latest-v1")
		proof.Tags = []workspaceenv.NativeTagRef{
			{Name: rel.TagName(), Object: recordGit(t, source.repo.Root, "rev-parse", "refs/tags/"+rel.TagName()), Commit: proof.NewHead},
			{Name: "latest-v1", Object: aliasObject, Commit: proof.NewHead, Moving: true},
		}
		require.NoError(t, workspaceenv.CompleteNativeTransition(scope, token, proof))
		return proof.NewHead, aliasObject
	}
	firstHead, firstAlias := write(lib, source.expectedHead, "fix(lib): first native release")
	lastHead, lastAlias := write(&tool, firstHead, "fix(tool): second native release")
	recordGit(t, source.repo.Root, "update-ref", "refs/tags/latest-v1", firstAlias)
	err := w.verifySnapshot(t.Context(), lib)
	require.ErrorContains(t, err, "E330")
	assert.NotEqual(t, lastHead, source.expectedHead)
	recordGit(t, source.repo.Root, "update-ref", "refs/tags/latest-v1", lastAlias)
	require.NoError(t, w.verifySnapshot(t.Context(), lib))
	assert.Equal(t, lastHead, source.expectedHead)
}

func TestNativeEvidenceOmitsAnAdvisoryAliasThatDidNotMove(t *testing.T) {
	w, rel := recordFixture(t, false, false)
	rel.Pkg.Space.AliasTags = []model.AliasTag{{Format: "latest-v{major}", Moving: true, Force: true}}
	source := w.byName["source"]
	recordGit(t, source.repo.Root, "tag", "-a", "latest-v1", "-m", "older alias")
	recordGit(t, source.repo.Root, "commit", "--allow-empty", "-qm", "fix(lib): next native release")
	head := recordGit(t, source.repo.Root, "rev-parse", "HEAD")
	recordGit(t, source.repo.Root, "tag", "-a", rel.TagName(), "-m", "required release tag")
	refs, err := nativeTags(t.Context(), nativeTagQuery{git: source.git, release: rel, tag: rel.TagName(), head: head})
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, rel.TagName(), refs[0].Name)
}
