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

const (
	tinyGoLinuxProbeListenError        = "listen for closed-socket probe: %v"
	tinyGoLinuxProbeDialError          = "dial closed-socket probe: %v"
	tinyGoLinuxProbeAcceptError        = "accept closed-socket probe: %v"
	tinyGoLinuxProbeAcceptTimeoutError = "accept closed-socket probe timed out"
	tinyGoLinuxProbeLingerError        = "set closed-socket probe linger: %v"
	tinyGoLinuxProbeCloseError         = "close closed-socket probe peer: %v"
)

func TestTinyGoLinuxClosedSocketReturnsBrokenPipe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf(tinyGoLinuxProbeListenError, err)
	}
	defer listener.Close()
	type acceptedConnection struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan acceptedConnection)
	abort := make(chan struct{})
	defer close(abort)
	go func() {
		conn, acceptErr := listener.Accept()
		select {
		case accepted <- acceptedConnection{conn: conn, err: acceptErr}:
		case <-abort:
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		select {
		case result := <-accepted:
			if result.conn != nil {
				_ = result.conn.Close()
			}
		case <-time.After(time.Second):
		}
		t.Fatalf(tinyGoLinuxProbeDialError, err)
	}
	defer conn.Close()
	var server net.Conn
	select {
	case result := <-accepted:
		if result.err != nil {
			t.Fatalf(tinyGoLinuxProbeAcceptError, result.err)
		}
		server = result.conn
	case <-time.After(time.Second):
		t.Fatal(tinyGoLinuxProbeAcceptTimeoutError)
	}
	// The peer reset must wait for Dial; this test exercises the later write.
	tcp := server.(*net.TCPConn)
	if err := tcp.SetLinger(0); err != nil {
		_ = server.Close()
		t.Fatalf(tinyGoLinuxProbeLingerError, err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf(tinyGoLinuxProbeCloseError, err)
	}
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
