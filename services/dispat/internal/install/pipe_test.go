package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// requireShell skips on the platform where /bin/sh is not the shell a pipe
// runs through.
func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the pipe scenarios need a POSIX shell")
	}
}

// fakeFetcher stands in for the installer: it writes the bytes a release would
// have served, so what a pipe does with them can be exercised with no network.
type fakeFetcher struct {
	body []byte
	err  error
	// dir records where it was asked to stage, which is what proves a piped
	// download never lands in the install folder.
	dir string
}

func (f *fakeFetcher) Fetch(_ context.Context, a selfupdate.Asset, dir, target string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.dir = dir
	path := filepath.Join(dir, filepath.Base(target))
	if err := os.WriteFile(path, f.body, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// TestPipeHandsTheFileOverTwice: the two shapes of command need different
// things. "tar -xz" and "sh" read the standard input; a command that has to
// seek takes the path instead, and the name the release published is what says
// which archive layout it is looking at.
func TestPipeHandsTheFileOverTwice(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "tool.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte("the payload"), 0o644))

	var out bytes.Buffer
	pipe := Pipe{
		Command: `cat; echo "name=$DISPAT_ASSET_NAME"; echo "bytes=$(wc -c < "$DISPAT_ASSET" | tr -d " ")"; pwd`,
		Dir:     dir, Stdout: &out, Stderr: &out,
	}
	require.NoError(t, pipe.Run(context.Background(), path, "tool.tar.gz"))

	got := out.String()
	assert.Contains(t, got, "the payload", "the standard input carries it")
	assert.Contains(t, got, "name=tool.tar.gz")
	assert.Contains(t, got, "bytes=11", "and so does the path")
	assert.Contains(t, got, dir, "the pipe runs in the install folder, so an archive unpacks there")
}

// TestPipeReportsWhatTheCommandMadeOfIt: a pipe is the last step of an install
// rather than a script under a helper, so a command that fails is a failed
// install and says so.
func TestPipeReportsWhatTheCommandMadeOfIt(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	var out bytes.Buffer
	err := Pipe{Command: "exit 3", Dir: dir, Stdout: &out, Stderr: &out}.Run(context.Background(), path, "tool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the pipe failed")
	assert.Contains(t, err.Error(), "exit status 3")

	err = Pipe{Command: "cat", Dir: dir, Stdout: &out, Stderr: &out}.
		Run(context.Background(), filepath.Join(dir, "absent"), "tool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the downloaded file")
}

// TestPipeStopsWithTheContext: a Ctrl-C during an install must reach the
// command holding the download, not be waited out.
func TestPipeStopsWithTheContext(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := Pipe{Command: "sleep 30", Dir: dir, Stdout: &out, Stderr: &out}.
		Run(ctx, path, "tool")
	require.Error(t, err)
}

// TestStageKeepsAPipedDownloadOffPath: a pipe never renames the file into
// place, so a half-read archive must not be left where a shell would find it.
// The staging folder is its own and is removed whatever the command did.
func TestStageKeepsAPipedDownloadOffPath(t *testing.T) {
	f := &fakeFetcher{body: []byte("the payload")}
	var staged string
	require.NoError(t, Stage(context.Background(), f, selfupdate.Asset{Name: "tool.tar.gz"},
		func(path string) error {
			staged = path
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, "the payload", string(body))
			assert.Equal(t, ".gz", filepath.Ext(path), "the asset's extension is kept")
			return nil
		}))
	require.NotEmpty(t, staged)
	assert.NoFileExists(t, staged, "the staging folder goes when the command is done")
	assert.Contains(t, filepath.Base(filepath.Dir(staged)), strings.TrimSuffix(stagePattern, "-"))

	// And it goes when the command failed, too, which is the case that
	// matters: the file that could not be unpacked must not survive.
	boom := errors.New("no")
	err := Stage(context.Background(), f, selfupdate.Asset{Name: "tool"},
		func(path string) error { staged = path; return boom })
	assert.ErrorIs(t, err, boom)
	assert.NoFileExists(t, staged)

	// A download that never arrived reaches the command not at all.
	var ran bool
	err = Stage(context.Background(), &fakeFetcher{err: boom}, selfupdate.Asset{Name: "tool"},
		func(string) error { ran = true; return nil })
	assert.ErrorIs(t, err, boom)
	assert.False(t, ran, "there is nothing to hand over")
}

// TestStageKeepsAnAssetNameOffTheFilesystem: the name comes off the API, so a
// release naming its asset "../../etc/passwd" must reach the command as the
// staged file it is rather than as a path outside dispat's own folder.
func TestStageKeepsAnAssetNameOffTheFilesystem(t *testing.T) {
	f := &fakeFetcher{body: []byte("the payload")}
	for name, asset := range map[string]string{
		"a traversal":      "../../etc/passwd",
		"a nested path":    "dist/tool",
		"a bare dot":       ".",
		"two dots":         "..",
		"no name at all":   "",
		"a trailing slash": "tool/",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, Stage(context.Background(), f, selfupdate.Asset{Name: asset},
				func(path string) error {
					assert.Equal(t, f.dir, filepath.Dir(path),
						"the staged file never leaves the folder dispat made for it")
					body, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, "the payload", string(body))
					return nil
				}))
		})
	}
}

// TestStageReportsAFolderItCannotMake: a machine with no usable temporary
// folder cannot stage a download, and saying so beats failing later on a path
// that was never created.
func TestStageReportsAFolderItCannotMake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TMPDIR is not what names the temporary folder here")
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "nowhere"))
	err := Stage(context.Background(), &fakeFetcher{body: []byte("x")},
		selfupdate.Asset{Name: "tool"}, func(string) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot stage the download")
}

// TestNewInstallerValidatesNothing: there is nothing it could check. A foreign
// binary need not answer --version, and one downloaded for another platform
// cannot run here at all, so the size and the checksum the release published
// are what stands in its place.
func TestNewInstallerValidatesNothing(t *testing.T) {
	i := NewInstaller("/opt/bin/tool", nil, zeroLogger())
	assert.Nil(t, i.Validator)
	assert.Equal(t, "/opt/bin/tool", i.Exe)
	assert.Equal(t, Command, i.Command, "so its failures name the command the operator typed")
}

// zeroLogger is the logger every unit here passes: it writes nowhere, which is
// what the package's own callers hand it when nothing is listening.
func zeroLogger() zerolog.Logger { return zerolog.Nop() }
