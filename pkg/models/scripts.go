package models

import (
	"encoding/json"
	"fmt"
)

// This file is the value side of the `scripts` key: the shapes one entry may
// be written in, and the one shape it is written back in.
//
// The reference side already worked this way — a `flow` entry or a `run` hook
// names one script or an array of them, run in order — and this makes the two
// sides symmetric. Everything downstream of decoding sees a sequence of
// commands and never learns which form the file used.

// Script is what one `scripts` entry binds a name to: the commands it runs, in
// order.
//
// One command is written as a bare string, which is what almost every script
// is:
//
//	"scripts": { "build": "npm run build" }
//
// Several are written as an array, and run as separate invocations of the
// configured shell, in order:
//
//	"scripts": { "build": ["npm ci", "npm run build"] }
//
// Separate invocations, rather than one shell string joined together, is what
// keeps each command reaching the shell exactly as it was written: a sequence
// is not a rewrite of the operator's text. It also means no shell state
// carries between entries — a `cd` in one does not move the next, which starts
// where the script started. The commands fold into whichever sequence names
// the script, so a failure behaves the way that sequence's failures behave:
// fail-fast for a release-gating stage, warn-and-continue for a hook that
// observes work already done.
type Script []string

// MarshalJSON writes the shortest shape that carries everything the script
// says: a bare string for the one-command case, an array otherwise. A script
// that came in as a string goes back out as one, so a config dispat rewrites
// does not grow arrays around every entry it left alone.
func (s Script) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.canonical())
}

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (s Script) MarshalYAML() (any, error) {
	return s.canonical(), nil
}

// canonical renders the script in the shape both marshallers write.
func (s Script) canonical() any {
	if len(s) == 1 {
		return s[0]
	}
	return []string(s)
}

// UnmarshalJSON accepts either form the entry may be written in.
func (s *Script) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	script, err := NormalizeScript(raw, "scripts")
	if err != nil {
		return err
	}
	*s = script
	return nil
}

// NormalizeScript expands one `scripts` value into the command sequence
// everything downstream works with. where names the entry for error messages,
// since the same shape is read at four levels and the reader has to be told
// which one is wrong.
//
// It is the single implementation behind both entry points — UnmarshalJSON
// here, and the CLI's config reader, whose weak decoding lifts a scalar into a
// one-element sequence the same way. Two readers of one syntax would be two
// syntaxes eventually.
func NormalizeScript(raw any, where string) (Script, error) {
	switch x := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return Script{x}, nil
	case []any:
		out := make(Script, 0, len(x))
		for i, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: wants a shell command", where, i)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return Script(x), nil
	}
	return nil, fmt.Errorf("%s: wants a shell command, or an array of commands run in order", where)
}
