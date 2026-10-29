// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"errors"
	"io"
	"os"
)

const gitInputEmptyByteCount = 0

type gitCommandInput struct {
	io.Reader
	err error
}

func newGitCommandInput(reader io.Reader) (io.Reader, *gitCommandInput) {
	if reader == nil {
		return nil, nil
	}
	if _, isFile := reader.(*os.File); isFile {
		return reader, nil
	}
	input := &gitCommandInput{Reader: reader}
	return input, input
}

func (input *gitCommandInput) WriteTo(destination io.Writer) (int64, error) {
	count, err := io.Copy(completeGitInputWriter{destination: destination}, input.Reader)
	input.err = err
	return count, err
}

type completeGitInputWriter struct {
	destination io.Writer
}

// TinyGo's pipe writes may return positive partial progress without an error.
// Complete only the unwritten suffix of this same input.
func (writer completeGitInputWriter) Write(content []byte) (int, error) {
	written := gitInputEmptyByteCount
	for written < len(content) {
		count, err := writer.destination.Write(content[written:])
		if count < gitInputEmptyByteCount || count > len(content)-written {
			return written, errors.Join(io.ErrShortWrite, err)
		}
		written += count
		if err != nil {
			return written, err
		}
		if count == gitInputEmptyByteCount {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}
