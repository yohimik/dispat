// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const returnTransportTestSecret = "pinned-transport-secret"
const returnTransportTestResponseWait = 2 * time.Second
const returnTransportTestCancellationWait = 100 * time.Millisecond
const returnTransportTestMaxResultBytes int64 = 1024

func newReturnTransportTestClient(t *testing.T, endpoint, pin string) *ChoreographyReturnClient {
	t.Helper()
	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	client, err := NewChoreographyReturnClient(ReturnClientOptions{BaseURL: endpoint,
		PinnedCertSHA256: pin, Signer: signer,
		Binding: ChoreographyRunBinding{Run: "transport-run", Generation: "transport-generation",
			PlanDigest: "transport-plan", GraphDigest: "transport-graph"},
		MaxResultBytes: returnTransportTestMaxResultBytes})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func returnTransportTestPin(server *httptest.Server) string {
	sum := sha256.Sum256(server.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func TestPinnedReturnTransportTLSIdentityAndCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			select {
			case <-r.Context().Done():
			case <-time.After(returnTransportTestResponseWait):
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newReturnTransportTestClient(t, server.URL, returnTransportTestPin(server))
	response, err := client.client.Get(server.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.NoError(t, response.Body.Close())

	wrongPin := strings.Repeat("0", sha256.Size*2)
	forged := newReturnTransportTestClient(t, server.URL, wrongPin)
	_, err = forged.client.Get(server.URL)
	require.ErrorContains(t, err, returnCertificateMismatchError)

	signer, err := NewSigner(returnTransportTestSecret)
	require.NoError(t, err)
	_, err = NewChoreographyReturnClient(ReturnClientOptions{BaseURL: server.URL,
		Signer: signer, Binding: client.binding, MaxResultBytes: returnTransportTestMaxResultBytes})
	require.ErrorContains(t, err, returnTLSIdentityInvalidError)

	ctx, cancel := context.WithTimeout(t.Context(), returnTransportTestCancellationWait)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/wait", nil)
	require.NoError(t, err)
	_, err = client.client.Do(request)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "request error: %v", err)
}

func TestPinnedReturnTransportRefusesRedirect(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client := newReturnTransportTestClient(t, redirect.URL, returnTransportTestPin(redirect))
	_, err := client.client.Get(redirect.URL)
	require.ErrorContains(t, err, returnRedirectRefusedError)
	require.Zero(t, destinationRequests.Load())
}
