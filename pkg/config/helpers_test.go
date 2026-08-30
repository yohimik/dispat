package config

// The model the tests decode into, and the two file writers they build
// repositories with.
//
// The model is the package's own: a small configuration language with the
// shapes that matter — an optional sub-object, a map of named entries, a list
// of objects, an env layer, a free-form block — so that the tests state the
// library's semantics rather than some consumer's. Fixtures are marshalled
// from typed values wherever the point is a configuration; raw text appears
// only where the point is a file no marshaller would produce.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// appConfig is a whole configuration file.
type appConfig struct {
	Name        string
	Shell       []string
	Concurrency []int
	LogLevel    string
	Strict      bool
	Quiet       *bool
	Verbose     *bool
	Env         map[string]string
	Custom      map[string]any
	Flow        *flowConfig
	Areas       map[string]areaConfig
	Hooks       []hookConfig
	Tags        []string
}

// flowConfig is the optional sub-object: nil means the file said nothing.
type flowConfig struct {
	Build   []string
	Publish []string
}

// areaConfig is one named entry of a map of them.
type areaConfig struct {
	Path       []string
	Versioning string
	Flow       *flowConfig
	Env        map[string]string
	Areas      map[string]areaConfig
}

// hookConfig is one element of a list of objects.
type hookConfig struct {
	URL     string
	Events  []string
	Retries int
	Enabled *bool
}

func appFields(dst *appConfig) Fields {
	return Fields{
		"name":        String(&dst.Name),
		"shell":       Strings(&dst.Shell),
		"concurrency": Ints(&dst.Concurrency),
		"loglevel":    String(&dst.LogLevel),
		"strict":      Bool(&dst.Strict),
		"quiet":       BoolPtr(&dst.Quiet),
		"verbose":     BoolPtr(&dst.Verbose),
		"env":         StringMap(&dst.Env),
		"custom":      RawMap(&dst.Custom),
		"flow":        Object(&dst.Flow, flowFields),
		"areas":       ObjectMap(&dst.Areas, areaFields),
		"hooks":       ObjectList(&dst.Hooks, hookFields),
		"tags":        Strings(&dst.Tags),
	}
}

func flowFields(dst *flowConfig) Fields {
	return Fields{
		"build":   Strings(&dst.Build),
		"publish": Strings(&dst.Publish),
	}
}

func areaFields(dst *areaConfig) Fields {
	return Fields{
		"path":       pathStrings(&dst.Path),
		"versioning": String(&dst.Versioning),
		"flow":       Object(&dst.Flow, flowFields),
		"env":        StringMap(&dst.Env),
		"areas":      ObjectMap(&dst.Areas, areaFields),
	}
}

func hookFields(dst *hookConfig) Fields {
	return Fields{
		"url":     String(&dst.URL),
		"events":  Strings(&dst.Events),
		"retries": Int(&dst.Retries),
		"enabled": BoolPtr(&dst.Enabled),
	}
}

// pathStrings is the setter a caller writes for itself: a list of plain values
// whose scalar form never splits on commas, because a folder name may hold
// one. It is here to prove the library's own Strings is a choice rather than
// the only way to fill a []string.
func pathStrings(dst *[]string) Setter {
	return func(val any, at string) error {
		if s, ok := val.(string); ok {
			*dst = []string{s}
			return nil
		}
		items, ok := WeakList(val)
		if !ok {
			s, err := WeakString(val, at)
			if err != nil {
				return err
			}
			*dst = []string{s}
			return nil
		}
		out := make([]string, 0, len(items))
		for i, item := range items {
			s, err := WeakString(item, IndexPath(at, i))
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		*dst = out
		return nil
	}
}

// loadApp is the whole pipeline the tests exercise: read the tree, render the
// settings with whatever overrides, decode into the model.
func loadApp(t *testing.T, path string, ov Overrides) (*appConfig, *Tree, error) {
	t.Helper()
	l := NewLoader(Options{})
	tree, err := l.ReadTree(t.Context(), path)
	if err != nil {
		return nil, nil, err
	}
	var cfg appConfig
	if err := DecodeObject(tree.Settings(l, ov), "", appFields(&cfg)); err != nil {
		return nil, tree, err
	}
	return &cfg, tree, nil
}

// writeFile writes one file into dir, creating the folders above it, and
// returns its path. name may name a sub-folder.
func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeJSON marshals a typed fixture into dir, which is how a configuration
// the tests mean as a configuration is written.
func writeJSON(t *testing.T, dir, name string, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	return writeFile(t, dir, name, string(data)+"\n")
}

// readTree is the one-line form the tests that only care about the tree use.
func readTree(t *testing.T, path string) (*Tree, error) {
	t.Helper()
	return NewLoader(Options{}).ReadTree(t.Context(), path)
}

// settingsOf renders a raw map as the decode reads it, for the tests about the
// two rules that rendering carries.
func settingsOf(root map[string]any, ov Overrides) map[string]any {
	return (&Tree{Root: root}).Settings(nil, ov)
}

// boolPtr is the tri-state a file wrote.
func boolPtr(b bool) *bool { return &b }
