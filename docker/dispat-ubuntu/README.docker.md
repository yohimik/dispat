# dispat on Ubuntu

The default [dispat](https://github.com/yohimik/dispat) image puts the release orchestrator CLI on `ubuntu:24.04`. This
matches the base environment of most CI runners. It supports `linux/amd64` and `linux/arm64`.

dispat releases a monorepo from its conventional commits. It plans version bumps across packages and spaces, then runs
their version, build and publish scripts in dependency order.

It writes changelogs, creates tags, and pushes GitHub releases. If a leg fails, dispat survives without re-releasing
what already shipped. Read the [documentation](https://yohimik.github.io/dispat/) to understand the full model.

The image carries the CLI and the four dependencies it needs to run. These are `git`, `ca-certificates`,
`openssh-client` for `git+ssh` remotes, and `tzdata`. It includes nothing else, because your pipeline provides its own
toolchain.

The image bakes in `git config --system --add safe.directory '*'` so a repository mounted from a foreign uid works
immediately. It omits the `ENTRYPOINT` to keep the image usable as a GitLab CI or GitHub Actions job container.

```sh
docker run --rm -v "$PWD:/workspace" -w /workspace yohimik/dispat-ubuntu:1 dispat status
```

## Tags

| Tag                | Example        | Moves                         |
|--------------------|----------------|-------------------------------|
| exact version      | `1.4.2`        | never                         |
| channel            | `stable`, `rc` | every release on that channel |
| major, major.minor | `1`, `1.4`     | stable releases only          |
| `latest`           |                | stable releases only          |

Pin the major (`:1`) in CI. The `latest` tag never points at a prerelease.

## The other images

Choose `yohimik/dispat-alpine` for the smallest footprint or `yohimik/dispat-debian` for a standard environment. Pull
`yohimik/dispat-dind` for release flows whose build stage is itself a `docker build`.

Links: [documentation](https://yohimik.github.io/dispat/) · [source](https://github.com/yohimik/dispat) ·
[releases](https://github.com/yohimik/dispat/releases)
