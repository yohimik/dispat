# Shared versions

By default every package in a monorepo gets its own version, computed from its own history. That is usually what you
want. Sometimes it is not: a set of libraries released together, a plugin and the host it plugs into, a client and a
server that must never disagree about which generation they belong to. For those, dispat lets a set of packages hold
part of their version in common.

This page explains how much they can hold in common, what happens to a package that has not changed, and how to put
packages into such a set. The reference tables live in [space options](../configuration/spaces.md#versioning); this page
is the walkthrough.

## The two questions

Every shared-versioning mode is an answer to two questions.

**How much of the version is shared?** A version has three parts, `MAJOR.MINOR.PATCH`. A group can share the whole
thing, or just the leading `MAJOR.MINOR`, or just the `MAJOR`. Whatever is shared is always equal across the group.
Whatever is not shared belongs to each package alone, so a package can move it without anybody else moving.

**What happens to a package that has not changed?** When the shared part moves, a package with nothing of its own
either moves along with it, or stays where it is until it next has something to release. Modes that move it along are
the plain ones; modes that leave it behind are the `Sparse` ones.

Three answers to the first question, two to the second, and one mode that shares nothing at all:

| Mode                    | Shared         | Each package's own | An unchanged package     |
|-------------------------|----------------|--------------------|--------------------------|
| `independent` *(default)* | nothing      | everything         | stays where it is        |
| `fixed`                 | the whole version | nothing         | is released too          |
| `fixedSparse`           | the whole version | nothing         | stays where it is        |
| `fixedMajorMinor`       | major, minor   | patch              | is released too          |
| `fixedMajorMinorSparse` | major, minor   | patch              | stays where it is        |
| `fixedMajor`            | major          | minor, patch       | is released too          |
| `fixedMajorSparse`      | major          | minor, patch       | stays where it is        |

Which one to reach for:

* `fixed` when the version number is a badge of the release itself, and readers expect every package of the set to
  carry the same one. The cost is noise: a typo fix in one package publishes all of them.
* `fixedMajorMinor` when the set ships features together but patches separately. A bugfix in one library is that
  library's business; a new capability is the set's.
* `fixedMajor` when only compatibility is shared. Everything with the same major works together; below that, each
  package moves at whatever pace its own work demands.
* A `Sparse` variant of any of those when you want the shared part respected but do not want empty releases.
* `independent` when none of that applies, which is most of the time.

## What actually moves the group

The rule is short: **a release moves the whole group when it reaches the part the group shares.** A release that stays
below the shared part is the package's own, and nobody else hears about it.

| Mode              | A patch release moves | A minor release moves | A breaking release moves |
|-------------------|-----------------------|-----------------------|--------------------------|
| `fixed`           | the group             | the group             | the group                |
| `fixedMajorMinor` | one package           | the group             | the group                |
| `fixedMajor`      | one package           | one package           | the group                |

When the group does move, every member lands on the same version, and the parts below the shared one restart at zero.
Under `fixedMajor` a group going to major 2 lands on `2.0.0`, not on each package's own patch count carried forward.

## A worked example

A group of two packages, `core` and `ui`, with four commits arriving one after another. Only `core` ever changes.
`core` starts at `1.4.2` and `ui` at `1.4.0`, which is a state only the partial modes can reach.

| Commit             | `fixed`                 | `fixedMajorMinor`       | `fixedMajor`            |
|--------------------|-------------------------|-------------------------|-------------------------|
| `fix(core): ...`   | `core 1.4.3`, `ui 1.4.3` | `core 1.4.3`, ui unchanged | `core 1.4.3`, ui unchanged |
| `fix(core): ...`   | `core 1.4.4`, `ui 1.4.4` | `core 1.4.4`, ui unchanged | `core 1.4.4`, ui unchanged |
| `feat(core): ...`  | `core 1.5.0`, `ui 1.5.0` | `core 1.5.0`, `ui 1.5.0` | `core 1.5.0`, ui unchanged |
| `feat(core)!: ...` | `core 2.0.0`, `ui 2.0.0` | `core 2.0.0`, `ui 2.0.0` | `core 2.0.0`, `ui 2.0.0` |

Read the `fixedMajor` column downwards: `ui` sits still through two patches and a feature, then joins `core` on major
2 because that is the only part it promised to share. Read the `fixedMajorMinor` column and `ui` also comes along for
the feature. Read `fixed` and `ui` moves every single time.

The `Sparse` variants are the same table with every "and `ui ...`" replaced by "and ui unchanged". `ui` catches up on
its own next release instead.

## Sparse: staying behind, then rejoining

A plain mode keeps the promise by publishing. A sparse mode keeps it by waiting.

Take `fixedMajorSparse` with `core` at `1.4.2` and `ui` at `1.4.0`. A breaking change in `core` publishes `core 2.0.0`
and leaves `ui` alone: no tag, no changelog entry, no scripts run. `ui` is now on major 1 while the group is on major
2, which looks like a violation and is in fact the deal. The moment `ui` has anything of its own to release, however
small, it does not continue its old line. It joins the group's major at the start of a fresh line, so a bugfix in `ui`
publishes `ui 2.0.0`, not `ui 1.4.1`.

That is the whole trade. Sparse buys you a quiet release log at the cost of a window where the versions on disk do not
yet agree.

## The changelog entry a passenger gets

Under a plain mode, a package released only because the group moved is a real release. Its version stage runs, its
build and publish scripts run, it is tagged, and it gets a [changelog](../configuration/records.md#changelog) entry like
everybody else. What it does not get is somebody else's release notes. Commit scopes still decide which notes belong to
which package, so the passenger's entry is a single line saying why the version moved:

```markdown
## ui@2.0.0 (2026-08-11)

No changes — version bump to keep the versioning group on one major version.
```

The wording names the part that is actually shared, so a reader of a `fixedMajor` changelog is not told that the whole
version is common when only the major is. The same line becomes the body of the package's GitHub release.

## Putting packages in a group

The simplest group is a space. Give the space a `versioning` and every package in it shares versions:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "versioning": "fixedMajor"
    }
  }
}
```

Groups often do not follow folders, though. Two packages that build differently belong in different spaces and may
still want one major; one space may hold packages on completely different release cadences. For those, declare the
group by name at the top level and let members join it:

```json
{
  "versionGroups": {
    "platform": {
      "versioning": "fixedMajor"
    }
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "versionGroup": "platform"
    },
    "apps": {
      "path": "apps"
    }
  },
  "packages": {
    "shell": {
      "versionGroup": "platform"
    }
  }
}
```

Every package of `libs`, plus the single `shell` package out of `apps`, now shares a major as group `platform`. The
rest of `apps` stays independent.

Three things are worth knowing about joining:

* The declaration owns the mode. That is why `versionGroup` and `versioning` cannot both be set on the same space or
  package: a member is not allowed to contradict the group it joined.
* A `versionGroup` may also name another space, which joins that space's own implicit group. Group names and space
  names share one namespace, so a declaration cannot shadow a space, and an unknown name is an error rather than a
  silent no-op.
* Setting `versioning` on a single package overrides its space's without leaving the space's group. That is how you
  opt one package out entirely (`"versioning": "independent"`), and also how a group can end up with members asking to
  share different amounts. When that happens the group uses the deepest sharing any member asked for, because sharing
  the major and minor also shares the major, and reports it as `W213`.

The full reference for all of this is under
[versioning groups](../configuration/spaces.md#versioning-groups).

## Acting on a group

A group is a name you can point a command at, the same way you point one at a package or a space. That is `--group`,
short `-g`:

```sh
dispat status -g platform         # what a release of the group would do
dispat release -g platform        # release the group, and nothing else
dispat run test -g platform       # run one script across the group
dispat preview -g platform        # the pending notes of its members
```

It takes the same spellings as the other two selection flags: repeatable, comma separated, case insensitive, and `*`
globs over group names. `-g '*'` is every package that versions in a group, which leaves out every package that
versions on its own.

Naming the group is the difference between a clean partial release and a noisy one. A group moves as a unit, so
selecting one member of it releases a shared version that is briefly untrue for the others and dispat warns about it
(`W231`). Selecting the group takes every member at once, so there is nothing to warn about, and it passes `--strict`
where the single package would not. [Partial releases](./partial-releases.md) has the worked output.

Two details worth remembering. A space that versions as a group is a group name too, so `-g libs` works with no
`versionGroups` entry anywhere. And a group is a versioning relationship rather than a folder, so it can hold packages
from several spaces, or a standalone package that belongs to no space at all, and standing in a folder never selects a
group for you.

## Prereleases and pinned versions

Both follow the same rule as everything else: they move the group when they reach the shared part, and stay local when
they do not.

A prerelease train started by a breaking change in a `fixedMajor` group is the group's train. Every member goes to
`2.0.0-beta.0` together, later work takes all of them to `beta.1`, and a graduation on any one member ends the train
for all of them. A prerelease train started by a *patch* in the same group belongs to the package that started it, and
nobody else joins.

An exact [`Release-As`](../reference/commits.md) works the same way. `Release-As: 2.0.0` in a `fixedMajor` group at major 1 names
a different major, so it pins the whole group's version. `Release-As: 1.7.0` in the same group names the major it is
already on, so it pins that one package and leaves the rest untouched.

## What you see in the log

| Code   | Meaning                                                                                                        |
|--------|----------------------------------------------------------------------------------------------------------------|
| `W210` | A package was released with nothing of its own, to keep the group together. Also raised when a package that fell behind is caught up to the shared part. |
| `W211` | Two exact `Release-As` pins both named the group's shared part. The newest wins.                                |
| `W212` | Members resolved to different prerelease channels while the group was moving as one, so a single winner is picked. |
| `W213` | Members asked to share different parts of the version. The group uses the deepest, which satisfies all of them.  |

`W210` cannot be suppressed. Nothing in the commit log explains why that package is in the plan, so the warning is the
only place a reader can find out.

Two convergence properties hold under every mode, and are worth checking if something looks wrong. A group whose
members already agree on the shared part releases nothing on a quiet run. And under a plain (non-sparse) mode, a member
that fell behind the group, because an earlier release failed or because the group was formed out of unequal versions,
is caught up on the next run rather than staying behind for ever.

## Where to go next

* [Space options](../configuration/spaces.md#versioning) for the reference table and the exact rules.
* [Package options](../configuration/packages.md#package-options) for overriding a single package's mode.
* [Release records](../configuration/records.md#changelog) for what lands in a changelog and a GitHub release.
* [Commit messages](../reference/commits.md) for `Release-As`, channels and the rest of the directive vocabulary.
