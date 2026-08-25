# dispat on Alpine

Choose this [dispat](https://github.com/yohimik/dispat) image for the smallest possible footprint. It packages the
release orchestrator CLI on `alpine:3.24` into a 15 MB image for `linux/amd64` and `linux/arm64`.

dispat reads your conventional commits to release a monorepo. It plans version bumps across packages and spaces, runs
their version, build, and publish scripts in dependency order, and writes changelogs, tags, and GitHub releases. It
survives a failed leg without re-releasing what already shipped, as explained in the full
[documentation](https://dispat.dev/).

The image carries the CLI and four required dependencies: `git`, `ca-certificates`, `openssh-client` for `git+ssh`
remotes, and `tzdata`. It leaves your pipeline toolchain up to you. The container bakes in
`git config --system --add safe.directory '*'` so you can mount a repository from a foreign uid without ceremony, and
it omits an `ENTRYPOINT` so you can use it directly in GitLab CI or GitHub Actions.

```sh
docker run --rm -v "$PWD:/workspace" -w /workspace yohimik/dispat-alpine:1 dispat status
```

## Tags

| Tag                | Example        | Moves                         |
|--------------------|----------------|-------------------------------|
| exact version      | `1.4.2`        | never                         |
| channel            | `stable`, `rc` | every release on that channel |
| major, major.minor | `1`, `1.4`     | stable releases only          |
| `latest`           |                | stable releases only          |

Pin the major tag (`:1`) when you configure CI. The `latest` tag never points at a prerelease.

## The other images

Use `yohimik/dispat-ubuntu` as your default, because it matches the base of most CI runners. You can also pull
`yohimik/dispat-debian`, or choose `yohimik/dispat-dind` for release flows where the build stage is a `docker build`.

Read the [documentation](https://dispat.dev/), view the [source](https://github.com/yohimik/dispat), or
browse the [releases](https://github.com/yohimik/dispat/releases).
