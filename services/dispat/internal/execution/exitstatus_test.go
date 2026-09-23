package execution

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveExitStatusReadsTheCommandsStatusThroughWrapping fences the exit
// status a node reports: the command's own, found through whatever wrapping
// the sequence added, and 0 for a failure that was not a command's.
func TestResolveExitStatusReadsTheCommandsStatusThroughWrapping(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 3").Run()
	require.Error(t, err)
	assert.Equal(t, 3, resolveExitStatus(err), "the bare exit error")
	assert.Equal(t, 3, resolveExitStatus(fmt.Errorf("stage build: %w", err)), "wrapped once")
	assert.Equal(t, 0, resolveExitStatus(errors.New("the shell could not start")), "not a command's failure")
	assert.Equal(t, 0, resolveExitStatus(nil))
}

// TestFormatExitStatusSaysNothingForZero fences the run's message: a status
// is named only when a command produced one.
func TestFormatExitStatusSaysNothingForZero(t *testing.T) {
	assert.Equal(t, "", formatExitStatus(0))
	assert.Equal(t, " (exit 1)", formatExitStatus(1))
	assert.Equal(t, " (exit 66)", formatExitStatus(66))
}
