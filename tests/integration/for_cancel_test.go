package integration

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// Cancelling an active loop stops its remaining items even with keep-going,
// but still runs the one failure cleanup outside the cancelled context.
func TestForCancellationStopsRemainingItemsAndRunsCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this fixture sends a POSIX interrupt to the command")
	}
	r := harness.New(t)
	proc := r.StartCommandEnv(nil, "for", "first", "second", "third", "--keep-going",
		"--log-level", "debug", "--log-format", "json",
		"--do", `printf '%s\n' "$DISPAT_ITEM" >> started; exec sleep 60`,
		"--on-failure", `printf 'cleanup\n' >> cleaned; exit 3`)
	started := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(r.Path("started")); err == nil {
			started = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	proc.Signal(os.Interrupt)
	res := proc.Wait()
	require.True(t, started, "the first loop item must start before cancellation")
	require.Equal(t, 3, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, "first\n", readRepoFile(t, r, "started"))
	assert.Equal(t, "cleanup\n", readRepoFile(t, r, "cleaned"))
	assert.Contains(t, res.Stdout, "the loop was cancelled, stopping")
	assert.Contains(t, res.Stdout, `"ran":1`)
}
