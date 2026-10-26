//go:build tinygo

// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const returnTinyGoTestOverlongHeader = "X-Long"
const returnTinyGoTestUnfinishedBodyBytes = "one"
const returnTinyGoTestUnfinishedContentLength = "99999999"
const returnTinyGoTestCloseDeadline = 500 * time.Millisecond

type returnTinyGoCloseTrackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *returnTinyGoCloseTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

func TestPinnedTinyGoReturnTransportBoundsHeadersAndClosesBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(returnTinyGoTestOverlongHeader,
			strings.Repeat("a", int(returnTinyGoMaxResponseHeaderBytes)+1))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := newReturnTransportTestClient(t, server.URL, returnTransportTestPin(server))
	_, err := client.client.Get(server.URL)
	require.ErrorContains(t, err, returnTinyGoHeaderLimitError)

	body := &returnTinyGoCloseTrackingBody{Reader: strings.NewReader("request")}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, body)
	require.NoError(t, err)
	wrong := newReturnTransportTestClient(t, server.URL, strings.Repeat("0", 64))
	_, err = wrong.client.Do(request)
	require.ErrorContains(t, err, returnCertificateMismatchError)
	require.True(t, body.closed.Load(), "the request body closes even when TLS fails")
}

func TestPinnedTinyGoReturnTransportUnfinishedBodyCloseIsPrompt(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", returnTinyGoTestUnfinishedContentLength)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(returnTinyGoTestUnfinishedBodyBytes))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer server.Close()
	defer close(release)
	client := newReturnTransportTestClient(t, server.URL, returnTransportTestPin(server))
	response, err := client.client.Get(server.URL)
	require.NoError(t, err)
	closed := make(chan error, 1)
	go func() { closed <- response.Body.Close() }()
	select {
	case <-closed:
	case <-time.After(returnTinyGoTestCloseDeadline):
		t.Fatal("closing an unfinished response waited for the remote body")
	}
}
