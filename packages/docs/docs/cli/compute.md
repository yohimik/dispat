# The compute command

Run `dispat compute` when you are setting up dispat or checking whether the configuration still matches your files.
It reads package manifests and suggests two kinds of configuration:

- **Dependencies:** compare the manifests with the combined dependency declarations, including the top-level
  `dependencies` key and [package-owned lists](../configuration/packages.md#package-dependencies) in `packages`
  entries or package config files.
- **Starting versions:** suggest [`initials`](../configuration/versions.md#initials) entries for packages that already
  have a version in their manifests but no usable release tag.

By default, dispat only prints the suggestions. Pass `--write` to apply them, `--interactive` to confirm each one, or
`--check` to gate CI. The edges need no git history, but the baselines read each package's release tags and only those
tags.

Pass [`--package` / `--space` / `--group`](./run.md#choosing-the-packages) to scope the report to the selected
packages' own declarations. Detection still reads every package's manifests whichever way you narrow. The workspace
name index resolves a declared dependency onto a provider, so an edge onto a package outside the selection stays
recognised rather than being proposed for removal.

## Manifests and dependency matching

The command scans every package folder for manifests. It reads the same twenty-one ecosystems
`dispat scanner` reads: npm (`package.json`), Go (`go.mod`), Cargo (`Cargo.toml`), Python (`pyproject.toml`,
requirements files), Composer (`composer.json`), Maven (`pom.xml`), NuGet (`*.csproj` and the flat lists), pub
(`pubspec.yaml`), Ruby (`Gemfile`, `*.gemspec`), CocoaPods (`Podfile`, `*.podspec`), Xcode (`project.pbxproj`), Apple
bundles (`Info.plist`), Android (`AndroidManifest.xml`), Gradle (`libs.versions.toml`, `build.gradle(.kts)`), Docker
(`Dockerfile`, `compose.yaml`), Unity (`Packages/manifest.json`, `ProjectSettings/ProjectSettings.asset`), Godot
(`project.godot`, `plugin.cfg`, `export_presets.cfg`), Unreal (`*.uproject`, `*.uplugin`, `Config/Default*.ini`),
Defold (`game.project`) and O3DE (`project.json`, `gem.json`).

A declared dependency matches a workspace package by manifest name first, then by a
declared local path (`file:`, a relative `replace`, `path =`, a `ProjectReference`). Python names are PEP
503-normalised, Maven names are `groupId:artifactId`, and Docker names are image repositories such as
`ghcr.io/acme/api`. Two packages declaring the same manifest name is ambiguous, so dispat reports `W220` and derives no
edges from that name.

## Read the suggestions

Each suggestion includes the manifest line that explains it:

- `+ add` for a detected pair no source declares.
- `~ kind` for a declared pair whose `kind` disagrees with the manifests.
- `- remove` for a declared pair no manifest supports. dispat suggests removal only when the consumer actually has
  parsed manifests, and unconditionally when an edge names a package that no longer exists on disk, which is the one
  drift every other command refuses to load. An edge marked `keep: true` is never suggested for removal. This is the
  escape hatch for deliberate relations no manifest declares, like a Docker image chain, and `keep` works wherever the
  edge is declared, including a package's own list.
- `+ initial` for a package whose starting version only its manifests know, described below.

A suggestion against a package-declared edge names its source (`[packages/core/dispat.json: dependencies[0]]`). The
listing tells you exactly which file an applied change would touch. A
[choreographed fleet](../choreographed-repositories.md) adds three more kinds, described below.

## Keep existing versions when adopting dispat

Without a release tag or an `initials` entry, a package starts at `0.0.0`. If your manifests already declare a released
version, `compute` can suggest that version as the starting point:

```console
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
```

A starting version is suggested only when all of these conditions hold:

- Its manifests declare a version. Root manifests are asked first, and nested ones are asked only when no root manifest
  has an answer, matching the rank that decides manifest names.
- They agree on it. Two root manifests declaring different versions is `W225`, and no baseline comes from them.
- The version is a plain semver release. dispat passes over anything that is not semver at all, and it passes over a
  prerelease such as `1.0.0-SNAPSHOT` because that is a version being worked toward rather than one released. It also
  skips `0.0.0`, which is already where a package with no entry starts.
- The config has no `initials` entry for it yet. An entry already there is your decision, and compute never rewrites
  one, whatever the manifests say.
- Its release tags cannot answer. The planner only reads `initials` when a package has no parseable stable tag, so that
  is the only case an entry is worth writing. A package with a readable release tag is silent. A package whose newest
  tag matches the format but is not a version gets the suggestion with that tag named in the evidence.

The entries land in the top-level `initials` map. Every entry already there is left exactly as it is, spelling
included. To silence a suggestion for good, write the entry yourself, because `"core": "0.0.0"` is a decision like any
other and compute will leave it alone. Without a git repository, dispat skips the baselines altogether with one warning
and computes the edges as usual.

## Apply the suggestions

The default command writes nothing. It lists dependency changes first, then starting versions, sorted by package
name. Pass `--write` to apply all suggestions or `--interactive` to answer `y`/`N` for each one.

dispat applies each change to the file that holds the declaration. An addition goes into the root config's top-level
`dependencies` object under its consumer. If that consumer already declares its providers in a
`packages.<name>.dependencies` entry or in its own in-folder file, the addition joins them there. A removal and a kind
correction edit the declaring source in place, and a baseline goes into the root config's `initials` map.

Changes to each file are combined into one atomic write. Before editing, dispat saves the original as
`<name>.backup`. That backup is overwritten the next time the file is edited, so review it before another run. Add
these backup files to `.gitignore` if you do not want them listed as untracked files.

dispat refuses cases it cannot write safely. A TOML file is not rewritten in place, so `--write` prints a
paste-ready block for it and fails. A key composed from both a [referenced file](../configuration/refs.md) *and* the
keys written beside it belongs to two files at once, so `--write` refuses it rather than choosing one. A key kept
wholly in a referenced file is written in that file at the key it holds there. The `$ref` survives the write, and the
backup sits beside the file that changed. Reading through a filesystem symlink works for preview and `--check`, but
`--write` or an accepted interactive edit refuses a symlinked configuration file, the root file or a `$ref` fragment
alike, before writing a backup: replacing the link atomically would turn it into a regular file and leave its target
stale. To apply the suggestions, replace the link with the file it points to, or edit that file by hand.

## Connect repositories in a fleet

In an [identity-linked fleet](../choreographed-repositories.md), `compute` also suggests missing links and roster
entries. These suggestions concern the whole fleet and are not narrowed by package selection:

```console
$ dispat compute
+ link api sdk  connects sdk to the fleet

1 suggestion(s); apply all with --write, choose with --interactive
```

- `+ link <repository> <peer>` creates a missing link. With `--topology minimal`, the default, dispat preserves every
  existing link and joins free path endpoints. It previews at most one new joining edge per repair round. With
  `--write`, dispat applies that edge, recomposes the fleet, and continues toward a single chain; with
  `--interactive`, it applies only explicitly accepted suggestions and shows newly discovered suggestions for
  another invocation. A complete chain of `Q` repositories has `Q - 1` links and `Q - 1` hops along its longest
  route. With `--topology star`, it proposes a direct link from the entry repository to every peer. Star mode errors when existing links are
  incompatible with that shape; it does not convert or remove them. The same line also proposes the half of a
  link only one of its two repositories declares, which is what `W332` reports: the pair is already joined, so nothing
  is fetched and only the missing declaration is written.
- `+ init <repository> <path>` materialises a link the fleet declares and this checkout does not have.
- `+ repository <repository> <peer>` adds a roster entry a peer has not heard of, written into that peer's own file.
  An entry no roster states a `url` for is reported rather than written.

Links are applied after the configuration edits, because a link is created against the roster the file now holds.
For either topology, dispat proposes a missing entry in the link owner's own roster before trying to stage a
new forward link; a gitlink without that declaration would be ignored during fleet composition.
In `minimal` mode, missing checkouts are initialized before new chain joins. A `--write` repair may take
several recomposition rounds. Afterward, dispat lists remaining fleet suggestions from the fresh checkout
view; a newly exposed roster entry needs a safe configuration edit on another invocation. If suggestions
remain, `--write` reports incomplete repair as an error and leaves staged edits for review. `--interactive`
also shows newly discovered suggestions but applies none without explicit acceptance. If a newly reached
peer exposes an incompatible link, dispat stops and leaves staged edits for review. The
forward half is a real checkout and the other half is declared inside it without cloning, pinned at a revision the
declaring repository's own remote can serve. A declaring repository with no remote has that half withheld with a
warning, and you add it by hand. A missing URL, unresolved checkout initialization, or withheld reverse half
leaves repair incomplete: `--write` exits nonzero even when it printed a warning, and keeps staged changes for
review. Nothing is committed and no link is ever removed: each repository is left with
staged changes for you to review and commit. A link URL carrying user information is refused, because `.gitmodules`
is committed and pushed.

The `--check` flag overrides both apply modes. It writes nothing and exits `1` when any suggestion exists across any
source, fleet links included. Use this as the CI gate for a config lagging the manifests.

`--topology` chooses the shape used for fleet-link suggestions. `minimal` is the default and requires a chain:
it preserves existing links and joins path endpoints. An existing branch with degree greater than two cannot
be repaired into a chain without removing links, so compute refuses it. New links
use the path declared by their owning repository,
or `.links/<peer>` when that owner states none. `star` links every peer directly to the entry repository and
errors when existing links cannot fit that shape. Both choices preserve existing links.
An existing cycle (`E338`) or invalid repository identity (`E339`) stops compute, including under `--check`;
neither is reported as in sync.
An incompatible link already visible in the composed fleet is rejected before writing. With `--write`, a newly
checked-out peer can reveal another link that was not visible earlier; if that link conflicts with the selected
topology, compute fails and leaves the changes it already staged for review. It does not roll them back or delete a
link.

## Flags

Beside the [global flags](./README.md#global-flags):

### `--package`, `-p`

Select named packages. Repeat the flag or separate names with commas. Names are case-insensitive and accept `*`
globs; quote them so the shell does not expand them first. For example, `-p core,web` selects two packages and `-p '*'`
selects every package. See [Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Select every package in the named spaces. This accepts the same lists and globs as `--package`. Standalone packages
do not belong to a space, so `-s '*'` does not include them. See
[Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Select every package in a named [versioning group](../reference/releasing/versioning.md), using the same lists and
globs as `--package`. A group may be a `versionGroups` entry or a space that versions together, and may span several
spaces. See [Choosing the packages](./run.md#choosing-the-packages).

### `--write`

Apply every suggestion to the config file for `compute` only. The previous copy is saved as `<name>.backup`.

### `--interactive`, `-i`

Confirm each suggestion (`y`/`N` on stdin) before applying it for `compute` only. This flag wins over `--write`.

### `--check`

Report only, change nothing, and exit `1` when there is something to do for `compute` and `self-update`. For `compute`,
this triggers on any suggestion at all: edges, baselines and fleet links alike. This is the CI gate for a config
lagging the manifests, and it overrides both apply modes.

### `--topology`

Choose the fleet-link shape proposed by `compute`: `minimal` (the default) or `star`. Minimal preserves existing links
and joins free endpoints until the known fleet is one chain; it rejects an existing branch or cycle. Preview proposes
at most one new joining edge, while `--write` recomposes between joins. Star proposes a direct link from every peer
to the entry repository and errors if the existing links are incompatible. `--interactive` applies only explicitly
accepted suggestions; newly discovered ones wait for another invocation. No topology mode removes a link.
`star` requires a linked fleet with a repository identity. `minimal` also works with ordinary configurations, where
compute still derives dependencies and baselines.
