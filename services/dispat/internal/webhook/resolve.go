package webhook

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/rs/zerolog"
	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/cond"
)

// Header is one resolved request header.
type Header struct {
	Name, Value string
}

// Endpoint is one resolved webhook target: the validated config with its
// environment references settled — the secret read out of its variable, the
// header values expanded, the env gate answered — so the dispatcher consults
// the environment exactly once per run, not once per delivery.
type Endpoint struct {
	Name    string   // log label; never empty after Resolve
	URL     string   //
	Method  string   // POST, PUT or PATCH
	Events  []string // subscription patterns; empty means every event
	Headers []Header
	Secret  []byte        // HMAC-SHA256 key; nil delivers unsigned
	Timeout time.Duration // one delivery attempt's bound
	Format  string        // body template; empty sends the default payload
	// The routing membership. RunLevel admits the run-bracket events (an
	// event naming no package); Packages admits one package's events. A nil
	// Packages map is the unrestricted endpoint — every package and the run
	// brackets too — which is what a directly constructed Endpoint and the
	// out-of-workspace Resolve produce. ResolveWorkspace always states both.
	RunLevel bool
	Packages map[string]bool
}

// subscribes reports whether the endpoint's event filter admits the event.
func (ep *Endpoint) subscribes(event string) bool {
	if len(ep.Events) == 0 {
		return true
	}
	for _, pattern := range ep.Events {
		if public.MatchWebhookEvent(pattern, event) {
			return true
		}
	}
	return false
}

// covers reports whether the endpoint's membership admits the event's
// origin: the run itself when pkg is empty, one package otherwise.
func (ep *Endpoint) covers(pkg string) bool {
	if ep.Packages == nil {
		return true
	}
	if pkg == "" {
		return ep.RunLevel
	}
	return ep.Packages[pkg]
}

// Resolve maps one validated webhook list onto unrestricted endpoints: the
// shape for a caller with no workspace to route by, like a trigger outside a
// run. A webhook whose env gate does not match resolves to nothing, exactly
// as if it were not declared.
func Resolve(cfgs []public.WebhookConfig, log zerolog.Logger) []Endpoint {
	endpoints := make([]Endpoint, 0, len(cfgs))
	for _, c := range cfgs {
		if ep, active := resolveOne(c, log); active {
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

// ResolveWorkspace maps the root list and every package's effective list
// onto routed endpoints. A webhook declared once and inherited widely stays
// one endpoint — one queue, one delivery order — whose membership records
// the packages it observes; the root list's endpoints additionally receive
// the run-bracket events. Endpoints come out in a deterministic order: the
// root list first, then package-introduced ones by package name.
func ResolveWorkspace(root []public.WebhookConfig, perPackage map[string][]public.WebhookConfig, log zerolog.Logger) []Endpoint {
	// Deduplication is by the config entry's whole identity: two levels
	// stating the same webhook mean the same endpoint, while any difference
	// — one header, one event — is a different delivery policy and stays its
	// own lane.
	index := map[string]int{}
	inactive := map[string]bool{}
	var out []Endpoint
	add := func(c public.WebhookConfig) int {
		key := endpointKey(c)
		if i, ok := index[key]; ok {
			return i
		}
		if inactive[key] {
			return -1
		}
		ep, active := resolveOne(c, log)
		if !active {
			inactive[key] = true
			return -1
		}
		ep.Packages = map[string]bool{}
		index[key] = len(out)
		out = append(out, ep)
		return index[key]
	}
	for _, c := range root {
		if i := add(c); i >= 0 {
			out[i].RunLevel = true
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(perPackage)) {
		for _, c := range perPackage[pkg] {
			if i := add(c); i >= 0 {
				out[i].Packages[pkg] = true
			}
		}
	}
	return out
}

// endpointKey renders a config entry's identity for deduplication. The JSON
// form is canonical here: field order is the struct's, and two entries that
// marshal alike are one policy.
func endpointKey(c public.WebhookConfig) string {
	data, err := json.Marshal(c)
	if err != nil { // plain data; kept so a marshal surprise cannot merge endpoints
		return c.Name + "\x00" + c.URL
	}
	return string(data)
}

// resolveOne settles one webhook config against the environment. active is
// false when the env gate does not match: the webhook is disabled for this
// run, reported at debug — an expected quiet, not a warning.
func resolveOne(c public.WebhookConfig, log zerolog.Logger) (Endpoint, bool) {
	ep := Endpoint{
		Name:    c.Name,
		URL:     c.URL,
		Method:  c.Method,
		Events:  slices.Clone(c.Events),
		Format:  c.Format,
		Timeout: time.Duration(c.Timeout) * time.Second,
	}
	if ep.Name == "" {
		if u, err := url.Parse(c.URL); err == nil {
			ep.Name = u.Host
		}
	}
	if c.Env != "" {
		gate, err := cond.ParseCondition(c.Env)
		if err != nil { // validation refused this long ago; direct construction only
			log.Warn().Str("webhook", ep.Name).Err(err).Msg("webhook env condition is invalid, webhook disabled")
			return ep, false
		}
		if !gate.Match(os.Getenv) {
			log.Debug().Str("webhook", ep.Name).Str("env", c.Env).
				Msg("webhook env condition not met, webhook disabled for this run")
			return ep, false
		}
	}
	if ep.Method == "" {
		ep.Method = "POST"
	}
	if ep.Timeout <= 0 {
		ep.Timeout = defaultTimeout
	}
	for _, h := range c.Headers {
		ep.Headers = append(ep.Headers, Header{Name: h.Name, Value: os.ExpandEnv(h.Value)})
	}
	if c.SecretEnv != "" {
		if secret := os.Getenv(c.SecretEnv); secret != "" {
			ep.Secret = []byte(secret)
		} else {
			log.Warn().Str("webhook", ep.Name).Str("env", c.SecretEnv).
				Msg("webhook secret variable is unset or empty, deliveries are unsigned")
		}
	}
	return ep, true
}
