package models

import "strings"

// This file is the `webhooks` key: HTTP endpoints notified of release progress
// as it happens, so external tooling — a CI dashboard, a chat bot, a deploy
// tracker — can observe a release without scraping the log stream.
//
// Webhooks observe and never gate: a delivery runs asynchronously, its outcome
// is never waited on, and a failed or slow endpoint costs the run nothing but
// a warning. Anything that must be able to stop a release belongs in the
// script hooks instead.

// WebhookConfig declares one endpoint under the top-level `webhooks` list.
// Several webhooks may subscribe to the same event; each receives its own
// delivery.
type WebhookConfig struct {
	// Name labels the webhook in log lines. Default: the URL's host. When
	// set, it must be unique across the list, so a warning names exactly one
	// endpoint.
	Name string `json:"name,omitempty"`
	// URL is the endpoint the event payloads are sent to. Required; the
	// scheme must be http or https.
	URL string `json:"url,omitempty"`
	// Method is the HTTP method: POST (default), PUT or PATCH.
	Method string `json:"method,omitempty"`
	// Events are the event names this webhook subscribes to, from the
	// vocabulary WebhookEvents lists. "*" matches every event and a
	// "<prefix>.*" pattern such as "package.*" matches the whole family. An
	// empty list subscribes to every event.
	Events []string `json:"events,omitempty"`
	// Headers are extra request headers, sent with every delivery. A list of
	// name/value objects rather than a map, so header names keep their case:
	// dispat lowercases every map key it decodes. Values may reference
	// environment variables ($NAME, ${NAME}), resolved once per run against
	// the process environment, so a bearer token stays out of the file.
	Headers []WebhookHeader `json:"headers,omitempty"`
	// SecretEnv names an environment variable holding the signing secret.
	// When set, every delivery carries an X-Dispat-Signature header:
	// "sha256=" followed by the hex HMAC-SHA256 of the request body. The
	// variable's name goes in the file, never the secret itself.
	SecretEnv string `json:"secretEnv,omitempty"`
	// Timeout bounds one delivery attempt, in seconds. Default 10.
	Timeout int `json:"timeout,omitempty"`
	// Env gates the webhook on the process environment, in the condition
	// grammar `dispat if` uses: NAME, !NAME, NAME=value, NAME!=value,
	// NAME~glob or NAME!~glob. `env: CI=true` keeps a webhook silent on
	// every laptop and active on the runner. The condition is evaluated once
	// per run; an unmet condition disables the webhook exactly as if it were
	// not declared. Empty means always active.
	Env string `json:"env,omitempty"`
	// Format replaces the default JSON payload with a rendered template, for
	// endpoints that want their own shape (a Slack message, say). A {field}
	// token — letters only, from the names WebhookFormatFields lists — is
	// replaced by that field of the event, JSON-string-escaped, and every
	// other byte is literal, so a template may itself be JSON. An empty
	// Format sends the default payload. The signature, when configured, is
	// computed over the body actually sent.
	Format string `json:"format,omitempty"`
}

// WebhookHeader is one extra request header of a webhook.
type WebhookHeader struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// The webhook event vocabulary. The `release.` events bracket the run, the
// `stage.` events report one package's stage starting and succeeding, the
// `package.` events report how one package settled, and the `script.` event
// is the one a stage script raises itself through `dispat trigger`. The
// `run.` and `plan.` prefixes are deliberately left free for other commands
// to claim later.
const (
	WebhookReleaseStarted   = "release.started"
	WebhookReleaseFinished  = "release.finished"
	WebhookStageStarted     = "stage.started"
	WebhookStageSucceeded   = "stage.succeeded"
	WebhookPackagePublished = "package.published"
	WebhookPackageFailed    = "package.failed"
	WebhookPackageSkipped   = "package.skipped"
	WebhookPackageCancelled = "package.cancelled"
	WebhookScriptProgress   = "script.progress"
)

// WebhookEvents lists every event name a webhook may subscribe to, in a fixed
// order, so an error message and the documentation enumerate them identically.
func WebhookEvents() []string {
	return []string{
		WebhookReleaseStarted,
		WebhookReleaseFinished,
		WebhookStageStarted,
		WebhookStageSucceeded,
		WebhookPackagePublished,
		WebhookPackageFailed,
		WebhookPackageSkipped,
		WebhookPackageCancelled,
		WebhookScriptProgress,
	}
}

// KnownWebhookPattern reports whether p is something `events` accepts: an
// exact event name, the "*" wildcard, a "<prefix>.*" family pattern over a
// prefix at least one event uses, or a script-raised name — "script."
// followed by a word a `dispat trigger` invocation may say. Anything else is
// a typo the loader must refuse, because a misspelled subscription would
// otherwise silence the webhook forever without a word.
func KnownWebhookPattern(p string) bool {
	if p == "*" {
		return true
	}
	if word, ok := strings.CutPrefix(p, "script."); ok && word != "*" {
		return IsWebhookScriptWord(word)
	}
	for _, ev := range WebhookEvents() {
		if p == ev {
			return true
		}
		if prefix, ok := strings.CutSuffix(p, ".*"); ok && strings.HasPrefix(ev, prefix+".") {
			return true
		}
	}
	return false
}

// IsWebhookScriptWord reports whether a word may name a script-raised event:
// what `dispat trigger <word>` accepts, and what a subscription may name
// after the "script." prefix. A letter, then letters, digits, dashes or
// underscores — one word, so the event name stays one token in every log
// line and every subscription.
func IsWebhookScriptWord(word string) bool {
	if word == "" || !isFormatLetter(word[0]) {
		return false
	}
	for i := 1; i < len(word); i++ {
		b := word[i]
		if !isFormatLetter(b) && !(b >= '0' && b <= '9') && b != '-' && b != '_' {
			return false
		}
	}
	return true
}

// WebhookScriptEvent renders the event name a script-raised word travels
// under: the "script." family, so a listener telling dispat's own events
// apart from what a script said needs only the prefix.
func WebhookScriptEvent(word string) string { return "script." + word }

// WebhookFormatFields lists every event field a Format template may name in
// a {field} token: the scalar payload fields, spelled exactly as the JSON
// payload spells them. The list-valued `packages` field is deliberately
// absent — a template renders one line, and a list has no one rendering.
func WebhookFormatFields() []string {
	return []string{
		"event", "timestamp",
		"package", "stage", "version", "previousVersion", "channel", "tag",
		"status", "failedStage", "error", "code", "blockedBy",
		"progress", "message",
		"root", "published", "failed", "skipped", "cancelled",
	}
}

// KnownWebhookFormatField reports whether a {field} token names a field
// WebhookFormatFields lists.
func KnownWebhookFormatField(name string) bool {
	for _, f := range WebhookFormatFields() {
		if name == f {
			return true
		}
	}
	return false
}

// ExpandWebhookFormat renders a Format template: each {field} token — an
// opening brace, letters, a closing brace — is replaced by what expand
// returns for the field name, and every other byte is literal. Braces around
// anything that is not a bare word stay as written, which is what lets a
// template itself be JSON. It is the one tokenizer both the loader's
// validation and the delivery renderer go through, so the two cannot
// disagree about what counts as a token.
func ExpandWebhookFormat(format string, expand func(field string) string) string {
	var out strings.Builder
	out.Grow(len(format))
	for i := 0; i < len(format); {
		open := strings.IndexByte(format[i:], '{')
		if open < 0 {
			out.WriteString(format[i:])
			break
		}
		open += i
		out.WriteString(format[i:open])
		end := open + 1
		for end < len(format) && isFormatLetter(format[end]) {
			end++
		}
		if end > open+1 && end < len(format) && format[end] == '}' {
			out.WriteString(expand(format[open+1 : end]))
			i = end + 1
			continue
		}
		out.WriteByte('{')
		i = open + 1
	}
	return out.String()
}

func isFormatLetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// MatchWebhookEvent reports whether one subscription pattern admits an event:
// "*" admits every event, "<prefix>.*" admits the family, and anything else
// must match exactly.
func MatchWebhookEvent(pattern, event string) bool {
	if pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, ".*"); ok {
		return strings.HasPrefix(event, prefix+".")
	}
	return pattern == event
}
