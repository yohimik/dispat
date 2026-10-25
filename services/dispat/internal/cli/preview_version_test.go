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

func TestJoinPreviewVersion(t *testing.T) {
	tests := map[string]struct {
		args, want []string
	}{
		"a separate value is joined": {
			args: []string{"preview", "-p", "dispat", "--version", "1.11.0-rc.8"},
			want: []string{"preview", "-p", "dispat", "--version=1.11.0-rc.8"},
		},
		"a bare flag before another flag stays bare": {
			args: []string{"preview", "-p", "dispat", "--version", "--github"},
			want: []string{"preview", "-p", "dispat", "--version", "--github"},
		},
		"a bare flag at the end stays bare": {
			args: []string{"preview", "--version"},
			want: []string{"preview", "--version"},
		},
		"an inline value is left alone": {
			args: []string{"preview", "--version=services/dispat/v1.11.0-rc.8"},
			want: []string{"preview", "--version=services/dispat/v1.11.0-rc.8"},
		},
		"another command is left alone": {
			args: []string{"status", "--version", "x"},
			want: []string{"status", "--version", "x"},
		},
		"what follows -- belongs to nobody": {
			args: []string{"preview", "--", "--version", "1.0.0"},
			want: []string{"preview", "--", "--version", "1.0.0"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, joinPreviewVersion(tc.args))
		})
	}
}

func TestVersionFlagReadingPerCommand(t *testing.T) {
	tests := map[string]struct {
		args    []string
		isAsked bool
		value   string
	}{
		"bare, no command":      {args: []string{"--version"}, isAsked: true, value: "latest"},
		"on another command":    {args: []string{"status", "--version"}, isAsked: true, value: "latest"},
		"explicitly false":      {args: []string{"--version=false"}, isAsked: false, value: "false"},
		"on preview, bare":      {args: []string{"preview", "--version"}, isAsked: false, value: "latest"},
		"on preview, a version": {args: []string{"preview", "--version=1.2.3"}, isAsked: false, value: "1.2.3"},
		"absent":                {args: []string{"preview"}, isAsked: false, value: ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fs := pflag.NewFlagSet("dispat", pflag.ContinueOnError)
			o := declareFlags(fs)
			require.NoError(t, fs.Parse(tc.args))
			r := &runner{fs: fs, o: o}
			assert.Equal(t, tc.isAsked, r.isDispatVersionAsked())
			assert.Equal(t, tc.value, *o.version)
		})
	}
}

// TestPreviewVersionNeedsNoStrayArgument: the separate value reaches preview
// as the flag's value, so the command's no-arguments rule never sees it.
func TestPreviewVersionNeedsNoStrayArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"preview", "--version", "1.0.0", "--root", t.TempDir()}, &stdout, &stderr)
	assert.NotEqual(t, 0, code, "an empty folder is no repository")
	assert.NotContains(t, stderr.String(), "takes no arguments")
	assert.NotContains(t, stdout.String(), "dispat dev", "preview's --version is not the program's")
}
