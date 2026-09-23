//go:build !tinygo

package selfupdate

import (
	"os"
	"time"
)

// touch starts the backup's retention clock after a successful swap.
// A timestamp failure cannot turn an installed binary into a failed install.
func touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}
