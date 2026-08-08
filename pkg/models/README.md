# models

The public configuration model of the dispat CLI: the typed structs a `dispat.json` / `dispat.yaml` /
`dispat.toml` decodes into, published so external tooling (generators, migration scripts, the black-box
integration suite) can author configurations as typed values and marshal them to loadable files instead of
hand-writing raw config strings.

```go
cfg := models.File{
    Scripts: map[string]string{"build": "npm run build", "publish": "npm publish"},
    Spaces: map[string]models.SpaceConfig{
        "libs": {Path: "packages", Flow: &models.SpaceFlowConfig{
            Build: []string{"build"}, Publish: []string{"publish"},
        }},
    },
}
data, _ := json.MarshalIndent(cfg, "", "  ") // a loadable dispat.json
```

The contract: every field carries a `mapstructure` tag (how the CLI decodes the file) and a `json` tag with
the same key (how a model marshals back into a loadable file), so **a marshalled model is a loadable
config**. Optional sub-objects are pointers, so an unset object marshals as an absent key rather than `{}`
noise; tri-state options (`enabled`, `verify`, `writeVersion`, and every scalar of a `PackageConfig`
override, where absent must mean "inherit") are `*bool` with nil-safe accessors and a `Bool()` helper.

This module contains models only. Loading, validation, defaulting and package discovery live in the CLI's
internal config package: an invalid model marshals fine and fails with a clear error when the CLI loads it.

## Requirements

Go 1.22 or later. One dependency: the workspace's own [`pkg/ccme`](../ccme) (for the resolved parser
configuration type).

## Licence

MIT. See [LICENSE](../../LICENSE.md).
