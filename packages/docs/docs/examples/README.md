# Examples

These pages cover release setups by ecosystem and delivery target, plus verified integrations with public projects.
Release walkthroughs show their configuration, scripts and terminal output. The public-repository audit and
manifest-focused guides state the narrower checks they actually ran.

Every dispat transcript comes from a real run against a throwaway repository, with only timestamps and durations
normalized. Lines printed by your own commands, like `npm`, `docker`, or `butler`, come directly from those tools. Your
output will look different.

You do not need a monorepo to start. One folder, one package, one publish is a valid setup. Growing into a graph later
is additive, so you avoid restructuring and keep your already published versions as baselines.

A library can gain a CLI and documentation; an application can gain a container, SDK and website. Each deliverable
can join the same release graph while retaining its own build and publication tools.

Read [A single package](./single-package.md) to see the smallest form. Then read
[From one package to many](./one-to-many.md) to walk through the growth step by step.

Check [Concepts](../concepts.md) if a term is new. It defines all of them in a few minutes of reading. Read
[One repository or many](../monorepo.md) to decide your shape before you commit to it.

Start with [From one package to many](./one-to-many.md) for the general pattern, then choose the ecosystem whose
commands match your project. [An npm monorepo](./npm.md) provides a compact complete release setup.

After that, go to the page for your own ecosystem below. Read [Adopting dispat](./adopting.md) second instead if you
are bringing dispat to a repository that already ships versions.

Read [Integrating an existing release pipeline](./release-integration.md) for findings from public release incidents,
artifact checks, and the limits of orchestration.
[Skills, specifications and TeX documentation](./document-artifacts.md) covers versioned non-code deliverables.

Read [Open source integration checks](./open-source.md) for all 21 ecosystems, pinned public inputs, and locally
verified edits and limitations. [Python without uv and with Pants](./python-pants.md) covers historical build setups.

## Ecosystem by ecosystem

Choose your ecosystem below. Each page distinguishes release walkthroughs from narrower manifest and integration
checks, and explains what still belongs to the native toolchain.

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
| [Defold projects](./defold.md) | Monarch: project versions and archive-URL dependencies. |
| [O3DE projects and gems](./o3de.md) | Atom: gem versions and explicit dependency constraints. |
| [Aqua tool pins](./aqua.md) | Imported tool pins, asset selectors and the provider publication boundary. |

## Game development

| Example | What it covers |
|---------|----------------|
| [Game development](./game.md) | Engine and store integration, with a link to the general one-to-many guide. |
| [Publishing to Steam](./steam.md) | `steamcmd`, depots, and release channels mapped onto Steam branches. |
| [Publishing to itch.io](./itch.md) | `butler`, one channel per platform, and the version players see. |

## Shipping artifacts

| Example | What it covers |
|---------|----------------|
| [Cross-platform binaries](./binaries.md) | Four targets, checksums, and assets attached to the GitHub release. |
| [Helm charts that follow the image](./helm.md) | `appVersion` and image tags written by the run that pushed the image. |
| [Terraform before application deployment](./terraform.md) | Versioned infrastructure, temporary imported state, and dependent backend and frontend deployments. |
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

dispat reads and writes thirty-six manifest formats. You will find every one of them worked through on these pages.

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

You might have a version that lives outside these formats, like a Helm `Chart.yaml`, a README install line, or a plain
text file. The [replace strategy](../configuration/autoversion.md) handles those. Read the [Helm](./helm.md) page to
see it worked through.

The pages under [Editing the monorepo](../editing/manifests.md) provide the other half of the worked examples. They
show the commands that change files across many packages between releases.

Read [shared versions](../reference/releasing/versioning.md) to see how a release behaves once it covers more than one
package. Check [dispat in CI](../reference/ci.md) for the CI side.
