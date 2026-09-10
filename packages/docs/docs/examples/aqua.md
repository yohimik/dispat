# Aqua tool pins

An Aqua manifest describes tools a repository consumes. It is not a publication manifest for those tools. dispat can
scan and rewrite its literal version pins while Aqua continues to select release assets, verify checksums and install
the tools.

## A manifest split into imports

Large repositories often keep the registry and checksum policy in one file and one pin per imported file:

```text
aqua/aqua.yaml
aqua/imports/goreleaser.yaml
aqua/imports/cosign.yaml
```

```yaml title="aqua/aqua.yaml"
checksum:
  enabled: true
  require_checksum: true
registries:
  - type: standard
    ref: v4.558.1
import_dir: imports
```

```yaml title="aqua/imports/goreleaser.yaml"
packages:
  - name: goreleaser/goreleaser@v2.18.1
```

This is the shape used by [tfcmt](https://github.com/suzuki-shunsuke/tfcmt/blob/main/aqua/aqua.yaml). Its imported
files carry exact tool pins, while the root file owns the standard-registry version and checksum requirement.

## Inspect and update the pin

The scanner follows `import` and `import_dir` paths that remain inside the scan root:

```console
$ dispat scanner .
aqua/aqua.yaml  aqua
aqua/imports/goreleaser.yaml  aqua
  dependencies  goreleaser/goreleaser  v2.18.1
2 manifest(s), 1 dependency declaration(s)
```

An imported file may have any filename. Tell the writer its format when the name alone does not identify it:

```sh
dispat writer aqua/imports/goreleaser.yaml \
  --manifest-format aqua \
  --set goreleaser/goreleaser=v2.19.0
```

The writer retains the existing `v` prefix, comments, quotes and key order. It updates either `name@version` or a
separate `version` field. It reports `version_expr` and `go_version_file` entries as skipped because evaluating those
values belongs to Aqua.

## Connect a local tool provider

If this repository also releases the pinned tool, give its dispat package the Aqua identity.
For dependency inference, put the pin inside a discovered consumer package, such as `packages/web`:

```json title="dispat.json"
{
  "scripts": {
    "build": "./scripts/build",
    "publish": "./scripts/publish"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {"build": "build", "publish": "publish"},
      "autoVersion": {"enabled": true},
      "packages": {
        "cli": {"manifestNames": ["acme/cli"], "isBuildWaitingPublish": true}
      }
    }
  }
}
```

```yaml title="packages/web/.aqua/aqua.yaml"
packages:
  - name: acme/cli@v1.2.0
```

With `packages/cli` and `packages/web` present, this consumer pin lets `dispat compute` propose the `web -> cli`
dependency edge. Review and save that graph before releasing. The earlier repository-root `aqua/aqua.yaml` example
is independently scannable and writable, but lies outside these package directories and does not establish a consumer
edge for this configuration. The provider
waiting flag is appropriate only when the consumer installs the new tool through a registry after the provider
publishes. Leave it out when the build uses a local binary or another shared build context.

## Verify the installation boundary

Before the provider publication is complete, check that the selected Aqua registry entry resolves the new version and
that the upstream release has an asset and checksum for every required operating system and architecture. The
[`tfcmt` 4.14.19 release](https://github.com/suzuki-shunsuke/tfcmt/releases/tag/v4.14.19), for example, contains
macOS, Linux and Windows archives for amd64 and arm64 plus a checksum file.

dispat does not fetch the Aqua registry, install tools, or update `.aqua-checksums.json`. It also does not rewrite an
`aqua-registry` package definition: that schema describes how Aqua finds upstream artifacts, while this page concerns
the consuming repository's `aqua.yaml`. A registry update, a GitHub release and a consumer pin are therefore separate
destinations. Record each one explicitly, and reconcile the registry before retrying when publication may already
have succeeded.

## See also

- [Manifest tools](../editing/manifests.md#aqua) lists every accepted Aqua filename and safe YAML shape.
- [A Docker image chain](./docker.md) explains the same provider-publication boundary for registry-backed images.
- [Integrating an existing release pipeline](./release-integration.md) covers per-destination completion and recovery.
