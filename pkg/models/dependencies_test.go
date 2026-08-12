package models

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The `dependencies` key has exactly one shape: an object keyed by consumer.
// What is tested here is that every item shape inside it lands on the right
// edges, that the shape written back is the one that was read, and that a
// mistake is located precisely enough to fix.

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

func TestDependenciesEmpty(t *testing.T) {
	eq(t, decode(t, `{}`), nil, "an empty object declares no edges")
	eq(t, decode(t, `null`), nil, "and neither does an absent value")
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
			`dependencies["web"][0]: unknown key "kepe", want provider, kind or keep`},
		// The key an entry sits under is the consumer. An entry naming one
		// itself would mean two things at once, so `consumer` is refused like
		// any other key that does not belong.
		{"consumer named in an entry", `{"web": [{"consumer": "api", "provider": "core"}]}`,
			`dependencies["web"][0]: unknown key "consumer", want provider, kind or keep`},
		{"an array of edges is not a shape", `[{"consumer": "app", "provider": "core"}]`,
			`dependencies wants an object keyed by consumer`},
		{"an empty array is not a shape either", `[]`,
			`dependencies wants an object keyed by consumer`},
		{"not an object", `"core"`,
			`dependencies wants an object keyed by consumer`},
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

func TestDependenciesMarshalYAML(t *testing.T) {
	// The YAML writer goes through MarshalYAML rather than MarshalJSON, and
	// the two have to produce the same shape or a YAML config and a JSON one
	// would come back from `compute --write` spelled differently.
	got, err := Dependencies{
		{Consumer: "web", Provider: "core"},
		{Consumer: "web", Provider: "utils", Keep: true},
	}.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]any{"web": {"core", map[string]any{"provider": "utils", "keep": true}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MarshalYAML() = %+v, want %+v", got, want)
	}
}

func TestProviderList(t *testing.T) {
	// A package's own list holds the same entries a consumer's does, and is
	// written back without the consumer, which the key already says.
	var p ProviderList
	if err := json.Unmarshal([]byte(`["core", {"provider": "utils", "keep": true}]`), &p); err != nil {
		t.Fatal(err)
	}
	want := ProviderList{{Provider: "core"}, {Provider: "utils", Keep: true}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("decoded %+v, want %+v", p, want)
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `["core",{"keep":true,"provider":"utils"}]`
	if string(data) != wantJSON {
		t.Errorf("marshalled %s, want %s", data, wantJSON)
	}
	y, err := p.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(y, []any{"core", map[string]any{"provider": "utils", "keep": true}}) {
		t.Errorf("MarshalYAML() = %+v", y)
	}

	// One provider needs no array.
	var single ProviderList
	if err := json.Unmarshal([]byte(`"core"`), &single); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(single, Providers("core")) {
		t.Errorf("scalar form decoded %+v", single)
	}
}

func TestProviderListErrors(t *testing.T) {
	if _, err := NormalizeProviders(nil, "dependencies"); err != nil {
		t.Errorf("an absent list is not an error: %v", err)
	}
	for _, tc := range []struct{ name, src, want string }{
		{"not a list", `7`, "wants a provider name, or an array"},
		{"bad item", `[7]`, "dependencies[0]: wants a provider name or an object"},
		{"bad json", `{`, "unexpected end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p ProviderList
			err := json.Unmarshal([]byte(tc.src), &p)
			if err == nil {
				t.Fatalf("want an error, got %+v", p)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestDependenciesUnmarshalBadJSON(t *testing.T) {
	var d Dependencies
	if err := json.Unmarshal([]byte(`{`), &d); err == nil {
		t.Error("malformed JSON must be an error")
	}
}

func TestStringKeyedShapes(t *testing.T) {
	// Some YAML readers produce map[any]any; a key that is not a string is
	// not an object dispat can read.
	if _, ok := stringKeyed(map[any]any{"a": 1}); !ok {
		t.Error("map[any]any with string keys is an object")
	}
	if _, ok := stringKeyed(map[any]any{1: "x"}); ok {
		t.Error("a non-string key is not an object")
	}
	if _, ok := stringKeyed([]any{"nope"}); ok {
		t.Error("a list is not an object")
	}
}

func TestAliasTagConfigBehaviour(t *testing.T) {
	// Force defaults to the run's setting and overrides it when set.
	if !(AliasTagConfig{}).ForceEnabled(true) {
		t.Error("an alias with no opinion follows the run")
	}
	if (AliasTagConfig{}).ForceEnabled(false) {
		t.Error("and follows it when it is off too")
	}
	if !(AliasTagConfig{Force: Bool(true)}).ForceEnabled(false) {
		t.Error("an explicit true wins")
	}
	if (AliasTagConfig{Force: Bool(false)}).ForceEnabled(true) {
		t.Error("an explicit false wins")
	}

	// No channels means every channel; naming them is case-insensitive,
	// because a channel is a name a commit message writes by hand.
	if !(AliasTagConfig{}).AppliesTo("anything") {
		t.Error("an alias with no channel list applies everywhere")
	}
	a := AliasTagConfig{Channels: []string{"stable"}}
	if !a.AppliesTo("stable") || !a.AppliesTo("STABLE") {
		t.Error("the named channel matches, case-insensitively")
	}
	if a.AppliesTo("rc") {
		t.Error("an unnamed channel does not")
	}
}

func TestCommitForceEnabled(t *testing.T) {
	var nilCfg *CommitConfig
	if !nilCfg.ForceEnabled() {
		t.Error("force defaults to on, nil-safe")
	}
	if !(&CommitConfig{}).ForceEnabled() {
		t.Error("and on an empty object")
	}
	if (&CommitConfig{Force: Bool(false)}).ForceEnabled() {
		t.Error("and off when it says so")
	}
}
