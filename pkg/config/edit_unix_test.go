//go:build unix

package config

// The special files an edit must refuse without opening them. A named pipe is
// the dangerous one: opening it for reading blocks until a writer appears, so
// each refusal that reaches the read runs in a child process with a deadline,
// and a regression fails the test instead of hanging the suite.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const editFIFOHelperEnv = "DISPAT_CONFIG_EDIT_FIFO_HELPER"

// TestPrepareEditsRefusesANamedPipe: a FIFO wearing the config's name is
// refused by its type before it is opened, so `dispat compute --apply`
// cannot wait forever for a writer that never comes.
func TestPrepareEditsRefusesANamedPipe(t *testing.T) {
	if path := os.Getenv(editFIFOHelperEnv); path != "" {
		runEditFIFOHelper(t, path)
		return
	}
	path := filepath.Join(t.TempDir(), "app.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrepareEditsRefusesANamedPipe$", "-test.count=1")
	cmd.Env = append(os.Environ(), editFIFOHelperEnv+"="+path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("preparing an edit blocked opening a FIFO\n%s", out)
	}
	if err != nil {
		t.Fatalf("FIFO helper failed: %v\n%s", err, out)
	}
}

// runEditFIFOHelper is the child's half: it reports a wrong answer by exit
// status, and a hang by never exiting.
func runEditFIFOHelper(t *testing.T, path string) {
	_, err := PrepareEdits(t.Context(), path, []Edit{{KeyPath: []string{"tags"}, Value: []string{"new"}}})
	if err == nil || !strings.Contains(err.Error(), "refusing to rewrite a non-regular file") {
		fmt.Fprintf(os.Stderr, "PrepareEdits FIFO = %v\n", err)
		os.Exit(1)
	}
}

// TestPreparedEditRefusesTargetReplacedByANamedPipe: a FIFO swapped in after
// preparation is refused before the backup is written rather than silently
// replaced by the rename.
func TestPreparedEditRefusesTargetReplacedByANamedPipe(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.json", `{"tags":["old"]}`)
	p, err := PrepareEdits(t.Context(), path, []Edit{{KeyPath: []string{"tags"}, Value: []string{"new"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	if err := p.Commit(); err == nil || !strings.Contains(err.Error(), "refusing to rewrite a non-regular file") {
		t.Fatalf("commit = %v, want a non-regular refusal", err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("replacement = %v, %v; want the FIFO kept", info, err)
	}
	if _, err := os.Lstat(path + BackupSuffix); !os.IsNotExist(err) {
		t.Errorf("backup was written before refusal: %v", err)
	}
}
