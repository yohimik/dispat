package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// A `versionGroups` entry has two shapes, a bare mode or an object naming the
// three sharing axes, and they mean the same thing to everything
// downstream. What is tested here is that both land on the same three values,
// that the shape written back is the shortest one carrying the rule, and that
// an unknown key is refused rather than dropped, which is what the config
// loader does with every key it does not know.

func decodeGroup(t *testing.T, src string) VersionGroupConfig {
	t.Helper()
	var g VersionGroupConfig
	if err := json.Unmarshal([]byte(src), &g); err != nil {
		t.Fatalf("decoding %s: %v", src, err)
	}
	return g
}

func eqGroup(t *testing.T, got, want VersionGroupConfig, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func TestVersionGroupAcceptsBothForms(t *testing.T) {
	eqGroup(t, decodeGroup(t, `{"versioning": "fixed"}`),
		VersionGroupConfig{Versioning: "fixed"},
		"the scalar form states the semver axis and leaves the others alone")
	eqGroup(t, decodeGroup(t, `{"versioning": {"semver": "fixedMajorMinor"}}`),
		VersionGroupConfig{Versioning: "fixedMajorMinor"},
		"the object form with one axis means the same as the scalar")
	eqGroup(t, decodeGroup(t,
		`{"versioning": {"semver": "fixedMajorMinor", "counter": "independent", "channels": "independent"}}`),
		VersionGroupConfig{Versioning: "fixedMajorMinor", Counter: "independent", Channels: "independent"},
		"all three axes")
	eqGroup(t, decodeGroup(t, `{"versioning": {"SemVer": "fixed", "Counter": "independent"}}`),
		VersionGroupConfig{Versioning: "fixed", Counter: "independent"},
		"axis keys are matched folded, like every key of the config language")
	eqGroup(t, decodeGroup(t, `{}`), VersionGroupConfig{},
		"an entry that says nothing is the zero rule")
	eqGroup(t, decodeGroup(t, `{"versioning": null}`), VersionGroupConfig{},
		"an absent value states nothing either")
}

func TestVersionGroupWritesTheShortestForm(t *testing.T) {
	for _, c := range []struct {
		group VersionGroupConfig
		want  string
	}{
		{VersionGroupConfig{Versioning: "fixed"}, `{"versioning":"fixed"}`},
		{VersionGroupConfig{}, `{}`},
		{VersionGroupConfig{Versioning: "fixedMajor", Counter: "independent"},
			`{"versioning":{"semver":"fixedMajor","counter":"independent"}}`},
		{VersionGroupConfig{Versioning: "fixedMajorMinor", Counter: "independent", Channels: "independent"},
			`{"versioning":{"semver":"fixedMajorMinor","counter":"independent","channels":"independent"}}`},
	} {
		data, err := json.Marshal(c.group)
		if err != nil {
			t.Fatalf("marshalling %+v: %v", c.group, err)
		}
		if string(data) != c.want {
			t.Errorf("marshalling %+v:\n got %s\nwant %s", c.group, data, c.want)
		}
		eqGroup(t, decodeGroup(t, string(data)), c.group, "round trip")

		// The YAML counterpart writes the same value under the same key, so a
		// config rewritten in either format reads back the same rule.
		node, err := c.group.MarshalYAML()
		if err != nil {
			t.Fatalf("marshalling %+v to yaml: %v", c.group, err)
		}
		yamlJSON, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("rendering the yaml node of %+v: %v", c.group, err)
		}
		if string(yamlJSON) != c.want {
			t.Errorf("the yaml node of %+v:\n got %s\nwant %s", c.group, yamlJSON, c.want)
		}
	}
}

func TestVersionGroupRejectsWhatIsNotARule(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`{"versioning": {"semver": "fixed", "conter": "independent"}}`, `unknown key "conter"`},
		{`{"versioning": {"semver": "fixed", "Semver": "fixedMajor"}}`, "are the same key"},
		{`{"versioning": {"counter": 3}}`, "counter wants a value"},
		{`{"versioning": 3}`, "wants a versioning mode"},
		{`{"counter": "independent"}`, "unknown field"},
	} {
		var g VersionGroupConfig
		err := json.Unmarshal([]byte(c.src), &g)
		if err == nil {
			t.Errorf("decoding %s: wanted an error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("decoding %s:\n got %q\nwant it to mention %q", c.src, err, c.want)
		}
	}
}

func TestVersionGroupNormaliserAndDecoderAgree(t *testing.T) {
	// The CLI's config reader calls the normaliser directly on the decoded
	// value; encoding/json reaches it through UnmarshalJSON. Both must answer
	// the same rule, which is the whole reason there is one implementation.
	raw := map[string]any{"semver": "fixedMajor", "channels": "independent", "counter": "independent"}
	out, err := NormalizeVersionGroupVersioning(raw, "versionGroups[\"cli\"]: versioning")
	if err != nil {
		t.Fatalf("normalising: %v", err)
	}
	eqGroup(t, out, decodeGroup(t,
		`{"versioning": {"semver": "fixedMajor", "channels": "independent", "counter": "independent"}}`),
		"the two entry points agree")

	if _, err := NormalizeVersionGroupVersioning([]any{"fixed"}, "here"); err == nil ||
		!strings.Contains(err.Error(), "here: wants a versioning mode") {
		t.Errorf("a list is not a rule: %v", err)
	}
	if out, err := NormalizeVersionGroupVersioning(nil, "here"); err != nil || out != (VersionGroupConfig{}) {
		t.Errorf("an absent value states nothing: %+v %v", out, err)
	}
}
