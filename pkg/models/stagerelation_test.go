package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// `isBuildWaitingPublish` has two shapes, a boolean or an object naming what a
// consumer's build waits for and what a failed provider does to it, and they
// mean the same thing to everything downstream. What is tested here is that
// both land on the same relation, that the two relations a boolean names go
// back out as that boolean, that the defaults of `isBlocking` follow the wait,
// and that the object's refusals are refusals rather than silently dropped
// keys.
//
// Stdlib testing only: pkg/* is a published module and its tests may not ask
// callers to carry an assertion library.

func decodeRelation(t *testing.T, src string) StageRelation {
	t.Helper()
	var r StageRelation
	if err := json.Unmarshal([]byte(src), &r); err != nil {
		t.Fatalf("decoding %s: %v", src, err)
	}
	return r
}

// meaning renders a relation as the pair everything downstream reads, so two
// spellings of one relation can be compared without comparing pointers.
func meaning(r StageRelation) (StageWait, bool) {
	return r.ResolveBuildWait(), r.IsProviderBlocking()
}

func eqRelation(t *testing.T, got StageRelation, wantWait StageWait, wantBlocking bool, what string) {
	t.Helper()
	wait, isBlocking := meaning(got)
	if wait != wantWait || isBlocking != wantBlocking {
		t.Errorf("%s:\n got %s/%v\nwant %s/%v", what, wait, isBlocking, wantWait, wantBlocking)
	}
}

func TestStageRelationAcceptsBothForms(t *testing.T) {
	eqRelation(t, decodeRelation(t, `false`), StageWaitBuild, false,
		"false is the build relation, which leaves a consumer's own reason standing")
	eqRelation(t, decodeRelation(t, `true`), StageWaitPublish, true,
		"true is the publish relation, which outranks it")
	eqRelation(t, decodeRelation(t, `{"build": "build"}`), StageWaitBuild, false,
		"the object spelling of false")
	eqRelation(t, decodeRelation(t, `{"build": "publish"}`), StageWaitPublish, true,
		"the object spelling of true")
	eqRelation(t, decodeRelation(t, `{"build": "none"}`), StageWaitNone, false,
		"none does not block by default: a consumer with work of its own proceeds")
	eqRelation(t, decodeRelation(t, `{"build": "none", "isBlocking": false}`), StageWaitNone, false,
		"and the block may be relaxed")
	eqRelation(t, decodeRelation(t, `{"build": "build", "isBlocking": true}`), StageWaitBuild, true,
		"a build relation may block")
	eqRelation(t, decodeRelation(t, `{"Build": "none", "IsBlocking": true}`), StageWaitNone, true,
		"keys are matched folded, like every key of the config language")

	var unstated *StageRelation
	eqRelation(t, StageRelation{}, StageWaitBuild, false, "the zero relation is the default one")
	if wait := unstated.ResolveBuildWait(); wait != StageWaitBuild {
		t.Errorf("a nil relation waits for %s, want %s", wait, StageWaitBuild)
	}
	if unstated.IsProviderBlocking() {
		t.Error("a nil relation does not block")
	}

	// A null states nothing and must leave a relation alone rather than
	// resetting it, which is what encoding/json's own decoding of an absent
	// value does.
	kept := StageRelation{Build: StageWaitNone}
	if err := kept.UnmarshalJSON([]byte(`null`)); err != nil {
		t.Fatalf("decoding null: %v", err)
	}
	eqRelation(t, kept, StageWaitNone, false, "null left the relation alone")
}

func TestStageRelationWritesTheShortestForm(t *testing.T) {
	for _, c := range []struct {
		relation StageRelation
		want     string
	}{
		{StageRelation{}, `false`},
		{*StageRelationOf(false), `false`},
		{*StageRelationOf(true), `true`},
		{StageRelation{Build: StageWaitBuild, IsBlocking: Bool(false)}, `false`},
		{StageRelation{Build: StageWaitPublish, IsBlocking: Bool(true)}, `true`},
		{StageRelation{Build: StageWaitNone}, `{"build":"none"}`},
		{StageRelation{Build: StageWaitNone, IsBlocking: Bool(false)}, `{"build":"none"}`},
		{StageRelation{Build: StageWaitNone, IsBlocking: Bool(true)},
			`{"build":"none","isBlocking":true}`},
		{StageRelation{Build: StageWaitBuild, IsBlocking: Bool(true)},
			`{"build":"build","isBlocking":true}`},
	} {
		data, err := json.Marshal(c.relation)
		if err != nil {
			t.Fatalf("marshalling %+v: %v", c.relation, err)
		}
		if string(data) != c.want {
			t.Errorf("marshalling %+v:\n got %s\nwant %s", c.relation, data, c.want)
		}
		wait, isBlocking := meaning(c.relation)
		eqRelation(t, decodeRelation(t, string(data)), wait, isBlocking, "round trip of "+c.want)

		// The YAML counterpart writes the same value, so a config rewritten in
		// either format reads back the same relation.
		node, err := c.relation.MarshalYAML()
		if err != nil {
			t.Fatalf("marshalling %+v to yaml: %v", c.relation, err)
		}
		yamlJSON, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("rendering the yaml node of %+v: %v", c.relation, err)
		}
		if string(yamlJSON) != c.want {
			t.Errorf("the yaml node of %+v:\n got %s\nwant %s", c.relation, yamlJSON, c.want)
		}
	}
}

func TestStageRelationRejectsWhatIsNotARelation(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`{"isBlocking": true}`, "build is required"},
		{`{"build": "none", "blocking": true}`, `unknown key "blocking"`},
		{`{"build": "published"}`, `build "published" is not one of none, build or publish`},
		{`{"build": "Publish"}`, `build "Publish" is not one of none, build or publish`},
		{`{"build": 3}`, "build wants none, build or publish"},
		{`{"build": "none", "isBlocking": "yes"}`, "isBlocking wants true or false"},
		{`{"build": "publish", "isBlocking": false}`, "cannot state isBlocking: false"},
		{`["none"]`, "wants true or false, or an object"},
		{`3`, "wants true or false, or an object"},
	} {
		var r StageRelation
		err := json.Unmarshal([]byte(c.src), &r)
		if err == nil {
			t.Errorf("decoding %s: wanted an error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("decoding %s:\n got %q\nwant it to mention %q", c.src, err, c.want)
		}
		if !strings.Contains(err.Error(), "isBuildWaitingPublish") {
			t.Errorf("decoding %s: %q does not name the key", c.src, err)
		}
	}
	// Called directly rather than through json.Unmarshal, which rejects
	// malformed input before it reaches any unmarshaller.
	if err := (&StageRelation{}).UnmarshalJSON([]byte(`{`)); err == nil {
		t.Error("malformed JSON was accepted")
	}
}

func TestStageRelationNormaliserAndDecoderAgree(t *testing.T) {
	// The CLI's config reader calls the normaliser directly on the decoded
	// value; encoding/json reaches it through UnmarshalJSON. Both must answer
	// the same relation, which is the whole reason there is one implementation.
	// The map[any]any is what some YAML readers produce.
	raw := map[any]any{"build": "none", "isBlocking": false}
	out, err := NormalizeStageRelation(raw, `spaces["infra"]: isBuildWaitingPublish`)
	if err != nil {
		t.Fatalf("normalising: %v", err)
	}
	eqRelation(t, *out, StageWaitNone, false, "the two entry points agree")

	if out, err := NormalizeStageRelation(nil, "here"); err != nil || out != nil {
		t.Errorf("an absent value states nothing: %+v %v", out, err)
	}
	_, err = NormalizeStageRelation(map[string]any{"build": "none", "isBlocking": 1}, "here")
	if err == nil || !strings.Contains(err.Error(), "here: isBlocking wants true or false") {
		t.Errorf("where names the level that is wrong: %v", err)
	}
}
