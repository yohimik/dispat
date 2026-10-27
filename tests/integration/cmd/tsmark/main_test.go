// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tsmarkFailureProbeLabel = "failed-probe"
	tsmarkSuccessProbeLabel = "successful-probe"
	tsmarkFailureExitCode   = 7
	tsmarkFailureCommand    = "exit 7"
	tsmarkSuccessCommand    = "exit 0"
)

// A rendezvous timeout must fail the build and still leave a complete measured
// interval. Otherwise a failed peer could disappear from concurrency evidence.
func TestCommandFailureClosesMeasuredInterval(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "tsmark")
	build := exec.Command("go", "build", "-o", probe, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building timing probe: %v\n%s", err, output)
	}
	for _, example := range []struct {
		label   string
		command string
		exit    int
	}{
		{tsmarkSuccessProbeLabel, tsmarkSuccessCommand, 0},
		{tsmarkFailureProbeLabel, tsmarkFailureCommand, tsmarkFailureExitCode},
	} {
		t.Run(example.label, func(t *testing.T) {
			logFile := filepath.Join(t.TempDir(), "timeline.log")
			command := exec.Command(probe, logFile, example.label, "0",
				tsmarkCommandSeparator, "sh", "-c", example.command)
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if example.exit == 0 && err != nil ||
				example.exit != 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != example.exit) {
				t.Fatalf("command must retain exit %d: err=%v output=%s", example.exit, err, output)
			}
			data, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], example.label+" start ") ||
				!strings.HasPrefix(lines[1], example.label+" end ") {
				t.Fatalf("command must have start and end marks: %q", data)
			}
		})
	}
}
