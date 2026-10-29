// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type partialGitInputWriter struct {
	output bytes.Buffer
	limit  int
	calls  int
}

type gitInputWriteFunc func([]byte) (int, error)

func (write gitInputWriteFunc) Write(content []byte) (int, error) { return write(content) }

type failedGitInputReader struct{ err error }

func (reader failedGitInputReader) Read([]byte) (int, error) { return 0, reader.err }

func TestGitCommandInputRefusesInvalidProgress(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		count int
	}{
		{name: "zero", count: 0},
		{name: "negative", count: -1},
		{name: "beyond input", count: 6},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			stdin, input := newGitCommandInput(bytes.NewBufferString("input"))
			calls := 0
			count, err := io.Copy(gitInputWriteFunc(func([]byte) (int, error) {
				calls++
				return scenario.count, nil
			}), stdin)
			require.ErrorIs(t, err, io.ErrShortWrite)
			require.ErrorIs(t, input.err, io.ErrShortWrite)
			require.Zero(t, count)
			require.Equal(t, 1, calls, "invalid progress cannot spin or replay the input")
		})
	}
}

func TestGitCommandInputPreservesFailedWriteAndItsProgress(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		count int
		err   error
	}{
		{name: "no progress", err: errors.New("pipe failed")},
		{name: "partial progress", count: 2, err: errors.New("pipe failed after progress")},
		{name: "explicit short write", count: 2, err: io.ErrShortWrite},
		{name: "all bytes with error", count: 5, err: errors.New("pipe failed after input")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			stdin, input := newGitCommandInput(bytes.NewBufferString("input"))
			calls := 0
			count, err := io.Copy(gitInputWriteFunc(func([]byte) (int, error) {
				calls++
				return scenario.count, scenario.err
			}), stdin)
			require.ErrorIs(t, err, scenario.err)
			require.ErrorIs(t, input.err, scenario.err)
			require.Equal(t, int64(scenario.count), count)
			require.Equal(t, 1, calls, "an errored write must never be repeated")
		})
	}
}

func TestGitCommandInputPreservesEmptyAndDirectFileInput(t *testing.T) {
	stdin, input := newGitCommandInput(nil)
	require.Nil(t, stdin)
	require.Nil(t, input)
	file, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	stdin, input = newGitCommandInput(file)
	require.Same(t, file, stdin, "os/exec must continue passing the file descriptor directly")
	require.Nil(t, input)
	stdin, input = newGitCommandInput(bytes.NewBuffer(nil))
	calls := 0
	count, err := io.Copy(gitInputWriteFunc(func([]byte) (int, error) {
		calls++
		return 0, io.ErrClosedPipe
	}), stdin)
	require.NoError(t, err)
	require.NoError(t, input.err)
	require.Zero(t, count)
	require.Zero(t, calls)
}

func TestRunStreamRetainsInputFailureWhenGitExitsNonzero(t *testing.T) {
	_, git := initRepo(t)
	cause := errors.New("input source failed after its prefix")
	_, err := git.runStream(t.Context(), gitStream{stdin: io.MultiReader(
		strings.NewReader("input prefix"), failedGitInputReader{err: cause},
	)}, "-c", "alias.rejected-input=!cat >/dev/null; exit 23", "rejected-input")
	require.ErrorIs(t, err, cause)
	var processError *exec.ExitError
	require.ErrorAs(t, err, &processError)
	require.Equal(t, 23, processError.ExitCode())
}

func TestRunStreamHashesExactLargeInput(t *testing.T) {
	_, git := initRepo(t)
	content := bytes.Repeat([]byte("unaltered input bytes\x00\n"), 12_000)
	digest := sha1.New()
	_, err := fmt.Fprintf(digest, "blob %d\x00", len(content))
	require.NoError(t, err)
	_, err = digest.Write(content)
	require.NoError(t, err)
	actual, err := git.runStream(t.Context(), gitStream{stdin: bytes.NewBuffer(content)}, "hash-object", "--stdin")
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", digest.Sum(nil)), strings.TrimSpace(actual))
}

func (writer *partialGitInputWriter) Write(content []byte) (int, error) {
	writer.calls++
	return writer.output.Write(content[:min(len(content), writer.limit)])
}

type gitInputReaderOnly struct{ io.Reader }

func TestGitCommandInputCompletesPartialWrites(t *testing.T) {
	var paths strings.Builder
	for index := range 1000 {
		fmt.Fprintf(&paths, "/tmp/%s/packages/assets/dist/file%d.txt\n", strings.Repeat("worktree", 24), index)
	}
	content := paths.String()
	require.Greater(t, len(content), 200_000)
	for _, scenario := range []struct {
		name   string
		reader func() io.Reader
	}{
		{name: "buffer", reader: func() io.Reader { return bytes.NewBufferString(content) }},
		{name: "string", reader: func() io.Reader { return strings.NewReader(content) }},
		{name: "reader only", reader: func() io.Reader { return gitInputReaderOnly{strings.NewReader(content)} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			stdin, input := newGitCommandInput(scenario.reader())
			writer := &partialGitInputWriter{limit: 4096}
			count, err := io.Copy(writer, stdin)
			require.NoError(t, err)
			require.NoError(t, input.err)
			require.Equal(t, int64(len(content)), count)
			require.Equal(t, content, writer.output.String())
			require.Greater(t, writer.calls, 1)
		})
	}
}
