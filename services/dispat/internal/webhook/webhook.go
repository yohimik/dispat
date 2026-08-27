// Package webhook delivers release-progress events to the HTTP endpoints the
// `webhooks` config declares.
//
// The dispatcher is an observer in the strictest sense: enqueueing never
// blocks, deliveries run on their own goroutines detached from the run's
// context, and no outcome of a delivery — a refusal, a timeout, a full queue
// — is ever anything but a W239 warning. A release behaves identically with
// every webhook unreachable and with none configured.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	// defaultTimeout bounds one delivery attempt when the webhook does not
	// state its own. Shorter than the GitHub client's: a webhook receiver is
	// a listener, and a slow one must cost the flush little.
	defaultTimeout = 10 * time.Second
	// maxAttempts and retryDelay shape the retry ladder: 3 attempts, the
	// delay doubling between them (500ms, 1s).
	maxAttempts = 3
	retryDelay  = 500 * time.Millisecond
	// queueSize bounds each endpoint's delivery queue. A release emits a
	// handful of events per package, so 128 is comfortably more than any
	// realistic run — and when it is not, dropping the newest delivery with a
	// warning is the whole design: the queue must never grow without bound
	// and the enqueue must never wait.
	queueSize = 128
	// flushTimeout bounds Close: how long the end of a release waits for
	// deliveries still in flight before abandoning them.
	flushTimeout = 15 * time.Second
	// maxDrainBody caps how much of a response body is read before the
	// connection is released for reuse; a webhook answer past the status
	// line carries nothing dispat wants.
	maxDrainBody = 4 << 10
)

// delivery is one enqueued payload: the marshalled event, immutable, plus the
// identity the receiver can deduplicate on. The id stays the same across
// retries of one delivery and differs between endpoints.
type delivery struct {
	event string
	id    string
	body  []byte
}

// worker is one endpoint's delivery lane: its own queue and its own
// goroutine, so per-endpoint order is preserved and a slow endpoint never
// delays another.
type worker struct {
	ep    Endpoint
	queue chan delivery
}

// Dispatcher fans release events out to every subscribed endpoint. It
// implements release.Observer; see the package comment for the contract.
type Dispatcher struct {
	client  *http.Client
	log     zerolog.Logger
	workers []*worker
	wg      sync.WaitGroup
	// mu serialises enqueues against Close: Event holds it shared while
	// sending, Close holds it exclusively while closing the queues, so a send
	// on a closed channel cannot happen.
	mu      sync.RWMutex
	closed  bool
	once    sync.Once
	pending atomic.Int64
}

// NewDispatcher starts one delivery worker per endpoint. A nil client gets a
// default one; all endpoints share it, so connections are reused across
// deliveries. The returned dispatcher must be released with Close.
func NewDispatcher(endpoints []Endpoint, client *http.Client, log zerolog.Logger) *Dispatcher {
	if client == nil {
		client = &http.Client{}
	}
	d := &Dispatcher{client: client, log: log}
	for _, ep := range endpoints {
		w := &worker{ep: ep, queue: make(chan delivery, queueSize)}
		d.workers = append(d.workers, w)
		d.wg.Add(1)
		go d.work(w)
	}
	log.Debug().Int("webhooks", len(endpoints)).Msg("webhook dispatcher started")
	return d
}

// Event enqueues the event for every subscribed endpoint and returns
// immediately. A queue with no room drops the delivery with a warning rather
// than waiting: the caller is the release executor, and nothing here may ever
// hold it up.
func (d *Dispatcher) Event(ev release.Event) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		d.log.Debug().Str("event", ev.Name).Msg("webhook event after close, dropped")
		return
	}
	defaultBody, err := json.Marshal(ev)
	if err != nil { // an Event is plain data; kept because a silent drop would be worse
		d.log.Warn().Str("code", plan.CodeWebhookFailed).Err(err).Str("event", ev.Name).
			Msg("webhook event could not be encoded")
		return
	}
	// The template field values are built once per event, and only when some
	// subscribed endpoint actually formats.
	var fields map[string]string
	for _, w := range d.workers {
		if !w.ep.subscribes(ev.Name) || !w.ep.covers(ev.Package) {
			continue
		}
		body := defaultBody
		if w.ep.Format != "" {
			if fields == nil {
				fields = formatFields(ev)
			}
			body = []byte(public.ExpandWebhookFormat(w.ep.Format, func(f string) string { return fields[f] }))
		}
		del := delivery{event: ev.Name, id: deliveryID(), body: body}
		select {
		case w.queue <- del:
			d.pending.Add(1)
			d.log.Debug().Str("webhook", w.ep.Name).Str("event", ev.Name).Str("delivery", del.id).
				Msg("webhook delivery enqueued")
		default:
			d.log.Warn().Str("code", plan.CodeWebhookFailed).Str("webhook", w.ep.Name).
				Str("url", w.ep.URL).Str("event", ev.Name).
				Msg("webhook delivery dropped, queue is full")
		}
	}
}

// Close stops accepting events and waits for the queued deliveries to drain,
// bounded by flushTimeout (or the context's earlier deadline). Deliveries
// still in flight at the bound are abandoned with a warning — a listener that
// misses a notification is never worth holding the command open for.
// Idempotent; only the first call does anything.
func (d *Dispatcher) Close(ctx context.Context) {
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		for _, w := range d.workers {
			close(w.queue)
		}
		d.mu.Unlock()

		done := make(chan struct{})
		go func() {
			d.wg.Wait()
			close(done)
		}()
		timer := time.NewTimer(flushTimeout)
		defer timer.Stop()
		select {
		case <-done:
			d.log.Debug().Msg("webhook deliveries flushed")
		case <-ctx.Done():
			d.abandon()
		case <-timer.C:
			d.abandon()
		}
	})
}

func (d *Dispatcher) abandon() {
	d.log.Warn().Str("code", plan.CodeWebhookFailed).Int64("abandoned", d.pending.Load()).
		Msg("webhook deliveries abandoned, flush deadline reached")
}

// work drains one endpoint's queue until Close closes it.
func (d *Dispatcher) work(w *worker) {
	defer d.wg.Done()
	for del := range w.queue {
		d.deliver(w.ep, del)
		d.pending.Add(-1)
	}
}

// deliver walks the retry ladder for one delivery. Retries cover the failures
// a later attempt can outlive — a transport error, a 5xx, a 429 — and stop on
// anything else: a 404 or a 401 will not improve in a second.
func (d *Dispatcher) deliver(ep Endpoint, del delivery) {
	log := d.log.With().Str("webhook", ep.Name).Str("event", del.event).Str("delivery", del.id).Logger()
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(retryDelay << (attempt - 2))
		}
		status, err := d.attempt(ep, del)
		log.Trace().Int("attempt", attempt).Int("status", status).Err(err).Msg("webhook delivery attempt")
		if err == nil {
			log.Trace().Dur("duration", time.Since(start)).Msg("webhook delivered")
			return
		}
		lastErr = err
		if !retryableStatus(status) {
			break
		}
	}
	log.Warn().Str("code", plan.CodeWebhookFailed).Str("url", ep.URL).Err(lastErr).
		Msg("webhook delivery failed")
}

// retryableStatus reports whether a later attempt could go differently:
// 0 is a transport error (no response at all).
func retryableStatus(status int) bool {
	return status == 0 || status == 429 || status >= 500
}

// attempt sends the delivery once, bounded by the endpoint's timeout. Any 2xx
// is success; everything else is an error carrying the status. The request is
// built fresh per attempt — the body bytes are immutable, so each attempt
// reads them from the start.
func (d *Dispatcher) attempt(ep Endpoint, del delivery) (status int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), ep.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, ep.Method, ep.URL, bytes.NewReader(del.body))
	if err != nil {
		return 0, err
	}
	// The defaults first, then the configured headers — which may override
	// them — then dispat's own delivery headers, which nothing overrides: a
	// receiver verifying the signature must be able to trust who set it.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dispat")
	for _, h := range ep.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set("X-Dispat-Event", del.event)
	req.Header.Set("X-Dispat-Delivery", del.id)
	if len(ep.Secret) > 0 {
		req.Header.Set("X-Dispat-Signature", sign(ep.Secret, del.body))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return 0, err
	}
	// Drained and closed whatever the status, so the shared client's
	// connection goes back to the pool instead of leaking.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBody))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// formatFields renders every template field of one event: the scalar payload
// fields, spelled as the payload spells them, with string values escaped for
// a JSON string position — a template embedding {error} inside its quotes
// stays valid JSON whatever the error text carries. The field set mirrors
// models.WebhookFormatFields, and a test holds the two together.
func formatFields(ev release.Event) map[string]string {
	esc := func(s string) string {
		data, err := json.Marshal(s)
		if err != nil { // a string always marshals; kept for the impossible
			return ""
		}
		return string(data[1 : len(data)-1])
	}
	progress := ""
	if ev.Progress != nil {
		progress = strconv.Itoa(*ev.Progress)
	}
	return map[string]string{
		"event":           esc(ev.Name),
		"timestamp":       ev.Time.Format(time.RFC3339Nano),
		"package":         esc(ev.Package),
		"stage":           esc(ev.Stage),
		"version":         esc(ev.Version),
		"previousVersion": esc(ev.PreviousVersion),
		"channel":         esc(ev.Channel),
		"tag":             esc(ev.Tag),
		"status":          esc(ev.Status),
		"failedStage":     esc(ev.FailedStage),
		"error":           esc(ev.Error),
		"code":            esc(ev.Code),
		"blockedBy":       esc(ev.BlockedBy),
		"progress":        progress,
		"message":         esc(ev.Message),
		"root":            esc(ev.Root),
		"published":       strconv.Itoa(ev.Published),
		"failed":          strconv.Itoa(ev.Failed),
		"skipped":         strconv.Itoa(ev.Skipped),
		"cancelled":       strconv.Itoa(ev.Cancelled),
	}
}

// sign renders the signature header value: "sha256=" and the hex HMAC-SHA256
// of the body — the widely implemented X-Hub-Signature-256 convention, so
// existing receiver code verifies it unchanged.
func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// deliveryID is 16 random bytes in hex: unique enough to deduplicate on,
// stable across one delivery's retries.
func deliveryID() string {
	var b [16]byte
	rand.Read(b[:]) //nolint:errcheck // never fails; documented in crypto/rand
	return hex.EncodeToString(b[:])
}
