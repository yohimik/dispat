package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A `scripts` entry has two shapes — one command, or an array of them — and
// they mean the same thing to everything downstream. What is tested here is
// that both land on the same sequence, that the shape written back is the
// shortest one carrying what the script says, and that a mistake names the
// entry it is in.

func decodeScript(t *testing.T, src string) Script {
	t.Helper()
	var s Script
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatalf("decoding %s: %v", src, err)
	}
	return s
}

func eqScript(t *testing.T, got, want Script, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func TestScriptAcceptsBothForms(t *testing.T) {
	eqScript(t, decodeScript(t, `"npm run build"`), Script{"npm run build"},
		"one command is a bare string")
	eqScript(t, decodeScript(t, `["npm ci", "npm run build"]`), Script{"npm ci", "npm run build"},
		"several are an array, in the order they run")
	eqScript(t, decodeScript(t, `["only one"]`), Script{"only one"},
		"an array of one is the same script as the bare string")
	eqScript(t, decodeScript(t, `null`), nil, "an absent value binds nothing")
	eqScript(t, decodeScript(t, `[]`), Script{},
		"an empty array binds nothing either; validation is what rejects it")
}

func TestScriptRejectsWhatIsNotACommand(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`4`, "wants a shell command, or an array of commands run in order"},
		{`{"run": "npm ci"}`, "wants a shell command, or an array of commands run in order"},
		{`["npm ci", 4]`, "scripts[1]: wants a shell command"},
	} {
		var s Script
		err := json.Unmarshal([]byte(c.src), &s)
		if err == nil {
			t.Errorf("decoding %s: want an error, got %+v", c.src, s)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("decoding %s: error %q does not mention %q", c.src, err, c.want)
		}
	}
}

func TestScriptMarshalsTheShortestShape(t *testing.T) {
	// A script authored as a string goes back out as one, so rewriting a config
	// does not grow arrays around the entries it left alone.
	for _, c := range []struct {
		script Script
		want   string
	}{
		{Script{"npm run build"}, `"npm run build"`},
		{Script{"npm ci", "npm run build"}, `["npm ci","npm run build"]`},
		{Script{}, `[]`},
	} {
		out, err := json.Marshal(c.script)
		if err != nil {
			t.Fatalf("marshalling %+v: %v", c.script, err)
		}
		if string(out) != c.want {
			t.Errorf("marshalling %+v = %s, want %s", c.script, out, c.want)
		}
		yaml, err := c.script.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML %+v: %v", c.script, err)
		}
		// The two formats agree on the shape; only the encoder differs.
		if reencoded, _ := json.Marshal(yaml); string(reencoded) != c.want {
			t.Errorf("MarshalYAML %+v = %s, want %s", c.script, reencoded, c.want)
		}
	}
}

func TestScriptRoundTripsThroughAFile(t *testing.T) {
	f := File{Scripts: map[string]Script{
		"build": {"npm ci", "npm run build"},
		"lint":  {"npm run lint"},
	}}
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshalling the file: %v", err)
	}
	var back File
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decoding %s: %v", out, err)
	}
	if !reflect.DeepEqual(back.Scripts, f.Scripts) {
		t.Errorf("round trip:\n got %+v\nwant %+v", back.Scripts, f.Scripts)
	}
}

func TestCommandsFlattensMultiCommandScripts(t *testing.T) {
	// The two levels of ordering — the references, and the commands inside
	// each — flatten into the one order the sequence runs in.
	f := File{Scripts: map[string]Script{
		"a": {"cmd-a1", "cmd-a2"},
		"b": {"cmd-b"},
	}}
	got := f.Commands([]string{"b", "a"})
	want := []string{"cmd-b", "cmd-a1", "cmd-a2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Commands = %v, want %v", got, want)
	}
}
