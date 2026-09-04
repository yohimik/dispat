// Package harness is the black-box test support library for the integration
// suite: it builds the real dispat binary (never the internal cli.Run entry
// point — this module deliberately cannot import services/dispat/internal/*,
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
	dir            string // the temp build dir, removed by CleanupBinaries
	dispat, tsmark string
	err            error
}

// CleanupBinaries removes the once-per-run build directory. The sync.Once
// cache outlives any single test, so no t.Cleanup can own the directory; the
// test package's TestMain calls this after m.Run() instead, and a build that
// never happened is a no-op.
func CleanupBinaries() {
	if binaries.dir != "" {
		_ = os.RemoveAll(binaries.dir)
	}
}

// Build compiles the dispat CLI (from services/dispat) and the tsmark timing
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

// coverDir is the value of DISPAT_COVERDIR: when set, the dispat binary is
// built with coverage instrumentation and every invocation writes its
// counters there (via GOCOVERDIR), so the black-box suite contributes to the
// repository's coverage profile — the flows only this suite exercises would
// otherwise count for nothing. Empty (the default) means a plain build.
// Convert the counters afterwards with
//
//	go tool covdata textfmt -i="$DISPAT_COVERDIR" -o=cover-integration.out
func coverDir() string { return os.Getenv("DISPAT_COVERDIR") }

// prebuiltBin is the value of DISPAT_TEST_BINARY: a dispat binary to drive
// instead of building one from the working tree. The release build sets it to
// run the smoke suite against the exact artefact about to be exported (see
// services/dispat/Dockerfile's test stage) — a fresh native build passing
// says nothing about the cross-compiled bytes that ship. Empty (the default)
// means the suite builds its own, as it always has.
func prebuiltBin() string { return os.Getenv("DISPAT_TEST_BINARY") }

// go test -race instruments the test process, but not subprocesses built by
// this harness. The race pass sets this flag so every dispat binary it drives
// is instrumented too.
func raceBuild() bool { return os.Getenv("DISPAT_TEST_RACE") == "1" }

func build() (dispat, tsmark string, err error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", "", fmt.Errorf("go toolchain not found on PATH: %w", err)
	}
	dir, err := os.MkdirTemp("", "dispat-it-bin-")
	if err != nil {
		return "", "", err
	}
	binaries.dir = dir

	// atomic matches the unit profiles, so the text profiles concatenate into
	// one; -coverpkg=./... mirrors the unit job's scope for the CLI module.
	var coverArgs []string
	if coverDir() != "" {
		coverArgs = []string{"-cover", "-covermode=atomic", "-coverpkg=./..."}
	}
	if raceBuild() {
		coverArgs = append(coverArgs, "-race")
	}
	if pre := prebuiltBin(); pre != "" {
		// A prebuilt binary carries no instrumentation, so counters asked for
		// here would silently never arrive and the coverage profile would
		// shrink with nothing failing. Refusing the combination keeps that a
		// loud configuration error instead.
		if coverDir() != "" {
			return "", "", fmt.Errorf("DISPAT_TEST_BINARY and DISPAT_COVERDIR are mutually exclusive: a prebuilt binary has no coverage instrumentation")
		}
		dispat, err = filepath.Abs(pre)
		if err != nil {
			return "", "", fmt.Errorf("resolving DISPAT_TEST_BINARY: %w", err)
		}
		if info, statErr := os.Stat(dispat); statErr != nil || info.IsDir() {
			return "", "", fmt.Errorf("DISPAT_TEST_BINARY is not a runnable file: %s", dispat)
		}
	} else {
		// Only a from-source build needs the workspace: the CLI lives in a
		// sibling module go.work stitches in. The prebuilt path deliberately
		// never asks — the release build's test stage has no go.work at all,
		// which is exactly the published module's own build environment.
		root, rootErr := monorepoRoot()
		if rootErr != nil {
			return "", "", rootErr
		}
		dispat = filepath.Join(dir, "dispat")
		if err := goBuild(goBin, dispat, filepath.Join(root, "services", "dispat"), coverArgs...); err != nil {
			return "", "", fmt.Errorf("building dispat: %w", err)
		}
	}
	tsmark = filepath.Join(dir, "tsmark")
	if err := goBuild(goBin, tsmark, filepath.Join(moduleRoot(), "cmd", "tsmark")); err != nil {
		return "", "", fmt.Errorf("building tsmark: %w", err)
	}
	return dispat, tsmark, nil
}

// moduleRoot is this module's own folder — where cmd/tsmark lives — derived
// from this source file rather than from go.work, because tsmark must build
// wherever the module itself can: the workspace has a go.work, the release
// build's test stage does not, and tsmark belongs to both.
func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// stamped caches the version-stamped builds, one per version, for the same
// reason binaries caches the plain one: a `go build` per test case is not a
// price a suite this size can pay twice.
var stamped struct {
	sync.Mutex
	byVersion map[string]string
}

// BuildVersioned compiles dispat with a version baked in, exactly the way
// services/dispat/Dockerfile does for a release, and returns the path.
//
// Some behaviour only exists on a released binary: it is the one that reports
// a real version, checks for updates, and can replace itself. An unstamped
// build says "dev", which is deliberately none of those things, so those
// scenarios need this.
func BuildVersioned(t testing.TB, version string) string {
	t.Helper()
	// The plain build owns the temp directory and its cleanup, so ask for it
	// first and put the stamped binaries alongside.
	Build(t)

	stamped.Lock()
	defer stamped.Unlock()
	if path, ok := stamped.byVersion[version]; ok {
		return path
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go toolchain not found on PATH: %v", err)
	}
	root, err := monorepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(binaries.dir, "dispat-"+version)
	args := []string{"-ldflags",
		"-X github.com/yohimik/dispat/services/dispat/internal/cli.Version=" + version}
	if coverDir() != "" {
		args = append(args, "-cover", "-covermode=atomic", "-coverpkg=./...")
	}
	if raceBuild() {
		args = append(args, "-race")
	}
	if err := goBuild(goBin, out, filepath.Join(root, "services", "dispat"), args...); err != nil {
		t.Fatalf("building dispat %s: %v", version, err)
	}
	if stamped.byVersion == nil {
		stamped.byVersion = map[string]string{}
	}
	stamped.byVersion[version] = out
	return out
}

func goBuild(goBin, out, dir string, extraArgs ...string) error {
	args := append(append([]string{"build"}, extraArgs...), "-o", out, ".")
	cmd := exec.Command(goBin, args...)
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
