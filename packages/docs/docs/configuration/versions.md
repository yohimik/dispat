# Tags and baselines

Versions live exclusively in annotated git tags; these options control how tags are spelled and where a package's
baseline comes from when its tags cannot provide one.

## `tagFormat`

The template release tags are built from and read back with. Four placeholders are substituted; every other byte is
literal:

| Placeholder | Meaning                                            |
|-------------|----------------------------------------------------|
| `{name}`    | The package name.                                  |
| `{version}` | The semver version, with no `v` prefix of its own. |
| `{channel}` | The prerelease channel, e.g. `beta`.               |
| `{counter}` | The prerelease counter, e.g. `4`.                  |

Exactly one `{version}` is required (none leaves every version indistinguishable, more than one makes parsing
ambiguous), and every format is validated at load time, including a render-and-read-back round trip. `{name}` may appear
any number of times, including none. The repository-wide format is overridable per space and, through a
[per-package entry](./packages.md), per package.

| Format                         | Example tag            |
|--------------------------------|------------------------|
| `{name}@{version}` *(default)* | `core@1.2.3`           |
| `{name}@v{version}`            | `core@v1.2.3`          |
| `services/{name}@v{version}`   | `services/core@v1.2.3` |

`{channel}` and `{counter}` spell the prerelease out instead of leaving it inside `{version}`, for the conventions that
do not write it the way semver does. They are used together (a counter with no channel cannot tell two trains apart, a
channel with no counter gives every prerelease of a train the same tag), must follow `{version}` in that order, and
their presence narrows `{version}` to the `MAJOR.MINOR.PATCH` core. On a stable version there is no channel to write, so
the placeholders *and the literal text glued to them* are dropped:

| Format                                 | Prerelease tag      | Stable tag    |
|----------------------------------------|---------------------|---------------|
| `{name}@{version}` *(default)*         | `core@1.2.3-beta.4` | `core@1.2.3`  |
| `{name}@v{version}-{channel}{counter}` | `core@v1.2.3-beta4` | `core@v1.2.3` |
| `{name}@{version}.{channel}.{counter}` | `core@1.2.3.beta.4` | `core@1.2.3`  |

Only the tag's shape changes: the version is semver throughout, `beta.10` still sorts above `beta.9`, and a tag read
back yields the semver spelling whatever the tag wrote. The counter is not limited to the bare number the automatic
train produces: an exact `Release-As: 2.0.0-rc.1.hotfix` renders and reads back too, the counter being everything after
the channel. With no literal between `{channel}` and `{counter}`, a fused `beta10` splits at the letter-digit boundary
(`beta`/`10`); a channel name that itself ends in a digit (`rc2`) would be misread under such a format, so give the two
placeholders a separator literal if you use channels like that.

Set it at the top level for the repository and override it per space, so a monorepo whose Go modules want the path form
and whose npm packages want the plain one can have both. The format is what a *later* run reads a package's baseline
from, so changing it retroactively hides the existing history: either re-tag, or seed the new line with
[`initials`](#initials).

Tags that match a package's glob but not the format's literal text belong to someone else's convention and are ignored.
A tag that matches the shape but whose version cannot be parsed is the case [`initials`](#initials) exists for.

## `initials`

A map of package name → `MAJOR.MINOR.PATCH` (validated at load time). The value is the *baseline* the next release bumps
from; it never becomes a release by itself. It applies in exactly two situations:

- the package has no matching tag at all (a first release), or
- the newest matching tag (by creation date) exists but its version cannot be parsed as semver, e.g. a stray
  `core@0.0.1.0`. In that case older parseable tags are deliberately *not* used, and commits are still scanned from the
  unparseable tag (not the whole history).

Example: `"initials": {"core": "1.0.0"}` with an unparseable newest tag and one `fix(core)` commit since it releases
`core@1.0.1`. Packages without an entry fall back to `0.0.0` as usual. A parseable latest tag always beats initials.
Keys are matched case-insensitively against discovered packages (viper lowercases map keys); entries matching no package
are warned about and ignored.

You rarely have to write these by hand. [`dispat compute`](../cli.md#the-compute-command) reads the version each
package's manifests declare and proposes the entries for exactly the packages in one of the two situations above, so
adopting dispat in a repository that already ships versions is one `dispat compute --write` rather than a transcription
job. What it will not do is overwrite an entry that is already there, which is what makes writing one yourself the way
to settle the question for good: `"core": "0.0.0"` says "this package starts from zero" and is left alone from then on.
