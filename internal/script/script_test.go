package script

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireShell(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available", name)
	}
}

func TestShellRunnerDefault(t *testing.T) {
	requireShell(t, "/bin/sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{}
	err := r.Run(context.Background(), t.TempDir(), "echo $DISPAT_PACKAGE", []string{"DISPAT_PACKAGE=core"}, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, "core\n", out.String(), "env must reach the script")
}

func TestShellRunnerCustomShell(t *testing.T) {
	requireShell(t, "sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{Shell: []string{"sh", "-c"}}
	err := r.Run(context.Background(), t.TempDir(), "echo custom", nil, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, "custom\n", out.String())
}

func TestShellRunnerFailure(t *testing.T) {
	requireShell(t, "/bin/sh")
	var out, errb bytes.Buffer
	r := &ShellRunner{}
	err := r.Run(context.Background(), t.TempDir(), "exit 3", nil, &out, &errb)
	assert.Error(t, err)
}
