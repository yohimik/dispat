# Examples

Choose a guide for what you want to release. Start with the setup guides if you are new to dispat; go straight to your
language or delivery target if the release graph is already configured.

The walkthroughs include configuration, scripts and terminal output. Some pages focus on reading and updating
manifests, so check what a configuration includes before using it as a complete release job. The dispat transcripts
come from runs in throwaway repositories, with timestamps and durations normalized. Output from commands such as
`npm`, `docker` and `butler` will vary with your tools and project.

## Start or adopt an existing project {#shaping-the-repository}

One package is enough. You can add a CLI, documentation site or container later without resetting the versions you
have already published.

| I want to… | Start here |
|---|---|
| Release one package | [A single package, no monorepo](./single-package.md) |
| Keep the versions and history of an existing project | [Adopting dispat](./adopting.md) |
| Add more things to release | [From one package to many](./one-to-many.md) |
| Keep my current build and publish tools | [Integrating an existing release pipeline](./release-integration.md) |
| Put configuration beside the code it describes | [Keeping configuration beside the code](./layout.md) |
| Connect npm packages and Docker images | [npm and Docker in one graph](./mixed.md) |
| Share registry authentication across packages | [Registry login, once per space](./login.md) |
| Coordinate releases across repositories | [A control repository](../control-repository.md#source-history-mode) |

The [concepts guide](../concepts.md) explains packages, spaces and dependency edges. If you are still choosing a
repository layout, read [One repository or many](../monorepo.md).

## Find your language or package manager {#ecosystem-by-ecosystem}

These guides keep your existing build tools and show where dispat supplies versions, dependency updates and ordering.

| Example | What it covers |
|---------|----------------|
| [An npm monorepo](./npm.md) | Package scripts, a build and a publish, versions from commits. |
| [A pnpm workspace](./pnpm.md) | `workspace:` ranges, the shared lock file, and `pnpm publish` mid-release. |
| [A Go module workspace](./go.md) | Module tags, temporary local `replace` entries, and why `tagFormat` matters. |
| [A Cargo workspace](./rust.md) | `path` dependencies, `[patch.crates-io]` links, and workspace inheritance. |
| [A Python monorepo](./python.md) | `pyproject.toml` and `requirements*.txt` reconciled in one pass. |
| [Python without uv, including Pants](./python-pants.md) | Build/Twine commands, BUILD targets and shared version sources. |
| [Maven modules](./java.md) | `pom.xml`, `${property}` skips, and parent-managed versions. |
| [A Gradle library and its version catalog](./gradle.md) | `libs.versions.toml`, `version.ref`, and literal coordinates. |
| [.NET packages](./dotnet.md) | `dotnet pack`, project references, and central package management. |
| [Composer packages](./php.md) | Versions from tags, and a `composer.json` with no version field. |
| [Ruby gems](./ruby.md) | Gemspecs, Gemfiles, and the `VERSION` constant a writer will not touch. |
| [A Flutter app and its packages](./flutter.md) | `pubspec.yaml`, the `+N` build number, and `dependency_overrides`. |
| [An iOS app and a CocoaPods library](./apple.md) | `Info.plist`, `project.pbxproj`, Podfiles and podspecs. |
| [An Android app](./android.md) | Gradle, a monotonic `versionCode`, and a bundle on the GitHub release. |
| [Aqua tool pins](./aqua.md) | Imported tool pins, asset selectors and the provider publication boundary. |

## Ship files, images or a deployment {#shipping-artifacts}

| Example | What it covers |
|---------|----------------|
| [A Docker image chain](./docker.md) | Wait for a base image to be pushed before building its consumers. |
| [Cross-platform binaries](./binaries.md) | Four targets, checksums, and assets attached to the GitHub release. |
| [Helm charts that follow the image](./helm.md) | `appVersion` and image tags written by the run that pushed the image. |
| [Terraform before application deployment](./terraform.md) | Versioned infrastructure, temporary imported state, and dependent backend and frontend deployments. |
| [A site deployed from the release](./pages.md) | A docs or marketing site published during the run, with a CI-only guard. |
| [Skills, specifications and TeX documentation](./document-artifacts.md) | Versioned documents, their supporting files and publication checks. |

## Build and publish a game {#game-development}

Start with [Game development](./game.md) for the release graph, then choose an engine and a store.

| Example | What it covers |
|---------|----------------|
| [Unity](./unity.md) | Project settings, UPM packages and store build counters. |
| [Godot](./godot.md) | Project and addon versions, plus export presets. |
| [Unreal Engine](./unreal.md) | Plugin descriptors, project settings and native artifact checks. |
| [Defold projects](./defold.md) | Project versions and archive-URL dependencies. |
| [O3DE projects and gems](./o3de.md) | Gem versions and explicit dependency constraints. |
| [Publishing to Steam](./steam.md) | `steamcmd`, depots and release channels mapped to Steam branches. |
| [Publishing to itch.io](./itch.md) | `butler`, one channel per platform and the version players see. |

## Releasing on several machines

| Example | What it covers |
|---------|----------------|
| [Worker nodes on Kubernetes](./kubernetes-workers.md) | Workers as an Indexed Job for one release, machines added for pending pods, and why CPU autoscaling does not fit. |

## Which example covers my manifest

If you know the file you need to update, use this table to find its guide.

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
| `game.project` | [Defold](./defold.md) |
| `project.json`, `gem.json` | [O3DE](./o3de.md) |
| `aqua.yaml` and imported tool pins | [Aqua](./aqua.md) |

You might have a version that lives outside these formats, like a Helm `Chart.yaml`, a README install line, or a plain
text file. The [replace strategy](../configuration/autoversion.md) handles those. Read the [Helm](./helm.md) page to
see it worked through.

For edits between releases, [Manifest tools](../editing/manifests.md) shows how to inspect and update files across
packages without running a release.

Read [shared versions](../reference/releasing/versioning.md) to see how a release behaves once it covers more than one
package. Check [dispat in CI](../reference/ci.md) for the CI side.
