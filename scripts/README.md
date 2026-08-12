# Release scripts

The shell scripts dispat runs as this repository's own release stages. They are referenced by name from `scripts` in
[`dispat.yaml`](../dispat.yaml) and run **inside the releasing package's folder**, which is why each one reaches the
repository root as `../../`. Everything they need arrives in the environment: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/reference/environment) a stage is given, plus whatever CI exports.

| Script                                       | Package  | `flow` slot | Reads                                                        | Produces                                                                                     |
|----------------------------------------------|----------|-------------|--------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| [`build-dispat.sh`](./build-dispat.sh)       | `dispat` | `build`     | `DISPAT_NEW_VERSION`, `DISPAT_OUTPUT`                        | One cross-compiled binary per platform in `services/dispat/dist`, exported as GitHub release assets through `DISPAT_EXPORT_GITHUB`. |
| [`cut-docs-version.sh`](./cut-docs-version.sh) | `docs`   | `version`   | `DISPAT_IS_PRERELEASE`, `DISPAT_VERSION`, `DISPAT_NEW_VERSION` | A `packages/docs/versioned_docs/version-<minor>` snapshot, once per stable minor. Nothing on a prerelease. |
| [`deploy-docs.sh`](./deploy-docs.sh)         | `docs`   | `publish`   | `CI`, `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `DISPAT_NEW_VERSION` | A force-pushed orphan `gh-pages` branch carrying `packages/docs/build`.                       |

The `docker` space has no scripts here at all. Its stages are
[`docker compose build` and `docker compose push`](../docker/README.md) with a `docker login` one-liner, written
straight into [`dispat.yaml`](../dispat.yaml), because that is all they are: the image's version lives in its
`docker-compose.yml`, which dispat reads and rewrites as a manifest, and its moving tags live in a per-channel file the
stage selects with `-f $DISPAT_CHANNEL.yml`. Nothing is left for a shell to decide.

Each script's own header comment carries the reasoning behind it: why the binaries are built with `GOWORK=off`, why a
docs snapshot is cut per minor rather than per patch, and why the deploy pushes a branch instead of using
`actions/deploy-pages`.

## Running one by hand

`dispat run <script> --since all --package <name>` runs a script once inside that package, with the exact environment
its stage would hand it, releasing nothing. `--since all` is what reaches a package that has not changed; drop it to
run the script only when the package is in the release window.

```sh
dispat run build-dispat --since all -p dispat        # cross-compiles into services/dispat/dist
dispat run build-docs --since all -p docs            # pnpm install + docusaurus build
dispat run cut-docs-version --since all -p docs      # writes a versioned_docs snapshot if the version warrants one
dispat run build-image --since all -s docker         # docker compose build, all four images, pushing nothing
```

`deploy-docs.sh` is the exception: **do not run it**, by hand or through `dispat run`. It publishes the live site and
refuses unless `CI=true`, for the reason spelled out next.

`deploy-docs.sh` force-pushes the live site,
and the tag dispat writes afterwards is the only record that the leg completed, so a deploy outside a release run
publishes a working tree to production and leaves nothing behind saying so. The script refuses unless `CI=true` for
exactly that reason. Note that these are top-level `scripts`, so every changed package resolves them: without a
`--package` filter (`dispat run deploy-docs`) the run would reach all of them at once.

The scripts are POSIX `sh` with `set -eu`, and they are the release path itself: a change here is only really exercised
by the [Release workflow](../.github/workflows/release.yml).
