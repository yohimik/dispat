package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deaf is the TinyGo client as a RoundTripper: it holds the calling goroutine
// until release closes and consults neither the request's context nor any
// timeout. closed reports what happened to the response it finally produced.
type deaf struct {
	release chan struct{}
	closed  chan struct{}
}

func newDeaf() *deaf {
	return &deaf{release: make(chan struct{}), closed: make(chan struct{})}
}

func (d *deaf) RoundTrip(*http.Request) (*http.Response, error) {
	<-d.release
	return &http.Response{StatusCode: http.StatusOK, Body: closeSpy{Reader: strings.NewReader("late"), closed: d.closed}}, nil
}

type closeSpy struct {
	io.Reader
	closed chan struct{}
}

func (c closeSpy) Close() error {
	close(c.closed)
	return nil
}

func TestDoReturnsWhatTheClientReturns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "answer")
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := Do(&http.Client{Timeout: time.Minute}, req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "answer", string(body), "the client's deadline must not end a body that is still being read")

	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:1/", nil)
	require.NoError(t, err)
	resp, err = Do(&http.Client{}, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	var opErr *net.OpError
	assert.ErrorAs(t, err, &opErr, "a transport failure passes through unchanged")
}

// A transport that never looks at the context must not be able to hold the
// caller past a cancellation, which is what an interrupt is.
func TestDoReturnsWhenTheContextEndsUnderADeafTransport(t *testing.T) {
	transport := newDeaf()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid/releases?token=secret", nil)
	require.NoError(t, err)

	returned := make(chan error, 1)
	go func() {
		resp, err := Do(&http.Client{Transport: transport}, req)
		assert.Nil(t, resp)
		returned <- err
	}()
	select {
	case err := <-returned:
		t.Fatalf("Do returned before the context ended: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()

	select {
	case err = <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Do stayed blocked in a transport that ignores its context")
	}
	require.ErrorIs(t, err, context.Canceled)
	var urlErr *url.Error
	require.ErrorAs(t, err, &urlErr, "callers strip the URL off a *url.Error, as they do for net/http's own")
	assert.Equal(t, "Post", urlErr.Op)
	assert.False(t, urlErr.Timeout())

	// The abandoned round trip finishes on its own, and its response is closed.
	close(transport.release)
	select {
	case <-transport.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the late response was never closed")
	}
}

// The client's Timeout is a promise too, and the same transport ignores it.
func TestDoHonoursTheClientTimeoutUnderADeafTransport(t *testing.T) {
	transport := newDeaf()
	defer close(transport.release)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid/", nil)
	require.NoError(t, err)
	// A request built by hand may leave the method empty, which means GET.
	req.Method = ""

	start := time.Now()
	resp, err := Do(&http.Client{Transport: transport, Timeout: 100 * time.Millisecond}, req)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
	var urlErr *url.Error
	require.True(t, errors.As(err, &urlErr))
	assert.Equal(t, "Get", urlErr.Op)
	assert.True(t, urlErr.Timeout(), "a timeout must answer Timeout, as net/http's does")
}
