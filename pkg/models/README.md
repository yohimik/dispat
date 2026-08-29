# models

This package provides the public configuration model for the dispat CLI. It contains the typed structs that
`dispat.json`, `dispat.yaml`, and `dispat.toml` decode into. External tools like generators, migration scripts, and
test suites can build configurations as typed Go values and marshal them directly to valid config files.

```go
cfg := models.File{
Scripts: map[string]models.Script{
"build":   {"npm run build"},             // one command
"release": {"npm ci", "npm publish"},     // or a sequence, run in order
},
Spaces: map[string]models.SpaceConfig{
"libs": {Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{
Build: []string{"build"}, Publish: []string{"publish"},
}},
},
Packages: map[string]models.PackageConfig{
"core": {RevertOnFail: models.Bool(false)}, // override for a space package
"cli":  {Path: "tools/cli", Dependencies: models.Providers("core")}, // standalone package
},
}
data, _ := json.MarshalIndent(cfg, "", "  ") // a loadable dispat.json
```

Every field carries one `json` tag, naming both the key the config file is decoded by and the key the model is encoded
back into, so **a marshalled model is a loadable config**. Optional sub-objects use pointers so unset fields are
omitted from output instead of emitting empty `{}` blocks. Tri-state options use `*bool` with nil-safe accessors and a
`Bool()` helper, which covers `enabled`, `verify`, `writeVersion`, and every scalar in a `PackageConfig` override where
absent means "inherit".

A `Packages` entry plays one of two roles: without `Path` it overrides the space configuration for the matching package
folder, and with `Path` it declares a standalone package outside every space. The model stores dependency edges as a
flat `[]DependencyConfig` slice. `Dependencies` and `ProviderList` unmarshal config shorthands (bare names, consumer
maps, full edge objects) into that list and marshal back the shortest equivalent form, while `Providers` lets you build
that list in code.

A `Scripts` value is a `Script`, representing the commands bound to a name in execution order. It decodes from a single
string or an array of strings. When marshalled, it outputs the shortest form, writing single-command scripts back as
bare strings.

This module contains data models only. Loading, validation, defaulting, and package discovery live in the CLI internal
config package. An invalid model will marshal without errors, but the CLI will reject it when loading.

## Requirements

You need Go 1.22 or later (the workspace builds with the version declared in `go.work`). The package has one
dependency: [`pkg/ccme`](../ccme), used for resolved parser configuration types.

## Licence

MIT. See [LICENSE](./LICENSE).

