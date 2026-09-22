package models

import (
	"encoding/json"
	"reflect"
	"testing"
)

// `runOutputs` is a map from script name to a list of roots, read from the
// root file alone. What the model owes it is the reading itself and the
// lookup: a script named on a command line in one case finds the roots a file
// declared in another, and a configuration that declares nothing answers that
// it declares nothing.
//
// Stdlib only, like every test in this module: pkg/models is a published
// module and a test dependency would travel with it.

// TestRunOutputsRoundTrip: what a file writes is what the model marshals back,
// and an absent key stays absent rather than becoming an empty object.
func TestRunOutputsRoundTrip(t *testing.T) {
	var file File
	if err := json.Unmarshal([]byte(`{"runOutputs": {"tests": ["coverage", "reports/junit"]}}`), &file); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string][]string{"tests": {"coverage", "reports/junit"}}
	if !reflect.DeepEqual(file.RunOutputs, want) {
		t.Fatalf("runOutputs decodes as written, got %#v", file.RunOutputs)
	}
	document, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(document) != `{"runOutputs":{"tests":["coverage","reports/junit"]}}` {
		t.Errorf("runOutputs marshals back as written, got %s", document)
	}
	empty, err := json.Marshal(File{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(empty) != `{}` {
		t.Errorf("an absent runOutputs stays absent, got %s", empty)
	}
}

// TestFindRunOutputsMatchesScriptNamesInAnyCase: the script a sweep runs is
// named on the command line, the roots are named in the file, and the two
// spellings are not asked to agree.
func TestFindRunOutputsMatchesScriptNamesInAnyCase(t *testing.T) {
	file := &File{RunOutputs: map[string][]string{"Tests": {"coverage"}}}
	for _, name := range []string{"Tests", "tests", "TESTS"} {
		roots, ok := file.FindRunOutputs(name)
		if !ok || !reflect.DeepEqual(roots, []string{"coverage"}) {
			t.Errorf("%q finds the roots declared under Tests, got %#v, %v", name, roots, ok)
		}
	}
	if roots, ok := file.FindRunOutputs("lint"); ok || roots != nil {
		t.Errorf("a script that declares nothing finds nothing, got %#v, %v", roots, ok)
	}
	var unloaded *File
	if roots, ok := unloaded.FindRunOutputs("tests"); ok || roots != nil {
		t.Errorf("a configuration nobody loaded declares nothing, got %#v, %v", roots, ok)
	}
}
