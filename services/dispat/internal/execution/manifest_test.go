// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Capturing, validating and installing build outputs against real
// repositories.
//
// Nothing here is faked. What a capture produces depends on what git does with
// an index, an ignore file and a mode bit, and what a validator has to refuse
// is exactly the trees a hostile writer could build with git plumbing, so the
// fixtures build them with git plumbing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// outputFixture is one repository holding a package whose build wrote files.
type outputFixture struct {
	dir     string
	pkgPath string
	pkgDir  string
	git     *gitx.LocalGitx
}

// newOutputFixture seeds a repository with one package folder, an ignore file
// that ignores exactly what a build writes, and nothing else.
func newOutputFixture(t *testing.T, pkgPath string) *outputFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	fixture := &outputFixture{dir: dir, pkgPath: pkgPath,
		pkgDir: filepath.Join(dir, filepath.FromSlash(pkgPath)),
		git:    &gitx.LocalGitx{Dir: dir, Log: zerolog.Nop()}}
	fixture.run(t, "init", "-q")
	fixture.run(t, "config", "user.email", "unit@dispat.test")
	fixture.run(t, "config", "user.name", "dispat unit")
	require.NoError(t, os.MkdirAll(fixture.pkgDir, 0o755))
	fixture.write(t, "main.txt", "source\n", 0o644)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\nassets/\n"), 0o644))
	fixture.run(t, "add", "-A")
	fixture.run(t, "commit", "-q", "-m", "seed")
	return fixture
}

func (f *outputFixture) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", f.dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// write puts one file inside the package folder, creating its folders.
func (f *outputFixture) write(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(f.pkgDir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), mode))
	require.NoError(t, os.Chmod(path, mode))
}

// capture is the operation under test, with the binding a real build would
// have filled in.
func (f *outputFixture) capture(t *testing.T, roots []string, limits TransferLimits) (*OutputManifest, error) {
	t.Helper()
	return CaptureOutputs(context.Background(), CaptureRequest{
		Git: f.git, Dir: f.pkgDir, PackagePath: f.pkgPath, Roots: roots, Limits: limits,
		Manifest: OutputManifest{Run: "run-1", PlanDigest: "digest", Task: "core:build",
			Attempt: 1, Generation: "generation", Node: "build-a", Package: "core", Version: "1.0.0",
			Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"}},
	})
}

// testLimits are ceilings no fixture reaches, for the scenarios that are not
// about ceilings.
var testLimits = TransferLimits{MaxFiles: 1000, MaxBytes: 1 << 30, MaxManifestBytes: 1 << 20}

// digestOf is the digest a manifest entry has to carry for one content.
func digestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// TestCaptureDescribesWhatTheBuildWrote: a capture is about the files a build
// left behind, whatever git would otherwise say about them: ignored files are
// in, the executable bit survives, a link inside the root travels as a link,
// an empty file is a file, and a name holding a space, a glob character or a
// character outside ASCII is the name it is.
func TestCaptureDescribesWhatTheBuildWrote(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "console.log(1)\n", 0o644)
	fixture.write(t, "dist/run.sh", "#!/bin/sh\n", 0o755)
	fixture.write(t, "dist/empty.txt", "", 0o644)
	fixture.write(t, "dist/a b?c*.txt", "globby\n", 0o644)
	fixture.write(t, "dist/ünïcode.txt", "wide\n", 0o644)
	require.NoError(t, os.Symlink("app.js", filepath.Join(fixture.pkgDir, "dist", "latest.js")))

	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)

	require.NoError(t, err)
	byPath := map[string]ManifestEntry{}
	for _, entry := range manifest.Entries {
		byPath[entry.Path] = entry
	}
	assert.Equal(t, EntryModeFile, byPath["dist/app.js"].Mode)
	assert.Equal(t, digestOf("console.log(1)\n"), byPath["dist/app.js"].SHA256)
	assert.Equal(t, EntryModeExecutable, byPath["dist/run.sh"].Mode, "the executable bit travels")
	assert.Equal(t, int64(0), byPath["dist/empty.txt"].Size, "an empty file is a file")
	assert.Equal(t, digestOf(""), byPath["dist/empty.txt"].SHA256)
	assert.Equal(t, EntryFile, byPath["dist/a b?c*.txt"].Type, "a glob character is a name")
	assert.Equal(t, EntryFile, byPath["dist/ünïcode.txt"].Type)
	assert.Equal(t, EntrySymlink, byPath["dist/latest.js"].Type)
	assert.Equal(t, "app.js", byPath["dist/latest.js"].Target)
	assert.Equal(t, digestOf("app.js"), byPath["dist/latest.js"].SHA256,
		"a link is digested over the target it holds")
	assert.Equal(t, 6, manifest.Files)
	assert.Equal(t, []string{"dist"}, manifest.Roots)
	assert.Equal(t, CalculateManifestDigest(manifest), manifest.Digest)

	for index := 1; index < len(manifest.Entries); index++ {
		assert.Less(t, manifest.Entries[index-1].Path, manifest.Entries[index].Path,
			"the entries are sorted by path")
	}
}

// TestCaptureTakesTwoSiblingRootsAndNothingElse: a package may declare more
// than one root, and what is captured is those roots and not the source beside
// them.
func TestCaptureTakesTwoSiblingRootsAndNothingElse(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "one\n", 0o644)
	fixture.write(t, "assets/logo.svg", "two\n", 0o644)
	fixture.write(t, "src/index.ts", "source\n", 0o644)

	manifest, err := fixture.capture(t, []string{"dist", "assets/"}, testLimits)

	require.NoError(t, err)
	paths := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		paths = append(paths, entry.Path)
	}
	assert.Equal(t, []string{"assets/logo.svg", "dist/app.js"}, paths)
	assert.Equal(t, []string{"dist", "assets"}, manifest.Roots, "a trailing slash is the same root")
}

// TestCaptureRefusesWhatCannotTravel: the conditions a capture fails on, each
// before a manifest exists at all.
func TestCaptureRefusesWhatCannotTravel(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, f *outputFixture)
		roots   []string
		limits  TransferLimits
		want    OutputReason
	}{
		"a declared root the build did not write": {
			prepare: func(t *testing.T, f *outputFixture) { f.write(t, "dist/app.js", "one\n", 0o644) },
			roots:   []string{"dist", "assets"},
			limits:  testLimits,
			want:    ReasonRootAbsent,
		},
		"more files than the ceiling allows": {
			prepare: func(t *testing.T, f *outputFixture) {
				f.write(t, "dist/a.js", "a\n", 0o644)
				f.write(t, "dist/b.js", "b\n", 0o644)
			},
			roots:  []string{"dist"},
			limits: TransferLimits{MaxFiles: 1, MaxBytes: 1 << 30},
			want:   ReasonTooManyFiles,
		},
		"more bytes than the ceiling allows": {
			prepare: func(t *testing.T, f *outputFixture) {
				f.write(t, "dist/a.js", strings.Repeat("a", 200), 0o644)
			},
			roots:  []string{"dist"},
			limits: TransferLimits{MaxFiles: 100, MaxBytes: 10},
			want:   ReasonTooManyBytes,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputFixture(t, "packages/core")
			tc.prepare(t, fixture)

			manifest, err := fixture.capture(t, tc.roots, tc.limits)

			assert.Nil(t, manifest, "nothing describes a set that was refused")
			assert.Equal(t, tc.want, OutputFaultReason(err), "%v", err)
		})
	}
}

// TestValidationRefusesASubmodule: a gitlink names a commit of another
// repository, so a set holding one is a set whose bytes are somewhere else.
// The tree is built with plumbing, because a working tree cannot hold a
// submodule on every machine this suite runs on.
func TestValidationRefusesASubmodule(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	head := fixture.run(t, "rev-parse", "HEAD")
	inner := fixture.mktree(t, fmt.Sprintf("160000 commit %s\tsub\x00", head))
	tree := fixture.mktree(t, fmt.Sprintf("040000 tree %s\tdist\x00", inner))

	_, err := ValidateOutputs(context.Background(), fixture.git,
		fixture.manifestOver(t, tree, nil), OutputExpectation{Roots: []string{"dist"}, Limits: testLimits})

	assert.Equal(t, ReasonGitlink, OutputFaultReason(err), "%v", err)
}

// TestValidationRefusesACaseFoldingCollision: two entries that are one file on
// a case-insensitive file system are a set whose content depends on where it
// lands, and the tree is built with plumbing because a working tree cannot
// hold both on every machine.
func TestValidationRefusesACaseFoldingCollision(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	first := fixture.hashObject(t, "one\n")
	second := fixture.hashObject(t, "two\n")
	inner := fixture.mktree(t, fmt.Sprintf("100644 blob %s\tApp.js\x00100644 blob %s\tapp.js\x00", first, second))
	tree := fixture.mktree(t, fmt.Sprintf("040000 tree %s\tdist\x00", inner))

	_, err := ValidateOutputs(context.Background(), fixture.git,
		fixture.manifestOver(t, tree, []ManifestEntry{
			{Path: "dist/App.js", Type: EntryFile, Mode: EntryModeFile, Size: 4, SHA256: digestOf("one\n")},
			{Path: "dist/app.js", Type: EntryFile, Mode: EntryModeFile, Size: 4, SHA256: digestOf("two\n")},
		}), OutputExpectation{Roots: []string{"dist"}, Limits: testLimits})

	assert.Equal(t, ReasonDuplicatePath, OutputFaultReason(err), "%v", err)
}

// hashObject writes one blob into the fixture's store and answers its id.
func (f *outputFixture) hashObject(t *testing.T, body string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", f.dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "hash-object: %s", out)
	return strings.TrimSpace(string(out))
}

// mktree writes one tree level from a NUL-separated listing.
func (f *outputFixture) mktree(t *testing.T, listing string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", f.dir, "mktree", "-z")
	cmd.Stdin = strings.NewReader(listing)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "mktree: %s", out)
	return strings.TrimSpace(string(out))
}

// manifestOver is a correctly bound manifest over a crafted tree, so that a
// scenario changes exactly the one thing it is about.
func (f *outputFixture) manifestOver(t *testing.T, tree string, entries []ManifestEntry) *OutputManifest {
	t.Helper()
	manifest := &OutputManifest{
		Protocol: ProtocolVersion, Run: "run-1", PlanDigest: "digest", Task: "core:build",
		Attempt: 1, Generation: "generation", Node: "build-a", Package: "core",
		Platform: Platform{OS: "linux", Arch: "amd64", Dispat: "test"},
		Roots:    []string{"dist"}, Entries: entries, OutputTree: tree,
	}
	for _, entry := range entries {
		manifest.Files++
		manifest.Bytes += entry.Size
	}
	manifest.Digest = CalculateManifestDigest(manifest)
	return manifest
}

// TestValidationRefusesEveryRuleItsOwnWay: the table of everything an output
// set may not be, each with the word the log and the tests read.
func TestValidationRefusesEveryRuleItsOwnWay(t *testing.T) {
	body := "one\n"
	for name, tc := range map[string]struct {
		change func(manifest *OutputManifest)
		expect OutputExpectation
		want   OutputReason
	}{
		"a path leaving its root": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "dist/../../escape.js" },
			want:   ReasonPathEscape,
		},
		"an absolute path": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "/etc/passwd" },
			want:   ReasonPathAbsolute,
		},
		"a path reaching into repository metadata": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "dist/.git/config" },
			want:   ReasonPathGitFolder,
		},
		"a path written with backslashes": {
			change: func(m *OutputManifest) { m.Entries[0].Path = `dist\app.js` },
			want:   ReasonPathBackslash,
		},
		"a path holding a colon": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "dist/c:app.js" },
			want:   ReasonPathColon,
		},
		"a path holding a NUL": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "dist/app\x00.js" },
			want:   ReasonPathNul,
		},
		"a path with an empty component": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "dist//app.js" },
			want:   ReasonPathComponent,
		},
		"a path outside every declared root": {
			change: func(m *OutputManifest) { m.Entries[0].Path = "elsewhere/app.js" },
			want:   ReasonPathOutsideRoot,
		},
		"entries out of order": {
			change: func(m *OutputManifest) {
				m.Entries = append(m.Entries, ManifestEntry{Path: "dist/a.js",
					Type: EntryFile, Mode: EntryModeFile, Size: 1, SHA256: digestOf("x")})
				m.Files, m.Bytes = 2, m.Bytes+1
			},
			want: ReasonPathUnsorted,
		},
		"a mode no output may carry": {
			change: func(m *OutputManifest) { m.Entries[0].Mode = "0777" },
			want:   ReasonMode,
		},
		"a kind of entry that is neither a file nor a link": {
			change: func(m *OutputManifest) { m.Entries[0].Type = "device" },
			want:   ReasonEntryType,
		},
		"a totals line that does not count its own entries": {
			change: func(m *OutputManifest) { m.Files = 7 },
			want:   ReasonTreeTotals,
		},
		"more files than the reader allows": {
			change: func(m *OutputManifest) {
				m.Entries = append(m.Entries, ManifestEntry{Path: "dist/zz.js",
					Type: EntryFile, Mode: EntryModeFile, Size: 1, SHA256: digestOf("x")})
				m.Files, m.Bytes = 2, m.Bytes+1
			},
			expect: OutputExpectation{Limits: TransferLimits{MaxFiles: 1, MaxBytes: 1 << 30}},
			want:   ReasonTooManyFiles,
		},
		"more bytes than the reader allows": {
			change: func(m *OutputManifest) {},
			expect: OutputExpectation{Limits: TransferLimits{MaxFiles: 10, MaxBytes: 1}},
			want:   ReasonTooManyBytes,
		},
		"a manifest of another protocol": {
			change: func(m *OutputManifest) { m.Protocol = ProtocolVersion + 1 },
			want:   ReasonOutputProtocol,
		},
		"a manifest of another run": {
			change: func(m *OutputManifest) { m.Run = "somebody-else" },
			expect: OutputExpectation{Run: "run-1", Limits: testLimits},
			want:   ReasonOutputIdentity,
		},
		"a manifest of another ownership": {
			change: func(m *OutputManifest) { m.Generation = "older" },
			expect: OutputExpectation{Generation: "generation", Limits: testLimits},
			want:   ReasonOutputIdentity,
		},
		"a manifest of another attempt": {
			change: func(m *OutputManifest) { m.Attempt = 9 },
			expect: OutputExpectation{Attempt: 1, Limits: testLimits},
			want:   ReasonOutputIdentity,
		},
		"a digest that does not recompute": {
			change: func(m *OutputManifest) { m.Version = "9.9.9" },
			want:   ReasonOutputDigest,
		},
		"a digest that is not the one the reader was promised": {
			change: func(m *OutputManifest) {},
			expect: OutputExpectation{Digest: "somebody-elses-manifest", Limits: testLimits},
			want:   ReasonOutputDigest,
		},
		"a set built on a platform the consumer cannot use": {
			change: func(m *OutputManifest) {},
			expect: OutputExpectation{Platforms: []string{"windows/arm64"}, Limits: testLimits},
			want:   ReasonOutputPlatform,
		},
		"a file the tree does not hold": {
			change: func(m *OutputManifest) {
				m.Entries = append(m.Entries, ManifestEntry{Path: "dist/zz.js",
					Type: EntryFile, Mode: EntryModeFile, Size: 1, SHA256: digestOf("x")})
				m.Files, m.Bytes = 2, m.Bytes+1
			},
			want: ReasonTreeMissing,
		},
		"a file the manifest does not name": {
			change: func(m *OutputManifest) { m.Entries, m.Files, m.Bytes = nil, 0, 0 },
			want:   ReasonTreeExtra,
		},
		"a length the tree disagrees with": {
			change: func(m *OutputManifest) { m.Entries[0].Size, m.Bytes = 99, 99 },
			want:   ReasonTreeSize,
		},
		"a mode the tree disagrees with": {
			change: func(m *OutputManifest) { m.Entries[0].Mode = EntryModeExecutable },
			want:   ReasonTreeMode,
		},
		"a kind the tree disagrees with": {
			change: func(m *OutputManifest) { m.Entries[0].Type, m.Entries[0].Target = EntrySymlink, "a.js" },
			want:   ReasonTreeType,
		},
		"a tree nothing fetched": {
			change: func(m *OutputManifest) {
				m.OutputTree = "0000000000000000000000000000000000000000"
			},
			want: ReasonBytesMissing,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputFixture(t, "packages/core")
			blob := fixture.hashObject(t, body)
			inner := fixture.mktree(t, fmt.Sprintf("100644 blob %s\tapp.js\x00", blob))
			tree := fixture.mktree(t, fmt.Sprintf("040000 tree %s\tdist\x00", inner))
			manifest := fixture.manifestOver(t, tree, []ManifestEntry{
				{Path: "dist/app.js", Type: EntryFile, Mode: EntryModeFile,
					Size: int64(len(body)), SHA256: digestOf(body)},
			})
			tc.change(manifest)
			expect := tc.expect
			if expect.Limits.MaxFiles == 0 && expect.Limits.MaxBytes == 0 {
				expect.Limits = testLimits
			}
			expect.Roots = []string{"dist"}
			if tc.want != ReasonOutputDigest || expect.Digest == "" {
				manifest.Digest = CalculateManifestDigest(manifest)
			}
			if tc.want == ReasonOutputDigest && expect.Digest == "" {
				// The one row whose whole point is a document nobody re-named.
				manifest.Digest = fixture.manifestOver(t, tree, manifest.Entries).Digest
			}

			_, err := ValidateOutputs(context.Background(), fixture.git, manifest, expect)

			assert.Equal(t, tc.want, OutputFaultReason(err), "%v", err)
		})
	}
}

// TestValidationRefusesALinkThatLeavesItsRoot: a symlink is the one entry that
// can make a path mean something else, so where it points is held to the same
// rule as where it is.
func TestValidationRefusesALinkThatLeavesItsRoot(t *testing.T) {
	for name, tc := range map[string]struct {
		target string
		want   OutputReason
	}{
		"a target above the root":   {target: "../../etc/passwd", want: ReasonLinkEscape},
		"an absolute target":        {target: "/etc/passwd", want: ReasonLinkAbsolute},
		"a target with no name":     {target: "", want: ReasonLinkTargetEmpty},
		"a target with backslash":   {target: `..\..\x`, want: ReasonPathBackslash},
		"a target leaving sideways": {target: "../sibling/x", want: ReasonLinkEscape},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOutputFixture(t, "packages/core")
			blob := fixture.hashObject(t, tc.target)
			inner := fixture.mktree(t, fmt.Sprintf("120000 blob %s\tlink\x00", blob))
			tree := fixture.mktree(t, fmt.Sprintf("040000 tree %s\tdist\x00", inner))
			manifest := fixture.manifestOver(t, tree, []ManifestEntry{
				{Path: "dist/link", Type: EntrySymlink, Mode: EntryModeFile,
					Size: int64(len(tc.target)), SHA256: digestOf(tc.target), Target: tc.target},
			})

			_, err := ValidateOutputs(context.Background(), fixture.git, manifest,
				OutputExpectation{Roots: []string{"dist"}, Limits: testLimits})

			assert.Equal(t, tc.want, OutputFaultReason(err), "%v", err)
		})
	}
}

// TestValidationAdmitsWhatACaptureProduced: the two halves meet, which is the
// only claim that makes either of them worth anything.
func TestValidationAdmitsWhatACaptureProduced(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	fixture.write(t, "dist/app.js", "one\n", 0o644)
	fixture.write(t, "dist/nested/deep.js", "two\n", 0o755)
	require.NoError(t, os.Symlink("app.js", filepath.Join(fixture.pkgDir, "dist", "latest.js")))

	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)

	totals, err := ValidateOutputs(context.Background(), fixture.git, manifest, OutputExpectation{
		Run: "run-1", PlanDigest: "digest", Generation: "generation", Task: "core:build", Attempt: 1,
		Roots: []string{"dist"}, Digest: manifest.Digest, Limits: testLimits,
	})

	require.NoError(t, err)
	assert.Equal(t, 3, totals.Files)
	assert.Equal(t, manifest.Bytes, totals.Bytes)
}

// TestCaptureAndInstallStreamALargeFile: the one claim that cannot be made by
// reading a small fixture. Eight mebibytes go through the capture and the
// install, and the process never holds them: the proof is the one place a
// whole file could be accumulated, the reader's own writer, which is asked how
// large the largest write it saw was.
func TestCaptureAndInstallStreamALargeFile(t *testing.T) {
	fixture := newOutputFixture(t, "packages/core")
	body := strings.Repeat("0123456789abcdef", 8<<20/16)
	fixture.write(t, "dist/big.bin", body, 0o644)

	manifest, err := fixture.capture(t, []string{"dist"}, testLimits)
	require.NoError(t, err)
	require.Equal(t, int64(8<<20), manifest.Entries[0].Size)
	assert.Equal(t, digestOf(body), manifest.Entries[0].SHA256)

	counter := &countingSink{}
	reader, err := gitx.OpenObjectReader(context.Background(), fixture.git)
	require.NoError(t, err)
	objects := map[string]string{}
	plumbing := gitx.NewPlumbing(fixture.git)
	plumbing.ListTree(context.Background(), manifest.OutputTree, func(entry gitx.TreeEntry) error {
		objects[entry.Name] = entry.OID
		return nil
	})
	require.NoError(t, plumbing.Err())
	read, err := reader.ReadBlob(objects["dist/big.bin"], counter, 1<<30)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	assert.Equal(t, int64(8<<20), read)
	assert.Equal(t, 8<<20, counter.total)
	assert.LessOrEqual(t, counter.largest, 1<<20,
		"the bytes arrive in pieces rather than as one eight mebibyte write")
	assert.Greater(t, counter.writes, 8, "and there were many of them")
}

// countingSink is a writer that remembers how the bytes arrived, which is how
// a test asserts that nothing was accumulated.
type countingSink struct {
	total   int
	largest int
	writes  int
}

func (c *countingSink) Write(p []byte) (int, error) {
	c.total += len(p)
	c.writes++
	if len(p) > c.largest {
		c.largest = len(p)
	}
	return len(p), nil
}
