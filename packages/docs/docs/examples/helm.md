# Helm charts that follow the image

You can configure a chart so the same run that pushes the image also writes the `appVersion` and the image tag. The
chart `version` moves on its own schedule.

A chart carries two version numbers that mean different things. The `version` belongs to the chart, and it changes when
the templates change. The `appVersion` belongs to the application, and it changes when the image updates. Updating
these numbers by hand is bookkeeping that goes wrong quietly.

## The layout

```
services/api/Dockerfile   builds ghcr.io/acme/api
charts/api/Chart.yaml     version (the chart) and appVersion (the image)
charts/api/values.yaml    image.tag
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "build-image": "docker build -t ghcr.io/acme/api:$DISPAT_NEW_VERSION .",
    "push-image": "docker push ghcr.io/acme/api:$DISPAT_NEW_VERSION",
    "package-chart": "helm package . --version $DISPAT_NEW_VERSION",
    "push-chart": "helm push api-$DISPAT_NEW_VERSION.tgz oci://ghcr.io/acme/charts"
  },
  "packages": {
    "api": {
      "path": "services/api",
      "isBuildWaitingPublish": true,
      "flow": {"build": "build-image", "publish": "push-image"}
    },
    "api-chart": {
      "path": "charts/api",
      "flow": {"build": "package-chart", "publish": "push-chart"},
      "dependencies": [{"provider": "api", "keep": true}],
      "autoVersion": {
        "enabled": true,
        "manifests": "none",
        "replace": [
          {"files": ["Chart.yaml"], "find": "version: {previous}", "write": "version: {version}"},
          {"files": ["Chart.yaml"], "find": "appVersion: \"{providerPrevious}\"", "write": "appVersion: \"{providerVersion}\""},
          {"files": ["values.yaml"], "find": "tag: {providerPrevious}", "write": "tag: {providerVersion}"}
        ]
      }
    }
  }
}
```

Three ideas drive this configuration.

**`dependencies` with `keep: true`.** Nothing in the chart files says the chart belongs to the image, so you declare
the edge by hand. The `keep: true` field tells [`dispat compute`](../cli/compute.md) this dependency is deliberate. The
command leaves it alone instead of offering to remove it.

**`isBuildWaitingPublish` on the image.** You set this on the *provider* package. Consumers of that package wait for
the image to be published rather than merely built. A chart naming a missing image tag is useless, so the chart waits.

**The three `replace` rules.** The `Chart.yaml` file is YAML, but it lacks a standard dependency manifest. Literal
find-and-write is the right tool here. You use `{version}` for this package and `{providerVersion}` for the package it
follows.

## A release

```console
$ git commit -m "feat(api)^: paginated search"
$ dispat
12:57:34 INF release started root=.
12:57:34 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=api reason=direct space=api version="1.4.0 -> 1.5.0"
12:57:34 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["api"] dueToProviders=["api"] ownCommits=0 package=api-chart reason="propagated from api" space=api-chart version="0.3.0 -> 0.3.1"
12:57:34 INF release plan ready held=0 packages=2 releasing=2
12:57:34 INF build started package=api stage=build version=1.5.0
12:57:34 INF build succeeded package=api stage=build version=1.5.0
12:57:34 INF publish started package=api stage=publish version=1.5.0
12:57:35 INF published package=api stage=publish tag=api@1.5.0 version=1.5.0
12:57:35 INF file reconciled file=Chart.yaml occurrences=2 package=api-chart stage=version version=0.3.1
12:57:35 INF file reconciled file=values.yaml occurrences=1 package=api-chart stage=version version=0.3.1
12:57:35 INF version succeeded package=api-chart stage=version version=0.3.1
12:57:35 INF build started package=api-chart stage=build version=0.3.1
12:57:35 INF build succeeded package=api-chart stage=build version=0.3.1
12:57:35 INF publish started package=api-chart stage=publish version=0.3.1
12:57:35 INF published package=api-chart stage=publish tag=api-chart@0.3.1 version=0.3.1
12:57:35 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=1.1s unchanged=0
```

Look at the log output. The chart version stage starts only after the image publishes. The `occurrences=2` in
`Chart.yaml` means two rules matched in that file. Expect these changes on disk:

```yaml title="charts/api/Chart.yaml"
apiVersion: v2
name: api
description: The API service
type: application
version: 0.3.1
appVersion: "1.5.0"
```

```yaml title="charts/api/values.yaml"
image:
  repository: ghcr.io/acme/api
  tag: 1.5.0
replicaCount: 2
```

## When the chart changes but the image does not

Commit against the chart to see only the chart move:

```console
$ git commit -m "fix(api-chart): correct the readiness probe path"
$ dispat
12:58:16 INF unchanged channel=stable package=api space=api version=1.5.0
12:58:16 INF ● changed bump=patch channel=stable dependsOn=["api"] dueToProviders=[] ownCommits=1 package=api-chart reason=direct space=api-chart version="0.3.1 -> 0.3.2"
12:58:16 INF file reconciled file=Chart.yaml occurrences=1 package=api-chart stage=version version=0.3.2
12:58:16 INF summary channel=stable package=api-chart status=published tag=api-chart@0.3.2 took=0.4s version="0.3.1 -> 0.3.2"
12:58:16 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=0.4s unchanged=1
```

The `version` becomes `0.3.2`, but `appVersion` stays at `1.5.0`. The `occurrences=1` line shows only the chart-version
rule found anything to change. This separation is why you keep the two numbers distinct.

## Kubernetes manifests without Helm

You can update plain manifests the same way. Point the rule at your deployment file instead:

```json
{
  "replace": [
    {
      "files": ["deploy/*.yaml"],
      "find": "image: ghcr.io/acme/{provider}:{providerPrevious}",
      "write": "image: ghcr.io/acme/{provider}:{providerVersion}"
    }
  ]
}
```

A rule naming `{provider}` applies once per provider, so one rule covers every image the deployment references.

## Worth knowing

- **A rule that matches nothing is reported.** The `W222` warning means dispat found no matching text in any selected
  file. This usually indicates a typo or a stale glob. Re-running a release suppresses the warning, because dispat
  checks whether the file already reads the way the rule wants.
- **Chart versions must be semver.** Helm rejects anything else. This matches exactly what dispat computes.
- **`helm package --version` and the file must agree.** The version stage writes `Chart.yaml` before the build runs.
  Passing `$DISPAT_NEW_VERSION` on the command line acts as a safety measure rather than a second source of truth.
- **A chart repository is append-only in practice.** Overwriting a published chart version breaks anyone who pinned it.
  Let the next version number take the fix.

## See also

- [A Docker image chain](./docker.md) for images depending on images.
- [The replacer](../editing/replacer.md) for the find-and-write strategy in full.
- [autoVersion](../configuration/autoversion.md) for the rules table and the templates.
