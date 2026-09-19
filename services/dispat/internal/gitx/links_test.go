// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const linkPath = ".links/sdk"

// linkRepo is a repository holding one fleet link whose checkout was never
// made: a declaration in `.gitmodules`, a pin in the index and the empty
// folder that keeps Git from calling the link deleted.
func linkRepo(t *testing.T) (string, *LocalGitx, string) {
	t.Helper()
	root, git := initRepo(t)
	peer := polyrepoGit(t, root, "rev-parse", "HEAD")
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(linkPath)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitmodules"),
		[]byte("[submodule \"sdk\"]\n\tpath = "+linkPath+"\n\turl = ../sdk\n"), 0o644))
	polyrepoGit(t, root, "add", ".gitmodules")
	polyrepoGit(t, root, "update-index", "--add", "--cacheinfo", "160000", peer, linkPath)
	polyrepoGit(t, root, "commit", "-qm", "chore: link sdk")
	git.Name, git.Email = "Release Bot", "bot@example.com"
	git.LinkPaths = []string{linkPath}
	return root, git, peer
}

// TestLinkPathsAreExcludedFromTheReleasesOwnWork: a link's pin is advisory
// between settlements, so an advisory move must not make the repository look
// dirty, must not enter a release commit, and must survive a revert.
func TestLinkPathsAreExcludedFromTheReleasesOwnWork(t *testing.T) {
	root, git, peer := linkRepo(t)
	before := polyrepoGit(t, root, "rev-parse", "HEAD")
	// The advisory pin: any commit object will do, and this one is not what
	// the link's last settlement recorded, which is the whole point.
	advisory := before
	polyrepoGit(t, root, "update-index", "--add", "--cacheinfo", "160000", advisory, linkPath)
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("released"), 0o644))

	dirty, err := git.DirtyPaths(t.Context(), []string{root})
	require.NoError(t, err)
	assert.Equal(t, []string{"packages/core/main.txt"}, dirty, "the advisory pin is not the release's business")

	created, err := git.CommitDirs(t.Context(), []string{root}, "chore(release): core@1.0.0")
	require.NoError(t, err)
	assert.True(t, created)
	tree := polyrepoGit(t, root, "ls-tree", "-r", "HEAD")
	assert.Contains(t, tree, peer, "the release commit records the settled pin, not the advisory one")
	assert.Equal(t, advisory, pinOf(t, root), "the advisory pin stays in the index")

	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("rolled back"), 0o644))
	require.NoError(t, git.RevertDir(t.Context(), root))
	content, err := os.ReadFile(filepath.Join(root, "packages", "core", "main.txt"))
	require.NoError(t, err)
	assert.Equal(t, "released", string(content))
	assert.Equal(t, advisory, pinOf(t, root), "a revert does not withdraw an advisory pin either")
	assert.NotEqual(t, before, polyrepoGit(t, root, "rev-parse", "HEAD"))

	// Without link paths the very same calls behave exactly as they always
	// have: the pin is dirty, and it is committed.
	plain := &LocalGitx{Dir: root, Name: "Release Bot", Email: "bot@example.com"}
	dirty, err = plain.DirtyPaths(t.Context(), []string{root})
	require.NoError(t, err)
	assert.Equal(t, []string{linkPath}, dirty)
}

func pinOf(t *testing.T, root string) string {
	t.Helper()
	entry := polyrepoGit(t, root, "ls-files", "-s", "--", linkPath)
	require.NotEmpty(t, entry)
	return entry[7:47]
}

// TestGitlinksAtPathsReadsExactlyWhatWasAsked: a boundary walk reads one hop
// at a time, and a path the revision holds no link at is an answer rather
// than a failure.
func TestGitlinksAtPathsReadsExactlyWhatWasAsked(t *testing.T) {
	root, git, peer := linkRepo(t)
	links, err := git.GitlinksAtPaths(t.Context(), "", []string{linkPath, "packages/core"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{linkPath: peer}, links)

	empty, err := git.GitlinksAtPaths(t.Context(), "HEAD", nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	first := polyrepoGit(t, root, "rev-list", "--max-parents=0", "HEAD")
	links, err = git.GitlinksAtPaths(t.Context(), first, []string{linkPath})
	require.NoError(t, err)
	assert.Empty(t, links, "a link added later is absent from the revision before it")

	_, err = git.GitlinksAtPaths(t.Context(), "no-such-revision", []string{linkPath})
	assert.Error(t, err)
}

// TestCommitSubjectsReadsEveryRevisionInOneCall: cross-repository evidence is
// a subject line, and a walk must not pay one Git process per hop for it.
func TestCommitSubjectsReadsEveryRevisionInOneCall(t *testing.T) {
	root, git, _ := linkRepo(t)
	head := polyrepoGit(t, root, "rev-parse", "HEAD")
	first := polyrepoGit(t, root, "rev-list", "--max-parents=0", "HEAD")

	before := GitInvocations()
	subjects, err := git.CommitSubjects(t.Context(), []string{head, first})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), GitInvocations()-before, "one call for every revision")
	assert.Equal(t, map[string]string{head: "chore: link sdk", first: "feat(core): initial"}, subjects)

	empty, err := git.CommitSubjects(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = git.CommitSubjects(t.Context(), []string{head, "0000000000000000000000000000000000000000"})
	assert.Error(t, err, "a revision this repository does not hold is not an empty subject")
}

// TestCommitGitlinksRecordsPinsWithoutStagingTheWorktree: the link being
// recorded is usually a back-link nobody ever checked out, so the commit is
// built from the parent tree rather than from what is on disk.
func TestCommitGitlinksRecordsPinsWithoutStagingTheWorktree(t *testing.T) {
	root, git, peer := linkRepo(t)
	parent := polyrepoGit(t, root, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(root, "packages", "core", "main.txt"), []byte("unstaged work"), 0o644))

	same, created, err := git.CommitGitlinks(t.Context(), "chore(release): settle links", map[string]string{linkPath: peer})
	require.NoError(t, err)
	assert.False(t, created, "a pin the tree already carries needs no commit")
	assert.Equal(t, parent, same)

	polyrepoGit(t, root, "commit", "-q", "--allow-empty", "-m", "feat(core): the revision the peer released")
	target := polyrepoGit(t, root, "rev-parse", "HEAD")
	parent = target
	before := GitInvocations()
	revision, created, err := git.CommitGitlinks(t.Context(), "chore(release): settle links", map[string]string{linkPath: target})
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, uint64(8), GitInvocations()-before,
		"one settlement is: read HEAD, read the tree, read-tree, stage every pin, write-tree, commit-tree, update-ref, refresh the index")
	assert.Equal(t, revision, polyrepoGit(t, root, "rev-parse", "HEAD"))
	assert.Equal(t, parent, polyrepoGit(t, root, "rev-parse", "HEAD~1"))
	assert.Equal(t, "refs/heads/"+polyrepoGit(t, root, "rev-parse", "--abbrev-ref", "HEAD"),
		polyrepoGit(t, root, "symbolic-ref", "HEAD"), "a branch checkout stays on its branch")
	assert.Equal(t, "Release Bot <bot@example.com>",
		polyrepoGit(t, root, "log", "-1", "--format=%an <%ae>"), "the configured identity signs the settlement")
	links, err := git.GitlinksAtPaths(t.Context(), revision, []string{linkPath})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{linkPath: target}, links)
	assert.Equal(t, target, pinOf(t, root), "the repository index is left holding what was recorded")
	assert.Equal(t, []string{"packages/core/main.txt"},
		mustDirty(t, git), "unstaged work is neither committed nor reverted")

	head, created, err := git.CommitGitlinks(t.Context(), "chore(release): settle links", nil)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, revision, head)
}

func mustDirty(t *testing.T, git *LocalGitx) []string {
	t.Helper()
	dirty, err := git.DirtyPaths(t.Context(), []string{git.Dir})
	require.NoError(t, err)
	return dirty
}

// TestCommitGitlinksMovesADetachedHeadAndRefusesAConcurrentWriter: a link
// checkout is usually detached, and two settlements racing for the same
// repository must not overwrite one another.
func TestCommitGitlinksMovesADetachedHeadAndRefusesAConcurrentWriter(t *testing.T) {
	root, git, _ := linkRepo(t)
	polyrepoGit(t, root, "commit", "-q", "--allow-empty", "-m", "feat(core): the revision the peer released")
	target := polyrepoGit(t, root, "rev-parse", "HEAD")
	polyrepoGit(t, root, "checkout", "-q", "--detach")
	parent := polyrepoGit(t, root, "rev-parse", "HEAD")

	revision, created, err := git.CommitGitlinks(t.Context(), "chore(release): settle links", map[string]string{linkPath: target})
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, revision, polyrepoGit(t, root, "rev-parse", "HEAD"))
	_, err = git.run(t.Context(), "symbolic-ref", "HEAD")
	assert.Error(t, err, "a detached checkout stays detached")

	// A writer that moved HEAD between the read and the write is refused
	// rather than overwritten: the compare-and-swap names both revisions.
	blocked := &blockingGitx{LocalGitx: git, root: root, parent: parent}
	assert.Error(t, blocked.settle(t, target))
}

// blockingGitx moves HEAD after the settlement has read it, which is the race
// the compare-and-swap exists for.
type blockingGitx struct {
	*LocalGitx
	root   string
	parent string
}

func (b *blockingGitx) settle(t *testing.T, target string) error {
	t.Helper()
	// Recording against a stale parent is exactly what a concurrent writer
	// leaves behind, and `update-ref` is asked to prove the parent is current.
	tree := polyrepoGit(t, b.root, "rev-parse", "HEAD^{tree}")
	revision := polyrepoGit(t, b.root, "commit-tree", tree, "-p", b.parent, "-m", "chore(release): stale")
	_, err := b.run(t.Context(), "update-ref", "-m", "stale", "HEAD", revision, b.parent)
	return err
}

// TestAddSubmoduleAndGitlinkDeclareBothHalvesOfALink: one half is cloned, the
// other is only declared, and neither may publish a credential.
func TestAddSubmoduleAndGitlinkDeclareBothHalvesOfALink(t *testing.T) {
	peerRoot, peer := initRepo(t)
	polyrepoGit(t, peerRoot, "branch", "-M", "main")
	peerHead, err := peer.HeadSHA(t.Context())
	require.NoError(t, err)

	root, git := initRepo(t)
	polyrepoGit(t, root, "branch", "-M", "main")
	git.Name, git.Email = "Release Bot", "bot@example.com"
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	forward := Submodule{Name: "sdk", URL: peerRoot, Path: linkPath, Branch: "main"}
	require.NoError(t, git.AddSubmodule(t.Context(), forward))
	assert.Equal(t, peerHead, pinOf(t, root))
	assert.Equal(t, "main", polyrepoGit(t, root, "config", "--file", ".gitmodules", "submodule.sdk.branch"))
	polyrepoGit(t, root, "commit", "-qm", "chore: link sdk")

	// The peer declares the other half without fetching anything: a pin, a
	// declaration and the empty folder that keeps its checkout clean.
	back := Submodule{Name: "core", URL: root, Path: ".links/core", Branch: "main"}
	require.NoError(t, peer.AddGitlink(t.Context(), back, polyrepoGit(t, root, "rev-parse", "HEAD")))
	assert.DirExists(t, filepath.Join(peerRoot, ".links", "core"))
	assert.Equal(t, ".links/core", polyrepoGit(t, peerRoot, "config", "--file", ".gitmodules", "submodule.core.path"))
	staged := polyrepoGit(t, peerRoot, "diff", "--cached", "--name-only")
	assert.Contains(t, staged, ".gitmodules")
	assert.Contains(t, staged, ".links/core")
	polyrepoGit(t, peerRoot, "commit", "-qm", "chore: link core")
	assert.Empty(t, polyrepoGit(t, peerRoot, "status", "--porcelain=v1"),
		"an unpopulated link with its folder present leaves the repository clean")

	// The link the peer never checked out can still be materialized, and the
	// copy of this repository inside it stays unpopulated.
	require.NoError(t, peer.InitSubmodule(t.Context(), ".links/core"))
	assert.FileExists(t, filepath.Join(peerRoot, ".links", "core", ".gitmodules"))
	nested := polyrepoGit(t, peerRoot, "-C", filepath.Join(peerRoot, ".links", "core"), "submodule", "status")
	assert.True(t, len(nested) > 0 && nested[0] == '-', "the nested link is declared and not initialized: %q", nested)

	credential := Submodule{Name: "leak", URL: "https://user:secret@example.test/sdk.git", Path: ".links/leak"}
	assert.ErrorContains(t, git.AddSubmodule(t.Context(), credential), "carries user information")
	assert.ErrorContains(t, git.AddGitlink(t.Context(), credential, peerHead), "carries user information")
	assert.NotContains(t, git.AddGitlink(t.Context(), credential, peerHead).Error(), "secret")
}

// TestCommitGitlinksLeavesTheRepositoryAloneWhenItCannotRecord: a settlement
// that fails must fail before the reference moves, so a retry starts from the
// same place rather than from half a record.
func TestCommitGitlinksLeavesTheRepositoryAloneWhenItCannotRecord(t *testing.T) {
	root, git, _ := linkRepo(t)
	head := polyrepoGit(t, root, "rev-parse", "HEAD")

	_, created, err := git.CommitGitlinks(t.Context(), "chore(release): settle links",
		map[string]string{linkPath: "not-a-revision"})
	require.Error(t, err)
	assert.False(t, created)
	assert.Equal(t, head, polyrepoGit(t, root, "rev-parse", "HEAD"))

	_, _, err = git.CommitGitlinks(t.Context(), "chore(release): settle links",
		map[string]string{"packages/core/main.txt": head})
	require.Error(t, err, "a path the tree holds a file at is not a link")
	assert.Equal(t, head, polyrepoGit(t, root, "rev-parse", "HEAD"))
}

// TestWithoutLinksLeavesAnOrchestratedPathspecAlone: the exclusion is what an
// orchestrated release must never see, so it is asserted directly.
func TestWithoutLinksLeavesAnOrchestratedPathspecAlone(t *testing.T) {
	plain := &LocalGitx{Dir: "."}
	assert.Equal(t, []string{"packages/core"}, plain.withoutLinks([]string{"packages/core"}))

	linked := &LocalGitx{Dir: ".", LinkPaths: []string{"./.links//sdk", ""}}
	assert.Equal(t, []string{"packages/core", ":(exclude).links/sdk"},
		linked.withoutLinks([]string{"packages/core"}))
	assert.Equal(t, []string{":/", ":(exclude).links/sdk"}, linked.withoutLinks(nil),
		"an exclusion on its own names nothing to exclude from")
}
