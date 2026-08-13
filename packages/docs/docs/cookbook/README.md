# Cookbook

Worked examples rather than reference. Each page here takes one thing you actually want to do and walks it end to end,
with the config, the scripts people write, and the output of a real run. The [CLI](../cli/README.md) and
[Configuration](../configuration/README.md) pages are the other half: they say what every flag and key means, and this
half says which ones you reach for.

If a term is new, [Concepts](../concepts.md) defines all of them in a few minutes of reading.

## Setting one up

[**Recipes**](./recipes.md) is the place to start: copy-ready setups for the most common stacks, each a complete
configuration you can paste and adapt.

| Recipe | What it covers |
|--------|----------------|
| [An npm package](./recipes.md#an-npm-package) | The smallest useful monorepo, from zero to a published release. |
| [A single package, no monorepo](./recipes.md#a-single-package-no-monorepo) | dispat on a repository with one thing in it. |
| [Let the manifests declare the graph](./recipes.md#let-the-manifests-declare-the-graph) | `dispat compute`, so the dependency graph is not written twice. |
| [Adopt dispat in a repository that already ships versions](./recipes.md#adopt-dispat-in-a-repository-that-already-ships-versions) | Starting from versions that already exist, without restarting at `0.0.1`. |
| [A Docker image chain](./recipes.md#a-docker-image-chain) | Images depending on images, where a build needs its base *published*. |
| [An Android app](./recipes.md#an-android-app) | Gradle, a version catalog, and a build number beside the version. |
| [npm and Docker in one graph](./recipes.md#npm-and-docker-in-one-graph) | The mixed case dispat was built for. |
| [A pnpm workspace](./recipes.md#a-pnpm-workspace) | `workspace:*` ranges reconciled at the version stage. |
| [Registry login, once per space](./recipes.md#registry-login-once-per-space) | `flow.login`, and why it is a space-level idea. |
| [Keeping a space's exceptions inside its folder](./recipes.md#keeping-a-spaces-exceptions-inside-its-folder) | In-folder config files. |
| [Recovering from a failed run](./recipes.md#recovering-from-a-failed-run) | What to do when half the graph published. |
| [A beta channel: try, iterate, graduate](./recipes.md#a-beta-channel-try-iterate-graduate) | Prerelease trains from the first `%beta` to the graduation. |

## Releasing

How a release behaves once it is more than one package lives in
[Reference → Releasing](../reference/releasing/versioning.md): the [shared versioning
modes](../reference/releasing/versioning.md), [running the pipeline's own steps
yourself](../reference/releasing/steps.md), [releasing part of the
graph](../reference/releasing/partial-releases.md), and [the release
lock](../reference/releasing/release-lock.md).

## Editing the monorepo

The commands that change files across many packages at once. They are not part of a release, but they are what a
release-shaped repository needs between releases.

| Page | Covers |
|------|--------|
| [Manifest tools](./editing/manifests.md) | `dispat scanner` and `dispat writer`: read and edit one folder's manifests, with no config and no git. |
| [Editing across the monorepo](./editing/autowriter.md) | `dispat autowriter`: one manifest edit applied to every package a selection covers. |
| [Substituting across the monorepo](./editing/autosubstitute.md) | `dispat autosubstitute`: the same idea for literal text, so hand-written coordinates follow a release. |
| [The replacer](./editing/replacer.md) | `dispat replacer`: literal find-and-write over any file, parsing nothing. |
