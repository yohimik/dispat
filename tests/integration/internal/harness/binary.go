// Package harness is the black-box test support library for the integration
// suite: it builds the real dispat binary (never the internal cli.Run entry
// point — this module deliberately cannot import services/cli/internal/*,
// which is the point: it exercises dispat exactly as a user's shell does),
// drives it against disposable git repositories, and gives tests structured
// ways to read back what happened — parsed JSON log events and, where a
// script's timing rather than its mere ordering matters, nanosecond-resolution
// timelines recorded by the tsmark helper (see cmd/tsmark).
package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// binaries is the once-per-test-run build cache. One release run through the
// real binary is not fast enough to pay a `go build` per test case, and every
// test wants the same two binaries anyway, so the first caller builds and
// everyone else reuses the result.
var binaries struct {
	once           sync.Once
	dispat, tsmark string
	err            error
}

// Build compiles the dispat CLI (from services/cli) and the tsmark timing
// helper (from this module's cmd/tsmark) once and returns their paths,
// failing the test if the build failed.
func Build(t testing.TB) (dispatBin, tsmarkBin string) {
	t.Helper()
	binaries.once.Do(func() {
		binaries.dispat, binaries.tsmark, binaries.err = build()
	})
	if binaries.err != nil {
		t.Fatalf("building test binaries: %v", binaries.err)
	}
	return binaries.dispat, binaries.tsmark
}

func build() (dispat, tsmark string, err error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", "", fmt.Errorf("go toolchain not found on PATH: %w", err)
	}
	root, err := monorepoRoot()
	if err != nil {
		return "", "", err
	}
	dir, err := os.MkdirTemp("", "dispat-it-bin-")
	if err != nil {
		return "", "", err
	}

	dispat = filepath.Join(dir, "dispat")
	if err := goBuild(goBin, dispat, filepath.Join(root, "services", "cli")); err != nil {
		return "", "", fmt.Errorf("building dispat: %w", err)
	}
	tsmark = filepath.Join(dir, "tsmark")
	if err := goBuild(goBin, tsmark, filepath.Join(root, "tests", "integration", "cmd", "tsmark")); err != nil {
		return "", "", fmt.Errorf("building tsmark: %w", err)
	}
	return dispat, tsmark, nil
}

func goBuild(goBin, out, dir string) error {
	cmd := exec.Command(goBin, "build", "-o", out, ".")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build in %s: %w\n%s", dir, err, b)
	}
	return nil
}

// monorepoRoot locates the directory holding go.work by walking up from this
// source file, so the build works regardless of the test binary's working
// directory (go test sets it to the package under test, which future
// subpackages of this module would otherwise get wrong).
func monorepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("harness: cannot determine caller for monorepoRoot")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("harness: go.work not found above %s", file)
		}
		dir = parent
	}
}
