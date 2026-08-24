# dispat on Debian

Pull this image to run the [dispat](https://github.com/yohimik/dispat) release orchestrator CLI on
`debian:trixie-slim`. Use it for pipelines where your tooling expects a Debian userland. It supports `linux/amd64` and
`linux/arm64`.

dispat releases a monorepo from its conventional commits. It plans version bumps across packages and spaces, runs their
version, build, and publish scripts in dependency order, writes changelogs, creates tags, and publishes GitHub
releases. Read the [documentation](https://yohimik.github.io/dispat/) to see how it survives a failed leg without
re-releasing what already shipped.

The image carries the CLI and four required dependencies: `git`, `ca-certificates`, `openssh-client` for `git+ssh`
remotes, and `tzdata`. It provides nothing else, because your pipeline toolchain is your own business. The image bakes
in `git config --system --add safe.directory '*'` so a repository mounted from a foreign uid works immediately, and it
omits an `ENTRYPOINT` so you can use it as a GitLab CI or GitHub Actions job container.

```sh
docker run --rm -v "$PWD:/workspace" -w /workspace yohimik/dispat-debian:1 dispat status
```

## Tags

| Tag                | Example        | Moves                         |
|--------------------|----------------|-------------------------------|
| exact version      | `1.4.2`        | never                         |
| channel            | `stable`, `rc` | every release on that channel |
| major, major.minor | `1`, `1.4`     | stable releases only          |
| `latest`           |                | stable releases only          |

Pin the major tag (`:1`) in CI. The `latest` tag never points at a prerelease.

## The other images

You can also use `yohimik/dispat-alpine` for the smallest footprint or `yohimik/dispat-ubuntu` as the default. Use
`yohimik/dispat-dind` for release flows where your build stage is itself a `docker build`.

Links: [documentation](https://yohimik.github.io/dispat/) · [source](https://github.com/yohimik/dispat) ·
[releases](https://github.com/yohimik/dispat/releases)
