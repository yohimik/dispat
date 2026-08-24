# Tags and baselines

Versions live exclusively in annotated git tags. These options control how tags are spelled. They also set where a
package gets its baseline when tags cannot provide one.

You can give a package extra names beside its release tag. Use these for refs that follow a line rather than name a
release. See [Alias tags](./alias-tags.md).

## `tagFormat`

This is the template dispat uses to build and read release tags. It substitutes four placeholders. Every other byte is
literal:

| Placeholder | Meaning                                            |
|-------------|----------------------------------------------------|
| `{name}`    | The package name.                                  |
| `{version}` | The semver version, with no `v` prefix of its own. |
| `{channel}` | The prerelease channel, e.g. `beta`.               |
| `{counter}` | The prerelease counter, e.g. `4`.                  |

You must provide exactly one `{version}`. Zero leaves versions indistinguishable, and two makes parsing ambiguous.
dispat validates every format at load time with a render-and-read-back round trip.

`{major}`, `{minor}` and `{patch}` are **not** available here. A release tag must be readable back into the exact
version that made it, and `v1` names no release in particular. Those placeholders belong to
[alias tags](./alias-tags.md), which are only written.

You can include `{name}` any number of times, or omit it entirely. You can override the repository-wide format per
space or per package through a [per-package entry](./packages.md).

| Format                         | Example tag            |
|--------------------------------|------------------------|
| `{name}@{version}` *(default)* | `core@1.2.3`           |
| `{name}@v{version}`            | `core@v1.2.3`          |
| `services/{name}@v{version}`   | `services/core@v1.2.3` |

Use `{channel}` and `{counter}` to spell the prerelease out instead of leaving it inside `{version}`. This supports
conventions that do not write prereleases the way semver does. You must use them together, and they must follow
`{version}` in that order.

A counter with no channel cannot tell two trains apart. A channel with no counter gives every prerelease of a train the
same tag. Their presence narrows `{version}` to the `MAJOR.MINOR.PATCH` core.

A stable version has no channel to write. In that case, dispat drops the placeholders *and the literal text glued to
them*:

| Format                                 | Prerelease tag      | Stable tag    |
|----------------------------------------|---------------------|---------------|
| `{name}@{version}` *(default)*         | `core@1.2.3-beta.4` | `core@1.2.3`  |
| `{name}@v{version}-{channel}{counter}` | `core@v1.2.3-beta4` | `core@v1.2.3` |
| `{name}@{version}.{channel}.{counter}` | `core@1.2.3.beta.4` | `core@1.2.3`  |

Only the tag's shape changes. The version remains semver throughout, so `beta.10` still sorts above `beta.9`. A tag
read back always yields the semver spelling regardless of what the tag wrote.

The counter is not limited to the bare number the automatic train produces. An exact `Release-As: 2.0.0-rc.1.hotfix`
renders and reads back perfectly, treating everything after the channel as the counter.

If you leave out a literal between `{channel}` and `{counter}`, a fused `beta10` splits at the letter-digit boundary
into `beta` and `10`. A channel name ending in a digit like `rc2` breaks under a fused format. Give the two
placeholders a separator literal if you use channels like that.

You can set the format at the top level and override it per space. This lets a monorepo use the path form for Go
modules and the plain form for npm packages. The format dictates what a *later* run reads a package's baseline from.

Changing the format retroactively hides the existing history. Either re-tag or seed the new line with
[`initials`](#initials) before you change an established format.

dispat ignores tags that match a package's glob but not the format's literal text. It assumes they belong to someone
else's convention. A tag might match the shape but contain an unparseable version, which is the exact case
[`initials`](#initials) exists for.

## `initials`

This is a map of package name to a `MAJOR.MINOR.PATCH` version that dispat validates at load time. The value sets the
*baseline* the next release bumps from, but never becomes a release by itself. It applies in exactly two situations:

- The package has no matching tag at all. This happens on a first release.
- The newest matching tag by creation date exists, but its version cannot be parsed as semver. A stray `core@0.0.1.0`
  is a common example. dispat deliberately does *not* use older parseable tags here, and still scans commits from the
  unparseable tag rather than the whole history.

Take `"initials": {"core": "1.0.0"}` with an unparseable newest tag. If one `fix(core)` commit exists since that tag,
dispat releases `core@1.0.1`. Packages without an entry fall back to `0.0.0` as usual.

A parseable latest tag always beats initials. dispat matches keys case-insensitively against discovered packages
because viper lowercases map keys. It warns about and ignores entries that match no package.

You rarely have to write these by hand. Run [`dispat compute`](../cli/compute.md) to read the version each package's
manifests declare. It proposes entries for the exact packages in the two situations above.

Adopting dispat in a repository that already ships versions takes one `dispat compute --write` rather than a
transcription job. The command never overwrites an entry that is already there. Writing one yourself settles the
question for good.

An entry of `"core": "0.0.0"` says "this package starts from zero" and stays that way.
