//go:build darwin || linux

package publicapi_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yohimik/dispat/pkg/config"
)

const configEditFIFOHelperEnv = "DISPAT_CONFIG_EDIT_FIFO_HELPER"

// TestPublicAPIConfigEditRefusesANamedPipe proves a configuration-shaped FIFO
// cannot make ApplyEdits wait for a writer: the path is refused by its type
// before it is opened, and nothing is written beside it. The call runs in a
// child process so a regression fails within the deadline instead of hanging
// the integration suite.
func TestPublicAPIConfigEditRefusesANamedPipe(t *testing.T) {
	if path := os.Getenv(configEditFIFOHelperEnv); path != "" {
		runConfigEditFIFOHelper(path)
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dispat.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"-test.run=^TestPublicAPIConfigEditRefusesANamedPipe$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), configEditFIFOHelperEnv+"="+path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("ApplyEdits blocked opening a FIFO")
	}
	if err != nil {
		t.Fatalf("FIFO helper failed: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a refused edit wrote beside the FIFO: %v", entries)
	}
}

func runConfigEditFIFOHelper(path string) {
	err := config.ApplyEdits(context.Background(), path, []config.Edit{{KeyPath: []string{"logLevel"}, Value: "debug"}})
	if err == nil || !strings.Contains(err.Error(), "refusing to rewrite a non-regular file") {
		fmt.Fprintf(os.Stderr, "ApplyEdits FIFO refusal = %v\n", err)
		os.Exit(1)
	}
}
