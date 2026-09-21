package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// `runOnly` is a key whose misreading puts a signing build on a machine the
// operator meant to exclude, so what is tested here is the reading itself:
// every shape a file may write, every shape it may not, and the promise that
// what the model writes back is what the file wrote.
//
// Stdlib only, like every test in this module: pkg/models is a published
// module and a test dependency would travel with it.

// TestRunOnlyReadsBothShapes: one word states both stages, and a pair states
// them apart.
func TestRunOnlyReadsBothShapes(t *testing.T) {
	for name, row := range map[string]struct {
		document string
		want     RunOnly
	}{
		"one word for both stages": {
			document: `"orchestrator"`,
			want:     RunOnly{Build: RunOnlyOrchestrator, Publish: RunOnlyOrchestrator},
		},
		"the default written out": {
			document: `"both"`,
			want:     RunOnly{Build: RunOnlyBoth, Publish: RunOnlyBoth},
		},
		"a pair naming each stage": {
			document: `["worker", "orchestrator"]`,
			want:     RunOnly{Build: RunOnlyWorker, Publish: RunOnlyOrchestrator},
		},
		"a pair whose halves agree": {
			document: `["worker", "worker"]`,
			want:     RunOnly{Build: RunOnlyWorker, Publish: RunOnlyWorker},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var got RunOnly
			if err := json.Unmarshal([]byte(row.document), &got); err != nil {
				t.Fatalf("reading %s: %v", row.document, err)
			}
			if got != row.want {
				t.Fatalf("read %s as %+v, want %+v", row.document, got, row.want)
			}
		})
	}
}

// TestRunOnlyRefusesWhatTheKeyDoesNotSay: a value outside the vocabulary and
// a list that does not name both stages are refused rather than read as the
// nearest thing to them, and the refusal names the level it is about.
func TestRunOnlyRefusesWhatTheKeyDoesNotSay(t *testing.T) {
	for name, row := range map[string]struct {
		raw  any
		want string
	}{
		"a misspelled value":        {raw: "orchestartor", want: "is not a placement"},
		"a value of another key":    {raw: "any", want: "is not a placement"},
		"a list of one":             {raw: []any{"worker"}, want: "states both stages"},
		"a list of three":           {raw: []any{"worker", "worker", "worker"}, want: "states both stages"},
		"an empty list":             {raw: []any{}, want: "states both stages"},
		"a misspelled stage value":  {raw: []any{"worker", "orchestartor"}, want: "is not a placement"},
		"a number where a word is":  {raw: []any{"worker", 3}, want: "wants"},
		"an object where a word is": {raw: map[string]any{"build": "worker"}, want: "wants"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeRunOnly(row.raw, `packages["core"].runOnly`)
			if err == nil {
				t.Fatalf("read %v without refusing it", row.raw)
			}
			if !strings.Contains(err.Error(), row.want) {
				t.Fatalf("refusal %q does not say %q", err, row.want)
			}
			if !strings.Contains(err.Error(), `packages["core"].runOnly`) {
				t.Fatalf("refusal %q does not name the level it is about", err)
			}
		})
	}
}

// TestRunOnlyWritesTheShortestShape: a value whose stages agree goes back out
// as the one word it came in as, and only a value that says the stages differ
// grows a list around itself.
func TestRunOnlyWritesTheShortestShape(t *testing.T) {
	for name, row := range map[string]struct {
		value RunOnly
		want  string
	}{
		"both stages agree": {
			value: RunOnly{Build: RunOnlyWorker, Publish: RunOnlyWorker},
			want:  `"worker"`,
		},
		"the stages differ": {
			value: RunOnly{Build: RunOnlyWorker, Publish: RunOnlyOrchestrator},
			want:  `["worker","orchestrator"]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			written, err := json.Marshal(row.value)
			if err != nil {
				t.Fatalf("writing %+v: %v", row.value, err)
			}
			if string(written) != row.want {
				t.Fatalf("wrote %+v as %s, want %s", row.value, written, row.want)
			}
			yaml, err := row.value.MarshalYAML()
			if err != nil {
				t.Fatalf("writing %+v as YAML: %v", row.value, err)
			}
			rewritten, err := json.Marshal(yaml)
			if err != nil {
				t.Fatalf("writing the YAML shape of %+v: %v", row.value, err)
			}
			if string(rewritten) != row.want {
				t.Fatalf("the YAML shape of %+v is %s, want %s", row.value, rewritten, row.want)
			}
		})
	}
}

// TestRunOnlyResolvesAnUnstatedValueToBoth: nil is a level that said nothing,
// which is what every caller has to be able to ask without knowing whether
// anybody stated the key at all.
func TestRunOnlyResolvesAnUnstatedValueToBoth(t *testing.T) {
	var absent *RunOnly
	if absent.ResolveBuild() != RunOnlyBoth || absent.ResolvePublish() != RunOnlyBoth {
		t.Fatalf("an unstated runOnly resolved to %q/%q, want %q",
			absent.ResolveBuild(), absent.ResolvePublish(), RunOnlyBoth)
	}
	empty := &RunOnly{}
	if empty.ResolveBuild() != RunOnlyBoth || empty.ResolvePublish() != RunOnlyBoth {
		t.Fatalf("an empty runOnly resolved to %q/%q, want %q",
			empty.ResolveBuild(), empty.ResolvePublish(), RunOnlyBoth)
	}
	stated := &RunOnly{Build: RunOnlyWorker, Publish: RunOnlyOrchestrator}
	if stated.ResolveBuild() != RunOnlyWorker || stated.ResolvePublish() != RunOnlyOrchestrator {
		t.Fatalf("a stated runOnly resolved to %q/%q", stated.ResolveBuild(), stated.ResolvePublish())
	}
}

// TestRunOnlyLevelsTellAStatementFromSilence: the key rides the ordinary
// ladder and replaces whole, so every level that carries it has to keep "this
// level said nothing" apart from "this level said both", which is what the
// pointer is for.
func TestRunOnlyLevelsTellAStatementFromSilence(t *testing.T) {
	for name, level := range map[string]any{
		"File":          &File{},
		"SpaceConfig":   &SpaceConfig{},
		"SpaceFile":     &SpaceFile{},
		"PackageConfig": &PackageConfig{},
	} {
		t.Run(name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(`{}`), level); err != nil {
				t.Fatalf("reading an empty %s: %v", name, err)
			}
			if stated := runOnlyOf(level); stated != nil {
				t.Fatalf("%s read silence as %+v", name, stated)
			}
			if err := json.Unmarshal([]byte(`{"runOnly": "worker"}`), level); err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			stated := runOnlyOf(level)
			if stated == nil || stated.Build != RunOnlyWorker || stated.Publish != RunOnlyWorker {
				t.Fatalf("%s read a stated runOnly as %+v", name, stated)
			}
			written, err := json.Marshal(level)
			if err != nil {
				t.Fatalf("writing %s: %v", name, err)
			}
			if !strings.Contains(string(written), `"runOnly":"worker"`) {
				t.Fatalf("%s wrote the stated value back as %s", name, written)
			}
		})
	}
}

// runOnlyOf reads the key out of a decoded level by field name, so one table
// can ask every level the same question without four type switches.
func runOnlyOf(level any) *RunOnly {
	value, _ := reflect.ValueOf(level).Elem().FieldByName("RunOnly").Interface().(*RunOnly)
	return value
}
