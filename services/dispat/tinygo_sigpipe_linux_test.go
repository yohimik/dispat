//go:build tinygo && linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package main

import (
	"errors"
	"net"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestTinyGoLinuxClosedSocketReturnsBrokenPipe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			tcp := conn.(*net.TCPConn)
			_ = tcp.SetLinger(0)
			_ = tcp.Close()
		}
		close(closed)
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	<-closed
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, err = conn.Write([]byte("closed peer"))
		if errors.Is(err, syscall.EPIPE) {
			select {
			case <-tinyGoLinuxBrokenPipeSignals:
				return
			case <-time.After(time.Second):
				t.Fatal("socket returned EPIPE but no SIGPIPE was caught")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("closed socket did not return EPIPE: %v", err)
}

func TestTinyGoLinuxChildRetainsDefaultBrokenPipeDisposition(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "kill -PIPE $$; echo unexpectedly-alive")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("child did not terminate by signal: error=%v output=%q", err, output)
	}
	status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGPIPE {
		t.Fatalf("child inherited altered SIGPIPE behavior: status=%v output=%q", exitErr.ProcessState.Sys(), output)
	}
}
