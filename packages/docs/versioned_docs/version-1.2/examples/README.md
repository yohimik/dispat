# Examples

These pages give you complete, copy-ready release setups. You get one page per ecosystem and one per delivery target.
Each page shows the config, the scripts its stages run, and the terminal output of a real run.

Every dispat transcript comes from a real run against a throwaway repository, with only timestamps and durations
normalized. Lines printed by your own commands, like `npm`, `docker`, or `butler`, come directly from those tools. Your
output will look different.

You do not need a monorepo to start. One folder, one package, one publish is a valid setup. Growing into a graph later
is additive, so you avoid restructuring and keep your already published versions as baselines.

This matters because a repository holding one deliverable rarely stops there. A game gains a landing page, a docs site,
an SDK, and a server, while a library gains a CLI and a demo. Each addition becomes one more block in the same file
rather than another release script nobody maintains.

Read [A single package](./single-package.md) to see the smallest form. Then read
[A game, from one package to many](./game.md) to walk through the growth step by step.

Check [Concepts](../concepts.md) if a term is new. It defines all of them in a few minutes of reading. Read
[One repository or many](../monorepo.md) to decide your shape before you commit to it.

**Read one page, then one more.** Start with [An npm monorepo](./npm.md) whatever you build. It is the shortest
complete setup and the best first read, because every other page uses the same four pieces with different commands.

After that, go to the page for your own ecosystem below. Read [Adopting dispat](./adopting.md) second instead if you
are bringing dispat to a repository that already ships versions.

## Ecosystem by ecosystem

These pages cover one package manager each. They include a config you can copy, the scripts its stages run, and a real
run.

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

## Shaping the repository

The pages above focus on one ecosystem each. The pages below cover the repository around them. They apply to your setup
whatever you build.

| Example | What it covers |
|---------|----------------|
| [A single package, no monorepo](./single-package.md) | dispat on a repository with one thing in it, and what changes when it grows. |
| [Adopting dispat](./adopting.md) | Deriving the graph and the starting versions from the manifests, in a new repository or one that already ships. |
| [Keeping configuration beside the code](./layout.md) | Space and package config files in their own folders, and `.dispatexclude`. |
| [npm and Docker in one graph](./mixed.md) | Two ecosystems, one graph: the mixed case dispat was built for. |
| [Registry login, once per space](./login.md) | The `login` slot: one authentication per space, whatever the registry. |

## Which example covers my manifest

dispat reads and writes thirty-five manifest formats. You will find every one of them worked through on these pages.

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
| `Packages/manifest.json`, `ProjectSettings/ProjectSettings.asset` | [Unity](./unity.md) |
| `project.godot`, `plugin.cfg`, `export_presets.cfg` | [Godot](./godot.md) |
| `*.uproject`, `*.uplugin`, `Config/DefaultGame.ini`, `Config/DefaultEngine.ini` | [Unreal](./unreal.md) |
| `game.project`, `project.json`, `gem.json` | [Games](./game.md) |

You might have a version that lives outside these formats, like a Helm `Chart.yaml`, a README install line, or a plain
text file. The [replace strategy](../configuration/autoversion.md) handles those. Read the [Helm](./helm.md) page to
see it worked through.

The pages under [Editing the monorepo](../editing/manifests.md) provide the other half of the worked examples. They
show the commands that change files across many packages between releases.

Read [shared versions](../reference/releasing/versioning.md) to see how a release behaves once it covers more than one
package. Check [dispat in CI](../reference/ci.md) for the CI side.
