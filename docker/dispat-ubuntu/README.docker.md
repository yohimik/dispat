# dispat on Ubuntu

The default [dispat](https://github.com/yohimik/dispat) image: the release orchestrator CLI on `ubuntu:24.04`, the
base most CI runners already are, for `linux/amd64` and `linux/arm64`.

dispat releases a monorepo from its conventional commits: it plans version bumps across packages and spaces, runs
their version, build and publish scripts in dependency order, writes changelogs, tags, creates GitHub releases and
survives a failed leg without re-releasing what already shipped. See the
[documentation](https://yohimik.github.io/dispat/) for the full model.

The image carries the CLI plus the four things it cannot work without: `git`, `ca-certificates`, `openssh-client` for
`git+ssh` remotes, and `tzdata`. Nothing else: a pipeline's own toolchain is its own business.
`git config --system --add safe.directory '*'` is baked in, so a repository mounted from a foreign uid works without
ceremony. There is no `ENTRYPOINT`, which is what keeps the image usable as a GitLab CI or GitHub Actions job
container.

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

Pin the major (`:1`) in CI. `latest` never points at a prerelease.

## The other images

`yohimik/dispat-alpine` (the smallest), `yohimik/dispat-debian`, and `yohimik/dispat-dind` for release flows whose
build stage is itself a `docker build`.

Links: [documentation](https://yohimik.github.io/dispat/) · [source](https://github.com/yohimik/dispat) ·
[releases](https://github.com/yohimik/dispat/releases)
