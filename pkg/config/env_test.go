package config

// The env helpers: the deterministic pair list, the merge every layer shares,
// and the keys that could never reach a process intact.

import (
	"errors"
	"reflect"
	"testing"
)

// TestEnvPairs: sorted KEY=value, so two runs of the same configuration build
// the same environment in the same order. An empty layer is no pairs at all
// rather than an empty list, because that is what a caller appends.
func TestEnvPairs(t *testing.T) {
	got := EnvPairs(map[string]string{"B": "2", "A": "1", "a": "3"})
	if want := []string{"A=1", "B=2", "a=3"}; !reflect.DeepEqual(want, got) {
		t.Errorf("EnvPairs = %#v, want %#v", got, want)
	}
	if got := EnvPairs(nil); got != nil {
		t.Errorf("EnvPairs(nil) = %#v", got)
	}
	if got := EnvPairs(map[string]string{}); got != nil {
		t.Errorf("EnvPairs of an empty layer = %#v", got)
	}
}

// TestMergeEnv: the overlay wins key by key, a nil overlay leaves the base
// alone — which is what "the layer says nothing" has to mean — and neither
// input is written into.
func TestMergeEnv(t *testing.T) {
	base := map[string]string{"A": "1", "B": "2"}
	if got := MergeEnv(base, nil); !reflect.DeepEqual(base, got) {
		t.Errorf("nil overlay = %#v", got)
	}
	got := MergeEnv(base, map[string]string{"B": "over", "C": "3"})
	want := map[string]string{"A": "1", "B": "over", "C": "3"}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("merged = %#v, want %#v", got, want)
	}
	if base["B"] != "2" {
		t.Error("the base was written into")
	}
	if got := MergeEnv(nil, map[string]string{"A": "1"}); len(got) != 1 {
		t.Errorf("nil base = %#v", got)
	}
}

// TestValidateEnvRefusals: the keys that could never reach a process intact,
// each named in the layer's own label.
func TestValidateEnvRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"empty", map[string]string{"": "v"}, "env contains an empty key"},
		{"an equals sign", map[string]string{"A=B": "v"}, `env: key "A=B" must not contain '='`},
		{"a reserved prefix", map[string]string{"APP_X": "v"}, `env: key "APP_X" uses the reserved APP_ prefix`},
		{"a reserved prefix in lower case", map[string]string{"app_x": "v"},
			`env: key "app_x" uses the reserved APP_ prefix`},
		{"two spellings", map[string]string{"path": "a", "PATH": "b"},
			`env: keys "PATH" and "path" collide case-insensitively`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEnv("env", tc.env, "APP_")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := ValidateEnv("env", map[string]string{"APP_X": "v"}); err != nil {
		t.Errorf("no reserved prefixes means none are reserved: %v", err)
	}
	if err := ValidateEnv("env", map[string]string{"A": "1", "b": "2"}, "APP_"); err != nil {
		t.Errorf("a sound layer: %v", err)
	}
}

// TestValidateEnvCollisionIsTheSharedSentinel: two spellings of one name is
// the decode's own refusal, applied once more here, so a caller matching on it
// matches one thing.
func TestValidateEnvCollisionIsTheSharedSentinel(t *testing.T) {
	err := ValidateEnv("spaces[\"libs\"].env", map[string]string{"a": "1", "A": "2"})
	if !errors.Is(err, ErrFoldCollision) {
		t.Fatalf("err = %v, want ErrFoldCollision", err)
	}
	var collision *FoldCollisionError
	if !errors.As(err, &collision) || collision.At != `spaces["libs"].env` {
		t.Fatalf("err = %#v, want the layer's label carried", collision)
	}
}

// TestValidateEnvReportsTheFirstMistakeDeterministically: keys are checked in
// sorted order, so a layer with several mistakes always reports the same one.
func TestValidateEnvReportsTheFirstMistakeDeterministically(t *testing.T) {
	env := map[string]string{"ZZ=1": "v", "AA=2": "v", "MM=3": "v"}
	for i := 0; i < 50; i++ {
		err := ValidateEnv("env", env)
		if err == nil || err.Error() != `env: key "AA=2" must not contain '='` {
			t.Fatalf("run %d: err = %v", i, err)
		}
	}
}

// TestEnvKeyCaseSurvivesEveryFormat: PATH and Path are two variables, and a
// process handed one when it asked for the other would fail in a way nothing
// here could explain.
func TestEnvKeyCaseSurvivesEveryFormat(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct{ name, body string }{
		{"app.json", `{"env": {"MiXed": "v", "lower": "w"}}`},
		{"app.yaml", "env:\n  MiXed: v\n  lower: w\n"},
		{"app.toml", "[env]\nMiXed = \"v\"\nlower = \"w\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _, err := loadApp(t, writeFile(t, dir, tc.name, tc.body), nil)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			want := map[string]string{"MiXed": "v", "lower": "w"}
			if !reflect.DeepEqual(want, cfg.Env) {
				t.Errorf("env = %#v, want %#v", cfg.Env, want)
			}
		})
	}
}

// TestEnvValuesAreWeaklyTyped: a value written as a number or a flag is the
// text a process receives, rendered the way every other scalar is.
func TestEnvValuesAreWeaklyTyped(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.yaml",
		"env:\n  N: 7\n  B: true\n  F: 1.5\n  S: text\n  E:\n")

	cfg, _, err := loadApp(t, path, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]string{"N": "7", "B": "true", "F": "1.5", "S": "text", "E": ""}
	if !reflect.DeepEqual(want, cfg.Env) {
		t.Errorf("env = %#v, want %#v", cfg.Env, want)
	}
}
