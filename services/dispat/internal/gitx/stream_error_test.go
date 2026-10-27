// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"io"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

type rejectedGitOutput struct {
	err error
}

func (w rejectedGitOutput) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRunStreamRetainsOutputFailureWhenGitExitsNonzero(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		writeErr error
		wantErr  error
	}{
		{name: "transport limit", writeErr: ErrTransportLimit, wantErr: ErrTransportLimit},
		{name: "short write", wantErr: io.ErrShortWrite},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, git := initRepo(t)
			// The explicit exit makes Git fail independently of whether its
			// small output reaches the rejecting writer before it exits.
			_, err := git.runStream(t.Context(), gitStream{
				stdout: rejectedGitOutput{err: scenario.writeErr},
			}, "-c", "alias.rejected-output=!printf result; exit 23", "rejected-output")

			require.ErrorIs(t, err, scenario.wantErr)
			var processError *exec.ExitError
			require.ErrorAs(t, err, &processError)
			require.Equal(t, 23, processError.ExitCode(), "the process failure remains available too")
		})
	}
}
