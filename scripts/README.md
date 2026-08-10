# Release scripts

The shell scripts dispat runs as this repository's own release stages. They are referenced by name from `scripts` in
[`dispat.json`](../dispat.json) and run **inside the releasing package's folder**, which is why each one reaches the
repository root as `../../`. Everything they need arrives in the environment: the
[`DISPAT_*` variables](https://yohimik.github.io/dispat/environment) a stage is given, plus whatever CI exports.

| Script                                       | Package  | `flow` slot | Reads                                                        | Produces                                                                                     |
|----------------------------------------------|----------|-------------|--------------------------------------------------------------|----------------------------------------------------------------------------------------------|
| [`build-dispat.sh`](./build-dispat.sh)       | `dispat` | `build`     | `DISPAT_NEW_VERSION`, `DISPAT_OUTPUT`                        | The three cross-compiled binaries in `services/dispat/dist`, exported as GitHub release assets through `DISPAT_EXPORT_GITHUB`. |
| [`cut-docs-version.sh`](./cut-docs-version.sh) | `docs`   | `version`   | `DISPAT_IS_PRERELEASE`, `DISPAT_VERSION`, `DISPAT_NEW_VERSION` | A `packages/docs/versioned_docs/version-<minor>` snapshot, once per stable minor. Nothing on a prerelease. |
| [`deploy-docs.sh`](./deploy-docs.sh)         | `docs`   | `publish`   | `CI`, `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, `DISPAT_NEW_VERSION` | A force-pushed orphan `gh-pages` branch carrying `packages/docs/build`.                       |

Each script's own header comment carries the reasoning behind it: why the binaries are built with `GOWORK=off`, why a
docs snapshot is cut per minor rather than per patch, and why the deploy pushes a branch instead of using
`actions/deploy-pages`.

## Running one by hand

`dispat run <script> <package>` runs a script once inside that package, with the exact environment its stage would hand
it, releasing nothing — the package does not have to be changed:

```sh
dispat run build-dispat dispat        # cross-compiles into services/dispat/dist
dispat run build-docs docs            # pnpm install + docusaurus build
dispat run cut-docs-version docs      # writes a versioned_docs snapshot if the version warrants one
```

`deploy-docs.sh` is the exception: **do not run it**, by hand or through `dispat run`. It force-pushes the live site,
and the tag dispat writes afterwards is the only record that the leg completed, so a deploy outside a release run
publishes a working tree to production and leaves nothing behind saying so. The script refuses unless `CI=true` for
exactly that reason. Note that these are top-level `scripts`, so every changed package resolves them: naming no package
(`dispat run deploy-docs`) would reach all of them at once.

The scripts are POSIX `sh` with `set -eu`, and they are the release path itself: a change here is only really exercised
by the [Release workflow](../.github/workflows/release.yml).
