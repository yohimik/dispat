//go:build tinygo

package selfupdate

import (
	"syscall"
	"time"
)

// TinyGo's os.Chtimes is a stub. Use its supported syscall path so an old
// binary promoted to a backup is not pruned on the next invocation.
func touch(path string) {
	now := syscall.NsecToTimespec(time.Now().UnixNano())
	_ = syscall.UtimesNano(path, []syscall.Timespec{now, now})
}
