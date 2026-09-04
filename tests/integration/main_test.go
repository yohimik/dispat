package integration

import (
	"fmt"
	"os"
	"testing"

	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// TestMain exists for one piece of cleanup: the compiled dispat and tsmark
// binaries live in a temp directory shared by every test through a sync.Once,
// so only the end of the whole run may remove it.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := harness.CheckRaceReports(os.Getenv("DISPAT_TEST_RACE_LOG_DIR")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	harness.CleanupBinaries()
	os.Exit(code)
}
