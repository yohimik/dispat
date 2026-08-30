package config

// The env binding: off unless asked for, closed to the keys it declares, and
// layered under whatever the caller sets explicitly.

import (
	"reflect"
	"strings"
	"testing"
)

func appBinding(environ ...string) EnvBinding {
	return EnvBinding{
		Prefix:  "APP_",
		Keys:    []string{"logLevel", "areas.libs.path", "name"},
		Environ: environ,
	}
}

// TestEnvBindingIsOffUntilItIsAskedFor: the zero value binds nothing, which is
// the safe direction for a feature that reads the whole environment.
func TestEnvBindingIsOffUntilItIsAskedFor(t *testing.T) {
	t.Setenv("APP_NAME", "from-env")
	ov, err := EnvBinding{}.Overrides(t.Context())
	if err != nil || ov != nil {
		t.Fatalf("ov = %#v, err = %v", ov, err)
	}

	// And a loaded configuration is untouched by an environment nobody bound.
	path := writeFile(t, t.TempDir(), "app.json", `{"name": "from-file"}`)
	cfg, _, err := loadApp(t, path, nil)
	if err != nil || cfg.Name != "from-file" {
		t.Fatalf("name = %q, err = %v", cfg.Name, err)
	}
}

// TestEnvBindingDerivesTheVariableName: the derivation runs one way, from a
// declared key to a name, so the name is a fact rather than a guess.
func TestEnvBindingDerivesTheVariableName(t *testing.T) {
	for _, tc := range []struct{ prefix, key, delim, want string }{
		{"APP_", "logLevel", ".", "APP_LOGLEVEL"},
		{"APP_", "areas.libs.path", ".", "APP_AREAS_LIBS_PATH"},
		{"APP_", "log-level", ".", "APP_LOG_LEVEL"},
		{"", "name", ".", "NAME"},
		{"APP_", "a/b", "/", "APP_A_B"},
		{"APP_", "a.b", "", "APP_A.B"},
	} {
		if got := EnvVarName(tc.prefix, tc.key, tc.delim); got != tc.want {
			t.Errorf("EnvVarName(%q, %q, %q) = %q, want %q",
				tc.prefix, tc.key, tc.delim, got, tc.want)
		}
	}
}

// TestEnvBindingSetsOnlyDeclaredKeys: a binding is closed, so a variable in
// the namespace that answers to no declared key sets nothing.
func TestEnvBindingSetsOnlyDeclaredKeys(t *testing.T) {
	ov, err := appBinding(
		"APP_LOGLEVEL=warn",
		"APP_AREAS_LIBS_PATH=elsewhere",
		"APP_SOMETHING=ignored",
		"PATH=/usr/bin",
	).Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	want := Overrides{"logLevel": "warn", "areas.libs.path": "elsewhere"}
	if !reflect.DeepEqual(want, ov) {
		t.Errorf("ov = %#v, want %#v", ov, want)
	}
}

// TestEnvBindingKeepsTheKeysSpelling: the key lands in the overrides exactly
// as the caller declared it, which is what lets it replace the file's own
// spelling rather than sit beside it.
func TestEnvBindingKeepsTheKeysSpelling(t *testing.T) {
	ov, err := appBinding("APP_LOGLEVEL=warn").Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if _, ok := ov["logLevel"]; !ok {
		t.Fatalf("ov = %#v", ov)
	}

	path := writeFile(t, t.TempDir(), "app.json", `{"LOGLEVEL": "debug"}`)
	cfg, _, err := loadApp(t, path, ov)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("logLevel = %q", cfg.LogLevel)
	}
}

// TestEnvBindingPrecedence: the file underneath, the environment over it, and
// what the operator typed over both.
func TestEnvBindingPrecedence(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json", `{"name": "from-file", "logLevel": "from-file"}`)
	env, err := appBinding("APP_NAME=from-env", "APP_LOGLEVEL=from-env").Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	flags := Overrides{"logLevel": "from-flags"}

	cfg, _, err := loadApp(t, path, MergeOverrides(env, flags))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Name != "from-env" {
		t.Errorf("name = %q, want the environment over the file", cfg.Name)
	}
	if cfg.LogLevel != "from-flags" {
		t.Errorf("logLevel = %q, want the flag over the environment", cfg.LogLevel)
	}
}

// TestEnvBindingEmptyIsAValue: unsetting a variable and setting it to nothing
// are two different things a deployment does on purpose.
func TestEnvBindingEmptyIsAValue(t *testing.T) {
	ov, err := appBinding("APP_NAME=").Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if got, ok := ov["name"]; !ok || got != "" {
		t.Errorf("ov = %#v, want the empty string as a value", ov)
	}

	ov, err = appBinding().Overrides(t.Context())
	if err != nil || ov != nil {
		t.Errorf("ov = %#v, err = %v; want an unset variable to leave its key alone", ov, err)
	}
}

// TestEnvBindingStrictRefusesATypo: a typo in a deployment manifest is a
// failure at startup rather than a setting that silently never applied.
func TestEnvBindingStrictRefusesATypo(t *testing.T) {
	b := appBinding("APP_LOGLEVL=warn", "APP_ZZZ=x", "PATH=/usr/bin")
	b.Strict = true

	_, err := b.Overrides(t.Context())
	if err == nil {
		t.Fatal("want an error")
	}
	want := "env binding: APP_LOGLEVL sets no configuration key; the APP_ variables that do are " +
		"APP_AREAS_LIBS_PATH, APP_LOGLEVEL, APP_NAME"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}

	// Without Strict the same environment is a warning and the load goes on.
	rec := newRecorder(LevelTrace)
	ov, err := appBinding("APP_LOGLEVL=warn", "APP_NAME=n").Overrides(WithLogger(t.Context(), rec))
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if ov["name"] != "n" {
		t.Errorf("ov = %#v", ov)
	}
	if unmatched, ok := rec.first(EventEnvUnmatched); !ok || unmatched.fields["var"].Text() != "APP_LOGLEVL" {
		t.Errorf("events = %v", rec.events())
	}
	if bound, ok := rec.first(EventEnvBind); !ok || bound.fields["key"].Text() != "name" {
		t.Errorf("events = %v", rec.events())
	}
}

// TestEnvBindingStrictReportsTheSameTypoEveryTime: an environment is a map by
// another name, and a startup that fails differently from one run to the next
// is a startup nobody can fix.
func TestEnvBindingStrictReportsTheSameTypoEveryTime(t *testing.T) {
	b := appBinding("APP_ZZZ=1", "APP_AAA=2", "APP_MMM=3")
	b.Strict = true
	for i := 0; i < 20; i++ {
		err := mustErr(t, b)
		if !strings.Contains(err.Error(), "APP_AAA") {
			t.Fatalf("run %d: %v", i, err)
		}
	}
}

func mustErr(t *testing.T, b EnvBinding) error {
	t.Helper()
	_, err := b.Overrides(t.Context())
	if err == nil {
		t.Fatal("want an error")
	}
	return err
}

// TestEnvBindingRefusesTwoKeysForOneVariable: two keys deriving one name is a
// binding nobody can read, so it is refused rather than resolved by whichever
// came first.
func TestEnvBindingRefusesTwoKeysForOneVariable(t *testing.T) {
	b := EnvBinding{Prefix: "APP_", Keys: []string{"log.level", "log-level"}, Environ: []string{}}
	err := mustErr(t, b)
	want := `env binding: keys "log.level" and "log-level" both bind APP_LOG_LEVEL`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
}

// TestEnvBindingTakesTheCallersDerivation: a program whose variables were
// named before its config keys were says so through Bind.
func TestEnvBindingTakesTheCallersDerivation(t *testing.T) {
	b := EnvBinding{
		Prefix:  "APP_",
		Keys:    []string{"logLevel"},
		Bind:    func(prefix, key string) string { return prefix + "LOG_LEVEL" },
		Environ: []string{"APP_LOG_LEVEL=warn"},
	}
	ov, err := b.Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if ov["logLevel"] != "warn" {
		t.Errorf("ov = %#v", ov)
	}
}

// TestEnvBindingReadsTheProcessEnvironmentByDefault: a nil Environ is the
// process's own, which is the whole point of the feature.
func TestEnvBindingReadsTheProcessEnvironmentByDefault(t *testing.T) {
	t.Setenv("APP_NAME", "from-process")
	b := EnvBinding{Prefix: "APP_", Keys: []string{"name"}}

	ov, err := b.Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if ov["name"] != "from-process" {
		t.Errorf("ov = %#v", ov)
	}
}

// TestEnvBindingSkipsWhatIsNotAPair: an environment entry with no equals sign
// is not a variable, whatever put it there.
func TestEnvBindingSkipsWhatIsNotAPair(t *testing.T) {
	ov, err := appBinding("APP_NAME=n", "NOTAPAIR").Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if len(ov) != 1 {
		t.Errorf("ov = %#v", ov)
	}
}

// TestEnvBindingWalksToANestedKey: the override lands where its key path says,
// which is what makes a nested key bindable at all.
func TestEnvBindingWalksToANestedKey(t *testing.T) {
	path := writeFile(t, t.TempDir(), "app.json",
		`{"areas": {"libs": {"Path": "pkgs", "versioning": "fixed"}}}`)
	ov, err := appBinding("APP_AREAS_LIBS_PATH=elsewhere").Overrides(t.Context())
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}

	cfg, _, err := loadApp(t, path, ov)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := []string{"elsewhere"}; !reflect.DeepEqual(want, cfg.Areas["libs"].Path) {
		t.Errorf("path = %#v", cfg.Areas["libs"].Path)
	}
	if cfg.Areas["libs"].Versioning != "fixed" {
		t.Errorf("the rest of the entry survived: %#v", cfg.Areas["libs"])
	}
}
