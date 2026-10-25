# config: the configuration loader

Use `github.com/yohimik/dispat/pkg/config` to load JSON, YAML, and TOML into your own Go structs. It is the same
loader dispat uses, but it works independently of dispat's configuration model.

The loader can find a configuration by walking up from the current directory, combine files with `$ref`, and apply
overrides. You declare the accepted fields in a table of setters.

```sh
go get github.com/yohimik/dispat/pkg/config
```

The package uses no reflection. The field table makes accepted keys explicit and lets the package link under TinyGo.

## Loading a file

```go
l := config.NewLoader(config.Options{})

tree, err := l.ReadTree(ctx, "app.yaml")   // parse, follow every $ref
if err != nil {
	// err names the file, the key path inside it, and what was wrong there.
}

var cfg Config
err = config.DecodeObject(tree.Settings(l, nil), "", configFields(&cfg))
```

Start with `Options{}` for the defaults; every option has a valid zero value. Set only what you need:

| Option | Controls |
| --- | --- |
| `RefKey` | The key used for file references. |
| `MaxRefDepth` | How deeply references may nest. |
| `KeyDelim` | The separator in nested key paths. |
| `ReadFile` | The function used for every file read. Useful for tests or a custom filesystem. |
| `Formats` | The parser associated with each file extension. |
| `Logger` | Where loader events are sent. |

## The fields table

List the accepted keys and their setters in a `config.Fields` table:

```go
func configFields(dst *Config) config.Fields {
	return config.Fields{
		"name":     config.String(&dst.Name),
		"loglevel": config.String(&dst.LogLevel),
		"env":      config.StringMap(&dst.Env),
		"areas":    config.ObjectMap(&dst.Areas, areaFields),
	}
}
```

Table keys use `Fold`: ordinary lowercase, with ς folded to σ. Input keys are folded for lookup without being
renamed, so `logLevel` and `loglevel` both match `loglevel` in the table. An unknown key is rejected with its full path
from the root. There is no separate option to enable unknown-key checking.

`DecodeObject` checks that the value is an object and that no two keys fold to the same name. It visits keys in
sorted order, making the first reported error consistent across runs. Empty values are treated as absent.

| Setter | Fills | Shorthands it takes |
|--------|-------|---------------------|
| `String`, `Int`, `Bool`, `BoolPtr` | a scalar field | any format's spelling of the value |
| `Strings`, `Ints` | a list | a scalar is the one-element list; a comma-separated string is the list |
| `StringMap`, `MapOf` | a map of names to values | keys keep the case the file wrote |
| `RawMap` | a free-form block | nothing inside is a key the model has to know |
| `Object` | an optional sub-object | allocated only when the file wrote one |
| `ObjectMap`, `ObjectList` | named entries, a list of objects | a lone object is the one-element list |

For a custom value shape, write a setter with the signature `func(val any, at string) error`. Use `MapOf` for a map
with custom values: it applies the object rules and calls your reader for each value.

## Resolving the file

`Loader.Resolve` walks up from a directory looking for your file names, which is what lets a command run from inside a
sub-folder and still load the project's configuration:

```go
path, root, err := l.Resolve(ctx, cwd, config.Resolver{
	Names:    []string{"app.json", "app.yaml", "app.toml"},
	Classify: config.MarkerClassify([]string{"areas"}, []string{"tasks"}),
	Owns:     config.FolderOwner("areas", "path"),
})
```

`Classify` labels each file as a root, candidate, or fallback. The search remembers the first candidate and fallback,
then stops at the first root. That root replaces a remembered candidate only if `Owns` confirms that it owns the
candidate's folder. This lets a subfolder keep a configuration of its own.

An unreadable file stops the search with an error. The loader does not skip a broken configuration and continue to a
parent.

## Composing with `$ref`

An object holding `$ref` is replaced by the file it names, resolved against the directory of the file that wrote it:

```yaml
areas:
  libs: {$ref: ./shared/area.json}
  apps: {$ref: ./shared/area.json, versioning: independent}
  both: {$ref: [./base.json, ./overlay.json]}
```

A reference replaces the entire value with the referenced document, which may be an object, list, or scalar. Keys
beside `$ref` override imported keys and keep their own spelling.

When `$ref` names several files, they are read in order. Objects merge by key, with later values winning; lists are
concatenated. Incompatible value shapes produce an error.

The same file may be used in separate branches. A file repeated within one reference chain is a cycle, and the error
lists the chain. Files are not cached between positions. A nesting limit also catches cases the chain check cannot
identify, such as different symlink paths to the same file.

`Tree.Files` lists the files read, in read order.

## Overrides

`Overrides` is a map from delimited key path to value, written over the settings once the tree has been rendered:

```go
settings := tree.Settings(l, config.Overrides{
	"logLevel":        "warn",
	"areas.libs.path": "elsewhere",
})
```

Overrides match keys regardless of case and replace the existing value. For example, a `loglevel` override replaces
`logLevel`; it does not create a second key. This is useful for applying command-line flags, including flags that
set nested values.

Rendering also removes empty objects and expands delimited key paths. A bare `{}` therefore does not enable an
optional block with its defaults, and a key containing the delimiter is treated as a nested path.

## Environment binding

Binding process environment variables onto configuration keys is opt-in, and a binding names the keys it will accept:

```go
ov, err := config.EnvBinding{
	Prefix: "APP_",
	Keys:   []string{"logLevel", "areas.libs.path"},
	Strict: true,
}.Overrides(ctx)

settings := tree.Settings(l, config.MergeOverrides(ov, flagOverrides))
```

Each declared key determines its environment variable name. The loader does not infer keys from variable names,
where `LOG_LEVEL` could mean either `log.level` or `logLevel`.

Set `Strict` to reject prefixed variables that do not match a declared key. This catches misspelled deployment
settings when the application starts.

## Editing a file back

The edit API updates a key in its source file. JSON edits preserve every byte outside the replaced span. YAML edits
preserve comments but may change layout. TOML editing returns `ErrTOMLEdit`; use `RenderKeyTOML` to produce a snippet
the caller can paste instead.

```go
file, keyPath, err := l.ResolveEdit(ctx, path, []string{"areas", "libs", "path"})
err = config.ApplyEdits(ctx, file, []config.Edit{{KeyPath: keyPath, Value: []string{"pkgs"}}})
```

`ResolveEdit` follows the same references the loader did, so a configuration split across files is written where each
key is written and the reference survives the write. The previous bytes are saved beside the file with `BackupSuffix`,
and both writes are atomic. A read follows a filesystem symlink, but an edit refuses a symlinked target before it saves
the backup, rather than replacing the link with a regular file; pass the path of the file the link points to.

## Logging

Logs show where configuration came from: directories searched, files loaded through `$ref`, overrides applied, and
the source key changed by an edit. Use them when a setting comes from an unexpected file.

The two-method `Logger` interface does not require a particular logging library. Set `Options.Logger` for a shared
logger, or use `config.WithLogger` to attach one to a context. Events check `Enabled` before being emitted, and `Field`
constructors keep scalar values typed.

## Watching for changes

`config/watch` reloads when the files a configuration was read from change:

```go
w, err := watch.Start(ctx, watch.Options[Config]{
	Load:     load, // returns the value and the files it came from
	OnUpdate: func(cfg Config) { current.Store(&cfg) },
})
defer w.Close()
```

The watcher monitors directories so it can detect files replaced by an atomic save. It updates its watch list from
the files reported by each load, including `$ref` fragments. A failed reload keeps the last good configuration.

Only `config/watch` depends on fsnotify. Programs that load configuration once at startup, including dispat, do not
need it. This also keeps the dependency out of dispat's TinyGo build.

## Performance

`bench_test.go` measures each loading stage with in-memory fixtures supplied through `Options.ReadFile`, excluding
filesystem time. `alloc_test.go` checks allocation counts to catch regressions such as an extra deep copy of the tree.

See [benchmarks](../internals/benchmarks.mdx) for results generated by the benchmark runs and the available fuzz targets.

## Requirements

Go 1.25 or later. The dependencies are `gopkg.in/yaml.v3` and `github.com/pelletier/go-toml/v2`, for the two formats
the standard library does not parse; `github.com/fsnotify/fsnotify` is reached only through `config/watch`.
