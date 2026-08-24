# dispat in CI

dispat is a release tool. You will nearly always run it on a CI runner. Choose one of three ways to install it based on
where your pipeline runs:

| You are on | Use |
|---|---|
| GitHub Actions | [the composite action](#the-github-action) |
| GitLab CI, Jenkins, Buildkite, anything with a job image | [a container image](#the-container-images) |
| anything else, or a custom image | [the install script](#the-install-script) |

All three methods install the same binary. The container images use the install script internally.

This page covers installing the binary on a runner. Read [Pipeline patterns](./pipelines.md) to learn what to run next.
That guide covers everything from per-commit test windows to a fully gated release pipeline.

## The GitHub Action

```yaml
- uses: yohimik/dispat@v1
- run: dispat --log-format json
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

The action installs the CLI and puts it on `PATH`. It does not run dispat. You run it in your own step, like you would
with `setup-go` and `setup-node`. This lets you pass your own flags, environment and working directory.

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `version` | the latest stable release | Version or tag to install: `1.2.3`, `v1.2.3` or `services/dispat/v1.2.3`. |
| `bin-dir` | `$HOME/.dispat/bin` | Where to install. The action adds it to `PATH` either way. |
| `github-token` | `${{ github.token }}` | Only raises the API rate limit; dispat's releases are public. |

Pin a version to make a release reproducible a year from now:

```yaml
- uses: yohimik/dispat@v1
  with:
    version: 1.2.3
```

### Outputs

The `version` output contains the installed version. The `path` output contains the binary's full path. Use the path
when a later step needs an explicit location instead of relying on `PATH`:

```yaml
- id: setup
  uses: yohimik/dispat@v1
- run: echo "installed ${{ steps.setup.outputs.version }}"
```

The action supports `ubuntu-*`, `macos-*` and `windows-*` runners.

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

Set up your stage scripts in steps before this one. If your scripts shell out to `node`, `go`, `cargo`, or `docker`,
you must install those tools first. dispat itself needs only `git` and a POSIX shell.

Grant `contents: write` even for a job that pushes nothing. The run claims the repository with a
[release lock](../reference/releasing/release-lock.md) tag on the remote before it plans. This refuses a second job
instead of racing it. You can safely trigger the job on every merge because the second run stops immediately.

## Gating a pipeline on the plan

A repository with nothing pending releases nothing and exits `0`. This keeps your pipeline green when a merge only
contains a `docs:` commit. But a stage whose *point* is that something shipped needs the opposite. A deploy, an
announcement, or a downstream trigger will deploy nothing if it passes quietly on an empty plan.

Pass `--require-release` to gate these steps. The `release` and `status` commands exit `3` when the plan releases
nothing. This gives you a dedicated code separate from exit `1` failures:

```sh
dispat status --require-release    # 0 releasing, 3 nothing to release, 1 something is wrong
```

Only packages the run will actually publish count. A package held by `Release-As: none` gets no version this run. A
package [withheld until its providers release](./releasing/partial-releases.md) or excluded by your
`--package`/`--space`/`--group` selection also counts as "not releasing".

The simplest pipeline uses one job that decides and one that acts:

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
          rc=0
          dispat status --require-release --log-format json || rc=$?
          case "$rc" in
            0) echo "releasing=true" >> "$GITHUB_OUTPUT" ;;
            3) echo "releasing=false" >> "$GITHUB_OUTPUT" ;;
            *) exit "$rc" ;;
          esac

  release:
    needs: plan
    if: needs.plan.outputs.releasing == 'true'
    runs-on: ubuntu-latest
    # ... the release job above
```

Run `status` as often as you like. It takes no lock and writes nothing. On `release`, dispat answers the flag before it
takes the [release lock](./releasing/release-lock.md) and before `beforeAll` runs. This means
`dispat --require-release` in a single-job pipeline refuses the run without claiming the repository or executing your
scripts.

Capture and map the exit code in your shell script. Running it bare with `set -e` would end the job on `3` before you
can read the answer. Use a `case` statement rather than an `if` statement. An `if` statement folds every nonzero code
together, so a broken configuration would look like "nothing to release" and skip the job instead of failing. Exit `3`
means nothing released, while everything else is a real failure. Use the bare form only when a step *should* fail the
pipeline when nothing releases.

## Gating a step on what changed

Use [`dispat if --changed`](../cli/if.md#changed-packages) to gate one step inside a script. This uses the same
selection `dispat run` covers packages by, while `--require-release` gates a whole job. You can build the docs in a PR
pipeline only when the docs changed:

```sh
dispat if --changed -p docs --consumers --since origin/main --then 'dispat run build-docs'
```

The condition holds when changes reach the selection. It runs the `--then` script and passes the exit code through. It
runs nothing and exits `0` when nothing relevant changed, keeping the step green. You need `fetch-depth: 0` to count
changes against a branch base.

File tests gate on artifacts the same way, with no repository involved. You can require a test report before deploying
the docs:

```sh
dispat if -f packages/docs/data/report.json \
  --then 'dispat run deploy-docs' \
  --else 'echo "no test report; run the test job first" >&2; exit 1'
```

## The container images

Choose from four images on `linux/amd64` and `linux/arm64`:

| Image | Base | Size |
|---|---|---|
| `yohimik/dispat-alpine` | `alpine` | 15 MB |
| `yohimik/dispat-ubuntu` | `ubuntu` | 59 MB |
| `yohimik/dispat-debian` | `debian` | 69 MB |
| `yohimik/dispat-dind` | `docker:dind` | 142 MB |

Each image carries the CLI, `git`, CA certificates, an SSH client and `tzdata`. Add your pipeline's toolchain yourself.
Every extra package in a base image is one more thing to patch.

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

A release publishes every tag from one build. The tags `1`, `1.4` and `latest` share the exact digest of the version
they follow.

Pin `1` in a pipeline. The `latest` tag never points at a prerelease. Use the channel tags for prereleases.

### Two things worth knowing

**The repository you mount is owned by another uid**. Every git call would fail with "dubious ownership" without
configuration. The images set `git config --system --add safe.directory '*'` to make a bind-mounted checkout work.

**Only `dispat-dind` declares an `ENTRYPOINT`**. It inherits this from `docker:dind` to keep behaving like that image.
You still need `--privileged` or a `services: docker:dind` companion. The other three images declare no entrypoint so
GitLab CI and GitHub Actions can run scripts through the image's shell.

**`dispat self-update` is not the way to upgrade one.** That command replaces a binary that dies with the container.
Change the image tag instead. The images set `DISPAT_UPDATE_CHECK=0` to disable update warnings.

## The install script

The container images use this install script. It is the same script [Getting started](../getting-started.md#install)
hands to a human:

```sh title="With curl"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```sh title="With wget"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

The script resolves the latest stable release or takes a `--version` flag. It downloads the binary for the current
platform and verifies it against the GitHub checksum. Then it installs the binary into `--bin-dir`. The script requires
only `curl` or `wget`, with no package manager or Go toolchain needed.

| Option | Default | Meaning |
|---|---|---|
| `--version` | latest stable | `1.2.3`, `v1.2.3` or `services/dispat/v1.2.3`. |
| `--bin-dir` | `/usr/local/bin`, else `$HOME/.local/bin` | Where to install. |
| `--os`, `--arch` | this machine's | Override the platform, for building an image for another one. |
| `--token` | `$GITHUB_TOKEN` | Raises the API rate limit. |

The script prints the resolved version to stdout and everything else to stderr. Capture the version in your own script:

```sh
VERSION=$(curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh)
```

Pin the URL to a tag rather than `main` to make the installer reproducible. Use a URL like
`https://raw.githubusercontent.com/yohimik/dispat/services/dispat/v1.2.3/install.sh`.

### In your own image

```dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
ADD https://raw.githubusercontent.com/yohimik/dispat/main/install.sh /tmp/install.sh
RUN sh /tmp/install.sh --version 1.2.3 && rm /tmp/install.sh
```

The dispat images run this script in a throwaway first stage and copy the binary out. This keeps the downloader out of
the published image. Read [docker/README.md](https://github.com/yohimik/dispat/tree/main/docker) to see how this works.

## Somewhere other than GitHub Actions

Read [The release job on other providers](./ci-providers.md) for full release jobs on GitLab CI, CircleCI, Jenkins,
Buildkite and Azure Pipelines. That guide includes the clone settings and tokens each provider needs.
