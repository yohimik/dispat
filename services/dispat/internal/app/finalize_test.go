package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// TestAppendIncludeDirsWarnsOnAMissingPath: a commit.include path that names
// nothing on disk is skipped with W227 — staged silence would cost the
// release commit its artifact on every release until a human noticed.
func TestAppendIncludeDirsWarnsOnAMissingPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "present.txt"), []byte("x"), 0o644))
	var logs bytes.Buffer
	a := New(root, &config.File{}, zerolog.New(&logs))

	dirs := a.appendIncludeDirs(nil, []string{"present.txt", "ghost.lock"})

	require.Len(t, dirs, 1, "the present path is staged")
	assert.Contains(t, dirs[0], "present.txt")
	assert.Contains(t, logs.String(), plan.CodeCommitIncludeMissing)
	assert.Contains(t, logs.String(), "ghost.lock", "the warning names the path")
}

// TestConflictBranchNamesTheReleaseAndTheMoment: the branch a conflict's other
// side is kept on has to say what it belongs to, so it carries what the leg
// released, and it has to be safe to push, so it carries when. The releases
// make it readable and the timestamp makes it unique.
func TestConflictBranchNamesTheReleaseAndTheMoment(t *testing.T) {
	at := time.Date(2026, 9, 2, 5, 30, 12, 0, time.UTC)
	rels := []*plan.Release{
		{Pkg: &model.Package{Name: "core"}, Next: ccme.Version{Minor: 1}},
		{Pkg: &model.Package{Name: "app"}, Next: ccme.Version{Patch: 1}},
	}
	assert.Equal(t, "release-conflicts/core-0.1.0-app-0.0.1-20260902-053012",
		conflictBranch(rels, at), "the leg's own order, then the moment")

	// A package git would not take in a ref name still gets a branch: the
	// timestamp identifies the run on its own, and a name nobody can push is
	// worse than one nobody can guess.
	odd := []*plan.Release{{Pkg: &model.Package{Name: "we?rd"}, Next: ccme.Version{Minor: 1}}}
	assert.Equal(t, "release-conflicts/20260902-053012", conflictBranch(odd, at))
	assert.NoError(t, gitx.ValidRefName(conflictBranch(odd, at)))
}
