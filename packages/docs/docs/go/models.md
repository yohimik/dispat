# models: the configuration model

The `github.com/yohimik/dispat/pkg/models` package holds the typed structs that a `dispat.json`, `dispat.yaml`, or
`dispat.toml` file decodes into. You can use it to write generators, migration scripts, and test suites. It lets you
author configurations as typed Go values and marshal them to loadable files, so you avoid assembling config text by
hand.

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
		"libs": {Path: models.PathList{"packages"}, Flow: &models.SpaceFlowConfig{
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

For a polyrepository control file, the model exposes the configuration contract directly:

```go
cfg := models.File{
	Polyrepo: true,
	Configs:  []string{"services/api/dispat.source.yaml"},
	RepositoryOverrides: map[string]models.RepositoryOverrideConfig{
		"api-source": {Commit: &models.CommitConfig{
			Enabled: models.Bool(true), Push: true, Branch: "main",
		}},
	},
	RepositoryBaselines: []models.RepositoryBaselineConfig{{
		Consumer: "web", ReleaseTag: "web@2.4.0",
		Repository: "api-source", Revision: "6f1a9f0d2b90c8f96a4d74dcb6568fd373b22c16",
	}},
}
```

`RepositoryOverrideConfig.Commit`, when present, replaces the complete inherited commit policy and uses ordinary
`CommitConfig` defaults for omitted fields. Leaving the whole object absent inherits the control policy. The
`DependencyConfig.External` boolean marks a provider that may be absent from the composed workspace; the edge becomes
active normally when the provider is imported. See [A control repository](../control-repository.md#source-history-mode)
for the runtime rules behind these values.

A peer of a fleet with no control repository states the same contract through three fields:

```go
cfg := models.File{
	Saga:       models.SagaChoreography,
	Repository: "api",
	Repositories: []models.RepositoryLinkConfig{
		{Name: "sdk", URL: "git@github.com:acme/sdk.git", Branch: "main"},
		{Name: "web", URL: "git@github.com:acme/web.git", Path: "vendor/web", Branch: "main"},
	},
}
```

`Saga` is `models.SagaOrchestration` or `models.SagaChoreography`, and an empty value is the orchestrated default.
`File.IsChoreographed` is the nil-safe predicate for it, and it compares the value without regard to case, like every
other configured value. `RepositoryLinkConfig.Path` is where a link to that peer lives inside this repository and
defaults to `.links/<name>`; `Branch` is the peer's release branch, which a created link follows and a recorded pin is
verified against. See [A choreographed fleet](../choreographed-repositories.md) for the runtime rules.

## The contract

Every field carries one `json` tag, and that tag is both halves of the contract: the key the CLI decodes the file by,
and the key the model marshals back into. This guarantees that **a marshalled model is a loadable configuration**, so
your generators can round-trip through Go types without needing hand-written templates.

Optional sub-objects are pointers. An unset object marshals as an absent key instead of printing `{}` noise to your
file.

Tri-state options are `*bool` fields with nil-safe accessors and a `Bool()` helper. This covers `enabled`, `verify`,
`writeVersion`, and every scalar of a package override. For these fields, an absent value means inherit instead of
false.

## Shapes the config file accepts

Two keys accept multiple spellings in a config file. dispat expands these spellings here in the models package instead
of in the CLI.

Dependency edges always resolve to a flat `[]DependencyConfig` list. The `Dependencies` and `ProviderList` fields
unmarshal every shorthand the file accepts into that flat list, and they marshal back to the shortest spelling that
carries the same meaning. You can use the `Providers` function to build this list in code exactly how the shorthand
works in a file.

A `Scripts` value is a `Script`. This represents the commands one name binds, in the order they run. It decodes from
either a bare string or an array of strings, and it marshals back as the shorter of the two whenever they mean the same
thing.

## Two roles for a package entry

A `Packages` entry means one of two things, depending on whether you set `Path`. Without `Path`, it overrides the space
configuration of the package whose folder name matches the key, but with `Path`, it declares a standalone package
outside every space. Both forms appear in the Go example above and are described in
[Packages](../configuration/packages.md).

## What is not here

This module contains models only. Loading, validation, defaulting, and package discovery all live inside the CLI, so an
invalid model marshals perfectly well but fails with a clear error when dispat loads it. The
[Configuration reference](../configuration/README.md) documents anything that behaves differently at runtime,
describing the file exactly as the CLI reads it.

## Further reading

- [Configuration reference](../configuration/README.md) documents every key these structs carry.
- Read the full API on [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/models) or view the source
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/models).
