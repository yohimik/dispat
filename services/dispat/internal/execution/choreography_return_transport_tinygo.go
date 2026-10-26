//go:build tinygo

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

const returnTinyGoOriginMismatchError = "execution: pinned return transport refuses a different endpoint"
const returnTinyGoHeaderLimitError = "execution: pinned return response headers exceed limit"
const returnTinyGoDefaultHTTPSPort = "443"
const returnTinyGoTCPNetwork = "tcp"
const returnTinyGoAbsentHost = ""
const returnTinyGoAbsentExplicitPort = ""
const returnTinyGoHeaderLimitDisabled = true
const returnTinyGoHeaderBytesExhausted int64 = 0
const returnTinyGoNoReadBytes = 0
const returnTinyGoMaxResponseHeaderBytes int64 = 1 << 20

// TinyGo's hosted net/http transport stores TLSClientConfig but dials HTTPS
// with its default TLS configuration. This transport uses the run's exact
// certificate pin for the actual handshake before sending an HTTP request.
type pinnedReturnTransport struct {
	host   string
	config *tls.Config
}

func newPinnedReturnTransport(host string, config *tls.Config) http.RoundTripper {
	return &pinnedReturnTransport{host: host, config: config}
}

func (t *pinnedReturnTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}
	if req.URL == nil || req.URL.Scheme != returnHTTPScheme || req.URL.Host != t.host ||
		(req.Host != returnTinyGoAbsentHost && req.Host != t.host) {
		return nil, fmt.Errorf(returnTinyGoOriginMismatchError)
	}
	port := req.URL.Port()
	if port == returnTinyGoAbsentExplicitPort {
		port = returnTinyGoDefaultHTTPSPort
	}
	addr := net.JoinHostPort(req.URL.Hostname(), port)
	conn, err := (&tls.Dialer{Config: t.config}).DialContext(req.Context(), returnTinyGoTCPNetwork, addr)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, err
	}
	var closeOnce sync.Once
	done := make(chan struct{})
	closeConn := func() {
		closeOnce.Do(func() {
			close(done)
			conn.Close()
		})
	}
	go func() {
		select {
		case <-req.Context().Done():
			closeConn()
		case <-done:
		}
	}()
	if err := req.Write(conn); err != nil {
		closeConn()
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, err
	}
	headerReader := &pinnedReturnHeaderReader{from: conn, remaining: returnTinyGoMaxResponseHeaderBytes}
	response, err := http.ReadResponse(bufio.NewReader(headerReader), req)
	if err != nil {
		closeConn()
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, err
	}
	headerReader.unlimited = returnTinyGoHeaderLimitDisabled
	response.Body = &pinnedReturnBody{ReadCloser: response.Body, closeConn: closeConn, ctx: req.Context()}
	return response, nil
}

type pinnedReturnHeaderReader struct {
	from      io.Reader
	remaining int64
	unlimited bool
}

func (r *pinnedReturnHeaderReader) Read(p []byte) (int, error) {
	if !r.unlimited {
		if r.remaining <= returnTinyGoHeaderBytesExhausted {
			return returnTinyGoNoReadBytes, fmt.Errorf(returnTinyGoHeaderLimitError)
		}
		if int64(len(p)) > r.remaining {
			p = p[:int(r.remaining)]
		}
	}
	n, err := r.from.Read(p)
	if !r.unlimited {
		r.remaining -= int64(n)
	}
	return n, err
}

type pinnedReturnBody struct {
	io.ReadCloser
	closeConn func()
	ctx       context.Context
}

func (b *pinnedReturnBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.ctx.Err() != nil {
		return n, b.ctx.Err()
	}
	return n, err
}

func (b *pinnedReturnBody) Close() error {
	b.closeConn()
	return b.ReadCloser.Close()
}
