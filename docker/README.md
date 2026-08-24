# The dispat images

You have four minimal images that carry the dispat CLI. Each uses a different base for `linux/amd64` and `linux/arm64`:

| Package         | Docker Hub              | Base                 | Size   |
|-----------------|-------------------------|----------------------|--------|
| `dispat-alpine` | `yohimik/dispat-alpine` | `alpine:3.24`        | 15 MB  |
| `dispat-ubuntu` | `yohimik/dispat-ubuntu` | `ubuntu:24.04`       | 59 MB  |
| `dispat-debian` | `yohimik/dispat-debian` | `debian:trixie-slim` | 69 MB  |
| `dispat-dind`   | `yohimik/dispat-dind`   | `docker:29-dind`     | 142 MB |

Each image carries the CLI and four essential packages. You get `git`, `ca-certificates`, `openssh-client` for
`git+ssh` remotes, and `tzdata`. dispat refuses to start without `git`. The images contain nothing else, because your
pipeline's toolchain belongs to you and extra packages create CVEs you have to triage.

```sh
docker run --rm -v "$PWD:/workspace" yohimik/dispat-alpine:1 dispat status
```

The images bake in `git config --system --add safe.directory '*'`. When you mount a repository, a uid owns it that the
container knows nothing about. This config prevents git from failing with a "dubious ownership" error.

The three ordinary images do not declare an `ENTRYPOINT`. GitLab CI and GitHub Actions container jobs run scripts
through the image's shell, so a fixed entrypoint breaks them. `dispat-dind` is the exception. It keeps the
`dockerd-entrypoint.sh` from its base, so it behaves exactly like `docker:dind`. You still need `--privileged` or a
`services: docker:dind` companion.

## Tags

| Tag | Example | Declared in | Moves |
|--------------------|----------------|---------------------|-------------------------------|
| exact version | `1.4.2` | `docker-compose.yml` | never |
| channel | `stable`, `rc` | `stable.yml`, `rc.yml` | every release on that channel |
| major, major.minor | `1`, `1.4` | `stable.yml` | stable releases only |
| `latest` | | `stable.yml` | stable releases only |

Pin `1` in your CI pipeline. The `latest` tag never points at a prerelease. The channel tags exist beside it to handle
those prereleases.

## How a build works

Every folder here is a dispat package containing four files. These files define the whole release. You will not find a
shell script anywhere in this leg:

- **`docker-compose.yml` is the manifest.** dispat reads a compose file the way it reads a `package.json`. The folder
  ships the service that declares a `build:` section and carries a tagged `image:`. That `image:` line defines the
  package's name and version, and the version stage rewrites its tag on release.
- **`stable.yml` and `rc.yml` carry the moving tags**, and the stage picks one by name:

  ```sh
  docker compose -f docker-compose.yml -f $DISPAT_CHANNEL.yml build
  ```

  `DISPAT_CHANNEL` is `stable` or `rc`. The channel selects its own tag set without any conditions. Compose *appends*
  `build.tags` across `-f` files, so these tags join the versioned tag instead of replacing it. One build pushes every
  tag at the same digest. A channel with no file fails the build, because a new train must not silently inherit
  `latest`.
- **`Dockerfile` fetches the binary rather than receiving it.** The first stage runs [`install.sh`](../install.sh),
  which is the same script the documentation gives you. The runtime stage copies the single file out. This creates one
  download path in the project instead of two that can drift. Every image build exercises the installer.

Know these three consequences before you edit any of these files:

- The CLI version an image installs is **not** the image's own version. An image-only patch ships the CLI that is
  already released. The compose files pass `${INSTALL_DISPAT_VERSION}` as a build argument to handle this. That
  variable is a top-level env default resolving to `${DISPAT_WORKSPACE_DISPAT_VERSION}`, which holds the CLI's planned
  version while it releases in the same run, and its baseline otherwise. The CI gate overrides it for the run.
- **The channel files are named the way they are on purpose.** dispat recognises manifests by exact file name. It looks
  for eight spellings of `compose.yaml` or `docker-compose.yml`, and `stable.yml` is none of them. The autoVersion
  stage therefore never rewrites what is inside `stable.yml`. This keeps `latest` as `latest` instead of reconciling it
  to the version on every release. Do not rename one of these to `compose.override.yml`, because the tags will start
  rotting.
- **`stable.yml` reads `DISPAT_MAJOR` and `DISPAT_MINOR`** from the stage environment. It uses the `${VAR:?}` form to
  stop the build rather than writing an image tagged with an empty string. A test in `internal/selfupdate` checks every
  variable the compose files name against what the planner actually emits. A new interpolation cannot reach a release
  without the variable behind it.

## Changing an image

An image is its own package on its own patch line. A base bump or a fix requires one commit and one release. You do not
involve a CLI release:

```sh
git commit -m "fix(dispat-alpine): pin alpine 3.25"
```

That command releases `dispat-alpine` alone. A `feat(dispat)` commit moves the whole `cli` version group onto one
major.minor. This group includes the CLI, the docs, and all four images. A `fix(dispat)` commit reaches the images
through their dependency edge, so they rebuild on the new binary.

Run this command to build an image by hand without releasing or pushing anything:

```sh
dispat run build --since all -p dispat-alpine
```

The stage environment `dispat run` provides is what the compose file interpolates. This means you run the exact same
command the release runs. You need QEMU to build for both architectures on one machine. The `docker/setup-qemu-action`
handles this in CI.
