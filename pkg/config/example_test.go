package config_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yohimik/dispat/pkg/config"
)

// Config is the struct a program loads its configuration into.
type Config struct {
	Name     string
	LogLevel string
	Env      map[string]string
	Areas    map[string]Area
}

// Area is one named entry of a map of them.
type Area struct {
	Path       []string
	Versioning string
}

// configFields is the whole config surface of Config: every key a file may
// write, and what writing it does. A key with no entry here is a key the model
// has no field for, which is where a typo lands.
//
// The table is keyed in lower case. A file spells a key however it likes and
// the decode folds it to find the setter, which is what lets `logLevel` and
// `loglevel` both load.
func configFields(dst *Config) config.Fields {
	return config.Fields{
		"name":     config.String(&dst.Name),
		"loglevel": config.String(&dst.LogLevel),
		"env":      config.StringMap(&dst.Env),
		"areas":    config.ObjectMap(&dst.Areas, areaFields),
	}
}

func areaFields(dst *Area) config.Fields {
	return config.Fields{
		"path":       config.Strings(&dst.Path),
		"versioning": config.String(&dst.Versioning),
	}
}

// write puts a file into a directory, for the examples' fixtures.
func write(dir, name, body string) string {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		panic(err)
	}
	return path
}

func Example() {
	dir, _ := os.MkdirTemp("", "config-example")
	defer os.RemoveAll(dir)
	path := write(dir, "app.yaml", "name: monorepo\nlogLevel: info\nareas:\n  libs:\n    path: pkgs\n")

	l := config.NewLoader(config.Options{})
	tree, err := l.ReadTree(context.Background(), path)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	var cfg Config
	if err := config.DecodeObject(tree.Settings(l, nil), "", configFields(&cfg)); err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("name:     ", cfg.Name)
	fmt.Println("logLevel: ", cfg.LogLevel)
	fmt.Println("libs path:", cfg.Areas["libs"].Path)

	// Output:
	// name:      monorepo
	// logLevel:  info
	// libs path: [pkgs]
}

// ExampleLoader_ReadTree shows a configuration composed from several files: a
// `$ref` naming another file whose content becomes the value, with the keys
// written beside it overriding what it brought in.
func ExampleLoader_ReadTree() {
	dir, _ := os.MkdirTemp("", "config-example")
	defer os.RemoveAll(dir)
	write(dir, "shared/area.json", `{"path": "pkgs", "versioning": "fixed"}`)
	path := write(dir, "app.json", `{
		"name": "monorepo",
		"areas": {
			"libs": {"$ref": "./shared/area.json"},
			"apps": {"$ref": "./shared/area.json", "versioning": "independent"}
		}
	}`)

	l := config.NewLoader(config.Options{})
	tree, _ := l.ReadTree(context.Background(), path)

	var cfg Config
	if err := config.DecodeObject(tree.Settings(l, nil), "", configFields(&cfg)); err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("libs:", cfg.Areas["libs"].Versioning)
	fmt.Println("apps:", cfg.Areas["apps"].Versioning)
	fmt.Println("read:", len(tree.Files), "files")

	// Output:
	// libs: fixed
	// apps: independent
	// read: 3 files
}

// ExampleTree_Settings shows the two rules the decode input carries: an object
// with no keys is not a key at all, and a key spelled with the delimiter is
// the levels it names.
func ExampleTree_Settings() {
	tree := &config.Tree{Root: map[string]any{
		"areas":           map[string]any{},
		"areas.libs.path": "pkgs",
		"env":             map[string]any{"PATH": "/usr/bin"},
		"hollow":          map[string]any{"inner": map[string]any{}},
		"logLevel":        "info",
	}}

	settings := tree.Settings(nil, config.Overrides{"logLevel": "warn"})

	_, hollow := settings["hollow"]
	fmt.Println("hollow survived:", hollow)
	fmt.Println("areas:          ", settings["areas"])
	fmt.Println("logLevel:       ", settings["logLevel"])

	// Output:
	// hollow survived: false
	// areas:           map[libs:map[path:pkgs]]
	// logLevel:        warn
}

// ExampleDecodeObject shows the unknown-key refusal, which is structural: a
// key with no setter is a key the model has no field for, and the error names
// it by its full path from the root.
func ExampleDecodeObject() {
	var cfg Config
	err := config.DecodeObject(map[string]any{
		"areas": map[string]any{"libs": map[string]any{"paht": "pkgs"}},
	}, "", configFields(&cfg))

	fmt.Println(err)

	// Output:
	// unknown key "areas.libs.paht"
}

// ExampleLoader_Resolve shows the ascent: a command run from inside a
// sub-folder loads the configuration above it, and the folder that
// configuration sits in is the root.
func ExampleLoader_Resolve() {
	dir, _ := os.MkdirTemp("", "config-example")
	defer os.RemoveAll(dir)
	write(dir, "app.json", `{"areas": {"libs": {"path": "pkgs"}}}`)
	deep := filepath.Join(dir, "pkgs", "core")
	_ = os.MkdirAll(deep, 0o755)

	l := config.NewLoader(config.Options{})
	path, root, err := l.Resolve(context.Background(), deep, config.Resolver{
		Names:    []string{"app.json", "app.yaml"},
		Classify: config.MarkerClassify([]string{"areas"}, nil),
		Owns:     config.FolderOwner("areas", "path"),
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("file:", filepath.Base(path))
	fmt.Println("root:", root == dir)

	// Output:
	// file: app.json
	// root: true
}

// ExampleEnvBinding shows the environment layered under what the operator
// typed: the file underneath, the environment over it, the flags over both.
func ExampleEnvBinding() {
	binding := config.EnvBinding{
		Prefix:  "APP_",
		Keys:    []string{"logLevel", "areas.libs.path"},
		Environ: []string{"APP_LOGLEVEL=debug", "APP_AREAS_LIBS_PATH=elsewhere", "PATH=/usr/bin"},
	}

	ov, err := binding.Overrides(context.Background())
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	tree := &config.Tree{Root: map[string]any{
		"logLevel": "info",
		"areas":    map[string]any{"libs": map[string]any{"path": "pkgs"}},
	}}
	settings := tree.Settings(nil, config.MergeOverrides(ov, config.Overrides{"logLevel": "warn"}))

	var cfg Config
	if err := config.DecodeObject(settings, "", configFields(&cfg)); err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("logLevel:", cfg.LogLevel)
	fmt.Println("path:    ", cfg.Areas["libs"].Path)

	// Output:
	// logLevel: warn
	// path:     [elsewhere]
}
