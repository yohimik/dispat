package integration

// Area 42: external webhooks through the compiled binary. The config declares
// HTTP endpoints, a release run delivers its progress to them, and nothing
// about the endpoints — refusals, timeouts, absence — is allowed to change
// what the release does or what the command exits with.
//
// What only a process-boundary test can witness is here: the deliveries a
// real release makes to a real HTTP server, in order, with their headers and
// signatures; the exit code staying what it was with every endpoint down; and
// the flush racing a SIGINT.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yohimik/dispat/pkg/models/v2"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// webhookSink is a webhook receiver: it records every delivery under a mutex
// and answers with the queued status codes, the last repeating forever
// (default 200).
type webhookSink struct {
	mu         sync.Mutex
	deliveries []webhookDelivery
	statuses   []int
	srv        *httptest.Server
}

type webhookDelivery struct {
	Method string
	Header http.Header
	Body   []byte
}

func newWebhookSink(t *testing.T, statuses ...int) *webhookSink {
	t.Helper()
	s := &webhookSink{statuses: statuses}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		s.mu.Lock()
		s.deliveries = append(s.deliveries, webhookDelivery{Method: req.Method, Header: req.Header.Clone(), Body: body})
		status := http.StatusOK
		if len(s.statuses) > 0 {
			status = s.statuses[0]
			if len(s.statuses) > 1 {
				s.statuses = s.statuses[1:]
			}
		}
		s.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *webhookSink) all() []webhookDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]webhookDelivery(nil), s.deliveries...)
}

// events returns the X-Dispat-Event header of every delivery, in arrival
// order — the sink's one-line answer to "what was I told".
func (s *webhookSink) events() []string {
	var names []string
	for _, d := range s.all() {
		names = append(names, d.Header.Get("X-Dispat-Event"))
	}
	return names
}

// payloads decodes every delivery body, in arrival order.
func (s *webhookSink) payloads(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, d := range s.all() {
		var p map[string]any
		require.NoError(t, json.Unmarshal(d.Body, &p))
		out = append(out, p)
	}
	return out
}

// find returns the first payload of the given event name, failing when none
// arrived.
func (s *webhookSink) find(t *testing.T, event string) map[string]any {
	t.Helper()
	for _, p := range s.payloads(t) {
		if p["event"] == event {
			return p
		}
	}
	t.Fatalf("no %q delivery arrived; got %v", event, s.events())
	return nil
}

// webhooksConfig is libsConfig with the given webhooks attached.
func webhooksConfig(buildScript string, hooks ...models.WebhookConfig) models.File {
	cfg := libsConfig(buildScript, 1)
	cfg.Webhooks = hooks
	return cfg
}

func TestWebhookHappyPathSequence(t *testing.T) {
	// One webhook with no filter observes a two-package release end to end:
	// release.started opens with the whole plan, every stage of every
	// package brackets in order, each package settles as published, and
	// release.finished closes with the counts. This is the feature's
	// contract in one run.
	sink := newWebhookSink(t)
	r := harness.New(t)
	cfg := webhooksConfig(echoBuild, models.WebhookConfig{URL: sink.srv.URL})
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,web): bootstrap both packages")
	r.ReleaseOK()

	events := sink.events()
	require.NotEmpty(t, events)
	assert.Equal(t, "release.started", events[0], "the plan snapshot opens the stream")
	assert.Equal(t, "release.finished", events[len(events)-1], "the outcome closes it")

	// Per-package order is exact: stages bracket, the outcome comes last.
	var core []string
	for _, p := range sink.payloads(t) {
		if p["package"] == "core" {
			core = append(core, p["event"].(string)+":"+str(p["stage"]))
		}
	}
	assert.Equal(t, []string{
		"stage.started:build", "stage.succeeded:build",
		"stage.started:publish", "stage.succeeded:publish",
		"package.published:",
	}, core)

	started := sink.find(t, "release.started")
	assert.Len(t, started["packages"], 2, "the plan snapshot lists every releasing package")

	// The published payload carries the release identity and nothing
	// unexpected: the field names are the log stream's own.
	published := sink.find(t, "package.published")
	assert.Equal(t, "core", published["package"])
	assert.Equal(t, "published", published["status"])
	assert.Equal(t, "0.1.0", published["version"])
	assert.Equal(t, "0.0.0", published["previousVersion"])
	assert.Equal(t, "core@0.1.0", published["tag"])
	assert.NotEmpty(t, published["timestamp"])
	for key := range published {
		assert.Contains(t, []string{"event", "timestamp", "package", "version", "previousVersion", "channel", "tag", "status"},
			key, "unexpected payload field %q", key)
	}

	finished := sink.find(t, "release.finished")
	assert.Equal(t, "succeeded", finished["status"])
	assert.Equal(t, float64(2), finished["published"])
	assert.Len(t, finished["packages"], 2)
}

// str renders a possibly-absent payload field, for order strings.
func str(v any) string {
	if v == nil {
		return ""
	}
	return v.(string)
}

func TestWebhookEventFilters(t *testing.T) {
	// Subscriptions are per webhook: an events list admits exactly what it
	// names, a family pattern admits the family, and two webhooks sharing an
	// event each get their own delivery.
	brackets, outcomes, shared := newWebhookSink(t), newWebhookSink(t), newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{Name: "brackets", URL: brackets.srv.URL, Events: []string{"release.started", "release.finished"}},
		models.WebhookConfig{Name: "outcomes", URL: outcomes.srv.URL, Events: []string{"package.*"}},
		models.WebhookConfig{Name: "shared", URL: shared.srv.URL, Events: []string{"package.published"}},
	))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	assert.Equal(t, []string{"release.started", "release.finished"}, brackets.events())
	assert.Equal(t, []string{"package.published"}, outcomes.events(),
		"package.* admits the family and nothing else")
	assert.Equal(t, []string{"package.published"}, shared.events(),
		"a second subscriber to the same event gets its own delivery")
}

func TestWebhookFailingEndpointNeverAffectsTheRelease(t *testing.T) {
	// The isolation promise, from the endpoint's side: a webhook answering
	// 500 to everything costs the run nothing but W239 warnings. The release
	// publishes, tags and exits 0 exactly as if the webhook were healthy.
	sink := newWebhookSink(t, http.StatusInternalServerError)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: sink.srv.URL, Events: []string{"package.published", "release.finished"}}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	res := r.ReleaseOK()

	assert.True(t, r.HasTag("core@0.1.0"), "the release itself is untouched")
	assert.True(t, harness.HasCode(res.Events, "W239"), "every failed delivery warns with its code")
	// The warning names the endpoint without repeating its path or query,
	// which may contain credentials.
	found := false
	for _, e := range res.Events {
		if e.Code() == "W239" && e.Str("webhook") == strings.TrimPrefix(sink.srv.URL, "http://") {
			found = true
		}
	}
	assert.True(t, found, "the W239 line carries the redacted endpoint name")
}

func TestWebhookUnreachableEndpointNeverAffectsTheRelease(t *testing.T) {
	// The same isolation with no server at all: a connection refused is
	// still only a warning.
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: "http://127.0.0.1:1/hook", Events: []string{"release.finished"}}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	res := r.ReleaseOK()
	assert.True(t, r.HasTag("core@0.1.0"))
	assert.True(t, harness.HasCode(res.Events, "W239"))
}

func TestWebhookSlowEndpointIsBounded(t *testing.T) {
	// A hanging endpoint is bounded twice: each attempt by the webhook's own
	// timeout, and the end-of-run flush by the dispatcher's deadline. The
	// command finishes promptly either way.
	hang := make(chan struct{})
	defer close(hang)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-hang
	}))
	t.Cleanup(srv.Close)

	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: srv.URL, Events: []string{"release.started"}, Timeout: 1}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	start := time.Now()
	res := r.ReleaseOK()
	assert.Less(t, time.Since(start), 30*time.Second, "the flush must not wait out a hanging endpoint")
	assert.True(t, r.HasTag("core@0.1.0"))
	assert.True(t, harness.HasCode(res.Events, "W239"))
}

func TestWebhookSignature(t *testing.T) {
	// With secretEnv set, every delivery carries the standard sha256= HMAC
	// over its exact body bytes — recomputed here from the captured body and
	// the same secret, the way a real receiver verifies it.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: sink.srv.URL, SecretEnv: "DISPAT_IT_HOOK_SECRET"}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	res := r.CommandEnv([]string{"DISPAT_IT_HOOK_SECRET=hook-secret"})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	deliveries := sink.all()
	require.NotEmpty(t, deliveries)
	for _, d := range deliveries {
		mac := hmac.New(sha256.New, []byte("hook-secret"))
		mac.Write(d.Body)
		assert.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), d.Header.Get("X-Dispat-Signature"),
			"delivery %s fails verification", d.Header.Get("X-Dispat-Delivery"))
	}
}

func TestWebhookHeadersAndMethod(t *testing.T) {
	// The configured method and headers reach the wire, with a $VAR header
	// value expanded from the process environment — the pattern that keeps a
	// token out of the config file.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild,
		models.WebhookConfig{URL: sink.srv.URL, Method: "PUT", Events: []string{"release.finished"},
			Headers: []models.WebhookHeader{{Name: "X-Api-Key", Value: "$DISPAT_IT_HOOK_TOKEN"}}}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	res := r.CommandEnv([]string{"DISPAT_IT_HOOK_TOKEN=tok-123"})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)

	deliveries := sink.all()
	require.Len(t, deliveries, 1)
	assert.Equal(t, "PUT", deliveries[0].Method)
	assert.Equal(t, "tok-123", deliveries[0].Header.Get("X-Api-Key"))
	assert.Equal(t, "dispat", deliveries[0].Header.Get("User-Agent"))
	assert.Equal(t, "application/json", deliveries[0].Header.Get("Content-Type"))
}

func TestWebhookConfigRejections(t *testing.T) {
	// A broken webhook declaration stops the load before any work: the
	// message names the entry, and nothing is released.
	for name, hook := range map[string]models.WebhookConfig{
		"unknown event": {URL: "https://example.com", Events: []string{"package.publishd"}},
		"missing url":   {Events: []string{"package.published"}},
		"bad method":    {URL: "https://example.com", Method: "DELETE"},
	} {
		t.Run(name, func(t *testing.T) {
			r := harness.New(t)
			r.WriteConfigModel(webhooksConfig(echoBuild, hook))
			r.SeedPackage("packages", "core")
			r.Commit("feat(core): bootstrap")
			res := r.Release()
			require.NotEqual(t, 0, res.Code)
			assert.Contains(t, res.Stdout+res.Stderr, "webhooks[0]")
			assert.False(t, r.HasTag("core@0.1.0"), "a refused load must release nothing")
		})
	}
}

func TestWebhookInterruptedRunStillReportsTheOutcome(t *testing.T) {
	// A SIGINT is the run outcome a listener most wants to hear about, so
	// the flush is detached from the cancellation: the interrupted run still
	// delivers its package.cancelled events and a release.finished saying
	// "interrupted" before the process exits.
	sink := newWebhookSink(t)
	r := harness.New(t)
	cfg := webhooksConfig("", models.WebhookConfig{
		URL: sink.srv.URL, Events: []string{"package.cancelled", "release.finished"}})
	cfg.Scripts["build"] = models.Script{r.TsmarkScript("build.tsmark", "$DISPAT_PACKAGE", 1500*time.Millisecond)}
	cfg.Dependencies = []models.DependencyConfig{{Consumer: "b", Provider: "a"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "a")
	r.SeedPackage("packages", "b")
	r.Commit("feat(a,b): bootstrap both packages")

	proc := r.StartRelease()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(r.Path("build.tsmark"))
		return err == nil && strings.Contains(string(data), "a start")
	}, 15*time.Second, 20*time.Millisecond, "a's build never started")
	proc.Signal(os.Interrupt)
	res := proc.Wait()
	require.NotEqual(t, 0, res.Code, "an interrupted run does not exit clean")

	finished := sink.find(t, "release.finished")
	assert.Equal(t, "interrupted", finished["status"])
	cancelled := 0
	for _, name := range sink.events() {
		if name == "package.cancelled" {
			cancelled++
		}
	}
	assert.GreaterOrEqual(t, cancelled, 1, "the interrupted packages are announced")
}

func TestWebhookFailedRunKeepsItsExitCode(t *testing.T) {
	// The exit-code fence: a run with a failing package exits with exactly
	// the code its webhook-less twin exits with, and the closing delivery
	// reports the failure honestly.
	sink := newWebhookSink(t)
	withHook := harness.New(t)
	withHook.WriteConfigModel(webhooksConfig("false", models.WebhookConfig{URL: sink.srv.URL}))
	withHook.SeedPackage("packages", "core")
	withHook.Commit("feat(core): bootstrap")
	hooked := withHook.Release()

	bare := harness.New(t)
	bare.WriteConfigModel(libsConfig("false", 1))
	bare.SeedPackage("packages", "core")
	bare.Commit("feat(core): bootstrap")
	baseline := bare.Release()

	require.NotEqual(t, 0, baseline.Code)
	assert.Equal(t, baseline.Code, hooked.Code, "webhooks must not change what the command exits with")

	finished := sink.find(t, "release.finished")
	assert.Equal(t, "failed", finished["status"])
	assert.Equal(t, float64(1), finished["failed"])
	failed := sink.find(t, "package.failed")
	assert.Equal(t, "build", failed["failedStage"])
	assert.NotEmpty(t, failed["error"])
}

func TestWebhookScriptProgressTrigger(t *testing.T) {
	// `dispat trigger progress <n> [message]` is a stage script raising its
	// own webhook event between the stage brackets the release emits on its
	// own. The child process reads the run's DISPAT_* environment, so the
	// delivery attributes itself to the package and stage that raised it —
	// and its exit code is 0 whatever the endpoints think of it.
	sink := newWebhookSink(t)
	bin, _ := harness.Build(t)
	r := harness.New(t)
	cfg := webhooksConfig("", models.WebhookConfig{URL: sink.srv.URL})
	cfg.Scripts["build"] = models.Script{
		bin + " trigger progress 0",
		bin + " trigger progress 60 compiling assets",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	// Both progress reports land between the build's own bracket events, in
	// the order the script raised them.
	var seen []string
	for _, p := range sink.payloads(t) {
		if p["package"] == "core" && str(p["stage"]) == "build" {
			seen = append(seen, p["event"].(string))
		}
	}
	assert.Equal(t, []string{"stage.started", "script.progress", "script.progress", "stage.succeeded"}, seen)

	progress := sink.find(t, "script.progress")
	assert.Equal(t, "core", progress["package"])
	assert.Equal(t, "build", progress["stage"])
	assert.Equal(t, "0.1.0", progress["version"])
	all := sink.payloads(t)
	var values []any
	var messages []string
	for _, p := range all {
		if p["event"] == "script.progress" {
			values = append(values, p["progress"])
			messages = append(messages, str(p["message"]))
		}
	}
	assert.Equal(t, []any{float64(0), float64(60)}, values, "the value travels, including a genuine 0")
	assert.Equal(t, []string{"", "compiling assets"}, messages)
}

func TestWebhookTriggerOutsideARunIsHarmless(t *testing.T) {
	// Invoked by hand rather than from a stage script, the command still
	// delivers — with the package fields simply absent — and exits 0; with a
	// dead endpoint it warns W239 and still exits 0, because a script must
	// not be able to fail its stage by reporting progress.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild, models.WebhookConfig{URL: sink.srv.URL}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Command("trigger", "progress", "25", "warming", "caches")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	progress := sink.find(t, "script.progress")
	assert.Equal(t, float64(25), progress["progress"])
	assert.Equal(t, "warming caches", progress["message"])
	assert.Nil(t, progress["package"], "outside a run there is no package to name")

	dead := harness.New(t)
	dead.WriteConfigModel(webhooksConfig(echoBuild, models.WebhookConfig{URL: "http://127.0.0.1:1/hook"}))
	dead.SeedPackage("packages", "core")
	dead.Commit("feat(core): bootstrap")
	res = dead.Command("trigger", "progress", "50")
	assert.Equal(t, 0, res.Code, "a dead endpoint is a warning, not an exit code")
	assert.True(t, harness.HasCode(res.Events, "W239"))

	// With no webhooks configured at all, the command is a clean no-op: a
	// script may carry its trigger lines into a repository that has not set
	// webhooks up yet without failing there.
	bare := harness.New(t)
	bare.WriteConfigModel(libsConfig(echoBuild, 1))
	bare.SeedPackage("packages", "core")
	bare.Commit("feat(core): bootstrap")
	res = bare.Command("trigger", "progress", "50")
	assert.Equal(t, 0, res.Code, "no webhooks configured is a no-op, not an error")
}

func TestWebhookPackageOverrideRouting(t *testing.T) {
	// A package that states its own webhooks list replaces the inherited one
	// for its events: the root endpoint keeps the run brackets and the other
	// packages, the overriding package's events go to its own endpoint
	// alone. This is the ladder's replace-wholesale rule observed on the
	// wire.
	rootSink, webSink := newWebhookSink(t), newWebhookSink(t)
	r := harness.New(t)
	cfg := webhooksConfig(echoBuild, models.WebhookConfig{URL: rootSink.srv.URL})
	libs := cfg.Spaces["libs"]
	libs.Packages = map[string]models.PackageConfig{
		"web": {Webhooks: []models.WebhookConfig{{URL: webSink.srv.URL}}},
	}
	cfg.Spaces["libs"] = libs
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.SeedPackage("packages", "web")
	r.Commit("feat(core,web): bootstrap both packages")
	r.ReleaseOK()

	rootPkgs := map[string]bool{}
	for _, p := range rootSink.payloads(t) {
		if pkg := str(p["package"]); pkg != "" {
			rootPkgs[pkg] = true
		}
	}
	assert.Equal(t, map[string]bool{"core": true}, rootPkgs,
		"the overriding package's events left the root endpoint")
	assert.Equal(t, "release.started", rootSink.events()[0], "the run brackets stay with the top level")

	for _, p := range webSink.payloads(t) {
		assert.Equal(t, "web", p["package"], "the package endpoint hears its package alone, got %v", p)
	}
	found := false
	for _, name := range webSink.events() {
		if name == "package.published" {
			found = true
		}
	}
	assert.True(t, found, "the override still observes its own package's outcome")
}

func TestWebhookEmptyListOptsOutOnTheWire(t *testing.T) {
	// `webhooks: []` at a package level silences that package: the root
	// endpoint still hears the run brackets and the sibling, and nothing
	// anywhere reports the opted-out package. Raw config, because the typed
	// model's omitempty cannot write an empty list.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigRaw(map[string]any{
		"logLevel": "info", "logFormat": "json",
		"github":      map[string]any{"enabled": false},
		"updateCheck": false,
		"scripts":     map[string]any{"build": echoBuild, "publish": "echo publishing"},
		"webhooks":    []any{map[string]any{"url": sink.srv.URL}},
		"spaces": map[string]any{"libs": map[string]any{
			"path": "packages",
			"flow": map[string]any{"build": "build", "publish": "publish"},
			"packages": map[string]any{
				"quiet": map[string]any{"webhooks": []any{}},
			},
		}},
	})
	r.SeedPackage("packages", "loud")
	r.SeedPackage("packages", "quiet")
	r.Commit("feat(loud,quiet): bootstrap both packages")
	r.ReleaseOK()

	events := sink.events()
	assert.Equal(t, "release.started", events[0])
	assert.Equal(t, "release.finished", events[len(events)-1])
	for _, p := range sink.payloads(t) {
		assert.NotEqual(t, "quiet", str(p["package"]), "the opted-out package must stay silent: %v", p)
	}
	published := false
	for _, p := range sink.payloads(t) {
		if p["event"] == "package.published" && p["package"] == "loud" {
			published = true
		}
	}
	assert.True(t, published, "the sibling still reports")
}

func TestWebhookCustomFormat(t *testing.T) {
	// A format template replaces the payload byte for byte: tokens render as
	// their event fields and everything else is literal, so a Slack-shaped
	// body arrives exactly as the config wrote it.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild, models.WebhookConfig{
		URL:    sink.srv.URL,
		Events: []string{"package.published"},
		Format: `{"text": "released {package} {version} as {tag}"}`,
	}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	deliveries := sink.all()
	require.Len(t, deliveries, 1)
	assert.Equal(t, `{"text": "released core 0.1.0 as core@0.1.0"}`, string(deliveries[0].Body))
	assert.Equal(t, "package.published", deliveries[0].Header.Get("X-Dispat-Event"),
		"the delivery headers still name the event a formatted body may not")
}

func TestWebhookEnvGate(t *testing.T) {
	// `env: DISPAT_IT_CI=true` keeps the webhook silent until the condition
	// holds — the same run delivers nothing without the variable and
	// everything with it.
	sink := newWebhookSink(t)
	r := harness.New(t)
	r.WriteConfigModel(webhooksConfig(echoBuild, models.WebhookConfig{
		URL: sink.srv.URL, Env: "DISPAT_IT_CI=true", Events: []string{"script.*"}}))
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")

	res := r.Command("trigger", "deployed", "quietly")
	require.Equal(t, 0, res.Code)
	assert.Empty(t, sink.all(), "an unmet env gate disables the webhook")

	res = r.CommandEnv([]string{"DISPAT_IT_CI=true"}, "trigger", "deployed", "loudly")
	require.Equal(t, 0, res.Code)
	deliveries := sink.all()
	require.Len(t, deliveries, 1, "the met gate enables it")
	assert.Equal(t, "script.deployed", deliveries[0].Header.Get("X-Dispat-Event"))
}

func TestWebhookTriggerCustomEvent(t *testing.T) {
	// `dispat trigger <word>` raises script.<word>: a stage script's own
	// vocabulary, subscribable by exact name, routed to the raising
	// package's effective list like any of its events.
	sink := newWebhookSink(t)
	bin, _ := harness.Build(t)
	r := harness.New(t)
	cfg := webhooksConfig("", models.WebhookConfig{URL: sink.srv.URL, Events: []string{"script.smoke-passed"}})
	cfg.Scripts["build"] = models.Script{
		"echo building",
		bin + " trigger smoke-passed all green",
		bin + " trigger ignored this one is not subscribed",
	}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	r.ReleaseOK()

	deliveries := sink.all()
	require.Len(t, deliveries, 1, "only the subscribed script event arrives")
	payload := sink.find(t, "script.smoke-passed")
	assert.Equal(t, "core", payload["package"])
	assert.Equal(t, "build", payload["stage"])
	assert.Equal(t, "all green", payload["message"])
	assert.Nil(t, payload["progress"], "a custom event carries no progress value")
}

func TestWebhookRefusedRunEmitsNothing(t *testing.T) {
	// Webhooks begin once the run is committed to execute: a run refused
	// before that — here by the branch guard — makes no delivery at all,
	// not even a started one, because nothing it planned was ever started.
	sink := newWebhookSink(t)
	r := harness.New(t)
	cfg := webhooksConfig(echoBuild, models.WebhookConfig{URL: sink.srv.URL})
	cfg.Run = &models.RunConfig{AllowBranch: []string{"release-only"}}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): bootstrap")
	res := r.Release()
	require.NotEqual(t, 0, res.Code)
	assert.Empty(t, sink.all(), "a refused run has nothing to report")
}
