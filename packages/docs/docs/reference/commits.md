# Commit message reference

Messages are parsed by [`pkg/ccme`](https://github.com/yohimik/dispat/tree/main/pkg/ccme). A message holds one or more **units** separated by a line of
`---`; each has its own header, body and footers.

```
<type>[(<scope-set>)][<directives>][!]: <description>

[body]

[footers]
```

The type decides the bump (overridable via [`parser.types`](../configuration/parser.md#parser)):

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

With no parentheses at all the set is file-derived, by longest matching path prefix: a file under a package nested
inside another belongs to the inner one only. A package that declares a [`src`](../configuration/packages.md#src) owns
only what sits under that sub-folder, so a change elsewhere in its folder does not address it. A unit resolving to no
package is inert and reported as such.

## Inline directives

Written between the scope-set and the `:`. Every one has an equivalent footer; stating both is redundant and
contradicting is an error.

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

Both depths default to `0`, and neither bounds the other: a unit reaches nobody on either axis until it says so.

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
| `Reverts: <sha>`                       | Informational.                                                                                |

## Channels and prereleases

A package's channel comes from its baseline tag: `1.5.0-beta.3` is on `beta`, `1.4.2` is on `stable`, and an untagged
package is on `stable`. Prerelease versions are `<major>.<minor>.<patch>-<channel>.<counter>` with a separate numeric
counter, so `beta.10` sorts above `beta.9`.

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

A `<from>><to>` transition matches against the *baseline* channel, which makes it idempotent: packages that already
graduated do not match, so the same directive is correct on the first run and the fifth. A bare `stable` arriving by
propagation never graduates a dependant; only a direct directive or a transition naming the train does, so graduation
cannot happen by accident.

A train converges the same way stable releases do: work a prerelease has already published cannot release again. The
version of the *next* prerelease (and of the graduation) is still computed over the whole train (a breaking change
shipped in `beta.0` keeps the target at the next major), but with no new commits since the last prerelease tag a re-run
finds nothing to do, a `Release-As` consumed by a prerelease is no longer in force, and a `cancel` cannot retract
train-published work (a *live* cancel aimed at published work warns `W170`). Convergence is quiet: the directive that
started the train is contained in the tag it produced, so it is not re-reported as "already on beta"
(`W199`) on later runs (that warning is reserved for a *fresh* directive pointing where the package already is), and a
spent cancel (one every package it names has released past) is not re-reported either.

## Release control

| Written                                    | Effect                                                                                   |
|--------------------------------------------|------------------------------------------------------------------------------------------|
| `release(<pkg>)` + `Release-As: none`      | Hold: the bump is retained and reported, not released, until a later directive lifts it. |
| `release(<pkg>)` + `Release-As: auto`      | Resume: release at the `max()` of everything accumulated, catch-up included.             |
| `release(<pkg>)` + `Release-As: <version>` | Pin an exact version.                                                                    |
| `cancel(<pkg>)`                            | Discard the package's unreleased metadata. Irreversible; never reaches a published tag.  |

A pin is rejected if it does not move the package forward, if it is below what the pending commits require, or if it
raises the major version more than one above the computed version. A rejected pin has the unit-scoped blast radius of
any other commit error: the error is reported (and, under `commitErrors: "error"`, stops the run), the bad directive
contributes nothing, and the package falls back to its ordinarily computed version. A `feat` sharing the commit with a
bad pin still releases at its computed bump, and a lone rejected pin releases nothing. A pin sets the version, never the
bump; how large a change is, is declared by the type. The version also decides the channel, so
`Release-As: 2.0.0-rc.0` enters the rc line and `Release-As: 2.0.0` graduates.

`cancel` only reaches backwards: it discards contributions from commits that are ancestors of the cancel, and work
landing afterwards accumulates normally. A `cancel` on a provider that has already published is a no-op and says so:
the version is public, and the right target for stopping a pending catch-up is the **consumer**.
