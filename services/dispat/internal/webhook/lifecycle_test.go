package webhook

import (
	"context"
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
