# models

The public configuration model of the dispat CLI: the typed structs a `dispat.json` / `dispat.yaml` /
`dispat.toml` decodes into, published so external tooling (generators, migration scripts, the black-box integration
suite) can author configurations as typed values and marshal them to loadable files instead of hand-writing raw config
strings.

```go
cfg := models.File{
Scripts: map[string]models.Script{
"build":   {"npm run build"},             // one command
"release": {"npm ci", "npm publish"},     // or a sequence, run in order
},
Spaces: map[string]models.SpaceConfig{
"libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
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

The contract: every field carries a `mapstructure` tag (how the CLI decodes the file) and a `json` tag with the same key
(how a model marshals back into a loadable file), so **a marshalled model is a loadable config**. Optional sub-objects
are pointers, so an unset object marshals as an absent key rather than `{}` noise. Tri-state options are
`*bool` with nil-safe accessors and a `Bool()` helper; that covers `enabled`, `verify`, `writeVersion`, and every scalar
of a `PackageConfig` override, where absent must mean "inherit".

A `Packages` entry plays one of two roles: without `Path` it overrides the space configuration of the package whose
folder name matches the key; with `Path` it declares a standalone package outside every space. The model always holds
dependency edges as the flat `[]DependencyConfig` list, and the expansion lives here too: `Dependencies` and
`ProviderList` unmarshal every shorthand the config file accepts (a bare provider name, a consumer-keyed map, a full
edge object) into that flat list, and marshal back the shortest spelling that says the same thing. `Providers` builds
the list in code the way the shorthand does in a file.

A `Scripts` value is a `Script`: the commands one name binds, in the order they run. It decodes from either shape the
config file accepts (a bare string or an array of them) and marshals back as the shortest one that carries what the
script says, so a single-command script written as a string is written back as a string.

This module contains models only. Loading, validation, defaulting and package discovery live in the CLI's internal
config package: an invalid model marshals fine and fails with a clear error when the CLI loads it.

## Requirements

Go 1.22 or later (the workspace itself builds with the Go version `go.work` declares). One dependency: the workspace's
own [`pkg/ccme`](../ccme) (for the resolved parser configuration type).

## Licence

MIT. See [LICENSE](../../LICENSE.md).
