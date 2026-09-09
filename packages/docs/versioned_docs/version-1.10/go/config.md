# config: the configuration loader

The `github.com/yohimik/dispat/pkg/config` package is the loader dispat reads its own configuration with, published
on its own. It parses JSON, YAML and TOML into one tree, composes that tree from several files through a `$ref` key,
finds the file a command was run beneath by walking up the directory tree, and turns the result into your structs
through a table of setters rather than through reflection.

```sh
go get github.com/yohimik/dispat/pkg/config
```

There is no reflection in the source at all. That is what makes the config surface of a struct something you read
rather than infer, and it is what lets the package link under TinyGo.

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

`Options{}` is the whole default configuration: every field has a valid zero value, so changing one thing is one
field. `RefKey` names the key that makes an object a reference, `MaxRefDepth` caps how far references nest,
`KeyDelim` is the separator a nested key path is spelled with, `ReadFile` is the one door every path this package
opens goes through, `Formats` maps a file extension to its parser, and `Logger` receives the package's events.

## The fields table

A struct's whole config surface is one table, written once:

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

The table is keyed in lower case. A file spells a key however it likes and the decode folds it to find the setter, so
`logLevel` and `loglevel` both load, and the key itself is never renamed. A key with no entry in the table is a key
your model has no field for, and that is the unknown-key refusal: **structural rather than a check somebody has to
remember to switch on**. The error names the key by its full path from the root, because a typo the loader accepts is
configuration that silently never applies.

The object rules live in `DecodeObject` and nowhere else. The value has to be an object; no two of its keys may fold
together; its keys are visited in sorted order, so a file with several mistakes always reports the same one first; and
a key holding nothing is a key that said nothing rather than a typo.

| Setter | Fills | Shorthands it takes |
|--------|-------|---------------------|
| `String`, `Int`, `Bool`, `BoolPtr` | a scalar field | any format's spelling of the value |
| `Strings`, `Ints` | a list | a scalar is the one-element list; a comma-separated string is the list |
| `StringMap`, `MapOf` | a map of names to values | keys keep the case the file wrote |
| `RawMap` | a free-form block | nothing inside is a key the model has to know |
| `Object` | an optional sub-object | allocated only when the file wrote one |
| `ObjectMap`, `ObjectList` | named entries, a list of objects | a lone object is the one-element list |

A setter is a `func(val any, at string) error`, so a key whose shape is your own is a closure you write. `MapOf` is
the setter to write a setter with: it carries the object rules and takes your reader for each value.

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

What a found file means is yours to decide, because a sub-folder may carry a configuration file of its own. `Classify`
places each file as a root, a candidate or a fallback. The ascent stops at the first root, remembers the first of each
of the others, and lets a root displace a remembered candidate only when `Owns` says the root claims the folder the
candidate was found in. A file that cannot be read stops the ascent as well, because a broken root configuration must
fail where configuration is loaded rather than be silently stepped over.

## Composing with `$ref`

An object holding `$ref` is replaced by the file it names, resolved against the directory of the file that wrote it:

```yaml
areas:
  libs: {$ref: ./shared/area.json}
  apps: {$ref: ./shared/area.json, versioning: independent}
  both: {$ref: [./base.json, ./overlay.json]}
```

A referenced document becomes the value whole, whether it is an object, a list or a single value. Keys written beside
the reference override what it brought in, in their own spelling. A reference naming several files reads them in order
and merges: objects key by key with the later file winning, lists end to end, with files that disagree about what they
hold refused rather than guessed at.

A file is never cached between positions, which is what makes a file appearing twice in one chain, and only that, a
cycle; the error names every hop that closed it. Nesting is capped as well, which catches the loops the chain check
cannot see, such as two names for one file through a symlink.

`Tree.Files` is every file the tree was read from, in the order it was read.

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
passed correctly. This is the generic form of a command-line flag overlay, and it reaches nested paths.

Rendering the settings is also where two of the config language's quieter rules live: an object with no keys is not a
key at all, so an opt-in block written as a bare `{}` says nothing rather than enabling itself at its defaults; and a
key spelled with the delimiter is the levels it names.

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

The derivation runs one way, from a declared key to a variable name, and never the other. Splitting a variable name
back into key levels is where an automatic binding has to guess whether `LOG_LEVEL` is `log.level` or `logLevel`, and
the guess is wrong for somebody. `Strict` makes a prefixed variable that no declared key answers to an error, which
turns a typo in a deployment manifest into a failure at startup rather than a setting that silently never applied.

## Editing a file back

The writers put one key back without disturbing the rest of the file. JSON is spliced, so every byte outside the
replaced span survives. YAML goes through a node tree that carries its comments, so it keeps them but may reflow. TOML
returns `ErrTOMLEdit`, for you to render a paste-ready snippet with `RenderKeyTOML` instead.

```go
file, keyPath, err := l.ResolveEdit(ctx, path, []string{"areas", "libs", "path"})
err = config.ApplyEdits(ctx, file, []config.Edit{{KeyPath: keyPath, Value: []string{"pkgs"}}})
```

`ResolveEdit` follows the same references the loader did, so a configuration split across files is written where each
key is written and the reference survives the write. The previous bytes are saved beside the file with `BackupSuffix`,
and both writes are atomic.

## Logging

The events are what someone reads when a loader has gone quiet and then, one day, loaded the wrong file: which
directories the ascent tried and what it made of each, which files a `$ref` pulled in, how many overrides landed,
which key an edit was written to.

The `Logger` interface is two methods and no dependency, because a library that picks a logging package picks it for
every program that imports it. Set `Options.Logger` for a program with one logger, or put one on the context with
`config.WithLogger` for a program with several. Every emit is guarded by `Enabled`, and the `Field` constructors keep
scalars in typed slots, so an event nobody asked for costs an interface call and a comparison.

## Watching for changes

`config/watch` reloads when the files a configuration was read from change:

```go
w, err := watch.Start(ctx, watch.Options[Config]{
	Load:     load, // returns the value and the files it came from
	OnUpdate: func(cfg Config) { current.Store(&cfg) },
})
defer w.Close()
```

It watches directories rather than files, because a config file is usually replaced rather than written in place and a
watch on the file itself follows the old inode into the void. The watch set is derived from the files each load
reports, so a configuration composed through `$ref` watches every fragment. A reload that fails keeps the last good
value.

`watch` is a subpackage on purpose: it is the only part of this module that needs fsnotify, so a program that reads
its configuration once at startup never links it. dispat is one of those programs, which is what keeps its TinyGo
build clear of it.

## Performance

The package is alloc-budgeted rather than merely fast. `bench_test.go` measures every stage against fixtures built in
code and served through `Options.ReadFile`, so a run measures the loader and not the filesystem underneath it, and
`alloc_test.go` pins the allocation counts as a regression gate: a change that reintroduces a second deep copy of the
tree, or a flat intermediate map in the settings rendering, fails the build rather than being noticed later.

The numbers each release measured are on the [benchmarks](../internals/benchmarks.mdx) page, alongside the fuzz
targets. They are injected from the run that produced them rather than written by hand, so a number on that page is a
number that was measured.

## Requirements

Go 1.25 or later. The dependencies are `gopkg.in/yaml.v3` and `github.com/pelletier/go-toml/v2`, for the two formats
the standard library does not parse; `github.com/fsnotify/fsnotify` is reached only through `config/watch`.
