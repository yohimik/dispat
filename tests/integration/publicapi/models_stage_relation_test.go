package publicapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/models"
)

// TestPublicAPIModelStageRelationShapes round-trips `isBuildWaitingPublish`
// through both shapes the key accepts and every error its normaliser reports,
// the way a program authoring a dispat configuration outside this repository
// would reach them: through the published type rather than through a loaded
// configuration.

func TestPublicAPIModelStageRelationShapes(t *testing.T) {
	t.Run("the two relations a boolean names marshal back as that boolean", func(t *testing.T) {
		for _, c := range []struct {
			relation models.StageRelation
			want     string
		}{
			{models.StageRelation{}, `false`},
			{*models.StageRelationOf(false), `false`},
			{*models.StageRelationOf(true), `true`},
			{models.StageRelation{Build: models.StageWaitBuild, IsBlocking: models.Bool(false)}, `false`},
			{models.StageRelation{Build: models.StageWaitNone}, `{"build":"none"}`},
			{models.StageRelation{Build: models.StageWaitNone, IsBlocking: models.Bool(true)},
				`{"build":"none","isBlocking":true}`},
			{models.StageRelation{Build: models.StageWaitBuild, IsBlocking: models.Bool(true)},
				`{"build":"build","isBlocking":true}`},
		} {
			data, err := json.Marshal(c.relation)
			if err != nil || string(data) != c.want {
				t.Errorf("Marshal(%+v) = %s, %v, want %s", c.relation, data, err, c.want)
			}
			node, err := c.relation.MarshalYAML()
			if err != nil {
				t.Fatalf("MarshalYAML(%+v): %v", c.relation, err)
			}
			yamlJSON, err := json.Marshal(node)
			if err != nil || string(yamlJSON) != c.want {
				t.Errorf("MarshalYAML(%+v) = %s, %v, want %s", c.relation, yamlJSON, err, c.want)
			}
		}
	})

	t.Run("both written shapes decode to the same relation", func(t *testing.T) {
		for _, c := range []struct {
			src        string
			wait       models.StageWait
			isBlocking bool
		}{
			{`false`, models.StageWaitBuild, false},
			{`{"build": "build"}`, models.StageWaitBuild, false},
			{`true`, models.StageWaitPublish, true},
			{`{"build": "publish"}`, models.StageWaitPublish, true},
			{`{"build": "none"}`, models.StageWaitNone, false},
			{`{"BUILD": "none", "ISBLOCKING": false}`, models.StageWaitNone, false},
		} {
			var relation models.StageRelation
			if err := json.Unmarshal([]byte(c.src), &relation); err != nil {
				t.Fatalf("Unmarshal(%s): %v", c.src, err)
			}
			if got := relation.ResolveBuildWait(); got != c.wait {
				t.Errorf("%s: ResolveBuildWait() = %s, want %s", c.src, got, c.wait)
			}
			if got := relation.IsProviderBlocking(); got != c.isBlocking {
				t.Errorf("%s: IsProviderBlocking() = %v, want %v", c.src, got, c.isBlocking)
			}
		}
	})

	t.Run("an unstated relation is the default one", func(t *testing.T) {
		var absent *models.StageRelation
		if absent.ResolveBuildWait() != models.StageWaitBuild || absent.IsProviderBlocking() {
			t.Error("a nil relation is not the relation the key's false has always named")
		}
		// A null states nothing and leaves a relation alone.
		relation := models.StageRelation{Build: models.StageWaitNone}
		if err := relation.UnmarshalJSON([]byte(`null`)); err != nil {
			t.Fatalf("Unmarshal null: %v", err)
		}
		if relation.ResolveBuildWait() != models.StageWaitNone {
			t.Errorf("null reset the relation to %s", relation.ResolveBuildWait())
		}
	})

	t.Run("a malformed relation is refused", func(t *testing.T) {
		for src, want := range map[string]string{
			`{"isBlocking": true}`:                      "build is required",
			`{"build": "published"}`:                    "is not one of none, build or publish",
			`{"build": "Publish"}`:                      "is not one of none, build or publish",
			`{"build": 1}`:                              "build wants none, build or publish",
			`{"build": "none", "keep": true}`:           `unknown key "keep"`,
			`{"build": "none", "isBlocking": "y"}`:      "isBlocking wants true or false",
			`{"build": "publish", "isBlocking": false}`: "cannot state isBlocking: false",
			`["none"]`: "wants true or false, or an object",
		} {
			var relation models.StageRelation
			err := json.Unmarshal([]byte(src), &relation)
			if err == nil {
				t.Errorf("Unmarshal(%s) was accepted", src)
				continue
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Unmarshal(%s) = %q, want it to mention %q", src, err, want)
			}
		}
		if err := (&models.StageRelation{}).UnmarshalJSON([]byte(`{`)); err == nil {
			t.Error("UnmarshalJSON accepted malformed JSON")
		}
	})

	t.Run("the normaliser reads every shape a reader can hand it", func(t *testing.T) {
		if got, err := models.NormalizeStageRelation(nil, "isBuildWaitingPublish"); err != nil || got != nil {
			t.Errorf("nil = %v, %v", got, err)
		}
		if got, err := models.NormalizeStageRelation(true, "isBuildWaitingPublish"); err != nil ||
			got.ResolveBuildWait() != models.StageWaitPublish {
			t.Errorf("boolean = %v, %v", got, err)
		}
		// map[any]any is what some YAML readers produce.
		got, err := models.NormalizeStageRelation(
			map[any]any{"build": "none", "isBlocking": false}, `spaces["infra"]: isBuildWaitingPublish`)
		if err != nil || got.ResolveBuildWait() != models.StageWaitNone || got.IsProviderBlocking() {
			t.Errorf("yaml object = %v, %v", got, err)
		}
		_, err = models.NormalizeStageRelation(map[string]any{"build": "none", "isBlocking": 1}, "here")
		if err == nil || !strings.Contains(err.Error(), "here: isBlocking") {
			t.Errorf("error = %v, want the level named", err)
		}
	})
}
