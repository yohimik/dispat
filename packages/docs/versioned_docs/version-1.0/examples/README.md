# Examples

Complete, copy-ready release setups, one page per package manager, each with the config, the shell scripts its stages
run, and the terminal output of a real run. Every transcript in this section was produced by running dispat against a
throwaway repository; only timestamps and durations are normalized. Script output lines (the `npm` and `docker` lines)
come from your own commands, so yours will differ.

If a term is new, [Concepts](../concepts.md) defines all of them in a few minutes of reading.

| Example | What it covers |
|---------|----------------|
| [An npm monorepo](./npm.md) | The smallest useful setup, from zero to a published release. |
| [A pnpm workspace](./pnpm.md) | `workspace:` ranges, the shared lock file, and `pnpm publish` mid-release. |
| [A Docker image chain](./docker.md) | Images depending on images, where a build needs its base *published*. |
| [An Android app](./android.md) | Gradle, a monotonic `versionCode`, and a bundle attached to the GitHub release. |
| [npm and Docker in one graph](./mixed.md) | The mixed case dispat was built for. |
| [Registry login, once per space](./login.md) | The `login` slot: one authentication per space, whatever the registry. |
| [A single package, no monorepo](./single-package.md) | dispat on a repository with one thing in it. |
| [Adopting dispat](./adopting.md) | Deriving the graph and the starting versions from the manifests, in a new repository or one that already ships. |
| [Keeping configuration beside the code](./layout.md) | Space and package config files in their own folders, and `.dispatexclude`. |

The pages under [Editing the monorepo](../editing/manifests.md) are the other half of the worked examples: the
commands that change files across many packages between releases. How a release behaves once it covers more than one
package is under Releasing, starting with [shared versions](../reference/releasing/versioning.md), and the CI side is
under [dispat in CI](../reference/ci.md).
