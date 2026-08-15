# dispat in CI

dispat is a release tool, so the machine that runs it is nearly always a CI runner. There are three ways to get it
there, and which one you want is decided by where your pipeline runs:

| You are on | Use |
|---|---|
| GitHub Actions | [the composite action](#the-github-action) |
| GitLab CI, Jenkins, Buildkite, anything with a job image | [a container image](#the-container-images) |
| anything else, or a custom image | [the install script](#the-install-script) |

All three end at the same binary, and the last two are the same script: the images install themselves with it.

This page is about getting the binary onto a runner. What to run once it is there, from per-commit test windows to a
fully gated release pipeline, is [Pipeline patterns](./pipelines.md).

## The GitHub Action

```yaml
- uses: yohimik/dispat@v1
- run: dispat --log-format json
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

The action installs the CLI and puts it on `PATH`. It does not run it: like `setup-go` and `setup-node`, it leaves
that to a step of your own, which is what lets you pass your own flags, environment and working directory.

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `version` | the latest stable release | Version or tag to install: `1.2.3`, `v1.2.3` or `services/dispat/v1.2.3`. |
| `bin-dir` | `$HOME/.dispat/bin` | Where to install. The action adds it to `PATH` either way. |
| `github-token` | `${{ github.token }}` | Only raises the API rate limit; dispat's releases are public. |

Pin a version when a release should be reproducible a year from now:

```yaml
- uses: yohimik/dispat@v1
  with:
    version: 1.2.3
```

### Outputs

`version` is what was installed, and `path` is the binary's full path, useful when a later step wants to be explicit
rather than rely on `PATH`:

```yaml
- id: setup
  uses: yohimik/dispat@v1
- run: echo "installed ${{ steps.setup.outputs.version }}"
```

The action runs on `ubuntu-*`, `macos-*` and `windows-*` runners.

### A full release job

```yaml
name: Release
on: workflow_dispatch

permissions:
  contents: write        # the release lock, tags, the release commit, releases

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0     # dispat reads tags and commit ranges; a shallow clone is refused (E196)
      - uses: yohimik/dispat@v1
      - run: dispat --log-format json
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Whatever your stage scripts shell out to (`node`, `go`, `cargo`, `docker`) is still yours to set up in steps before
this one. dispat itself needs only `git` and a POSIX shell.

`contents: write` is needed even by a job that pushes nothing: the run claims the repository with a
[release lock](../reference/releasing/release-lock.md) tag on the remote before it plans, so that two jobs releasing at once are refused
rather than raced. That also makes the job safe to trigger on every merge; the second run stops immediately instead of
publishing beside the first.

## Gating a pipeline on the plan

A repository with nothing pending releases nothing and exits `0`. That is the right answer, because there was nothing to do,
and a job triggered on every merge must not go red because this particular merge was a `docs:` commit. But a stage
whose *point* is that something shipped needs the opposite: a deploy that runs after the release, an announcement, a
downstream trigger. Passing quietly on an empty plan is how a pipeline ends up deploying nothing.

`--require-release` is that gate. On `release` and `status` alike, it exits `1` when the plan releases nothing:

```sh
dispat status --require-release    # 0 if this run would publish something, 1 if not
```

Only packages the run will actually publish count. A package held by `Release-As: none`, one
[withheld until its providers release](./releasing/partial-releases.md), and one your `--package`/`--space`/`--group`
selection left out are all "not releasing", because none of them gets a version this run.

The plain shape is one job that decides and one that acts:

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    outputs:
      releasing: ${{ steps.plan.outputs.releasing }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: yohimik/dispat@v1
      - id: plan
        run: |
          if dispat status --require-release --log-format json; then
            echo "releasing=true" >> "$GITHUB_OUTPUT"
          else
            echo "releasing=false" >> "$GITHUB_OUTPUT"
          fi

  release:
    needs: plan
    if: needs.plan.outputs.releasing == 'true'
    runs-on: ubuntu-latest
    # ... the release job above
```

`status` takes no lock and writes nothing, so the gate is free to run as often as you like. On `release` the flag is
answered before the [release lock](./releasing/release-lock.md) is taken and before `beforeAll` runs, so
`dispat --require-release` in a single-job pipeline refuses without ever claiming the repository or executing one of
your scripts.

Note the shell here: `if dispat status --require-release` rather than a bare `run:` step, because `set -e` would
otherwise end the job on the `1` that is the whole point. A step that *should* fail the pipeline when nothing
released (a manually dispatched release, say) wants the bare form instead.

## The container images

Four images, one per base, on `linux/amd64` and `linux/arm64`:

| Image | Base | Size |
|---|---|---|
| `yohimik/dispat-alpine` | `alpine` | 15 MB |
| `yohimik/dispat-ubuntu` | `ubuntu` | 59 MB |
| `yohimik/dispat-debian` | `debian` | 69 MB |
| `yohimik/dispat-dind` | `docker:dind` | 142 MB |

Each carries the CLI, `git`, CA certificates, an SSH client and `tzdata`. Nothing else: your pipeline's toolchain is
yours to add, and every extra package in a base image is one more thing to patch.

```yaml
# GitLab CI
release:
  image: yohimik/dispat-alpine:1
  script:
    - dispat --log-format json
```

```sh
# Anywhere with a Docker daemon
docker run --rm -v "$PWD:/workspace" yohimik/dispat-alpine:1 dispat status
```

### Tags

| Tag | Example | Moves |
|---|---|---|
| exact version | `1.4.2` | never |
| channel | `stable`, `rc` | every release on that channel |
| major, major.minor | `1`, `1.4` | stable releases only |
| `latest` | | stable releases only |

A release publishes every tag from one build, so `1`, `1.4` and `latest` are the same digest as the version they
follow rather than a later copy of it.

Pin `1` in a pipeline. `latest` never points at a prerelease: that is what the channel tags are for.

### Two things worth knowing

**The repository you mount is owned by another uid**, so every git call would fail with "dubious ownership". The
images set `git config --system --add safe.directory '*'` to make a bind-mounted checkout work.

**Only `dispat-dind` declares an `ENTRYPOINT`**, inherited from `docker:dind` so it keeps behaving like that image
(it still wants `--privileged`, or a `services: docker:dind` companion). The other three declare none on purpose:
GitLab CI and GitHub Actions container jobs run their script through the image's shell, and a fixed entrypoint is what
breaks that.

**`dispat self-update` is not the way to upgrade one.** It replaces a binary that does not outlive the container;
change the image tag instead. The images set `DISPAT_UPDATE_CHECK=0` so they do not nag about it.

## The install script

The same script the images use, and the one [Getting started](../getting-started.md#install) hands to a human:

```sh title="With curl"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```sh title="With wget"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

It resolves the latest stable release (or takes `--version`), downloads the binary for the platform it is running on,
verifies it against the checksum GitHub published, and installs it into `--bin-dir`. It needs `curl` or `wget` and
nothing else: no package manager, no Go toolchain.

| Option | Default | Meaning |
|---|---|---|
| `--version` | latest stable | `1.2.3`, `v1.2.3` or `services/dispat/v1.2.3`. |
| `--bin-dir` | `/usr/local/bin`, else `$HOME/.local/bin` | Where to install. |
| `--os`, `--arch` | this machine's | Override the platform, for building an image for another one. |
| `--token` | `$GITHUB_TOKEN` | Raises the API rate limit. |

The resolved version is printed to stdout and everything else to stderr, so a script can capture it:

```sh
VERSION=$(curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh)
```

Pin the URL to a tag rather than `main` if you want the installer itself to be reproducible:
`https://raw.githubusercontent.com/yohimik/dispat/services/dispat/v1.2.3/install.sh`.

### In your own image

```dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
ADD https://raw.githubusercontent.com/yohimik/dispat/main/install.sh /tmp/install.sh
RUN sh /tmp/install.sh --version 1.2.3 && rm /tmp/install.sh
```

dispat's own images do this in a throwaway first stage and copy the binary out, which keeps the downloader out of the
published image; [docker/README.md](https://github.com/yohimik/dispat/tree/main/docker) has the shape.
