# The dispat images

Four minimal images carrying the dispat CLI, one per base, for `linux/amd64` and `linux/arm64`:

| Package         | Docker Hub              | Base                 | Size   |
|-----------------|-------------------------|----------------------|--------|
| `dispat-alpine` | `yohimik/dispat-alpine` | `alpine:3.24`        | 15 MB  |
| `dispat-ubuntu` | `yohimik/dispat-ubuntu` | `ubuntu:24.04`       | 59 MB  |
| `dispat-debian` | `yohimik/dispat-debian` | `debian:trixie-slim` | 69 MB  |
| `dispat-dind`   | `yohimik/dispat-dind`   | `docker:29-dind`     | 142 MB |

Each carries the CLI plus the four things it cannot work without: `git` (dispat refuses to start without it),
`ca-certificates`, `openssh-client` for `git+ssh` remotes, and `tzdata`. Nothing else — a pipeline's own toolchain is
its own business, and every extra package here is a CVE somebody has to triage.

```sh
docker run --rm -v "$PWD:/workspace" yohimik/dispat-alpine:1 dispat status
```

`git config --system --add safe.directory '*'` is baked in, because the repository you mount is owned by a uid the
container knows nothing about and every git call would otherwise fail with "dubious ownership".

None of the three ordinary images declares an `ENTRYPOINT`. GitLab CI and GitHub Actions container jobs run their
script through the image's shell, and a fixed entrypoint is what makes an otherwise fine image unusable there.
`dispat-dind` is the exception: it keeps its base's `dockerd-entrypoint.sh`, so it goes on behaving exactly like
`docker:dind` and still needs `--privileged` or a `services: docker:dind` companion.

## Tags

| Tag | Example | Declared in | Moves |
|--------------------|----------------|---------------------|-------------------------------|
| exact version | `1.4.2` | `docker-compose.yml` | never |
| channel | `stable`, `rc` | `stable.yml`, `rc.yml` | every release on that channel |
| major, major.minor | `1`, `1.4` | `stable.yml` | stable releases only |
| `latest` | | `stable.yml` | stable releases only |

Pin `1` in CI. `latest` never points at a prerelease, which is the whole reason the channel tags exist beside it.

## How a build works

Every folder here is a dispat package of four files, and between them they are the whole release — there is no shell
script anywhere in this leg:

- **`docker-compose.yml` is the manifest.** dispat reads a compose file the way it reads a `package.json`: the service
  that both declares a `build:` section and carries a tagged `image:` is what the folder ships, so that `image:` line
  is the package's name and version, and the version stage rewrites its tag on release.
- **`stable.yml` and `rc.yml` carry the moving tags**, and the stage picks one by name:

  ```sh
  docker compose -f docker-compose.yml -f $DISPAT_CHANNEL.yml build
  ```

  `DISPAT_CHANNEL` is `stable` or `rc`, so the channel selects its own tag set with no condition anywhere. Compose
  *appends* `build.tags` across `-f` files, so these join the versioned tag rather than replacing it, and one build
  pushes every tag at the same digest. A channel with no file fails the build — a new train should not silently
  inherit `latest`.
- **`Dockerfile` fetches the binary rather than receiving it.** Its first stage runs [`install.sh`](../install.sh) —
  the same script the documentation hands to users — and the runtime stage copies the one file out. One download path
  in this project instead of two that can drift, and the installer is exercised by every image build.

Three consequences worth knowing before editing any of them:

- The CLI version an image installs is **not** the image's own version. An image-only patch ships the CLI that is
  already released, which is why the compose files pass `${DISPAT_WORKSPACE_DISPAT_VERSION}` as a build argument: that
  variable is the CLI's planned version while it is releasing in the same run, and its baseline otherwise.
- **The channel files are named the way they are on purpose.** dispat recognises manifests by exact file name — eight
  spellings of `compose.yaml` / `docker-compose.yml` — and `stable.yml` is none of them. autoVersion therefore never
  rewrites what is in there, which is the only reason `latest` can stay `latest` instead of being reconciled to the
  version on every release. Rename one of these to `compose.override.yml` and the tags start rotting.
- **`stable.yml` needs `DISPAT_MAJOR` and `DISPAT_MINOR`** in the stage environment. They do not exist yet; until they
  do, a stable build stops with `required variable DISPAT_MAJOR is missing a value`. Prerelease builds are unaffected.

## Changing an image

An image is its own package on its own patch line, so a base bump or a fix is one commit and one release, with no CLI
release involved:

```sh
git commit -m "fix(dispat-alpine): pin alpine 3.25"
```

That releases `dispat-alpine` alone. A `feat(dispat)` moves the whole `cli` version group — the CLI, the docs and all
four images — onto one major.minor, and a `fix(dispat)` reaches the images through their dependency edge, so they
rebuild on the new binary.

To build one by hand, without releasing or pushing anything:

```sh
dispat run build-image --since all -p dispat-alpine
```

The stage environment `dispat run` provides is what the compose file interpolates, so this is the same command the
release runs. Building for both architectures on one machine needs QEMU (`docker/setup-qemu-action` does it in CI).
