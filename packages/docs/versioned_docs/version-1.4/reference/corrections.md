# Correcting a release record

A commit message is a release record. The type decides the bump, the description becomes the changelog entry, and the
footers decide what travels to consumers. This works well until you push a wrong message and put it out of reach of
`git commit --amend`.

Write a correction to fix a record from a later commit. You name the wrong record and either replace it or throw it
away:

```
Edits:   <sha>      restate that record as this commit's own
Deletes: <sha>      discard that record, and put nothing in its place
```

Both are ordinary footers on an ordinary commit. The commit around them keeps its type, its scope-set, its description,
and its directives. It releases like any other commit.

## The shortest example

Assume you classified a refactor as a breaking feature and nothing has shipped yet:

```sh
git log --oneline -1
# 8cb07a5 feat(core)!: rewrite internals
```

Left alone, that takes `core` to `2.0.0`. Write a commit that says what the change actually was. Point it at the one
you got wrong:

```
fix(core): rewrite internals

The change is a refactor with a defensive fix, not a breaking feature.

Edits: 8cb07a5
```

Now `core` releases `0.1.1`. The changelog carries the new description rather than the old one:

```
## core@0.1.1

### Fixes

- rewrite internals (corrects 8cb07a5944b1)
The change is a refactor with a defensive fix, not a breaking feature.
```

Run `git commit --allow-empty` to write a correction without changing files. A correction commit is usually empty,
which is fine.

## What a correction can reach

Two rules decide this.

**A correction reaches only work that has not been released.** Releasing writes a tag, and a tag is a promise. Once
`core` ships `2.0.0`, the record that caused it becomes public history. No later commit takes it back. dispat does not
fail the run over this, but it tells you the correction did nothing:

```
W209  Edits: f8c56e0 addresses no pending record; released work is history and cannot be corrected (§7.4.2)
```

You cannot turn that warning off, not even with `--quiet-parser`. You must know when a correction fails so you do not
believe a record was fixed when it was not. Publish a new release to explain the change if the record has already
shipped.

**A correction reaches only earlier commits.** The target must be an ancestor of the commit carrying the correction. A
commit cannot correct itself or anything after it. This makes it safe to discard several old records and supply their
replacement in a single commit. Naming a commit that is not an earlier one yields `E210`.

## Correcting part of a record

A record is a claim about specific packages, so a correction and its target have scopes that must line up. The rule is
containment. Your correction may name fewer packages than its target, never more.

Assume one commit was scoped to the whole workspace:

```
feat(*)!: one record for every package
```

Scope a correction to a single package to fix it for that package alone. Every other package keeps the original:

```
fix(core): smaller than that, for core

Edits: 4f2a1c9
```

`core` now carries the `fix`. The rest of the workspace still carries the `feat!`. dispat refuses corrections going the
other way:

```
E213  correction names utils, which its target's record does not; a correction may narrow a record, never widen it
```

The reason for the asymmetry is ownership. Narrowing only touches records that already covered the packages you named.
Widening would extend a record to packages it never claimed, which releases a package by accident.

**When you write no scope-set at all**, the correction takes the packages of the records it targets. This is the
recommended form, and it is why the short example above works. The correction commit is empty, so there are no changed
files to derive a scope from. The targets already say which packages are meant.

## Clearing a whole scope

Use `*` in place of a sha to target every record still pending for the packages the commit names:

```
chore(core): start the ledger over

Deletes: *
```

`chore` maps to a bump of `none`, so nothing replaces the deleted records. Use this to clear a ledger full of records
that should never have existed. This typically happens after importing history from another tool.

The wildcard reaches back from the correction's own commit and no further. Anything committed afterwards is unaffected.
It also leaves `cancel` barriers and `release` directives alone, because those carry no record to discard.

## Several corrections of the same record

The newest one wins. Write corrections as a sequence to have each supersede the one before it. dispat reports the
superseded correction:

```
W210  Deletes: 4f2a1c9 was already corrected by the newer commit 9de20b1; the newest correction wins
```

A `Deletes` followed later by an `Edits` of the same target ends with the restatement in force. This matches what
reading the history top to bottom suggests.

## Undoing a correction

A correction is an ordinary commit, so it has a record of its own. You can correct that record too. Delete a
correction's record to void the correction. Everything it did stops applying, and whatever it discarded comes back.

```
chore(core): that correction was wrong

Deletes: <sha of the correction>
```

dispat reports the voiding per package:

```
W215  correction is void here: a newer correction discarded its own record for this package
```

This is the undo, and it composes to any depth. Delete a delete to restore its target. Delete an edit to restore the
original and drop the restatement. You do not need to worry about cycles, because a correction only ever names earlier
commits.

An `Edits` aimed at a correction replaces *that correction's own record*, not the record it was correcting. Aim at the
original commit if you want to restate the original again. A newer `Edits` of the same target supersedes the older one
directly.

## Reverts and the changelog

`revert` is a different tool from the two footers here. The difference is which layer it works on. A revert changes
code, so the commit contains the inverse diff. A correction changes metadata and touches no files.

`Reverts:` is informational for the version. A `feat!` and a revert of it in one window still take the package to a
major. `max()` sees both, and consumers may already have seen the feature in a prerelease. Silently downgrading is
worse than the extra major.

`Reverts:` is not informational for the changelog. Both entries are suppressed when the reverted commit has not been
released. The release contains neither the change nor its removal:

```
W212  revert and the entries of 4f2a1c9 leave the changelog together; both still count toward the bump
```

Nothing is suppressed if the reverted commit has already shipped. Readers were told about the change, so they should be
told it is going away. The revert appears in the changelog as usual.

Two degraded forms leave the footer purely informational. The release goes ahead in both:

| Written | Reported | Why |
|---------|----------|-----|
| `Reverts: not-a-sha` | `W214` | The value is not a commit id, so there is nothing to look up. |
| `Reverts: <sha>` naming an unreachable commit | `W213` | The id is well-formed but names no commit reachable from the revert. |

Pair the revert with `Deletes:` against the reverted commit, or with a `cancel`, to drop the version signal as well as
the entries.

## Which tool for which job

These tools form a ladder from the gentlest to the most destructive:

| Tool | What it undoes | Reaches |
|------|----------------|---------|
| `revert` | The code | Nothing about the record, except the two changelog entries |
| `Release-As: none` | Nothing, yet | Holds the release until a later directive lifts it |
| `Edits:` | One record's contents | The commits you name |
| `Deletes:` | One record | The commits you name |
| `cancel` | The whole pending ledger | Everything behind the barrier, irreversibly |

Use `Edits` when the work was real and the message was wrong. Use `Deletes` when the record should not exist. A
`cancel` names nothing and cannot be undone. Use it only when a package's whole unreleased ledger is wrong.

## Writing the target

The sha is 7 to 64 lowercase hexadecimal characters, full or abbreviated. dispat resolves an abbreviation the way git
does. Run `git rev-parse --short HEAD` after the commit you mean to get one.

A bare sha does not say which record you mean if the commit carries several units separated by lines of `---`. Add the
unit's 1-based position in the message:

```
Deletes: 4f2a1c9#2
```

Counting is over the message, not over the units that parsed. A broken unit still takes up its position. A bare sha on
a multi-unit commit, or a position past the end, yields `E211`.

## What each diagnostic means

The four errors are unit-scoped. The offending commit contributes nothing at all, and every other commit in the run is
unaffected. The release still goes ahead without it under the default
[`commitErrors: "warn"`](../configuration/parser.md).

| Code | Means |
|------|-------|
| `E210` | The target names no commit, names more than one, or is not an earlier commit than the correction. |
| `E211` | The unit position is past the end of the target commit, or a bare sha names a commit carrying several units. |
| `E212` | The target is a `cancel` or `release` unit, neither of which carries a record. |
| `E213` | The correction names a package its target's record does not. |
| `W209` | The correction found nothing to act on: released, already discarded, or a `*` over a scope with nothing pending. Cannot be suppressed. |
| `W210` | A newer correction of the same target supersedes this one. |
| `W211` | An `Edits` restating its target with the same type, marker and description, so nothing changes. |
| `W212` | A revert and the entries it reverted left the changelog together. |
| `W213` | A `Reverts` value naming no reachable commit. Informational. |
| `W215` | The correction is void for a package, because a newer correction discarded its own record there. |

## Reading the plan

Run `dispat status` to compute the same plan a release does. It changes nothing, so it is the safe way to see whether a
correction landed. A package whose entries were corrected says so:

```
INF ● changed bump=patch corrected=1 ownCommits=1 package=core version="0.1.0 -> 0.1.1"
```

`corrected` counts the entries this release restates. `suppressedNotes` counts the entries a revert took out. Pass
`--log-level trace` to print every correction, its targets, and each record it discarded per package.
