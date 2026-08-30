package config

// The benchmarks, over fixtures built in code rather than read from testdata,
// so a run measures the loader and not the filesystem underneath it. The
// documents are served through Options.ReadFile from a map; the two that
// cannot be — the ascent, which stats directories, and the writers, which
// rewrite files — use a real temp directory and say so.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The three fixture sizes, in keys.
const (
	benchSmall  = 10
	benchMedium = 100
	benchLarge  = 1000
)

// benchDoc builds one document of n top-level keys, a tenth of which are
// objects holding three keys of their own and a list. The shapes are what a
// real configuration holds; the values are derived from the index, so a run is
// the same run every time.
func benchDoc(format string, n int) string {
	var b strings.Builder
	switch format {
	case "json":
		b.WriteString("{\n")
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",\n")
			}
			key := benchKey(i)
			if i%10 == 0 {
				fmt.Fprintf(&b, "  %q: {\"name\": %q, \"count\": %d, \"tags\": [%q, %q]}",
					key, key, i, "a"+strconv.Itoa(i), "b"+strconv.Itoa(i))
				continue
			}
			fmt.Fprintf(&b, "  %q: %q", key, "value"+strconv.Itoa(i))
		}
		b.WriteString("\n}\n")
	case "yaml":
		for i := 0; i < n; i++ {
			key := benchKey(i)
			if i%10 == 0 {
				fmt.Fprintf(&b, "%s:\n  name: %s\n  count: %d\n  tags: [a%d, b%d]\n", key, key, i, i, i)
				continue
			}
			fmt.Fprintf(&b, "%s: value%d\n", key, i)
		}
	case "toml":
		for i := 0; i < n; i++ {
			if i%10 != 0 {
				fmt.Fprintf(&b, "%s = \"value%d\"\n", benchKey(i), i)
			}
		}
		for i := 0; i < n; i += 10 {
			key := benchKey(i)
			fmt.Fprintf(&b, "\n[%s]\nname = %q\ncount = %d\ntags = [\"a%d\", \"b%d\"]\n", key, key, i, i, i)
		}
	}
	return b.String()
}

// benchKey is a key of the fixture: mixed case, because folding a name that
// is already lower case is the fast path and a benchmark that only ever takes
// it is not measuring the fold.
func benchKey(i int) string {
	if i%3 == 0 {
		return fmt.Sprintf("Key%03d", i)
	}
	return fmt.Sprintf("key%03d", i)
}

// benchLoader serves a set of documents from memory.
func benchLoader(files map[string]string) *Loader {
	return NewLoader(Options{ReadFile: func(path string) ([]byte, error) {
		body, ok := files[filepath.ToSlash(path)]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(body), nil
	}})
}

func BenchmarkReadTree(b *testing.B) {
	for _, format := range []string{"json", "yaml", "toml"} {
		for _, size := range []struct {
			name string
			n    int
		}{{"small", benchSmall}, {"medium", benchMedium}, {"large", benchLarge}} {
			doc := benchDoc(format, size.n)
			path := "/bench/app." + format
			l := benchLoader(map[string]string{path: doc})
			ctx := context.Background()
			b.Run(format+"/"+size.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(doc)))
				for i := 0; i < b.N; i++ {
					if _, err := l.ReadTree(ctx, path); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// benchChain is a chain of n files, each referencing the next, with the last
// one holding a small document.
func benchChain(n int) (map[string]string, string) {
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("/bench/f%d.json", i)] =
			fmt.Sprintf(`{"next": {"$ref": "./f%d.json"}}`, i+1)
	}
	files[fmt.Sprintf("/bench/f%d.json", n)] = benchDoc("json", benchSmall)
	return files, "/bench/f0.json"
}

func BenchmarkRefDepth(b *testing.B) {
	for _, depth := range []int{1, 8, 32} {
		files, path := benchChain(depth)
		l := benchLoader(files)
		ctx := context.Background()
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := l.ReadTree(ctx, path); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRefWidth(b *testing.B) {
	for _, width := range []int{2, 8, 32} {
		files := map[string]string{}
		targets := make([]string, 0, width)
		for i := 0; i < width; i++ {
			name := fmt.Sprintf("./part%d.json", i)
			targets = append(targets, strconv.Quote(name))
			files[fmt.Sprintf("/bench/part%d.json", i)] =
				fmt.Sprintf(`{"key%03d": "value", "shared": %d}`, i, i)
		}
		files["/bench/app.json"] = `{"merged": {"$ref": [` + strings.Join(targets, ", ") + `]}}`
		l := benchLoader(files)
		ctx := context.Background()
		b.Run(strconv.Itoa(width), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := l.ReadTree(ctx, "/bench/app.json"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// benchTrees are the four shapes Settings has a rule about.
func benchTrees() map[string]map[string]any {
	flat := make(map[string]any, benchMedium)
	dotted := make(map[string]any, benchMedium)
	hollow := make(map[string]any, benchMedium)
	nested := make(map[string]any, benchMedium/10)
	for i := 0; i < benchMedium; i++ {
		key := benchKey(i)
		flat[key] = "value" + strconv.Itoa(i)
		dotted[key+".inner.leaf"] = "value" + strconv.Itoa(i)
		hollow[key] = map[string]any{"inner": map[string]any{}}
	}
	for i := 0; i < benchMedium/10; i++ {
		level := map[string]any{}
		for j := 0; j < 10; j++ {
			level[benchKey(j)] = "value" + strconv.Itoa(j)
		}
		nested[benchKey(i)] = level
	}
	return map[string]map[string]any{
		"flat": flat, "nested": nested, "dotted": dotted, "empty-objects": hollow,
	}
}

func BenchmarkSettings(b *testing.B) {
	l := NewLoader(Options{})
	for name, root := range benchTrees() {
		tree := &Tree{Root: root}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = tree.Settings(l, nil)
			}
		})
	}
}

func BenchmarkOverrides(b *testing.B) {
	l := NewLoader(Options{})
	tree := &Tree{Root: benchTrees()["nested"]}
	ov := Overrides{
		"Key000.Key003":   "override",
		"key001.key004":   "override",
		"missing.deep":    "created",
		benchKey(9) + ".": "odd",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = tree.Settings(l, ov)
	}
}

// benchDecodeSrc is the settings map the decode benchmarks read: a small one,
// a full one naming every key of the model, and one whose keys are all spelled
// in a case the table is not keyed by.
func benchDecodeSrc() map[string]map[string]any {
	full := map[string]any{
		"name":        "app",
		"shell":       []any{"/bin/sh", "-c"},
		"concurrency": []any{4, 2},
		"loglevel":    "warn",
		"strict":      true,
		"quiet":       false,
		"verbose":     true,
		"env":         map[string]any{"PATH": "/usr/bin", "HOME": "/root", "LANG": "C"},
		"custom":      map[string]any{"team": "platform", "oncall": "nobody"},
		"flow":        map[string]any{"build": []any{"compile"}, "publish": []any{"ship"}},
		"tags":        []any{"a", "b", "c"},
		"hooks": []any{
			map[string]any{"url": "one", "events": []any{"a"}, "retries": 3},
			map[string]any{"url": "two", "events": []any{"b"}, "enabled": true},
		},
		"areas": map[string]any{},
	}
	areas := map[string]any{}
	for i := 0; i < 20; i++ {
		areas[benchKey(i)] = map[string]any{
			"path":       []any{"pkgs/" + benchKey(i)},
			"versioning": "fixed",
			"env":        map[string]any{"A": "1"},
		}
	}
	full["areas"] = areas

	folded := make(map[string]any, len(full))
	for k, v := range full {
		folded[strings.ToUpper(k[:1])+k[1:]] = v
	}
	return map[string]map[string]any{
		"small":    {"name": "app", "loglevel": "warn", "tags": []any{"a"}},
		"full":     full,
		"fold-hit": folded,
	}
}

func BenchmarkDecode(b *testing.B) {
	for name, src := range benchDecodeSrc() {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var cfg appConfig
				if err := DecodeObject(src, "", appFields(&cfg)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLoad is the whole pipeline: read a split configuration, render the
// settings with an override over it, decode into the model.
func BenchmarkLoad(b *testing.B) {
	files := map[string]string{
		"/bench/app.json": `{
			"name": "app",
			"loglevel": "info",
			"env": {"$ref": "./env.yaml"},
			"areas": {"$ref": ["./base.json", "./extra.json"]},
			"hooks": [{"url": "one", "events": "a,b"}]
		}`,
		"/bench/env.yaml":   "PATH: /usr/bin\nHOME: /root\n",
		"/bench/base.json":  `{"libs": {"path": "pkgs", "versioning": "fixed"}}`,
		"/bench/extra.json": `{"apps": {"path": ["apps", "extra"]}}`,
	}
	l := benchLoader(files)
	ctx := context.Background()
	ov := Overrides{"logLevel": "warn"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tree, err := l.ReadTree(ctx, "/bench/app.json")
		if err != nil {
			b.Fatal(err)
		}
		var cfg appConfig
		if err := DecodeObject(tree.Settings(l, ov), "", appFields(&cfg)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkResolve is the ascent, which stats real directories and so runs
// against a real temp tree.
func BenchmarkResolve(b *testing.B) {
	for _, depth := range []int{1, 4, 16} {
		dir := b.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "app.json"),
			[]byte(`{"areas": {"libs": {"path": "pkgs"}}}`), 0o644); err != nil {
			b.Fatal(err)
		}
		deep := dir
		for i := 0; i < depth; i++ {
			deep = filepath.Join(deep, "d"+strconv.Itoa(i))
		}
		if err := os.MkdirAll(deep, 0o755); err != nil {
			b.Fatal(err)
		}
		l := NewLoader(Options{})
		r := Resolver{
			Names:    []string{"app.json", "app.yaml"},
			Classify: MarkerClassify([]string{"areas"}, []string{"hooks"}),
			Owns:     FolderOwner("areas", "path"),
		}
		ctx := context.Background()
		b.Run(strconv.Itoa(depth), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := l.Resolve(ctx, deep, r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEnvBinding(b *testing.B) {
	keys := make([]string, 0, 32)
	environ := make([]string, 0, 64)
	for i := 0; i < 32; i++ {
		key := "section" + strconv.Itoa(i) + ".value"
		keys = append(keys, key)
		environ = append(environ, EnvVarName("APP_", key, ".")+"=v")
		environ = append(environ, "UNRELATED"+strconv.Itoa(i)+"=v")
	}
	binding := EnvBinding{Prefix: "APP_", Keys: keys, Environ: environ}
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := binding.Overrides(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkApplyEdits rewrites a real file, which is what it is for.
func BenchmarkApplyEdits(b *testing.B) {
	for _, format := range []string{"json", "yaml"} {
		doc := benchDoc(format, benchMedium)
		ctx := context.Background()
		b.Run(format, func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "app."+format)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				err := ApplyEdits(ctx, path, []Edit{
					{KeyPath: []string{benchKey(1)}, Value: []string{"one", "two"}},
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFold(b *testing.B) {
	for _, tc := range []struct{ name, in string }{
		{"lower", "loglevel"},
		{"mixed", "LogLevel"},
		{"unicode", "logLevelÄ"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = Fold(tc.in)
			}
		})
	}
}
