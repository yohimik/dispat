# models: the configuration model

`github.com/yohimik/dispat/pkg/models` holds the typed structs a `dispat.json`, `dispat.yaml` or `dispat.toml` decodes
into. It is published so that generators, migration scripts and test suites can author configurations as typed values
and marshal them to loadable files instead of assembling config text by hand.

```sh
go get github.com/yohimik/dispat/pkg/models
```

## Writing a configuration in Go

```go
cfg := models.File{
	Scripts: map[string]models.Script{
		"build":   {"npm run build"},         // one command
		"release": {"npm ci", "npm publish"}, // or a sequence, run in order
	},
	Spaces: map[string]models.SpaceConfig{
		"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
			Build: []string{"build"}, Publish: []string{"publish"},
		}},
	},
	Packages: map[string]models.PackageConfig{
		"core": {RevertOnFail: models.Bool(false)},
		"cli":  {Path: "tools/cli", Dependencies: models.Providers("core")},
	},
}
data, _ := json.MarshalIndent(cfg, "", "  ") // a loadable dispat.json
```

## The contract

Every field carries a `mapstructure` tag, which is how the CLI decodes the file, and a `json` tag with the same key,
which is how a model marshals back. The consequence is the point of the package: **a marshalled model is a loadable
configuration**, so a generator can round-trip through Go types without a hand-written template.

Optional sub-objects are pointers, so an unset object marshals as an absent key rather than as `{}` noise.

Tri-state options are `*bool` with nil-safe accessors and a `Bool()` helper, which covers `enabled`, `verify`,
`writeVersion` and every scalar of a package override, where absent has to mean inherit rather than false.

## Shapes the config file accepts

Two keys accept several spellings in a file, and the expansion lives here rather than in the CLI.

Dependency edges are always held as the flat `[]DependencyConfig` list. `Dependencies` and `ProviderList` unmarshal
every shorthand the file accepts, meaning a bare provider name, a consumer-keyed map or a full edge object, into that
flat list, and marshal back the shortest spelling that says the same thing. `Providers` builds the list in code the way
the shorthand does in a file.

A `Scripts` value is a `Script`: the commands one name binds, in the order they run. It decodes from either a bare
string or an array of them, and marshals back as the shorter of the two whenever that carries the same meaning.

## Two roles for a package entry

A `Packages` entry means one of two things, decided by whether it sets `Path`. Without `Path` it overrides the space
configuration of the package whose folder name matches the key. With `Path` it declares a standalone package outside
every space. Both forms appear in the example above, and both are described in
[Packages](../configuration/packages.md).

## What is not here

This module contains models only. Loading, validation, defaulting and package discovery live inside the CLI, so an
invalid model marshals perfectly well and fails with a clear error when dispat loads it. Anything that behaves
differently at runtime than the struct suggests is documented in the
[Configuration reference](../configuration/README.md), which describes the file as the CLI actually reads it.

## Further reading

- [Configuration reference](../configuration/README.md) documents every key these structs carry.
- The full API is on
  [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/models) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/models).
