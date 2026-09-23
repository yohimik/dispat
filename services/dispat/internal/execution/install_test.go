// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Putting a captured set back, against a real file system.
//
// What installation promises is a boundary rather than a copy: either the
// consumer's folder holds the whole set or it holds what it held before. The
// scenarios below are the ways that promise can be broken, so each of them
// breaks something and then asks what the destination looks like.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// install runs the operation under test into a destination of the caller's.
func (f *outputFixture) install(t *testing.T, manifest *OutputManifest, into string) error {
	t.Helper()
	return InstallOutputs(context.Background(), InstallRequest{
		Git: f.git, Manifest: manifest, Dir: into,
		Staging: filepath.Join(t.TempDir(), stagingDirName, manifest.Package),
		Log:     zerolog.Nop(),
	})
}

// Two package names that collapse to the same path word must never share a
// staging directory. The files are installed simultaneously to exercise the
// cleanup of one install while the other is still reading its bytes.
func TestOutputStagingSeparatesCollidingPackageNames(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/value.txt", "kept\n", 0o644)
	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	index, err := fixture.git.IndexPath(context.Background())
	require.NoError(t, err)
	// Both Unicode names became "-" under the old lossy path mapper.
	destinations := []string{t.TempDir(), t.TempDir()}
	staging := make([]string, 2)
	for i, name := range []string{"α", "β"} {
		staging[i], err = resolveOutputStagingPath(outputStagingSpec{
			indexPath: index, ownerDir: fixture.dir, destination: destinations[i],
			run: "same-run", packageName: name,
		})
		require.NoError(t, err)
		assert.Equal(t, filepath.Dir(index), filepath.Dir(staging[i]),
			"same-device checkouts keep their private Git staging")
	}
	assert.NotEqual(t, staging[0], staging[1])
	start := make(chan struct{})
	finished := make(chan error, 2)
	for i := range staging {
		request := InstallRequest{Git: fixture.git, Manifest: manifest,
			Dir: destinations[i], Staging: staging[i], Log: zerolog.Nop()}
		go func() {
			<-start
			finished <- InstallOutputs(context.Background(), request)
		}()
	}
	close(start)
	for range staging {
		require.NoError(t, <-finished)
	}
	for i := range staging {
		assert.Equal(t, "kept\n", readInstalled(t, destinations[i], "dist/value.txt"))
		assert.NoDirExists(t, staging[i])
	}
	long, err := resolveOutputStagingPath(outputStagingSpec{
		indexPath: index, ownerDir: fixture.dir, destination: destinations[0],
		run: "same-run", packageName: strings.Repeat("α", 300),
	})
	require.NoError(t, err)
	assert.Less(t, len(filepath.Base(long)), 255, "one path component stays bounded")
	require.NoError(t, stageOutputs(context.Background(), InstallRequest{
		Git: fixture.git, Manifest: manifest, Dir: destinations[0],
		Staging: long, Log: zerolog.Nop(),
	}))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(long)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
			"staged bytes stay private even under a shared sibling directory")
	}
	require.NoError(t, os.RemoveAll(long))
}

// TestInstallPutsBackWhatWasCaptured: every kind of entry survives the round
// trip, with its content, its mode and its target intact, which is the whole
// claim a transfer makes.
func TestInstallPutsBackWhatWasCaptured(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "console.log(1)\n", 0o644)
	fixture.write(t, "dist/run.sh", "#!/bin/sh\n", 0o755)
	fixture.write(t, "dist/empty.txt", "", 0o644)
	fixture.write(t, "dist/nested/deep/one.txt", "deep\n", 0o644)
	fixture.write(t, "dist/ünï code.txt", "wide\n", 0o644)
	require.NoError(t, os.Symlink("app.js", filepath.Join(fixture.pkgDir, "dist", "latest.js")))
	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	into := t.TempDir()

	require.NoError(t, fixture.install(t, manifest, into))

	assert.Equal(t, "console.log(1)\n", readInstalled(t, into, "dist/app.js"))
	assert.Equal(t, "deep\n", readInstalled(t, into, "dist/nested/deep/one.txt"))
	assert.Equal(t, "wide\n", readInstalled(t, into, "dist/ünï code.txt"))
	assert.Equal(t, "", readInstalled(t, into, "dist/empty.txt"))
	runnable, err := os.Stat(filepath.Join(into, "dist", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), runnable.Mode().Perm(), "the executable bit arrived")
	target, err := os.Readlink(filepath.Join(into, "dist", "latest.js"))
	require.NoError(t, err)
	assert.Equal(t, "app.js", target, "a link arrived as a link")
	assert.NoDirExists(t, filepath.Join(into, stagingDirName), "the staging folder is gone")
}

// readInstalled is one installed file's content.
func readInstalled(t *testing.T, into, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(into, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(content)
}

// TestInstallReplacesAnExistingRootWhole: what a consumer had under a declared
// root is gone when the set is installed, files the new set does not have
// included, because a root travels whole.
func TestInstallReplacesAnExistingRootWhole(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "new\n", 0o644)
	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "dist", "stale.js"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(into, "dist", "app.js"), []byte("old\n"), 0o644))

	require.NoError(t, fixture.install(t, manifest, into))

	assert.Equal(t, "new\n", readInstalled(t, into, "dist/app.js"))
	assert.NoFileExists(t, filepath.Join(into, "dist", "stale.js"),
		"the previous run's file is not left beside the new one")
	assert.Empty(t, asideLeftovers(t, into), "and the folder it was moved to is gone")
}

// TestInstallReplacesARootTheBuildLeftEmpty: a build that produced nothing
// under a declared root still replaces what the consumer had there, because
// the alternative is a consumer building against the previous run's bytes.
func TestInstallReplacesARootTheBuildLeftEmpty(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	require.NoError(t, os.MkdirAll(filepath.Join(fixture.pkgDir, "dist"), 0o755))
	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	require.Empty(t, manifest.Entries)
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "dist", "stale.js"), []byte("old\n"), 0o644))

	require.NoError(t, fixture.install(t, manifest, into))

	assert.NoFileExists(t, filepath.Join(into, "dist", "stale.js"))
	assert.DirExists(t, filepath.Join(into, "dist"), "the root itself is there and is empty")
}

// TestInstallRefusesBytesThatAreNotWhatWasPromised: a digest that does not
// match what arrived stops the installation where it is, so the destination is
// exactly what it was and the staging folder is gone.
func TestInstallRefusesBytesThatAreNotWhatWasPromised(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(manifest *OutputManifest)
		want   OutputReason
	}{
		"a content digest nothing hashes to": {
			change: func(m *OutputManifest) { m.Entries[0].SHA256 = digestOf("something else") },
			want:   ReasonContentDigest,
		},
		"a tree the store does not hold": {
			change: func(m *OutputManifest) {
				m.OutputTree = "0000000000000000000000000000000000000000"
			},
			want: ReasonBytesMissing,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputFixture(t, "packages/core")
			fixture.write(t, "dist/app.js", "new\n", 0o644)
			manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
			require.NoError(t, err)
			tc.change(manifest)
			into := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(into, "dist"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(into, "dist", "app.js"), []byte("old\n"), 0o644))

			err = fixture.install(t, manifest, into)

			assert.Equal(t, tc.want, OutputFaultReason(err), "%v", err)
			assert.Equal(t, "old\n", readInstalled(t, into, "dist/app.js"),
				"the destination is exactly what it was")
			assert.Empty(t, asideLeftovers(t, into), "and nothing was moved aside and left")
		})
	}
}

// TestInstallPutsBackTheRootItReplaced: a rename that fails after the previous
// folder was moved out of the way puts it back, so a failed transfer never
// costs a consumer the inputs it already had.
//
// The second root is what fails: its staged folder is removed behind the
// installation's back, which is the one thing that makes os.Rename fail
// without making the whole file system unusable.
func TestInstallPutsBackTheRootItReplaced(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "new\n", 0o644)
	fixture.write(t, "assets/logo.svg", "new\n", 0o644)
	manifest, err := fixture.capture(t, []string{"dist", "assets"}, testLimits)
	require.NoError(t, err)
	into := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "dist", "app.js"), []byte("old\n"), 0o644))
	// A destination that cannot be replaced: a folder whose parent is not
	// writable is one os.Rename refuses to move anything into.
	blocked := filepath.Join(into, "assets")
	require.NoError(t, os.MkdirAll(blocked, 0o755))
	require.NoError(t, os.Chmod(into, 0o555))
	t.Cleanup(func() { _ = os.Chmod(into, 0o755) })

	err = fixture.install(t, manifest, into)

	require.Error(t, err)
	require.NoError(t, os.Chmod(into, 0o755))
	assert.Equal(t, "old\n", readInstalled(t, into, "dist/app.js"),
		"the root that could not be replaced still holds what it held")
	assert.Empty(t, asideLeftovers(t, into))
}

// TestInstallRestoresEarlierRootsWhenALaterRootFails: one output set is one
// prerequisite. If the second root cannot be installed after the first moved,
// the consumer must still see its original complete set.
func TestInstallRestoresEarlierRootsWhenALaterRootFails(t *testing.T) {
	into := t.TempDir()
	staging := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(into, "a-dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(into, "a-dist", "old.txt"), []byte("old\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "a-dist"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "a-dist", "new.txt"), []byte("new\n"), 0o644))
	// z-assets is declared but its staged root has disappeared between
	// verification and installation, as a concurrent filesystem change can.
	request := InstallRequest{Dir: into, Staging: staging,
		Manifest: &OutputManifest{Roots: []string{"a-dist", "z-assets"}}, Log: zerolog.Nop()}

	err := replaceOutputRoots(request)

	require.ErrorContains(t, err, "z-assets")
	assert.Equal(t, "old\n", readInstalled(t, into, "a-dist/old.txt"))
	assert.NoFileExists(t, filepath.Join(into, "a-dist", "new.txt"))
	assert.Empty(t, asideLeftovers(t, into), "rollback removed all temporary root names")
}

// A nested declared root must stay under the consuming checkout even when a
// previous run left a link at one of its parent components.
func TestInstallRefusesLinkedDestinationParent(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "assets/nested/new.txt", "new\n", 0o644)
	manifest, err := fixture.capture(t, []string{"assets/nested"}, testLimits)
	require.NoError(t, err)
	into := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(into, "assets")))

	err = fixture.install(t, manifest, into)

	assert.Equal(t, ReasonDestinationComponent, OutputFaultReason(err), "%v", err)
	assert.NoDirExists(t, filepath.Join(outside, "nested"))
	assert.Empty(t, asideLeftovers(t, into))
}

// asideLeftovers is every folder an interrupted installation would have left
// beside a declared root.
func asideLeftovers(t *testing.T, into string) []string {
	t.Helper()
	var left []string
	entries, err := os.ReadDir(into)
	require.NoError(t, err)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), asidePrefix) {
			left = append(left, entry.Name())
		}
	}
	return left
}

// TestStagedPathsAreNeverWrittenThroughSomethingElse: the walk that proves
// every component of a staged path is a folder, asked directly.
//
// Directly because there is no manifest that reaches it. A git tree cannot
// hold a blob and a tree under one name, the staging folder is cleared before
// anything is written into it, every folder in it is created by the
// installation itself, and links are created after every file exists; the
// check is what keeps all four of those true rather than assumed, so it is
// tested as what it is.
func TestStagedPathsAreNeverWrittenThroughSomethingElse(t *testing.T) {
	staging := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(staging, "fine"), 0o755))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(staging, "elsewhere")))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "file"), []byte("x"), 0o644))

	resolved, err := resolveStagedPath(staging, "fine/deeper/app.js")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(staging, "fine", "deeper", "app.js"), resolved)

	for name, carried := range map[string]string{
		"a component that is a link": "elsewhere/app.js",
		"a component that is a file": "file/app.js",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveStagedPath(staging, carried)

			assert.Equal(t, ReasonStagedComponent, OutputFaultReason(err), "%v", err)
		})
	}
}
