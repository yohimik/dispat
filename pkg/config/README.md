# config

The `config` package reads configuration files the way a hand-written file wants to be read. It parses JSON, YAML and
TOML into one tree, composes that tree from several files through a `$ref` key, finds the file a command was run
beneath by walking up the directory tree, and turns the result into your own structs through a table of setters rather
than through reflection.

There is no reflection in the source at all. A decode is a map from key to closure, which is what makes the config
surface of a struct a thing you can read, and what lets the package link under TinyGo.

This package powers `dispat`'s own configuration: the `$ref` composition, the case-preserving keys, the unknown-key
refusal and the ref-aware editing are all in production there.

## Install

```sh
go get github.com/yohimik/dispat/pkg/config
```

Go 1.25 or later.

## Use

```go
l := config.NewLoader(config.Options{})

tree, err := l.ReadTree(ctx, "app.yaml")   // parse, follow every $ref
if err != nil {
    // err names the file, the key path inside it, and what was wrong there.
}

var cfg Config
err = config.DecodeObject(tree.Settings(l, nil), "", configFields(&cfg))
```

`configFields` is the whole config surface of `Config`, written once:

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

The table is keyed in lower case; a file spells a key however it likes and the decode folds it to find the setter, so
`logLevel` and `loglevel` both load. A key with no entry in the table is a key the model has no field for, and that is
the unknown-key refusal. This is structural rather than a check somebody has to remember to run.

## Resolving

`Loader.Resolve` walks up from a directory looking for the caller's file names, which is what lets a command run from
inside a sub-folder and still load the project's configuration.

```go
path, root, err := l.Resolve(ctx, cwd, config.Resolver{
    Names:    []string{"app.json", "app.yaml", "app.toml"},
    Classify: config.MarkerClassify([]string{"areas"}, []string{"tasks"}),
    Owns:     config.FolderOwner("areas", "path"),
})
```

What a found file means is the caller's to decide, because a sub-folder may carry a configuration file of its own.
`Classify` places each file as a root, a candidate or a fallback; the ascent stops at the first root, remembers the
first of each of the others, and lets a root displace a remembered candidate only when `Owns` says the root claims the
folder that candidate was found in. A file that cannot be read stops the ascent too: a broken root config must fail
where configuration is loaded, not be silently stepped over.

## Composing with `$ref`

An object holding `$ref` is replaced by the file it names, resolved against the directory of the file that wrote it:

```yaml
areas:
  libs:  {$ref: ./shared/area.json}
  apps:  {$ref: ./shared/area.json, versioning: independent}
  both:  {$ref: [./base.json, ./overlay.json]}
```

- A referenced document becomes the value whole: an object, a list or a single value.
- Keys written beside the reference override what it brought in, in their own spelling.
- A reference naming several files reads them in order and merges: objects key by key with the later file winning,
  lists end to end. Files that disagree about what they hold are refused rather than guessed at.
- A file is never cached between positions, which is what makes a file appearing twice in one chain, and only that,
  a cycle. The error names every hop that closed it.
- Nesting is capped (`Options.MaxRefDepth`, 32 by default), which catches the loops the chain check cannot see, such
  as two names for one file through a symlink.

`Tree.Files` is every file the tree was read from, in the order it was read. That is what a watch set is derived from,
and what a program prints when someone asks where a setting came from.

## Decoding

`DecodeObject` is the object rules and nothing else: the value has to be an object, no two of its keys may fold
together, its keys are visited in sorted order so the first mistake reported is always the same one, a key the table
does not hold is unknown, and a key holding nothing says nothing.

The setters are the shapes a key can hold:

| Setter | Fills | Shorthands |
|---|---|---|
| `String`, `Int`, `Bool`, `BoolPtr` | a scalar field | any format's spelling of the value |
| `Strings`, `Ints` | a list | a scalar is the one-element list; a comma-separated string is the list |
| `StringMap`, `MapOf[T]` | a map of names to values | keys keep the case the file wrote |
| `RawMap` | a free-form block | nothing inside is a key the model has to know |
| `Object[T]` | an optional sub-object | allocated only when the file wrote one |
| `ObjectMap[T]`, `ObjectList[T]` | named entries, a list of objects | a lone object is the one-element list |

A setter is a `func(val any, at string) error`, so a key whose shape is your own is a closure you write. `MapOf` is the
setter to write a setter with: it carries the object rules and takes your reader for each value, which is how a map of
something the library has no shape for still gets them.

The comma shorthand lives in `Strings` and `Ints` and nowhere else, which is what keeps a comma inside a shell command
the character the file wrote. This avoids the mistake a reflected decoder makes when the hook that lifts a scalar into a list
fires on a Go type and cannot see the key that produced it.

## Overrides

`Overrides` is a map from delimited key path to value, written over the settings once the tree has been rendered:

```go
settings := tree.Settings(l, config.Overrides{
    "logLevel":        "warn",
    "areas.libs.path": "elsewhere",
})
```

The value replaces whatever spelling the file used rather than sitting beside it. A file writing `logLevel` and an
override writing `loglevel` would otherwise be two keys the decode refuses as a collision, over a value the operator
passed correctly. This is the generic form of a flag overlay, and it works at nested paths.

## Environment binding

Opt-in and closed: a binding names the keys it will accept.

```go
ov, err := config.EnvBinding{
    Prefix: "APP_",
    Keys:   []string{"logLevel", "areas.libs.path"},
    Strict: true,
}.Overrides(ctx)

settings := tree.Settings(l, config.MergeOverrides(ov, flagOverrides))
```

The derivation runs one way, from a declared key to a variable name, and never the other. Splitting a variable name
back into key levels is where an automatic binding has to guess whether `LOG_LEVEL` is `log.level` or `logLevel`, and
the guess is wrong for somebody. `Strict` makes a prefixed variable that no declared key answers to an error, which
turns a typo in a deployment manifest into a failure at startup rather than a setting that silently never applied.

## Editing

The writers put one key back without disturbing the rest of the file. JSON is spliced, so every byte outside the
replaced span survives; YAML goes through a node tree that carries its comments, so it keeps them but may reflow; TOML
returns `ErrTOMLEdit` for a caller to render a paste-ready snippet with `RenderKeyTOML`.

```go
file, keyPath, err := l.ResolveEdit(ctx, path, []string{"areas", "libs", "path"})
err = config.ApplyEdits(ctx, file, []config.Edit{{KeyPath: keyPath, Value: []string{"pkgs"}}})
```

`ResolveEdit` follows the same references the loader did, so a configuration split across files is written where each
key is written and the reference itself survives the write. The previous bytes are saved beside the file with
`BackupSuffix`, and both writes are atomic through a temporary file, fsync, and rename.

## Logging

The events are what someone reads when a loader has gone quiet and then, one day, loaded the wrong file: which
directories the ascent tried and what it made of each, which files a `$ref` pulled in, how many overrides landed, which
key an edit was written to.

```go
type adapter struct{ log *slog.Logger }

func (a adapter) Enabled(l config.Level) bool { return a.log.Enabled(ctx, level(l)) }
func (a adapter) Log(l config.Level, event string, fields ...config.Field) { /* ... */ }

l := config.NewLoader(config.Options{Logger: adapter{log}})
```

Two methods and no dependency, because a library that picks a logging package picks it for every program that imports
it. `WithLogger(ctx, …)` puts one on the context for the programs that have several. Every emit is guarded by
`Enabled`, and the `Field` constructors keep scalars in typed slots, so an event nobody asked for costs an interface
call and a comparison.

## Watching

`config/watch` reloads when the files a configuration was read from change.

```go
w, err := watch.Start(ctx, watch.Options[Config]{
    Load:     load,          // returns the value and the files it came from
    OnUpdate: func(cfg Config) { current.Store(&cfg) },
})
defer w.Close()
```

It watches directories, not files: a config file is usually replaced rather than written in place, and a watch on the
file itself follows the old inode into the void. The watch set is derived from the files each load reports, so a
configuration composed through `$ref` watches every fragment, and a reload that changes which fragments are involved
moves the watches with it. A reload that fails keeps the last good value.

## Performance

The package is alloc-budgeted rather than merely fast. `bench_test.go` measures every stage against fixtures built in
code and served through `Options.ReadFile`, so a run measures the loader and not the filesystem underneath it; the two
benchmarks that cannot be deterministic, including the directory ascent and file writers, use a temporary tree
and say so. Benchmarks run with `-run '^$'`, because a pass that runs the tests alongside them is timing a machine that
was busy doing something else.

`alloc_test.go` is what turns those measurements into a gate. It pins the allocation counts as tests, with one
allocation of headroom so a toolchain change does not fail a build for a rounding difference, and a reintroduced deep
copy of the tree, or a flat intermediate map in the settings rendering, fails it immediately. An allocation count is
a property of the code rather than of the machine, which is what makes it worth pinning at all.

There are no figures in this file on purpose. The numbers each release measured are on the
[benchmarks page](https://dispat.dev/internals/benchmarks/), injected there from the run that took them: a timing is a
fact about a machine and a toolchain, and a number pasted into a document goes stale the day after it is written while
still reading like a promise.

```sh
go test ./... -run '^$' -bench . -benchmem   # what the release measures
go test ./...                                # the budgets, as ordinary tests
```

## Compared to viper and koanf

Named here as the two libraries most Go programs reach for, and only to say what is different. Neither is a dependency
of this one.

- **`$ref` composition, and edits that follow it.** A configuration split across files loads as one document and is
  written back to the file that holds each key.
- **Keys keep their written case.** A lower-casing loader cannot carry an env layer, because `PATH` and `Path` are two
  variables. Names are matched case-insensitively at the lookup, where the two spellings actually meet.
- **Two spellings of one name in one object are refused.** Which of them a lookup found would otherwise be whatever the
  map iteration handed over.
- **Unknown keys are refused structurally.** There is no `ErrorUnused` to remember to switch on: a key with no setter
  has nowhere to go.
- **The first error is always the same error.** Keys are visited sorted, so a config with several mistakes fails the
  same way on every run and on every machine.
- **No reflection, and no `Get(key)`.** The config surface is a table you read; the result is your struct, typed. There
  is no dynamic lookup by string path, which is the feature this package deliberately does not have.
- **An empty object says nothing.** `autoVersion: {}` leaves an optional block nil rather than enabling it at its
  defaults, which is what lets a pointer sub-object mean "this layer did not speak".

## Not here today

- No struct-tag table generator. A `go:generate` tool that writes a `Fields` table from tags would remove the one piece
  of duplication the design has; the tables are hand-written for now.
- No remote providers such as etcd, Consul, or S3. `Options.ReadFile` is the seam a caller reaches through, and a provider
  belongs in a module of its own rather than in this one's dependency list.
- No pflag adapter. `Overrides` is the shape a flag overlay takes; building one from a `*pflag.FlagSet` is a dozen
  lines a caller writes, and is not worth a dependency here.
- No dotenv, INI or HCL. `Options.Formats` takes a parser for any extension, so these are a caller's to register.
- No defaults or alias registration. Defaults belong to the struct the decode fills, before or after it runs; an alias
  is a second setter pointing at the same field. Both are non-goals rather than gaps.

## Dependencies

`gopkg.in/yaml.v3` and `github.com/pelletier/go-toml/v2`, for the two formats the standard library does not parse.
`github.com/fsnotify/fsnotify` is used only by `config/watch`, so a program that does not reload never links it.

## Licence

MIT. See [LICENSE](./LICENSE).
