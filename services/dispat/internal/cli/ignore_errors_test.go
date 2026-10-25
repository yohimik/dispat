// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIgnoreErrorsParsing: the record commands forgive a failed record unless
// told otherwise, so a flow that never mentions the flag gets the safe reading.
func TestIgnoreErrorsParsing(t *testing.T) {
	tests := map[string]struct {
		args             []string
		isIgnoringErrors bool
	}{
		"github, unpassed":         {args: []string{"github"}, isIgnoringErrors: true},
		"changelog, unpassed":      {args: []string{"changelog"}, isIgnoringErrors: true},
		"github, bare":             {args: []string{"github", "--ignore-errors"}, isIgnoringErrors: true},
		"github, turned off":       {args: []string{"github", "--ignore-errors=false"}, isIgnoringErrors: false},
		"changelog, turned off":    {args: []string{"changelog", "--ignore-errors=false"}, isIgnoringErrors: false},
		"changelog, spelled true":  {args: []string{"changelog", "--ignore-errors=true"}, isIgnoringErrors: true},
		"after the selection flag": {args: []string{"github", "-p", "core", "--ignore-errors=false"}, isIgnoringErrors: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
			o := declareFlags(fs)
			require.NoError(t, fs.Parse(tc.args))
			assert.Equal(t, tc.isIgnoringErrors, *o.ignoreErrors)
		})
	}
}

// TestIgnoreErrorsBelongsToTheRecordCommands: commit records the version with
// its tag, so it takes no such flag and says why; every other command is told
// whose flag it is.
func TestIgnoreErrorsBelongsToTheRecordCommands(t *testing.T) {
	tests := map[string]struct {
		args []string
		has  []string
	}{
		"commit": {
			args: []string{"commit", "--tag", "--ignore-errors"},
			has: []string{"--ignore-errors is not a commit flag",
				"it belongs to dispat changelog or dispat github",
				"a tag commit could not create or push always fails it"},
		},
		"run": {
			args: []string{"run", "lint", "--ignore-errors=false"},
			has:  []string{"--ignore-errors is not a run flag", "it belongs to dispat changelog or dispat github"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(append(tc.args, "--root", t.TempDir()), &stdout, &stderr)
			require.Equal(t, 2, code, "stderr:\n%s", stderr.String())
			for _, want := range tc.has {
				assert.Contains(t, stderr.String(), want)
			}
		})
	}
}

// TestIgnoreErrorsIsInTheRecordCommandsHelp: the flag a command takes is the
// flag its help offers.
func TestIgnoreErrorsIsInTheRecordCommandsHelp(t *testing.T) {
	for _, command := range []string{"changelog", "github"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			require.Equal(t, 0, Run([]string{command, "--help"}, &stdout, &stderr))
			assert.Contains(t, stdout.String()+stderr.String(), "--ignore-errors")
		})
	}
}
