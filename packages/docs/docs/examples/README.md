# Examples

Complete, copy-ready release setups, one page per ecosystem and one per delivery target, each with the config, the
scripts its stages run, and the terminal output of a real run. Every dispat transcript in this section was produced by
running dispat against a throwaway repository; only timestamps and durations are normalized. Lines printed by your own
commands (the `npm`, `docker`, `butler` lines) come from those commands, so yours will differ.

You do not need a monorepo to start. One folder, one package, one publish is a valid setup, and growing into a graph
later is additive: no restructuring, and the versions already published stay the baselines everything counts from.
That matters because a repository holding one deliverable rarely keeps holding one. A game gains a landing page, a
docs site, an SDK and a server; a library gains a CLI and a demo. Each of those is one more block in the same file
rather than one more release script nobody maintains. [A single package](./single-package.md) is the smallest form,
and [A game, from one package to many](./game.md) walks the growth step by step.

If a term is new, [Concepts](../concepts.md) defines all of them in a few minutes of reading.

## Start here

| Example | What it covers |
|---------|----------------|
| [An npm monorepo](./npm.md) | The smallest useful setup, from zero to a published release. |
| [A single package, no monorepo](./single-package.md) | dispat on a repository with one thing in it. |
| [Adopting dispat](./adopting.md) | Deriving the graph and the starting versions from the manifests, in a new repository or one that already ships. |
| [Keeping configuration beside the code](./layout.md) | Space and package config files in their own folders, and `.dispatexclude`. |
| [Registry login, once per space](./login.md) | The `login` slot: one authentication per space, whatever the registry. |
| [npm and Docker in one graph](./mixed.md) | The mixed case dispat was built for. |

## Ecosystem by ecosystem

| Example | What it covers |
|---------|----------------|
| [An npm monorepo](./npm.md) | Package scripts, a build and a publish, versions from commits. |
| [A pnpm workspace](./pnpm.md) | `workspace:` ranges, the shared lock file, and `pnpm publish` mid-release. |
| [A Go module workspace](./go.md) | Tag-driven versions, the `replace` link bracket, and why `tagFormat` matters. |
| [A Cargo workspace](./rust.md) | `path` dependencies, `[patch.crates-io]` links, and workspace inheritance. |
| [A Python monorepo](./python.md) | `pyproject.toml` and `requirements*.txt` reconciled in one pass. |
| [Maven modules](./java.md) | `pom.xml`, `${property}` skips, and parent-managed versions. |
| [A Gradle library and its version catalog](./gradle.md) | `libs.versions.toml`, `version.ref`, and literal coordinates. |
| [.NET packages](./dotnet.md) | `dotnet pack`, project references, and central package management. |
| [Composer packages](./php.md) | Versions from tags, and a `composer.json` with no version field. |
| [Ruby gems](./ruby.md) | Gemspecs, Gemfiles, and the `VERSION` constant a writer will not touch. |
| [A Flutter app and its packages](./flutter.md) | `pubspec.yaml`, the `+N` build number, and `dependency_overrides`. |
| [An iOS app and a CocoaPods library](./apple.md) | `Info.plist`, `project.pbxproj`, Podfiles and podspecs. |
| [An Android app](./android.md) | Gradle, a monotonic `versionCode`, and a bundle on the GitHub release. |
| [A Docker image chain](./docker.md) | Images depending on images, where a build needs its base *published*. |

## Game development

| Example | What it covers |
|---------|----------------|
| [A game, from one package to many](./game.md) | One game today; a landing page, docs, an SDK and a server later. Godot and Unity. |
| [Publishing to Steam](./steam.md) | `steamcmd`, depots, and release channels mapped onto Steam branches. |
| [Publishing to itch.io](./itch.md) | `butler`, one channel per platform, and the version players see. |

## Shipping artifacts

| Example | What it covers |
|---------|----------------|
| [Cross-platform binaries](./binaries.md) | Four targets, checksums, and assets attached to the GitHub release. |
| [Helm charts that follow the image](./helm.md) | `appVersion` and image tags written by the run that pushed the image. |
| [A site deployed from the release](./pages.md) | A docs or marketing site published during the run, with a CI-only guard. |

## Which example covers my manifest

dispat reads and writes twenty-three manifest formats. Every one of them is worked through on one of these pages.

| Manifest | Example |
|----------|---------|
| `package.json` | [npm](./npm.md), [pnpm](./pnpm.md) |
| `go.mod` | [Go](./go.md) |
| `Cargo.toml` | [Rust](./rust.md) |
| `pyproject.toml`, `requirements*.txt` | [Python](./python.md) |
| `composer.json` | [PHP](./php.md) |
| `pom.xml` | [Maven](./java.md) |
| `*.csproj`, `*.fsproj`, `*.vbproj`, `*.nuspec`, `Directory.Packages.props`, `packages.config` | [.NET](./dotnet.md) |
| `pubspec.yaml` | [Flutter and Dart](./flutter.md) |
| `Gemfile`, `*.gemspec` | [Ruby](./ruby.md) |
| `Podfile`, `*.podspec`, `Info.plist`, `project.pbxproj` | [Apple](./apple.md) |
| `AndroidManifest.xml`, `build.gradle`, `build.gradle.kts` | [Android](./android.md), [Gradle](./gradle.md) |
| `libs.versions.toml` | [Gradle](./gradle.md) |
| `Dockerfile`, `Containerfile`, `compose.yaml` | [Docker](./docker.md) |

A version that lives in none of these, a Godot `project.godot`, a Unity `ProjectSettings.asset`, a Helm `Chart.yaml`,
a README install line, is handled by the [replace strategy](../configuration/autoversion.md) and worked through in
[the game](./game.md) and [Helm](./helm.md) pages.

The pages under [Editing the monorepo](../editing/manifests.md) are the other half of the worked examples: the
commands that change files across many packages between releases. How a release behaves once it covers more than one
package is under Releasing, starting with [shared versions](../reference/releasing/versioning.md), and the CI side is
under [dispat in CI](../reference/ci.md).
