// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// recordsFixture is a checkout with a bare remote it records to: the two
// stores the comparison holds against each other, with a helper for each way
// they can disagree.
type recordsFixture struct {
	t     *testing.T
	root  string
	bare  string
	store releaseStore
}

func newRecordsFixture(t *testing.T, packages []*model.Package) *recordsFixture {
	t.Helper()
	root := t.TempDir()
	bare := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run(bare, "init", "-q", "--bare", ".")
	run(bare, "symbolic-ref", "HEAD", "refs/heads/main")
	run(root, "init", "-q")
	run(root, "symbolic-ref", "HEAD", "refs/heads/main")
	run(root, "config", "user.email", "test@example.com")
	run(root, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644))
	run(root, "add", ".")
	run(root, "commit", "-qm", "feat(core): initial")
	run(root, "remote", "add", "origin", bare)
	run(root, "push", "-q", "origin", "main")
	return &recordsFixture{t: t, root: root, bare: bare, store: releaseStore{
		git:      &gitx.LocalGitx{Dir: root, Log: zerolog.Nop()},
		remote:   "origin",
		commit:   &config.CommitConfig{Enabled: models.Bool(true), Push: true},
		packages: packages,
		log:      zerolog.Nop(),
	}}
}

func (f *recordsFixture) git(args ...string) string {
	f.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.root}, args...)...).CombinedOutput()
	require.NoError(f.t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// recordOnRemoteOnly writes a tag, pushes it and deletes the local copy: the
// clone that never fetched the record another run wrote.
func (f *recordsFixture) recordOnRemoteOnly(name, target string) {
	f.t.Helper()
	f.git("tag", "-a", name, "-m", "release "+name, target)
	f.git("push", "-q", "origin", name)
	f.git("tag", "-d", name)
}

func (f *recordsFixture) commitEmpty(message string) string {
	f.t.Helper()
	f.git("commit", "-q", "--allow-empty", "-m", message)
	return f.git("rev-parse", "HEAD")
}

// compare runs the whole decision the release path makes: which stores have
// anything to compare, and then the comparison itself.
func (f *recordsFixture) compare() error {
	f.t.Helper()
	stores := comparingStores([]releaseStore{f.store})
	if len(stores) == 0 {
		return nil
	}
	return stores[0].compare(context.Background(), plan.NewAliasFilter(f.store.packages))
}

// recordsPackages is one package in one space, named and formatted the way a
// configuration would produce.
func recordsPackages(aliases ...model.AliasTag) []*model.Package {
	space := &model.Space{Name: "libs", TagFormat: "{name}@{version}", AliasTags: aliases}
	return []*model.Package{{Name: "core", Dir: "packages/core", Space: space}}
}

// TestReleaseStoreComparison is §13.2's decision table: what each shape of
// difference between the store's records and the planning input answers,
// stated once, against a real checkout and a real remote.
func TestReleaseStoreComparison(t *testing.T) {
	t.Run("a record the checkout lacks on a commit it reaches is incomplete history", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")

		err := f.compare()
		require.Error(t, err)
		assert.Equal(t, plan.CodeShallowRepository, config.DiagnosticCode(err))
		assert.Contains(t, err.Error(), "core@0.1.0", "the record is named")
		assert.Contains(t, err.Error(), "git fetch --tags", "with the remedy")
	})

	t.Run("a record at a newer head than the checkout's tag is still incomplete history", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")
		f.commitEmpty("feat(core): later work")

		err := f.compare()
		require.Error(t, err, "the recorded commit is an ancestor of the new head")
		assert.Equal(t, plan.CodeShallowRepository, config.DiagnosticCode(err))
	})

	t.Run("a record off the checkout's history changes no plan", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.git("checkout", "-q", "-b", "elsewhere")
		unrelated := f.commitEmpty("feat(core): on another branch")
		f.recordOnRemoteOnly("core@0.1.0", unrelated)
		f.git("checkout", "-q", "main")

		require.NoError(t, f.compare(),
			"a commit the planned head does not reach cannot affect the plan")
	})

	t.Run("a record the checkout holds at the same commit agrees", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.git("tag", "-a", "core@0.1.0", "-m", "release core@0.1.0")
		f.git("push", "-q", "origin", "core@0.1.0")

		require.NoError(t, f.compare())
	})

	t.Run("an annotation rewritten over the same commit still agrees", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.git("tag", "-a", "core@0.1.0", "-m", "release core@0.1.0")
		f.git("push", "-q", "origin", "core@0.1.0")
		f.git("tag", "-d", "core@0.1.0")
		f.git("tag", "-a", "core@0.1.0", "-m", "annotated again")

		require.NoError(t, f.compare(),
			"a record is the commit it names, not the tag object that names it")
	})

	t.Run("a record the checkout holds elsewhere is one version at two commits", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")
		moved := f.commitEmpty("feat(core): later work")
		f.git("tag", "-a", "core@0.1.0", "-m", "the same release, elsewhere")

		err := f.compare()
		require.Error(t, err)
		assert.Equal(t, plan.CodeDuplicateVersionTag, config.DiagnosticCode(err))
		assert.Contains(t, err.Error(), moved, "both commits are named")
		assert.Contains(t, err.Error(), "never moved")
	})

	t.Run("a moving alias is not a record", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages(model.AliasTag{Format: "v{major}", Moving: true, Force: true}))
		f.recordOnRemoteOnly("v1", "HEAD")

		require.NoError(t, f.compare(), "an alias exists to move and records nothing")
	})

	t.Run("the release lock is not a record", func(t *testing.T) {
		// The broadest format a space can carry: every name on the remote has
		// its shape, the coordination refs included.
		space := &model.Space{Name: "libs", TagFormat: "{version}"}
		f := newRecordsFixture(t, []*model.Package{{Name: "core", Space: space}})
		f.recordOnRemoteOnly(gitx.LockTagName, "HEAD")
		f.recordOnRemoteOnly(gitx.LockAttemptTagPrefix+"abcdef", "HEAD")

		require.NoError(t, f.compare(), "a run's own coordination refs are not releases")
	})

	t.Run("somebody else's tag is not a record", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("other@0.1.0", "HEAD")
		f.recordOnRemoteOnly("v2026.09", "HEAD")

		require.NoError(t, f.compare(), "only the workspace's own formats name records")
	})

	t.Run("a name with no version in it records no release", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@backup", "HEAD")

		require.NoError(t, f.compare(),
			"the planner reads no baseline and no duplicate out of it either")
	})
}

// TestReleaseStoreComparisonIsSkipped covers the three runs that have nothing
// to compare, each of which must reach no remote at all: the comparison would
// otherwise change what a repository without a store, without packages or
// without ls-remote access is allowed to do.
func TestReleaseStoreComparisonIsSkipped(t *testing.T) {
	t.Run("a run that pushes nothing records in its own repository", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")
		f.store.commit = &config.CommitConfig{Enabled: models.Bool(true)}

		require.NoError(t, f.compare())
	})

	t.Run("commit.verify off keeps its exemption and says so", func(t *testing.T) {
		f := newRecordsFixture(t, recordsPackages())
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")
		f.store.commit = &config.CommitConfig{
			Enabled: models.Bool(true), Push: true, Verify: models.Bool(false)}
		var said bytes.Buffer
		f.store.log = zerolog.New(&said)

		require.NoError(t, f.compare(),
			"the same ls-remote the up-front checks are excused from")
		assert.Contains(t, said.String(), "\"level\":\"warn\"",
			"an engine that forgoes the read may not read as though it had compared")
		assert.Contains(t, said.String(), "release records are not compared")
	})

	t.Run("a repository owning no package has no record namespace", func(t *testing.T) {
		f := newRecordsFixture(t, nil)
		f.recordOnRemoteOnly("core@0.1.0", "HEAD")

		require.NoError(t, f.compare())
	})
}

// TestReleaseStoreUnreadableStoreRefuses: a store that cannot be read is a
// plan nothing checked, which is the answer a failed remote verification
// already gives.
func TestReleaseStoreUnreadableStoreRefuses(t *testing.T) {
	f := newRecordsFixture(t, recordsPackages())
	f.store.remote = "nowhere"

	err := f.compare()
	require.Error(t, err)
	assert.Equal(t, plan.CodeShallowRepository, config.DiagnosticCode(err))
	assert.Contains(t, err.Error(), "cannot read the release records")
}

// TestReleaseStorePrefixNamesTheRepository: a composed workspace refuses in
// the name of one repository, because the remedy is applied in that clone.
func TestReleaseStorePrefixNamesTheRepository(t *testing.T) {
	f := newRecordsFixture(t, recordsPackages())
	f.store.repository = "tools"
	f.recordOnRemoteOnly("core@0.1.0", "HEAD")

	err := f.compare()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository tools:")
}
