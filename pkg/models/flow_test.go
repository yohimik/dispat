package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlowReadsEveryEntryForm: a string, a list and the object form all load,
// the object form of a built-in stage splits into its script half and its
// stage half, and a key that is no built-in defines a stage with its hooks.
func TestFlowReadsEveryEntryForm(t *testing.T) {
	var flow SpaceFlowConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"build": "build",
		"tests": ["unit", "smoke"],
		"announce": {"script": "announce", "label": "Tell the world", "after": "build"},
		"publish": {"label": "Ship"},
		"lint": {"script": ["lint"], "parallel": ["tests"], "label": "Lint"},
		"beforeLint": "pre-lint",
		"postLint": ["post-lint"],
		"docs": "docs"
	}`), &flow))

	assert.Equal(t, []string{"build"}, flow.Build)
	assert.Equal(t, []string{"unit", "smoke"}, flow.Tests)
	assert.Equal(t, []string{"announce"}, flow.Announce, "the object form's script fills the typed field")
	assert.Nil(t, flow.Publish, "an object without a script states no script")
	assert.Equal(t, map[string]StageConfig{
		"announce": {Label: "Tell the world", After: []string{"build"}},
		"publish":  {Label: "Ship"},
		"lint":     {Script: []string{"lint"}, Parallel: []string{"tests"}, Label: "Lint"},
		"docs":     {Script: []string{"docs"}},
	}, flow.Stages)
	assert.Equal(t, map[string][]string{"beforeLint": {"pre-lint"}, "postLint": {"post-lint"}}, flow.StageHooks)
}

// TestFlowRoundTrips: what the marshaller writes, the unmarshaller reads back
// as the same flow, in the shortest form each entry has.
func TestFlowRoundTrips(t *testing.T) {
	flow := SpaceFlowConfig{
		Build: []string{"build"}, Publish: []string{"publish"},
		Stages: map[string]StageConfig{
			"announce": {Label: "Tell", After: []string{"build"}},
			"lint":     {Script: []string{"lint"}, Before: []string{"publish"}},
			"docs":     {Script: []string{"docs"}},
		},
		StageHooks: map[string][]string{"beforeLint": {"pre"}},
	}
	data, err := json.Marshal(flow)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, []any{"build"}, raw["build"], "a plain entry stays a list")
	assert.Equal(t, []any{"docs"}, raw["docs"], "a defined stage with scripts alone is written as its list")
	assert.Equal(t, map[string]any{"label": "Tell", "after": []any{"build"}}, raw["announce"])
	assert.Equal(t, map[string]any{"script": []any{"lint"}, "before": []any{"publish"}}, raw["lint"])
	assert.Equal(t, []any{"pre"}, raw["beforeLint"])

	var back SpaceFlowConfig
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, flow, back)
}

// TestFlowWritesABuiltInStageScriptHeldInItsObject: a Go caller may give a
// built-in stage its scripts in the object half alone; they are written as
// the entry's scripts and read back into the typed field.
func TestFlowWritesABuiltInStageScriptHeldInItsObject(t *testing.T) {
	flow := SpaceFlowConfig{Stages: map[string]StageConfig{
		"announce": {Script: []string{"boom"}, After: []string{"build"}},
		"tests":    {Script: []string{"unit"}},
	}}
	data, err := json.Marshal(flow)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, map[string]any{"script": []any{"boom"}, "after": []any{"build"}}, raw["announce"])
	assert.Equal(t, []any{"unit"}, raw["tests"])

	var back SpaceFlowConfig
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, []string{"boom"}, back.Announce)
	assert.Equal(t, []string{"unit"}, back.Tests)
	assert.Equal(t, map[string]StageConfig{"announce": {After: []string{"build"}}}, back.Stages)

	typed := SpaceFlowConfig{Announce: []string{"typed"}, Stages: map[string]StageConfig{"announce": {Script: []string{"object"}}}}
	data, err = json.Marshal(typed)
	require.NoError(t, err)
	assert.JSONEq(t, `{"announce": ["typed"]}`, string(data), "the typed field wins where both are set")
}

// TestFlowStagePlacement: a stage places itself by a before or an after; a
// parallel constraint or a label alone leaves it in its default place, and
// is still written in the object form.
func TestFlowStagePlacement(t *testing.T) {
	assert.True(t, StageConfig{Before: []string{"publish"}}.IsPlaced())
	assert.True(t, StageConfig{After: []string{"build"}, Parallel: []string{"tests"}}.IsPlaced())
	assert.False(t, StageConfig{Parallel: []string{"tests"}}.IsPlaced())
	assert.False(t, StageConfig{Label: "Lint", After: []string{}}.IsPlaced(), "an empty list states no place")

	data, err := json.Marshal(SpaceFlowConfig{Stages: map[string]StageConfig{"lint": {Script: []string{"lint"}, Parallel: []string{"tests"}}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"lint": {"script": ["lint"], "parallel": ["tests"]}}`, string(data))
}

// TestFlowRefusesWhatIsNoStage: a key that cannot name a stage, a hook of one,
// the lock-file stage written as a flow key, an unknown key inside a stage
// object and a label that is not a string are each refused by name.
func TestFlowRefusesWhatIsNoStage(t *testing.T) {
	for name, row := range map[string]struct {
		raw  string
		want string
	}{
		"a capitalised stage":     {`{"Lint2": "lint"}`, `"Lint2" is not a stage or hook name`},
		"a hook of no stage name": {`{"beforeLint-x": "pre"}`, `"lint-x" is not a stage a hook can bracket`},
		"the lock-file stage":     {`{"syncLock": "lock"}`, "configured under autoVersion.syncLock"},
		"an unknown stage object key": {`{"lint": {"script": "lint", "weight": 2}}`,
			`unknown key "weight"; a stage entry names script, label, before, after and parallel`},
		"a label that is no string": {`{"build": {"label": 3}}`, "flow.build.label: wants a string"},
		"an object for a hook":      {`{"beforeBuild": {"script": "x"}}`, "wants a script name"},
		"two spellings of one key":  {`{"build": "a", "Build": "b"}`, "one name written twice"},
	} {
		t.Run(name, func(t *testing.T) {
			var flow SpaceFlowConfig
			err := json.Unmarshal([]byte(row.raw), &flow)
			require.Error(t, err)
			assert.Contains(t, err.Error(), row.want)
		})
	}
}

// TestFlowStageNames: the alphabet of a defined stage, the hook spellings it
// gets, and the stages no constraint may name.
func TestFlowStageNames(t *testing.T) {
	for _, name := range []string{"lint", "scanImages", "e"} {
		assert.True(t, IsFlowStageName(name), name)
	}
	for _, name := range []string{"", "Lint", "lint2", "lint-x", "run:lint", "9"} {
		assert.False(t, IsFlowStageName(name), name)
	}
	assert.Equal(t, "Lint", FlowStageTitle("lint"))
	assert.Equal(t, "ScanImages", FlowStageTitle("scanImages"))
	stage, isBefore, ok := FlowHookStage("beforeScanImages")
	assert.True(t, ok && isBefore)
	assert.Equal(t, "scanImages", stage)
	stage, isBefore, ok = FlowHookStage("postLint")
	assert.True(t, ok && !isBefore)
	assert.Equal(t, "lint", stage)
	_, _, ok = FlowHookStage("postgres")
	assert.False(t, ok, "a lower-case continuation is a stage name, not a hook")
	_, _, ok = FlowHookStage("before")
	assert.False(t, ok)
	for _, fixed := range []string{"sign", "version", "propagate", "syncLock"} {
		assert.True(t, IsFixedFlowStage(fixed), fixed)
	}
	assert.False(t, IsFixedFlowStage("build"))
	assert.True(t, IsBuiltInFlowStage("Announce"), "built-in stage keys fold")
	assert.False(t, IsBuiltInFlowStage("beforeBuild"), "a hook is no stage")
	assert.Len(t, FlowEntryKeys(), 25)
}
