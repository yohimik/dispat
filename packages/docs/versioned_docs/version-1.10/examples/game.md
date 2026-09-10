# Game development

A game is a release package with engine-specific build and publication commands. Start with the general guide
[From one package to many](./one-to-many.md): it shows how to add deliverables while preserving an existing
package’s source, release tags and version history. The same pattern applies when a game gains a dedicated server,
modding SDK, launcher, editor or website.

## Choose the engine and destinations

| Engine | Version and dependency inputs |
| --- | --- |
| [Godot](./godot.md) | `project.godot`, addon metadata and export presets. |
| [Unity](./unity.md) | Package dependencies and ProjectSettings. |
| [Unreal](./unreal.md) | Project/plugin manifests and Config files. |
| [Defold](./defold.md) | `game.project` version; explicit release edges for library archives. |
| [O3DE](./o3de.md) | Project and gem manifests, including gem dependency constraints. |

Build with the engine’s existing export or packaging command. Validate the exact platform artifacts before the
publisher uploads them. Use the configured version for the artifact metadata and store-facing version where those
represent the same identity. Store build counters, engine compatibility versions and semantic versions may have
different policies; do not overwrite them simply to make the numbers match.

[Publishing to Steam](./steam.md) covers depots, branches and `steamcmd`.
[Publishing to itch.io](./itch.md) covers `butler` and per-platform channels.
For Android, validate [native-library alignment](./android.md#inspect-every-native-wrapper-for-16-kb-alignment)
for the engine and every bundled middleware/plugin wrapper.

## Add the surrounding packages

Use the [expanded graph](./one-to-many.md#add-the-next-deliverables) for a game client, protocol library, SDK,
server image and site. Declare only real dependencies: a website needs a game edge when it consumes that game’s
release data, not merely because the two share a repository. Request propagation explicitly when consumers should
release with a provider.

A server that downloads a published SDK must wait at its provider’s publication boundary; one compiling against
local source may only need build ordering. Keep the appropriate engine, package-manager and container caches.
There is no promised game-build speedup from adding release orchestration.

Playtests can use [prerelease channels](../reference/releasing/prerelease-branches.md). Unversioned build inputs can
use [versioning: none](../reference/releasing/versioning.md#packages-that-never-release-none); separately shipped
asset bundles may instead need their own release records.

## Keep installers and upload results consistent

Tie installer payload URLs to the exact build, preserve completed uploads on retry, and fail when a required asset
cannot be published. These checks complement the graph; they belong in the packaging and publishing scripts. See the
[Defold and O3DE findings](./release-integration.md).

Do not assume every version-looking field is the release identity. A Defold extension can use a GitHub tag as its
installable version while `game.project` describes only the example project. An O3DE release-train tag such as
`2605.0` can intentionally contain a gem and engine compatibility version such as `4.2.0`. Give dispat the package
whose identity you intend to release, and leave consumer, demo, and engine compatibility versions under their native
tooling unless they are deliberately part of the same version policy.

These distinctions are visible in [extension-webview 1.5.1's demo manifest](https://github.com/defold/extension-webview/blob/1.5.1/game.project)
and [o3de-extras 2605.0's ROS2 gem](https://github.com/o3de/o3de-extras/blob/2605.0/Gems/ROS2/gem.json).
Do not count an intentional compatibility version as an unpublished release or silently rewrite it to match the tag.
