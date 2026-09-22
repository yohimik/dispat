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

A peer of a fleet with no control repository states its identity and, when it has peers, their roster:

```go
cfg := models.File{
	Repository: "api",
	Repositories: []models.RepositoryLinkConfig{
		{Name: "sdk", URL: "git@github.com:acme/sdk.git", Branch: "main"},
		{Name: "web", URL: "git@github.com:acme/web.git", Path: "vendor/web", Branch: "main"},
	},
}
```

Non-empty `Repository` activates linked-peer composition. `Repositories` optionally lists the other peers;
an empty list is valid for a one-member fleet. `RepositoryLinkConfig.Path` is where a link to that peer lives inside this repository and
defaults to `.links/<name>`; `Branch` is the peer's release branch, which a created link follows and a recorded pin is
verified against. See [A choreographed fleet](../choreographed-repositories.md) for the runtime rules.

## A node that serves tasks, and where a stage runs

`File.Execution` is the [`execution`](../configuration/execution.md) object of a node taking part in
[distributed execution](../distributed-execution.md). It stays `nil` when the key is absent, which is what keeps a
configuration that never mentions it exactly as it was:

```go
cfg := models.File{
	Execution: &models.ExecutionConfig{
		Role:      models.ExecutionRoleOrchestrator,
		SecretEnv: "DISPAT_EXECUTION_SECRET",
		Workers: []models.ExecutionWorkerConfig{
			{Name: "build-a", Endpoint: "git@github.com:acme/release-mailbox.git"},
		},
		Timeouts: &models.ExecutionTimeoutsConfig{Preflight: 600},
	},
	BuildOutputs: []string{"dist"},
	Packages: map[string]models.PackageConfig{
		"signer": {RunOnly: &models.RunOnly{Build: models.RunOnlyOrchestrator, Publish: models.RunOnlyOrchestrator}},
	},
}
```

`ExecutionConfig`, `ExecutionWorkerConfig`, `ExecutionTimeoutsConfig` and `ExecutionTransferConfig` all read through
nil-safe resolvers, so a caller asks one question whether or not the object or the key is there: `ResolveRole`,
`IsWorker`, `IsDistributed`, `ResolveConcurrency`, `ResolveTimeouts` and `ResolveTransfer`. Every unstated bound comes
back as the documented default (`DefaultExecutionConcurrency`, `DefaultExecutionTaskTimeout` and the rest), and a
stated value comes back as written, including one the loader will refuse, so a refusal names what the file said.

`RunOnly` is a pair with a `Build` and a `Publish` placement, and it marshals as the shortest spelling that carries the
same meaning: one word when the stages agree, a `[build, publish]` list when they differ. `NormalizeRunOnly` expands
either spelling and is the single reader behind both the model and the CLI. `BuildOutputs` and `BuildPlatforms` are
plain string lists on `File`, `SpaceConfig`, `SpaceFile`, `PackageConfig` and a package folder's own file, and each
replaces the inherited list whole.

`IsBuildWaitingPublish` is a `*StageRelation` rather than a `*bool`: the relation carries what a consumer's build waits
for (`StageWaitNone`, `StageWaitBuild`, `StageWaitPublish`) and whether a failed provider blocks its consumers.
`StageRelationOf(true)` and `StageRelationOf(false)` build the two boolean spellings, which marshal back as booleans.
See [The provider relation](../configuration/spaces.md#the-provider-relation).

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
