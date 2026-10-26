//go:build windows

package script

import "os/exec"

// setSysProcAttr is a no-op on Windows: there is no process group to signal,
// so exec's default cancellation (killing the process) applies and WaitDelay
// alone bounds the wait for the pipes.
func setSysProcAttr(cmd *exec.Cmd) {}

func setSysProcAttrForce(cmd *exec.Cmd) { setSysProcAttr(cmd) }

func settleOwnedProcessGroup(_ *exec.Cmd, _ error) error {
	// Windows has no owned process-group proof in this runner. A strict task
	// must keep its lease rather than claim its descendants have stopped.
	return ErrProcessGroupUnsettled
}
