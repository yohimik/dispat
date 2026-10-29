//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processTerminationGrace = 2 * time.Second

func configureProcess(process *exec.Cmd) {
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	process.Cancel = func() error {
		err := syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if err != nil {
			return err
		}
		timer := time.NewTimer(processTerminationGrace)
		defer timer.Stop()
		<-timer.C
		// Keep cancellation pending until the entire group has received escalation.
		if err := syscall.Kill(-process.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
}
