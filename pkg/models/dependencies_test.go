package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The `dependencies` key is the one place the config language accepts more
// than one shape for the same thing, so what is tested here is that every
// shape lands on the same edges, that the shape written back is the canonical
// one, and that a mistake is located precisely enough to fix.

// decode is the public entry point a config file goes through.
func decode(t *testing.T, src string) Dependencies {
	t.Helper()
	var d Dependencies
	if err := json.Unmarshal([]byte(src), &d); err != nil {
		t.Fatalf("decoding %s: %v", src, err)
	}
	return d
}

func decodeErr(t *testing.T, src string) string {
	t.Helper()
	var d Dependencies
	err := json.Unmarshal([]byte(src), &d)
	if err == nil {
		t.Fatalf("decoding %s: want an error, got %v", src, d)
	}
	return err.Error()
}

func eq(t *testing.T, got, want Dependencies, what string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func TestDependenciesMapForm(t *testing.T) {
	// Consumers are visited in sorted order, because an object has no order of
	// its own and the edge list has to be the same on every load.
	eq(t, decode(t, `{
		"web": ["core", {"provider": "utils", "keep": true}],
		"api": {"provider": "core", "kind": "devDependencies"},
		"cli": "core"
	}`), Dependencies{
		{Consumer: "api", Provider: "core", Kind: "devDependencies"},
		{Consumer: "cli", Provider: "core"},
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
	}, "the map form, with every item shape it accepts")
}

func TestDependenciesEdgeListForm(t *testing.T) {
	eq(t, decode(t, `[
		{"consumer": "app", "provider": "core", "kind": "peerDependencies"},
		{"web": ["core", {"provider": "utils", "keep": true}]}
	]`), Dependencies{
		{Consumer: "app", Provider: "core", Kind: "peerDependencies"},
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
	}, "full edges and consumer-keyed items in one array")
}

func TestDependenciesEmpty(t *testing.T) {
	eq(t, decode(t, `{}`), nil, "an empty object declares no edges")
	eq(t, decode(t, `[]`), nil, "and neither does an empty array")
	eq(t, decode(t, `null`), nil, "nor an absent value")
}

func TestDependenciesMarshalsTheCanonicalForm(t *testing.T) {
	// Whatever a config was authored as, this is what is written back: an
	// object keyed by consumer, each provider in the shortest shape carrying
	// everything the edge says.
	deps := Dependencies{
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
		{Consumer: "api", Provider: "core", Kind: "devDependencies"},
	}
	data, err := json.Marshal(deps)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"api":[{"kind":"devDependencies","provider":"core"}],` +
		`"web":["core",{"keep":true,"provider":"utils"}]}`
	if string(data) != want {
		t.Errorf("marshalled:\n got %s\nwant %s", data, want)
	}

	// And it reads back as itself, consumers sorted: the write-read pair is
	// what `dispat compute --write` relies on to converge on a second run.
	eq(t, decode(t, string(data)), Dependencies{
		{Consumer: "api", Provider: "core", Kind: "devDependencies"},
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
	}, "round trip")
}

func TestDependenciesMarshalEmpty(t *testing.T) {
	// Not "null": a key holding null no longer says "nothing depends on
	// anything", it says nothing at all, and the next reader cannot tell the
	// difference between that and a truncated file.
	data, err := json.Marshal(Dependencies(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}" {
		t.Errorf("marshalled empty dependencies = %s, want {}", data)
	}
}

func TestDependenciesErrorsLocateTheEntry(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"value is neither a name nor a list", `{"web": 7}`,
			`dependencies["web"]: wants a provider name, or an array of provider names and objects`},
		{"item is neither a name nor an object", `{"web": [7]}`,
			`dependencies["web"][0]: wants a provider name or an object`},
		{"provider is not a name", `{"web": [{"provider": 7}]}`,
			`dependencies["web"][0]: provider wants a package name`},
		{"keep is not a boolean", `{"web": [{"provider": "core", "keep": "yes"}]}`,
			`dependencies["web"][0]: keep wants true or false`},
		{"unknown key", `{"web": [{"provider": "core", "kepe": true}]}`,
			`dependencies["web"][0]: unknown key "kepe", want consumer, provider, kind or keep`},
		{"consumer named twice", `{"web": [{"consumer": "api", "provider": "core"}]}`,
			`dependencies["web"][0]: consumer is already "api" here, so the entry must not name another one`},
		{"array item located by position", `[{"web": 7}]`,
			`dependencies[0]["web"]: wants a provider name`},
		{"array item is not an object", `["core"]`,
			`dependencies[0]: wants an object`},
		{"neither an object nor an array", `"core"`,
			`dependencies wants an object keyed by consumer, or an array of {consumer, provider} edges`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decodeErr(t, tc.src); !strings.Contains(got, tc.want) {
				t.Errorf("error = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestDependenciesGrouped(t *testing.T) {
	got := Dependencies{
		{Consumer: "web", Provider: "core"},
		{Consumer: "api", Provider: "core"},
		{Consumer: "web", Provider: "utils"},
	}.Grouped()
	want := map[string][]DependencyConfig{
		"web": {{Consumer: "web", Provider: "core"}, {Consumer: "web", Provider: "utils"}},
		"api": {{Consumer: "api", Provider: "core"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Grouped() = %+v, want %+v", got, want)
	}
}
