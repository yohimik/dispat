# The compute command

Run `dispat compute` to read what every package declares about itself and turn it into configuration. You do not have
to transcribe the dependency graph or the starting versions by hand. The command derives two things:

- the **declared edges**, diffed against the **merged** declaration list. This list includes the top-level
  `dependencies` key and every [package-declared list](../configuration/packages.md#package-dependencies) from a
  `packages` entry or an in-folder config file.
- the **baselines** a first release starts from. These become [`initials`](../configuration/versions.md#initials)
  entries taken from the version the manifests declare.

By default, dispat only prints the suggestions. Pass `--write` to apply them, `--interactive` to confirm each one, or
`--check` to gate CI. The edges need no git history, but the baselines read each package's release tags and only those
tags.

Pass [`--package` / `--space` / `--group`](./run.md#choosing-the-packages) to scope the report to the selected
packages' own declarations. Detection still reads every package's manifests whichever way you narrow. The workspace
name index resolves a declared dependency onto a provider, so an edge onto a package outside the selection stays
recognised rather than being proposed for removal.

**What it reads.** The command scans every package folder for manifests. It reads the same twenty-one ecosystems
`dispat scanner` reads: npm (`package.json`), Go (`go.mod`), Cargo (`Cargo.toml`), Python (`pyproject.toml`,
requirements files), Composer (`composer.json`), Maven (`pom.xml`), NuGet (`*.csproj` and the flat lists), pub
(`pubspec.yaml`), Ruby (`Gemfile`, `*.gemspec`), CocoaPods (`Podfile`, `*.podspec`), Xcode (`project.pbxproj`), Apple
bundles (`Info.plist`), Android (`AndroidManifest.xml`), Gradle (`libs.versions.toml`, `build.gradle(.kts)`), Docker
(`Dockerfile`, `compose.yaml`), Unity (`Packages/manifest.json`, `ProjectSettings/ProjectSettings.asset`), Godot
(`project.godot`, `plugin.cfg`, `export_presets.cfg`), Unreal (`*.uproject`, `*.uplugin`, `Config/Default*.ini`),
Defold (`game.project`) and O3DE (`project.json`, `gem.json`).

**How a dependency becomes an edge.** A declaration matches a workspace package by manifest name first, then by a
declared local path (`file:`, a relative `replace`, `path =`, a `ProjectReference`). Python names are PEP
503-normalised, Maven names are `groupId:artifactId`, and Docker names are image repositories such as
`ghcr.io/acme/api`. Two packages declaring the same manifest name is ambiguous, so dispat reports `W220` and derives no
edges from that name.

**What it suggests.** You will see four kinds of change. Each prints with the manifest line that motivates it:

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

**Baselines from manifest versions.** A repository adopting dispat already carries its versions in the manifests.
Without an entry for them, dispat would start every package at `0.0.0` and release `0.0.1`, throwing away the history
the files know about. The compute command offers the missing entries instead:

```console
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
```

dispat proposes an entry for a package only when all of the following conditions hold. The last point keeps an
established repository quiet:

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

**How changes are applied.** Nothing is written by default. The listing puts the edges first and the baselines after
them, sorted by package name. Pass `--write` to apply every suggestion, or `--interactive` to answer `y`/`N` per
suggestion on stdin.

dispat applies each change to the file that holds the declaration. An addition goes into the root config's top-level
`dependencies` object under its consumer. If that consumer already declares its providers in a
`packages.<name>.dependencies` entry or in its own in-folder file, the addition joins them there. A removal and a kind
correction edit the declaring source in place, and a baseline goes into the root config's `initials` map.

Every write is guarded. dispat writes everything one file receives in a single pass, so a run that changes two keys
still leaves one backup. Every edited file is first copied to `<name>.backup`, and each write is atomic. The backup
file is untracked, worth a `.gitignore` entry, and overwritten on every applying run.

dispat refuses two cases rather than guessing at them. A TOML file is not rewritten in place, so `--write` prints a
paste-ready block for it and fails. A key composed from both a [referenced file](../configuration/refs.md) *and* the
keys written beside it belongs to two files at once, so `--write` refuses it rather than choosing one. A key kept
wholly in a referenced file is written in that file at the key it holds there. The `$ref` survives the write, and the
backup sits beside the file that changed.

**Fleet links.** In an [identity-linked fleet](../choreographed-repositories.md), the command also reads the fleet and
proposes what joins it. The suggestions are independent of the package selection, because a fleet is either linked or
it is not:

```console
$ dispat compute
+ link api sdk  connects sdk to the fleet
+ link api web  connects web to the fleet

2 suggestion(s); apply all with --write, choose with --interactive
```

- `+ link <repository> <peer>` creates a missing link. With `--topology minimal`, the default, dispat preserves every
  existing link and proposes the fewest additions that connect everything the rosters name, chosen so that no two
  repositories are ever joined twice. With `--topology star`, it proposes a direct link from the entry repository to
  every peer. Star mode errors when existing links are incompatible with that shape; it does not convert or remove them.
  The same line also proposes the half of a
  link only one of its two repositories declares, which is what `W332` reports: the pair is already joined, so nothing
  is fetched and only the missing declaration is written.
- `+ init <repository> <path>` materialises a link the fleet declares and this checkout does not have.
- `+ repository <repository> <peer>` adds a roster entry a peer has not heard of, written into that peer's own file.
  An entry no roster states a `url` for is reported rather than written.

Links are applied after the configuration edits, because a link is created against the roster the file now holds. The
forward half is a real checkout and the other half is declared inside it without cloning, pinned at a revision the
declaring repository's own remote can serve. A declaring repository with no remote has that half withheld with a
warning, and you add it by hand. Nothing is committed and no link is ever removed: each repository is left with
staged changes for you to review and commit. A link URL carrying user information is refused, because `.gitmodules`
is committed and pushed.

The `--check` flag overrides both apply modes. It writes nothing and exits `1` when any suggestion exists across any
source, fleet links included. Use this as the CI gate for a config lagging the manifests.

`--topology` chooses the shape used for fleet-link suggestions. `minimal` is the default and adds the fewest links
needed while preserving the links already present. New links use the path declared by their owning repository,
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

Narrow to the named packages for every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`,
`autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`). This flag is repeatable and
comma-separated, matched case-insensitively, and accepts `*` globs (`-p '*'` is every package); see
[Choosing the packages](./run.md#choosing-the-packages).

### `--space`, `-s`

Narrow to every package of the named spaces for the same eleven commands, using the same spellings. A standalone
package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).

### `--group`, `-g`

Narrow to every package of the named [versioning groups](../reference/releasing/versioning.md) for the same eleven
commands, using the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross
spaces; see [Choosing the packages](./run.md#choosing-the-packages).

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
and adds the fewest needed to connect the roster. Star proposes a direct link from every peer to the entry repository
and errors if the existing links are incompatible. `--write` and `--interactive` apply these suggestions in the same
way as other compute suggestions; no topology mode removes a link. `star` requires a linked fleet with a repository identity.
`minimal` also works with ordinary configurations, where compute still derives dependencies and baselines.
