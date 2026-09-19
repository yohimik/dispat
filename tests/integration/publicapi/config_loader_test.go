package publicapi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yohimik/dispat/pkg/config"
)

// TestPublicAPIConfigLoaderOptions drives the loader's own configuration: the
// defaults every zero value takes, the fields a caller replaces, and the format
// table that decides what a file is.
func TestPublicAPIConfigLoaderOptions(t *testing.T) {
	t.Run("the zero options are the documented defaults", func(t *testing.T) {
		got := config.NewLoader(config.Options{}).Options()
		if got.RefKey != config.DefaultRefKey {
			t.Errorf("RefKey = %q", got.RefKey)
		}
		if got.MaxRefDepth != config.DefaultMaxRefDepth {
			t.Errorf("MaxRefDepth = %d", got.MaxRefDepth)
		}
		if got.KeyDelim != config.DefaultKeyDelim {
			t.Errorf("KeyDelim = %q", got.KeyDelim)
		}
		if got.ReadFile == nil || got.Formats == nil {
			t.Error("the reader or the format table was left unset")
		}
		def := config.Default()
		if def.RefKey != got.RefKey || def.MaxRefDepth != got.MaxRefDepth || def.KeyDelim != got.KeyDelim {
			t.Errorf("Default() disagrees with the filled-in zero value: %#v", def)
		}
		if len(def.Formats) != len(config.DefaultFormats()) {
			t.Errorf("Default().Formats = %d entries", len(def.Formats))
		}
	})

	t.Run("a caller's own fields are kept", func(t *testing.T) {
		log := &recordingLogger{floor: config.LevelTrace}
		opts := config.Options{
			RefKey:      "$include",
			MaxRefDepth: 4,
			KeyDelim:    "/",
			ReadFile:    func(string) ([]byte, error) { return []byte("{}"), nil },
			Formats:     map[string]config.Unmarshal{".json": nil},
			Logger:      log,
		}
		got := config.NewLoader(opts).Options()
		if got.RefKey != "$include" || got.MaxRefDepth != 4 || got.KeyDelim != "/" {
			t.Errorf("options = %#v", got)
		}
		if got.Logger != log || len(got.Formats) != 1 {
			t.Errorf("options = %#v", got)
		}
	})

	t.Run("the standard formats parse their own documents", func(t *testing.T) {
		dir := t.TempDir()
		files := map[string]string{
			"app.json": `{"name":"json"}`,
			"app.yaml": "name: yaml\n",
			"app.yml":  "name: yml\n",
			"app.toml": "name = \"toml\"\n",
		}
		for name, body := range files {
			path := writeConfigFile(t, dir, name, body)
			tree, err := config.NewLoader(config.Options{}).ReadTree(context.Background(), path)
			if err != nil {
				t.Fatalf("ReadTree(%s): %v", name, err)
			}
			if tree.Root["name"] == nil {
				t.Errorf("%s parsed to %#v", name, tree.Root)
			}
			if !slices.Equal(tree.Files, []string{path}) {
				t.Errorf("%s reported files %v", name, tree.Files)
			}
		}
	})

	t.Run("a malformed document is a read failure naming the file", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{
			"bad.json": `{"name":`,
			"bad.yaml": "name: [unterminated\n",
			"bad.toml": "name = \n",
		} {
			path := writeConfigFile(t, dir, name, body)
			_, err := config.NewLoader(config.Options{}).ReadTree(context.Background(), path)
			if err == nil {
				t.Fatalf("ReadTree(%s) accepted a malformed document", name)
			}
			var fileErr *config.FileError
			if !errors.As(err, &fileErr) || fileErr.Path != path {
				t.Errorf("%s error = %v, want a FileError naming it", name, err)
			}
		}
	})

	t.Run("an empty document is an empty tree in every format", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{
			"empty.json": `null`,
			"empty.yaml": "",
			"empty.toml": "",
		} {
			path := writeConfigFile(t, dir, name, body)
			tree, err := config.NewLoader(config.Options{}).ReadTree(context.Background(), path)
			if err != nil {
				t.Fatalf("ReadTree(%s): %v", name, err)
			}
			if len(tree.Root) != 0 {
				t.Errorf("%s = %#v, want an empty root", name, tree.Root)
			}
		}
	})

	t.Run("a document whose top level is not an object is refused", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "list.json", `[1,2]`)
		_, err := config.NewLoader(config.Options{}).ReadTree(context.Background(), path)
		if err == nil || !strings.Contains(err.Error(), "the top level is not an object") {
			t.Fatalf("ReadTree = %v", err)
		}
	})

	t.Run("a file no entry of the table claims is refused", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.ini", "name = x\n")
		_, err := config.NewLoader(config.Options{}).ReadTree(context.Background(), path)
		if !errors.Is(err, config.ErrUnsupportedFormat) {
			t.Fatalf("ReadTree = %v, want ErrUnsupportedFormat", err)
		}
	})

	t.Run("a format under the empty extension claims what the others leave", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.ini", "anything")
		formats := config.DefaultFormats()
		formats[""] = func(data []byte) (any, error) {
			return map[string]any{"body": string(data)}, nil
		}
		tree, err := config.NewLoader(config.Options{Formats: formats}).ReadTree(context.Background(), path)
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if tree.Root["body"] != "anything" {
			t.Errorf("root = %#v", tree.Root)
		}
	})

	t.Run("a caller's own reader is what every path goes through", func(t *testing.T) {
		var asked []string
		l := config.NewLoader(config.Options{
			ReadFile: func(path string) ([]byte, error) {
				asked = append(asked, path)
				return []byte(`{"name":"from the reader"}`), nil
			},
		})
		tree, err := l.ReadTree(context.Background(), "nowhere/app.json")
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if tree.Root["name"] != "from the reader" {
			t.Errorf("root = %#v", tree.Root)
		}
		if !slices.Equal(asked, []string{"nowhere/app.json"}) {
			t.Errorf("the reader was asked for %v", asked)
		}
	})

	t.Run("a reader's failure names the file", func(t *testing.T) {
		broken := errors.New("no such thing")
		l := config.NewLoader(config.Options{
			ReadFile: func(string) ([]byte, error) { return nil, broken },
		})
		_, err := l.ReadTree(context.Background(), "app.json")
		if !errors.Is(err, broken) {
			t.Fatalf("ReadTree = %v", err)
		}
	})

	t.Run("a nil loader reads the defaults", func(t *testing.T) {
		var absent *config.Loader
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"name":"x"}`)
		tree, err := absent.ReadTree(context.Background(), path)
		if err != nil || tree.Root["name"] != "x" {
			t.Fatalf("ReadTree through a nil loader = %#v, %v", tree, err)
		}
		if got := tree.Settings(absent, nil); got["name"] != "x" {
			t.Errorf("Settings through a nil loader = %#v", got)
		}
	})

	t.Run("a generic mapping becomes the one kind of map everything reads", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.generic", "ignored")
		formats := config.DefaultFormats()
		formats[".generic"] = func([]byte) (any, error) {
			return map[any]any{
				1:       "int one",
				"1":     "string one",
				true:    "flag",
				"plain": map[any]any{2: "two"},
			}, nil
		}
		tree, err := config.NewLoader(config.Options{Formats: formats}).ReadTree(context.Background(), path)
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if _, ok := tree.Root["1"]; !ok {
			t.Errorf("root = %#v, want the rendered key", tree.Root)
		}
		if tree.Root["true"] != "flag" {
			t.Errorf("root = %#v", tree.Root)
		}
		nested, ok := tree.Root["plain"].(map[string]any)
		if !ok || nested["2"] != "two" {
			t.Errorf("nested = %#v", tree.Root["plain"])
		}
	})
}

// TestPublicAPIConfigReferenceComposition drives the `$ref` key: the file it
// names, the keys written beside it, the several files it may merge, and every
// way it can be written wrong.
func TestPublicAPIConfigReferenceComposition(t *testing.T) {
	newTree := func(t *testing.T, dir, entry string, opts config.Options) (*config.Tree, error) {
		t.Helper()
		return config.NewLoader(opts).ReadTree(context.Background(), filepath.Join(dir, entry))
	}

	t.Run("a reference becomes the file it names", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"spaces":{"$ref":"fragments/spaces.json"},"name":"app"}`)
		writeConfigFile(t, dir, "fragments/spaces.json", `{"apps":{"path":"apps"}}`)
		log := &recordingLogger{floor: config.LevelTrace}
		tree, err := newTree(t, dir, "app.json", config.Options{Logger: log})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		spaces := tree.Root["spaces"].(map[string]any)["apps"].(map[string]any)
		if spaces["path"] != "apps" {
			t.Errorf("spaces = %#v", tree.Root["spaces"])
		}
		if len(tree.Files) != 2 {
			t.Errorf("Files = %v, want both files", tree.Files)
		}
		for _, event := range []string{config.EventFileRead, config.EventRefFollow, config.EventTreeLoaded} {
			if !log.saw(event) {
				t.Errorf("no %s event was written", event)
			}
		}
	})

	t.Run("an absolute reference is taken as written", func(t *testing.T) {
		dir := t.TempDir()
		target := writeConfigFile(t, dir, "fragments/spaces.json", `{"apps":{"path":"apps"}}`)
		body := `{"spaces":{"$ref":` + quoteJSON(target) + `}}`
		writeConfigFile(t, dir, "app.json", body)
		tree, err := newTree(t, dir, "app.json", config.Options{})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if tree.Root["spaces"] == nil {
			t.Errorf("root = %#v", tree.Root)
		}
	})

	t.Run("a reference inside a list is followed too", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"webhooks":[{"$ref":"hook.json"},{"url":"b"}]}`)
		writeConfigFile(t, dir, "hook.json", `{"url":"a"}`)
		tree, err := newTree(t, dir, "app.json", config.Options{})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		hooks := tree.Root["webhooks"].([]any)
		if hooks[0].(map[string]any)["url"] != "a" {
			t.Errorf("webhooks = %#v", hooks)
		}
	})

	t.Run("a key beside a reference overrides what it brought in", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json",
			`{"space":{"$ref":"base.json","Path":"overridden","extra":1}}`)
		writeConfigFile(t, dir, "base.json", `{"path":"base","kept":true}`)
		log := &recordingLogger{floor: config.LevelTrace}
		tree, err := newTree(t, dir, "app.json", config.Options{Logger: log})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		space := tree.Root["space"].(map[string]any)
		if space["Path"] != "overridden" {
			t.Errorf("space = %#v", space)
		}
		if _, stale := space["path"]; stale {
			t.Error("both spellings of the overridden key survived")
		}
		if space["kept"] != true || space["extra"] == nil {
			t.Errorf("space = %#v", space)
		}
		if !log.saw(config.EventRefMerge) {
			t.Error("no merge event was written")
		}
	})

	t.Run("several referenced files merge in the order they are written", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"env":{"$ref":["a.json","b.json"]}}`)
		writeConfigFile(t, dir, "a.json", `{"A":"1","Shared":"from a"}`)
		writeConfigFile(t, dir, "b.json", `{"B":"2","shared":"from b"}`)
		tree, err := newTree(t, dir, "app.json", config.Options{})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		env := tree.Root["env"].(map[string]any)
		if env["A"] != "1" || env["B"] != "2" || env["shared"] != "from b" {
			t.Errorf("env = %#v", env)
		}
		if _, stale := env["Shared"]; stale {
			t.Error("both spellings of a merged key survived")
		}
	})

	t.Run("several referenced lists are joined", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"paths":{"$ref":["a.json","b.json"]}}`)
		writeConfigFile(t, dir, "a.json", `["one"]`)
		writeConfigFile(t, dir, "b.json", `["two"]`)
		tree, err := newTree(t, dir, "app.json", config.Options{})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if got := tree.Root["paths"].([]any); len(got) != 2 || got[1] != "two" {
			t.Errorf("paths = %#v", got)
		}
	})

	t.Run("a list of one is the same reference written the long way", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"env":{"$ref":["a.json"]}}`)
		writeConfigFile(t, dir, "a.json", `{"A":"1"}`)
		tree, err := newTree(t, dir, "app.json", config.Options{})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if tree.Root["env"].(map[string]any)["A"] != "1" {
			t.Errorf("env = %#v", tree.Root["env"])
		}
	})

	t.Run("a caller's own reference key is the one that is followed", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, "app.json", `{"env":{"$include":"a.json"},"other":{"$ref":"x"}}`)
		writeConfigFile(t, dir, "a.json", `{"A":"1"}`)
		tree, err := newTree(t, dir, "app.json", config.Options{RefKey: "$include"})
		if err != nil {
			t.Fatalf("ReadTree: %v", err)
		}
		if tree.Root["env"].(map[string]any)["A"] != "1" {
			t.Errorf("env = %#v", tree.Root["env"])
		}
		if tree.Root["other"].(map[string]any)["$ref"] != "x" {
			t.Errorf("the unused key was followed: %#v", tree.Root["other"])
		}
	})

	t.Run("a reference written wrong is refused", func(t *testing.T) {
		cases := []struct {
			name  string
			files map[string]string
			opts  config.Options
			want  string
			is    error
		}{
			{
				name: "a target that is not a path",
				files: map[string]string{
					"app.json": `{"env":{"$ref":42}}`,
				},
				is: config.ErrRefTarget,
			},
			{
				name: "a target that is blank",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"   "}}`,
				},
				is: config.ErrRefTarget,
			},
			{
				name: "a list naming no files",
				files: map[string]string{
					"app.json": `{"env":{"$ref":[]}}`,
				},
				want: "names no files",
			},
			{
				name: "a list element that is not a path",
				files: map[string]string{
					"app.json": `{"env":{"$ref":["a.json",7]}}`,
					"a.json":   `{"A":"1"}`,
				},
				want: "$ref[1] must name another config file",
			},
			{
				name: "a target that does not exist",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"absent.json"}}`,
				},
				want: "$ref \"absent.json\"",
			},
			{
				name: "a target that is empty",
				files: map[string]string{
					"app.json":   `{"env":{"$ref":"empty.yaml"}}`,
					"empty.yaml": "\n",
				},
				want: "the file is empty",
			},
			{
				name: "a target that is a single value beside other keys",
				files: map[string]string{
					"app.json":    `{"env":{"$ref":"scalar.json","extra":1}}`,
					"scalar.json": `5`,
				},
				want: "a single value",
			},
			{
				name: "targets that merge to a list beside other keys",
				files: map[string]string{
					"app.json": `{"env":{"$ref":["a.json","b.json"],"extra":1}}`,
					"a.json":   `["one"]`,
					"b.json":   `["two"]`,
				},
				want: "the files merge to a list",
			},
			{
				name: "targets that disagree about what they hold",
				files: map[string]string{
					"app.json": `{"env":{"$ref":["a.json","b.json"]}}`,
					"a.json":   `{"A":"1"}`,
					"b.json":   `["two"]`,
				},
				want: "must all hold objects, or all hold lists",
			},
			{
				name: "targets whose first holds a single value",
				files: map[string]string{
					"app.json":    `{"env":{"$ref":["scalar.json","b.json"]}}`,
					"scalar.json": `5`,
					"b.json":      `{"B":"2"}`,
				},
				want: "a single value",
			},
			{
				name: "a file that reaches itself",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"a.json"}}`,
					"a.json":   `{"back":{"$ref":"app.json"}}`,
				},
				is: config.ErrRefCycle,
			},
			{
				name: "a chain nested further than the cap",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"a.json"}}`,
					"a.json":   `{"next":{"$ref":"b.json"}}`,
					"b.json":   `{"deep":1}`,
				},
				opts: config.Options{MaxRefDepth: 1},
				is:   config.ErrRefDepth,
			},
			{
				name: "a cap that refuses every reference",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"a.json"}}`,
					"a.json":   `{"A":"1"}`,
				},
				opts: config.Options{MaxRefDepth: -1},
				is:   config.ErrRefDepth,
			},
			{
				name: "a reference inside a list written wrong",
				files: map[string]string{
					"app.json": `{"webhooks":[{"$ref":42}]}`,
				},
				is: config.ErrRefTarget,
			},
			{
				name: "a key beside a reference written wrong",
				files: map[string]string{
					"app.json": `{"env":{"$ref":"a.json","bad":{"$ref":42}}}`,
					"a.json":   `{"A":"1"}`,
				},
				is: config.ErrRefTarget,
			},
			{
				name: "a later target that does not exist",
				files: map[string]string{
					"app.json": `{"env":{"$ref":["a.json","absent.json"]}}`,
					"a.json":   `{"A":"1"}`,
				},
				want: "$ref \"absent.json\"",
			},
			{
				name: "a list joined onto an object",
				files: map[string]string{
					"app.json": `{"env":{"$ref":["a.json","b.json"]}}`,
					"a.json":   `["one"]`,
					"b.json":   `{"B":"2"}`,
				},
				want: "must all hold objects, or all hold lists",
			},
			{
				name: "a target that cannot be parsed",
				files: map[string]string{
					"app.json":    `{"env":{"$ref":"broken.json"}}`,
					"broken.json": `{"a":`,
				},
				want: "$ref \"broken.json\"",
			},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir := t.TempDir()
				for name, body := range c.files {
					writeConfigFile(t, dir, name, body)
				}
				_, err := newTree(t, dir, "app.json", c.opts)
				if err == nil {
					t.Fatal("ReadTree accepted the reference")
				}
				if c.is != nil && !errors.Is(err, c.is) {
					t.Errorf("error = %v, want it to wrap %v", err, c.is)
				}
				if c.want != "" && !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
			})
		}
	})
}

// quoteJSON renders a path as a JSON string, which is what a fixture written
// around an absolute path needs on every platform.
func quoteJSON(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// TestPublicAPIConfigAscent drives the walk up the directory tree: what ends
// it, what is remembered on the way, and what a root claiming a folder does to
// a candidate found below it.
func TestPublicAPIConfigAscent(t *testing.T) {
	const name = "publicapi-ascent.json"
	resolver := func(classify func(map[string]any) config.Class,
		owns func(map[string]any, string, string) bool) config.Resolver {
		return config.Resolver{
			Names:    []string{name},
			Classify: classify,
			Owns:     owns,
			Candidates: func(dir string) ([]string, error) {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					return nil, nil
				}
				return []string{name}, nil
			},
		}
	}
	marker := config.MarkerClassify([]string{"spaces"}, []string{"packages"})
	owner := config.FolderOwner("spaces", "path")

	t.Run("a class names itself", func(t *testing.T) {
		for _, c := range []struct {
			class config.Class
			want  string
		}{
			{config.ClassRoot, "root"},
			{config.ClassCandidate, "candidate"},
			{config.ClassFallback, "fallback"},
			{config.Class(9), "fallback"},
		} {
			if got := c.class.String(); got != c.want {
				t.Errorf("Class(%d).String() = %q, want %q", c.class, got, c.want)
			}
		}
	})

	t.Run("a root ends the ascent", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{"apps":{"path":"apps"}}}`)
		if err := os.MkdirAll(filepath.Join(dir, "apps", "web"), 0o755); err != nil {
			t.Fatal(err)
		}
		log := &recordingLogger{floor: config.LevelTrace}
		l := config.NewLoader(config.Options{Logger: log})
		path, root, err := l.Resolve(context.Background(), filepath.Join(dir, "apps", "web"),
			resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(dir, name) || root != dir {
			t.Errorf("Resolve = %q, %q", path, root)
		}
		for _, event := range []string{config.EventResolveStep, config.EventResolveDone} {
			if !log.saw(event) {
				t.Errorf("no %s event was written", event)
			}
		}
	})

	t.Run("a root claiming the folder displaces the candidate below it", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{"apps":{"path":"apps/web"}}}`)
		writeConfigFile(t, filepath.Join(dir, "apps", "web"), name, `{"packages":{"web":{}}}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), filepath.Join(dir, "apps", "web"),
			resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(dir, name) || root != dir {
			t.Errorf("Resolve = %q, %q, want the owning root", path, root)
		}
	})

	t.Run("a root that claims nothing leaves the candidate standing", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{"apps":{"path":"elsewhere"}}}`)
		nested := filepath.Join(dir, "apps", "web")
		writeConfigFile(t, nested, name, `{"packages":{"web":{}}}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q, want the candidate", path, root)
		}
	})

	t.Run("a resolver claiming nothing always leaves the candidate standing", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{"apps":{"path":"apps/web"}}}`)
		nested := filepath.Join(dir, "apps", "web")
		writeConfigFile(t, nested, name, `{"packages":{"web":{}}}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(marker, nil))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q, want the candidate", path, root)
		}
	})

	t.Run("a candidate with no root above it is the answer anyway", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "apps", "web")
		writeConfigFile(t, nested, name, `{"packages":{"web":{}}}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q", path, root)
		}
	})

	t.Run("a file declaring neither is the weakest answer", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "apps", "web")
		writeConfigFile(t, nested, name, `{"other":1}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q", path, root)
		}
	})

	t.Run("a file that cannot be read ends the ascent where it is", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{}}`)
		nested := filepath.Join(dir, "apps", "web")
		writeConfigFile(t, nested, name, `{"broken":`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(marker, owner))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q, want the broken file rather than a parent's", path, root)
		}
	})

	t.Run("a nil Classify makes every file a root", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "apps")
		writeConfigFile(t, nested, name, `{"anything":1}`)
		l := config.NewLoader(config.Options{})
		path, root, err := l.Resolve(context.Background(), nested, resolver(nil, nil))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(nested, name) || root != nested {
			t.Errorf("Resolve = %q, %q", path, root)
		}
	})

	t.Run("a resolver with no probe of its own looks the names up", func(t *testing.T) {
		dir := t.TempDir()
		writeConfigFile(t, dir, name, `{"spaces":{}}`)
		l := config.NewLoader(config.Options{})
		path, _, err := l.Resolve(context.Background(), dir, config.Resolver{Names: []string{name}})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if path != filepath.Join(dir, name) {
			t.Errorf("Resolve = %q", path)
		}
	})

	t.Run("a probe that fails stops the ascent", func(t *testing.T) {
		broken := errors.New("cannot list")
		l := config.NewLoader(config.Options{})
		_, _, err := l.Resolve(context.Background(), t.TempDir(), config.Resolver{
			Names:      []string{name},
			Candidates: func(string) ([]string, error) { return nil, broken },
		})
		if !errors.Is(err, broken) {
			t.Fatalf("Resolve = %v", err)
		}
	})

	t.Run("no file anywhere lists what was looked for", func(t *testing.T) {
		l := config.NewLoader(config.Options{})
		_, _, err := l.Resolve(context.Background(), t.TempDir(), config.Resolver{
			Names:      []string{"publicapi-nowhere.json", "publicapi-nowhere.yaml"},
			Candidates: func(string) ([]string, error) { return nil, nil },
		})
		if !errors.Is(err, config.ErrNoConfig) {
			t.Fatalf("Resolve = %v, want ErrNoConfig", err)
		}
		var noConfig *config.NoConfigError
		if !errors.As(err, &noConfig) || len(noConfig.Names) != 2 {
			t.Errorf("error = %#v", noConfig)
		}
	})

	t.Run("the marker classifier reads the keys a file declares", func(t *testing.T) {
		if got := marker(map[string]any{"Spaces": map[string]any{"a": 1}}); got != config.ClassRoot {
			t.Errorf("a root marker classified as %v", got)
		}
		if got := marker(map[string]any{"Packages": map[string]any{"a": 1}}); got != config.ClassCandidate {
			t.Errorf("a candidate marker classified as %v", got)
		}
		if got := marker(map[string]any{"spaces": nil}); got != config.ClassFallback {
			t.Errorf("a key holding nothing classified as %v", got)
		}
		if got := marker(map[string]any{}); got != config.ClassFallback {
			t.Errorf("an empty document classified as %v", got)
		}
	})

	t.Run("the folder owner compares folders by identity", func(t *testing.T) {
		dir := t.TempDir()
		apps := filepath.Join(dir, "apps")
		if err := os.MkdirAll(apps, 0o755); err != nil {
			t.Fatal(err)
		}
		root := map[string]any{"Spaces": map[string]any{
			"apps":  map[string]any{"Path": []any{"unknown", "apps"}},
			"one":   map[string]any{"path": "apps"},
			"empty": map[string]any{"path": ""},
		}}
		if !owner(root, dir, apps) {
			t.Error("a declared folder was not claimed")
		}
		if owner(root, dir, filepath.Join(dir, "absent")) {
			t.Error("a folder that does not exist was claimed")
		}
		for _, c := range []struct {
			name string
			root map[string]any
		}{
			{"a collection that is not an object", map[string]any{"spaces": 1}},
			{"a collection that is absent", map[string]any{}},
			{"an entry that is not an object", map[string]any{"spaces": map[string]any{"a": 1}}},
			{"an entry with no path key", map[string]any{"spaces": map[string]any{"a": map[string]any{}}}},
			{"a path that is neither a name nor a list", map[string]any{
				"spaces": map[string]any{"a": map[string]any{"path": 7}}}},
			{"a path list holding something that is not a name", map[string]any{
				"spaces": map[string]any{"a": map[string]any{"path": []any{7}}}}},
			{"a path written empty", map[string]any{
				"spaces": map[string]any{"a": map[string]any{"path": ""}}}},
		} {
			if owner(c.root, dir, apps) {
				t.Errorf("%s claimed the folder", c.name)
			}
		}
	})
}

// TestPublicAPIConfigEditWriting drives writing one key back into a config file:
// the formats that can be spliced, the one that cannot, the backup, and the
// bytes outside the edit that have to survive.
func TestPublicAPIConfigEditWriting(t *testing.T) {
	ctx := context.Background()

	t.Run("a JSON key is spliced and everything around it survives", func(t *testing.T) {
		dir := t.TempDir()
		const body = `{
    "name": "app",
    "dependencies": {
        "app": ["core"]
    },
    "logLevel": "info"
}
`
		path := writeConfigFile(t, dir, "app.json", body)
		log := &recordingLogger{floor: config.LevelTrace}
		err := config.ApplyEdits(config.WithLogger(ctx, log), path, []config.Edit{
			{KeyPath: []string{"dependencies"}, Value: map[string][]string{"app": {"core", "utils"}}},
			{KeyPath: []string{"webhooks"}, Value: []string{"one"}},
		})
		if err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		out := readConfigFile(t, path)
		if !strings.Contains(out, `"logLevel": "info"`) || !strings.Contains(out, `"name": "app"`) {
			t.Errorf("the untouched keys did not survive:\n%s", out)
		}
		if !strings.Contains(out, `"utils"`) || !strings.Contains(out, `"webhooks"`) {
			t.Errorf("the edits did not land:\n%s", out)
		}
		if !strings.Contains(out, "\n    \"webhooks\"") {
			t.Errorf("the file's own indent was not used:\n%s", out)
		}
		if got := readConfigFile(t, path+config.BackupSuffix); got != body {
			t.Errorf("the backup is not the pre-edit file:\n%s", got)
		}
		for _, event := range []string{config.EventEditPrepared, config.EventEditCommitted} {
			if !log.saw(event) {
				t.Errorf("no %s event was written", event)
			}
		}
	})

	t.Run("a key created in an empty object opens the object", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{}\n")
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"name"}, Value: "app"},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		if out := readConfigFile(t, path); !strings.Contains(out, `"name": "app"`) {
			t.Errorf("out =\n%s", out)
		}
	})

	t.Run("a nested JSON key is found case-insensitively", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json",
			"{\n  \"Packages\": {\n    \"web\": {\n      \"dependencies\": [\"core\"]\n    }\n  }\n}\n")
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"packages", "WEB", "dependencies"}, Value: []string{"core", "utils"}},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		if out := readConfigFile(t, path); !strings.Contains(out, `"utils"`) {
			t.Errorf("out =\n%s", out)
		}
	})

	t.Run("an emptied list is written as one rather than as nothing", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{\n  \"paths\": [\"a\"]\n}\n")
		var none []string
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"paths"}, Value: none},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		if out := readConfigFile(t, path); !strings.Contains(out, `"paths": []`) {
			t.Errorf("out =\n%s", out)
		}
	})

	t.Run("a YAML key keeps the file's comments", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.yaml",
			"# a retained comment\nname: app\npackages:\n  web:\n    dependencies: [core]\n")
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"packages", "web", "dependencies"}, Value: []string{"core", "utils"}},
			{KeyPath: []string{"logLevel"}, Value: "debug"},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		out := readConfigFile(t, path)
		if !strings.Contains(out, "# a retained comment") {
			t.Errorf("the comment did not survive:\n%s", out)
		}
		if !strings.Contains(out, "utils") || !strings.Contains(out, "logLevel: debug") {
			t.Errorf("the edits did not land:\n%s", out)
		}
	})

	t.Run("a whole document is replaced when the key is the reference", func(t *testing.T) {
		dir := t.TempDir()
		jsonPath := writeConfigFile(t, dir, "fragment.json", "{\n  \"old\": true\n}\n")
		if err := config.ApplyEdits(ctx, jsonPath, []config.Edit{
			{Value: map[string]any{"new": true}},
		}); err != nil {
			t.Fatalf("ApplyEdits(json): %v", err)
		}
		if out := readConfigFile(t, jsonPath); !strings.Contains(out, `"new"`) {
			t.Errorf("out =\n%s", out)
		}
		yamlPath := writeConfigFile(t, dir, "fragment.yaml", "old: true\n")
		if err := config.ApplyEdits(ctx, yamlPath, []config.Edit{
			{Value: map[string]any{"new": true}},
		}); err != nil {
			t.Fatalf("ApplyEdits(yaml): %v", err)
		}
		if out := readConfigFile(t, yamlPath); !strings.Contains(out, "new: true") {
			t.Errorf("out =\n%s", out)
		}
		emptied := writeConfigFile(t, dir, "list.json", "[\n  \"a\"\n]\n")
		var none []string
		if err := config.ApplyEdits(ctx, emptied, []config.Edit{{Value: none}}); err != nil {
			t.Fatalf("ApplyEdits(empty list): %v", err)
		}
		if out := strings.TrimSpace(readConfigFile(t, emptied)); out != "[]" {
			t.Errorf("out = %q, want an empty list", out)
		}
	})

	t.Run("an edit set that changes nothing writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{\n  \"name\": \"app\"\n}\n")
		if err := config.ApplyEdits(ctx, path, nil); err != nil {
			t.Fatalf("ApplyEdits with no edits: %v", err)
		}
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"name"}, Value: "app"},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		if _, err := os.Stat(path + config.BackupSuffix); err == nil {
			t.Error("a no-op edit wrote a backup")
		}
	})

	t.Run("a prepared edit is rendered before anything is written", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{\n  \"name\": \"app\"\n}\n")
		prepared, err := config.PrepareEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"name"}, Value: "renamed"},
		})
		if err != nil {
			t.Fatalf("PrepareEdits: %v", err)
		}
		if prepared.Path != path {
			t.Errorf("Path = %q", prepared.Path)
		}
		if strings.Contains(readConfigFile(t, path), "renamed") {
			t.Error("PrepareEdits wrote the file")
		}
		if err := prepared.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if !strings.Contains(readConfigFile(t, path), "renamed") {
			t.Error("Commit did not write the file")
		}
	})

	t.Run("a TOML config is refused with a paste-ready snippet instead", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.toml", "name = \"app\"\n")
		err := config.ApplyEdits(ctx, path, []config.Edit{{KeyPath: []string{"name"}, Value: "x"}})
		if !errors.Is(err, config.ErrTOMLEdit) {
			t.Fatalf("ApplyEdits = %v, want ErrTOMLEdit", err)
		}
		snippet, err := config.RenderKeyTOML([]string{"packages", "web", "dependencies"}, []string{"core"})
		if err != nil {
			t.Fatalf("RenderKeyTOML: %v", err)
		}
		if !strings.Contains(snippet, "dependencies") || !strings.Contains(snippet, "core") {
			t.Errorf("snippet =\n%s", snippet)
		}
	})

	t.Run("an edit written wrong is refused", func(t *testing.T) {
		cases := []struct {
			name string
			file string
			body string
			edit config.Edit
			want string
		}{
			{"a format with no writer", "app.ini", "name = x\n",
				config.Edit{KeyPath: []string{"name"}, Value: "y"}, "unknown config format"},
			{"a whole document in a format with no writer", "app.ini", "name = x\n",
				config.Edit{Value: map[string]any{"a": 1}}, "unknown config format"},
			{"a value JSON cannot render", "app.json", "{}\n",
				config.Edit{KeyPath: []string{"name"}, Value: make(chan int)}, "chan"},
			{"a whole document JSON cannot render", "app.json", "{}\n",
				config.Edit{Value: make(chan int)}, "chan"},
			{"a nested key that is not there", "app.json", "{\n  \"a\": {}\n}\n",
				config.Edit{KeyPath: []string{"a", "b"}, Value: 1}, "not found"},
			{"an ancestor that is not an object", "app.json", "{\n  \"a\": 1\n}\n",
				config.Edit{KeyPath: []string{"a", "b"}, Value: 1}, `"a" is not an object`},
			{"a JSON document that is not an object", "app.json", "[1]\n",
				config.Edit{KeyPath: []string{"a"}, Value: 1}, "top level is not an object"},
			{"a JSON document that is malformed", "app.json", "{\"a\": [1, 2\n",
				config.Edit{KeyPath: []string{"b"}, Value: 1}, "EOF"},
			{"an empty JSON document", "app.json", "",
				config.Edit{KeyPath: []string{"a"}, Value: 1}, "EOF"},
			{"a YAML document that is malformed", "app.yaml", "a: [1\n",
				config.Edit{KeyPath: []string{"a"}, Value: 1}, "yaml"},
			{"a YAML document that is not a mapping", "app.yaml", "- a\n",
				config.Edit{KeyPath: []string{"a"}, Value: 1}, "top level is not a mapping"},
			{"a YAML ancestor that is not a mapping", "app.yaml", "a: 1\n",
				config.Edit{KeyPath: []string{"a", "b"}, Value: 1}, `"a" is not a mapping`},
			{"a nested YAML key that is not there", "app.yaml", "a:\n  c: 1\n",
				config.Edit{KeyPath: []string{"a", "b"}, Value: 1}, "not found"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir := t.TempDir()
				path := writeConfigFile(t, dir, c.file, c.body)
				err := config.ApplyEdits(ctx, path, []config.Edit{c.edit})
				if err == nil {
					t.Fatal("ApplyEdits accepted the edit")
				}
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), c.want)
				}
				if got := readConfigFile(t, path); got != c.body {
					t.Errorf("a refused edit rewrote the file:\n%s", got)
				}
			})
		}
	})

	t.Run("a file that is not there is not an edit", func(t *testing.T) {
		_, err := config.PrepareEdits(ctx, filepath.Join(t.TempDir(), "absent.json"),
			[]config.Edit{{KeyPath: []string{"a"}, Value: 1}})
		if err == nil {
			t.Fatal("PrepareEdits accepted a file that is not there")
		}
	})

	t.Run("a backup that cannot be written leaves the config alone", func(t *testing.T) {
		dir := t.TempDir()
		const body = "{\n  \"name\": \"app\"\n}\n"
		path := writeConfigFile(t, dir, "app.json", body)
		if err := os.Mkdir(path+config.BackupSuffix, 0o755); err != nil {
			t.Fatal(err)
		}
		err := config.ApplyEdits(ctx, path, []config.Edit{{KeyPath: []string{"name"}, Value: "renamed"}})
		if err == nil || !strings.Contains(err.Error(), "saving backup") {
			t.Fatalf("ApplyEdits = %v, want the backup failure", err)
		}
		if got := readConfigFile(t, path); got != body {
			t.Errorf("the config was rewritten anyway:\n%s", got)
		}
	})

	t.Run("a folder nothing can be written into leaves the config alone", func(t *testing.T) {
		dir := t.TempDir()
		const body = "{\n  \"name\": \"app\"\n}\n"
		path := writeConfigFile(t, dir, "app.json", body)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		err := config.ApplyEdits(ctx, path, []config.Edit{{KeyPath: []string{"name"}, Value: "renamed"}})
		if err == nil || !strings.Contains(err.Error(), "saving backup") {
			t.Fatalf("ApplyEdits = %v, want the backup failure", err)
		}
		if got := readConfigFile(t, path); got != body {
			t.Errorf("the config was rewritten anyway:\n%s", got)
		}
	})

	t.Run("the config's own permissions survive the rewrite", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", "{\n  \"name\": \"app\"\n}\n")
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := config.ApplyEdits(ctx, path, []config.Edit{
			{KeyPath: []string{"name"}, Value: "renamed"},
		}); err != nil {
			t.Fatalf("ApplyEdits: %v", err)
		}
		for _, p := range []string{path, path + config.BackupSuffix} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("%s = %v, want the config's own permissions", p, info.Mode().Perm())
			}
		}
	})
}

// TestPublicAPIConfigEditResolution drives which file an edit is written to
// when the configuration is split across several through `$ref`.
func TestPublicAPIConfigEditResolution(t *testing.T) {
	ctx := context.Background()

	t.Run("a key the entry file holds stays there", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"packages":{"web":{"dependencies":["core"]}}}`)
		file, keyPath, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"packages", "web", "dependencies"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != path || !slices.Equal(keyPath, []string{"packages", "web", "dependencies"}) {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a reference moves the edit into the file it names", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"packages":{"$ref":"packages.json"}}`)
		target := writeConfigFile(t, dir, "packages.json", `{"web":{"dependencies":["core"]}}`)
		file, keyPath, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"packages", "web", "dependencies"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != target || !slices.Equal(keyPath, []string{"web", "dependencies"}) {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a key that is the reference rewrites the whole referenced file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"dependencies":{"$ref":"deps.json"}}`)
		target := writeConfigFile(t, dir, "deps.json", `{"app":["core"]}`)
		file, keyPath, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"dependencies"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != target || len(keyPath) != 0 {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a key written beside a reference keeps the edit where it is", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json",
			`{"packages":{"$ref":"packages.json","web":{"dependencies":["core"]}}}`)
		writeConfigFile(t, dir, "packages.json", `{"docs":{}}`)
		file, keyPath, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"packages", "web", "dependencies"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != path || len(keyPath) != 3 {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a key composed from a reference and its neighbours has no one file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json",
			`{"packages":{"$ref":"packages.json","web":{}}}`)
		writeConfigFile(t, dir, "packages.json", `{"docs":{}}`)
		_, _, err := config.NewLoader(config.Options{}).ResolveEdit(ctx, path, []string{"packages"})
		if !errors.Is(err, config.ErrRefEdit) {
			t.Fatalf("ResolveEdit = %v, want ErrRefEdit", err)
		}
	})

	t.Run("a key merged from several files has no one file either", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"packages":{"$ref":["a.json","b.json"]}}`)
		writeConfigFile(t, dir, "a.json", `{"web":{}}`)
		writeConfigFile(t, dir, "b.json", `{"docs":{}}`)
		_, _, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"packages", "web"})
		if !errors.Is(err, config.ErrMultiRefEdit) {
			t.Fatalf("ResolveEdit = %v, want ErrMultiRefEdit", err)
		}
	})

	t.Run("a key no file holds comes back unchanged", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"name":"app"}`)
		l := config.NewLoader(config.Options{})
		file, keyPath, err := l.ResolveEdit(ctx, path, []string{"packages", "web"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != path || len(keyPath) != 2 {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
		file, keyPath, err = l.ResolveEdit(ctx, path, []string{"name", "deeper"})
		if err != nil {
			t.Fatalf("ResolveEdit through a scalar: %v", err)
		}
		if file != path || len(keyPath) != 2 {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a document that is not an object is handed back as it is", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "list.json", `[1,2]`)
		file, keyPath, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"a"})
		if err != nil {
			t.Fatalf("ResolveEdit: %v", err)
		}
		if file != path || len(keyPath) != 1 {
			t.Errorf("ResolveEdit = %q, %v", file, keyPath)
		}
	})

	t.Run("a file that cannot be read is a read failure", func(t *testing.T) {
		_, _, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, filepath.Join(t.TempDir(), "absent.json"), []string{"a"})
		var fileErr *config.FileError
		if !errors.As(err, &fileErr) {
			t.Fatalf("ResolveEdit = %v, want a FileError", err)
		}
	})

	t.Run("a reference written wrong is reported where it is", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"packages":{"$ref":42}}`)
		_, _, err := config.NewLoader(config.Options{}).
			ResolveEdit(ctx, path, []string{"packages", "web"})
		if !errors.Is(err, config.ErrRefTarget) {
			t.Fatalf("ResolveEdit = %v, want ErrRefTarget", err)
		}
	})

	t.Run("a chain nested past the cap stops rather than explains", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"a":{"$ref":"one.json"}}`)
		writeConfigFile(t, dir, "one.json", `{"b":{"$ref":"two.json"}}`)
		writeConfigFile(t, dir, "two.json", `{"c":1}`)
		_, _, err := config.NewLoader(config.Options{MaxRefDepth: 1}).
			ResolveEdit(ctx, path, []string{"a", "b", "c"})
		if err == nil || !strings.Contains(err.Error(), "files deep") {
			t.Fatalf("ResolveEdit = %v", err)
		}
	})
}

// TestPublicAPIConfigStringMapReading drives the reader a writer starts from:
// the entries a file already carries, key for key, in every format.
func TestPublicAPIConfigStringMapReading(t *testing.T) {
	ctx := context.Background()
	l := config.NewLoader(config.Options{})

	t.Run("the entries come back exactly as the file spells them", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json",
			`{"spaces":{"apps":{"env":{"PATH":"/bin","Path":"/other"}}}}`)
		got, err := l.StringMapAt(ctx, path, []string{"spaces", "apps", "env"})
		if err != nil {
			t.Fatalf("StringMapAt: %v", err)
		}
		if got["PATH"] != "/bin" || got["Path"] != "/other" {
			t.Errorf("StringMapAt = %v, want both spellings kept", got)
		}
	})

	t.Run("a TOML config is read too", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.toml", "[env]\nCI = \"true\"\n")
		got, err := l.StringMapAt(ctx, path, []string{"env"})
		if err != nil {
			t.Fatalf("StringMapAt: %v", err)
		}
		if got["CI"] != "true" {
			t.Errorf("StringMapAt = %v", got)
		}
	})

	t.Run("the whole document is the map when no key is named", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "env.json", `{"CI":"true"}`)
		got, err := l.StringMapAt(ctx, path, nil)
		if err != nil || got["CI"] != "true" {
			t.Errorf("StringMapAt = %v, %v", got, err)
		}
	})

	t.Run("a key the file does not carry is no error", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"name":"app"}`)
		got, err := l.StringMapAt(ctx, path, []string{"env"})
		if err != nil || got != nil {
			t.Errorf("StringMapAt = %v, %v", got, err)
		}
	})

	t.Run("a value that is not a map of scalars is", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfigFile(t, dir, "app.json", `{"env":"CI=true","other":{"a":1}}`)
		if _, err := l.StringMapAt(ctx, path, []string{"env"}); err == nil ||
			!strings.Contains(err.Error(), "is not an object") {
			t.Errorf("StringMapAt = %v", err)
		}
		if _, err := l.StringMapAt(ctx, path, []string{"other"}); err == nil ||
			!strings.Contains(err.Error(), "is not a string") {
			t.Errorf("StringMapAt = %v", err)
		}
	})

	t.Run("a file that cannot be read is a read failure", func(t *testing.T) {
		_, err := l.StringMapAt(ctx, filepath.Join(t.TempDir(), "absent.json"), []string{"env"})
		if err == nil {
			t.Fatal("StringMapAt accepted a file that is not there")
		}
	})
}
