// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// A sweep's outputs, from the three places they are handled: the capture on
// the node, which admits a root the script did not write as an empty one; the
// merge on the orchestrator, which writes one set's files into a folder other
// sets write into too; and the record that decides which sets are merged at
// all once two tasks disagree about a path.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// captureSweep is a sweep task's capture: the roots are relative to the
// repository root and a root the script did not write is admitted as empty.
func (f *outputFixture) captureSweep(t *testing.T, roots []string) (*OutputManifest, error) {
	t.Helper()
	return CaptureOutputs(context.Background(), CaptureRequest{
		Git: f.git, Dir: f.dir, Roots: roots, Limits: testLimits, IsAbsentRootEmpty: true,
		Manifest: OutputManifest{Run: "run-1", PlanDigest: "digest", Task: "core:run",
			Attempt: 1, Generation: "generation", Node: "build-a", Package: "core",
			Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"}},
	})
}

// writeAtRoot puts one file at a path relative to the repository root.
func (f *outputFixture) writeAtRoot(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(f.dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// mergeInto runs the merge under test into a destination of the caller's.
func (f *outputFixture) mergeInto(t *testing.T, manifest *OutputManifest, into string) error {
	t.Helper()
	return MergeInstallOutputs(context.Background(), InstallRequest{
		Git: f.git, Manifest: manifest, Dir: into,
		Staging: filepath.Join(t.TempDir(), stagingDirName, manifest.Package),
		Log:     zerolog.Nop(),
	})
}

// TestSweepCaptureAdmitsARootTheScriptDidNotWrite: a sweep's script may have
// nothing to say for a package, so an absent root is an empty set rather than
// a refusal (§28.10), beside a root it did write; and a set whose every root
// is absent is the empty tree. A build's capture still refuses the same
// absence.
func TestSweepCaptureAdmitsARootTheScriptDidNotWrite(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.writeAtRoot(t, "coverage/core.out", "mode: set\n")

	manifest, err := fixture.captureSweep(t, []string{"coverage/", "reports"})

	require.NoError(t, err)
	assert.Equal(t, []string{"coverage", "reports"}, manifest.Roots,
		"every declared root is named, the absent one included")
	require.Len(t, manifest.Entries, 1)
	assert.Equal(t, "coverage/core.out", manifest.Entries[0].Path,
		"the path is relative to the repository root")

	empty, err := fixture.captureSweep(t, []string{"reports"})
	require.NoError(t, err)
	assert.Empty(t, empty.Entries)
	assert.Equal(t, 0, empty.Files)

	_, err = fixture.capture(t, []string{"dist"}, testLimits)
	assert.Equal(t, ReasonRootAbsent, OutputFaultReason(err), "a build's absent root is still refused")
}

// TestMergeInstallKeepsWhatItDoesNotReplace: a merged set replaces the file of
// its own path and leaves every other file of the root where it was, because
// the root is shared by every task of the sweep.
func TestMergeInstallKeepsWhatItDoesNotReplace(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.writeAtRoot(t, "coverage/core.out", "new\n")
	fixture.writeAtRoot(t, "coverage/nested/deep.out", "deep\n")
	manifest, err := fixture.captureSweep(t, []string{"coverage"})
	require.NoError(t, err)
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "coverage"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "coverage", "core.out"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(into, "coverage", "other.out"), []byte("kept\n"), 0o644))

	require.NoError(t, fixture.mergeInto(t, manifest, into))

	assert.Equal(t, "new\n", readInstalled(t, into, "coverage/core.out"), "the file of its own path is replaced")
	assert.Equal(t, "deep\n", readInstalled(t, into, "coverage/nested/deep.out"), "a new folder is made")
	assert.Equal(t, "kept\n", readInstalled(t, into, "coverage/other.out"), "a sibling is kept")
	assert.Empty(t, asideLeftovers(t, filepath.Join(into, "coverage")), "nothing set aside is left")
	assert.NoDirExists(t, filepath.Join(into, stagingDirName))
}

// TestMergeInstallRefusesBytesThatAreNotWhatWasPromised: verification comes
// before the first file is moved, so a set whose bytes are not what its
// manifest says leaves the destination exactly as it was.
func TestMergeInstallRefusesBytesThatAreNotWhatWasPromised(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.writeAtRoot(t, "coverage/a.out", "new a\n")
	fixture.writeAtRoot(t, "coverage/b.out", "new b\n")
	manifest, err := fixture.captureSweep(t, []string{"coverage"})
	require.NoError(t, err)
	manifest.Entries[1].SHA256 = digestOf("something else")
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "coverage"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "coverage", "a.out"), []byte("old a\n"), 0o644))

	err = fixture.mergeInto(t, manifest, into)

	assert.Equal(t, ReasonContentDigest, OutputFaultReason(err), "%v", err)
	assert.Equal(t, "old a\n", readInstalled(t, into, "coverage/a.out"),
		"the file that did verify was not installed either")
	assert.NoFileExists(t, filepath.Join(into, "coverage", "b.out"))
}

// TestMergeInstallPutsBackWhatItAlreadyMoved: a move that fails after earlier
// files of the same set were moved takes those back and restores the files
// they replaced, so a set is installed whole or not at all.
func TestMergeInstallPutsBackWhatItAlreadyMoved(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.writeAtRoot(t, "coverage/a.out", "new a\n")
	fixture.writeAtRoot(t, "coverage/z/z.out", "new z\n")
	manifest, err := fixture.captureSweep(t, []string{"coverage"})
	require.NoError(t, err)
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "coverage", "z"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "coverage", "a.out"), []byte("old a\n"), 0o644))
	// A folder nobody may write into is one os.Rename refuses to move a file
	// into, which fails the second entry after the first was moved.
	require.NoError(t, os.Chmod(filepath.Join(into, "coverage", "z"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(into, "coverage", "z"), 0o755) })

	err = fixture.mergeInto(t, manifest, into)

	require.Error(t, err)
	assert.Equal(t, "old a\n", readInstalled(t, into, "coverage/a.out"),
		"the file moved in first was taken back and the one it replaced restored")
	assert.Empty(t, asideLeftovers(t, filepath.Join(into, "coverage")))
}

// TestMergeInstallNeverWritesThroughALink: a merge writes into folders it does
// not own, so a folder on the way that is a link, a file where a folder has to
// be, and a folder where the file has to go are all refused before anything
// is moved.
func TestMergeInstallNeverWritesThroughALink(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, into string){
		"a folder on the way that is a link": func(t *testing.T, into string) {
			require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(into, "coverage")))
		},
		"a file where a folder has to be": func(t *testing.T, into string) {
			require.NoError(t, os.WriteFile(filepath.Join(into, "coverage"), []byte("x"), 0o644))
		},
		"a folder where the file has to go": func(t *testing.T, into string) {
			require.NoError(t, os.MkdirAll(filepath.Join(into, "coverage", "core.out"), 0o755))
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputFixture(t, "packages/core")
			fixture.writeAtRoot(t, "coverage/core.out", "new\n")
			manifest, err := fixture.captureSweep(t, []string{"coverage"})
			require.NoError(t, err)
			into := t.TempDir()
			prepare(t, into)

			err = fixture.mergeInto(t, manifest, into)

			assert.Equal(t, ReasonDestinationComponent, OutputFaultReason(err), "%v", err)
		})
	}
}

// sweepSetOf is a set the record is asked about: one task's entries, merged
// into one repository.
func sweepSetOf(task, repository string, entries ...ManifestEntry) *sweepSet {
	return &sweepSet{task: task, packageName: task, repository: &gitx.LocalGitx{Dir: repository},
		manifest: &OutputManifest{Entries: entries, Files: len(entries)}}
}

// sweepFile is one entry naming a content.
func sweepFile(path, body string) ManifestEntry {
	return ManifestEntry{Path: path, Type: EntryFile, Mode: EntryModeFile, SHA256: digestOf(body)}
}

// TestSweepOutputsRefuseTwoTasksWritingOnePathApart: two tasks that name one
// path with different bytes both leave the merge, so the root holds neither
// file at that path; identical bytes are admitted once; two spellings of one
// path are one path; and a set that shares nothing with the disagreement is
// merged as it is.
func TestSweepOutputsRefuseTwoTasksWritingOnePathApart(t *testing.T) {
	record := newSweepOutputs()
	_, isConflicting := record.admit(sweepSetOf("a:run", "/repo", sweepFile("coverage/a.out", "a"),
		sweepFile("coverage/shared.txt", "same")))
	require.False(t, isConflicting)
	_, isConflicting = record.admit(sweepSetOf("b:run", "/repo", sweepFile("coverage/b.out", "b"),
		sweepFile("coverage/shared.txt", "same")))
	require.False(t, isConflicting, "identical bytes at one path agree")

	conflict, isConflicting := record.admit(sweepSetOf("c:run", "/repo", sweepFile("coverage/a.out", "c")))
	require.True(t, isConflicting)
	assert.Equal(t, sweepConflict{path: "coverage/a.out", first: "a:run"}, conflict)
	_, isConflicting = record.admit(sweepSetOf("d:run", "/repo", sweepFile("coverage/B.out", "b")))
	assert.True(t, isConflicting, "two spellings of one path are one file on a case-insensitive checkout")
	_, isConflicting = record.admit(sweepSetOf("e:run", "/other", sweepFile("coverage/a.out", "e")))
	assert.False(t, isConflicting, "the same path in another repository is another file")

	var merged []string
	for _, set := range record.collectMergeable() {
		merged = append(merged, set.task)
	}
	assert.Equal(t, []string{"e:run"}, merged,
		"every set naming a disputed path is left out whole, and only the undisputed one is merged")
	for task, want := range map[string]string{
		"a:run": OutputsRejected, "c:run": OutputsRejected, "e:run": OutputsAdmitted, "z:run": OutputsNone,
	} {
		outcome, _ := record.resolveOutcome(task)
		assert.Equal(t, want, outcome, task)
	}
	record.reject("e:run")
	outcome, manifest := record.resolveOutcome("e:run")
	assert.Equal(t, OutputsRejected, outcome)
	assert.NotNil(t, manifest)
}
