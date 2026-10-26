//go:build !tinygo

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"crypto/tls"
	"net/http"
)

func newPinnedReturnTransport(_ string, config *tls.Config) http.RoundTripper {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = config
	return transport
}
