//go:build unix

package script

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	strictProcessGroupTermGrace           = 250 * time.Millisecond
	strictProcessGroupKillGrace           = 2 * time.Second
	strictProcessGroupPoll                = 10 * time.Millisecond
	strictProcessGroupCheckErrorFormat    = "%w: checking owned group: %v"
	strictProcessGroupTermErrorFormat     = "%w: signaling owned group: %v"
	strictProcessGroupTermWaitErrorFormat = "%w: waiting after TERM: %v"
	strictProcessGroupKillErrorFormat     = "%w: forcing owned group: %v"
	strictProcessGroupKillWaitErrorFormat = "%w: waiting after KILL: %v"
	strictProcessGroupExistenceSignal     = syscall.Signal(0)
	strictProcessGroupPresent             = true
	strictProcessGroupAbsent              = false
)

// settleOwnedProcessGroup proves only that the process group created for this
// command has disappeared. A descendant that deliberately creates another
// process group cannot be covered by this proof.
func settleOwnedProcessGroup(cmd *exec.Cmd, runErr error) error {
	if cmd.Process == nil {
		return runErr
	}
	pgid := cmd.Process.Pid
	active, err := processGroupActive(pgid)
	if err != nil {
		return fmt.Errorf(strictProcessGroupCheckErrorFormat, ErrProcessGroupUnsettled, err)
	}
	if !active {
		if errors.Is(runErr, exec.ErrWaitDelay) {
			return ErrProcessGroupUnsettled
		}
		return runErr
	}
	if err := signalProcessGroup(pgid, syscall.SIGTERM); err != nil {
		return fmt.Errorf(strictProcessGroupTermErrorFormat, ErrProcessGroupUnsettled, err)
	}
	stopped, err := awaitProcessGroupGone(pgid, strictProcessGroupTermGrace)
	if err != nil {
		return fmt.Errorf(strictProcessGroupTermWaitErrorFormat, ErrProcessGroupUnsettled, err)
	}
	if !stopped {
		if err := signalProcessGroup(pgid, syscall.SIGKILL); err != nil {
			return fmt.Errorf(strictProcessGroupKillErrorFormat, ErrProcessGroupUnsettled, err)
		}
		stopped, err = awaitProcessGroupGone(pgid, strictProcessGroupKillGrace)
		if err != nil {
			return fmt.Errorf(strictProcessGroupKillWaitErrorFormat, ErrProcessGroupUnsettled, err)
		}
	}
	if !stopped || errors.Is(runErr, exec.ErrWaitDelay) {
		return ErrProcessGroupUnsettled
	}
	if runErr != nil {
		return runErr
	}
	return ErrProcessGroupStopped
}

func processGroupActive(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, strictProcessGroupExistenceSignal)
	switch err {
	case nil:
		return strictProcessGroupPresent, nil
	case syscall.ESRCH:
		return strictProcessGroupAbsent, nil
	default:
		return strictProcessGroupAbsent, err
	}
}

func signalProcessGroup(pgid int, signal syscall.Signal) error {
	err := syscall.Kill(-pgid, signal)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func awaitProcessGroupGone(pgid int, bound time.Duration) (bool, error) {
	deadline := time.Now().Add(bound)
	for {
		active, err := processGroupActive(pgid)
		if err != nil || !active {
			return !active, err
		}
		if !time.Now().Before(deadline) {
			return strictProcessGroupAbsent, nil
		}
		time.Sleep(strictProcessGroupPoll)
	}
}

// setSysProcAttr puts the script into its own process group and makes
// cancellation signal the whole group, so children die with the shell and
// release the output pipes instead of holding Wait hostage.
func setSysProcAttr(cmd *exec.Cmd) {
	setProcessGroupCancel(cmd, syscall.SIGTERM)
}

func setSysProcAttrForce(cmd *exec.Cmd) {
	setProcessGroupCancel(cmd, syscall.SIGKILL)
}

func setProcessGroupCancel(cmd *exec.Cmd, signal syscall.Signal) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, signal)
		if err == syscall.ESRCH {
			// The group is already gone; exec ignores ErrProcessDone.
			return os.ErrProcessDone
		}
		return err
	}
}
