//go:build tinygo && linux

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package main

import (
	"os"
	"os/signal"
	"syscall"
)

const tinyGoLinuxBrokenPipeSignalBufferCapacity = 1

// The pinned TinyGo Linux net stack writes sockets with syscall.Write. A
// closed peer can therefore send SIGPIPE before Write returns EPIPE. Catching
// the signal keeps the worker alive to reconcile its signed return receipt.
// Caught dispositions reset to the default on exec, so task commands retain
// their ordinary SIGPIPE behavior.
var tinyGoLinuxBrokenPipeSignals = make(chan os.Signal, tinyGoLinuxBrokenPipeSignalBufferCapacity)

func init() {
	signal.Notify(tinyGoLinuxBrokenPipeSignals, syscall.SIGPIPE)
}
