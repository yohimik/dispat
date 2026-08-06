package models

import (
	"encoding/json"
	"testing"
)

// The models are data; what is worth testing here is the pure helpers and
// the one contract external tooling relies on: a marshalled model is a
// loadable config (keys match the mapstructure names — the CLI's own config
// tests load marshalled models end to end).

func boolPtr(b bool) *bool { return &b }

func TestEnabledDefaults(t *testing.T) {
	if !(ChangelogConfig{}).IsEnabled() {
		t.Error("changelog defaults to enabled")
	}
	if (ChangelogConfig{Enabled: boolPtr(false)}).IsEnabled() {
		t.Error("changelog can be disabled")
	}
	if !(GitHubConfig{}).IsEnabled() {
		t.Error("github defaults to enabled")
	}
	if (GitHubConfig{Enabled: boolPtr(false)}).IsEnabled() {
		t.Error("github can be disabled")
	}
	if (CommitConfig{}).IsEnabled() {
		t.Error("the release commit defaults to disabled")
	}
	if !(CommitConfig{Enabled: boolPtr(true)}).IsEnabled() {
		t.Error("the release commit can be enabled")
	}
	if (CommitConfig{Enabled: boolPtr(true)}).PushEnabled() {
		t.Error("push needs its own flag")
	}
	if !(CommitConfig{Enabled: boolPtr(true), Push: true}).PushEnabled() {
		t.Error("push follows commit")
	}
	if (CommitConfig{Push: true}).PushEnabled() {
		t.Error("push without commit is inert")
	}
}

func TestScriptLookupsAreCaseInsensitive(t *testing.T) {
	// Viper lowercases map keys when a file is loaded, so lookups match
	// case-insensitively against lowercased keys.
	f := File{Scripts: map[string]string{"build": "make"}}
	if s, ok := f.Script("BUILD"); !ok || s != "make" {
		t.Errorf("Script(BUILD) = %q, %v", s, ok)
	}
	if _, ok := f.Script("missing"); ok {
		t.Error("unknown scripts do not resolve")
	}

	sc := SpaceConfig{RunScripts: map[string]string{"lint": "npm run lint"}}
	if s, ok := sc.RunScript("LINT"); !ok || s != "npm run lint" {
		t.Errorf("RunScript(LINT) = %q, %v", s, ok)
	}
	if _, ok := sc.RunScript("format"); ok {
		t.Error("unknown run scripts do not resolve")
	}
}

func TestCommandsPreservesOrder(t *testing.T) {
	f := File{Scripts: map[string]string{"a": "cmd-a", "b": "cmd-b"}}
	got := f.Commands([]string{"b", "a"})
	if len(got) != 2 || got[0] != "cmd-b" || got[1] != "cmd-a" {
		t.Errorf("Commands = %v", got)
	}
	if f.Commands(nil) != nil {
		t.Error("no refs, no commands")
	}
}

func TestDefaultNonPackageScopes(t *testing.T) {
	got := DefaultNonPackageScopes()
	if len(got) != 1 || got[0] != "release" {
		t.Errorf("DefaultNonPackageScopes = %v", got)
	}
}

func TestMarshalledModelUsesTheConfigKeys(t *testing.T) {
	// The json tags mirror the mapstructure keys, so a marshalled model is a
	// loadable dispat.json; resolved fields never leak into the file.
	f := File{
		Scripts: map[string]string{"build": "make"},
		Spaces: map[string]SpaceConfig{
			"libs": {Path: "packages", Versioning: VersioningFixed,
				Run:        SpaceRunConfig{Build: []string{"build"}},
				RunScripts: map[string]string{"lint": "make lint"}},
		},
		Dependencies:     []DependencyConfig{{Consumer: "app", Provider: "core"}},
		BuildConcurrency: 99, // resolved: must not marshal
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	space := raw["spaces"].(map[string]any)["libs"].(map[string]any)
	for _, key := range []string{"path", "versioning", "run", "runScripts"} {
		if _, ok := space[key]; !ok {
			t.Errorf("space is missing key %q: %v", key, space)
		}
	}
	if _, ok := raw["BuildConcurrency"]; ok {
		t.Error("resolved fields must not marshal")
	}
	if string(data) != "" && json.Valid(data) != true {
		t.Error("marshalled config must be valid JSON")
	}
}
