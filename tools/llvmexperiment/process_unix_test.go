//go:build !windows

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCancellationStopsTermIgnoringDescendantsBeforeReturning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "heartbeat")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := makeCommand(ctx, command{Name: os.Args[0], Args: []string{"-test.run=^TestCommandTerminationHelper$"},
		Env: []string{"LLVM_TEST_PROCESS_ROLE=parent", "LLVM_TEST_HEARTBEAT=" + marker}, Output: io.Discard})
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := time.Now()
	cancel()
	if err := process.Wait(); err == nil {
		t.Fatal("canceled process succeeded")
	}
	if time.Since(started) > processTerminationGrace+commandWaitDelay+time.Second {
		t.Fatal("process group cleanup exceeded its bound")
	}
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatal("a TERM-ignoring descendant survived command cancellation")
	}
}

func TestCommandTerminationHelper(t *testing.T) {
	role := os.Getenv("LLVM_TEST_PROCESS_ROLE")
	if role == "" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if role == "parent" {
		child := exec.Command(os.Args[0], "-test.run=^TestCommandTerminationHelper$")
		child.Env = append(os.Environ(), "LLVM_TEST_PROCESS_ROLE=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = child.Wait()
		os.Exit(3)
	}
	marker, err := os.OpenFile(os.Getenv("LLVM_TEST_HEARTBEAT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, privateFileMode)
	if err != nil {
		os.Exit(4)
	}
	for {
		if _, err := marker.WriteString("."); err != nil {
			os.Exit(5)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
