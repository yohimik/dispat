package harness

import (
	"encoding/json"
	"strings"
)

// Event is one parsed line of a run's --log-format json output: a plan
// diagnostic, a graph line, streamed script output or the final summary.
// Reading the run this way — instead of grepping pretty console text — is
// the machine-readable contract CI ingestion relies on, so asserting against
// it exercises the real thing rather than a screen-scrape of formatting that
// owes nothing to program logic.
type Event map[string]any

// Str returns the string field named key, or "" when it is absent or not a
// string.
func (e Event) Str(key string) string {
	v, _ := e[key].(string)
	return v
}

// Code is the diagnostic "code" field (e.g. "W193", "E130"), set on plan
// diagnostics and on the blocked-package summary line.
func (e Event) Code() string { return e.Str("code") }

// Package is the "package" field, set on every per-package log line.
func (e Event) Package() string { return e.Str("package") }

// ParseEvents parses stdout into one Event per JSON line, skipping lines
// that do not decode as a JSON object. With --log-format json every line
// dispat itself writes is one; the tolerance is only for output this harness
// never expects to see (a stray banner, a partial line on a crash).
func ParseEvents(stdout string) []Event {
	var out []Event
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, Event(m))
	}
	return out
}

// GraphLine returns the plan-graph line for one package: the event naming it
// that also carries its space and no stage, which is the shape only the graph
// print has. Its "message" is the verdict the graph rendered — "● changed",
// "‖ held (Release-As: none)", "⊝ not selected" — so a test can assert on what
// the operator was actually shown. The zero Event when the package has no
// graph line at all.
func GraphLine(events []Event, pkg string) Event {
	for _, e := range events {
		if e.Package() == pkg && e.Str("space") != "" && e.Str("stage") == "" {
			return e
		}
	}
	return Event{}
}

// HasCode reports whether any event carries the given diagnostic code.
func HasCode(events []Event, code string) bool {
	for _, e := range events {
		if e.Code() == code {
			return true
		}
	}
	return false
}

// HasCodeForPackage reports whether any event carries both the given
// diagnostic code and the given package — the form to prefer whenever the
// diagnostic names one, so a test cannot pass on the right code raised
// against the wrong package.
func HasCodeForPackage(events []Event, code, pkg string) bool {
	for _, e := range events {
		if e.Code() == code && e.Package() == pkg {
			return true
		}
	}
	return false
}
