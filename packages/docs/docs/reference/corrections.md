# Correcting a release record

A commit message is a release record. The type decides the bump, the description becomes the changelog entry, and the
footers decide what travels to consumers. That works well until the message is wrong, and by then it is usually
pushed, which puts it out of reach of `git commit --amend`.

Corrections fix such a record from a later commit. You name the record you got wrong and either replace it or throw it
away:

```
Edits:   <sha>      restate that record as this commit's own
Deletes: <sha>      discard that record, and put nothing in its place
```

Both are ordinary footers on an ordinary commit. The commit around them keeps its type, its scope-set, its
description and its directives, and releases like any other commit.

## The shortest example

You classified a refactor as a breaking feature, and nothing has shipped yet:

```sh
git log --oneline -1
# 8cb07a5 feat(core)!: rewrite internals
```

Left alone, that takes `core` to `2.0.0`. Write a commit that says what the change actually was, and point it at the
one you got wrong:

```
fix(core): rewrite internals

The change is a refactor with a defensive fix, not a breaking feature.

Edits: 8cb07a5
```

Now `core` releases `0.1.1`, and the changelog carries the new description rather than the old one:

```
## core@0.1.1

### Fixes

- rewrite internals (corrects 8cb07a5944b1)
The change is a refactor with a defensive fix, not a breaking feature.
```

The correction commit is usually empty, which is fine. `git commit --allow-empty` is the normal way to write one.

## What a correction can reach

Two rules decide this, and between them they cover almost every question you will have.

**A correction reaches only work that has not been released.** Releasing writes a tag, and a tag is a promise. Once
`core` has shipped `2.0.0`, the record that caused it is public history, and no later commit takes it back. dispat
does not fail the run over this; it tells you the correction did nothing:

```
W209  Edits: f8c56e0 addresses no pending record; released work is history and cannot be corrected (§7.4.2)
```

That warning cannot be turned off, not even by `--quiet-parser`. Somebody who writes a correction has to find out
that it did not take, because the alternative is believing the record was fixed when it was not. If the record has
already shipped, the way forward is a new release that says what changed, not a rewrite of the old one.

**A correction reaches only earlier commits.** The target must be an ancestor of the commit carrying the correction.
A commit cannot correct itself or anything after it, which is what makes it safe to discard several old records and
supply their replacement in a single commit. Naming a commit that is not an earlier one is `E210`.

## Correcting part of a record

A record is a claim about specific packages, so a correction and its target have scopes that must line up. The rule is
containment: your correction may name fewer packages than its target, never more.

Say one commit was scoped to the whole workspace:

```
feat(*)!: one record for every package
```

A correction scoped to a single package fixes it for that package alone, and every other package keeps the original:

```
fix(core): smaller than that, for core

Edits: 4f2a1c9
```

`core` now carries the `fix`; the rest of the workspace still carries the `feat!`. Going the other way is refused:

```
E213  correction names utils, which its target's record does not; a correction may narrow a record, never widen it
```

The reason for the asymmetry is ownership. Narrowing only touches records that already covered the packages you named.
Widening would extend somebody else's record to packages it never claimed, which is a way to release a package by
accident.

**When you write no scope-set at all**, the correction takes the packages of the records it targets. This is the
recommended form, and it is why the short example above works: the correction commit is empty, so there are no changed
files to derive a scope from, and the targets already say which packages are meant.

## Clearing a whole scope

`*` in place of a sha means every record still pending for the packages the commit names:

```
chore(core): start the ledger over

Deletes: *
```

`chore` maps to a bump of `none`, so nothing replaces what went. This is the tool for a ledger full of records that
should never have existed, typically after importing history from another tool.

The wildcard reaches back from the correction's own commit and no further, so anything committed afterwards is
unaffected. It also leaves `cancel` barriers and `release` directives alone: those carry no record to discard.

## Several corrections of the same record

The newest one wins. Written as a sequence, each correction supersedes the one before it, and the superseded one is
reported:

```
W210  Deletes: 4f2a1c9 was already corrected by the newer commit 9de20b1; the newest correction wins
```

So a `Deletes` followed later by an `Edits` of the same target ends with the restatement in force, which is what
reading the history top to bottom would suggest.

## Undoing a correction

A correction is an ordinary commit, so it has a record of its own, and that record can be corrected too. Deleting a
correction's record voids the correction: everything it did stops applying, and whatever it discarded comes back.

```
chore(core): that correction was wrong

Deletes: <sha of the correction>
```

dispat reports the voiding per package:

```
W215  correction is void here: a newer correction discarded its own record for this package
```

This is the undo, and it composes to any depth. Deleting a delete restores its target. Deleting an edit restores the
original and drops the restatement. There is no cycle to worry about, because a correction only ever names earlier
commits.

One thing to watch: an `Edits` aimed at a correction replaces *that correction's own record*, not the record it was
correcting. If you want to restate the original again, aim at the original. A newer `Edits` of the same target
supersedes the older one directly, which is almost always what you meant.

## Reverts and the changelog

`revert` is a different tool from the two footers here, and the difference is which layer it works on. A revert
changes code: the commit contains the inverse diff. A correction changes metadata and touches no files.

For the version, `Reverts:` is informational. A `feat!` and a revert of it in one window still take the package to a
major, because `max()` sees both and because consumers may already have seen the feature in a prerelease. Silently
downgrading would be worse than the extra major.

For the changelog it is not informational at all. When the reverted commit has not been released, both entries are
suppressed, since the release contains neither the change nor its removal:

```
W212  revert and the entries of 4f2a1c9 leave the changelog together; both still count toward the bump
```

If the reverted commit has already shipped, nothing is suppressed. Readers were told about the change, so they should
be told it is going away, and the revert appears in the changelog as usual.

Two degraded forms leave the footer purely informational, and the release goes ahead in both:

| Written | Reported | Why |
|---------|----------|-----|
| `Reverts: not-a-sha` | `W214` | The value is not a commit id, so there is nothing to look up. |
| `Reverts: <sha>` naming an unreachable commit | `W213` | The id is well-formed but names no commit reachable from the revert. |

To drop the version signal as well as the entries, pair the revert with `Deletes:` against the reverted commit, or
with a `cancel`.

## Which tool for which job

These form a ladder from the gentlest to the most destructive:

| Tool | What it undoes | Reaches |
|------|----------------|---------|
| `revert` | The code | Nothing about the record, except the two changelog entries |
| `Release-As: none` | Nothing, yet | Holds the release until a later directive lifts it |
| `Edits:` | One record's contents | The commits you name |
| `Deletes:` | One record | The commits you name |
| `cancel` | The whole pending ledger | Everything behind the barrier, irreversibly |

Reach for `Edits` when the work was real and the message was wrong. Reach for `Deletes` when the record should not
exist. Reach for `cancel` only when a package's whole unreleased ledger is wrong, because unlike a correction it names
nothing and cannot be undone.

## Writing the target

The sha is 7 to 64 lowercase hexadecimal characters, full or abbreviated, and dispat resolves an abbreviation the way
git does. `git rev-parse --short HEAD` after the commit you mean is the usual way to get one.

If the commit carries several units, separated by lines of `---`, a bare sha does not say which record you mean. Add
the unit's 1-based position in the message:

```
Deletes: 4f2a1c9#2
```

Counting is over the message, not over the units that parsed, so a broken unit still takes up its position. A bare sha
on a multi-unit commit, or a position past the end, is `E211`.

## What each diagnostic means

The four errors are unit-scoped: the offending commit contributes nothing at all, and every other commit in the run is
unaffected. Under the default [`commitErrors: "warn"`](../configuration/parser.md) the release still goes ahead
without it.

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

`dispat status` computes the same plan a release does and changes nothing, so it is the safe way to see whether a
correction landed. A package whose entries were corrected says so:

```
INF ● changed bump=patch corrected=1 ownCommits=1 package=core version="0.1.0 -> 0.1.1"
```

`corrected` counts the entries this release restates, and `suppressedNotes` counts the entries a revert took out.
`--log-level trace` goes further and prints every correction, its targets, and each record it discarded, per package.
