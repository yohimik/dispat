# dispat with Docker-in-Docker

The [dispat](https://github.com/yohimik/dispat) release orchestrator CLI beside a Docker daemon, on `docker:29-dind`,
for release flows whose build stage is itself a `docker build`, for `linux/amd64` and `linux/arm64`.

dispat releases a monorepo from its conventional commits: it plans version bumps across packages and spaces, runs
their version, build and publish scripts in dependency order, writes changelogs, tags, creates GitHub releases and
survives a failed leg without re-releasing what already shipped. See the
[documentation](https://yohimik.github.io/dispat/) for the full model.

The image keeps its base's `ENTRYPOINT` (`dockerd-entrypoint.sh`), so it behaves exactly like `docker:dind`: run it
with `--privileged`, or use it as a `services: docker:dind` companion. It carries the CLI plus `git`,
`ca-certificates`, `openssh-client` and `tzdata` on top of the base, and
`git config --system --add safe.directory '*'` is baked in, so a repository mounted from a foreign uid works without
ceremony.

```sh
docker run --rm --privileged -v "$PWD:/workspace" -w /workspace yohimik/dispat-dind:1 dispat status
```

## Tags

| Tag                | Example        | Moves                         |
|--------------------|----------------|-------------------------------|
| exact version      | `1.4.2`        | never                         |
| channel            | `stable`, `rc` | every release on that channel |
| major, major.minor | `1`, `1.4`     | stable releases only          |
| `latest`           |                | stable releases only          |

Pin the major (`:1`) in CI. `latest` never points at a prerelease.

## The other images

`yohimik/dispat-alpine` (the smallest), `yohimik/dispat-ubuntu` (the default) and `yohimik/dispat-debian`, none of
which need a daemon or `--privileged`.

Links: [documentation](https://yohimik.github.io/dispat/) · [source](https://github.com/yohimik/dispat) ·
[releases](https://github.com/yohimik/dispat/releases)
