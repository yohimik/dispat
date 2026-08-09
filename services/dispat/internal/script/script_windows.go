//go:build windows

package script

import "os/exec"

// setSysProcAttr is a no-op on Windows: there is no process group to signal,
// so exec's default cancellation (killing the process) applies and WaitDelay
// alone bounds the wait for the pipes.
func setSysProcAttr(cmd *exec.Cmd) {}
