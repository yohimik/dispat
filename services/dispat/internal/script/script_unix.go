//go:build unix

package script

import (
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the script into its own process group and makes
// cancellation signal the whole group, so children die with the shell and
// release the output pipes instead of holding Wait hostage.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			// The group is already gone; exec ignores ErrProcessDone.
			return os.ErrProcessDone
		}
		return err
	}
}
