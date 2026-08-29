package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The event vocabulary is a contract three parties share: the config loader
// validates subscriptions against it, the dispatcher routes deliveries by it,
// and external receivers switch on it. What is tested here is that the
// matcher and the pattern check agree on that vocabulary, and that a webhook
// declaration survives a JSON round trip with header case intact — the whole
// reason headers are a list of objects rather than a map.

func TestMatchWebhookEvent(t *testing.T) {
	for name, tc := range map[string]struct {
		pattern, event string
		want           bool
	}{
		"exact name matches itself":        {"package.published", "package.published", true},
		"exact name matches nothing else":  {"package.published", "package.failed", false},
		"star matches everything":          {"*", "release.started", true},
		"family matches its members":       {"package.*", "package.skipped", true},
		"family excludes other families":   {"package.*", "stage.started", false},
		"family is a prefix, not a substr": {"stage.*", "restage.started", false},
		"no substring false positive":      {"release.started", "release.startedlater", false},
		"bare prefix is not a family":      {"package", "package.published", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := MatchWebhookEvent(tc.pattern, tc.event); got != tc.want {
				t.Errorf("MatchWebhookEvent(%q, %q) = %v, want %v", tc.pattern, tc.event, got, tc.want)
			}
		})
	}
}

func TestKnownWebhookPattern(t *testing.T) {
	// Every listed event is a valid subscription, as are the wildcard and the
	// three family patterns; a typo is not, because a misspelled subscription
	// would silence the webhook without a word.
	for _, ev := range WebhookEvents() {
		if !KnownWebhookPattern(ev) {
			t.Errorf("event %q is listed but not accepted as a pattern", ev)
		}
	}
	for _, p := range []string{"*", "release.*", "stage.*", "package.*", "script.*"} {
		if !KnownWebhookPattern(p) {
			t.Errorf("pattern %q should be accepted", p)
		}
	}
	for _, p := range []string{"", "packge.published", "package.publishd", "run.*", "plan.started", "package.", ".*"} {
		if KnownWebhookPattern(p) {
			t.Errorf("pattern %q should be refused", p)
		}
	}
	// Script-raised names are open-ended: "script." plus any word a
	// `dispat trigger` may say is a valid subscription, because the config
	// cannot know in advance what a script will call its events.
	for _, p := range []string{"script.deployed", "script.smoke_passed", "script.e2e-green", "script.v2"} {
		if !KnownWebhookPattern(p) {
			t.Errorf("script pattern %q should be accepted", p)
		}
	}
	for _, p := range []string{"script.", "script.2fast", "script.has space", "script.a.b"} {
		if KnownWebhookPattern(p) {
			t.Errorf("script pattern %q should be refused", p)
		}
	}
}

func TestIsWebhookScriptWord(t *testing.T) {
	// The word grammar `dispat trigger` and a subscription share: a letter,
	// then letters, digits, dashes or underscores — one token, so the event
	// name never needs quoting anywhere it travels.
	for _, w := range []string{"progress", "deployed", "smoke_passed", "e2e-green", "V2"} {
		if !IsWebhookScriptWord(w) {
			t.Errorf("word %q should be accepted", w)
		}
	}
	for _, w := range []string{"", "2fast", "-lead", "has space", "a.b", "star*"} {
		if IsWebhookScriptWord(w) {
			t.Errorf("word %q should be refused", w)
		}
	}
	if got := WebhookScriptEvent("deployed"); got != "script.deployed" {
		t.Errorf("WebhookScriptEvent = %q", got)
	}
}

func TestKnownWebhookFormatField(t *testing.T) {
	// The format vocabulary is a contract with the payload: every listed
	// field is accepted as a {field} token, and what the payload does not
	// carry as a scalar — the packages list above all — is refused, so a
	// template cannot silently render an empty hole forever.
	for _, f := range WebhookFormatFields() {
		if !KnownWebhookFormatField(f) {
			t.Errorf("field %q is listed but not accepted", f)
		}
	}
	for _, f := range []string{"", "packages", "Event", "pkg", "previousversion"} {
		if KnownWebhookFormatField(f) {
			t.Errorf("field %q should be refused", f)
		}
	}
}

func TestExpandWebhookFormat(t *testing.T) {
	// The tokenizer's whole contract: a {letters} token expands, everything
	// else — JSON's own braces above all — passes through byte for byte.
	upper := func(f string) string { return "<" + f + ">" }
	for name, tc := range map[string]struct{ format, want string }{
		"plain token":            {"{package}", "<package>"},
		"token in json":          {`{"text": "{package} {version}"}`, `{"text": "<package> <version>"}`},
		"empty braces literal":   {"{} {package}", "{} <package>"},
		"digits are not a token": {"{v1}", "{v1}"},
		"unclosed brace":         {"{package", "{package"},
		"nested open brace":      {"{{package}", "{<package>"},
		"no tokens":              {`{"a": 1}`, `{"a": 1}`},
		"empty format":           {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ExpandWebhookFormat(tc.format, upper); got != tc.want {
				t.Errorf("ExpandWebhookFormat(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

func TestWebhookConfigRoundTripKeepsHeaderCase(t *testing.T) {
	// Headers are a list of name/value objects precisely so the name's case
	// survives the trip through the file; a map's keys would be lowercased
	// before the model ever saw them.
	in := WebhookConfig{
		Name:      "tracker",
		URL:       "https://tracker.internal/dispat",
		Method:    "PUT",
		Events:    []string{WebhookPackagePublished, WebhookPackageFailed},
		Headers:   []WebhookHeader{{Name: "X-Api-Key", Value: "$TRACKER_TOKEN"}},
		SecretEnv: "DISPAT_WEBHOOK_SECRET",
		Timeout:   3,
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var out WebhookConfig
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip changed the config:\n got %+v\nwant %+v", out, in)
	}
}
