# Release scripts

The shell scripts dispat runs as this repository's own release stages. They are referenced by name from `scripts` in
[`dispat.json`](../dispat.json) and run **inside the releasing package's folder**, which is why each one reaches the
repository root as `../../`. Everything they need arrives in the environment: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/environment) a stage is given, plus whatever CI exports.

| Script                                       | Package  | `flow` slot | Reads                                                        | Produces                                                                                     |
|----------------------------------------------|----------|-------------|--------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| [`build-dispat.sh`](./build-dispat.sh)       | `dispat` | `build`     | `DISPAT_NEW_VERSION`, `DISPAT_OUTPUT`                        | One cross-compiled binary per platform in `services/dispat/dist`, exported as GitHub release assets through `DISPAT_EXPORT_GITHUB`. |
| [`cut-docs-version.sh`](./cut-docs-version.sh) | `docs`   | `version`   | `DISPAT_IS_PRERELEASE`, `DISPAT_VERSION`, `DISPAT_NEW_VERSION` | A `packages/docs/versioned_docs/version-<minor>` snapshot, once per stable minor. Nothing on a prerelease. |
| [`deploy-docs.sh`](./deploy-docs.sh)         | `docs`   | `publish`   | `CI`, `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `DISPAT_NEW_VERSION` | A force-pushed orphan `gh-pages` branch carrying `packages/docs/build`.                       |

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
```

`deploy-docs.sh` is the exception: **do not run it**, by hand or through `dispat run`. It force-pushes the live site,
and the tag dispat writes afterwards is the only record that the leg completed, so a deploy outside a release run
publishes a working tree to production and leaves nothing behind saying so. The script refuses unless `CI=true` for
exactly that reason. Note that these are top-level `scripts`, so every changed package resolves them: without a
`--package` filter (`dispat run deploy-docs`) the run would reach all of them at once.

The scripts are POSIX `sh` with `set -eu`, and they are the release path itself: a change here is only really exercised
by the [Release workflow](../.github/workflows/release.yml).
