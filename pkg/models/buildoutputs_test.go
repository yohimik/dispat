package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The two build keys are lists that replace whole, so the question worth
// testing here is the one that distinguishes them from a merged key: a level
// that states an empty list has said something, and a level that states
// nothing has not. Nothing in this package decides what the lists mean; the
// model only has to keep the two states apart on every level that carries
// them, so that the ladder below can read them.
//
// Stdlib only, like every test in this module: pkg/models is a published
// module and a test dependency would travel with it.

// levelsWithBuildKeys returns one empty value per configuration level that
// carries the build keys, keyed by the name the failure should name.
func levelsWithBuildKeys() map[string]any {
	return map[string]any{
		"File":          &File{},
		"SpaceConfig":   &SpaceConfig{},
		"SpaceFile":     &SpaceFile{},
		"PackageConfig": &PackageConfig{},
	}
}

// buildKeyLists reads the two lists out of a decoded level by field name, so
// one table can ask every level the same question without four type switches.
func buildKeyLists(level any) ([]string, []string) {
	value := reflect.ValueOf(level).Elem()
	outputs, _ := value.FieldByName("BuildOutputs").Interface().([]string)
	platforms, _ := value.FieldByName("BuildPlatforms").Interface().([]string)
	return outputs, platforms
}

// TestBuildKeysTellAnEmptyListFromAnAbsentOne: an explicit empty list is how
// a level opts out of what it would otherwise inherit, so it has to survive
// decoding as a list that is there and holds nothing. An absent key stays
// nil, which is the level saying nothing at all.
func TestBuildKeysTellAnEmptyListFromAnAbsentOne(t *testing.T) {
	for name, level := range levelsWithBuildKeys() {
		if err := json.Unmarshal([]byte(`{"buildOutputs": [], "buildPlatforms": []}`), level); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		outputs, platforms := buildKeyLists(level)
		if outputs == nil || len(outputs) != 0 {
			t.Errorf("%s: an explicit empty buildOutputs decodes to an empty list, got %#v", name, outputs)
		}
		if platforms == nil || len(platforms) != 0 {
			t.Errorf("%s: an explicit empty buildPlatforms decodes to an empty list, got %#v", name, platforms)
		}
	}
	for name, level := range levelsWithBuildKeys() {
		if err := json.Unmarshal([]byte(`{}`), level); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		outputs, platforms := buildKeyLists(level)
		if outputs != nil || platforms != nil {
			t.Errorf("%s: an absent key states nothing, got %#v and %#v", name, outputs, platforms)
		}
	}
}

// TestBuildKeysRoundTripThroughEveryLevel: a marshalled model is a loadable
// configuration, which for these keys means the root default, the space, the
// space's package entry and the package's own entry all write their list
// under the key the loader reads. The empty list is deliberately not in the
// fixture: omitempty drops it on the way out, exactly as it does for every
// other list that replaces whole.
func TestBuildKeysRoundTripThroughEveryLevel(t *testing.T) {
	space := SpaceConfig{
		Path:           PathList{"packages"},
		BuildOutputs:   []string{"dist", "types/generated"},
		BuildPlatforms: []string{"linux/amd64"},
		Packages: map[string]PackageConfig{
			"core": {BuildOutputs: []string{"lib"}, BuildPlatforms: []string{"darwin/arm64"}},
		},
	}
	original := File{
		BuildOutputs:   []string{"dist"},
		BuildPlatforms: []string{"linux/amd64", "linux/arm64"},
		Spaces:         map[string]SpaceConfig{"libs": space},
		Packages: map[string]PackageConfig{
			"tool": {Path: "tools/tool", BuildOutputs: []string{"bin/tool"}},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var loaded File
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Errorf("a marshalled configuration must load back unchanged:\nwrote %+v\nread  %+v", original, loaded)
	}

	// The space folder's own file mirrors the space entry, so the same lists
	// have to travel through its shape too.
	file := SpaceFile{
		BuildOutputs:   space.BuildOutputs,
		BuildPlatforms: space.BuildPlatforms,
		Packages:       space.Packages,
	}
	data, err = json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal space file: %v", err)
	}
	var loadedFile SpaceFile
	if err := json.Unmarshal(data, &loadedFile); err != nil {
		t.Fatalf("unmarshal space file: %v", err)
	}
	if !reflect.DeepEqual(file, loadedFile) {
		t.Errorf("a space folder's file must load back unchanged:\nwrote %+v\nread  %+v", file, loadedFile)
	}
}
