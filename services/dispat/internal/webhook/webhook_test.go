package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	public "github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// The dispatcher's contract is the executor's safety: enqueueing never
// blocks, per-endpoint order holds, failures warn and nothing else. The
// tests run against real HTTP servers — the delivery path is the feature.

// captureServer records every request it receives and answers with the
// status codes queued on it (the last one repeating forever; default 200).
type captureServer struct {
	mu       sync.Mutex
	requests []capturedRequest
	statuses []int
	srv      *httptest.Server
}

type capturedRequest struct {
	Method string
	Header http.Header
	Body   []byte
}

func newCaptureServer(t *testing.T, statuses ...int) *captureServer {
	t.Helper()
	c := &captureServer{statuses: statuses}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{Method: r.Method, Header: r.Header.Clone(), Body: body})
		status := http.StatusOK
		if len(c.statuses) > 0 {
			status = c.statuses[0]
			if len(c.statuses) > 1 {
				c.statuses = c.statuses[1:]
			}
		}
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *captureServer) all() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

// dispatch runs a set of events through a dispatcher over the endpoints and
// closes it, so every test reads settled state.
func dispatch(t *testing.T, endpoints []Endpoint, events ...release.Event) {
	t.Helper()
	d := NewDispatcher(endpoints, nil, zerolog.Nop())
	for _, ev := range events {
		d.Event(ev)
	}
	d.Close(context.Background())
}

func endpointFor(c *captureServer) Endpoint {
	return Endpoint{Name: "test", URL: c.srv.URL, Method: "POST", Timeout: 5 * time.Second}
}

func TestDispatcherFansOut(t *testing.T) {
	// Two endpoints subscribed to the same event both receive their own
	// delivery of it.
	a, b := newCaptureServer(t), newCaptureServer(t)
	dispatch(t, []Endpoint{endpointFor(a), endpointFor(b)},
		release.Event{Name: release.EventPackagePublished, Package: "core"})

	require.Len(t, a.all(), 1)
	require.Len(t, b.all(), 1)
	// Payloads are identical; delivery ids are not — each endpoint's
	// deliveries are its own sequence.
	assert.Equal(t, a.all()[0].Body, b.all()[0].Body)
	assert.NotEqual(t, a.all()[0].Header.Get("X-Dispat-Delivery"), b.all()[0].Header.Get("X-Dispat-Delivery"))
}

func TestDispatcherFiltersEvents(t *testing.T) {
	// An endpoint receives exactly its subscriptions: exact names, families
	// and the empty filter behave as the config promises.
	all, pkgOnly, started := newCaptureServer(t), newCaptureServer(t), newCaptureServer(t)
	epAll, epPkg, epStarted := endpointFor(all), endpointFor(pkgOnly), endpointFor(started)
	epPkg.Events = []string{"package.*"}
	epStarted.Events = []string{public.WebhookReleaseStarted}
	dispatch(t, []Endpoint{epAll, epPkg, epStarted},
		release.Event{Name: release.EventReleaseStarted},
		release.Event{Name: release.EventStageStarted, Package: "core", Stage: "build"},
		release.Event{Name: release.EventPackagePublished, Package: "core"},
	)

	assert.Len(t, all.all(), 3, "an empty filter subscribes to everything")
	require.Len(t, pkgOnly.all(), 1, "package.* admits only the package family")
	assert.Equal(t, "package.published", pkgOnly.all()[0].Header.Get("X-Dispat-Event"))
	require.Len(t, started.all(), 1)
	assert.Equal(t, "release.started", started.all()[0].Header.Get("X-Dispat-Event"))
}

func TestDispatcherPreservesEndpointOrder(t *testing.T) {
	// One worker per endpoint: deliveries arrive in the order the events
	// happened, whatever the timing of the deliveries themselves.
	c := newCaptureServer(t)
	events := []release.Event{
		{Name: release.EventReleaseStarted},
		{Name: release.EventStageStarted, Package: "a", Stage: "build"},
		{Name: release.EventStageSucceeded, Package: "a", Stage: "build"},
		{Name: release.EventPackagePublished, Package: "a"},
		{Name: release.EventReleaseFinished},
	}
	dispatch(t, []Endpoint{endpointFor(c)}, events...)

	reqs := c.all()
	require.Len(t, reqs, len(events))
	for i, ev := range events {
		assert.Equal(t, ev.Name, reqs[i].Header.Get("X-Dispat-Event"), "delivery %d out of order", i)
	}
}

func TestDispatcherPayloadAndHeaders(t *testing.T) {
	// The payload is the event's JSON snapshot and the delivery headers name
	// what it is; the configured header arrives with its case and value
	// intact and may override a default, but never dispat's own headers.
	c := newCaptureServer(t)
	ep := endpointFor(c)
	ep.Method = "PUT"
	ep.Headers = []Header{
		{Name: "X-My-Token", Value: "sekrit"},
		{Name: "Content-Type", Value: "application/json+dispat"},
	}
	dispatch(t, []Endpoint{ep}, release.Event{
		Name: release.EventPackagePublished, Package: "core",
		Version: "1.4.0", PreviousVersion: "1.3.2", Channel: "stable",
		Tag: "core@1.4.0", Status: "published",
	})

	reqs := c.all()
	require.Len(t, reqs, 1)
	req := reqs[0]
	assert.Equal(t, "PUT", req.Method)
	assert.Equal(t, "sekrit", req.Header.Get("X-My-Token"))
	assert.Equal(t, "application/json+dispat", req.Header.Get("Content-Type"))
	assert.Equal(t, "dispat", req.Header.Get("User-Agent"))
	assert.Equal(t, "package.published", req.Header.Get("X-Dispat-Event"))
	assert.Len(t, req.Header.Get("X-Dispat-Delivery"), 32, "16 random bytes, hex encoded")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &payload))
	assert.Equal(t, "package.published", payload["event"])
	assert.Equal(t, "core", payload["package"])
	assert.Equal(t, "1.4.0", payload["version"])
	assert.Equal(t, "1.3.2", payload["previousVersion"])
	assert.Equal(t, "core@1.4.0", payload["tag"])
	assert.NotContains(t, payload, "failedStage", "empty fields stay out of the payload")
}

func TestDispatcherSignsWithSecret(t *testing.T) {
	// The signature is the standard sha256= HMAC over the exact body bytes,
	// so a receiver verifies with stock X-Hub-Signature-256 code — and a
	// configured header cannot spoof it.
	c := newCaptureServer(t)
	ep := endpointFor(c)
	ep.Secret = []byte("hook-secret")
	ep.Headers = []Header{{Name: "X-Dispat-Signature", Value: "sha256=forged"}}
	dispatch(t, []Endpoint{ep}, release.Event{Name: release.EventPackagePublished, Package: "core"})

	reqs := c.all()
	require.Len(t, reqs, 1)
	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write(reqs[0].Body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	assert.Equal(t, want, reqs[0].Header.Get("X-Dispat-Signature"))
}

func TestDispatcherUnsignedWithoutSecret(t *testing.T) {
	c := newCaptureServer(t)
	dispatch(t, []Endpoint{endpointFor(c)}, release.Event{Name: release.EventReleaseStarted})
	require.Len(t, c.all(), 1)
	assert.Empty(t, c.all()[0].Header.Get("X-Dispat-Signature"))
}

func TestDispatcherRetriesServerErrors(t *testing.T) {
	// A 500 can be a hiccup; the retry ladder gives it two more chances and
	// one delivery id ties the attempts together for the receiver.
	c := newCaptureServer(t, 500, 200)
	dispatch(t, []Endpoint{endpointFor(c)}, release.Event{Name: release.EventReleaseStarted})

	reqs := c.all()
	require.Len(t, reqs, 2)
	assert.Equal(t, reqs[0].Header.Get("X-Dispat-Delivery"), reqs[1].Header.Get("X-Dispat-Delivery"),
		"retries of one delivery share its id")
	assert.Equal(t, reqs[0].Body, reqs[1].Body, "each attempt sends the full body from the start")
}

func TestDispatcherDoesNotRetryClientErrors(t *testing.T) {
	// A 404 or a 401 will not improve on a second attempt: one try, one
	// warning, done.
	c := newCaptureServer(t, 404)
	dispatch(t, []Endpoint{endpointFor(c)}, release.Event{Name: release.EventReleaseStarted})
	assert.Len(t, c.all(), 1)
}

func TestDispatcherExhaustsRetries(t *testing.T) {
	// An endpoint that keeps failing costs exactly maxAttempts requests and
	// a warning; the dispatcher and the following deliveries carry on.
	c := newCaptureServer(t, 503)
	dispatch(t, []Endpoint{endpointFor(c)},
		release.Event{Name: release.EventReleaseStarted},
		release.Event{Name: release.EventReleaseFinished})
	assert.Len(t, c.all(), 2*maxAttempts)
}

func TestDispatcherEventNeverBlocks(t *testing.T) {
	// The whole design in one test: with the worker stuck on a hanging
	// endpoint and the queue full, Event still returns immediately, dropping
	// the overflow with a warning.
	release_ := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release_
	}))
	t.Cleanup(func() { once.Do(func() { close(release_) }); srv.Close() })

	ep := Endpoint{Name: "stuck", URL: srv.URL, Method: "POST", Timeout: time.Minute}
	d := NewDispatcher([]Endpoint{ep}, nil, zerolog.Nop())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// One delivery occupies the worker, queueSize fill the queue, and
		// the rest must drop without waiting.
		for i := 0; i < queueSize+10; i++ {
			d.Event(release.Event{Name: release.EventStageStarted, Package: "a", Stage: "build"})
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Event blocked on a stuck endpoint")
	}
	once.Do(func() { close(release_) })
	d.Close(context.Background())
}

func TestDispatcherCloseAbandonsHangingDelivery(t *testing.T) {
	// Close waits for in-flight deliveries only up to its bound — here the
	// context's deadline — and then abandons them: the command must end.
	release_ := make(chan struct{})
	defer close(release_)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release_
	}))
	t.Cleanup(srv.Close)

	ep := Endpoint{Name: "stuck", URL: srv.URL, Method: "POST", Timeout: time.Minute}
	d := NewDispatcher([]Endpoint{ep}, nil, zerolog.Nop())
	d.Event(release.Event{Name: release.EventReleaseStarted})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	d.Close(ctx)
	assert.Less(t, time.Since(start), 5*time.Second, "Close must not wait out the delivery")
}

func TestDispatcherEventAfterCloseIsDropped(t *testing.T) {
	c := newCaptureServer(t)
	d := NewDispatcher([]Endpoint{endpointFor(c)}, nil, zerolog.Nop())
	d.Close(context.Background())
	// Neither a panic (the queues are closed) nor a delivery.
	d.Event(release.Event{Name: release.EventReleaseStarted})
	d.Close(context.Background()) // idempotent
	assert.Empty(t, c.all())
}

func TestResolveWorkspaceMembership(t *testing.T) {
	// One webhook inherited by several packages is one endpoint — one queue,
	// one delivery order — whose membership records who it observes; a
	// package-introduced webhook observes its package alone and never the
	// run brackets.
	shared := public.WebhookConfig{URL: "https://shared.example.com"}
	own := public.WebhookConfig{URL: "https://own.example.com"}
	eps := ResolveWorkspace(
		[]public.WebhookConfig{shared},
		map[string][]public.WebhookConfig{
			"core": {shared, own},
			"web":  {shared},
		}, zerolog.Nop())

	require.Len(t, eps, 2, "the inherited webhook deduplicates to one endpoint")
	assert.Equal(t, "shared.example.com", eps[0].Name)
	assert.True(t, eps[0].RunLevel, "the root list receives the run brackets")
	assert.Equal(t, map[string]bool{"core": true, "web": true}, eps[0].Packages)
	assert.Equal(t, "own.example.com", eps[1].Name)
	assert.False(t, eps[1].RunLevel, "a package-introduced webhook never sees the run brackets")
	assert.Equal(t, map[string]bool{"core": true}, eps[1].Packages)
}

func TestDispatcherRoutesByPackage(t *testing.T) {
	// The routed dispatcher end to end: the root endpoint hears the run and
	// every covering package, the package endpoint hears its package alone.
	rootSink, coreSink := newCaptureServer(t), newCaptureServer(t)
	eps := ResolveWorkspace(
		[]public.WebhookConfig{{URL: rootSink.srv.URL}},
		map[string][]public.WebhookConfig{
			"core": {{URL: rootSink.srv.URL}, {URL: coreSink.srv.URL}},
			"web":  {{URL: rootSink.srv.URL}},
		}, zerolog.Nop())
	d := NewDispatcher(eps, nil, zerolog.Nop())
	d.Event(release.Event{Name: release.EventReleaseStarted})
	d.Event(release.Event{Name: release.EventPackagePublished, Package: "core"})
	d.Event(release.Event{Name: release.EventPackagePublished, Package: "web"})
	d.Close(context.Background())

	var rootEvents []string
	for _, r := range rootSink.all() {
		rootEvents = append(rootEvents, r.Header.Get("X-Dispat-Event"))
	}
	assert.Equal(t, []string{"release.started", "package.published", "package.published"}, rootEvents)
	require.Len(t, coreSink.all(), 1, "the package endpoint hears its package alone")
}

func TestResolveEnvGate(t *testing.T) {
	// `env: CI=true` disables the webhook when the condition does not match,
	// exactly as if it were not declared — and enables it when it does.
	t.Setenv("WEBHOOK_TEST_CI", "true")
	eps := Resolve([]public.WebhookConfig{
		{URL: "https://on.example.com", Env: "WEBHOOK_TEST_CI=true"},
		{URL: "https://off.example.com", Env: "WEBHOOK_TEST_CI!=true"},
		{URL: "https://unset.example.com", Env: "WEBHOOK_TEST_MISSING"},
	}, zerolog.Nop())
	require.Len(t, eps, 1)
	assert.Equal(t, "on.example.com", eps[0].Name)
}

func TestDispatcherFormat(t *testing.T) {
	// A format template replaces the payload: tokens render as their event
	// fields, JSON-escaped, so a template embedding them inside its own JSON
	// strings stays valid whatever the values carry — and the signature is
	// over the body actually sent.
	c := newCaptureServer(t)
	ep := endpointFor(c)
	ep.Format = `{"text": "release {package} {version} failed: {error}", "count": {failed}}`
	ep.Secret = []byte("hook-secret")
	dispatch(t, []Endpoint{ep}, release.Event{
		Name: release.EventPackageFailed, Package: "core", Version: "2.0.0",
		Error: `exit "status" 1` + "\\oops", Failed: 1,
	})

	reqs := c.all()
	require.Len(t, reqs, 1)
	var payload struct {
		Text  string `json:"text"`
		Count int    `json:"count"`
	}
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload), "the rendered template is valid JSON: %s", reqs[0].Body)
	assert.Equal(t, `release core 2.0.0 failed: exit "status" 1\oops`, payload.Text)
	assert.Equal(t, 1, payload.Count)

	mac := hmac.New(sha256.New, []byte("hook-secret"))
	mac.Write(reqs[0].Body)
	assert.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), reqs[0].Header.Get("X-Dispat-Signature"))
}

func TestFormatFieldsMatchVocabulary(t *testing.T) {
	// The renderer and the loader's validation must agree on the field set:
	// a field the validator accepts and the renderer does not know would
	// render an empty hole the load promised could not happen.
	progress := 3
	fields := formatFields(release.Event{Progress: &progress})
	for _, f := range public.WebhookFormatFields() {
		_, ok := fields[f]
		assert.True(t, ok, "validated field %q has no rendering", f)
	}
	assert.Len(t, fields, len(public.WebhookFormatFields()), "and the renderer offers nothing the validator refuses")
}

func TestResolveEndpoints(t *testing.T) {
	t.Setenv("WEBHOOK_TEST_SECRET", "s3cret")
	t.Setenv("WEBHOOK_TEST_TOKEN", "tok-123")
	eps := Resolve([]public.WebhookConfig{
		{URL: "https://hooks.example.com/dispat", Timeout: 3,
			Headers:   []public.WebhookHeader{{Name: "Authorization", Value: "Bearer $WEBHOOK_TEST_TOKEN"}},
			SecretEnv: "WEBHOOK_TEST_SECRET"},
		{Name: "named", URL: "https://b.example.com", Method: "PUT"},
	}, zerolog.Nop())

	require.Len(t, eps, 2)
	// The label falls back to the host, the timeout to its seconds, the
	// header value to its expanded form, the secret to the variable's bytes.
	assert.Equal(t, "hooks.example.com", eps[0].Name)
	assert.Equal(t, "POST", eps[0].Method)
	assert.Equal(t, 3*time.Second, eps[0].Timeout)
	assert.Equal(t, []Header{{Name: "Authorization", Value: "Bearer tok-123"}}, eps[0].Headers)
	assert.Equal(t, []byte("s3cret"), eps[0].Secret)
	assert.Equal(t, "named", eps[1].Name)
	assert.Equal(t, defaultTimeout, eps[1].Timeout)
}

func TestResolveEmptySecretDeliversUnsigned(t *testing.T) {
	// A named but empty variable warns and delivers unsigned rather than
	// signing with an empty key: the operator is told, the release goes on.
	t.Setenv("WEBHOOK_TEST_EMPTY", "")
	eps := Resolve([]public.WebhookConfig{
		{URL: "https://hooks.example.com", SecretEnv: "WEBHOOK_TEST_EMPTY"},
	}, zerolog.Nop())
	require.Len(t, eps, 1)
	assert.Nil(t, eps[0].Secret)
}
