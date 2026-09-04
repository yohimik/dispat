package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

type failedTransport struct{}

func (failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection unavailable")
}

func TestTransportFailureDoesNotLogWebhookCredentials(t *testing.T) {
	var logs bytes.Buffer
	d := NewDispatcher(nil, &http.Client{Transport: failedTransport{}}, zerolog.New(&logs))
	defer d.Close(context.Background())
	ep := Endpoint{Name: "notifications", URL: "https://host.test/private-hook-token?key=secret-query", Method: "POST", Timeout: time.Second}
	_, err := d.attempt(ep, delivery{body: []byte("{}")})
	require.ErrorContains(t, err, "connection unavailable")
	assert.NotContains(t, err.Error(), "private-hook-token")
	assert.NotContains(t, err.Error(), "secret-query")
	// A non-retryable response exercises the final warning as well.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	d.client = srv.Client()
	ep.URL = srv.URL + "/private-hook-token?key=secret-query"
	d.deliver(ep, delivery{body: []byte("{}")})
	assert.Contains(t, logs.String(), "webhook delivery failed")
	assert.NotContains(t, logs.String(), "private-hook-token")
	assert.NotContains(t, logs.String(), "secret-query")
}

func TestCloseCancelsRequestsAndDiscardsQueuedDeliveries(t *testing.T) {
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	d := NewDispatcher([]Endpoint{{Name: "slow", URL: srv.URL, Method: "POST", Timeout: time.Minute}},
		srv.Client(), zerolog.Nop())
	d.Event(release.Event{Name: release.EventPackagePublished, Package: "core"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		d.cancel()
		t.Fatal("the first delivery never started")
	}
	for range 10 {
		d.Event(release.Event{Name: release.EventPackagePublished, Package: "web"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Close(ctx)
	finished := make(chan struct{})
	go func() { d.wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("workers survived the flush deadline")
	}
	require.EqualValues(t, 1, requests.Load(), "queued deliveries must not start after abandonment")
	assert.Zero(t, d.pending.Load(), "abandoned payloads have been released")
}
