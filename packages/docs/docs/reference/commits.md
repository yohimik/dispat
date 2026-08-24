# Commit message reference

Write your commit messages in a strict superset of Conventional Commits that adds the monorepo dimension. This format
tells dispat which packages a change releases, how far it reaches, and which channel it lands on.

The [`pkg/ccme`](../go/ccme.md) package parses your messages. A message holds one or more **units** separated by a line
of `---`. Each unit has its own header, body, and footers.

```
<type>[(<scope-set>)][<directives>][!]: <description>

[body]

[footers]
```

The type decides the version bump. You can override these defaults in
[`parser.types`](../configuration/parser.md#parser):

| Type                    | Bump  |
|-------------------------|-------|
| `feat`                  | minor |
| `fix`, `perf`, `revert` | patch |
| any other known type    | none  |
| any type with `!`       | major |

## Scope sets

| Term      | Resolves to                                       | Unknown name                  |
|-----------|---------------------------------------------------|-------------------------------|
| `core`    | that package                                      | error                         |
| `core,ui` | both                                              | error                         |
| `*`       | every package in the workspace                    | (n/a)                         |
| `@acme/*` | every package matching the glob                   | warning if it matches nothing |
| `.`       | the packages owning the commit's changed files    | (n/a)                         |
| `-app`    | removes `app` from the set; exclusions always win | warning                       |

Omit the parentheses to derive the set from changed files using the longest matching path prefix, meaning a file under
a nested package belongs to the inner package only. A package declaring a [`src`](../configuration/packages.md#src)
owns only the files inside that sub-folder. dispat reports a unit as inert if it resolves to no packages.

## Inline directives

Write inline directives between the scope-set and the `:`. Every directive has an equivalent footer. Stating both is
redundant, and contradicting them causes an error.

| Written  | Footer equivalent            | Meaning                                                     |
|----------|------------------------------|-------------------------------------------------------------|
| `!`      | `BREAKING CHANGE: <text>`    | Raises the unit's own bump to major.                        |
| `^`      | `Propagate-Depth: 1`         | Reach direct consumers.                                     |
| `^^`     | `Propagate-Depth: all`       | Reach the whole transitive closure.                         |
| `+N`     | `Propagate-Depth: N`         | Reach consumers up to N edges away. `+0` is no propagation. |
| `^minor` | `Propagate: minor`           | The bump consumers take. Default `patch`.                   |
| `^none`  | `Propagate: none`            | Propagate nothing, whatever the depth.                      |
| `%beta`  | `Channel: beta`              | The unit's own packages move to that channel.               |
| `%%beta` | `Propagate-Channel: beta`    | The channel handed to the consumers reached below.          |
| `++N`    | `Propagate-Channel-Depth: N` | How far the channel travels. Default `0`.                   |

Both depths default to `0`, and neither bounds the other. A unit reaches nobody on either axis until you say so.

![The same commit planned twice: as feat(core) only core releases, and amended to feat(core)^^ the whole consumer closure joins the plan while utils, a provider, stays unchanged.](/demo-blast.gif)

## Footers

| Footer                                 | Effect                                                                                        |
|----------------------------------------|-----------------------------------------------------------------------------------------------|
| `BREAKING CHANGE: <text>`              | Major bump plus the text in the changelog. Case is significant; near-misses are warned about. |
| `Propagate: <bump>`                    | `none`, `patch`, `minor`, `major` or `inherit` (copy the unit's own bump).                    |
| `Propagate-Depth: <N\|all>`            | Bump-axis reach.                                                                              |
| `Propagate-Scope: <scope-set>`         | Intersects the reached set. If nothing survives, a warning and no propagation.                |
| `Propagate-Channel: <value>`           | `inherit` (default), `none`, `stable`, a channel name, or a `<from>><to>` transition.         |
| `Propagate-Channel-Depth: <N\|all>`    | Channel-axis reach.                                                                           |
| `Propagate-Channel-Scope: <scope-set>` | Restricts the channel axis. Defaults to `Propagate-Scope`.                                    |
| `Channel: <value>`                     | The unit's own channel, same grammar.                                                         |
| `Release-As: <none\|auto\|version>`    | Release control; see below.                                                                   |
| `Reverts: <sha>`                       | Informational for the bump; suppresses both changelog entries. See [corrections](./corrections.md). |
| `Edits: <sha[#n]\|*>`                  | Restate the records named, as this unit. See [corrections](./corrections.md).                 |
| `Deletes: <sha[#n]\|*>`                | Discard the records named. See [corrections](./corrections.md).                               |

## Channels and prereleases

dispat reads a package's channel from its baseline tag. For example, `1.5.0-beta.3` is on `beta`, `1.4.2` is on
`stable`, and an untagged package is on `stable`. Prerelease versions use a
`<major>.<minor>.<patch>-<channel>.<counter>` format with a separate numeric counter, so `beta.10` sorts above
`beta.9`.

```
feat(core)%beta:            core enters the beta line; nothing else moves
feat(core)^%beta:           the same: the caret reaches the consumers and every
                            one is suppressed, because a stable consumer cannot
                            resolve a beta release
feat(core)^%beta++1:        core and its direct consumers enter beta together
feat(core)^:                an established train stays on beta with no directive
release(core)%stable:       graduate core
release(core)%beta>stable%%beta>stable++*:
                            graduate core and everything still on beta behind it
```

A `<from>><to>` transition matches against the *baseline* channel, making the directive idempotent. Packages that
already graduated do not match, so the same directive works perfectly on the first run and the fifth. A bare `stable`
arriving by propagation never graduates a dependant, because only a direct directive or a transition naming the train
can do that.

A train converges exactly like stable releases do, meaning work a prerelease has already published cannot release
again, and a re-run with no new commits finds nothing to do. dispat computes the version of the *next* prerelease and
the graduation over the whole train, so a breaking change shipped in `beta.0` keeps the target at the next major. A
`Release-As` consumed by a prerelease loses force, and a `cancel` cannot retract train-published work (a *live* cancel
aimed at published work warns `W170`).

Convergence happens quietly. The tag contains the directive that started the train, so dispat does not re-report it as
"already on beta" (`W199`) on later runs. That warning is reserved for a *fresh* directive pointing where the package
already is, and a spent cancel is not re-reported either.

## Release control

| Written                                    | Effect                                                                                   |
|--------------------------------------------|------------------------------------------------------------------------------------------|
| `release(<pkg>)` + `Release-As: none`      | Hold: the bump is retained and reported, not released, until a later directive lifts it. |
| `release(<pkg>)` + `Release-As: auto`      | Resume: release at the `max()` of everything accumulated, catch-up included.             |
| `release(<pkg>)` + `Release-As: <version>` | Pin an exact version.                                                                    |
| `cancel(<pkg>)`                            | Discard the package's unreleased metadata. Irreversible; never reaches a published tag.  |

dispat rejects a pin if it fails to move the package forward, falls below what pending commits require, or raises the
major version more than one above the computed version. A rejected pin has a unit-scoped blast radius: dispat reports
the error (stopping the run under `commitErrors: "error"`), ignores the bad directive, and falls back to the computed
version while leaving other valid units unaffected. A pin sets the exact version and channel, so
`Release-As: 2.0.0-rc.0` enters the rc line and `Release-As: 2.0.0` graduates, but the commit type still declares the
bump size.

A `cancel` is irreversible and only reaches backwards. It discards contributions from ancestor commits, but work
landing afterwards accumulates normally. A `cancel` on a provider that has already published is a no-op and says so,
because the version is public and the right target for stopping a pending catch-up is the **consumer**.

A `cancel` names nothing and discards everything behind it. To act on one record rather than the whole ledger, `Edits`
restates a named commit's record and `Deletes` discards it. Both directives reach only unreleased work and can be
undone by a later correction, as explained in [Correcting a release record](./corrections.md).
