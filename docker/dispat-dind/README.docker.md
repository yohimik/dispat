# dispat with Docker-in-Docker

This image packages the [dispat](https://github.com/yohimik/dispat) release orchestrator CLI alongside a Docker daemon.
It runs on `docker:29-dind` for `linux/amd64` and `linux/arm64`. Use this image when your build stage is a
`docker build`.

dispat reads your conventional commits to release a monorepo, planning version bumps across packages and spaces before
running your version, build, and publish scripts in dependency order. The tool writes changelogs, creates tags, and
publishes GitHub releases. It survives a failed leg without re-releasing what already shipped, and you can see the
[documentation](https://dispat.dev/) for the full model.

Run this image with `--privileged` or use it as a `services: docker:dind` companion. It behaves exactly like
`docker:dind` because it keeps the base `ENTRYPOINT` (`dockerd-entrypoint.sh`), but it adds the dispat CLI, `git`,
`ca-certificates`, `openssh-client`, and `tzdata`. You can mount a repository from a foreign uid and it works
immediately because `git config --system --add safe.directory '*'` is baked in.

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

Pin the major tag (`:1`) in CI. The `latest` tag never points at a prerelease.

## The other images

You can also use `yohimik/dispat-alpine` (the smallest), `yohimik/dispat-ubuntu` (the default), or
`yohimik/dispat-debian`. None of these alternative images need a daemon or the `--privileged` flag.

Links: [documentation](https://dispat.dev/) · [source](https://github.com/yohimik/dispat) ·
[releases](https://github.com/yohimik/dispat/releases)
