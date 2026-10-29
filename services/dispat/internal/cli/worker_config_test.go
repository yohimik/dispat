package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configlib "github.com/yohimik/dispat/pkg/config"

	"github.com/yohimik/dispat/services/dispat/internal/execution"
)

// TestWorkerReadsItsConfigurationOnce: the ascent parses the file it settles
// on, and the serving node decodes that tree rather than reading the file
// again. Worker authority stops the node right after the load, before it
// serves anything, so the trace is the load's alone.
func TestWorkerReadsItsConfigurationOnce(t *testing.T) {
	t.Setenv(updateCheckEnv, "0")
	t.Setenv(execution.AuthorityEnv, execution.WorkerAuthority)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkgs", "core"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dispat.json"),
		[]byte(`{"packages":{"core":{"path":"pkgs/core"}}}`), 0o600))

	var stdout, stderr bytes.Buffer
	code := Run([]string{"choreographer", "--root", root, "--log-level", "trace", "--log-format", "json"}, &stdout, &stderr)

	assert.Equal(t, 1, code, "stderr:\n%s", stderr.String())
	assert.Contains(t, stderr.String()+stdout.String(), `"code":"`+execution.CodeAuthority+`"`)
	reads := strings.Count(stderr.String(), configlib.EventFileRead)
	assert.Equal(t, 1, reads, "dispat choreographer read its configuration %d times; stderr:\n%s", reads, stderr.String())
}
