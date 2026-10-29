package cli

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestRemovedWorkerCommandRefusesBeforeDiscovery(t *testing.T) {
	for _, suffix := range [][]string{nil, {"--help"}, {"--state-dir", t.TempDir()}} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"worker", "--root", t.TempDir()}, suffix...)
		assert.Equal(t, 2, Run(args, &stdout, &stderr))
		assert.Contains(t, stderr.String(), "use dispat choreographer")
		assert.NotContains(t, stderr.String(), "no dispat config")
		assert.Empty(t, stdout.String())
	}
}

func TestExplicitRunWorkerRemainsAScript(t *testing.T) {
	inv, bad := parseInvocation([]string{"run", "worker"}, -1, func(string) {}, zerolog.Nop())
	assert.False(t, bad)
	assert.Equal(t, cmdRun, inv.cmd)
	assert.Equal(t, "worker", inv.script)
}

func TestRemovedWorkerFlagRefusesBeforeDiscovery(t *testing.T) {
	var stdout, stderr bytes.Buffer
	assert.Equal(t, 2, Run([]string{"status", "--root", t.TempDir(), "--worker", "build-a"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "unknown flag: --worker")
	assert.NotContains(t, stderr.String(), "no dispat config")
}
