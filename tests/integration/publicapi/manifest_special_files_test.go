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

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"
)

const specialFileHelperEnv = "DISPAT_SPECIAL_MANIFEST_HELPER"

// TestPublicAPIManifestReadersRefuseSpecialFiles proves a manifest-shaped FIFO
// cannot make either public API wait for a producer. Each call runs in a child
// process so a regression fails within the deadline instead of hanging the
// integration suite.
func TestPublicAPIManifestReadersRefuseSpecialFiles(t *testing.T) {
	if action := os.Getenv(specialFileHelperEnv); action != "" {
		runSpecialFileHelper(action, os.Getenv("DISPAT_SPECIAL_MANIFEST_PATH"))
		return
	}

	for _, action := range []string{"scan", "write"} {
		t.Run(action+" fifo", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "package.json")
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skipf("cannot create FIFO: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := []string{"-test.run=^TestPublicAPIManifestReadersRefuseSpecialFiles$"}
			if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
				args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
			}
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), specialFileHelperEnv+"="+action,
				"DISPAT_SPECIAL_MANIFEST_PATH="+path)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s blocked opening a FIFO", action)
			}
			if err != nil {
				t.Fatalf("%s FIFO helper failed: %v\n%s", action, err, out)
			}
		})
	}
}

func runSpecialFileHelper(action, path string) {
	switch action {
	case "scan":
		mans, err := scanner.ScanRoot(context.Background(), filepath.Dir(path))
		if err != nil || len(mans) != 0 {
			fmt.Fprintf(os.Stderr, "ScanRoot FIFO = %+v, %v\n", mans, err)
			os.Exit(1)
		}
	case "write":
		if _, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", nil); err == nil ||
			!strings.Contains(err.Error(), "non-regular file") {
			fmt.Fprintf(os.Stderr, "writer FIFO refusal = %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown helper action %q\n", action)
		os.Exit(1)
	}
}

// TestPublicAPIWriterRefusesAManifestDirectory pins the same admission rule
// for another non-regular node without relying on the host's FIFO support.
func TestPublicAPIWriterRefusesAManifestDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (writer.LocalWriterx{}).Rewrite(path, "2.0.0", nil); err == nil ||
		!strings.Contains(err.Error(), "non-regular file") {
		t.Fatalf("writer directory refusal = %v", err)
	}
}
