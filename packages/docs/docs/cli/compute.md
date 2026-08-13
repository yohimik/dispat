# The compute command

`dispat compute` reads what every package already declares about itself and turns it into configuration, so neither the
dependency graph nor the starting versions have to be transcribed by hand. It derives two things:

- the **declared edges**, diffed against the **merged** declaration list: the top-level `dependencies` key plus every
  [package-declared list](../configuration/packages.md#package-dependencies) (a `packages` entry's or an in-folder
  config file's);
- the **baselines** a first release starts from, as [`initials`](../configuration/versions.md#initials) entries taken
  from the version the manifests declare.

By default the suggestions are only printed; `--write` applies them, `--interactive` confirms each, `--check` gates CI.
The edges need no git history. The baselines read each package's release tags, and only those.

[`--package` / `--space` / `--group`](./run.md#choosing-the-packages) scope the report to the selected packages' own declarations.
Detection still reads every package's manifests whichever way you narrow: the workspace name index is what resolves a
declared dependency onto a provider, so an edge onto a package outside the selection stays recognised rather than
being proposed for removal.

**What it reads.** Every package folder is scanned for manifests: `package.json`, `go.mod`, `Cargo.toml`,
`pyproject.toml` (PEP 621 and Poetry), `composer.json`, `pom.xml`, `*.csproj`, `pubspec.yaml`, `requirements*.txt`,
`Dockerfile` and `compose.yaml`.

**How a dependency becomes an edge.** A declaration matches a workspace package by manifest name first (Python names are
PEP 503-normalised, Maven names are `groupId:artifactId`, Docker names are image repositories such as
`ghcr.io/acme/api`), then by a declared local path (`file:`, a relative `replace`, `path =`, a `ProjectReference`). Two packages declaring the same manifest name is ambiguous: reported as
`W220`, and no edges are derived from that name.

**What it suggests.** Four kinds of change, each printed with the manifest line that motivates it:

- `+ add` for a detected pair no source declares;
- `~ kind` for a declared pair whose `kind` disagrees with the manifests;
- `- remove` for a declared pair no manifest supports. Removal is only suggested when the consumer actually has parsed
  manifests, plus, unconditionally, when an edge names a package that no longer exists on disk (the one drift every
  other command refuses to load). An edge marked `keep: true` is never suggested for removal: the escape hatch for
  deliberate relations no manifest declares, a Docker image chain being the usual one. `keep` works wherever the edge
  is declared, a package's own list included.
- `+ initial` for a package whose starting version only its manifests know, described below.

A suggestion against a package-declared edge names its source (`[packages/core/dispat.json: dependencies[0]]`), so the
listing says which file an applied change would touch.

**Baselines from manifest versions.** A repository adopting dispat already carries its versions somewhere, and that
somewhere is the manifests. Without an entry for them dispat would start every package at `0.0.0` and release `0.0.1`,
throwing away the history the files know about, so compute offers the missing entries:

```console
+ initial core 1.4.2  packages/core/package.json declares 1.4.2; no release tag yet
```

An entry is proposed for a package only when all of this holds, and the last point is the one that keeps an established
repository quiet:

- Its manifests declare a version. Root manifests are asked first and nested ones only when no root manifest has an
  answer, the same rank that decides manifest names.
- They agree on it. Two root manifests declaring different versions is `W225`, and no baseline comes from them.
- The version is a plain semver release. Something that is not semver at all, and a prerelease such as
  `1.0.0-SNAPSHOT` (a version being worked toward rather than one released), are both passed over, as is `0.0.0`,
  which is already where a package with no entry starts.
- The config has no `initials` entry for it yet. An entry already there is your decision, and compute never rewrites
  one, whatever the manifests say.
- Its release tags cannot answer. The planner only ever reads `initials` when a package has no parseable stable tag,
  so that is the only case an entry is worth writing. A package with a readable release tag is silent, and a package
  whose newest tag matches the format but is not a version gets the suggestion with that tag named in the evidence.

The entries land in the top-level `initials` map with every entry already there left exactly as it is, spelling
included. To silence a suggestion for good, write the entry yourself: `"core": "0.0.0"` is a decision like any other
and compute will leave it alone. Without a git repository the baselines are skipped altogether, with one warning, and
the edges are computed as usual.

**How changes are applied.** Nothing is written by default. The listing puts the edges first and the baselines after
them, by package name. `--write` applies every suggestion, `--interactive` asks `y`/`N` per suggestion on stdin. Each
change is applied to the file that holds the declaration: additions go into the root config's top-level
`dependencies` object under their consumer, unless that consumer already declares its providers in a
`packages.<name>.dependencies` entry or its own in-folder file, in which case the addition joins them there. A removal
and a kind correction edit the declaring source in place, and a baseline goes into the root config's `initials` map. Everything one
file receives is written in a single pass, so a run that changes two of its keys still leaves one backup. Every edited
file is first copied to `<name>.backup` (untracked files worth a `.gitignore` entry; overwritten on every applying
run), and each write is atomic. A TOML file is not rewritten in place: `--write` prints a paste-ready block for it and
fails instead. `--check` overrides both apply modes: it writes nothing and exits `1` when any suggestion exists across
any source, which is the CI gate for a config lagging the manifests.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](./run.md#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](./run.md#choosing-the-packages).            |
| `--write`             |             | `compute` only: apply every suggestion to the config file (previous copy saved as `<name>.backup`).                                                                                                    |
| `--interactive`, `-i` |             | `compute` only: confirm each suggestion (`y`/`N` on stdin) before applying it; wins over `--write`.                                                                                                    |
| `--check`             |             | `compute` and `self-update`: report only, change nothing, and exit `1` when there is something to do. For `compute`, any suggestion at all, edges and baselines alike, which is the CI gate for a config lagging the manifests, and it overrides both apply modes.  |
