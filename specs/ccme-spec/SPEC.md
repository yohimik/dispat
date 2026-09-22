# Conventional Commits: Monorepo Extension (CCME)

**Version:** 3.1.0-rc.2 **Status:** Normative specification; new protocol implementation pending **Extends:** Conventional
Commits 1.0.0 **Versioning model:** Semantic Versioning 2.0.0 **Version store:** immutable VCS release records;
Git tags of the form `<package>@<version>` by default
**Conformance:** §17 · **Security considerations:** §18 · **Test vectors:** Appendix B
**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE).

Requirement levels follow RFC 2119 (§2). This specification is itself versioned under SemVer; see §17.3 for what
constitutes a patch, minor, and major revision of the document.

**Implementation boundary.** The published CCME 3 line specifies VCS adapters and explicit rollback; the current
source also defines the optional polyrepository Git profile of §27 and distributed execution profile of §28.
The dispat release engine implements the distributed profile over a Git mailbox transport; what it implements, where it
departs from §28 and what it has measured are stated by date in [DESIGN-HISTORY.md](./DESIGN-HISTORY.md), and no
speedup is claimed. Implementing one profile does not implement the others or the adapter and rollback protocols.
Neither changes the message parser grammar. The version markers are stamped by the specification release process.
Publishing a specification does not implement its behavior. The immutable
[CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md)
remains the reference for existing CCME 2 consumers. New protocol examples MUST NOT be presented as runnable dispat
configuration for external adapters or rollback. The dated design history is in
[DESIGN-HISTORY.md](./DESIGN-HISTORY.md).

---

## Table of contents

1. [Summary](#1-summary)
2. [Conventions and terminology](#2-conventions-and-terminology)
3. [Relationship to Conventional Commits 1.0.0](#3-relationship-to-conventional-commits-100)
4. [Message structure](#4-message-structure)
5. [Header grammar](#5-header-grammar)
6. [Scope resolution](#6-scope-resolution)
7. [Types and bump mapping](#7-types-and-bump-mapping)
8. [Directives: footers and inline shorthand](#8-directives-footers-and-inline-shorthand)
9. [Propagation](#9-propagation)
10. [`cancel`](#10-cancel)
11. [Prerelease flow](#11-prerelease-flow)
12. [Version tags and state](#12-version-tags-and-state)
13. [Release computation algorithm](#13-release-computation-algorithm)
14. [Configuration](#14-configuration)
15. [Edge cases](#15-edge-cases)
16. [Diagnostics registry](#16-diagnostics-registry)
17. [Conformance](#17-conformance)
18. [Security considerations](#18-security-considerations)
19. [Implementation obligations for publishing](#19-implementation-obligations-for-publishing)
20. [Parsing without regular expressions](#20-parsing-without-regular-expressions)
21. [Appendix A: Regular expressions](#21-appendix-a-regular-expressions)
22. [Appendix B: Conformance test vectors](#22-appendix-b-conformance-test-vectors)
23. [Appendix C: Formal grammar (ABNF)](#23-appendix-c-formal-grammar-abnf)
24. [Appendix D: Worked examples](#24-appendix-d-worked-examples)
25. [VCS adapters](#25-vcs-adapters)
26. [Explicit rollback](#26-explicit-rollback)
27. [Polyrepository Git profile](#27-polyrepository-git-profile)
28. [Distributed task and release execution](#28-distributed-task-and-release-execution)

---

## 1. Summary

CCME extends Conventional Commits with release intent across a workspace of many packages and contracts for
planning, publication and recovery. Optional execution profiles preserve the message grammar:

| # | Capability                                                                                                     | Syntax                                                         |
|---|----------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| 1 | **Multiple units per commit**: one commit message describes several independent changes                       | blocks separated by `---`                                      |
| 2 | **Dependent propagation**: opt in, and declare how far a change bumps its consumers                           | `^`, `^minor+2`, `^^minor` / `Propagate:` + `Propagate-Depth:` |
| 3 | **Cancellation**: discard accumulated, unreleased release metadata without touching code                      | type `cancel`                                                  |
| 4 | **Explicit or derived targeting**: scope names a package, or is omitted to derive packages from changed files | `feat(api,web)` / `feat`                                       |
| 5 | **Prerelease channels**: enter, iterate on, graduate, and carry consumers along, each under its own depth     | `%beta`, `%%beta++1`, `%beta>stable`                           |
| 6 | **Corrections**: restate or discard a past commit's pending release record                                    | `Edits: <sha>`, `Deletes: <sha>`, `Deletes: *`                 |
| 7 | **VCS adapters**: use trusted shell commands behind a fixed snapshot and immutable-record contract | `vcs` configuration; Git remains the default (§25) |
| 8 | **Explicit rollback**: withdraw an identified published artifact while retaining its release history | `rollback(api)` + `Rollback-Version: 1.4.2` (§26) |
| 9 | **Polyrepository planning**: combine explicitly linked Git histories into one release graph without copying their commits | optional polyrepository profile (§27) |
| 10 | **Distributed execution**: assign tasks across nodes and reuse verified build outputs while preserving the release plan and native records | optional execution profile (§28); `execution` node settings |

Capabilities 2 and 5 are two **independent axes** of the same idea. A commit says separately how far a *version bump*
travels (`^`, `^^`, `+N`) and how far a *channel* travels (`%%`, `++N`), because the answers differ: a change usually
needs its consumers rebuilt, and much less often needs them moved onto a prerelease line. Both axes default to depth
`0`, so a commit that says nothing releases its own packages and nothing else (§8.3, §8.3a).

Everything is designed so that a conforming parser can be written with a linear index scan and no regular-expression
engine (§20). Appendix A gives regular expressions for implementers who prefer them.

Alongside these capabilities sits one guarantee that is not syntax: **a release is durable under partial failure.** Publishing
is a sequence of independent, individually-fallible registry operations, and any of them may fail. The engine therefore
computes propagation against the *consumer's* release position rather than the provider's (§13.7a), publishes in
dependency order and skips the dependents of anything that failed (§19), and converges to an empty plan by re-running
(§13.7c). A half-finished release is a resumable state, not a lost one. Explicit rollback adds a separate withdrawal ledger;
its activation and external-availability limits are specified in §26. Ordinary publish failure never requests rollback.

**Example combining the original commit directives:**

```
feat(@acme/core)^^minor%beta++*: streaming reader

Replaces the buffered reader with an incremental one.

---

fix(@acme/cli): correct exit code on SIGINT

---

cancel(@acme/legacy-adapter): reset release state
```

---

## 2. Conventions and terminology

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHOULD**, **SHOULD NOT**, **MAY**, and **OPTIONAL** are
to be interpreted as described in RFC 2119.

| Term                     | Definition                                                                                                              |
|--------------------------|-------------------------------------------------------------------------------------------------------------------------|
| **Workspace**            | The repository, containing one or more packages.                                                                        |
| **Package**              | An independently versioned unit with a name and a root directory. Names are compared byte-for-byte, case-sensitively.   |
| **Manifest**             | The file declaring a package's name and dependencies (`package.json`, `Cargo.toml`, `pyproject.toml`, …).               |
| **Graph**                | The directed graph of packages; an edge `A → B` means "A depends on B".                                                 |
| **Dependent / consumer** | `A` is a dependent of `B` if there is an edge `A → B`.                                                                  |
| **Depth**                | Edges traversed from a changed package to a dependent. Direct consumers are at depth 1; depth 0 means no propagation.   |
| **Unit**                 | One `<header>[body][footers]` block inside a commit message. A commit contains one or more units.                       |
| **Directive**            | A machine-readable instruction attached to a unit (propagation, channel, release override).                             |
| **Bump**                 | One of `none` \| `patch` \| `minor` \| `major`, ordered `none < patch < minor < major`.                                 |
| **Release tag**          | A git tag `<package>@<version>` marking a published version.                                                            |
| **Baseline**             | The highest-precedence release tag for a package that is reachable from `HEAD`.                                         |
| **Stable baseline**      | The highest-precedence *non-prerelease* release tag reachable from `HEAD`.                                              |
| **Pending window**       | The stable-to-`HEAD` train window used for aggregate version computation (§13.3). Written `W(P)`; fresh admission uses `Wfresh(P)`.                                    |
| **Release engine**       | The tool implementing this specification.                                                                               |
| **Inert**                | Syntactically valid but resolving to zero packages; produces a warning, never an error.                                 |
| **Run**                  | One execution of the engine against a fixed `HEAD`: compute a plan (§13), then publish it (§19).                        |
| **Admission**            | The test deciding whether a unit propagates to a dependent. Evaluated against the **dependent's** releases: what its sources have delivered to it (§13.4a). |
| **Stale**                | A package that would receive a non-`none` propagated bump: it has not released at or after its provider's release of a commit that propagates to it. |
| **Delivered**            | A source's contribution to a dependent is delivered once the dependent has released at or after a release of that source carrying the commit (§13.4a). Releasing past the commit alone is not delivery. |
| **Catch-up release**     | A release whose entire cause is a propagation from a dependency that has **already** been published (§13.7a).           |
| **Publish graph**        | The graph used to order publication, and to decide what a failed publish blocks (§19.2).                                |
| **Build readiness**      | What a consumer's build waits for in its provider: `none`, `build` or `publish` (§19.2a). Execution policy; it never changes the plan. |
| **Blocked**              | Planned, but not attempted in this run because a dependency's publish failed (`W194`).                                  |
| **Convergence**          | The property that re-running the engine at a fixed `HEAD` eventually yields an empty plan (§13.7c).                     |
| **Publish target**       | The registry a package's artefact is uploaded to, or `none`. Does not affect whether the package is released (§13.10a). |
| **Released**             | Assigned a version, tagged, and its manifest written. Every released package is tagged, whatever its target.            |
| **Bump axis**            | Propagation of a version bump to dependents. Governed by `Propagate`, `Propagate-Depth`, `Propagate-Scope`.             |
| **Channel axis**         | Propagation of a channel to dependents. Governed by `Propagate-Channel`, `-Depth`, `-Scope`. Independent of the bump.   |
| **Channel**              | `stable`, or the prerelease identifier a package's baseline carries. Derived from tags, never stored (§11.1).           |
| **Transition**           | A channel value of the form `<from>><to>`: move only packages whose baseline channel is `<from>` (§11.2).               |
| **Resolvable**           | A released version an installer will select for a given consumer. A prerelease is resolvable only on its own line.      |
| **Correction**           | A unit carrying an `Edits` or `Deletes` footer: a targeted rewrite of pending release records (§7.4).                   |
| **Graduation**           | Ending a package's prerelease line by releasing it on `stable` (§11.5). Never happens implicitly.                       |
| **Shared-version group** | A named set of packages holding a leading part of their versions in common, and optionally one prerelease counter and one channel (§13.9a). |
| **Ride**                 | A release of a group member with no cause of its own, made because the group's shared part moved (§13.9a).              |
| **Repository identity**  | In the polyrepository profile, `control` for the control repository, the exact `.gitmodules` name of a source repository, or a peer's own `repository` value (§§27.2, 27.11). |
| **Repository revision**  | A pair `(repository identity, full commit object ID)`. A bare commit ID is never a fleet-wide identity (§27.2).          |
| **Fleet link**           | A submodule joining two peers of a linked peer tree, named by the linked peer's identity and declared by its roster (§27.11). |
| **Execution node** | A machine or runner executing the release engine; independent of repository identity (§28.1). |
| **Orchestrator** | The node that owns one run's locks, fixed planning input, task assignment, admission and finalization (§28). |
| **Worker** | A node executing assigned tasks without authority to initiate or finalize a release (§28.1). |
| **Execution link** | An authenticated worker endpoint used for task assignment, distinct from a repository or fleet link (§28.2). |
| **Task receipt** | Identity-bound execution evidence; never an ordinary release or rollback-completion record (§28.6). |

`max(a, b)` over bumps returns the higher of the two in the ordering above.

**Storage terminology in CCME 3.** In §§4–24, `HEAD`, commit, ancestry, history and release tag denote the corresponding
fixed-head revision, revision node, reachability relation, complete DAG and immutable `release/version` record of §25.
Git remains the concrete default. Git-specific shell commands are examples for that backend. An external adapter
MUST preserve native parent order, exact messages, changed paths and record identity; a linear history that discards
merge parents is not a conforming substitute. SHA operands retain their existing meaning under Git; §25 defines
canonical operands for other backends. Rollback intent/completion records are never release baselines.

A **withdrawn artifact** has a verified rollback-completion record but retains its immutable published-version identity.
A **rollback request** is an explicitly scoped, version-bound operational unit with no bump. Neither changes the
meaning of pending ordinary release metadata or a package's version high-water mark.

---

## 3. Relationship to Conventional Commits 1.0.0

CCME 3 reserves `rollback` as an operational control type. Older engines can treat that spelling as an unknown type
with `W140` (or `E140` under strict types) and do no rollback. The change is not a forward-compatible execution
extension. An explicit activation revision and trusted execution enablement prevent historical inert messages from
becoming destructive operations merely because an engine was upgraded (§26).

CCME is a superset of Conventional Commits 1.0.0 **over the conforming subset described below**. Every message that is
valid under the base spec and stays inside that subset is valid under CCME and MUST produce the same release outcome,
with one clarification and one added default:

* **Clarification.** A message with no `---` separator is a single-unit message. The base spec's structure is the
  one-unit case of §4.
* **Added default.** When no scope is given, the base spec leaves the affected component unspecified. CCME defines it as
  the set of packages owning the commit's changed files (§6.2). In a single-package repository this set is always that
  package, so behaviour is unchanged.

**Where CCME is narrower.** The base spec is permissive in three places where CCME is not, so an unqualified *strict
superset* claim would be false. This is the complete list.

| Base spec accepts                                    | Under CCME                                       | Way out                                     |
|------------------------------------------------------|--------------------------------------------------|---------------------------------------------|
| Any casing of `type`, e.g. `Feat: x`                     | `E101`; types are lowercase (§5.1)               | `lenient: true` lowercases it, `W101`       |
| Spaces and commas inside a scope, e.g. `feat(my api): x` | `E102`; and a comma separates scope terms (§5.2) | Rename the scope; there is no CCME spelling |
| A bare `---` line in a body                          | Splits the message into units (§4.2)             | Escape as `\---`, or configure `separator`  |

The base spec makes only `BREAKING CHANGE` case-sensitive; CCME extends that to `type`, so `Feat: x`, which is valid CC 1.0.0
and accepted by much CC tooling, is an error here. `feat(my api): x` has no CCME spelling at all, because whitespace
inside a scope-set is exactly how the grammar detects a malformed header, and the comma is load-bearing in a multi-term
scope-set. The third differs in kind from the other two: a `---` in a body does not make the message *invalid*, it
changes the *outcome*, since what was one unit becomes several and each subsequent paragraph head is parsed as a header.

**Lenient mode is the migration path for the first of these.** A repository adopting CCME over existing CC 1.0.0 history
SHOULD start with `lenient: true`, which downgrades the casing error to `W101` and the separator error of §5.5 to
`W121`, and tighten once the log is clean. Lenient mode does not rescue the other two: a scope containing a space must
be renamed, and a `---` in a body must be escaped or the separator reconfigured.

Base-spec elements retained without modification: `type` (modulo casing), `(scope)`, `!`, `description`, body,
footer/trailer form, `BREAKING CHANGE:` and `BREAKING-CHANGE:` footers, and the `revert` convention.

CCME adds: the `---` separator, multi-term scope-sets, inline directives, the types `cancel`, `release`, and `rollback`, and the
footer keys listed in §8.1, including the correction footers `Edits` and `Deletes` (§7.4).

---

## 4. Message structure

### 4.1 Input normalisation

The release engine consumes the **cleaned** commit message, the output of `git log --format=%B`, i.e. after git has
stripped `#` comment lines, the `--verbose` diff, and any scissors section. Before parsing, the engine MUST:

1. Strip a leading UTF-8 BOM if present.
2. Normalise `\r\n` and `\r` line terminators to `\n`.
3. Strip trailing whitespace (space, tab) from the **end of each line**. Leading whitespace is preserved (it is
   significant for footer continuations).
4. Strip trailing blank lines from the end of the message.

The engine MUST NOT alter the message in any other way. Messages MUST be valid UTF-8; invalid byte sequences are `E001`.

### 4.2 Units and the separator

A commit message is a sequence of one or more **units** separated by a **separator line**.

A separator line is a line whose entire content, after normalisation (§4.1), is exactly the configured separator string.
The default separator is `---`.

```
<unit 1>
---
<unit 2>
---
<unit 3>
```

Rules:

* The separator MUST occupy a whole line by itself, with no leading whitespace.
* Blank lines around a separator are OPTIONAL and are discarded.
* A separator as the first or last non-blank line of the message yields an empty unit, which is discarded with `W001`.
* Two consecutive separators yield an empty unit, discarded with `W001`.
* A line whose content is `\` followed by the separator (e.g. `\---`) is **escaped**: it is treated as body text, and
  the leading `\` is removed from the rendered body. This is the only escape in the grammar.
* Separator detection is **not** aware of fenced code blocks. Use the escape inside code blocks, or configure a
  different separator.

### 4.3 Choosing a separator

`---` is the default because it is a whole-line token that never occurs in a conventional header and is familiar from
YAML and Markdown. It has one known collision: `git format-patch` writes `---` between the commit message and the
diffstat, and `git am` treats it as the message terminator. In repositories that exchange patches by mail, a message
containing `---` will be truncated by `git am` before the engine ever sees it.

Implementations MUST make the separator configurable (`separator`, §14). Repositories using `format-patch`/`am` SHOULD
set it to `%%%`.

Constraints on a configured separator: at least three characters; ASCII printable; MUST NOT begin with a character that
can begin a type (`a`–`z`); MUST NOT contain whitespace.

### 4.4 Unit structure

Within a unit:

```
<header>
                      <- REQUIRED blank line if body or footers follow
<body>
                      <- REQUIRED blank line before the footer block
<footers>
```

* The **header** is the first line of the unit. It is REQUIRED.
* The **body** is free-form and MAY span multiple paragraphs.
* The **footer block** is the last paragraph of the unit, if and only if every one of its lines is a footer start or a
  footer continuation (§20.5). Otherwise the unit has no footers and that paragraph is body text.
* Footers bind to the unit in which they appear. A `BREAKING CHANGE:` in unit 2 has no effect on unit 1.

### 4.5 Message-level trailers

The following trailer keys are **message-level**: they describe authorship or review, not release intent. They MUST be
ignored by the release engine wherever they appear, and their presence MUST NOT prevent the surrounding paragraph from
being recognised as a footer block:

`Signed-off-by`, `Co-authored-by`, `Change-Id`, `Reviewed-by`, `Acked-by`, `Tested-by`, `Reported-by`, `Suggested-by`,
`Cc`.

Rationale: `git commit -s`, Gerrit hooks, and GitHub's co-author convention append these to the very end of the
message (that is, into the final unit) regardless of unit boundaries. Treating them as unit directives would attach
them arbitrarily to the last unit.

Issue-reference trailers (`Closes`, `Fixes`, `Refs`, `Resolves`) are also ignored for versioning but MAY be surfaced in
generated changelogs, attributed to the unit that contains them.

---

## 5. Header grammar

```
<type>[(<scope-set>)][<inline-directives>][!]: <description>
```

Component order is fixed. Each component is defined below; the full ABNF is in Appendix C.

### 5.1 `type`

One or more characters from `a`–`z`. Lowercase is REQUIRED (`E101` otherwise; lenient mode lowercases with `W101`). No
digits, no hyphens, no underscores.

**`BREAKING CHANGE` is not a type and can never be one.** It is uppercase and contains a space, so it fails this rule
twice over. A unit whose header line begins with `BREAKING CHANGE` is `E100`, and implementations MUST special-case the
diagnostic message, because writing it as a header is a common mistake:

```
BREAKING CHANGE: the plugin API is gone          <- E100
```

The two correct spellings are the `!` marker (§5.4):

```
feat(core)!: replace the plugin API
```

and the footer (§8.1.1):

```
feat(core): replace the plugin API

BREAKING CHANGE: `registerPlugin` is gone.
```

Breaking-ness is a property of a unit, so in a multi-unit message each spelling binds only to its own unit; see §8.1.1
for the full treatment.

### 5.2 `(<scope-set>)`

OPTIONAL. A parenthesised, comma-separated list of one or more **scope terms**.

* Whitespace immediately after a comma is permitted and ignored. Whitespace elsewhere inside the parentheses is `E102`.
* Parentheses MUST NOT nest and MUST be balanced (`E103`).
* An empty scope-set `()` is `E104`.
* A scope term MUST NOT contain: `(`, `)`, `,`, `:`, or whitespace. It MAY contain `@`, `%`, `/`, `.`, `*`, `-`, `+`,
  `^` and any other printable character.

Inside the parentheses every sigil character is ordinary, so npm-style scoped names work unmodified: `feat(@acme/ui)`.
The `%` sigil for prerelease channels is only recognised **outside** the parentheses, and `@` carries no meaning
anywhere in the grammar (package names and tags use it freely).

Scope-term forms:

| Form                     | Meaning                                                                                             |
|--------------------------|-----------------------------------------------------------------------------------------------------|
| `name`                   | Include the package named `name`.                                                                   |
| `-name`                  | Exclude the package named `name`.                                                                   |
| `pattern` containing `*` | Include every package whose name matches the glob. `*` matches any run of characters including `/`. |
| `-pattern`               | Exclude every matching package.                                                                     |
| `*`                      | Include every package in the workspace.                                                             |
| `.`                      | Include the file-derived set for this commit (§6.2).                                                |
| `-.`                     | Exclude the file-derived set.                                                                       |

### 5.3 `<inline-directives>`

OPTIONAL. A run of directive tokens, each introduced by a sigil, with no separators between them. Order is not
significant. Each sigil MAY appear at most once per header (`E110` on repeat).

| Sigil | Token                                                             | Desugars to                                                       |
|-------|-------------------------------------------------------------------|-------------------------------------------------------------------|
| `^`   | `^`                                                               | `Propagate-Depth: 1`                                              |
| `^`   | `^none` `^patch` `^minor` `^major` `^inherit`                     | `Propagate: <value>` **and** `Propagate-Depth: 1`                 |
| `^^`  | `^^`                                                              | `Propagate-Depth: all`                                            |
| `^^`  | `^^none` `^^patch` `^^minor` `^^major` `^^inherit`                | `Propagate: <value>` **and** `Propagate-Depth: all`               |
| `+`   | `+0` … `+N`, `+*`                                                 | `Propagate-Depth: <N \| all>`                                     |
| `%`   | `%<channel>`, `%stable`, `%<from>><to>`                           | `Channel: <value>`                                                |
| `%%`  | `%%<channel>`, `%%stable`, `%%inherit`, `%%none`, `%%<from>><to>` | `Propagate-Channel: <value>` **and** `Propagate-Channel-Depth: 1` |
| `++`  | `++0` … `++N`, `++*`, `++direct`, `++all`                         | `Propagate-Channel-Depth: <N \| all>`                             |

Examples: `feat(api)^minor+2: …`, `fix^^inherit: …`, `feat(api)^^: …`, `feat(api)%beta!: …`,
`feat(core)^%beta++1: …`, `release(core)%beta>stable%%beta>stable++*: …`.

`^`, `^^`, `+` and `++` values are matched byte-for-byte against the enumerated words; abbreviations such as `^min` are
`E111`. `^` and `^^` MAY be written with no value at all, taking the default bump; every other sigil requires one.

#### Two axes, two depths

A commit answers two separate questions about its dependents, and they have different answers far more often than not:

* **how far does the version bump travel?** This is the bump axis: `^`, `^^`, `+N`, and the `Propagate*` keys;
* **how far does the channel travel?** This is the channel axis: `%%`, `++N`, and the `Propagate-Channel*` keys.

**Both axes are opt-in, and both default to depth `0`.** A unit with no propagation directive touches only its own
packages. One sigil reaches the direct consumers, and the depth token extends the reach:

| Written     | Bump reaches               | Bump to dependents |
|-------------|----------------------------|--------------------|
| *(nothing)* | nobody                     | none                  |
| `^`         | direct consumers           | `patch` (default)  |
| `^minor`    | direct consumers           | `minor`            |
| `^^`        | every transitive dependent | `patch` (default)  |
| `^^minor`   | every transitive dependent | `minor`            |
| `+N`        | up to `N` edges away       | `patch` (default)  |
| `^minor+3`  | up to 3 edges away         | `minor`            |

| Written         | Channel reaches            | Channel they take                  |
|-----------------|----------------------------|------------------------------------|
| *(nothing)*     | nobody                     | none                                  |
| `%%beta`        | direct consumers           | `beta`                             |
| `%%beta++3`     | up to 3 edges away         | `beta`                             |
| `%%beta++*`     | every transitive dependent | `beta`                             |
| `++1`           | direct consumers           | `inherit` (default): the origin's  |
| `++*`           | every transitive dependent | `inherit` (default): the origin's  |
| `%%none`, `++0` | nobody                     | none                                  |

The bump ladder reads `nothing` / `^` / `^^`, with `+N` when the answer is neither 1 nor all. The channel ladder reads
`nothing` / `%%x` / `%%x++*`, with `++N` for the same reason. `^^minor` is exactly `^minor+*`; `^^` on its own is
exactly `+*`. There is no doubled spelling of "channel to every level"; write `++*`.

A bare `^` and a bare `^^` are both legal: the value is the bump, and it has a default. This is the one place where an
empty value after a sigil is not `E111` (§16). A bare `%%` or `++` is `E111`: neither has a default worth guessing, and
`++` with no number is not a depth at all.

**The two axes never constrain one another.** A channel may be propagated further than a bump, less far, or with no bump
propagation at all: `feat(core)%%beta` puts the direct consumers on the beta line without giving them a bump, and
`feat(core)^^minor++1` bumps the whole closure while moving only the direct consumers onto the origin's channel. The
channel axis in particular does **not** require the unit to produce a bump, which is what lets a `release` unit, whose
bump is always `none` (§7.2), carry a graduation to its dependents.

#### The doubling rule

A doubled sigil is the **same** sigil as its single form when both spellings set the same key, and a **distinct** sigil
when they set different keys. This is the whole rule, and it decides the once-per-header question uniformly:

| Pair     | Keys                                          | One sigil or two? |
|----------|-----------------------------------------------|-------------------|
| `^` `^^` | both `Propagate-Depth`                        | **one**           |
| `%` `%%` | `Channel` / `Propagate-Channel`               | **two**           |
| `+` `++` | `Propagate-Depth` / `Propagate-Channel-Depth` | **two**           |

So `^minor^^major` is `E110`, while `feat(core)%beta%%rc+2++1` is legal and sets four different keys. All four of `%`,
`%%`, `+` and `++` are permitted once each, in any order.

`^` and `^^` differ only in what they do about an explicit `+N`. `^` states a depth only in the absence of one, so
`^minor+2` and `+2^minor` both mean depth `2` with no diagnostic (§20.3). `^^` exists to *assert* `all`, so combining it
with an explicit `+N` where `N` is not `all` is `E113`: two spellings of depth disagreeing in one header;
`^^minor+*` is legal but redundant and emits `W110`. `%%` behaves like `^`, not like `^^`: it implies
`Propagate-Channel-Depth: 1` only in the absence of an explicit `++N`, so `%%beta++3` is channel depth `3` with no
diagnostic.

**Why `%%` is not a reach.** `%` concerns the unit's own packages; `%%` changes the *audience* to the dependents.
`^`/`^^` keep one audience and change the *distance*. A channel has no distance dimension of its own to intensify, because how
far it goes is what `++N` says, so a doubled `%` could not have meant what a doubled `^` means. The doubled sigil `%%` keeps `%`
reading as "channel" throughout. A compound `^%` would make `^` a namespace prefix in one form and a bump sigil taking
bump words in every other.

#### Channel transitions

Every channel value (on `%` and on `%%` alike) MAY be written as a **transition**:

```
<from>><to>
```

It means: *move only the packages whose current channel is `<from>`, and move them to `<to>`.* Packages that are on some
other channel are left entirely alone: no channel change, and no release on the channel axis.

* `<from>` is a channel name, `stable`, or `*`. `*` matches **any prerelease channel**, and never matches `stable`.
* `<to>` is a channel name or `stable`. `inherit` is **not** a valid `<to>` (`E111`): a transition names the train it is
  ending, and "end it, onto whatever the origin happens to be on" is exactly the vagueness the form exists to remove.
* A package's channel for matching purposes is the channel of its **baseline** (§11.1), the tag it currently sits at,
  not any channel it acquires in this run. Matching against a value computed in the same run would make chained
  transitions order-dependent.
* `<from>` equal to `<to>` is inert, `W207`.
* A transition that matches nothing emits `W206`, which is what a mistyped `<from>` looks like.

| Written            | Effect                                                           |
|--------------------|------------------------------------------------------------------|
| `%stable`          | This unit's packages go stable, whatever they are on now         |
| `%beta>stable`     | Only those currently on `beta` go stable; the rest are untouched |
| `%beta>rc`         | Promote the `beta` train one step                                |
| `%*>stable`        | Every one of them that is on *some* prerelease goes stable       |
| `%%beta>stable++*` | Every transitive dependent currently on `beta` graduates         |
| `%%stable>beta++1` | Direct consumers currently on `stable` join the `beta` line      |

The transition form is what makes graduation composable. `release(@acme/*)%beta>stable` graduates a whole family of
packages in one unit and is **idempotent**: a package that was already graduated in an earlier run is on `stable`, does
not match `beta`, and is simply not touched: no error, no redundant release, no need to hand-maintain the scope-set. It
is also the only way a channel reaches a dependent and ends its prerelease line, because a propagated bare or inherited
`stable` never graduates (§9.3, §11.5).

Exclusions are available on both axes and at both levels: the scope-set excludes packages from the unit itself
(`release(@acme/*,-@acme/legacy)%beta>stable`), and `Propagate-Channel-Scope` excludes them from the channel axis
(§8.5a) without disturbing the bump axis.

#### Inline and footer forms

Inline directives are **exactly equivalent** to their footer forms. If a header and a footer both set the same key:

* identical values → accepted, `W110`;
* different values → `E112` (in lenient mode, the footer wins with `W112`).

### 5.4 `!`

OPTIONAL. Immediately precedes the colon. Marks the unit as a breaking change for its resolved package set. Equivalent
to a `BREAKING CHANGE:` footer on the same unit; both MAY be present.

### 5.5 `: ` and `<description>`

The prefix ends at the **first `:` that is not inside parentheses**. It MUST be followed by exactly one space, then a
non-empty description.

* Zero spaces or two or more spaces after the colon → `E120`. Lenient mode accepts a *missing* space with `W121`; two or
  more spaces remain `E120`, because the intended description cannot be recovered from them.
* Empty description → `E121`.
* A header that ends at the colon is `E121`, not `E120`: normalisation (§4.1) strips trailing whitespace, so `feat: `
  arrives at the parser as `feat:`, and the finding is the absent description rather than a malformed separator
  (vector 19). The zero-space case of `E120` is therefore a colon followed by a non-space character.
* The description is the remainder of the line; it MAY contain further colons (`feat(api): fix: nested` has description
  `fix: nested`).
* The description SHOULD be imperative mood and SHOULD NOT end with a period. Neither is enforced.
* A description exceeding `maxDescriptionLength` (default 100 characters, counted in Unicode scalar values) → `W120`.

### 5.6 Header examples

| Header                                  | Type       | Scopes                 | Inline                             | Breaking |
|-----------------------------------------|------------|------------------------|------------------------------------|----------|
| `feat: add retry`                       | `feat`     | derived                | none                                  | no       |
| `fix(api): null guard`                  | `fix`      | `api`                  | none                                  | no       |
| `feat(api,web,@acme/ui): unify theme`   | `feat`     | 3 packages             | none                                  | no       |
| `feat(*,-docs-site): bump runtime`      | `feat`     | all but `docs-site`    | none                                  | no       |
| `feat(.,-legacy): new codec`            | `feat`     | derived minus `legacy` | none                                  | no       |
| `perf(core)^patch+1: faster hash`       | `perf`     | `core`                 | prop=patch depth=1                 | no       |
| `refactor(core)^^major!: drop v1 API`   | `refactor` | `core`                 | prop=major depth=all               | yes      |
| `feat(core)^^: broad internal change`   | `feat`     | `core`                 | prop=patch depth=all               | no       |
| `feat(cli)%beta: experimental watch`    | `feat`     | `cli`                  | channel=beta, nobody follows       | no       |
| `feat(core)^%beta++1: broad change`     | `feat`     | `core`                 | self beta, direct consumers too    | no       |
| `feat(core)%%beta: let consumers try`   | `feat`     | `core`                 | core stable, consumers onto beta   | no       |
| `feat(core)^%beta%%stable: x`           | `feat`     | `core`                 | self beta, consumers pinned stable | no       |
| `feat(core)^^minor++*: whole train`     | `feat`     | `core`                 | minor to all, origin's channel too | no       |
| `release(cli)%stable: graduate 2.0`     | `release`  | `cli`                  | channel=stable                     | no       |
| `release(@acme/*)%beta>stable: ship`    | `release`  | glob                   | graduate only those on `beta`      | no       |
| `release(core)%stable%%beta>stable++*:` | `release`  | `core`                 | graduate core and its beta closure | no       |
| `cancel(*): reset release state`        | `cancel`   | all                    | none                                  | n/a      |

---

## 6. Scope resolution

Scope resolution turns a unit into a concrete set of packages. It runs per unit, per commit.

### 6.1 Algorithm

```
resolve(unit, commit, workspace):
    terms    = unit.scopeTerms            # empty if no parentheses
    includes = terms where not term.startsWith('-')
    excludes = terms where term.startsWith('-'), with '-' removed

    if includes is empty:
        base = derived(commit)            # §6.2
    else:
        base = {}
        for t in includes:
            if t == '.':            base |= derived(commit)
            else if t == '*':
                                    base |= workspace.allPackages
            else if t contains '*': base |= workspace.matching(t)
            else if t in workspace: base |= { t }
            else:                   raise E130(t)

    out = {}
    for t in excludes:
        if t == '.':            out |= derived(commit)
        else if t == '*':
                                out |= workspace.allPackages
        else if t contains '*': out |= workspace.matching(t)
        else if t in workspace: out |= { t }
        else:                   warn W130(t)     # exclusions are tolerant

    return base - out
```

Notes:

* Unknown **include** names are `E130`: a typo would silently drop a release otherwise.
* Unknown **exclude** names are `W130`: excluding a package that was deleted or renamed is harmless and common during
  refactors.
* Order of terms is irrelevant; excludes always apply last.
* An empty result makes the unit **inert**: `W131`, no bump, no error.
* Private and internal packages are ordinary packages: they resolve, release, tag, and propagate like any other. Their
  registry is a separate axis (§13.10a) and does not affect resolution. Every package in the workspace is a release
  unit; there is no per-package opt-out.

### 6.2 File-derived scopes

`derived(commit)` is the set of packages owning at least one path in the commit's changed-file list.

* Ownership is by **longest matching path prefix**. Given packages at `packages/ui` and `packages/ui/theme`, the file
  `packages/ui/theme/dark.ts` belongs to `packages/ui/theme` only.
* The changed-file list for an ordinary commit is its diff against its parent.
* For a **merge commit**, the diff is taken against the **first parent**. A merge introducing no changes relative to its
  first parent yields the empty set.
* Renames contribute both the old and new path. Deletions contribute the deleted path. Mode-only changes contribute the
  path.
* Paths matching `ignoredPaths` are removed before ownership resolution. The default list is **empty**; notably,
  documentation and test files count by default, because a `fix` to a package's tests is still a commit against that
  package and excluding them silently drops releases.
* Paths owned by no package (repo-root config, CI files, shared tooling) contribute nothing, unless a `rootPathMap`entry
  maps a glob to an explicit package list (§14).
* If the resulting set is empty, units relying on it are inert (`W131`).

### 6.3 Multi-unit commits and derived scopes

If a commit contains several units and none of them declares an explicit scope, **every** unit resolves to the same
derived set. A `feat` and a `fix` in the same unscoped commit therefore both apply to every changed package.

This is rarely what an author means. Implementations MUST emit `W132` for any commit with two or more units where fewer
than all units carry an explicit scope-set. Authors SHOULD scope every unit in a multi-unit commit.

---

## 7. Types and bump mapping

### 7.1 Standard types

| Type                                        | Default direct bump | Notes                                   |
|---------------------------------------------|---------------------|-----------------------------------------|
| `feat`                                      | `minor`             |                                         |
| `fix`                                       | `patch`             |                                         |
| `perf`                                      | `patch`             |                                         |
| `revert`                                    | `patch`             | Also records `Reverts:` if present.     |
| `refactor`                                  | `none`              |                                         |
| `docs`                                      | `none`              |                                         |
| `style`                                     | `none`              |                                         |
| `test`                                      | `none`              |                                         |
| `build`                                     | `none`              |                                         |
| `ci`                                        | `none`              |                                         |
| `chore`                                     | `none`              |                                         |
| **ordinary type** with `!` or `BREAKING CHANGE:` | `major`             | Overrides the row above.                |
| `cancel`                                    | *control*           | §10. Never produces a bump.             |
| `release`                                   | *control*           | §7.2. Never produces a bump on its own. |
| `rollback` | *control* | §26. Explicit withdrawal; never produces a bump. |

The control types `cancel`, `release`, and `rollback` cannot be remapped into ordinary bump types. `rollback` requires explicit literal package scopes and forbids breaking/propagation/channel directives (§26).

The mapping is configurable via `types` (§14). Unknown types are accepted and default to `none` with `W140`, unless
`strictTypes` is enabled, in which case they are `E140`.

### 7.2 `release`

`release` is a **directive-only type**. It carries channel and propagation directives for packages without asserting
that any code changed.

```
release(@acme/cli)%stable: graduate to 2.0.0
```

```
release(@acme/core,@acme/cli)%rc: move the release train to rc
```

```
release(@acme/core)%stable%%beta>stable++*: graduate the 2.0 train

Propagate-Channel-Scope: @acme/*, -@acme/legacy-adapter
```

```
release(@acme/api): pin the coordinated launch version

Release-As: 3.0.0
```

Note the third form: `Release-As` is a **footer**, so it belongs in the unit's final paragraph after a blank line, never
on the header line. `release(@acme/api): Release-As: 3.0.0` is a valid header whose *description* happens to read
`Release-As: 3.0.0`; it sets nothing and is inert (`W141`). This applies to every footer in §8.1: the inline sigils of
§5.3 are the only directives that live on the header line.

* `release` contributes bump `none`. It cannot be combined with `!` (`E141`).
* A `release` unit whose only effect would be `Channel: stable`, or a transition ending on `stable`, triggers
  **graduation** (§11.5) even though its own bump is `none`.
* A `release` unit carrying a `Release-As` footer holds, resumes, or pins the version per §8.6. It cannot change a
  bump, because `release` contributes `none` and no footer alters that.
* A `release` unit with no directive at all is inert (`W141`).

**`release` and the two axes.** The bump axis requires a bump: a unit whose own bump is `none` propagates none, so
`release(core)^minor` reaches nobody and is inert for propagation (#38a). This is neither `W152` nor `W201`: the
directive resolves to a real value at a real depth, and what silences it is the type's bump of `none`, not anything
written in the directive (§8.3b). The **channel axis does not**, and that asymmetry is the point of the type. A channel
is a statement about *where a package publishes*, not about whether it changed, so it is coherent (and necessary) for
a package that changed nothing to move its consumers off a prerelease line. `release(core)%stable%%beta>stable++*` is
therefore the canonical graduation of a whole train, and it is the only shape in which a unit with no bump releases
other packages.

### 7.3 `revert` versus `cancel`

Both undo something; they operate on different layers and are not interchangeable.

|                           | `revert`                                   | `cancel`                                                                        |
|---------------------------|--------------------------------------------|---------------------------------------------------------------------------------|
| Layer                     | Source code                                | Release metadata                                                                |
| Changes files             | Yes: the commit contains the inverse diff  | No, typically an empty or unrelated commit                                     |
| References a target       | Yes, `Reverts: <sha>` per the base spec    | **No.** Deliberately carries no target and no reason                            |
| Own version bump          | Yes (`patch` by default)                   | None, ever                                                                      |
| Effect on history reading | None                                       | Truncates the pending window for its scopes (§10)                               |
| Effect on published tags  | None                                       | None; never deletes or rewrites a tag                                           |
| Typical use               | "We shipped a bug; undo the code"          | "The migration/importer invented bumps that don't exist; start the ledger over" |

`revert` remains defined exactly as in the base specification; CCME adds nothing to it beyond the scope-set and
directive syntax available to every type.

**The `revert` trap.** Reverting a commit does **not** remove the reverted commit's bump from the window. If a `feat!`
and a `revert` of it both sit in one window, the package still takes a `major`: the `feat!` contributed `major`, the
`revert` contributed `patch`, and `max()` gives `major`. This is correct: the release will contain neither the feature
nor its removal, but consumers may already have seen the `feat!` in a prerelease, and silently downgrading the bump
would be worse. For the *bump*, `Reverts:` is informational, because acting on it would require the engine to reason
about whether the revert was complete. To also drop the version signal, pair the revert with a `cancel`, or discard
the one commit's record with `Deletes:` (§7.4).

**`Reverts` and the changelog.** For the *changelog*, `Reverts: <sha>` is not informational. When the reverted
commit's unit is still undischarged for a package, that unit's changelog entry and the revert unit's own entry are
both suppressed for that package: the release then describes neither the change nor its removal, which is accurate,
since it contains neither. The suppression is reported per package as `W212`, so the plan accounts for the absent
entries. When the reverted commit has already released for a package, nothing is suppressed for it: the earlier
changelog is published history, and the revert's own entry appears normally. Two degraded forms exist, and both leave
the footer informational: a value that is not a well-formed commit sha (7 to 64 lowercase hexadecimal characters) is
reported as `W214` at parse time, and a well-formed sha naming a commit that is not an ancestor-or-self of the revert
is reported as `W213` by the engine. In both cases the revert still releases and still bumps.

There is a third, weaker option that is often the one actually wanted: `Release-As: none` holds a release without
discarding anything (§8.6.1). Together with the corrections of §7.4 these form a ladder: **`revert`** undoes the
code, **`Release-As: none`** defers the release, **`Edits:`** restates one commit's record, **`Deletes:`** discards
one commit's record, and **`cancel`** erases the ledger.

### 7.4 Corrections: `Edits` and `Deletes`

Release metadata accumulates in history, where it cannot be rewritten. `cancel` (§10) discards a package's whole
pending ledger behind a positional barrier; the correction footers `Edits` and `Deletes` operate on named records. A
correction is an **ordinary unit** carrying one of the two footers: the unit keeps its own type, scope-set,
description, and breaking marker, releases like any other unit, and additionally rewrites the pending records it
names. Corrections act on release records, never on code, and never touch a published tag or a published changelog.

```
fix(core): guard against empty input       chore(core): restate the changeset

Edits: 4f2a1c9                             Deletes: *
```

The first message discards the record of commit `4f2a1c9` and stands in its place: whatever `4f2a1c9` claimed to be,
the ledger now carries this `fix` instead, with this description as the changelog text. The second discards every
pending record for `core` while contributing nothing itself, because `chore` maps to bump `none`.

#### 7.4.1 Syntax

The target lives in a footer, following the precedent of `Reverts:`. The values are:

```
Edits:   <sha>[#<n>]  |  Edits: *
Deletes: <sha>[#<n>]  |  Deletes: *
```

* `<sha>` is 7 to 64 lowercase hexadecimal characters, a full or abbreviated commit id.
* `<n>` is a 1-based unit index into the target commit, written without leading zeros. A bare `<sha>` is legal only
  when the target commit contains a single unit; naming a multi-unit commit without a selector is `E211`.
* `*` names every record pending for the unit's resolved scope-set. It is the whole value; `*` does not combine with
  a sha.
* A malformed value of either footer is `E151`.

Rules for the unit around the footer:

* Any type from §7.1 may carry the footers, including a type mapping to `none`, which is how a pure deletion is
  written. The three **control** types may not: a correction footer on `cancel` is `E171`, on `release` is `E173`, and on `rollback` is `E301`; none carries an ordinary record that could restate anything.
* A unit MAY carry several correction footers, `Edits` and `Deletes` mixed freely. Several `Edits` targets collapse
  into the one carrying record: the unit restates all of them at once.
* Everything else about the unit is ordinary. Its scope-set resolves per §6, its directives mean what they always
  mean, and `!` marks the restatement breaking.

#### 7.4.2 Semantics

Corrections rewrite the stream of pending tuples (§13.4) before cancellation, holds, and propagation run; §13.4b
places them in the algorithm. All of the following are normative.

**A correction reaches only undischarged work.** A correction applies to a `(package, unit)` pair only while the
package's pending window still contains the target commit (§13.4a). Once a package has released the target, the
record is published history: the correction is a no-op for that package and `W209` reports it. `W209` is
non-suppressible, because an operator who writes a correction must be able to see that it did not take. The carrying
unit still contributes its own record normally.

**A correction reaches only strict ancestors.** The target MUST be a proper ancestor of the correction's commit:
units of the correction's own commit are never targets, so a single commit can discard old records and supply their
restatement together, which is what makes the one-commit form of §D.6 safe where the `cancel` form is a trap. A
well-formed sha that is unknown, unreachable, or not a proper ancestor is `E210`; a selector out of range is `E211`;
a target unit that is a control unit is `E212`. Each is unit-scoped: the offending unit contributes nothing, and
other units still apply.

**Scopes must be contained, and corrections apply per package.** A correction claims to restate or discard a record,
and a record is a claim about specific packages, so the two scopes are reconciled rather than combined:

* A correction unit with **no scope-set** takes the union of its sha targets' resolved package sets as its own. This
  is the recommended form: a correction commit is typically empty, so the file-derived fallback of §6.2 would resolve
  to nothing, and the targets already say which packages are meant. A unit whose only target is the wildcard defaults
  to `*`.
* A correction unit **with a scope-set** is legal exactly when its resolved package set is a **subset** of each sha
  target's resolved set; a package outside a target's set is `E213`. Narrowing is deliberate and legal: it is how a
  record scoped `(*)` is corrected for some of its packages only. The correction then applies exactly to the packages
  it names, and the target's record survives untouched for the rest. Widening is what `E213` forbids, because a
  correction may not extend someone else's record to packages it never claimed.
* The wildcard has no target scope to be contained in. `Deletes: *` and `Edits: *` take the correction unit's
  resolved scope-set as their reach, defaulting to `*`: the scope-set selects whether the whole workspace's pending
  records are cleared or only the named packages'.

A partial correction is therefore ordinary, not an edge case: `chore(core): x` carrying `Deletes: <sha>` against a
target scoped `(*)` discards that record for `core` alone, and a partial `Edits` restates it for the named packages
while the original stands for the others. Every rule in this section is evaluated per `(package, record)` pair.

**`Deletes` discards the record.** Each named target unit is treated as if it had failed to parse (§16): it
contributes no direct bump, no propagation on either axis, no channel, and no changelog entry, for every package
whose window still contains it. `Deletes: *` does the same for every pending unit, direct and propagated
contributions alike, for each package in the correction unit's resolved scope-set. The wildcard's reach differs from
`cancel` in exactly one respect: its barrier excludes its own commit, where `cancel`'s is ancestor-or-self (§10.3).

**`Edits` is `Deletes` plus a restatement.** `Edits: T` discards `T` exactly as `Deletes: T` would, and designates
the carrying unit as `T`'s restatement: the carrying unit's type, breaking marker, and description are what the
ledger and the changelog now say about that work. No merging of the two records takes place; the target's directives
die with its record, and the carrying unit's own directives apply as written. A changelog SHOULD render the
restatement once, as the carrying unit's entry, and MAY annotate it as correcting the target commit. `Edits: *`
restates a scope's entire pending changeset as the single carrying record. An `Edits` whose carrying unit has the
same type, breaking marker, and description as its target changes nothing and is reported as `W211`.

**The newest correction wins.** When several corrections name the same target unit, the one in the newest commit
applies; within one commit, the last unit applies. The rule and its rationale are those of §8.6, and `W210` reports
each superseded correction. A `Deletes` followed by a newer `Edits` of the same target therefore ends with the
restatement in force: the sequence is read positionally, and each correction supersedes the last.

**A discarded correction is void.** A correction unit is an ordinary unit, so it can itself be the target of a later
correction, and one rule decides every such nesting: corrections apply newest first, and a correction whose own
record has been discarded for a package by an already-applied correction is **void for that
package**: none of its effects apply there, exactly as if the unit had failed to parse. Each voiding is reported as
`W215`. The consequences, each of which follows from the rule rather than from a case of its own:

* **`Deletes` of an `Edits` un-edits.** Deleting the correction discards its restatement record and voids its
  discard of the original, so the original record returns, provided it is still undischarged. This is the undo of a
  mistaken correction.
* **`Deletes` of a `Deletes` restores.** The newer delete voids the older one, so the older one's target returns.
  Chains of any depth resolve the same way, newest first; targets are proper ancestors, so a chain cannot cycle and
  one pass settles it.
* **`Edits` of a correction replaces its record and voids its effect.** The older correction's target returns, and
  the new unit restates the *correction's own record*, not the correction's target. To restate the original again,
  target the original: a newer `Edits` of the same target supersedes the older one directly (`W210`), and is almost
  always what the author means.
* **A discarded revert suppresses nothing.** The same rule applied to §7.3: when a revert unit's record is discarded
  for a package, its changelog suppression (`W212`) is void for that package, and the reverted entry returns.
* **The wildcard composes consistently.** A `Deletes: *` that discards a pending correction voids it, restoring its
  targets, and then discards those targets too where they fall inside the wildcard's scope and reach. The net effect
  is the expected one: nothing pending survives in the cleared scope.

The application order is well-founded: every target is a proper ancestor of its correction, and corrections apply
newest first, so by the time a correction applies, everything that could void it already has. The result is a single
deterministic pass, whatever the nesting depth.

**Corrections compose with the rest of the algorithm without special cases.** Cancellation, holds, propagation,
channel resolution, and versioning all run on the corrected stream (§13.4b). A cancel whose barrier covers a target
discards it like any other unit, after which a correction of that target has nothing to act on and reports `W209`.
Every operand is read from history and tags at `HEAD`, so corrections are deterministic, idempotent, and preserve
G1 through G8 of §13.7c: a corrected record discharges with the window exactly as the original would have.

**Worked example.** From `core@1.4.2`, with commit `A` classified `feat(core)!: rewrite internals` by mistake:

```
fix(core): rewrite internals

The change is a refactor with a defensive fix, not a breaking feature.

Edits: <sha of A>
```

`A`'s `major` leaves the window and this unit's `patch` enters it, so `core` releases `1.4.3` rather than `2.0.0`,
and the changelog carries one entry, this one. Had `core` already released `2.0.0`, the correction would be a no-op
with `W209`, because `2.0.0` is published history.

---

## 8. Directives: footers and inline shorthand

### 8.1 Footer registry

Footers are git trailers: `Key: value`, one per line, in the unit's final paragraph. Keys are matched
**case-insensitively**, but not hyphen-insensitively (`Propagate-Depth` and `propagate-depth` match; `PropagateDepth`
does not). **`BREAKING CHANGE` is the sole exception and is case-sensitive**; see §8.1.1.

| Footer                                | Inline            | Values                                                     | Default                      | Scope                       |
|---------------------------------------|-------------------|------------------------------------------------------------|------------------------------|-----------------------------|
| `BREAKING CHANGE` / `BREAKING-CHANGE` | `!`               | free text                                                  | none                            | unit                        |
| `Propagate`                           | `^x` / `^^x`      | `none` \| `patch` \| `minor` \| `major` \| `inherit`       | `patch`                      | unit                        |
| `Propagate-Depth`                     | `^` / `^^` / `+N` | non-negative integer \| `direct` \| `all`                  | `0` (no propagation)         | unit                        |
| `Propagate-Scope`                     | none                 | scope-set                                                  | `*`                          | unit                        |
| `Propagate-Channel`                   | `%%x`             | `inherit` \| `none` \| `stable` \| `<ch>` \| `<from>><to>` | `inherit`                    | unit                        |
| `Propagate-Channel-Depth`             | `%%` / `++N`      | non-negative integer \| `direct` \| `all`                  | `0` (no channel propagation) | unit                        |
| `Propagate-Channel-Scope`             | none                 | scope-set                                                  | the unit's `Propagate-Scope` | unit                        |
| `Channel`                             | `%x`              | `<channel>` \| `stable` \| `<from>><to>`                   | inherited from baseline      | unit                        |
| `Release-As`                          | none                 | exact semver \| `none` \| `auto`                           | none                            | package, this window (§8.6) |
| `Reverts`                             | none                 | commit sha                                                 | none                            | unit; changelog (§7.3)      |
| `Edits`                               | none                 | `<sha>[#<n>]` \| `*`                                       | none                            | targeted records (§7.4)     |
| `Deletes`                             | none                 | `<sha>[#<n>]` \| `*`                                       | none                            | targeted records (§7.4)     |
| `Rollback-Version` | none | exact SemVer | required on rollback | explicit rollback target (§26) |
| `Rollback-Cancel` | none | full revision plus `#` and one-based unit index | none | pre-intent cancellation (§26) |

Unknown footer keys are ignored with `W150`, which keeps CCME compatible with organisation-specific trailers.

A footer value MAY span multiple lines: a continuation line is any line in the footer block that is not itself a footer
start (§20.5). Multi-line values are only meaningful for `BREAKING CHANGE`; for other keys the continuation is joined
with a single space before parsing, and a resulting invalid value is `E151`.

`Rollback-Version` is an additional CCME 3 footer: exactly one complete SemVer value is REQUIRED on a rollback unit
and forbidden on other types (`E301`). It has no inline shorthand and is not a version bump or `Release-As` alias.
`Rollback-Cancel` is valid only with `Rollback-Version` on an explicitly scoped rollback cancellation and names a full revision plus unit index; §26 defines its pre-intent-only semantics. Rollback units reject the other release directives listed above; see §26 for the complete validation rule.

### 8.1.1 `BREAKING CHANGE`: the exception to four rules

`BREAKING CHANGE` breaks more of this grammar's regularities than any other token, so its handling is collected here
rather than scattered.

**1. It is not a type.** Uppercase and containing a space, it fails §5.1 twice. A header line beginning with
`BREAKING CHANGE` is `E100` with a dedicated message (§5.1).

**2. It is the only footer key containing a space.** Every other key is `[A-Za-z0-9-]+`. The scanner therefore
special-cases the literal string before falling through to the generic key loop (§20.5). Implementations MUST NOT
generalise this into "keys may contain spaces": that would make ordinary body prose like `Note this is important: ...`
parse as a footer.

**3. It is case-sensitive, and it is the only key that is.** Conventional Commits 1.0.0 requires uppercase, so CCME does
too. `BREAKING CHANGE` and the hyphenated alias `BREAKING-CHANGE` are recognised; nothing else is.

This creates the format's most dangerous silent failure: `Breaking change: ...` or `breaking change: ...` is *not* a
breaking change, parses cleanly as an unknown footer, and ships a major change as a minor one. Implementations MUST
therefore emit `W155` for any footer key that equals `BREAKING CHANGE` or `BREAKING-CHANGE` under a case-insensitive
comparison but not under an exact one. Commit-lint implementations SHOULD reject it outright. The warning is not
optional; a silently-swallowed breaking change is the worst outcome this specification can produce.

**4. It only counts in the footer block.** A `BREAKING CHANGE:` line in the middle of a body is body text, per §4.4.
This is inherited from the base specification and is a second silent failure, so implementations MUST emit `W156` when a
line matching the footer form appears in a unit's body rather than its final paragraph.

**Binding.** A `BREAKING CHANGE` footer marks **its own unit** breaking, for that unit's resolved package set only. In a
multi-unit message it does not reach the other units; see vector 29. The `!` marker (§5.4) is exactly equivalent; both
MAY appear on one unit, and doing so is not an error, since the footer carries prose the marker cannot.

**Value.** The value is free text and MAY span several lines; continuations need no indentation, because a
`BREAKING CHANGE` footer consumes subsequent non-footer lines to the end of the block (§20.5). The value is never parsed
and never validated: an empty value is legal, though `W157` notes that a breaking change with no explanation is
unhelpful to consumers.

**Bump.** `major`, overriding whatever the type would have produced, subject to §12.6 for `0.y.z` packages. A
`BREAKING CHANGE` footer on `cancel` is `E171`; on `release` it is `E141`; on `rollback` it is `E301`.

### 8.2 `Propagate`

Declares the bump given to **dependents** of this unit's packages.

* `none`: do not touch dependents.
* `patch` / `minor` / `major`: give every reached dependent exactly that bump.
* `inherit`: give every reached dependent the same bump this unit produces (`feat` → `minor`, `!` → `major`, …).

Default is `patch`: when a unit does propagate, the dependents it reaches have had a dependency change under them but no
change to their own public API, so `patch` is the honest signal. The default applies only once propagation has been
asked for; `Propagate-Depth` is `0` unless a caret or `+N` says otherwise (§8.3).

### 8.3 `Propagate-Depth`

* `0`: no propagation. Equivalent to `Propagate: none`.
* `1` (or `direct`): direct consumers only.
* `N`: up to N edges away.
* `all`: the full transitive closure of dependents.

**The default is `0`: a unit does not propagate unless it says so.** A commit with no propagation directive releases its
own packages and nothing else.

Rationale: the blast radius of a commit should be legible from the commit. Any non-zero default versions packages the
author never named, in numbers that grow with the workspace, and the author cannot see it from the message they wrote,
which is precisely the property §18 asks of every directive. A default of `1` is the tempting middle ground, on the
argument that a consumer whose dependency changed must be republished so its lockfile and bundled artefacts pick up the
new code. That argument is real, but it is a property of how a given repository builds, not of any individual commit:
a consumer that *declares* a compatible range already resolves the new dependency without being republished, and only a
consumer that *bundles* it is genuinely stale. Repositories where bundling is the norm should say so once, by setting
`propagation.depth: 1` (§14), rather than have every author inherit a reach they did not write.

The cost of this default is that forgetting a caret under-releases, which is quieter than over-releasing. Two things
mitigate it: the release plan (§13.10) names every package it will release, so a missing consumer is visible before
publication rather than after, and the staleness audit of §13.7b answers "which of my packages are behind their
dependencies?" on demand and on a schedule.

The same argument applies verbatim to the channel axis, which is why it too defaults to `0` (§8.3a).

**The ladder.** `Propagate` and `Propagate-Depth` are independent keys, but the carets set both at once:

| Written     | Bump to dependents | Depth                                        |
|-------------|--------------------|----------------------------------------------|
| *(nothing)* | none                  | `0`, no propagation                          |
| `^`         | `patch` (default)  | `1`                                          |
| `^minor`    | `minor`            | `1`                                          |
| `+3`        | `patch` (default)  | `3`                                          |
| `^minor+3`  | `minor`            | `3`: the explicit depth wins over `^`'s 1    |
| `^minor+1`  | `minor`            | `1`, explicit, identical to `^minor`         |
| `+*`        | `patch` (default)  | all levels                                   |
| `^^`        | `patch` (default)  | all levels, identical to `+*`               |
| `^^minor`   | `minor`            | all levels, identical to `^minor+*`         |
| `^^minor+*` | `minor`            | all levels; legal, redundant, `W110`         |
| `^^minor+2` | none                  | `E113`: `^^` and `+2` both assert depth      |
| `+0`        | none                  | `0`, explicit, identical to writing nothing  |

`^` implies depth 1 only in the absence of an explicit depth; `+N` supplies one and wins, without error. `^^` differs:
it exists to assert "all", so disagreeing with an explicit `+N` is `E113` rather than a silent override.

**Precedence, and what it is not.** The chain below says which source *supplies* a value for a key that no
higher-priority source has set at all. It is **not** a conflict rule: two sources setting one key to *different* values
is `E112` (§5.3), and the chain is never consulted for that case.

Precedence for depth is: **footer `Propagate-Depth` → inline `+N` → inline `^`/`^^` → configured `propagation.depth` →
spec default `0`.** Channel depth has the exactly parallel chain: **footer `Propagate-Channel-Depth` → inline `++N` →
inline `%%` → configured `propagation.channelDepth` → spec default `0`.** The same chain, minus the sigil-implication
step, applies to every other directive key.

Footer sits above inline because that is the only order consistent with §5.3. In strict mode the question never arises,
since a header and a footer that disagree are `E112`, and in lenient mode §5.3 resolves it by letting the **footer** win with
`W112`. There is no configuration under which an inline directive overrides a footer.

Worked: `feat(core)^: x` carrying `Propagate-Depth: 3` sets `Propagate-Depth` twice, to `1` and to `3`, so it is
`E112`, not depth `1`, and not depth `3`. Under `lenient: true` the footer wins: depth `3`, `W112` (vector 85c). By
contrast `feat(core)^minor: x` carrying `Propagate-Depth: 1` sets it twice to the *same* value, which is `W110`, and
`feat(core)^: x` carrying `Propagate: minor` sets two *different* keys, which is neither.

Within one header the caret and an explicit `+N` are not two sources but one: `^` states a depth only in the absence of
an explicit `+N`, so `^minor+2` and `+2^minor` both mean depth `2` with no diagnostic (§20.3). `^^` is the exception,
because it exists to assert "all": `E113` in either order.

`^none` propagates nothing (the bump wins); `^minor+0` propagates nothing (the depth wins). Both are legal and mean "no
propagation".

The two are **not** the same diagnostic. `^none` and `+0` ask for nothing and get nothing: that is `W152`, redundancy,
because writing nothing says the same thing. `^minor+0` asks for something (a `minor`) and a depth of `0` throws it
away: that is `W201`, an inert value, because a nonzero propagation bump has no effect at depth `0`. See
§8.3b for the rule that separates them, which is the same on both axes.

### 8.3a `Propagate-Channel-Depth`

* `0`: no channel propagation. Equivalent to `Propagate-Channel: none`.
* `1` (or `direct`): direct consumers only.
* `N`: up to N edges away.
* `all`: the full transitive closure of dependents.

**The default is `0`: a unit does not move anybody else's channel unless it says so.** Writing `%%<value>` is itself the
opt-in and supplies a depth of `1`; an explicit `++N` overrides that without diagnostic, exactly as `+N` overrides the
caret's implied `1` (§8.3).

| Written     | Channel depth | Note                                               |
|-------------|---------------|----------------------------------------------------|
| *(nothing)* | `0`           | dependents keep their own channel                  |
| `%%beta`    | `1`           | the sigil supplies the depth                       |
| `%%beta++3` | `3`           | the explicit depth wins over `%%`'s 1              |
| `%%beta++1` | `1`           | explicit, identical to `%%beta`                    |
| `++*`       | all levels    | value defaults to `inherit`                        |
| `%%beta++*` | all levels    | the whole reverse closure joins the `beta` line    |
| `++0`       | `0`           | explicit, identical to writing nothing             |
| `%%none`    | none             | no channel propagation whatever the depth          |
| `%%none++*` | none             | legal, redundant, `W152`                           |
| `%%beta++0` | `0`           | legal, inert, `W201`: a value that reaches nobody  |

`%%none` propagates nothing (the value wins); `++0` propagates nothing (the depth wins). Both are legal, both mean "no
channel propagation", and both are `W152`. `%%beta++0` is `W201` instead, by §8.3b. This mirrors `^none`, `+0` and
`^minor+0` on the bump axis exactly.

### 8.3b Which diagnostic an inert directive earns

Both axes obey one rule, stated once here and referenced from §8.3 and §8.3a:

* **`W152`, redundancy.** Every part of the directive resolves to "nothing", so deleting the whole directive changes no
  behaviour: `^none`, `+0`, `^none+*`, `^^none`, `%%none`, `++0`, `%%none++*`.
* **`W201`, an inert value.** A **value** other than `none` is supplied on either axis and the depth on that axis
  resolves to `0`, so the value reaches nobody: `^minor+0`, `%%beta++0`, and the footer forms `Propagate: minor` or
  `Propagate-Channel: beta` where nothing sets the corresponding depth above `0`.

Where the rule selects `W201`, `W152` is **not** also emitted: `W201` is the more specific finding and names the actual
mistake, and two diagnostics for one token would only obscure which part of it was wrong.

`W201` is a warning rather than an error because a repository that sets `propagation.depth` or
`propagation.channelDepth` above `0` makes the same footer meaningful. The unit is not wrong in itself, only inert here.

"Supplied" means written by the unit (in the header or in one of its footers) never taken from configuration.
`inherit` written by the unit counts; the `propagation.channel` default does not. So `^inherit+0` is `W201`, because the
unit named a bump and then discarded it, while a bare `++0` is `W152`, because the unit named no channel at all and the
`inherit` it would otherwise have used came from `propagation.channel` (§14).

**Why this defaults to `0`.** The rationale of §8.3 applies with more force here, not less. A propagated bump changes a
package's version; a propagated channel changes *which line it publishes on*, which decides what installers resolve by
default and can end or begin a release train. That is a larger consequence, it is invisible in the message unless the
message says it, and it grows with the workspace. `%%beta++*` on a widely-depended package moves an entire reverse
closure onto a prerelease line in one commit (§18.1), so the reach belongs in the commit that asks for it.

The default costs less than it appears to, because **a channel is derived from a tag** (§11.1). A package already on a
prerelease line stays on it whatever this unit says, so depth `0` does not fragment an existing train; it only stops
the train recruiting packages that were not on it. Where consumers genuinely should be dragged along, `++1` or `++*`
says so in three characters, and a repository whose answer is always the same says it once with
`propagation.channelDepth` (§14).

### 8.4 Which edges propagate

The manifest fields traversed as graph edges are a property of the **workspace**, set once by `propagation.kinds`
(§14), and default to `dependencies`, `peerDependencies`, and `optionalDependencies`.

`devDependencies` is excluded: a package that uses another only for its test suite does not need republishing when that
other package changes, and including such edges would make almost every workspace one large strongly-connected blob.
The wildcard `"*"`, reusing the scope-set selector of §5.2, selects every kind, `devDependencies` included, for
repositories that accept that trade.

There is deliberately **no per-unit override**. Which dependency fields imply "must be republished" is a fact about how
the repository builds and ships, not about any individual change, and it does not vary from commit to commit. A per-unit
form would let two commits touching the same packages disagree about the shape of the graph, which makes the blast
radius of a change unreadable from the message and unreviewable in the plan, the same argument that keeps
`Release-As` free of a bump form (§8.6). Repositories whose answer genuinely differs by area should say so once, in
configuration, rather than on every commit.

### 8.5 `Propagate-Scope`

Restricts the **bump axis** to a subset of the workspace. The reached-dependent set is intersected with the resolved
scope-set of this footer before bumps are applied.

```
feat(@acme/core)^^minor: new plugin API

Propagate-Scope: @acme/*, -@acme/experimental-*
```

Useful when a workspace contains both published packages and internal apps that must not be versioned.

The value is an ordinary scope-set (§5.2, §6.1), so every form is available: names, globs, `.`, `*`, and `-` exclusions.
Unknown includes are `E130` and unknown excludes are `W130`, as everywhere else. If the intersection is empty, no bump
propagates and `W135` is emitted.

### 8.5a `Propagate-Channel-Scope`

Restricts the **channel axis** in exactly the same way, and takes exactly the same value grammar.

```
release(@acme/core)%stable%%beta>stable++*: graduate the 2.0 train

Propagate-Channel-Scope: @acme/*, -@acme/legacy-adapter
```

**Its default is the unit's `Propagate-Scope`**, which itself defaults to `*`. A unit that restricts propagation once
therefore restricts both axes, which is nearly always the intent; a unit that needs them to differ says so with the
second footer. Writing `Propagate-Channel-Scope` never changes what the bump axis reaches.

This is the exclusion operator for graduation. Two levels are available and they do different jobs:

| Written                                      | Excludes                                                              |
|----------------------------------------------|-----------------------------------------------------------------------|
| `release(@acme/*,-@acme/legacy)%beta>stable` | `@acme/legacy` from the unit's **own** packages                       |
| `Propagate-Channel-Scope: *, -@acme/legacy`  | `@acme/legacy` from the **dependents** the channel reaches            |
| `Propagate-Scope: *, -@acme/legacy`          | `@acme/legacy` from both axes, since the channel scope defaults to it |

If the intersection is empty, no channel propagates and `W205` is emitted, the channel-axis counterpart of `W135`.

### 8.6 `Release-As`

`Release-As` decides **whether and at what version a package is released in this window**. It takes exactly three
values, all of which operate at the same level, the package, for the current window:

| Value                  | Meaning                                                                                                                                      |
|------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `4.0.0` (exact semver) | **Pin.** Publish exactly this version. MUST be strictly greater than the baseline (`E153`) and not lower than the computed version (`E156`). |
| `none`                 | **Hold.** Do not release these packages in this window. Pending units are *retained*, not discarded.                                         |
| `auto`                 | **Resume.** Lift an active hold and return to normal computation.                                                                            |

`Release-As` does **not** override a bump. There is deliberately no `Release-As: minor`.

**Why there is no bump form.** How large a change is, is a property of the change, and the type already declares it. A
commit that warrants a `minor` should say `feat`. If a whole *category* of change should release in a given repository,
say `build` commits because that repository ships compiled artefacts, that is a standing property of the repository,
expressed once in `types` (§14), not restated as a footer on every commit. Allowing a per-unit override would let a
commit's type and its release effect disagree, so the changelog would say one thing and the version another. Keeping
`Release-As` to whole-release decisions leaves exactly one place to look for "what does this commit do to versions": the
type, plus `!`.

`Release-As` with an exact version applied to a scope-set of more than one package is `E154`: two packages cannot both
become `4.0.0` unless they happen to share a baseline, and allowing it invites accidents. Use one `release` unit per
package.

**Precedence.** For each package, consider every surviving unit in the window carrying a `Release-As` whose resolved
scope includes that package. The directive from the **newest commit** wins; within a commit, the **last unit** wins.
`W153` is emitted when this discards a competing directive.

Because all three values live at one level, that single rule gives the hold/resume sequence its behaviour for free: no
state machine is needed, since "is this package held?" is answered by looking at one winning directive rather than by
replaying history.

### 8.6.1 Holds

`Release-As: none` is a **pause on publishing**, not an erasure of history.

```
release(@acme/core): hold pending disclosure

Embargoed until the coordinated disclosure on the 14th.

Release-As: none
```

While a package is held:

* it is excluded from the release plan (`W154`);
* it is **not** a propagation source *for work it has not yet released*: its dependents are not bumped on its behalf,
  because publishing `cli` against an unpublished `core@1.5.0` would produce a broken artefact. Work the package
  published **before** the hold keeps propagating: `core@1.5.0` being public is precisely what makes `cli`'s release
  safe, and a hold applied afterwards does not un-publish it (§13.4a);
* its pending units remain pending. They accumulate. Nothing is lost.

A hold persists across release runs until it is lifted, because it is a fact in history like any other directive. There
are two ways to lift it.

**Resume with the computed version, `Release-As: auto`:**

```
release(@acme/core): embargo lifted

Release-As: auto
```

The window becomes active again and everything that accumulated releases at the `max()` of all of it, including the
units that predate the hold. This is the default choice: the embargo is over, ship what the ledger says.

**Resume at a named version, `Release-As: <version>`:**

```
release(@acme/core): ship the embargoed release

Release-As: 1.5.0
```

Same effect, but the version is stated rather than derived. Use it when a coordinated launch has already published the
number somewhere a human can read.

Both are ordinary package-level directives, so the precedence rule above applies unchanged: whichever of `none`, `auto`,
or an exact version sits in the newest commit wins. A hold followed by `auto` followed by another `none` holds again:
the sequence is read positionally, and each directive supersedes the last.

`Release-As: auto` with no active hold is a harmless no-op (`W158`).

**What does *not* lift a hold.** Only the two forms above, plus a `cancel` whose barrier discards the holding unit. In
particular:

|                                              | Lifts a hold?          |                                                                 |
|----------------------------------------------|------------------------|-----------------------------------------------------------------|
| `Release-As: auto`                           | **Yes**                | Resume, computed version                                        |
| `Release-As: 1.5.0`                          | **Yes**                | Resume, named version                                           |
| `cancel(pkg)`                                | **Yes**, destructively | Discards the holding unit *and* the accumulated ledger (§8.6.2) |
| An ordinary `feat` / `fix` / breaking change | No                     | Accumulates into the pending ledger; the package stays held     |
| A channel directive (`%beta`, `%stable`)     | No                     | Recorded and re-evaluated when the hold lifts (#73j)            |
| A propagated bump from an unheld dependency  | No                     | Recorded, not released (§13.7)                                  |

This is the property that makes a hold usable as an embargo. If ordinary commits lifted it, a routine typo fix landing
on day two would publish the embargoed release, and the mechanism would be worthless. Ending a hold is therefore always
a deliberate, reviewable act: the diff shows either `auto`, a version number, or a `cancel`.

The corollary is that a forgotten hold blocks a package indefinitely (#73a). That is intentional, but it is also why
`W154` MUST be reported on every run rather than only on the run that creates the hold: a held package should be visible
in CI output for as long as it is held.

So that a named version does not have to be derived by hand, implementations MUST include the version the package
*would* have received in the `W154` diagnostic and in the release plan, so it can be read off the previous run's output.

Guard-rail: an exact `Release-As` that is **lower than the computed version** is `E156`. Naming `1.5.0` when a breaking
change has landed and `2.0.0` was computed would publish an incompatible release under a compatible number, the one
mistake the named-version form invites. `auto` cannot make this mistake, which is why it is the default choice. Lenient
mode downgrades `E156` to `W159`.

A `cancel` also ends a hold, by discarding the unit that carried it, but it discards the accumulated work along with
it. That is the difference the next section is about.

### 8.6.2 `Release-As: none` versus `cancel`

Both stop a release from happening. They differ in what survives.

|                                        | `Release-As: none`                                                                       | `cancel`                                                                                         |
|----------------------------------------|------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------|
| Nature                                 | A pause: "not yet"                                                                       | An erasure: "never"                                                                                |
| Pending units                          | Retained, still accumulating                                                             | Discarded permanently                                                                            |
| When lifted / afterwards               | Everything accumulated releases, at the `max()` of all of it                             | Only post-barrier commits count; pre-barrier work is unrecoverable                               |
| Changelog entries                      | Deferred, then published                                                                 | Dropped                                                                                          |
| Reversible                             | Yes: `Release-As: auto`, or a newer exact version                                       | No. Restoring the intent means rewriting the commits                                             |
| Ends by                                | An explicit newer directive                                                              | Nothing; the barrier is permanent                                                                |
| Affects the baseline of interpretation | No                                                                                       | Yes: history before it is invisible                                                             |
| Propagation from these packages        | Suppressed while held                                                                    | Suppressed permanently, because there is no bump to propagate                                    |
| Typical use                            | Embargo, coordinated launch, waiting on a downstream consumer, a broken publish pipeline | Migration artefacts, a mistyped commit on a protected branch, restating a changeset from scratch |

The one-line test: **if you would be upset to lose the changelog, you want a hold, not a cancel.**

Worked contrast, from `core@1.4.2`. Each line below summarises one commit; `«…»` marks a footer that in the real
message sits in the unit's final paragraph, not on the header line:

```
commit 1   feat(core): streaming reader
commit 2   release(core): hold          «Release-As: none»
commit 3   fix(core): guard against empty input
commit 4   release(core): resume        «Release-As: auto»
```

→ `core` releases once, at commit 4, as `1.5.0`, with both entries in its changelog. `Release-As: 1.5.0` at commit 4
would do the same thing with the number stated.

```
commit 1   feat(core): streaming reader
commit 2   cancel(core): reset release state
commit 3   fix(core): guard against empty input
```

→ `core` releases `1.4.3`. The feature is gone from the ledger; only the fix remains. The streaming reader is still *in
the code* (nothing was reverted) it simply no longer counts as a release event, and no changelog entry will ever
mention it.

That last consequence is the reason `cancel` is narrow by design. It is the right tool when the metadata was never
meaningful (an importer's guess, a typo). It is the wrong tool for anything you intend to ship later.

---

## 9. Propagation

### 9.1 Model

Propagation is an effect assigned to a package *because one of its dependencies changed*, as opposed to a **direct**
effect assigned because the package itself changed. There are two propagated effects, and they combine differently.

**Bumps accumulate.** The effective bump for a package is:

```
effective(P) = max( direct(P), propagated(P) )
```

This single rule delivers the intended behaviour: a package that has its own `feat` in the window is unaffected by an
incoming `patch` propagation, while a package with no changes of its own receives the propagated bump. Propagation can
only ever raise a version, never lower or replace a direct decision.

**Channels are chosen.** `max()` is meaningless over channel names, because `beta` and `rc` are alternatives rather than quantities,
so a package's channel is selected rather than accumulated: a direct directive beats a propagated one, and among equals
the newest commit wins, then the last unit within it (§13.8).

The two axes are computed by the same traversal machinery and never mix. A propagated channel neither raises nor lowers
a bump, and a propagated bump never implies a channel. What one axis *does* constrain is what the other is allowed to
mean: a bump propagated from a release the target cannot resolve is not a real obligation, which is the rule of §9.3a.

### 9.2 Computation

Propagation is computed after all direct bumps are known, after cancellation (§10) has removed discarded units, and
after holds (§13.6a) are resolved.

Propagation is admitted **per target**. A unit remains a propagation source for exactly as long as some potential target
has not yet released past it, *not* merely for as long as the unit's own packages have unreleased work. This
distinction is what makes a release resumable, and it is stated normatively in §13.4a; §13.7a explains why the
alternative reading loses releases.

It runs in **three phases**, in this order:

1. the **channel axis**: every unit's `Propagate-Channel*` directives, producing a proposed channel per package;
2. **channel resolution** (§13.8): direct directives beat propagated ones, yielding `channel(P)` for every package;
3. the **bump axis**: every unit's `Propagate*` directives, admitted only where the origin's release is resolvable by
   the target (§9.3a).

The order is forced and the phases MUST NOT be merged: phase 3 reads `channel(P)`, which phase 2 produces. There is no
circularity in the other direction, because phase 1 reads only the units and the packages' **baselines**, never any
value computed in this run, and because a unit never propagates to its own source packages.

**What that serialization costs.** It is a real one, and it is the price of §9.3a: making a bump's admission depend on
resolved channels means no bump can be decided until every channel is. The two traversals therefore cannot be fused into
a single walk of the graph, and an implementation MUST NOT try. But the cost is bounded and small, and it is worth being
precise about what it is *not*:

* It costs **a second pass over the unit list** and a second set of BFS traversals over the dependency graph.
* It does **not** cost a second pass over history. Commit walking, window computation, parsing, and scope resolution all
  happen once, before §9.2 runs, and both passes read their results (§13.3, §13.4). The expensive part of a run, going
  to the object store, is unaffected by the phase split.
* It does **not** double the graph work in practice. The two passes filter on different directives, and the channel pass
  sees only units that carry one at all: in a repository not running a prerelease train that is none of them, and the
  pass is skipped outright (§13.11).

So the honest accounting is `2 ×` a quantity that is usually `1 ×` a small number, layered on top of history work that
is paid once either way.

```
propagate(units, graph, held, W):
    edges = graph.restrictedTo(config.propagation.kinds)          # §8.4

    # ---------- shared, computed once ----------
    # sourcePackages is a pure function of the unit (§13.4a) and both passes read it.
    # Computing it inside each pass evaluates it twice for every unit carrying both axes.
    src = { u: sourcePackages(u) for u in units }                 # §13.4a

    # ---------- phase 1: channel axis ----------
    chan = {}                                   # package -> (channel, commit, unitIndex)
    for u in units:
        if u.channelDepth == 0:                     continue      # §8.3a: default
        if u.propagateChannel == none:              continue
        sources = src[u]
        if sources is empty:                        continue
        c      = commitOf(u)                                      # loop-invariant
        pscope = resolve(u.propagateChannelScope)                 # §8.5a: resolved once,
        origin = UNCOMPUTED                                     # memoized on first admitted target
        for (d, level) in reach(edges, sources, u.channelDepth):  #   never per target
            if d not in pscope:                           continue
            if c not in Wfresh(d):                             continue # admission, §13.4a
            if cancelledFor(c, d):                        continue # §13.5a
            if u.propagateChannel == 'inherit' and origin == UNCOMPUTED:
                origin = originChannel(u)
            v = propagatedChannelFor(u, d, origin)                 # §9.3; origin computed once
            if v is NONE:                                 continue
            chan[d] = newerOf(chan[d], (v, u))                     # W160 on conflict

    # ---------- phase 2: channel resolution ----------
    channel = resolveChannels(chan, units)                         # §13.8

    # ---------- phase 3: bump axis ----------
    prop = {}                                   # package -> bump
    prov = {}                                   # package -> set of origin packages
    for u in units where bumpOf(u) != none:
        b = (u.propagate == 'inherit') ? bumpOf(u) : u.propagate
        if b == none or u.depth == 0:                continue
        sources = src[u]
        if sources is empty:                        continue
        c       = commitOf(u)
        pscope  = resolve(u.propagateScope)                        # §8.5: once per unit
        # §9.3a resolvability depends only on the unit's sources, so it is decided here,
        # once, and reduces to membership in the distinct source-channel set per target.
        srcChan   = { channel[P] : P in sources }                  # §9.3a
        anyStable = 'stable' in srcChan
        for (d, level) in reach(edges, sources, u.depth):
            if d not in pscope:                           continue
            owed = { P in sources : not delivered(P, c, d) }    # admission, §13.4a
            if owed is empty:                             continue
            if cancelledFor(c, d):                        continue
            if not (anyStable or channel[d] in srcChan):  continue # §9.3a, W208
            prop[d] = max(prop[d], b)
            prov[d] = prov[d] | owed
    return prop, prov, chan
```

Everything hoisted above the target loop is loop-invariant, so this is the same computation written to evaluate each
invariant once. It matters because the target loop is the one that scales: a unit written `^^` over half the workspace
runs it thousands of times, and `resolve(<scope>)` and `resolvableBy` are not `O(1)`. Written literally, resolving the
scope-set and re-scanning the source set inside the loop, a single such unit costs
`O(|targets| · (|scope| + |sources|))`
where it should cost `O(|targets| + |scope| + |sources|)`.

Both axes use one traversal, which is where "depth" is defined:

```
reach(edges, sources, depth):
    frontier = sources
    seen     = set(sources)                     # a unit never reaches its own packages
    out      = {}                               # package -> level
    for level in 1 .. depth:                    # 'all' => until frontier is empty
        next = {}
        for p in frontier:
            for d in edges.dependentsOf(p):
                if d in seen: continue
                seen.add(d); next.add(d)
        if next is empty: break
        for d in next: out[d] = level
        frontier = next
    return out
```

Properties. Except where noted, each holds for **both** axes:

* **Per unit.** Each unit propagates independently from its own package set with its own settings. Bumps merge by
  `max`, so units never conflict; channels merge by recency, so conflicts are resolved rather than rejected (`W160`).
* **Single-visit.** The graph is acyclic (§13.1), so termination is not in question; `seen` additionally guarantees that
  a package reachable by several paths (a diamond) is visited once, at its shortest depth.
* **Depth is shortest-path.** A package reachable at both depth 1 and depth 3 is treated as depth 1 and is therefore
  included by `+1` or `++1`.
* **Depth is measured from the originating source set, always.** It is never recomputed from an intermediate package,
  and never re-based on a package that happens to be republishing. A unit written `+1` reaches exactly the direct
  consumers of its own packages, in this run and in every later catch-up run, whatever released in between.
* **Admission is the target's releases.** On the channel axis a dependent is reached if and only if it has not itself
  released past the commit carrying the unit. On the bump axis it is reached if and only if some source of the unit has
  not yet **delivered** that commit to it: it has not released at or after a release of that source which carries the
  commit (§13.4a). The two coincide except for a dependent that released past the commit before its source did. Every
  guarantee in §13.7c rests on this one test, and on it being the target's position rather than the source's.
* **Propagation does not cascade its own propagation.** A propagated bump on `B` does not itself trigger a fresh
  propagation pass from `B`, and a propagated channel on `B` does not carry onward to `B`'s dependents; the depth
  parameters of the originating unit are the only controls. This keeps the result a pure function of the units and
  prevents surprising blast radius. In particular, a package republishing as a *catch-up* does not propagate onward
  either; otherwise a failed publish would enlarge the blast radius of a commit after the fact, which §18 forbids.
* **The bump axis requires a bump; the channel axis does not.** A unit whose type maps to `none` propagates no bump at
  any depth (#38a), but MAY still propagate a channel, which is what makes `release` usable for graduation (§7.2).
* **Ranges are ignored by default.** Whether a dependent's declared range (`^1.2.0`) already admits the new version does
  not affect propagation, because range satisfaction is not stable across lockfile regeneration. Setting
  `propagation.respectRanges: true` (§14) skips the bump axis for dependents whose range still admits the new version;
  this is a deployment choice, not a spec default, and it does not affect the channel axis.

### 9.3 The channel axis

`Propagate-Channel` decides what channel the dependents reached by `Propagate-Channel-Depth` are released on:

* `inherit` (default): the same channel as the originating unit's own packages;
* `none`: nothing propagates, whatever the depth (§8.3a);
* `stable`: the stable channel, subject to the non-graduation rule below;
* `<channel>`: an explicit channel;
* `<from>><to>`: a **transition** (§5.3), where only dependents whose baseline channel is `<from>` move, and they move to
  `<to>`.

```
propagatedChannelFor(u, d, origin):
    spec = u.propagateChannel                    # default config.propagation.channel
    cur  = channelOf(baseline(d))                # §11.1: from d's tag, not from this run

    if spec is a transition (from, to):
        if from == to:              warn W207; return NONE
        if not matchesFrom(cur, from):           return NONE      # W206 if nothing matches
        target = to
    else:
        target = (spec == 'inherit') ? origin : spec
        if target == 'stable' and cur != 'stable':
            warn W200;                           return NONE      # never graduates; below

    if target == cur:               warn W199;   return NONE      # already there
    return target

matchesFrom(cur, from):
    if from == '*': return cur != 'stable'       # '*' is any prerelease, never stable
    return cur == from
```

If a package receives conflicting propagated channels from two units, the unit in the newest commit wins, then the last
unit within that commit (`W160`). If a package has a **direct** channel directive in the window, it always wins over any
propagated channel (§13.8).

**A propagated channel never graduates implicitly.** If a non-transition propagated channel resolves to `stable` for a
dependent whose baseline is a prerelease, the dependent is **not** graduated: it keeps its own channel, is released on
it as usual if anything else releases it, and `W200` reports the suppression. Graduation ends a prerelease train and
publishes under a version consumers will resolve by default, and it MUST NOT happen because an unrelated package's
commit propagated a channel to it. The rule holds however the `stable` arose: written as `%%stable`, configured as
`propagation.channel`, or inherited from an origin that happens to be stable.

**The transition form is the deliberate exception, and the only one.** `%%beta>stable` *does* graduate the dependents
whose baseline is on `beta`, because the author had to name the train being ended in order to write it. That is the
reviewability property `W200` exists to protect, not "graduation never propagates", but "graduation never happens by
accident". A transition is visible in the message, its `<from>` is checkable against the plan, and it is idempotent:
dependents that already graduated do not match and are untouched, which is what makes it safe to leave the same
directive in a long-lived release script. `%%*>stable` is the broad form and is subject to the same review as any other
broad directive (§18.1); `requireCodeownerFor` may name it (§14.1).

Moving *onto* a prerelease line is unrestricted: `%%beta++*` puts an entire reverse closure on the beta line, which is
the point of the operator and is safe because the stable baselines are untouched and §11.4 recomputes each train from
them. It is nonetheless a real widening of blast radius and is bounded by `maxPackagesPerRun` (§14.1).

**Resolving `inherit`.** In a catch-up run (§13.7a) the originating packages are typically no longer in the plan, so
"the channel of the origin's release" has to be defined without one. `originChannel(u)` resolves, in order:

1. the channel `u`'s own `Channel` directive assigns to the origin, if `u` carries one;
2. otherwise `channelOf(baseline(origin))`, the channel the origin was last published on;
3. if a unit's source packages disagree, the value from the byte-wise least package name, with `W160`.

Every input is read from the unit and from tags at `HEAD`, so the result is deterministic, is independent of run order only while those baseline tags are unchanged, and does not depend on any other unit. The practical effect is the
intended one: a consumer dragged along by a dependency that shipped on `beta` also ships on `beta`, and the prerelease
train stays installable as a set.

**Discharge of the channel axis.** Both axes use the same fresh-work gate: once `d` releases, the commit leaves
`Wfresh(d)`, so neither its bump nor its channel can be re-admitted (§13.7c G4). The final
`propagatedChannelFor` comparison against `channelOf(baseline(d))` remains independently necessary while work is fresh:
`target == cur` produces `W199`, and a transition whose `<from>` no longer matches proposes nothing. That comparison
prevents a no-op channel proposal; it is not the ledger-discharge mechanism and MUST NOT be used in place of
`C ∈ Wfresh(d)`.

### 9.3a Propagation follows resolvability

> A propagated bump is applied to dependent `d` only if at least one of the unit's source packages will be released in
> this run on a channel `d` can resolve, that is, on `stable`, or on `d`'s own resolved channel. Otherwise the bump is
> suppressed and `W208` is reported.

```
resolvableBy(sources, d, channel):
    for P in sources:
        if channel[P] == 'stable':      return true
        if channel[P] == channel[d]:    return true
    return false
```

**Evaluate it once per unit, not once per target.** The predicate reads `d` only in the single comparison
`channel[P] == channel[d]`; everything else is a function of the unit's source set alone. So the source scan is
loop-invariant and MUST NOT sit inside the target loop, where it costs `O(|sources| · |targets|)` per unit for an answer
that depends on `|sources|` distinct channels. Hoisted:

```
srcChannels(u, channel):                        # once per unit, O(|sources|)
    return { channel[P] : P in sourcePackages(u) }

# then, per target, O(1):
    srcChan   = srcChannels(u, channel)
    anyStable = 'stable' in srcChan
    admit(d)  = anyStable or channel[d] in srcChan
```

`srcChan` holds one entry per *distinct* channel among the sources, so it has one or two elements in almost every real
unit and rarely more than three: a repository does not run many trains at once. The per-target test is a membership
check on a set that size, which is where a predicate evaluated across a wide `^^` belongs. This is the form §9.2 phase 3
is written in, and the two are the same function: `admit(d)` is `true` exactly when `resolvableBy(sources, d, channel)`
is, because a set contains `channel[d]` exactly when some `P` in `sources` has `channel[P] == channel[d]`.

The cost is worth naming because of where it lands. A unit carrying `^^` over a scope-set covering half a large
workspace admits thousands of targets from a source set of one or two packages; the literal reading re-walks those
sources thousands of times to re-derive an answer that never changed. It is the one place in §9.2 where a naive
transcription is quadratic in quantities that are both large.

The rule exists because a bump is a claim that the dependent has something new to pick up, and across a channel boundary
that claim is false. If `core` releases `1.5.0-beta.0` and `cli` stays on `stable`, then `cli` resolves `core` by its
stable range exactly as before: a republished `cli@2.0.1` would contain byte-for-byte what `cli@2.0.0` contained, and
would be a release with no content. Worse, §9.4 would reconcile `cli`'s declared range against `core`'s new version, so
a **stable** `cli` would ship declaring a dependency on a **prerelease**, the one outcome §9.4 exists to prevent.

The consequence is the one to hold on to: **`feat(core)^%beta` releases `core` alone.** The caret is honoured, the
dependents are reached, and every one of them is suppressed because none of them is on the beta line. To take the
consumers along, put them on the line (`feat(core)^%beta++1`) and the suppression does not apply, because they are
then released on `beta` themselves.

Four details, each a place an implementation drifts:

* **`stable` is resolvable by everyone.** A dependent on `beta` whose dependency releases a stable version is bumped
  normally; prereleases are the asymmetric case, not channels in general.
* **The test uses `channel(d)` as resolved in this run**, not `d`'s baseline channel. A dependent that the channel axis
  has just moved onto `beta` is on `beta` for this purpose, which is exactly what makes `^%beta++1` work in one commit.
* **Any one source suffices.** A unit whose scope-set spans packages on different channels propagates its bump if any of
  them releases something the target can resolve.
* **A source that has already released still counts, on the channel it released on.** In a catch-up run (§13.7a) the
  origin is typically not in this run's plan, and §13.8 assigns it the channel of its baseline, which is precisely the
  channel its published artefact sits on. The test therefore asks the right question in a catch-up run without any
  special case: *can the target resolve what the origin actually published?*

`W208` is reported per suppressed `(unit, dependent)` pair. It is a warning rather than an error because the commit is
not wrong (a prerelease that deliberately does not disturb its consumers is a normal and good thing to write) but the
reader of a plan should be able to see that a caret was written and did not reach.

### 9.4 Propagation and manifest ranges

When a package is released, the release engine MUST update, in that package's manifest, the declared range for **every
workspace dependency**, according to `rangeStrategy` (§14), not only for those dependencies released in the same run.
This is an implementation obligation, not part of the commit syntax, but it is normative: a released package MUST NOT be
published with a range that excludes the version its workspace dependency carries at the end of the run: its planned
version where it is being released in the same run, its baseline otherwise (§19.5).

The breadth of that obligation is load-bearing, and narrowing it is a tempting mistake. Restricting reconciliation to
"released in the same run" is correct only if every propagation is discharged in the run that creates it. It is not:
a dependency may have been published by an *earlier* run whose dependent leg failed (§13.7a), or a dependent may already
have been published earlier in this same run before its dependency was, which is why §19.2 also fixes the publish
order. Reconciling against the dependency's current version closes both holes with one rule, and is a no-op whenever the
narrow rule would already have been correct. `W197` reports a range reconciled against a dependency that was released by
an earlier run.

**Reconciliation across a channel boundary.** A package released on `stable` whose workspace dependency currently
carries a **prerelease** version is the one case this rule cannot make safe, because there is no range that both admits
the prerelease and is honest about it. §9.3a prevents the common way of arriving there, since a prerelease no longer drags
its stable consumers into a release, but it remains reachable deliberately, most often by graduating a consumer while
its dependency stays on a train (`%%beta>stable` applied to a package whose provider is still on `beta`). The engine
MUST still reconcile, so that the published artefact resolves, and MUST report `W203` naming both packages and both
versions. The remedy is always the same: graduate the provider too, or do not graduate the consumer yet.

---

## 10. `cancel`

### 10.1 Purpose

`cancel` discards **unreleased release metadata** for a set of packages. It exists because release metadata accumulates
in history where it cannot be edited:

* A repository migrated onto Conventional Commits inherits thousands of commits whose messages an importer heuristically
  classified. Those inferred `feat`/`fix` markers are usually wrong, and no existing mechanism removes them without
  rewriting history.
* A commit lands with an incorrect type, or a bot-generated commit is misclassified, on a protected branch where
  rewriting is not an option.
* A release train is abandoned and the team wants to restate the changeset from scratch.

`revert` cannot do this: reverting a commit that was mis-tagged `feat!` produces a *second* release event, not the
absence of one.

**When not to use `cancel`.** If the changes are real and you intend to ship them later (an embargo, a coordinated
launch, a broken publish pipeline), use `Release-As: none` (§8.6.1). A hold defers; a cancel destroys. `cancel` discards
changelog entries irrecoverably, and the only way to get them back is to rewrite the commits that produced them. Reach
for it when the metadata was never meaningful in the first place. And when only one commit's record is wrong, prefer
the corrections of §7.4: `Edits:` restates the one record and `Deletes:` discards it, leaving the rest of the ledger
intact.

### 10.2 Syntax

```
cancel[(<scope-set>)]: <description>
```

* The scope-set follows the ordinary rules of §6. Omitted → file-derived, which for an otherwise-empty commit is the
  empty set; authors SHOULD therefore always write an explicit scope, most often `cancel(*)`.
* `!` on a `cancel` unit is `E170`.
* Inline directives and the footers of §8.1 on a `cancel` unit are `E171`. `cancel` takes a scope-set and nothing else.
* **Message-level trailers (§4.5) are exempt from `E171`.** `git commit -s` appends `Signed-off-by:` to the end of the
  message, which is the last unit, often the `cancel`. Rejecting that would make `cancel` unusable on any repository
  with a DCO hook. Unknown footer keys are likewise ignored (`W150`) rather than rejected; `E171` applies only to the
  release-directive keys of §8.1.
* The description is REQUIRED by the grammar and is **semantically ignored**. It carries no target reference, no reason,
  no metadata. Recommended text: `reset release state`.

Deliberate non-features: `cancel` names no commit, no version, no date, and no reason. It is a positional barrier,
nothing else. Anything that must be explained belongs in the body or in a `revert`.

### 10.3 Semantics

> For each package `P` in the resolved scope-set of a `cancel` unit in commit `C`: every unit in the pending window of
> `P` that is contained in a commit which is an **ancestor of `C`, or `C` itself**, is discarded for `P`.

Consequences:

* `cancel` produces no bump for any package.
* `cancel` never deletes, moves, or rewrites a git tag. Published versions are immutable; cancellation only affects what
  has *not* been released.
* The baseline for `P` is unchanged. After a cancel, the next release for `P` is computed from the same baseline tag,
  using only the units in commits after the barrier.
* If no units remain for `P` after the barrier, `P` is not released. Its baseline stands.
* Multiple `cancel` units for the same package are cumulative: a unit is discarded if its commit is an ancestor-or-self
  of **any** applicable cancel commit. No "latest cancel wins" rule is needed, and none is defined.
* A `cancel` for a package with nothing pending is a valid no-op (`W170`).

### 10.4 The ancestry rule and non-linear history

Ancestry, not commit date or log order, defines the barrier. This is what makes `cancel` deterministic under merges and
rebases.

```
        A(feat core) ──── B(cancel core) ──── D(merge) ── HEAD
                     \                       /
                      C(feat core, branched from A)
```

* `A` is an ancestor of `B` → discarded.
* `C` is **not** an ancestor of `B` → **retained**.

`C`'s author branched before the cancel and never saw it; discarding their work because of a wall-clock ordering would
be unpredictable. The rule is therefore: *a cancel affects exactly the history that the cancel's author could see.* To
also discard `C`, place a second `cancel` after the merge.

Corollaries:

* Rebasing a `cancel` commit changes its ancestor set, and therefore changes its effect. This is correct and intended.
* Cherry-picking a `cancel` into another branch creates an independent barrier over that branch's ancestry.
* A `cancel` in a commit that is not reachable from `HEAD` has no effect.

### 10.5 Cancel and prereleases

* `cancel` does not delete published prerelease tags and does not change the channel a package is on.
* If a package sits at `1.3.0-beta.4` and every pending unit is cancelled, the package stays at `1.3.0-beta.4` and is
  not re-released.
* Cancelling does not graduate. To leave a prerelease line, use `release(pkg)%stable`.
* Because the pending window is measured from the last **stable** tag (§13.3), a `cancel` also discards units that
  contributed to already-published prerelease versions. Those prereleases remain published, but the eventual stable
  version will be computed without the cancelled units, which is precisely the "restate the changeset from scratch"
  behaviour. `W171` is emitted when a cancel discards units that were already reflected in a published prerelease.

### 10.6 Migration recipe

The intended workflow for adopting CCME in a repository with pre-existing history:

```
cancel(*): reset release state

Adopting CCME. All classification of pre-migration history is discarded;
released versions in tags remain authoritative.
```

Commit this as the first commit after adoption. Every package's ledger restarts from its current tag, with no rewriting
of history and no tag surgery.

---

## 11. Prerelease flow

### 11.1 Channel state

A package's channel is **derived from tags**, never stored in a side file:

* baseline is `1.4.2` → channel `stable`.
* baseline is `1.5.0-beta.3` → channel `beta`.
* no baseline → channel `stable` unless a directive says otherwise.

This keeps state recoverable from a clone with no extra files, and makes a wrong channel fixable by tagging.

Two consequences are used throughout §9 and §11. A package **stays** on its channel with no directive at all, so an
established prerelease train needs nothing written to keep it together; and the channel a **transition** matches against
is this baseline-derived one, never a value computed earlier in the same run (§9.3).

### 11.2 Channel names and channel values

A **channel name** is the prerelease identifier itself:

* Charset: `a`–`z`, `0`–`9`, `-`. MUST begin with a letter. Length 1–32.
* Reserved and MUST NOT be used as a prerelease identifier: `stable`, `latest`. `stable` is accepted as a **value**
  meaning the non-prerelease line; `latest` is `E180`.
* Uppercase is `E181`: SemVer prerelease identifiers are case-sensitive and mixed case makes precedence comparisons
  hostile.

A **channel value** is what a directive carries. Both `Channel` and `Propagate-Channel` take the same grammar:

```
channel-value = [ from ">" ] to
from          = channel-name / "stable" / "*"
to            = channel-name / "stable"
```

with `Propagate-Channel` additionally accepting the two non-channel words `inherit` and `none` in place of a whole
value. `inherit` and `none` are values, not channel names: they MUST NOT appear on either side of a transition (`E111`),
and a package may not be named after them any more than after `stable`.

* `*` is legal only as a `from`, where it matches **any prerelease channel** and never matches `stable`. `*` as a `to`
  is `E111`: "move them to some prerelease or other" is not a releasable instruction.
* `>` is the transition separator and cannot occur in a channel name, so the split is unambiguous. More than one `>` in
  a value is `E111`.
* Both sides are validated as channel values in full: `%%beta>Latest` is `E181` on the right-hand side, and
  `%%Beta>stable` is `E181` on the left.

`channels.allowed` (§14), when set, restricts both sides of every value, so a repository can enumerate its trains.

### 11.3 Prerelease version format

```
<major>.<minor>.<patch>-<channel>.<counter>
```

The counter is a **numeric** SemVer identifier starting at `0`. Numeric identifiers are compared numerically, so
`1.3.0-beta.10 > 1.3.0-beta.9`. Formats without a separate numeric identifier (`1.3.0-beta10`) MUST NOT be produced,
because they compare as ASCII strings and misorder at 10.

### 11.4 Computing the next prerelease

Let `S` be the stable baseline (or the virtual `0.0.0` if none), `B` the baseline, `ch` the target channel, and `E` the
effective bump accumulated over the pending window (§13.3).

```
target = applyBump(S, E)                     # the core version this train is heading to

if B is a prerelease AND channelOf(B) == ch AND coreOf(B) == target:
    next = target - ch . (counterOf(B) + 1)
else:
    next = target - ch . 0
```

This yields the standard, correct behaviours:

| Baseline       | Pending bump            | Channel  | Next                                          |
|----------------|-------------------------|----------|-----------------------------------------------|
| `1.2.3`        | minor                   | `beta`   | `1.3.0-beta.0`                                |
| `1.3.0-beta.0` | minor (same window)     | `beta`   | `1.3.0-beta.1`                                |
| `1.3.0-beta.1` | minor + a new `fix`     | `beta`   | `1.3.0-beta.2`; target unchanged              |
| `1.3.0-beta.2` | a breaking change lands | `beta`   | `2.0.0-beta.0`; target moved, counter resets  |
| `1.3.0-beta.2` | nothing new; `E` is still the window's `minor` | `rc`     | `1.3.0-rc.0`; channel switch, counter resets  |
| `1.3.0-rc.1`   | nothing new; `E` is still the window's `minor` | `stable` | `1.3.0`; graduation                           |

Because `target` is recomputed from the stable baseline on every run, a breaking change arriving mid-train correctly
moves the whole train, and the counter resets rather than continuing under a version that no longer describes the
content.

For a member of a shared-version group, `target` is additionally raised to the core of the group's line where it falls
below it, because the member's own window need not carry the work that put the group there (§13.9a).

**The channel-entry patch.** A package can be released for a channel change alone; that is what the channel axis does
to a dependent with no bump of its own (§9.3). Entering a train from a clean stable baseline then computes a version
that is *lower* than the baseline: from `1.2.0` with `E = none`, `target` is `1.2.0` and `next` is `1.2.0-beta.0`, which
SemVer ranks below `1.2.0`. The engine MUST therefore apply one further step:

> If `effective(P) == none`, and `P` is being released only because its channel changed, and `next` is not greater than
> `baseline(P)`, recompute `next` with `E = patch`. Report `W204`. If `next` is *still* not greater than `baseline(P)`,
> raise `E195` as usual.

From `1.2.0` this yields `1.2.1-beta.0`, the lowest version that both sorts above the baseline and sits on the new line.
The step is deliberately narrow (one patch, only for a channel-only release, and only when the computed version would
otherwise regress), so that it can never mask the genuine regression `E195` exists to catch (vectors 46 and 47), and so
that it never applies to a package that has a bump of its own to be honest about.

### 11.5 Graduation

Graduation is a channel value resolving to `stable` for a package whose baseline is a prerelease. It is the deliberate,
reviewable act of ending a train, and it happens in exactly two ways.

**Directly**, by a `Channel` directive on the package itself:

```
release(@acme/core,@acme/cli)%stable: promote the 2.0 train
```

```
release(@acme/*,-@acme/legacy-adapter)%beta>stable: graduate everything still on beta
```

**By propagation**, and then only through a transition (§9.3):

```
release(@acme/core)%stable%%beta>stable++*: graduate the 2.0 train

Propagate-Channel-Scope: @acme/*, -@acme/legacy-adapter
```

Rules, common to both:

* The published version is `applyBump(S, E)`, the same `target` as §11.4, with no prerelease suffix. For a member of a
  shared-version group it is raised to the core of the group's line where it falls below it (§13.9a), which is what
  lets a half-finished graduation be retried.
* Graduation never lowers a version: if `target` is lower than the core of the baseline, `E185` is raised. A `target`
  equal to that core is the ordinary graduation, because `1.3.0` ranks above `1.3.0-rc.1`. `E185` is reachable from
  hand-edited tags, and from a train that an exact `Release-As` raised above what its window computes, whose graduation
  must then be pinned too.
* A train entered by the channel-entry patch graduates by it. Its window carries no bump, so `applyBump(S, E)` returns
  the stable baseline itself, below the core the train was published under: from `2.0.0`, entered as `2.0.1-beta.0`,
  it returns `2.0.0`. The step of §11.4 therefore applies to a graduation exactly as it is written there. With
  `effective(P) == none`, a release made only for its channel, and a `target` not greater than `baseline(P)`, the
  engine recomputes `target` with `E = patch` and reports `W204`: from `2.0.0` that is `2.0.1`, the core the train
  carried. If the result is still lower than the core of the baseline, `E185` stands. Without this step the two
  canonical forms compose into a run that cannot complete: `feat(core)%beta++1` enters the consumers by the patch
  (vector 39b), and `release(core)%stable%%beta>stable++*` then raises `E185` against every one of them.
* Graduating a package already on `stable` is a no-op with `W185`, unless the window contains bumps, in which case it is
  an ordinary stable release. Written as a transition it is not even that: a stable package does not match a `<from>` of
  any prerelease channel, so nothing is proposed and no `W185` arises.
* A `feat(cli)%stable:` unit both adds a feature and graduates; this is legal and equivalent to the two-unit form.
* A bare or inherited `stable` arriving by propagation is suppressed with `W200` (§9.3). Only a direct directive, or a
  propagated **transition**, graduates.

**Graduating a partly-graduated set.** This is the situation the transition form is for. A release train rarely ends in
one run: a package is graduated by hand, or an earlier run failed partway, or one consumer was ready before the others.
Restating `%stable` over the whole set then re-releases everything already on stable that has pending work, and emits
`W185` for the rest; maintaining an ever-shrinking scope-set by hand is worse. `%beta>stable` states the intent
directly (*whatever is still on beta, finish it*) and is idempotent, so the same directive is correct on the first run
and on the fifth. `%*>stable` says the same across several trains at once.

**Excluding packages from graduation.** Three levels, from narrowest to broadest:

| Written                                                | Excludes                                           |
|--------------------------------------------------------|----------------------------------------------------|
| `release(@acme/*,-@acme/legacy)%beta>stable`           | from the unit's own packages (§5.2)                |
| `Propagate-Channel-Scope: @acme/*, -@acme/legacy`      | from the dependents the graduation reaches (§8.5a) |
| `channels.allowed`, `requireCodeownerFor` (§14, §14.1) | from what may be written at all, repository-wide   |

An excluded package simply stays on its prerelease line. It is not an error, it produces no release, and it will
graduate whenever a later directive names it, which is the behaviour that makes staged graduation of a large workspace
possible without any per-run bookkeeping.

**What graduation does not do.** It does not touch the provider side: graduating `cli` while `core` remains on `beta`
publishes a stable `cli` whose declared range admits a prerelease `core`, which is reported as `W203` (§9.4) and is
almost always a mistake. Graduate the whole train, or none of it.

### 11.6 Channel conflicts

If a package's pending window contains units setting two different channels, the unit in the **newest commit** wins;
within one commit, the **last unit** wins. `W186` is emitted with both values. Determinism is chosen over rejecting the
commit, because a channel conflict is usually the result of a merge and blocking the release is worse than picking the
later intent.

A transition that does not match a package is not a competing directive for it and takes no part in this rule: it
proposes nothing, so an older `%beta` and a newer `%rc>stable` on a package sitting on `beta` leave it on `beta`, with
no `W186`. Only directives that actually propose a channel compete.

The same rule, applied to propagated channels, is `W160` (§9.3); a direct directive beats every propagated one
regardless of age (§13.8).

### 11.7 Channels and propagation

The two axes are independent and both default to depth `0` (§8.3, §8.3a). For prereleases this means:

* `feat(core)%beta`: `core` enters the beta line. Nothing else moves and nothing else releases.
* `feat(core)^%beta`: the same. The caret reaches the direct consumers, but every one of them is suppressed by §9.3a,
  because a stable consumer cannot resolve a beta release. `W208` reports each suppression.
* `feat(core)^%beta++1`: `core` and its direct consumers all enter the beta line together, the consumers taking the
  propagated `patch`. This is the form that keeps a train installable as a set, and it is what has to be written.
* `feat(core)^: x` where `core` and its consumers are **already** on `beta`: everything stays on `beta` and takes its
  bump, with no channel directive anywhere, because a channel is derived from each package's own baseline (§11.1). An
  established train needs no directives to stay together.
* `release(core)%stable%%beta>stable++*`: the train ends, for `core` and for every transitive dependent still on
  `beta`.

The pattern to read out of that list is that directives are needed at the **boundaries** of a train, entering it and
leaving it, and nowhere in between. See §9.3 for the full rules and §24 D.4 for a worked train.

---

## 12. Version tags and state

### 12.1 Tag format

```
<package>@<version>
```

* `<version>` MUST be a valid SemVer 2.0.0 version, with no `v` prefix.
* `<package>` MUST equal a workspace package name byte-for-byte.
* **Parse at the last `@`.** Package names may contain `@` (`@acme/ui@1.2.3`); versions never do. Splitting at the first
  `@` is a conformance failure.
* Tags whose left part is not a known package are ignored silently (they are someone else's tags).
* Tags whose right part is not valid SemVer are ignored with `W190`.
* Build metadata (`1.2.3+sha.abc`) is permitted in tags, ignored for precedence per SemVer, and MUST NOT be carried into
  computed versions.

Annotated and lightweight tags are both accepted. Implementations SHOULD create annotated tags.

A tag is created for **every released package**, whatever its publish target: a package on a private registry, or one
producing no artefact at all, is tagged exactly like a public one (§13.10a). Tags record what the repository released;
they are not a record of what reached a public registry.

### 12.2 Reachability

Only tags **reachable from `HEAD`** are considered. A tag on an unmerged branch does not affect the current branch's
computation. This makes per-branch release lines (a maintenance `1.x` branch alongside `main`) work without
configuration.

### 12.3 Baselines

For package `P`, over reachable tags `P@*`:

* `baseline(P)` = the highest by SemVer precedence.
* `stableBaseline(P)` = the highest with no prerelease component.
* `stableCommit(P)` = the commit that `stableBaseline(P)` points at (after peeling annotated tags).

If two reachable tags carry the same version for the same package but point at different commits, the engine MUST raise
`E191`. No tie-break is defined: any rule based on commit date, tag creation order, or graph depth would let a
re-tagging accident silently change the pending window. Duplicate versions are a repository-integrity problem and are
for a human to resolve.

### 12.4 Tags are authoritative

The version recorded in a manifest is **not** authoritative and MUST NOT be read as state. Manifest versions drift
(release commits are sometimes not merged back, forks edit them, importers rewrite them). Tags are append-only and
reachable-from-HEAD, which makes them the only state that survives rebasing, forking, and shallow clones with `--tags`.

Implementations SHOULD warn (`W192`) when a manifest version disagrees with the baseline, and MUST write the computed
version into the manifest as part of publishing.

A verified rollback withdraws an external artifact, not its tag. Its version remains in baseline/high-water
computation permanently. Rollback receipts are separate immutable records, ignored by tag selection. Removing or
reusing the tag would re-admit already discharged metadata and is forbidden (§26).

### 12.5 Unreleased packages

A package with no reachable tag has no baseline. Its first computed version is `initialVersion` (default `0.1.0`),
**regardless of the pending bump**: a breaking change in a never-published package does not produce `1.0.0`.

To publish `1.0.0`, use `Release-As: 1.0.0` or set `initialVersion: 1.0.0`.

### 12.6 Major zero

While a version is `0.y.z`, the SemVer specification gives no compatibility guarantees. With `preserveMajorZero: true`
(the default), bumps are remapped before application:

| Requested | Applied while `0.y.z`       |
|-----------|-----------------------------|
| `major`   | `minor` (`0.4.1` → `0.5.0`) |
| `minor`   | `patch` (`0.4.1` → `0.4.2`) |
| `patch`   | `patch`                     |

With `preserveMajorZero: true`, no accumulation of bumps will ever take a package out of `0.y.z`; the only exit is an
explicit `Release-As: 1.0.0`. With `preserveMajorZero: false`, a `major` bump on `0.4.1` produces `1.0.0` in the
ordinary way.

---

## 13. Release computation algorithm

The complete ordinary-release procedure. It is a pure function of (repository history, immutable release records, workspace graph, configuration) and
MUST be deterministic.

Under §28, the complete semantic plan remains `plan = Plan(input)`: worker placement and scheduling MUST NOT alter it.
A distributed release MUST acquire every participating repository's lock before fixing `input` and computing `plan`;
read-only planning remains lock-free (§28.3). Preparation and output-transfer prerequisites do not add release
intent or discharge pending work.

The procedure below computes the **ordinary forward-release projection**. Before applying it, a CCME 3 engine parses
and validates rollback units under §26, collects their separate operational requests, and excludes them from ordinary
cancellation, correction, channel, propagation and bump tuples. A rollback unit never enters `W` or `Wfresh` as work
that a subsequent release tag can discharge. The complete plan includes both projections and must satisfy the combined
preflight restrictions of §26 before any artifact mutation. Publication cannot start while requested rollback is
incomplete. Withdrawal receipts are an additional availability input, never a replacement baseline.

### 13.1 Load the workspace

Build the package list (name, root path, publish target) and the dependency graph from manifests **at `HEAD`**. Publish
targets are resolved per §13.10a.

An implementation using the optional polyrepository profile MUST first construct the fixed repository snapshot and
workspace ownership map of §27. The resulting package graph is one graph for every later phase. Repository boundaries
do not break dependency propagation, version groups (§13.9a), publish ordering, failure blocking, or package selection.

**The dependency graph MUST be acyclic** over the edge kinds of `propagation.kinds` and `publish.orderKinds` (§14). A
cycle is `E200`, repository-scoped: the run aborts before any plan is computed, and the diagnostic MUST name every
package in the cycle and the manifest field carrying each edge, because a cycle is otherwise tedious to locate by hand.

A cycle is rejected rather than accommodated because it has no correct release. Registries have no transactions and
§19.1 forbids moving or deleting a tag, so the members of a cycle cannot be published atomically: whichever goes first
declares a range on a version that does not yet exist, and if the run then fails, that unresolvable state is permanent.
No publish order avoids this, because a cycle admits no order. Accepting cycles would mean specifying a release that is
transiently broken by construction and occasionally broken for ever, so the graph constraint is stated once, here, and
everything downstream may assume a DAG.

`devDependencies` are not in either edge-kind list by default, so a cycle existing only through them is not a cycle for
this purpose and does not trigger `E200`, which is the common and legitimate case, since test fixtures routinely depend
back on the packages they exercise. Packages deleted before `HEAD` are not in the graph even if history mentions them;
units scoping them resolve to `E130`/
`W130` per §6.1.

### 13.2 Load tags

Enumerate tags reachable from `HEAD`, parse per §12.1, and compute `baseline`, `stableBaseline`, `stableCommit` per
§12.3.

**Records under the lock.** A run that can write plans from the authoritative store's release records, not from
whatever its checkout happened to fetch. After it holds every release lock it needs and before it fixes the input of
this section, the engine MUST compare, for each participating repository, the release records of the store that run
records to with the records it is about to plan from. A stored release record whose commit is reachable from the
planned head and which the planning input lacks is incomplete history and is `E196`. A stored release record that names
another commit than the planning input's record of the same package and version is `E191`. The engine MUST NOT repair
either difference by planning the package as unreleased, and MUST NOT refresh its records silently after the
comparison: a run that fetches does so before the comparison and plans from what it then holds. Complete history makes
reachability decidable locally, because a commit the checkout does not hold cannot be reachable from its head. The lock
is what makes one comparison sufficient: no other coordinated run can add a record between the comparison and this
run's own records. Without the comparison the lock serializes runs and isolates nothing, because two runs that never
overlap still plan the same version when the second one's checkout predates the first one's records. A run that records
nowhere but its own repository has that repository as its store and nothing to compare, and neither has a repository
that owns no package. Read-only planning takes no lock and makes no comparison; its result describes the checkout. The
comparison reads one record inventory per repository, `O(T)` each, and asks one ancestry question per record the input
lacks, which is none in the ordinary case.

Only what §12.1 parses as a release tag of a workspace package is a release record here; a ref the implementation
moves by design, its lock, and a name that merely resembles a tag format are not. A store whose records cannot be read
is not a store whose records agree: the run is refused as `E196`, because a plan whose completeness nothing could
check is a plan over incomplete history. An implementation MAY offer a setting that forgoes the engine's reads of a
store before it writes there, for a store that refuses them. A run under that setting makes no comparison and has
exactly the exposure this rule removes; the engine MUST NOT describe its records as compared and SHOULD report the
omission. A run under an unsafe lock bypass (`W331`) still compares. No lock then holds other runs off after the
comparison, so it narrows the exposure and does not close it.

Under §27, enumerate each package's records in its owning repository and reconstruct cross-repository consumer
positions from gitlink snapshots or explicit `repositoryBaselines`. Never compare or sort commit IDs from different
repositories as if they belonged to one ancestry relation.

### 13.3 Pending window

```
pendingWindow(P) = { c : c reachable from HEAD } - { c : c reachable from stableCommit(P) }
freshWindow(P)   = pendingWindow(P) - { c : c reachable from tagCommit(baseline(P)) }
```

If `P` has no stable baseline, `pendingWindow(P)` is every commit reachable from `HEAD`. If `P` has no baseline tag
at all, the subtracted reach in `freshWindow(P)` is empty, so `Wfresh(P) = W(P)`.

The window is measured from the last **stable** tag, not the last tag of any kind. This single definition serves both
cases:

* For a package on `stable`, last stable = last release, so the window is "changes since the last release".
* For a package on a prerelease, the window spans every commit since the last stable release, which is exactly what
  §11.4 needs to compute `target` across an entire prerelease train.

Traversal MUST visit each commit exactly once (a commit reachable by two paths contributes its units once).

`pendingWindow(P)`, abbreviated `W(P)`, is the **train window**. It deliberately retains commits already delivered on
the current prerelease train so that §11.4 can aggregate the train's target from its stable base. `freshWindow(P)`,
abbreviated `Wfresh(P)`, is the **undelivered window**: work after `P`'s highest-precedence baseline tag. For a stable
baseline the two windows coincide. These roles MUST NOT be conflated:

| Purpose                                   | Window consulted                 | Section     |
|-------------------------------------------|----------------------------------|-------------|
| Compute `P`'s aggregate train bump/target | `W(P)`, the unit's own package   | §11.4, §13.6 |
| Admit a fresh direct bump or channel      | `Wfresh(P)`                      | §13.6, §13.8 |
| Admit a bump or channel for dependent `D` | `Wfresh(D)`, the **dependent's** | §13.7       |

Reading the dependent-admission row against the source's window silently loses releases; §13.7a is about exactly that.

### 13.4 Parse and resolve

For every commit in the union of all pending windows: parse into units (§20), resolve scopes (§6), yielding a set of
`(package, commit, unitIndex, unit)` tuples.

Retention is **purpose-dependent**. A single retention rule serving both purposes cannot be correct, for the reason
given in §13.7a. A tuple `(P, C, i, u)` is retained:

* for fresh **direct admission** in §13.6/§13.8, only if `C ∈ Wfresh(P)`; the full `W(P)` remains available to §11.4 for aggregate train targets;
* for the **propagation-source** computation of §13.7, regardless of `W(P)`, per §13.4a.

### 13.4a Source packages

For a unit `u` in commit `C`, `sourcePackages(u)` is `u`'s resolved scope-set (§6) minus every package whose
contribution has been **suppressed**. There are two suppressors (cancellation (§10) and holds (§8.6.1)) and both are
**window-scoped** in exactly the same way:

```
discharged(P, C) =  C not in Wfresh(P)     # P has already released the work in C

sourcePackages(u) =
    { P in resolve(u) :  discharged(P, C)
                         or not ( cancelledFor(C, P) or held(P) ) }
```

`cancelledFor(C, X)` is true when `C` is an ancestor-or-self of some `cancel` commit whose resolved scope contains `X`
(§10.3). `held(P)` is true when `P`'s effective `Release-As` directive is `none` (§13.6a).

**Delivery.** On the bump axis a source's contribution is admitted for a target until the source has **delivered** it:

```
delivered(P, C, D) =  some release tag of P sits on a commit t
                      with C in reach(t) and t in reach(baselineCommit(D))
                      # D released at or after P's release carrying C; false for an unreleased D

owed(u, D)         =  { P in sourcePackages(u) : not delivered(P, commitOf(u), D) }
```

A unit propagates a bump to `D` while `owed(u, D)` is non-empty (§9.2), and `D`'s provenance names exactly the owed
sources. `delivered` implies `C ∉ Wfresh(D)`, so a target that has not released past `C` is owed by every source and
the test reduces to the window; the finer question arises only for a target that released past `C` before its source
did: a consumer that proceeded on a cause of its own while the source failed (§19.3), or one released while the
source was held (§13.6a). Such a target is still owed the source's release, and receives it as a catch-up when it
comes (§13.7a). Releasing past a commit is not delivery; only the source's release, followed by the target's, is. The
channel axis keeps `C ∈ Wfresh(D)` as its admission, because a channel is carried by the units and needs no release of
the source (G7).

**Suppression applies only to undischarged work, and this is normative.** Once `P` has published the version that
carries `u`, the artefact its consumers are owed is public. Nothing landing afterwards can retract that obligation:

* A `cancel(P)` after `P` released the unit is a no-op for `P` (`W170`, §15.4 #47), since there is nothing pending left to
  discard, so it MUST NOT retroactively strand `P`'s consumers.
* A `Release-As: none` on `P` after `P` released the unit stops `P`'s *future* releases. It MUST NOT strand `P`'s
  consumers either. The rationale given in §8.6.1 for excluding a held package as a propagation source is that
  publishing a dependent against an *unpublished* dependency version would produce a broken artefact, and that
  reasoning simply does not apply to a version that is already published.

Treating the two suppressors differently would be indefensible: §7.3 presents `revert`, `Release-As: none`, and
`cancel` as a deliberate ladder from weakest to strongest, and it would be perverse for the *weaker* of the two to
destroy an obligation the stronger one leaves intact.

Suppressing a catch-up that is genuinely unwanted is done where the pending contribution actually lives, in the
consumer's ledger, with `cancel(<consumer>)` or a hold on the consumer. §13.7d is the operator-facing summary.

Every operand is computed from tags and ancestry at `HEAD`, so the rule is deterministic for a fixed tag state
(§17.2). A later source release can change `sourcePackages(u)` by discharging a previously suppressed source; that
state transition is intentional and is not replay independence.

### 13.4b Apply corrections

Resolve and apply the `Edits` and `Deletes` footers of §7.4 to the tuple stream of §13.4, before cancellation runs.
Every later phase (§13.5 through §13.10, and §9.2 inside §13.7) operates on the corrected stream and needs no further
knowledge of corrections.

```
applyCorrections(tuples, units):
    # ---- resolve each correction unit ----
    corr = []
    for u in units where u carries Edits or Deletes footers:
        if u.type in {cancel, release, rollback}:   already E171 / E173 / E301 at parse; skip
        targets = []
        for f in u.correctionFooters:                  # in written order
            if f.value == '*':  targets.append(WILDCARD(f.kind)); continue
            (C, n) = resolveSha(f.value)               # E210 if unknown, unreachable,
            if C is not a proper ancestor of commitOf(u): raise E210    # or not older
            t = unitOf(C, n)                           # E211 if n out of range, or if
                                                       #   bare sha and C is multi-unit
            if t is a control unit:       raise E212
            targets.append((t, f.kind))                # kind: edit or delete

    # ---- reconcile scopes (§7.4.2): containment, not equality ----
        shaSets = { resolve(t) : (t, _) in targets, t != WILDCARD }
        if u has no scope-set:
            if shaSets is empty:  S = resolve('*')     # wildcard-only unit
            else:                 S = union(shaSets)   # inherited from the targets
        else:
            S = resolve(u.scopeSet)
            for T in shaSets:
                if S is not a subset of T:  raise E213 # widening someone else's record
        u.effectiveScope = S                           # replaces §6 resolution for u
        u.targets = targets;  corr.append(u)           # a unit that raised above is never appended

    # ---- apply, newest commit first, last unit first (§8.6 order) ----
    # dropped[(P, C, i)] records every discarded (package, record) pair; a
    # correction whose own record is in it for P is void for P (§7.4.2, W215).
    for u in corr in §11.6 precedence order:
        live = { P in u.effectiveScope : (P, commitOf(u), u.index) not in dropped }
        for P in u.effectiveScope - live:              warn W215              # void for P
        if live is empty:                              continue               # fully void
        for (t, kind) in u.targets:
            if t == WILDCARD:
                for P in live:
                    drop every tuple (P, C, i, x) where C is a proper
                    ancestor of commitOf(u) and not superseded(P, C, i)
                if nothing was dropped for any P:      warn W209
                continue
            for P in live ∩ resolve(t):
                if commitOf(t) not in Wfresh(P):       warn W209; continue   # discharged
                if (t) already corrected by a newer u'
                   for P:                              warn W210; continue
                drop the tuple (P, commitOf(t), t.index, t)                  # -> dropped
            if kind == edit:
                mark u as the restatement of t for those P                   # §13.10
                if u.type == t.type and u.breaking == t.breaking
                   and u.description == t.description:  warn W211
    return tuples minus dropped
```

Notes, all normative:

* The correction unit itself remains an ordinary tuple source: its own record enters the stream under
  `u.effectiveScope` exactly as a scope-set of that value would have entered it.
* `superseded(P, C, i)` is the newest-wins rule: a record already claimed by a newer correction is not re-dropped,
  and the older correction reports `W210`.
* Voiding needs no restoration pass. A correction can only be voided by a newer correction, and newer corrections
  apply first, so a void correction is skipped before it drops anything (`W215`). "Restoring" a nested target is
  therefore nothing more than never having dropped it, which is what makes the single pass sufficient (§7.4.2).
* Discharge is tested against the target's window per package, which is what confines corrections to unreleased work
  (§13.4a) and what makes them idempotent: after the package releases, the same correction is a `W209` no-op.
* Corrections precede §13.5, so a cancel barrier covering a corrected record discards the corrected form, and a
  barrier covering the correction's own commit discards the restatement record like any other pending unit.
* Every input is the message stream, ancestry, and tags at `HEAD`, so the step is a pure function of the repository:
  determinism (§17.2) and the guarantees G1 through G8 (§13.7c) are unaffected, because downstream phases cannot
  distinguish a corrected stream from one that was authored that way.

### 13.5 Apply cancellation

Compute the ancestor closure of each `cancel` commit. Discard tuples per §10.3. `cancel` units themselves are then
dropped.

### 13.5a Cancellation of propagated contributions

§10.3 is written in terms of units in a package's pending window, which covers direct bumps. A propagated contribution
has no unit of its own, so its treatment is stated explicitly:

> A propagated contribution to package `D`, arising from a unit in commit `C`, is discarded if `C` is an
> ancestor-or-self of a `cancel` commit whose resolved scope-set contains **`D`**.

The scope that governs is the **target's**, because that is whose ledger the pending bump sits in. This is the same
principle as §13.4a seen from the other end, and together they give a complete and symmetric account:

| Directive                                  | Effect on the origin `P`                               | Effect on a stale consumer `D`            |
|--------------------------------------------|--------------------------------------------------------|-------------------------------------------|
| `cancel(P)` while `P` still has it pending | Discards `P`'s direct bump and removes `P` as a source | Never bumped; the source is gone          |
| `cancel(P)` after `P` released it          | No-op, `W170`                                          | Unaffected; still catches up (§13.7a)     |
| `Release-As: none` on `P`, work pending    | `P` held; removed as a source for that work            | Not bumped until the hold lifts           |
| `Release-As: none` on `P`, work released   | `P` held for future work only                          | Unaffected; still catches up (§13.4a)     |
| `cancel(D)`                                | None                                                   | Pending propagated contribution discarded |
| `Release-As: none` on `D`                  | None                                                   | Recorded, not released, until lifted      |

Consequently `cancel` retains its 1.0.0 guarantee unchanged: it affects only what has not been released, and never
reaches a published tag (§10.3).

### 13.6 Direct bumps

```
direct(P) = max over surviving tuples for P whose commit is in Wfresh(P) of bumpOf(unit)
```

`bumpOf(unit)` comes from the type mapping (§7.1) and `!` alone. No footer overrides it; `Release-As` acts on the
release, not on the bump (§8.6).

Consequently historical train records alone cannot create a new plan entry. They are consulted only after fresh direct
or propagated work, a fresh channel move, or an exact `Release-As` has admitted `P`; §11.4 then uses the full `W(P)` to
compute that admitted prerelease's aggregate target.

### 13.6a Holds

For each package, resolve the package-level `Release-As` directives by the precedence rule of §8.6. A package whose
effective directive is `none` is **held**: it is recorded in `held`, excluded from the release plan in §13.10, and
excluded as a propagation source in §13.7. Its tuples are retained: they will be counted by whichever future run
releases it. A newer `auto` or exact version clears a hold; so does a `cancel`, by discarding the unit that carried it.

The engine MUST compute the would-be version for every held package anyway, and report it (`W154`), so that the value
needed to lift the hold is available without hand computation.

Holds are resolved **before** propagation, so that a held package cannot bump its dependents with work it has not
released. A hold suppresses a package as a propagation source only for units still pending for it; units it has already
published continue to propagate, per the `discharged` rule of §13.4a.

### 13.7 Propagation

Run §9.2 over the surviving tuples, using `sourcePackages(u)` (§13.4a), skipping any unit whose source set is entirely
held, and removing held packages from every unit's source set.

§9.2 is a three-phase procedure and its phases interleave with this section: phase 1 propagates channels, phase 2 is the
channel resolution of §13.8, and phase 3 propagates bumps using the channels phase 2 produced. §13.8 is therefore
*invoked from inside* §13.7 rather than performed after it; it is written as its own section because it also settles the
channels of packages that receive no propagation at all.

The output is `propagated(P)` and `channel(P)`. Then `effective(P) = max(direct(P), propagated(P))`.

Both suppressors apply to both axes. A held package is removed from every unit's source set for the work it has not
released, so it neither bumps nor re-channels its dependents on the strength of it (§13.4a). A held package MAY still
*receive* a propagated bump or channel from an unheld dependency; both are recorded but neither is released, because
§13.10 excludes it. Nothing is lost: both are recomputed from the same tuples on the run that lifts the hold.

### 13.7a Catch-up

**The failure this prevents.** Publishing is not atomic. A run publishes each package to a registry independently and
tags it on success (§19.1), so a run can end with some packages published and others not. The obvious expectation is
that re-running finishes the job, because packages already tagged fall out of the plan and the rest remain in it. For a
package released by its *own* commits that is exactly what happens. For a package released by **propagation** it does
not, not unless admission is defined as §13.4a defines it.

Suppose retention were governed by a single rule, the natural one: keep a unit's tuple only while the unit's *own*
package still has it pending. Then:

```
        C : feat(core)                 # core minor; cli, ui, api propagated patch
        run 1 : core@1.5.0 tagged, ui and api tagged, cli's publish fails
        run 2 : W(core) no longer contains C
                -> the tuple (core, C, u) is not retained
                -> u has no source packages
                -> u propagates to nothing
                -> cli is never released, on this or any future run
```

`cli` is **orphaned**: it is permanently one patch behind a dependency it declares, its manifest range was never
reconciled, and no diagnostic fires because from the engine's point of view there is simply nothing pending. The failure
is silent, and it is *more* likely the larger the workspace, because the chance that some leg of a wide fan-out fails
grows with the fan-out. Worse, the same shape arises without any failure at all: a package held by
`Release-As: none` past its provider's release reaches exactly the same state when the hold lifts.

The same orphan is reached from the other side by a consumer that gets **ahead** of its provider. Let one commit carry
`feat(core)^` and `feat(cli)`; `core`'s publish fails and `cli`, which has a cause of its own, proceeds (§19.3) and is
tagged past the commit. If admission were "the target has not released past the commit", `cli` would be owed nothing
when `core` publishes on the next run, would keep the manifest range it reconciled against `core`'s old version, and
would never be planned again: one patch behind a dependency it declares, silently. A consumer released on its own
change while its provider is held reaches the same state when the hold lifts. Hence admission is delivery (§13.4a),
not release position.

The root cause is that 1.0.0 tested a *dependent's* eligibility against the *source's* release position. Those are
different packages with different tags, and after a partial failure they are exactly the packages whose positions have
diverged.

**The rule.** Per §13.4a and §9.2, a unit propagates a bump to a dependent `D` whenever some source of the unit has not
yet delivered the unit's commit to `D`: `D` has not released at or after a release of that source carrying it. For a `D`
that has not released past the commit at all, that is the window test; for a `D` that got ahead of its source, it is the
source's release that decides. No separate catch-up pass exists, and none is needed: catch-up is not a repair mode
bolted onto the algorithm, it is what the algorithm does when the ordinary rule is evaluated against the right position.
A "catch-up release" is therefore only a *label*: a release whose entire cause is a propagation from a package that is
not itself in this run's plan. Implementations MUST report it as such (`W193`), because a package appearing in a plan
with no commits of its own and no releasing dependency is otherwise baffling to whoever reviews the plan.

**What catch-up does not do.** It does not re-run, re-time, or re-scope anything:

* it never widens depth: targets come from the same depth-bounded traversal from the same source set (§9.2);
* it never resurrects a cancelled contribution (§13.5a);
* it never releases a held package (§13.6a);
* it never changes the version the target was originally planned at (G3 below);
* it never propagates onward from the catching-up package (§9.2, last property).

### 13.7b Detecting staleness from the consumer

§9.2 walks *down* from sources. The equivalent walk *up* from a candidate consumer is what an audit command wants, and
implementations SHOULD offer it, because "which of my packages are behind their dependencies, and behind which?" is the
question a human asks after a failed run:

```
staleSources(D):
    edges = graph.restrictedTo(config.propagation.kinds)  # §8.4: the same edges as §9.2
    channel = resolveChannelsForAudit()                    # same phase-2 result as §9.2
    dist  = shortestPathsUp(D, edges)   # P -> edge count of the shortest path P → … → D
    out   = {}
    for u in units:                                       # every retained tuple, §13.4
        if bumpOf(u) == none:                             continue
        b = (u.propagate == 'inherit') ? bumpOf(u) : u.propagate    # as §9.2
        if b == none or u.depth == 0:                     continue
        sources = sourcePackages(u)                       # §13.4a
        if sources is empty:                              continue
        if D in sources:                                  continue   # §9.2 seeds seen = sources
        sources = owed(u, D)                              # §13.4a: what D is still owed
        if sources is empty:                              continue   # every source delivered
        reaching = { P in sources : P in dist }
        if reaching is empty:                             continue
        if not resolvableBy(sources, D, channel):     continue   # §9.3a
        level = min({ dist[P] for P in reaching })        # measured from the whole source set
        if level > u.depth:                               continue
        if D not in resolve(u.propagateScope):            continue
        if cancelledFor(commitOf(u), D):                  continue
        for P in reaching:
            out |= { (P, u, level, b) }
    return out
```

Four details carry the duality, and all four are places an implementation drifts:

* **`D` is excluded from its own unit's sources.** §9.2 seeds `seen = set(sources)`, so a unit never propagates to a
  package it already bumps directly; the `D in sources` test is that seeding, read from the other end. Without it a
  package that both changes and consumes a sibling in one unit audits as behind itself.
* **`level` is measured from the whole source set, not from one ancestor.** §9.2 walks outward from `sources` together,
  so a dependent reachable at depth 1 from one source and depth 3 from another is at depth 1 (§9.2, "depth is
  shortest-path", "depth is measured from the originating source set, always"). Taking `dist[P]` per ancestor instead of
  the minimum over `reaching` under-reports exactly the diamond cases.
* **Resolvability is the same admission predicate as §9.3a.** Reachability alone is insufficient: a prerelease source
  on a different line is not installable by `D`, so the downward and upward formulations MUST both reject it.
* **The units are not those of `D`'s window.** A commit `D` has released past can still be owed to it by a source that
  released it later (§13.4a); iterating `freshWindow(D)` alone misses exactly the consumers that got ahead of a
  provider, which are the ones this audit exists to find after a partial failure.
* **`b` is the effective propagated bump**, computed the same way as in §9.2: `inherit` resolves to the unit's own
  bump, and a unit whose propagation resolves to `none`, or whose depth is `0`, is not a source at all (§8.3). Such a
  unit warns (`W152` where it asked for nothing, `W201` where it named a value the depth then discarded (§8.3b)) but
  the diagnostic does not change the computation here: either way it contributes no tuples.

`shortestPathsUp(D, edges)` is a BFS up the dependency edges; it excludes `D` itself and terminates because the graph is
acyclic (§13.1). It needs no depth bound (the `level > u.depth` test is the bound) though an implementation MAY stop
the BFS at the largest `u.depth` in `W`, treating `all` as unbounded, since no unit can reach further.

`staleSources(D)` is non-empty exactly when §9.2 assigns `D` a non-`none` propagated bump, and `max({ b })` over its
rows equals that bump. The two formulations are duals over the same relation and MUST agree; disagreement is an
implementation bug, and testing one against the other over a random workspace is a cheap and effective conformance
check.

**The cheap tag-level screen.** Walking units is `O(|window|)`. A far cheaper *screening* test, adequate for a warning
banner or a CI dashboard, compares release positions directly:

> `D` is possibly behind `P` if `tagCommit(baseline(P))` is **not** an ancestor-or-self of `tagCommit(baseline(D))`.

That is the "the provider's tag is newer than the consumer's tag" intuition, expressed as ancestry so that it stays
deterministic under merges, rebases, and equal commit dates (§10.4). It is the shape of `delivered` (§13.4a) with the
unit's commit left out, which is why it screens: a consumer owed something has a provider release its own release does
not reach. It MUST NOT be used as the authoritative test: it is *necessary but not sufficient*. A provider can
legitimately be ahead of a consumer with nothing owed: the units between them may all be `^none`, or `+0`, or scoped
away by `Propagate-Scope`, or reach `D` only beyond their declared depth, or be `devDependencies`-only edges. Using the
screen to decide releases would manufacture bumps that no commit asked for. Use it to *find* candidates; use §9.2 to
decide.

Note also the case the screen cannot see at all: `P` and `D` released at the **same commit**, in the same run, but with
`D` published first. Ancestry cannot distinguish them, and no rule over tags can. That case is prevented rather than
detected, by publishing in dependency order (§19.2).

### 13.7c Guarantees

For a fixed `HEAD` and configuration, an implementation conforming to §13.4a, §9.2, and §19 satisfies the following.
The cross-run claims name two recurring hypotheses: **H1 (stable inherited-channel state)** means every baseline channel
read by `Propagate-Channel: inherit` remains unchanged during the run sequence; **H2 (no suppressed source release)**
means no package whose contribution to a unit is initially suppressed by cancellation, hold, or correction is
nevertheless released at `HEAD` during that sequence. They are components, not by themselves a complete proof premise.
Where a guarantee says **retry invariant**, it additionally requires the corrected tuple stream, source sets, graph,
scope/depth predicates, target baselines, and resolved source/target channel admission for every outstanding unit to
remain unchanged except for baseline tags written for packages that successfully released. Neither H1 nor H2 is needed
for one invocation's deterministic computation.
For CCME 3 these are claims about the forward-publish projection. The retry invariant also fixes rollback activation,
withdrawal inventories and completion state. They do not promise artifact availability after explicit withdrawal.
The separate rollback progress argument is in §26 and requires durable receipts and successful idempotent operations.
These guarantees are normative and testable; Appendix B.7 exercises G1–G6 and Appendix B.9 exercises G7 and G8.

**G1, termination.** Propagation halts. Each unit traverses a BFS that marks every package `seen` at most once, so it
performs at most `|V|` expansions regardless of depth, cycles, or `+*`; the outer loop is over a finite set of units.

**G2, completeness (no orphans).** Under the retry invariant, if unit `u` in commit `C` admits dependent `D`, then `D`
receives at least `u`'s propagated bump in every run until `D` releases at or after a release of every owed source
carrying `C`. Admission is `owed(u, D) ≠ ∅` (§13.4a); only a baseline tag for `D` at such a commit discharges it. A
release by the source alone cannot, and neither can a release by `D` that precedes the source's.

**G3, version stability.** Under the retry invariant, a package caught up in run *k* receives the same version it was
planned at in run 1. `effective(D)` is the same `max()` and the failed run wrote no tag for `D`. A source
graduation can change an `inherit` channel, and discharge of a suppressed source can add a tuple; either change requires
a newly surfaced plan rather than a claim that the old number is preserved.

**G4, no double ledger delivery.** Once `D` is tagged at a commit `T` that reaches a release of every source of `u`
carrying `C`, `owed(u, D)` is empty, so the contribution is not re-admitted. A `D` that proceeded past `C` before a
source released it is bumped once more when that release comes, and that is the delivery, not a second one. Combined
with G2 this is exactly-once **in CCME's tag ledger**. It does not promise an exactly-once registry operation: §19.4
explicitly reconciles interruption between external publication and tagging.

**G5, no blast-radius widening under the retry invariant.** Each unit's later target set is a subset of its initial set:
the source, traversal, and channel-admission predicates stay fixed while `delivered` only grows. Outside the
invariant, publishing a suppressed source (the failure of H2) or changing resolved channel admission may expose finite
catch-up targets. Such widening MUST be surfaced for review (§18.1).

**G6, restricted convergence.** Under the retry invariant, repeated running at
a fixed `HEAD` reaches an empty non-held plan in at most `n` successful-progress runs, where `n` is the number of
publishable packages in the first plan: G4 removes a published package and G5 admits no new one. Without H2, a source
release may expose a finite follow-up obligation; other failures of the invariant may also change admission. §19.6
therefore classifies the post-run replan; it does not claim
global convergence outside the retry invariant.

**Why tagging is universal.** It is tempting to version a private or artefact-less package in the plan but not tag it;
there is, after all, nothing in a registry for the tag to correspond to. Such a package can never converge: its window
never advances, so it reappears in every plan for ever, and `E199` (§19.6) would have to be weakened to exclude it,
which in turn blunts the one check that detects the failure of §13.7a, and blunts it precisely for the internal-only
packages where nobody is watching a registry. A permanent exception to convergence is not a caveat worth documenting; it
is a defect. Hence §13.10a: tag whatever is released.

**G7, channel discharge.** A propagated channel contribution from commit `C` is admitted for `d` only while
`C ∈ Wfresh(d)`. Once `d` releases at a commit containing `C`, its baseline tag removes `C` and the contribution cannot
reappear. While `C` is fresh, `propagatedChannelFor` (§9.3) separately rejects a value equal to
`channelOf(baseline(d))` (`W199`) and a transition whose `<from>` does not match. Implementations MUST apply both the
fresh-work admission gate and these no-op checks.

The no-op checks complement G7's common `Wfresh(d)` discharge gate. Both inputs are read from tags at `HEAD`, so
the result is deterministic. Across a partial failure it remains unchanged under the retry invariant; otherwise the
recomputed plan exposes the change.

**G8, one-way axis independence.** Suppressing a bump under §9.3a never suppresses a channel. The converse is false:
resolved channels are an input to bump admission under §9.3a. Two operational consequences: a `W208` in a plan
means a caret did not reach, not that a channel directive failed; and a package may legitimately appear in a plan with a
channel change and no bump (`W202`), or with a bump and no channel change, and neither shape indicates a defect.

### 13.7d Suppressing a catch-up

A catch-up is a pending release like any other, so it is stopped by the ordinary mechanisms of §8.6.1 and §10, applied
**to the consumer**. This matters most in the situation that produces catch-ups in the first place: a run failed, the
operator has looked at what is now owed, and has decided not to ship it after all. That decision is expressed as a new
commit on top, and it behaves exactly as it would for any other pending work.

| Intent                                  | Write                                       | Result                                                                   |
|-----------------------------------------|---------------------------------------------|--------------------------------------------------------------------------|
| Drop the owed release permanently       | `cancel(<consumer>)`                        | The pending propagated contribution is discarded (§13.5a). Irreversible. |
| Defer it: ship later, keep the ledger   | `release(<consumer>)` + `Release-As: none`  | Held; `W154` reports the withheld version every run until lifted.        |
| Ship it now at a stated version         | `release(<consumer>)` + `Release-As: <ver>` | Pinned, subject to the usual guards `E153`/`E156` (§8.6).                |
| Resume after a hold                     | `release(<consumer>)` + `Release-As: auto`  | Releases at the `max()` of everything accumulated, catch-up included.    |
| Drop everything pending, workspace-wide | `cancel(*)`                                 | Discards every pending contribution, direct and propagated alike.        |

What does **not** work is acting on the provider. Once the provider has published, neither `cancel(<provider>)` nor a
hold on it retracts what its consumers are owed (§13.4a): the version is public, and cancellation never reaches a
published release (§10.3). A `cancel(<provider>)` in that position reports `W170`, "nothing to discard", which is the
signal that it addressed the wrong package.

Two consequences worth stating plainly:

* **The barrier still only reaches backwards.** `cancel(<consumer>)` discards the contributions from commits that are
  ancestors-or-self of the cancel. Work landing afterwards accumulates normally, and the consumer releases again on the
  next run (§10.3, §10.4).
* **A suppressed catch-up leaves the consumer's manifest unreconciled.** It goes on declaring the range it was published
  with until its next release, whenever that comes. This is usually harmless, since a caret range admits the dependency's new
  version, but it is a real consequence of choosing not to release, and §9.4 reconciles it only at publish time.

### 13.8 Channels

Determine `channel(P)` for **every** package in the workspace, whether or not it will be released:

```
resolveChannels(chan, units):                   # chan from §9.2 phase 1
    # ---- pass 1: push from units to the packages they resolve to ----
    # Units are visited in §11.6 precedence order (newest commit first, and within a
    # commit the last unit first), so each cands[P] is built already ordered and is
    # never sorted per package.
    cands = {}                                  # package -> list of units, in §11.6 order
    for u in units in §11.6 order where u sets Channel:
        c = commitOf(u)
        for P in resolve(u.scopeSet):           # §13.4 has already resolved this scope-set
            if c not in Wfresh(P): continue       # §13.3
            cands[P].append(u)

    # ---- pass 2: read, once per package ----
    out = {}
    for P in workspace.allPackages:
        base = channelOf(baseline(P))           # §11.1; 'stable' if no baseline
        d    = directChannelFor(P, cands[P], base)   # NONE if no direct directive applies
        if d is not NONE:    out[P] = d
        else if P in chan:   out[P] = chan[P].channel
        else:                out[P] = base
    return out

directChannelFor(P, cands, base):               # cands already in §11.6 order
    if cands is empty:  return NONE             # the overwhelmingly common case
    winner = NONE
    proposalCount = 0
    for u in cands:
        v = u.channel
        if v is a transition (from, to):
            if from == to:                  warn W207; continue
            if not matchesFrom(base, from):            continue     # not a competitor
            v = to
        if v == base:                       warn W199; continue     # not a competitor
        proposalCount += 1
        if winner is NONE: winner = v
    if proposalCount > 1:                    warn W186              # retain required values
    return winner
```

The precedence body is unchanged; only the way `cands` is obtained has been inverted. Written the other way, with `cands` as
a comprehension over `W(P)` evaluated inside a loop over every package, it rescans the union window once per package,
which is `O(P · U)`, and it does that work for the great majority of packages that no `Channel` directive names at all.
Pushing from units costs one pass over the units and their already-resolved incidences, plus one flat pass over the
packages: `O(U + I + P)`. The two agree row for row, because a package
appears in `cands[P]` under the inverted form exactly when `u sets Channel and P in resolve(u) and commitOf(u) in Wfresh(P)`,
which is the comprehension's condition read in the other direction.

Two obligations come with the inversion. The push MUST visit units in §11.6 order, or `cands[P]` arrives unordered and
the first-proposal-wins loop silently picks a different directive, a determinism bug that shows up only when one
package is named by two commits. The `W186` count covers the whole candidate list, not only the prefix before the
winner. A package's result and diagnostics MUST NOT be finalized before all its candidates have been accounted for.
An implementation MAY accumulate the winner, proposal count and required diagnostic values while pushing candidates,
then emit in the specified package order. This saves candidate-list storage when the remaining output does not require
the lists. Finalizing on the first candidate loses `W186`; ignoring a later diagnostic changes conformance.

Three precedence rules, in force order: a **direct** directive beats every propagated one regardless of age; among
direct directives, and separately among propagated ones, the newest commit beats the older, and the last unit within a
commit beats the earlier; a directive that proposes nothing (a transition that does not match, or a value equal to the
package's current channel) is not a competitor at all (§11.6, §9.3).

`channelOf(baseline(P))` for a package with no baseline is `stable` (§11.1), so a never-released package is graduated by
nothing and entered onto a train by any directive naming it.

### 13.9 Versions

For each `P` with `effective(P) != none`, or with `channel(P)` differing from its baseline's channel, or with an exact
`Release-As`:

```
if exact Release-As present:              next = that version           # must exceed baseline
else if channel(P) == 'stable':
        next = applyBump(stableBaseline(P), effective(P))               # §12.5, §12.6
else:   next = prerelease per §11.4                                     # incl. channel-entry patch
```

`applyBump` on a virtual `0.0.0` baseline (no stable tag ever) returns `initialVersion`.

A package whose only reason for release is `channel(P) != channelOf(baseline(P))` is a **channel-only release**. It is a
release like any other (versioned, tagged, manifest written, artefact published) and it MUST be reported as `W202`,
for the same reason `W193` exists: a package in a plan with no commits of its own and no bump is otherwise unexplainable
to whoever reviews it. Its version comes from §11.4, including the channel-entry patch and `W204` where that applies.

`next` MUST be strictly greater than `baseline(P)` by SemVer precedence; otherwise `E195`.

A package belonging to a shared-version group is additionally subject to §13.9a, whose member target floor raises
`target` and the graduation version before this comparison is made.

### 13.9a Shared-version groups

This subsection is **normative for an engine that offers shared-version groups** and **OPTIONAL otherwise**. An engine
that versions every package from its own history alone conforms to §§4-24 without implementing any of it. An engine
that offers groups MUST implement all of it, because the rules below are the only thing that keeps a group's members
convergent (§13.7c) while they share a number none of their own windows explains.

**Membership.** A shared-version group is a named set of packages. How membership is stated is configuration's
business (§14); what matters here is that it is a property of the workspace at `HEAD`, that every member belongs to at
most one group, and that a group's rule is one rule for all of its members.

**The three sharing axes.** A group's rule has three axes.

| Axis       | Values                                                        | What it decides                                                         |
|------------|---------------------------------------------------------------|--------------------------------------------------------------------------|
| `semver`   | a shared **depth** `d` of 1, 2 or 3                           | How many leading core components (`MAJOR`, `MAJOR.MINOR`, the whole core) the members hold equal. |
| `counter`  | `fixed` (default) or `independent`                            | Whether the members also hold one prerelease counter (§11.3) in common. |
| `channels` | `fixed` (default) or `independent`                            | Whether the members also sit on one channel (§11.1) in common.          |

`counter: fixed` with `channels: independent` MUST be refused as a configuration error: one counter counts one train,
and a train runs on one channel. The remaining three combinations are valid, and both axes defaulting to `fixed` is
what makes a group stated as a depth alone behave exactly as it did before the axes existed.

An axis MUST NOT be stated for a set of packages that shares no version prefix; there is nothing for it to be an axis
of.

**The group baseline and the line.** `groupBaseline(G)` is the highest-precedence `baseline(P)` over every member `P`
of `G`, held members included: no shared version may fall below a position a member has already published.
`groupStable(G)` is the highest `stableBaseline(P)` over the same set. The group's **line** is `groupBaseline(G)` with
every core component below `d` set to zero, except that a `groupBaseline(G)` ranking below that prefix is itself the
line: a group mid-train is on the train, not past it.

A group **sits on a shared train** when `groupBaseline(G)` is a prerelease whose first `d` core components differ from
those of `groupStable(G)`. Its later prereleases and its graduation then move a part of the version the group shares,
which is the only reason a train belongs to a group rather than to the member that started it.

**Engagement.** A group **engages** when a part of the version it shares moves, and when it does it versions its
members as one: the whole group goes through §13.9 as a single virtual package whose baselines, bumps, new work and
channel proposals are those of its members. When it does not engage, every member is versioned by §13.9 on its own.

| Event                                                       | `counter` fixed, `channels` fixed | `counter` independent, `channels` fixed | both independent      |
|--------------------------------------------------------------|-------------------------------------|-------------------------------------------|-------------------------|
| The shared prefix moves, including onto the next train        | the group                          | the group                                | the group               |
| The group's channel changes on a shared train                 | the group                          | the group                                | members only            |
| Fresh work inside a shared train                              | the group                          | members only                             | members only            |
| A component below the shared prefix moves on the stable line  | members only                       | members only                             | members only            |

At `d = 3` the shared prefix is the whole core, so every stable movement is a movement of the shared part and the first
row covers it. A prerelease counter is not a core component, which is why `rc.N` to `rc.N+1` is the third row and not
the first.

**The member target floor.** Let `floor(G)` be the core of the group's line. Wherever a member `P` of `G` computes a
version of its own, the `target` of §11.4 and the graduation version of §11.5 MUST be raised to `floor(G)` when they
fall below it, **before** the `E185` and `E195` guards read the result.

The floor is what makes a group convergent. A member's own pending window need not contain the work that put the group
where it is: a ride carries none of it, and a leg that failed after its neighbours published carries only part of it,
so the member's own computation can land below a version the group already holds. Raising it before the guards leaves
both guards their meaning, because the floor never reaches past the line: a baseline that nothing in the group explains
still fails, and so does a channel switch that would go backwards.

One exception is required. A member releasing on `stable` whose own baseline is also on `stable` takes no floor while
the group's line is a prerelease: the group has published no stable version of that core, and a member MUST NOT be the
first to.

**Channel proposals.** Only a member whose own resolved channel differs from its own baseline's channel proposes a
channel to the group (§11.1: a channel is derived from a baseline, and a proposal is a directive). A member resting
where its own tags put it proposes nothing. Reading a resting channel as a proposal graduates a whole group because one
member never joined its train, and returns a graduated group to a train because one member never left it.

With `channels: independent` the group takes no channel proposals at all: it decides only whether the shared prefix
moves and to which core.

**Assignment.** When the group engages, each member that is not held is assigned as follows. A member whose own mode
leaves an unchanged package behind (a sparse mode) is assigned nothing unless it has a cause of its own. A member with
no cause of its own is released anyway and its release is a **ride**.

* With `channels: fixed`, every assigned member takes the group's computed version and the group's channel.
* With `channels: independent`, every assigned member is versioned by its own §13.9 computation with `floor(G)` raised
  to the core of the group's computed version, on its own channel, so that each continues its own counter. Two rules
  constrain the channel: a ride by a member on `stable` follows a **prerelease** group version onto that prerelease
  channel, because a ride must never be the first stable publication of a core the group has only reached as a
  prerelease; and a member on a prerelease is never graduated by a ride, because ending a train is deliberate (§11.5)
  and a movement nobody wrote for that member cannot be it.

A ride MUST be reported. The code is implementation-defined, and the report MUST NOT be suppressible: nothing in the
commit log explains why the package is in the plan, so the report is the only place a reviewer can find out.

**Alignment and laggards.** After a run in which the group did not engage, a member whose baseline holds the group's
shared prefix is **aligned** and is neither raised nor released. Under `counter: fixed` a member's whole version is
compared against the line, so a member behind the group's counter is not aligned; under `counter: independent` holding
the prefix on the line's channel is enough, and under `channels: independent` holding the prefix is enough on any
channel.

A member whose baseline is below the shared prefix is a **laggard**. A laggard that is releasing adopts the line when
the counter is shared, and is raised by the floor when it is not. Where the floor was withheld, a member on `stable`
while the line is a prerelease, it is brought to the prefix all the same, on the line's channel and at its own counter:
the floor cannot change a channel, and no axis excuses staying below the prefix. A laggard with nothing pending is
released at the line as a ride, unless its own mode leaves it behind, exactly as a `W193` catch-up discharges an
earlier run's unfinished propagation. With a counter of its own it joins at the start of its own line rather than at
the group's published prerelease.

**Guarantees.** Under `counter: independent`, `G1` to `G6` of §13.7c hold per member exactly as they hold for a package
that versions alone: a retry at a fixed `HEAD` plans each unpublished member at the version the failed run planned for
it, and plans nothing for the members that published.

Under `counter: fixed` this is weaker, and the weakening is stated rather than hidden. A retry after a partial
publication on a shared train advances the group's counter, so the members that failed are planned at a later
prerelease than the one the failed run planned, and the members that already published ride with them. `G3` there
covers the group's core, not its counter. The core is stable across the retry because §11.4 recomputes it from the
stable baseline, and it is the core that a consumer's range and a reader's expectations are about. An implementation
MUST NOT present the counter as stable under this axis.

### 13.10 Emit

Packages with `effective(P) == none` and no channel change and no `Release-As` are **not** released. **Held** packages
are not released regardless of their bump (`W154`). Every other package with a bump is released.

Every package that **is** released is versioned, tagged, and has its manifest written, including packages that are
private, internal, or published to a restricted registry. Where its artefact goes is a separate question, answered by
§13.10a and acted on in §19.

### 13.10a Publish targets

Where a package's artefact goes is independent of whether it is released. Conflating the two costs convergence, for the
reason given in §13.7c.

**`publishTarget(P)`**, where the artefact goes:

| Target   | Meaning                                                                    | Versioned | Tagged | Artefact uploaded     |
|----------|----------------------------------------------------------------------------|-----------|--------|-----------------------|
| `public` | The default public registry.                                               | yes       | yes    | yes                   |
| `<name>` | A named registry from `registries` (§14); private, internal, or per-team. | yes       | yes    | yes, to that registry |
| `none`   | The package produces no installable artefact.                              | yes       | yes    | no                    |

Resolved in precedence order:

1. an explicit `publishTargets` glob match (§14);
2. the manifest's own registry declaration: `publishConfig.registry` in npm, and its equivalents elsewhere;
3. `none`, if the manifest marks the package private and no target is configured for it;
4. otherwise `public`.

A manifest's `private` flag is a statement about **where** a package may be published, not about whether it is released.
A private package pointed at an internal registry is released, tagged, published there, and propagated from, exactly
like a public one. A package with target `none` is still versioned and tagged: its version is real, it is what §9.4
writes into dependents' manifests, and recording it in a tag is what allows the engine to tell next run that this
package is up to date.

**Every package in the workspace is a release unit.** There is no per-package flag that keeps a package in the graph
while excluding it from release: a package with a bump is versioned, tagged, and has its manifest written, and the only
question `publishTarget` answers is whether an artefact is uploaded and where. A package that should not produce an
artefact at all takes `publishTarget: none` and is otherwise ordinary.

This is deliberate. An excluded-but-present package is a permanent hole in the convergence argument of §13.7c and a
standing invitation to the orphan of §13.7a, and the two mechanisms below cover the cases it would have served:

| Want                                                                | Use                     |
|---------------------------------------------------------------------|-------------------------|
| Versioned, tagged, changelogged, but no artefact uploaded anywhere  | `publishTarget: none`   |
| Versioned and tagged, artefact to a private or internal registry    | `publishTarget: <name>` |
| Not a package in any sense, and nothing depends on it               | omit from the workspace |
| Still a package, but changes to certain paths should not release it | `ignoredPaths` (§14)    |

**Do not omit a package from the workspace merely to avoid releasing it.** Omission is only safe for a directory that is
not a package in any sense and that nothing depends on. Removing a real package from the workspace does two things that
are easy to miss and hard to diagnose:

* **It changes path lengths.** Every depth bound in this document is denominated in edges (§8.3). Dropping a package out
  of the graph either strands everything behind it (nothing reaches those packages at any depth, a permanent version of
  the orphan in §13.7a) or, if its dependents are re-pointed past it, shortens every path that crossed it, so a
  reviewed `+2` releases packages its author did not include.
* **It moves file ownership.** Ownership is by longest matching path prefix (§6.2), so a nested directory that stops
  being a package has its files absorbed by the enclosing one. An `examples/` directory under `packages/ui` that is
  removed from the workspace does not become inert: every change inside it starts releasing `ui`. Use `ignoredPaths`
  for that, which suppresses file-derived resolution without touching the graph.

The output is a release plan: package, baseline, next version, channel, contributing units, and for each package the
reason (`direct`, `propagated from X`, `channel from X` where the package has no bump of its own, or `catch-up from X@V`
where `X` is not itself in this plan). Where the channel differs from the baseline's, the plan MUST show both: the
transition a reader needs to see is `beta → stable`, not the word `stable` alone. Packages whose only reason is catch-up
MUST be marked as such (`W193`) and MUST carry the origin's **published** version, so that a reviewer can see at a
glance that the plan is discharging an earlier run's unfinished work rather than releasing something new.

The plan MUST additionally be emitted in the publish order of §19.2, so that the order in which packages will actually
be published is visible before the run starts rather than inferred afterwards.

**Changelog entries.** A package's changelog entries for a release are its surviving corrected tuples (§13.4b), with
two adjustments: an entry restated by `Edits` renders once, as the correcting unit's entry, and MAY be annotated with
the commit it corrects (§7.4); and an entry suppressed by a revert, together with the revert's own entry, is omitted
with `W212` (§7.3). The plan MUST mark corrected and suppressed entries, because records that were rewritten or
removed are exactly what a reviewer of the plan needs to see.

### 13.11 Complexity and scale

This section is **normative for semantic equivalence and informative for cost and technique**. A conforming
implementation MUST produce the plan §13 defines; it need not use or attain any implementation bound below (§17.2).
The table accounts for indexed, in-memory phase work after input decoding and validation. It assumes package and record
identifiers are fixed-width or interned and adjacency and precedence inputs are already in their specified order.
Otherwise comparisons, decoding, sorting, allocation, adapter calls, record matching and emitted bytes add their
ordinary input/output costs. The symbols make those costs visible where they dominate; the table is not an
unqualified end-to-end upper bound. Hash-table operations have expected constant cost under the chosen hashing
assumptions; a deterministic worst-case claim needs a suitable index and must include its construction. A faster
representation is not automatically optimal in either time or memory, and this section makes no global optimality claim.

Notation: `P` packages, `E` workspace dependency edges, `H` commits and `A` parent edges in the history reachable from
the fixed `HEAD`, `C` commits in the union of all pending windows, `U` units in those commits, `N` total bytes of their
messages and changed paths, `T` reachable tags, and `M` tag-record/package-format matches examined while partitioning
the inventory (at worst `P · T`). `k` is the number of **distinct** commits carrying a boundary: a stable baseline, or
a package's newest baseline of any channel where that is another commit. `m` is the number of distinct **marker**
commits, the commits some ancestry question of §13 is asked about: the `k` boundaries, every `cancel` commit, and every
commit carrying an `Edits`, `Deletes` or `Reverts` footer. `R` is the
actual work of resolving scopes and changed paths against the workspace, including candidates examined when the result
is empty. `I` is the number of resulting unit-to-package incidences (and can be `P · U`). `Z` is the number of
unit/source/target contribution or provenance incidences retained or emitted. `Zv` counts incidences examined while
constructing them, including duplicate insertions; `Zv` may exceed `Z`. Let `Fc` count correction footer selectors,
`Jcorr` the package/target incidences examined for correction scope containment, live scopes and application, including
unsuccessful probes, and `wildcards` the wildcard selectors. Let `Ic` count cancel-scope incidences, `Pc` the number of
packages named by a cancel, and `Jc` the number of cancellation queries across direct admission and **both** propagation
axes, including candidates rejected by cancellation. `Jc` is not bounded by direct incidence count `I` or retained
contribution count `Z`. Write `bw(x) = max(1, ceil(x / wordSize))`, so a zero-marker phase still pays for traversal or
query dispatch. `F` is the number of publish failures,
and `Oout` is the size of diagnostics and other emitted output.
In the per-target row, and there only, `D` is the number of targets one unit reaches, `S` its source-set size, and `Σ`
its resolved scope-set size.

For the polyrepository profile, `H` and `A` below mean the sums over the fixed reachable snapshots of all repositories,
not the size of a fictitious merged history. Let `Q` be the number of repositories, `Hq` and `Aq` one repository's
reachable commits and parent edges, `G` the number of control-repository gitlink transitions examined, and `Kq` the
number of distinct boundary revisions used in repository `q`, stable and newest-baseline boundaries counted together and
the no-boundary class counted once. `Kq` counts revisions and not `(stable, fresh)` pairs, which can number the product
of the two. `mq` is the marker count `m` restricted to the commits of repository `q`. Let `V` be the number of package
memberships in shared-version groups (`V <= P`). Under the linked peer topology of §27.11 there is no control index and
`G` is zero; let `X` be the number of `(consumer release tag, repository)` boundaries resolved, `Y` the number of hops
in the longest route between two peers (at most `Q - 1`, and at most 2 in a star), and `B` the number of distinct
`(repository, revision)` trees whose fleet links are read (`B <= X · Y`). The implementation MUST preserve repository
identity in every index and cache key.

For the execution profile of §28, let `Tk` be the run's tasks and `Dk` their precedence edges, `Wn` the worker nodes,
`Po` the packages whose outputs some other task consumes, and `Lr` the coordination refs alive in one transport
repository.

| Phase                      | Literal transcription | Achievable            | Note                                        |
|----------------------------|-----------------------|-----------------------|---------------------------------------------|
| Load workspace (§13.1)     | `O(P + E)`            | `O(P + E)`            |                                             |
| Load tags (§13.2)          | `O(T log T + M)`      | `O(T + M)`            | Running maximum per package; hash `(package, version)` for `E191`. One inventory can still require many matches |
| Pending windows (§13.3)    | **`O(P · (H + A))`**  | `O((H + A) · bw(m) + Iw)` | One marker pass; `O((k + 1) · (H + A) + Iw)` with a walk per boundary |
| Parse and resolve (§13.4)  | `O(N + R + I)`        | `O(N + R + I)`        | Lexing and resolution have different cache keys |
| Corrections (§13.4b)       | repeated ancestry and scope scans | `O(H + Fc + Jcorr + wildcards · I + Oout)` | Indexed ancestry charged above; count package/target probes even with no wildcards |
| Cancellation predicates (§13.5, §9.2) | a cancel scan per query | `O(Ic + (Pc + Jc) · bw(cancels))` | Build per-package cancel masks; ancestry charged above; diagnostic attribution adds its own work |
| Direct bumps (§13.6)       | `O(U + I)`            | `O(U + I)`            | Consume resolved incidences                 |
| Holds (§13.6a)             | `O(U + I)`            | `O(U + I)`            | Consume resolved incidences                 |
| Propagation (§13.7) graph walks | **`O(U · (P + E))`** | input-dependent    | Reuse walks only when full inputs match     |
| Propagation materialisation| repeated set unions | `O(Zv + Z)` | In-place indexed insertion; only unique required output has the lower bound `Ω(Z)` |
| per-target predicates      | **`O(D · (S + Σ))`**  | `O(D + S + Σ)`        | Per unit. Hoist `resolvableBy`, `resolve()` |
| Channel resolution (§13.8) | **`O(P · U)`**        | `O(U + I + P)`        | Invert unit-to-package incidences           |
| Versions/plan (§13.9–10)   | repeated member/record scans | `O(P + I + Z + Oout)` | Consume already-built aggregates/provenance; scan each disjoint version group once |
| Publish order (§19.2)      | `O(P² + E)`           | `O(E + P log P)`      | Scanning the ready set for the least name; a comparison heap instead |
| Blocking closure (§19.3)   | **`O(P · (P + E))`**  | `O(P + E)` per run    | A walk per planned package; one multi-source reverse traversal instead |
| Build readiness (§19.2a)   | `O(P · (P + E))`      | `O(P + E)`            | A search per building pair through the packages between them; a pass-through node per package that does not build, or one memoised visit per package, instead. At most two task edges per dependency edge survive transitive reduction |
| Polyrepository snapshots (§27) | repeated control scans | `O(G + sum(Hq + Aq))` input walk | Index control gitlinks once; walk each source snapshot once |
| Polyrepository windows (§27) | `O(P · sum(Hq + Aq))` | `O(sum((Hq + Aq) · bw(mq)) + Iw)` | One marker pass per repository; `O(sum(Kq · (Hq + Aq)) + Iw)` with a walk per boundary |
| Publication input closure (§27.2) | `O(P · (P + E + V))` | `O((P + E + V) · ceil(Q / wordSize))` | Condense the augmented graph, then one bitset union per edge |
| Link evidence (§27.11)     | `O(X · (Q + Y))`, `X · Y` tree reads | `O(Q + X · Y)`, `B` tree reads | Root the link tree once; read each `(repository, revision)` tree once |
| Link settlement (§27.11)   | one commit per route hop | one commit per recording repository | Merge a package's routes into one tree: at most `Q - 1` commits, 2 in a star |
| Record comparison (§13.2)  | `O(T)` per repository | `O(T)` per repository | One inventory read under the lock; one ancestry question per record the input lacks |
| Task graph and readiness (§28.3) | `O(Tk · (Tk + Dk))` | `O(Tk + Dk)` | Rescanning every task after each completion; indegree counters decrement each successor once |
| Placement (§28.2)          | `O(Tk · Wn)`          | `O(Tk · Wn)`          | `Wn` is small; a free list per compatibility class removes the scan and is rarely worth its bookkeeping |
| Output transfer (§28.5)    | up to `Tk · Po` transfers | at most `Po · Wn` transfers | One per distinct `(output set, node)`, not one per consumer task; verified bytes are not fetched again |
| Coordination polling (§28.4) | `O(Lr)` refs per tick | refs addressed to the polling node per tick | The branch-name prefix routes; one fetch per changed tip; refs of dead runs tax every tick until removed |

The bold rows highlight common multiplicative costs; corrections, cancellation, provenance and retained indexes can
also dominate. Each must be charged even when its final result is empty. Window classes safely share history reachability work. Propagation traversal is reusable
only under the stricter conditions below; predicate hoisting and channel incidence inversion remain safe independently.

The polyrepository bounds are deliberately sums over repositories. They are not globally linear in fleet history:
distinct boundaries can require distinct reachability sets, output can contain `P · U` scope incidences, and propagation
can still materialise `Z` contributions. An implementation MUST NOT scan the control history once per consumer. It
SHOULD index every relevant gitlink transition in one pass per fixed control snapshot, then answer consumer baseline
lookups from that immutable index. It MUST parse and store each `(repository, commit)` record at most once per plan.
Ancestry and walk caches MUST be bounded by configured memory or by an eviction policy; a cache of every queried pair
can itself grow quadratically in `Hq`. The bound covers **all** cache tiers, including single-source walks, exact-source
walks, empty results, keys and index metadata, not just the number of stored target entries. Per-plan lifetime alone
is not a memory bound. Recomputing after eviction preserves semantics but may lose the cached time bound; that tradeoff
must be stated. No cache may synthesize ancestry between repositories.

The publication input closure is over the graph augmented with shared-version-group membership, as §27.2 defines.
Dependency edges alone are acyclic, but a group joins its members in both directions, so dependency and group edges can
form a cycle and a single topological pass over packages does not reach the fixed point. Represent each group as one
node adjacent to its members, condense the strongly connected components of that graph in `O(P + E + V)`, and visit
the components in reverse topological order: a component's repository set is its members' owners united with the sets
of the components it reads, one bitset union per condensed edge. Every member of a component has the same closure, so
the result is exact. Computing one repository's reachable package set at a time, `O(Q · (P + E + V))`, also avoids a
graph walk per release and is a valid intermediate that gives up the word-parallel factor. A dense representation
costs `O(P · ceil(Q / wordSize))` words before equal sets are interned; interning reduces repeated storage but does not
change that worst case. Expanding every distinct bitset into repository-name slices raises retained storage to `O(P ·
Q)` name references in the worst case; implementations SHOULD keep the compact form across internal boundaries or at
least intern equal lists. Treating each group as a clique would add quadratic work in the group size; index each group
once and visit its members as one adjacency list instead.

**Windows: group by distinct baseline commit.** For a fixed `HEAD`, `W(P)` is determined by
`stableCommit(P)`. Different package tags that resolve to the same commit therefore share a window. A release MAY
record packages at different commits; `k` counts distinct boundary commits, not release runs, and can be as large as
`2P`. Computing reachability once per distinct boundary commit, plus once for packages with no baseline, and testing
membership by lookup replaces `P` traversals with at most `k + 1`, and the marker pass below replaces those with one.
The fresh window needs no class of its own. By
§13.3, `Wfresh(P) = W(P) - reach(baselineCommit(P))`, so with `after(b) = reach(HEAD) - reach(b)` a commit is in
`Wfresh(P)` exactly when it is in both `after(stableCommit(P))` and `after(baselineCommit(P))`. Every window is
therefore a function of one boundary commit, a fresh membership test is the conjunction of two lookups, and no
reachability set is keyed by a `(stable, fresh)` pair. The identity holds whether or not the newest baseline descends
from the stable one.
The traversals range over the reachable history, so their safe bound is in `H + A`, not `C`: proving that a commit is
outside a pending window may require walking commits that never enter the union. `Iw` is the number of stored or emitted
package/window memberships and is `P · C` in the worst case.

**Inventory once, partition explicitly.** An adapter may return one complete release-record inventory for the fixed
snapshot and let the planner match package formats in memory. This reduces adapter queries from one per package to one;
it does not erase the `M` matches or their byte costs. The response MUST be complete for the namespace and snapshot,
and implementations SHOULD validate any advertised metadata-object count before trusting it as a complete inventory.
An incomplete bulk response cannot be repaired by treating missing packages as unreleased.

Window storage may likewise be shared only inside one plan and fixed history snapshot, between packages whose stable
boundary identity is equal (with a separate no-baseline class). The shared membership set MUST be immutable. `Wfresh(P)`
also depends on `P`'s newest baseline of any channel: two packages can share `W(P)` because their stable baseline commit
is equal while having different prerelease `baselineCommit(P)`, so fresh/contained membership MUST remain per package
or be keyed by that second boundary too. Keying the second operand by its own boundary commit, as above, satisfies this
without a class per pair.

In the polyrepository profile, the identity of the repository whose history a window ranges over is also part of the
window key. Two equal SHA byte strings in different repositories are unrelated. A window over repository `q` is a
function of `q`, its planned head, and one boundary revision in `q`, and of nothing else: packages may share an
immutable window over `q` exactly when their boundary in `q` is the same qualified revision, whichever repositories
own them. A materialised fresh window may be shared only when both its stable and newest-baseline boundaries are equal.
This is the `Kq` grouping above. A control snapshot's gitlink index or a linked peer's recorded links can locate those
boundaries, but neither can replace the source repository's ancestry walk.

Commit-graph generation numbers can reject some ancestry candidates and bound a graph walk. They do not establish
ancestry by a constant-time comparison: commits on different branches can have ordered generation numbers without
an ancestor relationship. A positive answer still requires a reachability query or a previously computed index.
See Git's [commit-graph design](https://git-scm.com/docs/commit-graph).

Storing `W(P)` as an explicit set per package costs `O(P · C)` **memory**. A dense representation over every reachable
history index uses `k + 1` bitsets of `H` bits: one for each distinct boundary commit plus the no-baseline window class.
Alternatively, after the pending union is known, a representation indexed only by its `C` commits uses
`O((k + 1) · C)` bits, but computing that union and each membership still requires reachability over `H + A`. A sparse
representation costs in `Iw`, the actual membership incidences. Each makes §13.4a admission a membership lookup; none
turns the history traversal itself into `O(C)`.

**Ancestry: one marker pass, not one walk per question.** Every ancestry question §13 asks has one shape: is commit
`c` an ancestor-or-self of a marker `x`? The markers are the window boundaries (§13.3), the `cancel` commits (§10.3),
and the commits carrying a correction (§13.4b) or a `Reverts` footer (§7.3). Give each of the `m` markers one bit and
visit the reachable history once, children before parents, which is the reverse of the order §25 requires of an
adapter: set a marker's own bit when it is visited, then OR the visited commit's mask into each of its parents. `c` is
an ancestor-or-self of `x` exactly when `c` is `x` or some child of `c` is an ancestor-or-self of `x`, so when the pass
ends, bit `x` of `mask(c)` is set exactly when `c ∈ reach(x)`. The pass costs `O((H + A) · ceil(m / wordSize))` word
operations for `m > 0`, with `O(H + A)` indexing/traversal overhead also when `m = 0`; a walk per marker
costs `O(m · (H + A))`. Dense rows occupy `H · ceil(m / wordSize)` words, including word padding.
Transposed per-marker columns occupy `m · ceil(H / wordSize)` words. These have the same `H · m` logical bits,
but neither layout is universally optimal. Retaining both layouts adds their space costs. A transpose that enumerates
every set bit adds `Θ(H · m)` scalar work on a chain with many markers, losing the stated word-parallel saving.
Keep row masks when queries need one ancestry bit, or charge the chosen transpose algorithm separately. Afterwards `c ∈ after(b)` is one clear bit, `cancelledFor(c, X)` is a non-empty
intersection of `mask(c)` with the cancel markers whose scope contains `X`, and the proper-ancestor test of §13.4b is
one set bit together with `c ≠ x`. Under §27 the pass runs once per repository over that repository's own markers,
and repository identity stays in every key.

The pass need not leave the union of the pending windows. A window is closed under descendants, so every commit on a
path from a marker down to a commit of the union is itself in the union: no path leaves the union and returns to it.
A pass over the union's commits and the parent edges between them therefore sets exactly the bits the full pass sets
for those commits, at `O((C + Ac) · ceil(m / wordSize))` with `Ac` the parent edges inside the union, and a marker
the union does not hold is an ancestor of none of its commits, provided it is reachable from `HEAD`, which a release
tag is (§12.2). What remains in `H + A` is finding the union in the first place. A backend that can name it in one
query, as Git can (`HEAD` with the best common ancestors of the boundaries excluded), reads messages and changed
paths once for the union where a read per boundary repeats every commit two windows share; each window is then the
union minus one marker's bits, in the union's order.

Two refinements bound the pass further, and neither changes its worst case. Equal masks SHOULD be interned: on a
linear release history the boundaries are nested, `reach(b1) ⊆ reach(b2) ⊆ …`, so at most `k + 1` distinct boundary
masks exist. This `k + 1` count concerns **boundary bits only**; with correction and cancel markers, a chain
can have `m + 1` distinct masks. Interning stores an identifier per commit in addition to the mask dictionary and
its construction costs. A direct chain representation can instead store commit positions and answer ancestry by
comparing positions, using `O(H + m)` words and `O(H + A + m)` preprocessing without constructing dense masks.
Such a shortcut requires verifying that the reachable history is a chain; it is invalid for a general DAG.
An OR-result cache MAY reuse equal mask pairs, but its keys and values also need a memory budget. The pass MAY also stop early when its visiting order is known to be child-before-parent without a full
walk, as decreasing commit-graph generation number is. A window is closed under descendants, so its complement is
closed under ancestors. Once every unvisited commit that has a visited child carries all `k` boundary bits, every
unvisited commit is an ancestor-or-self of one of them, lies in no window, and is named by no tuple of §13.4. The stop
is sound only when no package lacks a stable baseline, because such a package keeps the whole history in its window,
and only after every commit named by an `Edits`, `Deletes` or `Reverts` footer has been visited, because `E210` and
`W213` read the ancestry of a target that lies outside every window.

**Propagation: cache traversal, preserve unit identity.** A graph walk may be reused when its source set, depth and edge
kinds are identical. Its reached nodes and distances are then the same. The remaining work MUST still be evaluated per
unit and target: admission uses that unit's commit in `Wfresh(d)`; cancellation uses the `(commit, d)` pair; scope
filters are unit-specific; inherited channel is derived from that unit and its sources; and provenance and diagnostics
identify the contributing unit and sources. Thus `(window class, directive)` is not a sufficient cache key, and taking
the union of source sets is not generally semantics-preserving. A safe implementation caches walks and hoisted per-unit
predicates, or uses indexes that retain the contributing unit for every reached target. No sublinear bound in
`U · (P + E)` follows merely from there being few directive spellings or window classes.

A walk keyed by **one source package** composes exactly, and recurs far more often than a whole source set does: there
are at most `P` source packages, and as many distinct source sets as there are units. A multi-source breadth-first
search assigns each node its least distance from any source, so for a unit with source set `S` and depth `N`, `reach`
returns exactly the packages `d ∉ S` with `min over p ∈ S of dist(p, d) ≤ N`, each at that minimum as its level. An
implementation MAY therefore cache one unbounded walk per `(source package, edge kinds)`, serve every depth from it by
filtering on level, and form a unit's reach as the union of its sources' walks minus `S`, taking the least level per
target. The union is sound **within** one unit; the union **across** units criticised above is not, and the trap below
is about exactly that. The union costs the summed size of its sources' walks, so for a unit whose scope-set covers
much of the workspace one multi-source walk is cheaper, and the choice is per unit. An unbounded single-source walk
is a preprocessing option, not a cost-free answer to a shallow query: on a chain of `P` packages, one depth-one unit
per package needs `Θ(P)` total reached-target work, while computing and retaining every unbounded source walk takes
`Θ(P²)` time and target entries. On a cache miss, prefer a walk bounded by the requested depth, or lazily extend a
cached breadth-first frontier. Charge extensions and use the cache budget across all entries; reuse the literal walk
when retaining or combining an index would cost more than the requested traversal.

The channel **propagation walk** can be skipped when no unit can propagate a channel. Direct channel resolution (§13.8)
must still run: a depth-zero `%beta` or `Channel: beta` names its own package without any propagation walk, and phase 3
must see the resulting channel. Only when there are neither propagated nor direct channel candidates may an
implementation initialize every package from its baseline and omit the rest of channel resolution.

The channel and bump phases are sequential because phase 3 reads the channels phase 2 produced, and they MUST NOT be
fused (§9.2). This phase ordering does not add a factor to asymptotic big-O notation. The phases share the history load;
their graph walks and contribution incidences are counted in the propagation rows above.

> **A correctness trap in traversal reuse.** Merging source sets is sound only for the question "is some source within
> this depth?" and **not** for the
> per-unit self-exclusion of §9.2. A single unit never propagates to its own source packages, per `seen = set(sources)`,
> but a package that is a source of one unit may legitimately be a *target* of another unit in the same bucket, and
> subtracting the merged source set silently withholds those bumps. The symptom is a package that should have taken a
> propagated `major` releasing as its own `patch`, with no diagnostic. Admit any node reached across **at least one
> edge** within the depth bound **from a unit for which that node is not a source**. Re-evaluate the per-unit
> self-source predicate for every reached node in the merged source set, or exclude such units from bucketing. Merely
> finding some edge over-admits a source of another unit. Implementations SHOULD test a bucketed
> propagation against a literal per-unit one over randomised workspaces; the two MUST agree exactly.

**Per-target predicates: hoist everything that does not read the target.** Inside the target loop of §9.2, three things
are loop-invariant and three are not. `commitOf(u)`, `resolve(u.propagateScope)`, and the source-channel set of §9.3a
depend only on the unit; only `Wfresh(d)`, `cancelledFor(·, d)` and the final `channel[d]` membership read the target. A
transcription that resolves the scope-set and re-scans the source set once per target turns an `O(D + S + Σ)` unit into
an `O(D · (S + Σ))` one, and the unit where that bites is precisely the one an author reaches for when they mean it: a
`^^` across a wide scope-set has a large `D`, and re-deriving a two-element channel set thousands of times to answer a
question whose inputs never changed is pure waste. Hoisting is not an optimisation to be justified by profiling; it is
what the predicate's own dependency structure already says (§9.3a). The inherited origin channel in phase 1 is likewise
memoized once per unit when first needed. Deferring it until an admitted target needs it avoids emitting diagnostics
from an otherwise unevaluated branch; required diagnostic attribution and order must be preserved. This bound covers the
hoisted scope/channel predicates only. The delivery test of §13.4a is one window bit for a target that has not released
past the unit's commit, which is every target in the ordinary case; a target that has costs one ancestry question per
release of each source past that commit, answered from the marker pass when those releases are markers, and such targets
exist only after a consumer got ahead of a provider. Cancellation intersections cost up to `bw(cancels)` per queried
target; materialising `prov[d] |= sources` must also charge the source insertions in `Zv`, even when deduplication
leaves `Z` unchanged.

The saving compounds with safe traversal caching: caching can reduce the number of graph walks, while hoisting reduces
the work in each unit's target loop. Neither subsumes the other, and a wide `^^` unit is the case where both can help.

**Channel resolution: push from units, do not pull from packages.** `directChannelFor(P)` reads naturally as "find the
directives in `P`'s window that name `P`", and written that way it scans the union window once per package: `O(P · U)`,
almost all of it spent confirming that no directive names the package at all. Inverting it (one pass over the units in
§11.6 order, appending each to the packages its scope-set resolves to) costs `O(U + I + P)` on top of the scope resolution
§13.4 has already performed, and touches only the packages some directive actually names. This is the same shape as
§13.4, which resolves units to packages for exactly the same reason, and an implementation that has already built that
mapping can often reuse it directly rather than rebuilding it here. The ordering and `W186` obligations that come with
the inversion are stated in §13.8 and are not optional. Reduce each completed candidate list in one pass, retaining the
newest actual proposal and the information required for `W186`. A directive equal to the baseline proposes nothing;
returning it before examining older candidates contradicts the precedence rule.

**Cache lexical parsing separately from resolution.** The union window spans all history whenever any package is
unreleased (§13.3), so a workspace that has just gained a package may revisit every record. Lexical parsing is a pure
function of message bytes, parser implementation/schema, and all configuration that affects parsing. It may be cached
by `(repository identity, immutable record identity, parser/schema identity, parsing-configuration digest)`. Under Git
the record identity is the commit SHA; other adapters' opaque identifiers are meaningful only within their repository.
Scope and changed-path resolution additionally depends on the current workspace package/path index, graph and relevant
resolution configuration. It MUST use a digest of those inputs or be recomputed. A cache hit for lexing therefore saves
`N` work but does not imply that `R` or `I` is cached, and no whole-plan cache follows from record immutability alone.

**Scope resolution: index the workspace, do not scan it.** `R` is where a transcription of §6 spends time that no row
above accounts for, because both of its lookups read naturally as loops over every package. File ownership (§6.2) is a
longest-prefix question, and the prefixes of a path are its ancestor directories: probe them, longest first, in a map
from package root to package, and the first hit owns the file. That costs the path's own components, where comparing
the path against every package root costs `P` per changed path, in every commit of every window. A literal scope term
is one map lookup. A glob whose only `*` is its last byte, which `@acme/*` and nearly every written glob is, selects a
contiguous range of the byte-wise sorted package names and costs `O(log P)` plus its matches; only a glob with an
interior `*` needs a pass over the names, at the per-candidate cost §18.3 requires. A scope-set's resolution depends
on its commit only through `.` and, under §27, through the repository that owns the commit, so every other scope-set,
a `Propagate-Scope` repeated along a train in particular, MAY be resolved once per distinct text. None of this changes
`I`, which is output.

**Deterministic order and failure closure.** Kahn's topological sort is `O(P + E)` if any available zero-indegree node
may be chosen. CCME requires the byte-wise least available package: scanning the ready set for it at every step is `O(P²
+ E)`, and a comparison heap gives `O(E + P log P)`. No comparison-based structure improves that bound, because a
workspace with unsorted names and no edges makes the sequence a sort of its names; any other structure MUST provably return the same least
element at every step. This is a worst-case bound for unsorted comparison keys, not an instance-optimality result:
if name ranks or sorted names are already provided, the sorting reduction alone proves no new `P log P` lower bound.
During publish, `run` as §19.3 writes it tests each planned package by a fresh transitive walk,
which is `O(P · (P + E))` in the worst case whatever the number of failures, and a walk from each failure instead is
`O(F · (P + E))`. For blocked membership alone, add failures to an incremental multi-source reverse traversal and mark
each newly reached consumer once; total traversal work is `O(P + E)` per run. If output records every failed ancestor or
path, its additional cost is the size of that provenance. The first cause and `W194` ordering MUST still follow
deterministic publish/failure order rather than queue or hash iteration order.

**Space and lower bounds.** Memory is counted in machine words below, except input/output byte buffers. With indexed
inputs retained, a conservative storage ledger is:

| Representation | Retained or auxiliary space | Qualification |
|----------------|-----------------------------|---------------|
| Package/history adjacency | `O(P + E + H + A)` | Input indexes; not repeated per package |
| Parsed messages and resolved incidences | `O(N + U + I)` | Byte buffers plus unit/incidence records; cached resolution has its own key |
| Dense history marker rows | `O(H · ceil(m / wordSize))` | Plus history traversal state; zero markers need no rows |
| Explicit package windows | `O(Iw)` | Optional materialisation; shared boundary indexes can avoid it |
| Cancel masks | `O(Pc · ceil(cancels / wordSize))` | Plus scoped-package lookup and diagnostic state |
| One propagation BFS | `O(P)` auxiliary | On top of shared adjacency; retained walk caches have a separate budget |
| Direct channel candidates | `O(P + I)` as lists, or `O(P)` summaries | Summaries require the §13.8 deferred-output rule; diagnostics/provenance storage is additional |
| Contribution/provenance records | `O(Z)` | Streaming can reduce retention only if subsequent phases and output permit it |
| Heap publish order / blocking membership | `O(P)` auxiliary | Shared graph excluded; all-cause/path diagnostics add output-sized storage |
| Publication input closure bitsets | `O(P · ceil(Q / wordSize))` | Shared SCC sets may reduce actual storage; expanded lists cost up to `O(P · Q)` |
| Link evidence cache | number of retained pins plus keys | `B` counts trees, not their entries or bytes; dense hub revisions can be large |

No implementation should add every alternative in this ledger by default. Account for peak simultaneously live
allocations, construction scratch space, immutable input buffers, retained output and cache keys. Batching marker
computation limits scratch space but does not bound all retained marker columns, and eviction changes recomputation
cost. Tree reads, decoding and output bytes remain separate from in-memory lookup counts.

Linear scanning is worst-case time-optimal when every input byte must be inspected; explicit output needs time at
least its size. A single adjacency-list traversal and blocked-membership closure have tight worst-case linear scan
bounds. The publish heap matches the comparison-sorting lower bound for unsorted names. These are conditional,
phase-specific statements. Neither `Ω(Z)` output work nor a dense `H × m` or `P × Q` representation proves that every
instance requires that representation or that the complete planner is jointly time- and space-optimal. Correction,
reachability, scope and cache choices retain input-dependent tradeoffs.

**Build readiness: what a weaker relation can and cannot buy.** Let `b_i` and `p_i` be the build and publication
durations of package `i`, and let every slot be free. A run then lasts as long as the longest path of the task graph
of §19.2a, and a weaker relation never lengthens it, because its constraints are a subset of the stronger one's. On a
chain of `n` packages, each consuming the one before it, under one relation throughout: `publish` gives
`sum(b_i + p_i)`; `build` gives the greatest over `k` of `b_1 + … + b_k + p_k + … + p_n`; `none` gives the greatest
over `k` of `b_k + p_k + … + p_n`. With equal durations these are `n · (b + p)`, `max(n · b + p, b + n · p)` and
`b + n · p`: `build` saves `(n - 1) · min(b, p)` over `publish`, and `none` saves a further `(n - 1) · (b - p)`, and
only where builds outlast publications. For one provider `0` and consumers `i` the three are
`b_0 + p_0 + max(b_i + p_i)`, `b_0 + max(max(b_i, p_0) + p_i)` and `max(max(b_i, b_0 + p_0) + p_i)`. For providers `i`
of one consumer `c` they are `max(b_i + p_i) + b_c + p_c`, `max(max(b_i) + b_c, max(b_i + p_i)) + p_c` and
`max(b_c, max(b_i + p_i)) + p_c`. Where one owner's publications are serialized (§27.2, §28.6) in the order `1 … n`,
every relation gives the greatest over `k` of `e_k + p_k + … + p_n`, with `e_k` the instant the build of `k` ends: the
floor is `sum(p_i)` whatever the relation, and a weaker relation only lowers the `e_k`.

Under budgets of `m_b` build slots and `m_p` publication slots, write `Wb = sum(b_i)`, `Wp = sum(p_i)` and `L` for the
longest path. No schedule beats `max(L, Wb / m_b, Wp / m_p)`. A placement that never idles a slot of a stage while a
task of that stage is ready finishes within `L + Wb / m_b + Wp / m_p`, because at every instant either a task of one
fixed chain is running or the stage that chain waits for has every slot busy; that is within three times the best
possible, and it is not claimed tight for this shape of graph. The unlimited-slot result does not carry over: under
budgets a weaker relation can lengthen such a schedule, although never the best one. With two build slots and one
publication slot, packages `(b, p) = (2, 3)`, `(1, 2)` consuming the first, and an unrelated `(8, 3)` finish at 11
under `build` and at 12 under `none`, because the consumer's early build takes the slot the long build would have had.
This is Graham's scheduling anomaly, and it is why §19.2a promises a happens-before relation and no duration.

**Distributed execution: what placement can and cannot buy.** Let `Wk` be the summed duration of a run's command
tasks, `Lk` the duration of its longest precedence chain, and `s` the task slots usable at once, the lesser of the
summed node capacities and the applicable run-wide stage limits. No schedule finishes before `max(Lk, Wk / s)`. A
placement that never leaves a usable slot idle while a compatible task is ready finishes within
`Wk / s + (1 - 1/s) · Lk`, which is at most `(2 - 1/s)` times the best possible, when slots are interchangeable and
transfers cost nothing; this is Graham's bound for list scheduling under precedence constraints. Neither premise holds
in general: nodes differ in platform and speed, and an output transfer delays the consumer it feeds. This section
therefore states no bound once transfers count, and the measurement §28.7 requires stands in its place. The inequality
still says what to expect. On a chain `Lk = Wk` and no number of nodes helps. Beyond `s = Wk / Lk` the chain and not
the pool is the limit: the best possible time is `Lk`, and more nodes can only close the gap between a greedy schedule
and `Lk`, which is below a factor of two.

**What none of this may change.** These are all internal representations. The plan, the diagnostics, and their order
MUST be identical to the literal reading (§17.2), and an implementation that trades a different plan for speed does not
conform however fast it is. Determinism in particular forbids introducing parallelism whose scheduling affects iteration
order of any collection that reaches the output.

**Appendix B.7a tests exactly this.** Each of its vectors is a case where one of the transformations above is *almost*
equivalence-preserving: a hoist that collapses a multi-channel source set, an inversion that loses §11.6 order, a fused
pass that miscounts `W186`, a skipped channel pass that leaves `channel(P)` unset. An implementation that applies none
of these optimisations passes them without effort; one that applies them and has not thought about the edges fails them,
which is the point. The general obligation stands on its own: B.7a is not exhaustive, and an optimisation it does not
name is still the implementer's to justify.

---

## 14. Configuration

CCME 3 additionally defines `vcs` (§25), explicit rollback activation plus package/space handler declarations (§26),
the optional polyrepository Git profile (§27), and optional distributed execution (§28). External VCS adapters and
rollback remain future engine contracts; implementing one profile does not implement the others. Omitting `vcs`
selects Git. Omitting rollback execution enablement never authorizes withdrawal. Omitting all §27 activation inputs
preserves the single-history model. Section 28.2 defines the `execution` node settings: the default role is
orchestrator, and an empty worker list preserves local execution.

Defaults are chosen so that an unconfigured repository behaves conservatively and predictably.

| Key                         | Default                                                      | Meaning                                                                                   |
|-----------------------------|--------------------------------------------------------------|-------------------------------------------------------------------------------------------|
| `separator`                 | `"---"`                                                      | Unit separator line (§4.3).                                                               |
| `tagFormat`                 | `"{name}@{version}"`                                         | Only `{name}@{version}` is normative; other formats are implementation-defined.           |
| `initialVersion`            | `"0.1.0"`                                                    | First version for an untagged package.                                                    |
| `preserveMajorZero`         | `true`                                                       | Remap bumps while `0.y.z` (§12.6).                                                        |
| `types`                     | table in §7.1                                                | Type → bump mapping.                                                                      |
| `strictTypes`               | `false`                                                      | Unknown types error instead of warn.                                                      |
| `lenient`                   | `false`                                                      | Downgrade selected errors to warnings (§16).                                              |
| `maxDescriptionLength`      | `100`                                                        | `W120` threshold.                                                                         |
| `propagation.bump`          | `"patch"`                                                    | Default `Propagate`.                                                                      |
| `propagation.depth`         | `0`                                                          | Default `Propagate-Depth`. Set to `1` for direct consumers, `"all"` for the full closure. |
| `propagation.kinds`         | `["dependencies","peerDependencies","optionalDependencies"]` | Manifest fields traversed as propagation edges (§8.4). The wildcard `"*"` selects every kind. |
| `propagation.channel`       | `"inherit"`                                                  | Default `Propagate-Channel`. Consulted only where channel depth is above `0`.             |
| `propagation.channelDepth`  | `0`                                                          | Default `Propagate-Channel-Depth` (§8.3a). Set to `1` or `"all"` to carry trains along.   |
| `propagation.respectRanges` | `false`                                                      | Skip dependents whose declared range still admits the new version (§9.2).                 |
| `rangeStrategy`             | `"caret"`                                                    | How dependent manifests are rewritten (§9.4).                                             |
| `rootPathMap`               | `{}`                                                         | Glob → package list for files owned by no package (§6.2).                                 |
| `ignoredPaths`              | `[]`                                                         | Globs removed before file-derived resolution.                                             |
| `channels.allowed`          | `null`                                                       | If set, restricts every channel value (both sides of a transition) to a list (§11.2).   |
| `versionGroups`             | `{}`                                                         | Group name → sharing rule: the shared depth, and whether the prerelease counter and the channel are shared (§13.9a). Membership is stated by the packages. |
| `publish.orderKinds`        | `["dependencies","peerDependencies","optionalDependencies"]` | Edges defining the publish order (§19.2).                                                 |
| `publish.blockingKinds`     | `["dependencies","peerDependencies"]`                        | Edges over which a failed publish blocks dependents (§19.3).                              |
| `publish.onFailure`         | `"skip-dependents"`                                          | `"skip-dependents"` or `"abort"`; what a failed publish does to the rest.                |
| `publish.adoptPublished`    | `true`                                                       | Tag a version the registry already holds, when identity is verified (§19.4).              |
| `publish.verifyConvergence` | `true`                                                       | Classify one post-success re-plan under §19.6.                                           |
| `registries`                | `{}`                                                         | Registry name → URL/credentials handle, referenced by `publishTargets` (§13.10a).         |
| `publishTargets`            | `{}`                                                         | Package glob → registry name or `none`. Highest-precedence target source.                 |
| `polyrepo`                  | `false`                                                      | Opt into the polyrepository Git profile (§27).                                            |
| `repository`                | `""`                                                         | This repository's own identity in a linked peer fleet. A non-empty value activates peer composition (§27.11). |
| `repositories`              | `[]`                                                         | The other peers in a linked fleet: `{name, url, path, branch}` per peer. MAY be empty for a one-member fleet; non-empty without `repository` is `E339` (§27.11). |
| `configs`                   | `[]`                                                         | Explicit configuration files imported into the combined workspace; a non-empty list activates the profile (§27.3). |
| `repositoryOverrides`       | `{}`                                                         | Source repository identity → participation and central release-commit policy override (§27.3). |
| `repositoryBaselines`       | `[]`                                                         | Explicit cross-repository consumer boundary tuples used only when ordinary checkpoint evidence is absent or ambiguous (§27.6). |

### 14.1 Safety limits

Referenced by §18. These divide into two groups, and the division is normative.

**Enforced by default.** These have concrete defaults and fire without any configuration. A repository that has never
written a config file is still subject to them; they may be raised or lowered, and `maxMajorJump` may be disabled by
setting it to `null`, but the `limits.*` bounds MUST NOT be disabled: they are the parser bounds of §18.3, and an
implementation that lets a message opt out of them is not conforming.

| Key                        | Default   | Disableable  | Meaning                                                                                                        |
|----------------------------|-----------|--------------|----------------------------------------------------------------------------------------------------------------|
| `maxMajorJump`             | `1`       | yes (`null`) | Reject an exact `Release-As` raising the major version more than this far above the computed version (`E157`). |
| `limits.unitsPerMessage`   | `64`      | **no**       | Cap on units in one commit message (`E158`).                                                                   |
| `limits.scopeTermsPerUnit` | `256`     | **no**       | Cap on scope terms in one scope-set, a header's or a `Propagate-Scope` or `Propagate-Channel-Scope` footer's (`E158`). |
| `limits.messageBytes`      | `1048576` | **no**       | Cap on message length (`E158`).                                                                                |

**Null until configured.** These are inert as shipped. Nothing about a default run consults them, and no diagnostic can
originate from them until an operator sets a value.

| Key                     | Default | Meaning                                                                                      |
|-------------------------|---------|----------------------------------------------------------------------------------------------|
| `maxPackagesPerRun`     | `null`  | Refuse or gate a run releasing more than this many packages (§18.2).                         |
| `maxMajorsPerRun`       | `null`  | Refuse or gate a run applying more than this many `major` bumps.                             |
| `maxChannelMovesPerRun` | `null`  | Refuse or gate a run moving more than this many packages between channels (§18.2).           |
| `requireCodeownerFor`   | `[]`    | Directives (`cancel`, `Release-As`, `Edits`, `Deletes`, `^^`, `++`, `>` transitions) needing CODEOWNER approval. |

`maxMajorJump` sits in the first group deliberately, and it is the row most often misread: a fresh repository that
writes `Release-As: 5.0.0` against a computed `1.5.0` gets `E157` with no configuration involved. It is a default, not
an opt-in.

### 14.2 What configuration may not change

Configuration MUST NOT be able to change:

* the meaning of `cancel`, or the ancestor-or-self rule (§10.3);
* the correction rules of §7.4: the strict-ancestor reach, scope containment, the voiding of a discarded correction,
  and the confinement of corrections to undischarged work;
* the `max()` combination rule (§9.1);
* the requirement that tags are the sole state store (§12.4);
* the non-suppressibility of `W155`, `W156`, `W172`, `W193`, `W194`, `W202`, `W208`, and `W209` (§17.1);
* determinism, idempotency, or convergence (§17.2);
* the admission rule of §13.4a, that a dependent's eligibility is tested against the **dependent's** window;
* that a propagated channel never graduates a dependent unless written as a transition naming the train (§9.3);
* that a transition matches against the dependent's **baseline** channel, not against a value computed in the same run;
* that both propagation axes default to depth `0`, and that neither bounds the other (§8.3, §8.3a);
* the requirement that dependencies are published before their dependents (§19.2);
* the requirement that a tag is written only after that package's publish succeeds (§19.1);
* the requirement that **every** released package is tagged, whatever its publish target (§13.10a);
* the fact that every workspace package is a release unit (§13.10a).
* repository-qualified revision identity, the ban on timestamp baseline inference, and source-first durable recording
  in the polyrepository profile (§27).
* worker authority restrictions, complete locking before distributed planning, exact input/output identity and
  exclusion of transport state from native release history in the distributed execution profile (§28).

These are the guarantees the format rests on. A tool that makes any of them configurable is not conforming, however it
is labelled.

---

## 15. Edge cases

Normative resolutions. Each is testable.

Note on numbering: this section and Appendix B use **separate** numbering spaces that both begin at 1. Throughout the
document, a bare `#n` refers to an edge case in this section; a conformance test is always written as "vector *n*".

### 15.1 Message and grammar

| #   | Case                                                | Resolution                                                                                                  |
|-----|-----------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| 1   | Message is empty or whitespace only                 | `E002`, no units.                                                                                           |
| 2   | First line is not a valid header                    | `E100`. The commit contributes nothing; the engine MUST continue with other commits.                        |
| 3   | `---` as the first line                             | Leading empty unit discarded, `W001`.                                                                       |
| 4   | `---` inside a fenced code block                    | Treated as a separator. Escape as `\---`, or configure `separator`.                                         |
| 5   | `\---` in a body                                    | Literal `---` in the rendered body.                                                                         |
| 6   | Windows line endings                                | Normalised in §4.1; MUST NOT affect parsing.                                                                |
| 7   | Trailing whitespace after a separator (`--- `)      | Stripped by §4.1, still a separator.                                                                        |
| 8   | Leading whitespace before a separator (` ---`)      | Not a separator; body text.                                                                                 |
| 9   | Unbalanced parenthesis `feat(api: x`                | `E103`.                                                                                                     |
| 10  | Colon inside scope `feat(a:b): x`                   | `E103`; `:` is not a legal scope-term character, so the scanner reaches it before the closing parenthesis. |
| 11  | No space after colon (`feat:x`)                     | `E120`. Lenient mode accepts with `W121`.                                                                   |
| 12  | Two spaces after colon                              | `E120`.                                                                                                     |
| 13  | Header longer than the terminal likes               | No limit enforced beyond `W120` on the description.                                                         |
| 14  | Emoji / non-ASCII in description                    | Legal. Length counted in Unicode scalar values.                                                             |
| 15  | Non-ASCII in a package name                         | Legal if the manifest says so; compared byte-for-byte.                                                      |
| 16  | Unit with header only, no body                      | Legal.                                                                                                      |
| 17  | Body that looks like footers but is mid-message     | Only the **last** paragraph is considered for footers.                                                      |
| 18  | Footer block where one line is not footer-shaped    | The whole paragraph is body; `W151` since this is usually a typo.                                           |
| 19  | `Breaking change: x`                                | **Not** breaking, and not even a footer; the generic key loop halts at the space. `W155`.                  |
| 19a | `breaking-change: x`                                | A valid footer with an unrecognised key. **Not** breaking. `W155`.                                          |
| 19b | `BREAKING-CHANGE: x`                                | Accepted, breaking, per base spec.                                                                          |
| 19c | `BREAKING CHANGE` as the header line                | `E100` with a dedicated message (§5.1).                                                                     |
| 19d | `BREAKING CHANGE:` mid-body, footer block elsewhere | Body text, no effect, `W156`.                                                                               |
| 19e | `BREAKING CHANGE: ` with an empty value             | Breaking, `W157`.                                                                                           |
| 19f | `!` and a `BREAKING CHANGE` footer on one unit      | Legal, not redundant; the footer carries prose the marker cannot. One major bump.                          |
| 20  | `BREAKING CHANGE` in unit 2 of a multi-unit message | Binds to unit 2 only (vector 29).                                                                           |

### 15.2 Scopes

| #   | Case                                              | Resolution                                                         |
|-----|---------------------------------------------------|--------------------------------------------------------------------|
| 21  | Explicit scope names a nonexistent package        | `E130`.                                                            |
| 22  | Exclusion names a nonexistent package             | `W130`, ignored.                                                   |
| 23  | Scope-set resolves to zero packages               | Unit is inert, `W131`.                                             |
| 24  | Same package included and excluded (`(api,-api)`) | Excluded. Excludes always win, `W133`.                             |
| 25  | Glob matches nothing                              | `W134`, inert contribution.                                        |
| 26  | Commit changes only root files, no scope given    | Derived set empty → inert (`W131`), unless `rootPathMap` applies.  |
| 27  | Nested packages (`ui` and `ui/theme`)             | Longest prefix wins; only `ui/theme` is derived for its files.     |
| 28  | Merge commit, no scope                            | Diff against first parent. Empty diff → empty set.                 |
| 29  | Commit renames a file across packages             | Both source and destination packages are derived.                  |
| 30  | Package deleted between the commit and `HEAD`     | Not in the graph; explicit scope → `E130`; derived → not produced. |
| 31  | Multi-unit commit with only some units scoped     | `W132`; unscoped units use the derived set.                        |
| 32a | Package literally named `*` or `.`                | Unaddressable; documented limitation (§5.2).                       |
| 33  | `(*)` in a workspace of 400 packages              | Legal; releases everything with a bump.                            |

### 15.3 Propagation

| #   | Case                                                                        | Resolution                                                                                                                                                                             |
|-----|-----------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 34  | Dependency cycle `A → B → A`                                                | Cannot arise: the graph is acyclic by §13.1, and such a workspace is rejected with `E200` before planning.                                                                             |
| 35  | Package reachable at depth 1 and depth 3                                    | Treated as depth 1, so `^` reaches it.                                                                                                                                                 |
| 36  | Dependent has its own `feat`, receives propagated `patch`                   | `max` → `minor`. Direct wins.                                                                                                                                                          |
| 37  | Dependent has its own `fix`, receives propagated `major`                    | `max` → `major`.                                                                                                                                                                       |
| 38  | `^none+*` or `^^none`                                                       | No propagation; `W152` for redundancy. `^^none` is legal: "all levels" of nothing is nothing.                                                                                          |
| 38g | `^minor+0`                                                                  | No propagation; `W201` alone; a bump value was supplied and the depth discards it (§8.3b). Not `W152`.                                                                                |
| 38a | `^^` on a unit whose type maps to `none`                                    | No **bump** propagation; §9.2 phase 3 skips units with no bump, and depth is irrelevant. The channel axis is unaffected and still runs (§7.2).                                        |
| 38b | A unit with no propagation directive at all                                 | Releases only its own packages. Both depths are `0` by default (§8.3, §8.3a); no dependent is touched.                                                                                 |
| 38d | `++0`, `%%none`, or `%%none++*`                                             | No channel propagation; `W152` for redundancy.                                                                                                                                         |
| 38h | `%%beta++0`                                                                 | No channel propagation; `W201` alone; a channel value was supplied and the depth discards it (§8.3b). Not `W152`. Exactly mirrors #38g.                                               |
| 38e | `++*` with no `^`, `^^` or `+N`                                             | Legal. The channel reaches the whole closure and no bump does; dependents that change channel are released as channel-only (`W202`).                                                   |
| 38f | `^^minor++1`                                                                | Legal and common: bump the whole closure, move only the direct consumers onto the origin's channel. The axes do not bound one another (§5.3).                                          |
| 38c | `^` on a unit whose scope resolves to a package with no dependents          | Legal and inert for propagation; the unit still releases its own packages.                                                                                                             |
| 39  | Propagation reaches a package on a private registry                         | Released, tagged, and published to that registry like any other (§13.10a); still propagates onward.                                                                                    |
| 39b | Propagation reaches a package whose target is `none`                        | Released and tagged normally; no artefact is uploaded. It converges like any other package.                                                                                            |
| 39c | A package is omitted from the workspace to avoid releasing it               | Its dependents are stranded at every depth, or re-pointed and reached too shallowly, and its files are absorbed by the enclosing package (§13.10a).                                    |
| 40  | Propagation reaches a package with no baseline                              | Gets `initialVersion` (§12.5).                                                                                                                                                         |
| 41  | Two units propagate different bumps to one package                          | `max` of the two. No conflict.                                                                                                                                                         |
| 42  | `Propagate-Scope` excludes everything                                       | No propagation; `W135`.                                                                                                                                                                |
| 43  | Dev-only dependent                                                          | Not traversed (§8.4); `devDependencies` is not in `propagation.kinds`.                                                                                                                 |
| 44  | Optional dependent                                                          | Traversed by default.                                                                                                                                                                  |
| 45  | Depth exceeds graph diameter                                                | Equivalent to `all`.                                                                                                                                                                   |
| 46  | A held package would receive a propagated bump                              | Recorded, not released (§13.7). It is recomputed when the hold lifts.                                                                                                                  |
| 46a | A held package is a dependency of an unheld one                             | The held package is removed as a propagation source on **both** axes; the dependent is neither bumped nor re-channelled on its behalf. It still releases if it has changes of its own. |
| 46b | A held package would receive a propagated channel                           | Recorded, not released, exactly as for a bump (§13.7). Re-proposed on the run that lifts the hold, against the then-current baseline.                                                  |
| 46c | Origin releases on `beta`; a stable dependent is reached by `^`             | Bump suppressed, `W208` (§9.3a). The dependent cannot resolve a prerelease, so the bump would be a release with no content.                                                            |
| 46d | Origin releases on `beta`; a dependent **already** on `beta` reached by `^` | Bumped normally. Resolvability, not channel equality with the baseline, is the test.                                                                                                   |
| 46e | Origin releases on `stable`; a dependent on `beta` reached by `^`           | Bumped normally; a stable release is resolvable by everyone (§9.3a).                                                                                                                  |
| 46f | A unit whose sources sit on two different channels                          | The bump propagates if **any** source releases something the target can resolve (§9.3a).                                                                                               |

### 15.4 Cancel

| #  | Case                                                     | Resolution                                                                                                          |
|----|----------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| 47 | `cancel` with nothing pending                            | No-op, `W170`.                                                                                                      |
| 48 | `cancel` on a branch not merged into `HEAD`              | No effect.                                                                                                          |
| 49 | `cancel` and a `feat` in the *same* commit, cancel first | Ancestor-or-**self**; the `feat` is discarded, regardless of unit order. Same-commit units are all at the barrier. |
| 50 | Two `cancel`s in unrelated branches, both merged         | Union of discarded sets.                                                                                            |
| 51 | `cancel(*)` followed by a `feat`                         | Only the `feat` remains.                                                                                            |
| 52 | `cancel` after prereleases were published                | Published tags stand; the eventual stable version ignores the cancelled units; `W171`.                              |
| 53 | `cancel` with `!`                                        | `E170`.                                                                                                             |
| 54 | `cancel` with `^minor` or `Channel:`                     | `E171`.                                                                                                             |
| 55 | `cancel` with no scope in an empty commit                | Derived set empty → inert `W131`. Always write `cancel(*)`.                                                         |
| 56 | `cancel` commit later rebased                            | Its ancestor set changes, so its effect changes. Intended.                                                          |
| 57 | `cancel` on a package with no tags at all                | Discards pending units; package remains unreleased.                                                                 |

### 15.5 Prereleases and versions

| #   | Case                                                                   | Resolution                                                                                                                                                |
|-----|------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| 58  | Two channels in one window                                             | Newest commit wins, then last unit; `W186`.                                                                                                               |
| 59  | Breaking change lands mid-beta                                         | `target` recomputed from stable baseline; counter resets to `0` (§11.4).                                                                                  |
| 60  | Channel switch `beta` → `rc`                                           | Counter resets to `0`.                                                                                                                                    |
| 60a | `^%%beta` on a unit whose own packages are stable                      | Origin releases stable; direct consumers enter the `beta` line and take the propagated patch. Legal; the stable baseline is untouched (§9.3).            |
| 60b | `%%stable` reaching a dependent whose baseline is a prerelease         | **Not** graduated. The dependent keeps its channel; `W200`. Only a direct directive or a transition graduates (§11.5).                                    |
| 60c | `%%stable` reaching a dependent already on stable                      | No-op; `W199` for the redundant directive.                                                                                                                |
| 60d | `%%` with no value, e.g. `feat(core)%%: x`                             | `E111`. Unlike `^^`, a bare `%%` has no meaning (§5.3).                                                                                                   |
| 60e | `%%` twice, or `%%%`                                                   | `E110`.                                                                                                                                                   |
| 60f | `%beta%%rc`                                                            | Legal: origin on `beta`, dependents on `rc`. `%` and `%%` are distinct sigils (§5.3).                                                                     |
| 60g | `%%Beta` / `%%latest`                                                  | `E181` / `E180`; propagated channel names obey §11.2 exactly as direct ones do.                                                                          |
| 60h | `%%beta` with no `++N` on the unit                                     | Channel depth `1`; the sigil supplies it (§8.3a). The direct consumers move; no bump moves unless a caret says so.                                       |
| 60i | `%%rc` and `%%beta` reaching one dependent from two units              | Newest commit wins, then last unit; `W160`.                                                                                                               |
| 60j | `%%beta` where the dependent has its own `%stable` in the window       | The direct directive wins (§13.8); the propagated channel is discarded.                                                                                   |
| 60k | `++` with no value, e.g. `feat(core)++: x`                             | `E111`. A depth sigil with no number is not a depth.                                                                                                      |
| 60l | `++` twice, or `+++`                                                   | `E110`.                                                                                                                                                   |
| 60m | `feat(core)^%beta: x`, consumers on stable                             | **`core` alone releases.** The caret reaches them; §9.3a suppresses each with `W208`. Add `++1` to take them along.                                       |
| 60n | `feat(core)%%beta: x`, consumers on stable, no caret                   | Consumers move onto `beta` with no bump: channel-only releases (`W202`), versioned by the channel-entry patch (`W204`).                                   |
| 60o | `%beta>stable` on a package that is on `rc`                            | No match, nothing proposed, no `W185`. `W206` if no package in the unit's scope matches.                                                                  |
| 60p | `%*>stable` on a mixed set                                             | Every package on some prerelease graduates; those already on stable are untouched.                                                                        |
| 60q | `%%beta>stable++*` reaching a dependent on `beta`                      | **Graduated.** A transition is the deliberate, reviewable exception to `W200` (§9.3).                                                                     |
| 60r | The same, run again after it succeeded                                 | Nothing. The dependent's baseline is now stable, so it no longer matches `<from>` (§13.7c G7).                                                            |
| 60s | `%beta>beta`, or `%%rc>rc`                                             | `W207`, inert.                                                                                                                                            |
| 60t | `%%stable>beta++1`                                                     | Direct consumers currently on stable enter the `beta` line; consumers already on a prerelease are untouched.                                              |
| 60u | `%%beta>inherit`, `%%none>beta`, `%%beta>*`                            | `E111`; `inherit` and `none` are values, not channels, and `*` is legal only as a `<from>` (§11.2).                                                      |
| 60v | `%%a>b>c`                                                              | `E111`; a transition has exactly one `>`.                                                                                                                |
| 60w | Graduating a consumer whose provider stays on `beta`                   | Permitted and reported: the stable consumer's range admits a prerelease, `W203` (§9.4). Graduate the provider too.                                        |
| 61  | Graduation with no pending bumps                                       | Publishes the accumulated `target`. A `target` equal to the baseline's core is the ordinary case; one lower than it is `E185`, after the channel-entry patch where the window carries no bump at all (§11.5). |
| 62  | Graduating a package already stable                                    | `W185` no-op, or an ordinary release if bumps are pending.                                                                                                |
| 63  | Prerelease with no stable baseline ever                                | Virtual stable baseline `0.0.0` → `target` is `initialVersion`; e.g. `0.1.0-beta.0`.                                                                      |
| 63a | Channel-only entry from a clean stable baseline `1.2.0`                | `1.2.1-beta.0`; the channel-entry patch, `W204` (§11.4). Without it the computed `1.2.0-beta.0` would rank **below** the baseline.                       |
| 63b | Channel-only entry where the window already carries a bump             | Ordinary §11.4; no channel-entry patch and no `W204`, because the computed version already exceeds the baseline.                                          |
| 63c | Channel-only entry that is still not greater after the patch           | `E195`. The patch is one step and never loops, so a hand-edited baseline ahead of the stable line still fails loudly (vectors 46–47).                     |
| 64  | Hand-written tag `pkg@1.3.0-beta10`                                    | Parsed as a prerelease with a single alphanumeric identifier; a computed `-beta.0` would compare **lower**. `E182`; refuse rather than regress.          |
| 65  | Tag with build metadata `pkg@1.2.3+abc`                                | Accepted; metadata ignored and not carried forward.                                                                                                       |
| 66  | Two tags with the same version on different commits                    | `E191`.                                                                                                                                                   |
| 67  | Tag `pkg@1.2.3` where `pkg` is unknown                                 | Ignored silently.                                                                                                                                         |
| 68  | Tag `pkg@not-semver`                                                   | Ignored, `W190`.                                                                                                                                          |
| 69  | `@acme/ui@1.2.3` split at first `@`                                    | Conformance failure; MUST split at last `@`.                                                                                                              |
| 70  | Manifest version disagrees with baseline                               | `W192`; tags win.                                                                                                                                         |
| 71  | `Release-As` lower than baseline                                       | `E153`.                                                                                                                                                   |
| 72  | `Release-As` exact on a multi-package scope                            | `E154`.                                                                                                                                                   |
| 73  | Two conflicting package-level `Release-As` for one package             | Newest commit, then last unit; `W153`.                                                                                                                    |
| 73a | `Release-As: none`, never lifted                                       | The package is held indefinitely. `W154` on every run, carrying the withheld version. This is intended; a hold is a fact in history, not a per-run flag. |
| 73b | Exact `Release-As` lower than the computed version                     | `E156` (lenient: `W159`). Guards against lifting a hold at a stale number after a breaking change landed.                                                 |
| 73c | `Release-As: none` and `auto` (or an exact version) in the same commit | Last unit wins, `W153`.                                                                                                                                   |
| 73d | `none` → `auto` → `none` across three commits                          | Held. The newest directive wins; no replay of the sequence is needed.                                                                                     |
| 73e | `Release-As: auto` with no active hold                                 | No-op, `W158`.                                                                                                                                            |
| 73f | `auto` in a commit that is an ancestor of a later `none`               | Held; the `none` is newer.                                                                                                                               |
| 73g | A `cancel` whose barrier covers the commit carrying a hold             | The hold is discarded along with everything else before the barrier; the package resumes, with an empty ledger.                                           |
| 73h | `Release-As: none` on a unit that also carries `^minor`                | Legal but pointless while held; propagation is suppressed at the source (§13.7).                                                                          |
| 73i | `Release-As: patch` / `minor` / `major`                                | `E151`; `Release-As` has no bump form (§8.6). Change the type, or configure `types`.                                                                     |
| 73j | Held package whose channel directive changes                           | Not released; the channel directive is re-evaluated when the hold lifts.                                                                                  |
| 73l | Held package receives ordinary `feat`/`fix` commits afterwards         | Still held. They accumulate; only `auto`, an exact version, or a `cancel` lifts it (§8.6.1).                                                              |
| 73m | Held package receives a `feat!` after the hold                         | Still held. The withheld version reported by `W154` rises to the new `max()`, but nothing publishes.                                                      |
| 73k | Hold on a package that is also `cancel`-ed in a *later* commit         | Cancel discards the units; the hold survives (it is newer than nothing) only if its own commit is after the barrier.                                      |
| 74  | `0.4.1` with a breaking change, `preserveMajorZero: true`              | `0.5.0`.                                                                                                                                                  |
| 75  | Untagged package with a breaking change                                | `initialVersion` (`0.1.0`), not `1.0.0`.                                                                                                                  |
| 76  | Shallow clone missing tags or ancestry                                 | The engine MUST detect a shallow repository and fail with `E196` rather than compute from partial history.                                                |
| 76a | Complete history, but the checkout lacks release records the authoritative store holds (a clone made without tags, or made before another run recorded) | A write-capable run MUST detect this under its lock and fail with `E196` before any build or publication (§13.2). Planning the package as unreleased republishes a released version. |
| 77  | Squash-merged PR containing many units                                 | Parsed as a multi-unit message; the primary reason the separator exists.                                                                                 |
| 78  | Commit reachable by two merge paths                                    | Counted once (§13.3).                                                                                                                                     |
| 79  | Empty commit (`--allow-empty`) carrying only directives                | Fully supported; this is the normal shape of a `release` or `cancel` commit.                                                                              |
| 80  | Two packages, one on `beta` and one stable, in one commit              | Independent; channel is per package.                                                                                                                      |
| 81  | `feat!` and a `revert` of it in one window                             | Still `major`; `max()` of `major` and `patch`. Pair with `cancel` to drop the signal (§7.3).                                                             |
| 82  | Engine run twice with no new commits                                   | Identical plan (§17.2).                                                                                                                                   |
| 83  | Engine run again after a successful publish                            | Empty plan; new baseline tags emptied `Wfresh`, including on a prerelease train.                                                                                                    |
| 84  | Run fails after publishing 3 of 7 packages                             | Under the §13.7c retry invariant, the 3 are tagged and re-running publishes the remaining 4 at the same versions (§13.7c G3, §19.3).                                 |
| 85  | Bot commit rewriting dependent manifests                               | MUST use a type mapping to `none`, or be excluded from the window, or the release loops (§19.5).                                                          |
| 86  | Message exceeding `limits.*`                                           | `E158`, message-scoped; never a crash (§18.3).                                                                                                            |

### 15.6 Partial failure, catch-up, and publishing

| #   | Case                                                                     | Resolution                                                                                                                                                                                          |
|-----|--------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 87  | Provider published, consumer's publish failed                            | Consumer is released on the next run at the **same** version (§13.7c G3), marked `W193`. The case §13.7a exists for.                                                                                |
| 88a | Scheduled staleness audit over a workspace that has never run the engine | Reports every package behind a dependency as `W195` (§13.7b). Reporting only; it never blocks and never releases.                                                                                  |
| 88  | The same, five runs later, with no new commits                           | Still released at the same version while the §13.7c retry invariant holds. Admission depends on the consumer's unchanged `Wfresh` (G2).                                                                                   |
| 89  | Provider published, consumer succeeded, run re-run                       | Empty plan. The contribution is discharged once in the tag ledger (G4).                                                                                                                                       |
| 90  | Consumer released in the interim for its own `feat`                      | No catch-up: its window no longer contains the commit, and its own release already picked up the dependency. Its range for that dependency is reconciled at publish time and reports `W197` (§9.4). |
| 91  | Mid-chain failure under `^^` (`core`→`ui`→`theme`, `ui` fails)           | `theme` is **blocked**, not published (`W194`). On resume, `ui` then `theme`, both at their originally planned versions.                                                                            |
| 92  | The same, but with `+1` instead of `^^`                                  | `theme` was never in the plan and never enters it. Catch-up cannot widen depth (G5).                                                                                                                |
| 93  | Consumer would be published before its provider in one run               | Impossible: §19.2 orders dependencies first. An implementation that emits this order fails conformance (`E197`).                                                                                    |
| 94  | Publish succeeded but the tag write failed                               | Next run re-attempts, registry reports the version exists; identity verified → tag adopted, `W196` (§19.4). Unverifiable → `E198`, abort.                                                           |
| 95  | `cancel(consumer)` after the provider released                           | The pending propagated contribution is discarded (§13.5a); the consumer is not released.                                                                                                            |
| 96  | `cancel(provider)` after the provider released                           | No-op for the provider (`W170`); the consumer still catches up (§13.4a). Cancel never reaches a published release.                                                                                  |
| 97  | Consumer held by `Release-As: none` when its provider releases           | Recorded, not released (`W154`). Catches up on the run that lifts the hold, at the then-current `max()`.                                                                                            |
| 98  | Private package in the plan, run fully successful                        | Plan is empty. Private packages are tagged like any other (§13.10a), so their ledger entries discharge; §19.6 still performs its post-success re-plan.                                                                    |
| 99  | Dependency cycle over runtime edges, anywhere in the workspace           | `E200`, repository-scoped. The run aborts before planning, whether or not the cycle is in this run's plan (§13.1).                                                                                  |
| 99a | Cycle existing only through `devDependencies`                            | Not a cycle for this purpose; those edges are in neither `propagation.kinds` nor `publish.orderKinds` (§13.1). Common and legitimate.                                                              |
| 99b | A cycle among packages none of which have a bump this run                | Still `E200`. Acyclicity is a property of the workspace, checked at load, not of the plan.                                                                                                          |
| 99c | A cycle is introduced by the commit being released                       | `E200`; nothing is published. The graph is read at `HEAD` (§13.1), so the offending commit is the one that must be fixed.                                                                           |
| 100 | Optional dependency fails to publish                                     | Dependents are **not** blocked; an optional dependency is installable in its absence. Ordering still respects the edge (`publish.blockingKinds`, §19.3).                                           |
| 101 | Every package in the plan fails to publish                               | Not a partial failure. The run made no progress; the engine MUST fail loudly rather than report a resumable state (§13.7c G6).                                                                      |
| 102 | Registry is idempotent and silently accepts a republish                  | Still MUST NOT be relied upon. Tags, not the registry, decide what is released (§12.4); the tag-after-publish rule (§19.1) is what prevents the loop.                                               |
| 103 | Ordering constraint runs through a package that is not in the plan       | Still binding. The order is computed over the full graph and filtered afterwards; inducing it on the plan first is a conformance failure (§19.2).                                                   |
| 104 | Failed package reachable only through a package with no bump this run    | The dependent is still blocked (`W194`). The blocking closure traverses packages that are not in the plan (§19.3).                                                                                  |
| 105 | `cancel(<consumer>)` lands after the run that stranded it                | The pending propagated contribution is discarded (§13.5a); the consumer is not released. Sibling consumers are unaffected.                                                                          |
| 106 | `Release-As: none` on a stranded consumer, later `auto`                  | Held meanwhile (`W154`, carrying the withheld version); on `auto` it releases at the `max()` of the catch-up and anything accumulated since.                                                        |
| 107 | `cancel(<provider>)` after the provider published                        | No-op for the provider (`W170`); consumers still catch up. Suppression reaches only undischarged work (§13.4a).                                                                                     |
| 108 | `Release-As: none` on a provider that already published                  | Identical: the hold governs the provider's future releases only, and consumers still catch up (§13.4a). Treating this differently from #107 is a conformance failure.                               |
| 109 | A breaking change lands on a stranded consumer after the failure         | It releases at `max(major, propagated)` = `major`. G3 pins the version only while `HEAD` is unchanged (§13.7c).                                                                                     |
| 110 | `cancel(*)` after a partial failure                                      | Every pending contribution is discarded, propagated ones included; nothing releases. The changelog is unrecoverable (§8.6.2).                                                                       |

### 15.7 Corrections and reverted changelogs

| #   | Case                                                                     | Resolution                                                                                                                                                                                            |
|-----|--------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 111 | `Edits: <sha>` where every affected package has released the target      | No-op, `W209` (non-suppressible). The carrying unit still contributes its own record normally (§7.4.2).                                                                                               |
| 112 | Correction unit with no scope-set, in an empty commit                    | Takes the union of its targets' resolved package sets; the file-derived fallback of §6.2 does not apply (§7.4.2).                                                                                     |
| 113 | Correction whose scope-set reaches outside the target's resolved set     | `E213`; the unit contributes nothing. A correction may narrow a record, never widen it (§7.4.2).                                                                                                      |
| 114 | Bare `<sha>` naming a multi-unit commit                                  | `E211`. Name the unit: `<sha>#2`.                                                                                                                                                                     |
| 115 | `chore(core): x` + `Deletes: *`                                          | Every pending record for `core` is discarded; `chore` maps to `none`, so nothing replaces them. The pure-deletion form.                                                                               |
| 116 | `Deletes: T`, then a newer commit `Edits: T`                             | The restatement is in force: the newest correction of a target wins, and the superseded delete reports `W210` (§7.4.2).                                                                               |
| 117 | Correction naming its own commit, or a descendant                        | `E210`. A correction reaches proper ancestors only, which is what lets one commit delete old records and restate them together.                                                                        |
| 118 | Correction naming a `cancel`, `release`, or `rollback` unit                           | `E212`.                                                                                                                                                                                               |
| 119 | `cancel` barrier covering a correction's commit                          | The restatement record is discarded like any pending unit; a later correction of a cancelled target has nothing to act on and reports `W209` (§13.4b).                                                |
| 120 | `Edits: T` where the carrying unit equals `T` in type, `!`, and text     | Applied, and `W211` reports that it changes nothing.                                                                                                                                                  |
| 121 | Two `Edits` footers in one unit                                          | Both targets are discarded and both are restated by the one carrying record (§7.4.1).                                                                                                                 |
| 122 | `Edits: *`                                                               | The scope's whole pending changeset collapses into the single carrying record.                                                                                                                        |
| 123 | `Deletes: T` where `T` propagated to dependents                          | The propagated contributions fall with the record: dependents whose windows still hold `T` are no longer reached by it (§13.4b).                                                                       |
| 124 | `feat!` and a `revert` of it in one window, `Reverts` well-formed        | Bump stays `major` (§7.3); both changelog entries are suppressed, `W212`.                                                                                                                             |
| 125 | `Reverts: not-a-sha` / `Reverts:` naming an unreachable commit           | `W214` at parse / `W213` from the engine; the footer is informational, the revert releases and bumps normally (§7.3).                                                                                 |
| 126 | `chore(core): x` + `Deletes: T` where `T` is scoped `(*)`                | Partial deletion: `T`'s record is discarded for `core` alone and stands for every other package (§7.4.2).                                                                                             |
| 127 | `fix(core): y` + `Edits: T` where `T` is scoped `(*)`                    | Partial restatement: `core`'s ledger carries the `fix`, the other packages keep `T`'s original record.                                                                                                |
| 128 | `Deletes: U` where `U` carries `Edits: T`                                | `U` is void (`W215`): its restatement record is discarded and its discard of `T` never applies, so `T`'s record returns. The undo of a mistaken correction (§7.4.2).                                   |
| 129 | `Deletes: X` where `X` carries `Deletes: T`                              | `X` is void (`W215`), so `T`'s record returns. Chains of any depth resolve newest first; ancestry makes cycles impossible.                                                                             |
| 130 | `Edits: U` where `U` is itself a correction                              | `U`'s record is restated and `U` is void, so `U`'s target returns. To restate the original again, target the original: a newer `Edits` of the same target supersedes directly (`W210`).                |
| 131 | `Deletes: R` where `R` is a revert unit suppressing entries via `W212`   | `R`'s record is discarded and its changelog suppression is void: the reverted entry returns (§7.4.2, §7.3).                                                                                            |
| 132 | Partial delete of a correction's own record, then the correction's turn  | Void only for the deleted packages (`W215` per package); the correction still applies for the rest (§13.4b).                                                                                           |
| 133 | `P@1.1.0-beta.1` at `HEAD` already carries commit `C`; rerun with no new commits | `C` remains in train `W(P)` for aggregate target calculation but is absent from `Wfresh(P)` and cannot re-admit `P` (G4/G6). |
| 134 | A cancelled source is nevertheless published before its consumer resumes | Its suppression may discharge and expose the consumer as a finite follow-up; this is permitted widening outside H2 and requires renewed review (G5, §18.1). |
| 135 | `staleSources(D)` reaches a provider on `beta` while `D` is on another prerelease line | No stale row: §9.3a resolvability rejects it, matching downward propagation. |
| 136 | A bump is rejected by §9.3a but the same unit carries an admissible propagated channel | The channel still applies; reversing this implication is forbidden (G8). |
| 137 | `Propagate-Channel: inherit`; the source baseline graduates between retries | The inherited channel and planned version may change; G3 applies only while the relevant baseline channels remain fixed. |
| 138 | Two bucketed units share target `cli`, but `cli` is a source of one unit only | Admit `cli` only for the other unit after a per-unit self-source check; “reached by some edge” alone over-admits. |

### 15.8 Shared-version groups

Every row assumes an engine that offers groups (§13.9a). `d` is the group's shared depth.

| #   | Case                                                                      | Resolution                                                                                                                                                                  |
|-----|---------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 139 | Retry at a fixed `HEAD` on a shared train, `counter: independent`         | Each unpublished member releases at the version the failed run planned for it; the members that published release nothing. `G3` per member.                                  |
| 140 | The same under `counter: fixed`                                           | The group's counter advances: every member releases at the next prerelease, the published ones as rides. `G3` covers the core, not the counter (§13.9a).                      |
| 141 | A member's own window justifies a core below the group's line             | Its `target` is raised to the line's core (§11.4, §13.9a), so a rider continues the train rather than raising `E195`.                                                        |
| 142 | A graduation whose second leg failed, retried                             | The members that never carried the feature setting the train's core graduate at that core, not at what their own windows compute: the floor applies before `E185` (§11.5).    |
| 143 | A member's baseline is above the group's line and nothing explains it      | Still `E185` or `E195`. The floor never reaches past the line, so a hand-edited tag fails exactly as before.                                                                  |
| 144 | A member resting on `stable` while the group rides a prerelease train      | It proposes nothing. The train continues; the group does not graduate because one member never joined it.                                                                    |
| 145 | A member left on a prerelease after the rest graduated                     | It proposes nothing. The graduated group stays graduated, and the member is released at the group's published version as a ride.                                              |
| 146 | Fresh work inside a shared train, `counter: independent`                   | Only the members with a cause of their own release, each continuing its own counter.                                                                                         |
| 147 | Work moving the shared prefix onto the next train                          | The group engages under every axis: every member takes the new core, and each counter restarts at `0`.                                                                       |
| 148 | A graduation naming one member, `channels: independent`                    | Only that member graduates. The others stay on the line they are on, and no channel conflict is reported.                                                                    |
| 149 | A member on `stable` riding a **prerelease** group version                 | It follows the group onto that prerelease channel: a ride is never the first stable publication of a core the group holds only as a prerelease (§13.9a).                      |
| 150 | A member on a prerelease riding a **stable** group version                 | It takes the new core on its own channel at counter `0`. A ride never graduates a member.                                                                                    |
| 151 | `counter: fixed` declared beside `channels: independent`                   | A configuration error. One counter counts one train, and a train runs on one channel (§13.9a).                                                                               |
| 152 | A sharing axis declared for packages that share no version prefix          | A configuration error: there is nothing for the axis to be an axis of.                                                                                                       |
| 153 | A quiet group whose members all hold the shared prefix                     | Nothing releases where every member is aligned. Under `counter: independent` a member behind the group's counter is aligned and is not caught up; under `counter: fixed` it is a laggard and rides to the line (vector 153). |
| 154 | A member on `stable` releases work of its own while the line is a prerelease | It joins the shared prefix on the line's channel at its own counter. The floor is withheld from it (it must not publish that core as `stable`) and staying below the prefix is excused by no axis (§13.9a). |

---

## 16. Diagnostics registry

CCME 3 adds the following operational diagnostics. External-adapter and rollback codes are specification requirements
for future engines, not claims that those optional facilities are implemented. `E300`–`E309` fail the combined run;
preflight failures prevent artifact mutation, while execution failures retain prior progress. `E320`–`E329` have the
scopes and recovery rules specified in §25. `E330`–`E339` apply only when the optional polyrepository profile is active
and have the recovery rules in §27. Existing message diagnostics retain their scope.

| Code | Condition |
| --- | --- |
| `E300` | Pending rollback execution is not explicitly enabled. |
| `E301` | Invalid rollback syntax, target version or incompatible directive. |
| `E302` | Missing, ambiguous, unreachable or later-introduced target release identity. |
| `E303` | Missing, disabled or unsupported rollback handler. |
| `E304` | Incomplete consumer inventory, unsafe omission, cyclic withdrawal order or overlapping ordinary release. |
| `E305` | Rollback handler protocol, output, process or timeout failure. |
| `E306` | Ambiguous rollback outcome, unverifiable absence or mismatched artifact identity. |
| `E307` | Rollback intent/completion conflict or durable recording failure. |
| `E308` | Ordinary release or adoption requires a withdrawn artifact identity. |
| `E309` | Invalid rollback cancellation or cancellation after a durable intent exists. |
| `W300` | Historical rollback directive predates explicit activation and cannot execute. |

The VCS adapter diagnostics `E320`–`E329`, `W320` and `W321` are defined in the incorporated
[VCS protocol registry](./VCS-PROTOCOL.md#7-diagnostics). No operational warning may be used to mark incomplete rollback as successful.

| Code | Condition |
| --- | --- |
| `E330` | A source repository is missing, uninitialized, shallow, unpinned, duplicated, or outside the declared workspace, or a relevant planned head, release-tag ref, or pin changes before publication. |
| `E331` | A package or space path crosses repository ownership, a source-local path escapes its source repository, or a package is inside an unlisted nested Git worktree. |
| `E332` | Imported declarations conflict, or a repository override names no exact `.gitmodules` source identity. |
| `E333` | A cross-repository consumer boundary is missing, ambiguous, conflicting, or unreachable, or an applicable control directive's causal snapshot pins a source revision the active source checkout does not contain. |
| `E334` | Semantics require precedence between incomparable source revisions and no applicable control directive resolves it. |
| `E335` | A source release record, source push, or control gitlink checkpoint failed after publication. |
| `E336` | A release lock cannot be acquired or returned, so exclusive release ownership is not cleanly coordinated. |
| `E337` | A configured release branch is absent, or a detached repository needs a branch push without `commit.branch`. |
| `E338` | A linked peer fleet's links do not form a tree: a second route reaches a repository the walk already entered. |
| `E339` | A repository identity cannot be trusted to name one participant: it is missing, reserved, malformed, repeated in a roster, self-naming, or contradicted by the link it was reached through. |
| `W330` | An `external: true` dependency provider is absent, so its edge is inactive for this snapshot. |
| `W331` | The release lock is switched off by an explicit unsafe setting, for every repository the warning names. |
| `W332` | A fleet link is declared by only one of its two repositories, so a release starting at the other end composes a smaller fleet. |
| `W333` | A participant's roster does not name every member of the composed fleet, so a release starting there plans without them. |

Errors (`E`) MUST be reported. Their blast radius depends on the code:

* **Unit-scoped** (`E100`–`E181` except `E158`, and `E210`–`E213`): the offending unit contributes nothing; other
  units in the same commit still apply, and other commits are unaffected.
* **Message-scoped** (`E001`, `E002`, `E158`): the commit contributes nothing.
* **Repository-scoped** (`E182`, `E185`, `E191`, `E195`, `E196`, `E200`): the run cannot produce a correct plan and
  MUST abort. These are integrity failures, not authoring mistakes, and no partial release may be emitted.
* **Run-scoped** (`E197`, `E198`, `E199`, `E300`–`E309`, `E320`–`E329`, `E335`–`E337`): the run's *publication* cannot be completed or trusted. Packages already
  published and tagged before the error remain published and tagged. An error found before package execution aborts
  the run. `E335`–`E337` found on an active package stop that publication or recording path and its dependent work;
  independent packages MAY continue. The run MUST report what completed and exit non-zero. These are recoverable by a
  later run, unlike repository-scoped errors.
* **Polyrepository-scoped** (`E330`–`E334`, `E338`, `E339`): while the optional profile is active, the combined run
  cannot produce a
  trustworthy plan. An initial error MUST abort before package work. When `E330` is instead discovered by the required
  final revalidation after package work has begun, it MUST fail that package before its publish command and block its
  dependent work; independent packages MAY continue. The same checkout evaluated with the profile omitted retains the
  single-repository semantics and diagnostic scope of earlier revisions.

Every code is in exactly one bucket. `E182` and `E185` are repository-scoped although each is discovered while computing
one package's version: neither has an offending unit, since both are properties of a tag that already exists, found during
baseline computation, so "the offending unit contributes nothing" has nothing to name, and neither can be resolved by
anything in the commit log. Like `E191` and `E195`, they are resolved by a human correcting the tag, after which the run
is simply repeated.

Warnings (`W`) never block a release. Commit-lint implementations SHOULD reject a commit at authoring time on any unit-
or message-scoped `E`, and SHOULD additionally reject `W155`, `W156`, and `W172`, which are silent-wrong-answer warnings
rather than style notes. `W193`, `W194`, `W202` and `W208` are release-time, not authoring-time, and are
non-suppressible for the same reason: each reports a release outcome that a reader of the commit log alone cannot
account for. `W193` and `W202` explain a package's *presence* in a plan (a catch-up and a channel-only release both
appear with no commits of their own), while `W194` and `W208` explain an *absence*: a package that was planned and not
attempted, and a dependent a caret reached but could not oblige. `W209` joins them because a correction that did not
take is a silent wrong answer: the author believes a record was rewritten and it was not. The complete
non-suppressible set is therefore `W155`, `W156`, `W172`, `W193`, `W194`, `W202`, `W208`, and `W209` (§14.2,
§17.1 #8).

### Errors

| Code   | Condition                                                                                                                                                                                                 |
|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `E001` | Message is not valid UTF-8.                                                                                                                                                                               |
| `E002` | Message is empty.                                                                                                                                                                                         |
| `E100` | Unit header does not match the grammar.                                                                                                                                                                   |
| `E101` | Type contains uppercase or illegal characters.                                                                                                                                                            |
| `E102` | Whitespace inside a scope-set other than after a comma.                                                                                                                                                   |
| `E103` | Unbalanced or nested parentheses.                                                                                                                                                                         |
| `E104` | Empty scope-set `()`.                                                                                                                                                                                     |
| `E110` | Duplicate inline directive sigil (including a second `%`, a second `+`, `^` with `^^`, a third caret, a second `%%`, a third percent sign, a second `++`, and a third plus).                                   |
| `E111` | Unknown inline directive value, or an empty value after a sigil other than `^` and `^^`. Includes a malformed channel transition: more than one `>`, `inherit` or `none` on either side, `*` as a `<to>`. |
| `E112` | Inline and footer set the same key to different values.                                                                                                                                                   |
| `E113` | `^^` combined with an explicit `+N` where `N` is not `all`.                                                                                                                                               |
| `E120` | Missing or malformed `": "` separator.                                                                                                                                                                    |
| `E121` | Empty description.                                                                                                                                                                                        |
| `E130` | Explicit include names an unknown package.                                                                                                                                                                |
| `E140` | Unknown type under `strictTypes`.                                                                                                                                                                         |
| `E141` | `release` unit with `!`.                                                                                                                                                                                  |
| `E151` | Footer value is not valid for its key; including `Release-As: patch\|minor\|major`, which has no bump form (§8.6).                                                                                       |
| `E153` | `Release-As` version not greater than baseline.                                                                                                                                                           |
| `E154` | Exact `Release-As` on a multi-package scope-set.                                                                                                                                                          |
| `E156` | Exact `Release-As` lower than the computed version (lenient: downgraded to `W159`).                                                                                                                       |
| `E157` | Exact `Release-As` exceeds the computed version by more than `maxMajorJump` majors (§14.1).                                                                                                               |
| `E158` | A `limits.*` cap was exceeded (§14.1). Message-scoped.                                                                                                                                                    |
| `E170` | `cancel` unit with `!`.                                                                                                                                                                                   |
| `E171` | `cancel` unit with inline directives or a §8.1 release-directive footer. Message-level trailers (§4.5) and unknown keys are exempt.                                                                       |
| `E173` | Correction footer (`Edits`, `Deletes`) on a `release` unit, which carries no record that could restate anything (§7.4).                                                                                   |
| `E180` | Reserved channel name `latest`.                                                                                                                                                                           |
| `E181` | Channel name contains uppercase or illegal characters, or is outside `channels.allowed`. Applies to both sides of a transition.                                                                           |
| `E182` | Existing prerelease tag uses a non-numeric counter (§15.5 #64). Repository-scoped; no offending unit.                                                                                                     |
| `E185` | Graduation would not increase the version (§11.5). Repository-scoped; reachable from hand-edited tags or a pinned train.                                                                                               |
| `E191` | Two reachable tags carry the same version for one package on different commits, or the authoritative store records a version on another commit than the checkout a write-capable run plans from (§13.2). |
| `E195` | Computed version not greater than baseline.                                                                                                                                                               |
| `E196` | Repository is shallow or grafted; history is incomplete. Also a write-capable run whose checkout lacks a reachable release record the authoritative store holds (§13.2).                                  |
| `E197` | Publish order violation: a package was published before a workspace dependency also in this run's plan (§19.2). Run-scoped.                                                                               |
| `E198` | The registry already holds this version and its identity could not be verified as this run's artefact (§19.4). Run-scoped.                                                                                |
| `E199` | Fixed-point exhaustion failed: a non-held plan repeated without discharge or has no permitted state-change explanation (§19.6). Run-scoped.                                                                                                       |
| `E200` | The dependency graph contains a cycle over runtime edge kinds (§13.1). Repository-scoped; names the members.                                                                                              |
| `E210` | A correction targets a commit that is unknown, unreachable, or not a proper ancestor of the correction's own commit (§7.4.2).                                                                             |
| `E211` | A correction's unit selector is out of range, or a bare sha names a multi-unit commit (§7.4.1).                                                                                                           |
| `E212` | A correction targets a control unit (§7.4.2).                                                                                                                                                             |
| `E213` | A correction's resolved scope-set is not a subset of a sha target's: it would widen the record to packages the target never claimed (§7.4.2, §13.4b). Narrowing is legal, and is how a record is corrected partially. |

### Warnings

| Code   | Condition                                                                                                                                                                                                                                                                                                 |
|--------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `W001` | Empty unit discarded.                                                                                                                                                                                                                                                                                     |
| `W101` | Type lowercased under lenient mode.                                                                                                                                                                                                                                                                       |
| `W110` | Redundant restatement of a directive: inline and footer setting the same key to the same value, or `^^` combined with `+*`.                                                                                                                                                                               |
| `W112` | Footer overrode inline under lenient mode.                                                                                                                                                                                                                                                                |
| `W120` | Description exceeds `maxDescriptionLength`.                                                                                                                                                                                                                                                               |
| `W121` | Missing space after `": "` accepted under lenient mode (§5.5). Two or more spaces remain `E120`.                                                                                                                                                                                                          |
| `W130` | Exclusion names an unknown package.                                                                                                                                                                                                                                                                       |
| `W131` | Unit resolved to zero packages (inert).                                                                                                                                                                                                                                                                   |
| `W132` | Multi-unit commit with unscoped units.                                                                                                                                                                                                                                                                    |
| `W133` | Package both included and excluded.                                                                                                                                                                                                                                                                       |
| `W134` | Glob matched nothing.                                                                                                                                                                                                                                                                                     |
| `W135` | `Propagate-Scope` excluded every reached dependent.                                                                                                                                                                                                                                                       |
| `W140` | Unknown type mapped to `none`.                                                                                                                                                                                                                                                                            |
| `W141` | `release` unit with no directives.                                                                                                                                                                                                                                                                        |
| `W150` | Unknown footer key ignored.                                                                                                                                                                                                                                                                               |
| `W151` | Trailing paragraph nearly footer-shaped but treated as body.                                                                                                                                                                                                                                              |
| `W152` | A propagation directive in which **every** part resolves to no propagation, on either axis; `^none`, `+0`, `^none+*`, `^^none`, `%%none`, `++0`, `%%none++*`. Writing nothing says the same thing. Where a value was supplied and the depth is `0`, `W201` is emitted instead and `W152` is not (§8.3b). |
| `W153` | Conflicting package-level `Release-As`; newest won.                                                                                                                                                                                                                                                       |
| `W154` | Package held by `Release-As: none`; not released. The message MUST carry the withheld version.                                                                                                                                                                                                            |
| `W155` | Footer key matches `BREAKING CHANGE` case-insensitively but not exactly; **not** treated as breaking.                                                                                                                                                                                                     |
| `W156` | A `BREAKING CHANGE:` line appears in a body rather than the footer block; no effect.                                                                                                                                                                                                                      |
| `W157` | `BREAKING CHANGE` with an empty value.                                                                                                                                                                                                                                                                    |
| `W158` | `Release-As: auto` with no active hold.                                                                                                                                                                                                                                                                   |
| `W159` | Exact `Release-As` lower than the computed version, accepted under lenient mode (§8.6, `E156`).                                                                                                                                                                                                           |
| `W160` | Conflicting propagated channels; newest won.                                                                                                                                                                                                                                                              |
| `W170` | `cancel` had nothing to discard.                                                                                                                                                                                                                                                                          |
| `W171` | `cancel` discarded units already reflected in a published prerelease.                                                                                                                                                                                                                                     |
| `W172` | A commit contains a `cancel` unit alongside a bump-producing unit with an overlapping scope; the latter is discarded by the ancestor-or-self rule (§10.3).                                                                                                                                                |
| `W185` | Graduating a package already stable.                                                                                                                                                                                                                                                                      |
| `W186` | Conflicting channels; newest won.                                                                                                                                                                                                                                                                         |
| `W190` | Tag ignored: version is not valid SemVer.                                                                                                                                                                                                                                                                 |
| `W192` | Manifest version disagrees with baseline.                                                                                                                                                                                                                                                                 |
| `W193` | Catch-up release: the package's only cause is a propagation from an already-published dependency. Carries the origin and its version.                                                                                                                                                                     |
| `W194` | Package blocked: planned, but not attempted because a dependency failed to publish in this run.                                                                                                                                                                                                           |
| `W195` | Staleness audit found a package behind a dependency (§13.7b). Reporting only; never blocks.                                                                                                                                                                                                               |
| `W196` | Tag adopted for a version the registry already held, after identity verification (§19.4).                                                                                                                                                                                                                 |
| `W197` | Manifest range reconciled against a dependency released by an earlier run (§9.4).                                                                                                                                                                                                                         |
| `W199` | A proposed channel equals the package's current channel; the directive is redundant and nothing is proposed (§9.3).                                                                                                                                                                                       |
| `W200` | A propagated `stable` would have graduated a dependent off a prerelease; suppressed. Write a transition to graduate deliberately (§9.3).                                                                                                                                                                  |
| `W201` | A propagation **value** was supplied on either axis while that axis's depth resolves to `0`, so it reaches nobody and is inert; `^minor+0`, `%%beta++0`, or the footer equivalents (§8.3b). Supersedes `W152`.                                                                                           |
| `W202` | Channel-only release: the package is in the plan solely because its channel changed. Carries the old and new channel (§13.9).                                                                                                                                                                             |
| `W203` | A package released on `stable` declares a range on a workspace dependency whose current version is a prerelease (§9.4). Names both packages and versions.                                                                                                                                                 |
| `W204` | Channel-entry patch applied: the computed prerelease would not have exceeded the baseline, so the target was advanced by one patch (§11.4).                                                                                                                                                               |
| `W205` | `Propagate-Channel-Scope` excluded every reached dependent. The channel-axis counterpart of `W135`.                                                                                                                                                                                                       |
| `W206` | A channel transition matched no package; usually a mistyped `<from>` (§5.3).                                                                                                                                                                                                                              |
| `W207` | A channel transition whose `<from>` equals its `<to>`; inert.                                                                                                                                                                                                                                             |
| `W208` | Propagated bump suppressed: no source package releases on a channel this dependent can resolve (§9.3a). Names the unit, the origin's channel and the target's.                                                                                                                                            |
| `W209` | A correction had nothing to act on: the target is discharged, already discarded, or the wildcard's scope holds nothing pending (§7.4.2). Non-suppressible.                                                                                                                                                 |
| `W210` | A correction was superseded by a newer correction of the same target; the newest wins (§7.4.2).                                                                                                                                                                                                            |
| `W211` | An `Edits` restatement carries the same type, breaking marker, and description as its target; the correction changes nothing (§7.4.2).                                                                                                                                                                     |
| `W212` | A revert suppressed its target's changelog entry, and its own, for a package whose window still held both (§7.3).                                                                                                                                                                                          |
| `W213` | A `Reverts` value names a commit that is not an ancestor-or-self of the revert; the footer is informational for this unit (§7.3).                                                                                                                                                                          |
| `W214` | A `Reverts` value is not a well-formed commit sha; the footer is informational for this unit (§7.3).                                                                                                                                                                                                       |
| `W215` | A correction is void for a package: its own record there was discarded by a newer correction, so none of its effects apply for that package (§7.4.2, §13.4b).                                                                                                                                              |

**CCME 3 operational buckets.** The rollback codes `E300`–`E309` and adapter codes `E320`–`E329` are run-scoped,
unlike a malformed ordinary commit which can be discarded under message policy. A preflight finding stops all
artifact changes; a finding after a side effect preserves recorded progress, blocks unsafe subsequent work and makes
the run fail. They cannot be downgraded to warnings by lenient commit parsing. `W300`, `W320` and `W321` belong to
the warning bucket and never discharge an incomplete operation.

---

## 17. Conformance

### 17.1 What a conforming implementation must do

An implementation conforms to CCME 3.1.0-rc.2 if and only if it:

1. Parses messages per §4 and §5, producing exactly the units, scopes, and directives those sections define.
2. Resolves scopes per §6, including file-derived resolution (§6.2).
3. Applies the bump mapping of §7.1 and the `max()` combination rule of §9.1.
4. Implements `cancel` per §10, **including the ancestor-or-self rule of §10.3**.
5. Implements holds per §8.6.1 and §13.6a.
6. Computes versions per §11 and §13, reading version state exclusively from immutable release records (§12.4, §25), including the channel-entry patch
   of §11.4.
7. Computes the two propagation axes independently and in the phase order of §9.2, with both defaulting to depth `0`;
   graduates a dependent only through an explicit transition (§9.3); and suppresses a propagated bump the target cannot
   resolve (§9.3a).
8. Reproduces every vector in Appendix B.
9. Emits every diagnostic in §16 under the stated conditions, with `W155`, `W156`, `W172`, `W193`, `W194`, `W202`,
   `W208`, and `W209` non-suppressible.
10. Admits propagation per §13.4a (against the **dependent's** undelivered window) and satisfies G1–G8 under their stated hypotheses of §13.7c.
11. Publishes per §19: dependency-first order, tag-after-publish, dependents blocked on failure, ranges reconciled
    against current versions.
12. Produces the plan of §13 exactly, whatever internal representation or optimisation it uses (§13.11).
13. Applies corrections per §7.4 and §13.4b (strict-ancestor reach, scope containment, newest-wins precedence,
    voiding of discarded corrections, confinement to undischarged work), and suppresses reverted changelog entries
    per §7.3.
14. Implements the Git-default VCS record model and the configurable external adapter contract of §25, including
    fixed snapshots, complete history, immutable records, ownership-safe locking, and its conformance vectors.
15. Recognizes and separately plans rollback per §26, requires explicit activation, executes its identity-bound
    handlers and receipts, refuses unsafe or incomplete withdrawal, and satisfies its conformance vectors.

The polyrepository behavior of §27 is an **optional conformance profile**. An implementation that advertises that
profile MUST satisfy every §27 rule and vector in addition to the applicable core requirements above. An implementation
that does not advertise it remains conforming for a single repository and MUST preserve that behavior when `polyrepo`
and `configs` are absent. Merely discovering nested Git repositories or accepting source paths is not profile
conformance.

Distributed execution (§28) is a separate **optional conformance profile**. An implementation advertising it MUST
satisfy every §28 rule and vector across all three history modes, including transfer and reuse of actual dependent
build outputs. Remote command dispatch alone is insufficient. The vectors specify required behavior, not measured
speedups; an implementation that meets some of them and not others MUST say which (§17.3), as
[DESIGN-HISTORY.md](./DESIGN-HISTORY.md) does for the dispat engine.

A CCME 2 parser or an engine implementing only the ordinary forward-release projection MUST identify that narrower
support and MUST NOT claim full CCME 3 conformance. Specification publication, prose examples, and static protocol
vectors are not implementation or experimental evidence.

An implementation that computes correct plans but publishes them in an arbitrary order does **not** conform. The
guarantees of §13.7c are joint properties of the computation and the publish protocol; either alone is insufficient,
because a plan that is correct when computed can still be executed into an inconsistent registry state.

An implementation MAY additionally support configuration beyond §14, extra footer keys, and richer output, none of
which affect conformance, provided §14's final paragraph is respected.

### 17.2 Determinism and idempotency

Both are normative, and both are testable:

* **Determinism.** For a fixed (repository state at `HEAD`, configuration, withdrawal inventory and receipts), the release plan MUST be byte-identical
  across runs, machines, and implementations. Plan contents may not depend on wall-clock time, commit dates, tag creation order,
  filesystem iteration order, hash-map iteration order, or locale.
* **Idempotency.** Running the engine twice from the same repository, release records, rollback records, configuration and fixed verified inventory MUST produce the same plan. A new completion or cancellation changes that state and discharges its request.
  After a successful publish, each package's new baseline tag empties its `Wfresh`; train-wide `W` may remain non-empty
  solely to retain aggregate prerelease history (§13.3).
* **Resumption.** After a partial publish, implementations MUST recompute from tags. Under G3's hypotheses, the plan
  contains the unpublished packages at their reviewed versions. A changed inherited source channel or discharge of a
  suppressed source may produce a finite, newly surfaced follow-up plan; it MUST be reviewed rather than described as
  byte-identical resumption. Held packages remain excluded from publication (§13.6a).

Idempotency is the special case of convergence in which nothing failed. Stating only the special case is what leaves the
general one unimplemented, so both are required here explicitly.

Implementations MUST sort every collection that reaches the output (packages, contributing units, diagnostics) by a
total order (package name byte-wise; then commit topological index; then unit index). Iteration order of an unordered
container MUST NOT be observable.

For §27, fixed repository state means the complete map from repository identity to full head object ID, the control
gitlink snapshot, reachable release tags, and explicit baseline tuples. Output order compares repository identities
byte-wise before repository-local topological commit index and unit index. Commit dates never provide an order.

For §28, placement settings, worker availability, transient run identifiers, branch names and completion order
MUST NOT change the semantic plan. Operational receipt metadata is separate from its canonical serialization;
diagnostics retain deterministic semantic ordering rather than worker arrival order.

### 17.3 Versioning of this specification

This document is CCME **3.1.0-rc.2** and is itself versioned under SemVer:

* **Patch**: clarifications and editorial fixes that cannot change any release plan.
* **Minor**: backward-compatible types, footers, inline sigils, diagnostics or configuration keys that preserve existing execution semantics. A 1.x implementation MUST
  ignore unknown footer keys (`W150`) and MAY ignore unknown types (`W140`), so minor additions are forward-compatible
  by construction.
* **Major**: any change that alters the release plan for a message that was already valid.

The 2.0.0 revision preserved the 1.0.0 message grammar while correcting pending-ledger computation and proof premises.
The 3.0.0 revision reserves previously inert `rollback` units for explicitly activated artifact withdrawal and adds
VCS-dependent canonical revision operands. It also expands full-engine conformance to the adapter and rollback
protocols. These semantic and conformance changes require a major revision. A new optional key alone would not.
The activation boundary prevents an engine upgrade from silently executing old rollback-shaped messages.
An optional execution profile that preserves the previous plan exactly when omitted is a minor addition; §27 is such a
profile. Section 28 is also optional: its task transport and execution obligations apply only to implementations
advertising that profile and preserve the semantic plan. Adding the profile does not itself stamp a new version;
the specification release process owns the version declarations.

The escape hatches that make minor versions safe are `W140` and `W150`. Implementations MUST NOT convert either into an
error by default; `strictTypes` is opt-in for exactly this reason.

---

## 18. Security considerations

CCME 3 adapters and rollback handlers are trusted executable configuration. Request data MUST be JSON encoded, never
interpolated into shell source. An untrusted commit cannot select a command or authorize removal. Missing atomic lock
semantics, durable records or artifact identity is a refusal, not a reason to weaken safety. Rollback cannot revoke
external copies or prove the absence of external consumers; the plan must surface that limitation (§26).

Commit messages are **untrusted input**. In any repository that accepts contributions, the message text is
attacker-controlled, and under CCME that text directs version numbers, release scope, and publication. This section is
normative.

### 18.1 Threat model

The release engine typically runs in CI with credentials to publish packages. An attacker who can land a commit, or
merely open a pull request if CI runs the engine on PR branches with those credentials, controls the input to that
engine.

| Attack                         | Vector                                                                | Effect                                                                                                                                                                                                                                                           |
|--------------------------------|-----------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Mass version burn**          | `feat(*)^^major!: x`                                                  | Every package in the workspace takes a major bump. Irreversible: published versions cannot be recalled, and the version space below them is gone forever.                                                                                                        |
| **Version-space exhaustion**   | `Release-As: 999999.0.0`                                              | Burns the package's version space permanently. No subsequent release can ever be lower.                                                                                                                                                                          |
| **Ledger wipe**                | `cancel(*): x`                                                        | Discards every pending change, silently dropping the release and its changelog.                                                                                                                                                                                  |
| **Silent release freeze**      | `Release-As: none` on a wide scope                                    | Blocks releases indefinitely; visible only as `W154`, easily lost in CI logs.                                                                                                                                                                                    |
| **Channel hijack**             | `%beta` on a package expected to be stable                            | Diverts a release to a prerelease channel, or graduates a prerelease that was not ready.                                                                                                                                                                         |
| **Prerelease flood**           | `%%beta++*` on a widely-depended package                              | Moves an entire reverse closure onto a prerelease line in one commit. Reversible; stable baselines are untouched, but noisy, and it is `maxPackagesPerRun` and `maxChannelMovesPerRun` that bound it.                                                          |
| **Forced graduation**          | `%%*>stable++*` on a widely-depended package                          | Ends every dependent's prerelease train at once, publishing versions consumers resolve by default. Irreversible, since the stable versions cannot be recalled. Requires an explicit transition to write, which is why `W200` blocks every implicit route (§9.3). |
| **Blast-radius amplification** | `^^inherit` on a leaf package                                         | Turns a one-package change into a workspace-wide major release. Bounded by propagation being opt-in (§8.3): the reach is always written in the message.                                                                                                          |
| **Resource exhaustion**        | Pathological scope globs or very large depths across a huge workspace | CPU/memory pressure in CI. Bounded by §18.3.                                                                                                                                                                                                                     |
| **Induced-failure widening**   | Causing one publish to fail, hoping the retry releases more           | Under the §13.7c retry invariant, G5 makes later targets a subset. Otherwise changed source or channel admission can expose a finite catch-up; the recomputed plan and limits MUST be reviewed again before publication.                                             |
| **Stale-consumer wedge**       | A hold left in place on a widely-depended package                     | Consumers accumulate unreleased propagation indefinitely. Visible as `W154` plus a growing `W193` set; surfaced by the audit of §13.7b.                                                                                                                          |
| **Artefact adoption**          | Pre-publishing a version the engine is about to publish               | Blocked by the identity check of §19.4: an unverifiable pre-existing version is `E198`, never silently tagged.                                                                                                                                                   |
| **Record falsification**       | `fix: x` + `Edits: <sha>` targeting someone's `feat!`                 | Downgrades a pending breaking change to a patch, shipping it under a compatible number. Confined to undischarged work (§7.4.2) and to matching scopes (`E213`); the plan marks every corrected entry (§13.10), and `requireCodeownerFor` can gate the footer.     |
| **Targeted ledger wipe**       | `chore(pkg): x` + `Deletes: *`                                        | Discards a scope's pending records without the visibility of a `cancel` unit. Same reach as the ledger-wipe row, same mitigations, plus the corrected-entry marking of §13.10.                                                                                   |

Note that none of these require unusual syntax. They are ordinary, valid CCME, which is the point: **the format's
expressive power is exactly its attack surface.**

### 18.2 Required mitigations

Implementations MUST:

1. **Never publish from an untrusted ref.** The engine MAY be run in *plan-only* mode on pull-request branches for
   preview, but MUST NOT be given publish credentials there. Publication runs only from a protected branch, after
   review.
2. **Treat the release plan as reviewable output.** Every run MUST be able to emit its plan without side effects
   (`--dry-run` or equivalent), and CI SHOULD surface it on the pull request so a human sees the blast radius before
   merge.
3. **Fail closed on integrity errors.** `E191`, `E195`, and `E196` abort the run (§16). No partial publication.
4. **Fail safe on publication errors.** `E197`, `E198`, and `E199` stop the run but do not un-publish or un-tag what
   already succeeded (§16). Rolling back a published version is impossible; pretending otherwise would corrupt the tag
   state that the next run reads.

Under the §13.7c retry invariant, G5 makes the initially reviewed target set an upper bound for a resume. If the
invariant changes, implementations MUST show the recomputed target set, reapply the configured safety limits, and
require the repository's ordinary plan approval before publishing it.

Implementations SHOULD additionally offer, and repositories accepting external contributions SHOULD enable:

5. **A blast-radius gate**: refuse or require explicit approval when a single run would release more than
   `maxPackagesPerRun` packages, or apply a `major` bump to more than `maxMajorsPerRun` (§14).
6. **A directive allowlist**: restrict which directives may originate from outside a trusted path. `cancel`,
   `Release-As`, `Edits`, `Deletes`, `^^`, `++*`, and any transition ending in `>stable` are the high-privilege ones;
   `requireCodeownerFor` names the directives that must be approved by a CODEOWNER.
7. **A channel-move gate**: refuse or require approval when a single run would move more than
   `maxChannelMovesPerRun` packages between channels, in either direction. Channel moves are the cheapest broad effect
   to write (`++*` is three characters) and, in the graduating direction, the least reversible.
8. **Version sanity bounds**: reject an exact `Release-As` that raises the major version by more than `maxMajorJump`
   (default 1) beyond the computed version. This closes version-space exhaustion without blocking legitimate
   `Release-As: 2.0.0`.

### 18.3 Parser hardening

The parser uses a bounded number of scans (§20.7): `O(n)` time in message bytes, `O(1)` scanner state beyond the
`O(n)` parsed result, no backtracking, and no recursion. A hostile message cannot induce superlinear parsing, which is the concrete reason §20 exists alongside
Appendix A, since a careless regex implementation reintroduces the risk it was designed to remove.

Remaining bounds implementations MUST enforce:

* Depth values saturate at 1024 (§20.3): a larger value means `all`, and the digit loop holds a bounded integer
  however long the run is.
* Scope-set length, unit count per message, and message length **MUST** be capped, and are, by the defaults of
  `limits.*` (§14.1): these are the parser bounds, they are on without configuration, and they cannot be disabled. An
  operator may raise or lower the numbers; setting them to `null` or to zero is not conforming. Exceeding a cap is a
  diagnostic (`E158`), never a crash.
* Each glob/candidate match MUST cost `O(|pattern| + |candidate|)`; patterns are never compiled to a backtracking
  engine. `*` is the only metacharacter, so the bound is met by splitting the pattern at `*` into literal segments: the
  first is a prefix test, the last is a suffix test on what remains, and each segment between them takes its leftmost
  occurrence after the previous one, found by a linear-time substring search. Leftmost placement is sufficient, because
  a match that places a segment further right still matches with that segment moved left. The familiar two-pointer walk
  that restarts one byte after its last `*` is `O(|pattern| · |candidate|)`, as `*aaab` against a run of `a` shows, and
  does not meet the bound. Whole-run scope cost also includes every candidate examined, including zero-match patterns,
  as `R` in §13.11.

Parser hardening bounds one message. It says nothing about the cost of the run as a whole, which is bounded in §13.11; a
workspace large enough for that section to matter is also a workspace where a hostile commit has more leverage, and
`maxPackagesPerRun` (§14.1) is the control that limits it.

### 18.4 Supply-chain notes

* Tags are the state store, so **tag write access is release authority**. Restrict tag creation to CI.
* `E191` (duplicate version on different commits) is an integrity signal, not a nuisance: it is what a tag-rewriting
  attack looks like. It MUST abort.
* A `cancel` cannot delete a published tag (§10.3), so cancellation cannot be used to un-publish or to re-issue an
  existing version under different content.
* Manifest versions are never read as state (§12.4), so a poisoned manifest in a fork cannot influence versioning.

---

## 19. Implementation obligations for publishing

Parsing and computation are the specification's subject; publication is not. But publication writes the state that the
next run reads, so the obligations below are normative: the guarantees of §13.7c are joint properties of the computation
and the publish protocol, and do not survive an implementation that gets this section wrong.

The model throughout is that **every publish can fail independently**, and that a run which fails partway leaves a state
that is consistent, inspectable, and resumable without operator intervention.

CCME 3 runs execute explicitly requested rollback under §26 before ordinary publication; a rollback failure prevents
ordinary publication in that attempt. No rollback is inferred from failure below. Existing tags remain immutable,
including tags whose external artifacts have been withdrawn. §19.4 adoption cannot revive a withdrawn identity.

Delegating tasks under §28 does not replace these obligations. Verified local build outputs satisfy only their
local-output prerequisites; registry-availability edges still require publication and applicable native records.
Task receipts and transport commits MUST NOT become release evidence or release-tag ancestry. Section 28.6
adds per-owner publication serialization, fencing and reconciliation while preserving ordinary native recording.

### 19.1 Tagging

* A release tag MUST be created at the exact commit the plan was computed from (`HEAD` of the run) for every released
  package, **whatever its publish target** (§13.10a).
* Tags MUST be created **after** a successful publish of that package, never before. A tag for an unpublished version
  makes the version unrecoverable, since §13.3 will treat it as released.
* Tags MUST be created **immediately** after that package publishes, not batched to the end of the run (§19.3).
* Tags SHOULD be annotated, and MUST NOT be moved or deleted once pushed.
* A release tag is created, never replaced. Writing it to the authoritative store MUST fail when the store already
  holds that tag name, unless the stored tag names the same commit for the same package and version, which is the retry
  of an uncertain write (§19.4). A forced ref update MUST NOT be used for a release tag. A ref an implementation moves
  by design is not a release record and is outside this rule.
* For a package whose target is a private or internal registry, all of the above applies unchanged: it is published
  there first, then tagged.
* For a package whose target is `none` there is no publish step, so there is nothing to wait for: the tag is written
  once that package's manifest write succeeds.

### 19.2 Publish order

Publication MUST proceed in **dependency-first order**: no package is published before any workspace dependency that is
also in this run's plan.

```
publishOrder(plan, graph):
    G    = graph.restrictedTo(config.publish.orderKinds)    # ALL packages, not just planned
    seq  = topologicalSort(G, tieBreak = byteWiseLeastName)  # acyclic by §13.1
    return [ P for P in seq if P in plan ]                   # filter LAST; see below
```

**Order over the whole graph, then filter, never over the induced subgraph.** The traversal MUST run on every package
in the workspace and drop the unplanned ones only at the end. Inducing the graph on the planned packages first deletes
the paths that run *through* unplanned ones, and those paths still constrain the order. Given `A → B → C` where `B`
has no bump this run (by far the common case in an incremental release), the induced subgraph on `{A, C}` has **no edge
at all**, so `A` and `C` become mutually unordered and `A` may publish first, against a `C` that has not published yet.
The constraint is real even though `B` is not being released, because `A` still resolves `C` through `B` at install
time. The same applies to a package that is merely up to date, which is the common case in any incremental release.

* The order exists and is total because the graph is acyclic (§13.1). No condensation step is needed, and an
  implementation that finds itself needing one has a workspace that should have been rejected by `E200`.
* Ties are broken byte-wise by name so that the order is deterministic (§17.2). Two implementations MUST produce the
  same sequence.
* Ordering uses `publish.orderKinds`, which includes `optionalDependencies`: an optional dependency should still be
  available before its dependent, even though its absence does not block (§19.3).

Order matters for a reason that tags cannot express. If a dependent is published before its dependency, it is published
carrying a range reconciled against the dependency's *old* version, and both land at the same commit, so no later run
can tell from ancestry that anything is wrong (§13.7b). The inconsistency is undetectable after the fact, which is why
it is prevented rather than diagnosed. An implementation that detects it MUST raise `E197`.

`E197` is scoped to the rule above, not to propagation. Every workspace dependency edge between two packages in the plan
constrains the order, whether or not either package's bump arrived by propagation: a dependent that is in the plan for
its own unrelated `feat` still resolves its dependency at install time (vector 80a). An `E197` that fires only for
propagated pairs is under-reporting.

### 19.2a Build readiness

§19.2 orders publication. An engine that also runs the packages' builds has a second question for every edge `C → P`
between two packages that build in this run: what `C`'s build reads of `P`. The answer is a declared property of the
edge, its **build readiness relation**, with three values named for what `C`'s build waits for:

| Relation  | `C`'s build reads                                                      | `C`'s build starts after              |
|-----------|------------------------------------------------------------------------|---------------------------------------|
| `publish` | `P` as an installer resolves it: the published artefact and its record | `P` is published and recorded (§19.1) |
| `build`   | what `P`'s build leaves in the workspace                               | `P`'s build has succeeded             |
| `none`    | nothing that `P`'s build or publication produces in this run           | nothing of `P`                        |

`C`'s build here includes every preparation step of `C` that can read the provider, lockfile regeneration in
particular. Manifest reconciliation (§19.5) reads planned versions only and waits for nothing.

The values are ordered `none < build < publish`, and each precondition implies the one before it, because a package
builds before it publishes. Under all three, `P` publishes before `C` does: §19.2 is unchanged, is not a fourth value,
and cannot be declared away. `none` therefore means build in parallel and publish in order, which is the relation of a
deployment order: infrastructure before the application that runs on it, a schema before the service that reads it.

* **Declared, never inferred.** `none` states that `C`'s build reads nothing of `P`, and an engine cannot check that. It
  MUST NOT infer `none` and MUST NOT make it a default. A consumer whose preparation resolves the provider from a
  registry needs `publish`; one that reads the provider's workspace output needs `build`. An implementation documents
  where the relation is declared and what its default is.
* **Policy, not plan.** The relation is execution policy. It never changes which packages release or at which versions,
  it is no part of the input of §13 or of a plan digest, and two runs of one plan under different relations record the
  same releases when every task succeeds. Under §28 it travels with the run's resolved configuration, and a node's own
  settings MUST NOT change it.
* **Ordered over the whole graph.** Build order, like publication order, is taken over the whole graph and then
  restricted to the packages that build. For two packages `C` and `P` that build in this run, a path from `C` to `P`
  with no `none` edge on it places `P`'s build before `C`'s, whether or not the packages between them are in the plan,
  because `C` can read `P` through them. A `none` edge ends the constraint of every path through it: beyond it nothing
  is read. Either a pass-through node for each package that does not build, or a memoised index of the nearest building
  packages each package reaches without a `none` edge, keeps this at `O(P + E)`; a search per pair does not.
* **No new cycle.** Every build constraint runs along a dependency path and every package builds before it publishes,
  so the task graph stays acyclic for every choice of relations, and weakening a relation only removes constraints.
  `E197` cannot arise from a relation, because the publication order does not depend on it.
* **Failure is §19.3's.** Under `build` and `none`, `C` may be prepared and built against `P`'s planned version
  (§19.5) before the outcome of `P`'s publication is known. That is sound because §19.3 then either blocks `C` or, where
  `C` has a cause of its own, reconciles its manifest to `P`'s published version before `C` publishes and leaves `P`'s
  contribution owed (§13.4a): a manifest naming a version that was never published is never published itself. A build
  that embedded the planned version is rebuilt, or `C` is blocked; a build finished for a blocked consumer confers
  nothing. It is not published, discharges no obligation, and the next run builds again or reuses it under the identity
  rules of §28.5. Restoring the working tree of a blocked consumer is the implementation's business.
* **Under §28.** A `build` edge is the local-output dependency of §28.5 and a `publish` edge its registry-availability
  edge. A `none` edge carries no output into the consumer's build task; the requirement that task inputs include every
  build dependency is met because the declaration says there is none. `C`'s publication task runs after `P`'s
  publication and MAY read what that publication exported.

§13.11 gives the run durations the three values lead to, and what no value promises.

### 19.3 Partial failure

A run that publishes several packages MAY fail partway. Implementations MUST:

* tag each package immediately after that package publishes, so a partial run leaves a consistent, resumable state;
* on a failure, **block** every package in the plan that transitively depends on the failed one over
  `publish.blockingKinds` and has no cause of its own, marking each `W194`, and not attempt it. The closure is computed
  over the **full** workspace graph, so it traverses packages that are not in the plan: a package with no bump this run
  is still a path from a dependent to a failed dependency. A cause of its own is a fresh direct bump, a channel change,
  or a contribution from a provider that is neither failed nor blocked, including one that published in an earlier run;
  a dependent with one **proceeds**, its manifests reconciled to what its providers have published (§19.5), and what
  the failed provider owed it stays owed (§13.4a) and arrives as a catch-up when that provider publishes;
* continue publishing packages that are not blocked: an unrelated subtree has no reason to be punished for another's
  failure;
* report a completion summary naming what published, what failed, and what was blocked, and exit non-zero;
* on re-run, recompute from tags. Packages already tagged fall out of the plan by §13.6; packages that failed or were
  blocked remain candidates by §13.4a, and a dependent that proceeded returns as a catch-up (`W193`) once the failed
  provider publishes. Their versions remain unchanged only under G3's stated hypotheses; channel or
  source-set changes MUST appear in the recomputed plan.

```
run(plan):
    published, failed, blocked = {}, {}, {}
    for P in publishOrder(plan, graph):            # a flat sequence, §19.2
        bad = transitiveBlockingDeps(P, graph) & (failed | blocked)   # full graph, §19.3
        if bad and ownCauses(P, plan, bad) is empty:
            blocked[P] = W194; continue
        reconcileRanges(P, published)              # §9.4, §19.5: providers as published by now
        try:
            if publishTarget(P) != none:           # §13.10a
                publishTo(publishTarget(P), P, plan[P].version)   # or adopt, §19.4
            tag(P, plan[P].version, HEAD)          # §19.1: always, immediately
            published[P] = plan[P].version
        except PublishError as e:
            failed[P] = e
            if config.publish.onFailure == 'abort': return report(...)
    return report(published, failed, blocked)
```

`publishOrder` returns a **flat, totally ordered sequence**, not levels, so `run` iterates it directly. Publishing
strictly one at a time is the normative reading; an implementation MAY publish concurrently, but only if it preserves
the same happens-before relation (every dependency in the plan completes before its dependent starts) and reports in
the sequence order regardless, so that output stays deterministic (§17.2).

Blocking uses `publish.blockingKinds`, which excludes `optionalDependencies`. The distinction is deliberate: ordering is
about giving an install the best chance of resolving cleanly, whereas blocking is about not publishing an artefact that
is *broken*. A missing optional dependency does not break an install; a missing required one does.

Implementations MUST NOT batch tags to the end of a run. Doing so makes a mid-run failure republish everything, and
republishing an already-published version is a hard error in most registries. Implementations MUST NOT, on failure,
delete or move tags already written: those versions are published and immutable (§12.4, §18.4).

### 19.4 Publish/tag reconciliation

Publishing and tagging are two operations against two systems, so a run can be interrupted between them. On the next run
the package is still untagged, so it is still in the plan, and the registry rejects the republish. Without a rule the
repository is wedged: every subsequent run fails identically, and only a hand-written tag clears it.

When a publish fails because **the version already exists**, implementations MUST:

1. verify that the published artefact is the one this run would have published, by registry digest, provenance
   attestation, or an equivalent content identity check;
2. if it matches, treat the publish as successful, write the tag, and emit `W196`;
3. if it does not match, or identity cannot be established, raise `E198` and stop.

Step 3 is not pedantry. A mismatch means the version was published by something other than this run (a hand publish, a
competing branch, a compromised token), and tagging it would enrol a foreign artefact into the release lineage as if
this repository had produced it. Verification is what makes step 2 safe; without it, adoption is a supply-chain
vulnerability rather than a convenience. `publish.adoptPublished: false` disables adoption entirely for repositories
whose registry cannot support such a check.

### 19.5 Manifest writes

Per §9.4, each released package's manifest MUST be updated so that no released package declares a range excluding the
version each workspace dependency **has published** by the time the package is reconciled: its **planned** version when
it is in this run's plan and published, and its current baseline otherwise (`W197` when that baseline came from an
earlier run). A dependency that is in the plan but failed or was blocked has published nothing new: a dependent that
proceeds past it (§19.3) is reconciled to the baseline, and `W193` follows when the dependency publishes.

Because the graph is acyclic (§13.1) and §19.2 publishes every dependency before its dependents, the planned version and
the published one coincide at the moment each package is reconciled whenever the dependency published. Reconciliation
MAY therefore be computed once up front from the plan and corrected, before the dependent's publish, only for the
providers that did not publish; a manifest naming a version that was never published MUST NOT be published, whatever
was computed up front.

These writes are part of the publish step, not the plan, and MUST NOT be committed back in a way that creates a release
loop, that is, a bot commit that itself parses as a bump-producing unit. Bot commits SHOULD use a type mapped to `none`, and
repositories SHOULD exclude the bot's commits from the pending window by convention.

### 19.6 Convergence verification

After a run in which nothing failed or was blocked, implementations SHOULD re-plan once. An empty non-held plan proves
exhaustion for that observed state. Under the §13.7c retry invariant, any non-empty result is `E199`, because G4
discharged every published package and G5 admitted no new one.

Outside that invariant, publishing a previously suppressed source can legitimately expose a finite follow-up
obligation, and a source baseline move can change channel admission. Such a plan is not `E199` merely for being
non-empty. The implementation MUST stop before publishing it, report it with available `W193`/`W199` provenance,
reapply §14 safety limits, and require §18.2 review. A later explicitly approved invocation may publish it and perform
its own post-run check. This protocol does not promise that arbitrary changing states reach a global fixed point.

`E199` MUST be raised when a non-empty result occurs despite the retry invariant, or when a follow-up cannot be
explained by an observed source-set, baseline, or channel-admission change. Held packages are omitted from this comparison.
Private, internal, and artefact-less packages are not omitted, because §13.10a tags them.

The check uses no registry traffic. Repositories running the engine on a schedule SHOULD also run §13.7b and surface
`W195`, which detects pre-existing stale consumers.

### 19.7 Adopting CCME where packages are already stale

A workspace arriving from another release tool (or from a hand-run process that failed partway at some point) may hold
packages that are already behind their dependencies. The engine does not treat these as a special case: their
propagating commits are still inside their pending windows, precisely because those packages never released, so the
first run finds them and releases them like any other catch-up (§13.7a). The backlog may nonetheless be large enough to
be worth looking at before it publishes:

1. Run the engine in plan-only mode (`--dry-run`, §18.2) and read the `W193` entries. These are the stale packages.
2. Confirm the versions look right. By G3 they are the versions those packages were owed, which may be lower than an
   operator expects if the workspace has moved on considerably. They are still correct: the bump is `max()` over
   everything pending, so nothing is under-counted.
3. Release normally. One run discharges the whole backlog: one release per package, not one per missed propagation.

If an accumulated catch-up is genuinely unwanted (the dependency change no longer matters and the operator prefers not
to publish at all), the supported way to drop it is `cancel(<consumer>)` (§13.5a), not tag surgery. A blanket
`cancel(*)` as the first commit after adoption (§10.6) drops all of them at once.

## 20. Parsing without regular expressions

For CCME 3, the scanner still accepts a lowercase `type` token. Semantic validation MUST classify `rollback` before
ordinary bump processing, validate its explicit scopes and `Rollback-Version` under §26, and exclude the resulting
operational request from ordinary correction/cancellation/propagation tuples. The pseudocode below describes that
ordinary projection; it is not sufficient by itself to implement the new operational type. External VCS revision
operands follow §25 rather than Git-specific SHA validation.

The grammar is designed so that a conforming parser is a single left-to-right index scan with a fixed lookahead of one
character. No backtracking, no regular-expression engine, no recursion. This section is normative for behaviour and
illustrative for structure.

### 20.1 Primitives

```
Scanner:
    s: string, i: index

    eof()               -> i >= len(s)
    peek()              -> s[i]           (undefined at eof)
    next()              -> c = s[i]; i += 1; return c
    accept(c)           -> if not eof and s[i] == c: i += 1; return true; else false
    expect(c, code)     -> if not accept(c): raise code
    readWhile(pred)     -> start = i; while not eof and pred(s[i]): i += 1; return s[start..i]
    readUntilAny(chars) -> start = i; while not eof and s[i] not in chars: i += 1; return s[start..i]
    rest()              -> r = s[i..]; i = len(s); return r
```

Character predicates are table lookups, not classes:

```
isLower(c)   -> 'a' <= c <= 'z'
isUpper(c)   -> 'A' <= c <= 'Z'
isDigit(c)   -> '0' <= c <= '9'
isChannel(c) -> isLower(c) or isDigit(c) or c == '-'
isFooterKeyChar(c) -> isLower(c) or isUpper(c) or isDigit(c) or c == '-'
```

### 20.2 Splitting a message into units

```
splitUnits(message, separator):
    lines = message.split('\n')
    units = [], current = []
    for line in lines:
        if line == separator:
            units.append(join(current)); current = []
        else:
            if line.startsWith('\\') and line[1..] == separator:
                line = line[1..]                     # unescape
            current.append(line)
    units.append(join(current))
    return [ trimBlankEdges(u) for u in units if not isBlank(u) ]   # W001 for dropped
```

Line comparison is byte equality after §4.1 normalisation. No pattern matching is involved.

### 20.3 Parsing a header

Single pass, five phases:

```
parseHeader(line):
    sc = Scanner(line)
    h  = { type:'', scopes:[], inline:{}, breaking:false, description:'' }
    sawCaret   = false     # scratch: a '^' or '^^' has been consumed (one sigil, §5.3)
    sawPlus    = false     # scratch: a '+N' has been consumed, whichever token holds the depth
    depthFrom  = none      # scratch: which token supplied h.inline['depth']: '^', '^^' or '+'
    cdepthFrom = none      # scratch: which token supplied h.inline['channelDepth']: '%%' or '++'

    # 1. type
    if line.startsWith('BREAKING CHANGE') or line.startsWith('BREAKING-CHANGE'):
        raise E100   # dedicated message: this is a footer, not a type (§5.1)

    h.type = sc.readWhile(isLower)
    if h.type == '':
        if not sc.eof and isUpper(sc.peek): raise E101      # 'Feat: x'
        raise E100                                          # '123: x', ': x', ''
    if not sc.eof and sc.peek not in '(^+%!:': raise E101    # 'feat2: x', 'feat_x: y'

    # 2. optional scope-set
    if sc.accept('('):
        term = ''
        loop:
            if sc.eof: raise E103
            c = sc.next()
            if c == ')':
                if term == '': raise E104          # 'feat(): x' and 'feat(a,): x'
                h.scopes.append(term); break
            if c == ',':
                if term == '': raise E104
                h.scopes.append(term); term = ''
                while not sc.eof and sc.peek == ' ': sc.next()   # allowed after comma
                continue
            if c == ' ' or c == '\t': raise E102
            if c == '(':             raise E103
            if c == ':':             raise E103
            term += c

    # 3. inline directives
    while not sc.eof and sc.peek in '^+%':
        sigil = sc.next()

        if sigil == '^' and not sc.eof and sc.peek == '^':
            sc.next()                                  # doubled caret
            if sawCaret: raise E110                    # ^ and ^^ are one sigil
            if not sc.eof and sc.peek == '^': raise E110   # ^^^: third caret
            sawCaret = true
            value = sc.readUntilAny('^+%!:')
            if value != '':
                h.inline['propagate'] = validateInline('propagate', value)
            if depthFrom == '+':                       # an explicit +N is already in hand
                if h.inline['depth'] != ALL: raise E113
                warn W110
            h.inline['depth'] = ALL
            depthFrom         = '^^'
            continue

        if sigil == '%' and not sc.eof and sc.peek == '%':
            sc.next()                                  # doubled at-sign
            if not sc.eof and sc.peek == '%': raise E110   # %%%: third percent sign
            if 'propagateChannel' in h.inline: raise E110
            value = sc.readUntilAny('^+%!:')
            if value == '': raise E111                 # a bare '%%' means nothing
            h.inline['propagateChannel'] = validateInline('propagateChannel', value)
            if cdepthFrom == none:                     # '%%' implies channel depth 1 only if
                h.inline['channelDepth'] = 1           # no explicit ++N has been seen yet;
                cdepthFrom               = '%%'        # a later ++N may still override
            continue

        if sigil == '+' and not sc.eof and sc.peek == '+':
            sc.next()                                  # doubled plus
            if not sc.eof and sc.peek == '+': raise E110   # +++: third plus
            if cdepthFrom == '++': raise E110          # one ++N per header
            value = sc.readUntilAny('^+%!:')
            if value == '': raise E111                 # '++' carries no default depth
            h.inline['channelDepth'] = validateInline('depth', value)
            cdepthFrom               = '++'            # wins over '%%'s implied 1, silently
            continue

        value = sc.readUntilAny('^+%!:')
        if value == '' and sigil != '^': raise E111    # '^' alone is legal
        key = { '^':'propagate', '+':'depth', '%':'channel' }[sigil]

        if sigil == '^':
            if sawCaret: raise E110
            sawCaret = true
            if value != '':
                h.inline['propagate'] = validateInline('propagate', value)
            if depthFrom == none:                      # '^' implies depth 1 only if no
                h.inline['depth'] = 1                  # explicit +N has been seen yet;
                depthFrom         = '^'                # a later +N may still override
            continue

        if key == 'depth':
            if sawPlus: raise E110                     # one +N per header, after '^^' as well
            sawPlus = true
            if depthFrom == '^^':                      # '^^' asserts 'all'; disagreement is
                if validateInline('depth', value) != ALL: raise E113
                warn W110                              # '^^…+*': redundant, not wrong
                continue
            if depthFrom == '^':                       # §8.3: +N supplies the depth and wins,
                h.inline['depth'] = validateInline('depth', value)   # without error
                depthFrom         = '+'                # a *second* +N is now E110
                continue

        if key in h.inline: raise E110
        h.inline[key] = validateInline(key, value)
        if key == 'depth': depthFrom = '+'

    # 4. breaking marker
    if sc.accept('!'): h.breaking = true

    # 5. separator and description
    if not sc.eof and sc.peek == '(': raise E103   # 'feat(a)(b): x'
    sc.expect(':', E120)
    if not sc.accept(' '): raise E120
    if not sc.eof and sc.peek == ' ': raise E120
    h.description = sc.rest()
    if h.description == '': raise E121
    return h
```

`validateInline` is a table lookup plus, for `depth`, a digit loop, and for the two channel keys a split at `>`:

```
validateInline('propagate', v):
    if v in ['none','patch','minor','major','inherit']: return v
    raise E111

validateInline('depth', v):
    if v == '*' or v == 'all': return ALL
    if v == 'direct':          return 1
    if v == '':                            raise E111   # bare '+' or '++'
    if v[0] == '0' and length(v) > 1:      raise E111   # no leading zeros: '00', '007'
    n = 0
    for c in v:
        if not isDigit(c): raise E111    # every byte is checked, saturated or not
        if n <= 1024: n = n * 10 + (c - '0')   # saturate; n stays below 10250
    if n > 1024: return ALL
    return n

validateInline('channel', v):           return parseChannelValue(v, allowInherit = false)
validateInline('propagateChannel', v):  return parseChannelValue(v, allowInherit = true)

parseChannelValue(v, allowInherit):
    if allowInherit and (v == 'inherit' or v == 'none'): return v     # whole-value words
    gt = indexOf(v, '>')
    if gt < 0:
        return (NONE, channelSide(v, asFrom = false))                 # plain value
    if indexOf(v, '>', gt + 1) >= 0: raise E111                       # 'a>b>c'
    from = channelSide(v[0..gt],   asFrom = true)
    to   = channelSide(v[gt+1..],  asFrom = false)
    return (from, to)

channelSide(v, asFrom):
    if v == '':                raise E111       # '>stable', 'beta>'
    if v == '*':
        if asFrom: return ANY_PRERELEASE
        raise E111                              # '*' is a from-value only (§11.2)
    if v == 'inherit' or v == 'none': raise E111    # values, not channels
    if v == 'stable':          return STABLE
    if v == 'latest':          raise E180
    if not isLower(v[0]):      raise E181
    for c in v: if not isChannel(c): raise E181
    if len(v) > 32:            raise E181
    if config.channels.allowed is set and v not in it: raise E181
    return v
```

The digit loop saturates without returning. A loop that returns `all` at the first value above `1024` never reads the
rest of the run, so it accepts `+99999x`, which Appendix A rejects as `E111` because its depth pattern is `[1-9][0-9]*`
to the end of the value. Holding `n` once it has passed `1024` also keeps it below `10250`, so no digit run overflows
an integer. Saturation is a definition and not a fact about graphs: a depth above `1024` **means** `all`, including in
a workspace whose longest dependency chain exceeds `1024` edges.

`isChannel` (§20.1) does not admit `>`, so the split is unambiguous: a channel name can never contain the separator, and
`indexOf` needs no lookahead. `readUntilAny('^+%!:')` does not stop at `>`, so the whole transition arrives as one
value.

**Why phase 3 is unambiguous.** The scope-set has already been consumed at phase 2, so any `%` remaining is outside
parentheses and can only be a channel sigil. `readUntilAny('^+%!:')` stops at the next sigil, the breaking marker, or
the colon, none of which may appear in a directive value. No lookahead beyond one character is needed.

The three doubled tokens do not change that bound. Each is a fixed two-character token distinguished from its single
form by one `peek`, and each is followed by a guard against a third repetition, because without it `^^^minor` would
tokenise as `^^` (empty value) followed by `^minor` and parse silently as `^^minor`; `%%%rc` and `+++2` have the same
shape. Repeated sigils are never a count.

An empty value is legal **only** after `^` and `^^`. `%%`, `++`, `%` and `+` all raise `E111` on an empty value: a
channel with no name and a depth with no number carry no default worth guessing, whereas a caret's value is a bump and
bumps have one.

`sawCaret`, `sawPlus`, `depthFrom` and `cdepthFrom` in the listing are scanner scratch state, not part of the parsed
result. `sawCaret` enforces the once-per-header rule across both caret spellings, so `^minor^^` and `^+2^^` are alike
`E110` rather than one of them falling through to a depth check. `sawPlus` enforces the same rule for `+N`, and
`depthFrom` cannot carry it alone: after `^^` it reads `'^^'` whether or not a `+*` has been consumed, so a listing
without `sawPlus` accepts `^^+*+*` with two `W110` where §5.3 requires `E110`. `depthFrom` and `cdepthFrom` record
*which token* supplied each depth, which is what keeps every combination order-independent:

| Header             | transitions                      | Result                                                   |
|--------------------|----------------------------------|----------------------------------------------------------|
| `^minor+2`         | `depthFrom: none → '^' → '+'`    | depth `2`; the `+N` overrides `^`'s implied 1           |
| `+2^minor`         | `depthFrom: none → '+'`          | depth `2`; `^` supplies nothing, none needed            |
| `^^minor+2`        | `depthFrom: none → '^^'`         | `E113`                                                   |
| `+2^^minor`        | `depthFrom: none → '+'`          | `E113`                                                   |
| `^^minor+*`        | `depthFrom: none → '^^'`         | depth `all`, `W110`                                      |
| `^^minor+*+*`      | `depthFrom: none → '^^'`         | `E110` on the second `+*`, by `sawPlus`                  |
| `^minor+2+3`       | `depthFrom: none → '^' → '+'`    | `E110` on `+3`; one `+N` per header                     |
| `%%beta++3`        | `cdepthFrom: none → '%%' → '++'` | channel depth `3`; the `++N` overrides `%%`'s implied 1 |
| `++3%%beta`        | `cdepthFrom: none → '++'`        | channel depth `3`; `%%` supplies nothing, none needed   |
| `%%beta++1++2`     | `cdepthFrom: none → '%%' → '++'` | `E110` on `++2`; one `++N` per header                   |
| `%%beta%%rc`       | none                                | `E110`; one `%%` per header                             |
| `^^minor%%beta++1` | both, independently              | bump all levels, channel one level; legal (§5.3)        |

`^` yields silently to an explicit `+N` (§8.3) and `^^` refuses to disagree with one (`E113`); `%%` behaves like `^`,
since there is no doubled channel token asserting `all` for it to disagree with. Without the `depthFrom == '^'` and
`cdepthFrom == '%%'` arms, the same header would mean two different things depending on the order it was typed in.

### 20.4 Splitting a unit into header, body, footers

```
parseUnit(text):
    lines  = text.split('\n')
    header = parseHeader(lines[0])
    rest   = lines[1..]
    if rest is empty: return (header, '', [])
    if rest[0] != '': raise E100          # blank line required after header
    rest = rest[1..]

    paragraphs = splitOnBlankLines(rest)
    if paragraphs is empty: return (header, '', [])

    last = paragraphs[-1]
    if isFooterBlock(last):
        return (header, join(paragraphs[..-1]), parseFooters(last))
    else:
        if nearlyFooterBlock(last): warn W151
        return (header, join(paragraphs), [])
```

### 20.5 Footer detection without patterns

A line is a **footer start** if, scanning from index 0:

1. it begins with `BREAKING CHANGE` followed by `: `, a literal string comparison; or
2. it has a **key** of one or more characters, each `a`–`z`, `A`–`Z`, `0`–`9`, or `-`, followed immediately by `: `; or
3. it has such a key followed immediately by ` #` (the git issue-reference form).

```
footerKeyEnd(line):
    i = 0
    if line.startsWith('BREAKING CHANGE: '): return 15     # exact case, the only
                                                           # key containing a space
    if equalsIgnoreCase(line[0..15], 'BREAKING CHANGE')
       and line.startsWith(': ', 15): warn W155             # 'Breaking change: ...'
                                                           # falls through -> not a footer
    while i < len(line) and isFooterKeyChar(line[i]): i += 1
    if i == 0: return -1
    if line.startsWith(': ', i):  return i
    if line.startsWith(' #',  i): return i
    return -1

isFooterBlock(paragraph):
    lines = paragraph.split('\n')
    if footerKeyEnd(lines[0]) < 0: return false
    for l in lines[1..]:
        if footerKeyEnd(l) >= 0:   continue          # new footer
        if l.startsWith(' ') or l.startsWith('\t'): continue   # indented continuation
        if previousFooterKey in ['BREAKING CHANGE', 'BREAKING-CHANGE']:
                                                   continue   # free-form multiline
        return false
    return true
```

Two case traps sit on either side of this function, and they fail differently:

* `Breaking change: ...`: the generic loop halts at the space, `startsWith(': ')` fails, and the line is **not a footer
  at all**. `footerKeyEnd` catches it explicitly and warns `W155` before returning `-1`.
* `breaking-change: ...`: the hyphenated form contains only footer-key characters, so it **is** a well-formed footer
  with the key `breaking-change`. `footerKeyEnd` cannot catch this one; the check belongs at key resolution, where the
  key is compared to `BREAKING-CHANGE` exactly (breaking) and then case-insensitively (`W155`, not breaking) before
  falling through to `W150` for unknown keys.

Every other key is resolved case-insensitively at that same point, so `BREAKING CHANGE` is the one place where the
resolver must compare twice.

A `nearlyFooterBlock` is one where the first line is a footer start but a later line is not, the common typo of a body
sentence appended after trailers. It produces `W151` and the paragraph is body.

### 20.6 Parsing tags without patterns

```
parseTag(ref):
    at = lastIndexOf(ref, '@')
    if at <= 0 or at == len(ref) - 1: return NONE
    name = ref[0..at]
    ver  = ref[at+1..]
    if name not in workspace: return NONE
    return (name, parseSemver(ver))            # returns NONE on failure -> W190
```

`lastIndexOf` handles `@acme/ui@1.2.3` correctly and is a single reverse scan.

`parseSemver` is likewise a scan: three digit runs separated by `.`, an optional `-` followed by dot-separated
identifiers, an optional `+` followed by dot-separated identifiers. Leading zeros in numeric identifiers are invalid.
Each step is `readWhile(isDigit)` or `readUntilAny('.-+')`.

### 20.7 Complexity and determinism

* Time: `O(n)` in message bytes. The procedures use a bounded number of forward or reverse scans and no backtracking;
  they need not be fused into one physical pass.
* Space: `O(n)` for the parsed result in the worst case, plus `O(1)` scanner state when slices may refer to the input.
  An implementation that copies tokens still remains `O(n)` total space.
* No input can cause superlinear behaviour, the property that motivates avoiding regular expressions in code that runs
  over untrusted commit messages in CI.
* A scanner can retain the byte offset at which each error is detected, so implementations can render a caret pointing
  at the offending character without changing the accepted language or diagnostic code.

---

## 21. Appendix A: Regular expressions

Provided for implementers who prefer patterns. The equivalence claimed is exact and holds over **all** input, not merely
well-formed input: for every byte string, a pattern here matches if and only if §20 accepts. §20 remains normative for
*which* diagnostic is raised, for error positions, and for every check the patterns cannot express: repetition guards,
`latest`, scope-term semantics, and the saturation of out-of-range depths.

Where a pattern and §20 appear to disagree, §20 is wrong or the pattern is wrong; neither is licensed to be laxer than
the other. The depth patterns below are where the two formulations most easily diverge, so they deserve particular
care when either side is changed.

All patterns are PCRE and anchored. One of them, the `inline` group of the header pattern, nests a quantifier, so its
safety is argued rather than assumed: every alternative begins with a sigil that no value class admits, and the
lookahead below leaves each input exactly one tokenisation. With one way to parse there is nothing to backtrack into,
and no pattern here can backtrack catastrophically.

**Header (single pattern):**

```regex
^(?<type>[a-z]+)(?:\((?<scopes>[^()\r\n]+)\))?(?<inline>(?:\^\^[^\^+%!:\r\n]*|%%[^\^+%!:\r\n]+|\+\+[^\^+%!:\r\n]+|\^(?!\^)[^\^+%!:\r\n]*|[+%][^\^+%!:\r\n]+)*)(?<breaking>!)?: (?<description>\S[^\r\n]*)$
```

Group notes: `scopes` still requires splitting on `,` and per-term validation; `inline` still requires tokenising by
sigil. The pattern recognises shape, not validity.

**The `(?!\^)` after the single caret is load-bearing.** Both caret alternatives accept an empty value, so without the
lookahead `^^` tokenises two ways, as one doubled caret or as two single ones, and a run of `n` carets tokenises in
`Fib(n + 1)` ways. A backtracking engine tries every one of them before it reports that a header with no `: ` does
not match: `feat`, forty carets and ` x` costs on the order of `10^8` steps, and sixty carets cost `10^12`. That is
the superlinear behaviour §18.3 forbids, reachable from one commit message. With the lookahead a single caret is
never followed by another, a run of carets has exactly one reading (pairs, then at most one single), and a failed
match is linear. The accepted language and every capture are unchanged, because any caret run the old pattern matched
is matched by that one reading. An engine without lookahead, such as RE2, does not backtrack and may drop it.

The description group opens with `\S`, not `[^\r\n]`, so that the two-space form `feat:  x` is rejected rather than
parsed with a leading space in the description (`E120`, vector 18). A `+` quantifier over `[^\r\n]` silently accepts it.

**Inline directive tokens (apply with a global match to `inline`):**

```regex
(\^\^|%%|\+\+|[\^+%])([^\^+%!:]*)
```

All three doubled alternatives MUST come first: with `[\^+%]` first, `^^minor` tokenises as a bare `^` with an empty
value followed by `^minor`, `%%rc` as `%` followed by `%rc`, and `++2` as `+` with an empty value followed by `+2`,
which is `E111` where the correct answer is a channel depth of 2. Note also that the value quantifier is `*`, not `+`,
so that `^` and `^^` may stand alone and so that a valueless `%%` or `++` still tokenises: an empty value is legal after
`^` and `^^`, and is `E111` after `+`, `%`, `%%`, and `++`, which the pattern does not catch and the caller MUST check.
In the header pattern the single-caret and doubled-caret alternatives therefore take `*`, while `+`, `%`, `%%`
and `++` keep `+`.

Neither pattern validates a repetition guard. `^^^minor`, `%%%rc` and `+++2` all tokenise as a doubled token followed by
a single one and MUST be rejected as `E110` by the caller, exactly as in §20.3.

**Directive value validation:**

```regex
^(?:none|patch|minor|major|inherit)$                  # ^  propagate
^(?:\*|all|direct|0|[1-9][0-9]*)$                     # +  depth
^(?:\*|all|direct|0|[1-9][0-9]*)$                     # ++ propagate-channel-depth
^(?:(?:\*|stable|[a-z][a-z0-9-]{0,31})>)?(?:stable|[a-z][a-z0-9-]{0,31})$
                                                      # %  channel, optional transition
^(?:inherit|none|(?:(?:\*|stable|[a-z][a-z0-9-]{0,31})>)?(?:stable|[a-z][a-z0-9-]{0,31}))$
                                                      # %% propagate-channel
```

The two channel patterns already exclude `*` as a `<to>` and `inherit`/`none` on either side of a `>`, because neither
word nor `*` is in the right-hand alternation. They do **not** exclude `a>a`, which is `W207` rather than an error, nor
`latest`, which must be rejected as `E180` at validation because it is shape-valid.

The two depth patterns are deliberately **unbounded in length**: any run of digits without a leading zero is
shape-valid, and the saturation to `all` above `1024` happens in the caller's digit loop (§20.3), not in the pattern. A
bounded form such as `[1-9][0-9]{0,3}` would make `+20000` an `E111` here while §20.3 accepts it and saturates it, which
breaks the equivalence this appendix claims. Length is bounded elsewhere, since `limits.messageBytes` (§14.1) caps the whole
message, so an unbounded digit run is not an attack surface, and the pattern still cannot backtrack: `[0-9]*`
follows a `[1-9]` that no other alternative can match, so there is exactly one way to parse any input.

The leading-zero exclusion is real and normative (`00` and `007` are `E111`, not `0` and `7`) and §20.3's digit loop
carries the matching guard for it. A depth is either `0` exactly, or a digit run beginning `1`–`9`.

**Scope term:**

```regex
^-?[^\s(),:]+$
```

**Separator line (default separator):**

```regex
^---$
```

**Escaped separator:**

```regex
^\\---$
```

**Footer start:**

```regex
^(?:BREAKING[ -]CHANGE|[A-Za-z0-9-]+)(?:: | \#)
```

This pattern MUST NOT be compiled with the `i` flag. Case-insensitivity here would make `Breaking change: ...` match the
first alternative and be treated as a genuine breaking change, inverting the `W155` rule of §8.1.1. Every *other* footer
key is resolved case-insensitively, but at key resolution, not in this pattern.

**Release tag.** Note the greedy prefix, which is what makes the last-`@` rule work:

```regex
^(?<name>.+)@(?<version>(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$
```

Using `(?<name>.+?)` (lazy) here is a conformance bug: `@acme/ui@1.2.3` would yield the name `` and fail, or split at
the wrong `%`.

**Full SemVer 2.0.0** (the official pattern, reproduced for completeness):

```regex
^(?<major>0|[1-9]\d*)\.(?<minor>0|[1-9]\d*)\.(?<patch>0|[1-9]\d*)(?:-(?<prerelease>(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+(?<buildmetadata>[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$
```

**Prerelease counter extraction (§11.4):**

```regex
^(?<core>\d+\.\d+\.\d+)-(?<channel>[a-z][a-z0-9-]*)\.(?<counter>0|[1-9]\d*)$
```

A prerelease tag that does not match this pattern but is otherwise valid SemVer triggers `E182`.

### A.1 Pitfalls

| Pitfall                                    | Consequence                                                                                                                  | Avoidance                                                               |
|--------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------|
| `[\^+%]` before `\^\^` in the tokeniser    | `^^minor` silently becomes `^minor` at depth 1                                                                               | Order the alternation longest-first                                     |
| `\^+` to match the caret run               | `^^^minor` accepted as `^^minor`; carets read as a repetition count                                                          | Match the literal two-character token, then guard against a third caret |
| `\^` without `(?!\^)` in the header        | A caret run tokenises in `Fib(n + 1)` ways; a non-matching header costs exponential time                                     | Keep the lookahead, or parse with §20                                   |
| `[\^+%]` before `%%` in the tokeniser      | `%%rc` becomes `%` with an empty value followed by `%rc`, silently setting the unit's own channel instead of its dependents' | Order the alternation longest-first, exactly as for `\^\^`              |
| `[\^+%]` before `\+\+` in the tokeniser    | `++2` becomes `+` with an empty value followed by `+2`, silently setting the bump depth instead of the channel depth         | Order the alternation longest-first                                     |
| Splitting a channel value at the first `-` | `beta-2>stable` loses its channel name; hyphens are legal in channel names                                                   | Split at `>`, which `isChannel` excludes                                |
| Case-folding a channel value               | `%%Beta>stable` silently becomes a valid transition                                                                          | Channel names are case-sensitive; `E181`                                |
| Lazy name in the tag pattern               | Scoped package names split at the wrong `%`                                                                                  | Greedy `.+` before the final `%`                                        |
| `[a-zA-Z]+` for type                       | Accepts `Feat`, diverging from `E101`                                                                                        | `[a-z]+`, or lowercase explicitly                                       |
| `[^\r\n]+` for the description             | `feat:  x` parses with a leading space instead of `E120`                                                                     | Anchor the group with `\S`                                              |
| `.*` for scope contents                    | Swallows the `)` and the colon                                                                                               | `[^()\r\n]+`                                                            |
| Matching the separator with `^-{3,}$`      | `----` becomes a separator; a Markdown rule in a body truncates the unit                                                     | Exact equality with the configured string                               |
| Multiline mode on the whole message        | Header patterns match mid-body lines                                                                                         | Split into units and lines first                                        |
| `[\s\S]*` around footers                   | Quadratic on long bodies                                                                                                     | Split into paragraphs first                                             |
| The `i` flag on the footer-start pattern   | `Breaking change:` becomes a real breaking change, inverting `W155`                                                          | Never case-fold that alternative; fold at key resolution instead        |
| Unicode-unaware `.`                        | Breaks on emoji in descriptions                                                                                              | Enable `u` mode, or use §20                                             |

---

## 22. Appendix B: Conformance test vectors

The vectors below retain their original Git/forward-release fixtures. Full CCME 3 conformance additionally requires
all vectors in [VCS-PROTOCOL.md §6](./VCS-PROTOCOL.md#6-conformance-vectors) and
[ROLLBACK.md §8](./ROLLBACK.md#8-conformance-vectors). They are normative expected outcomes, not measured dispat results.

Each vector is `input → expected`. An implementation is conforming if it reproduces every one. Workspace for all
vectors:

```
packages: core, cli, ui, api, docs-site (private -> internal registry), @acme/theme
edges:    cli -> core, ui -> core, api -> core, docs-site -> ui, @acme/theme -> ui
tags:     core@1.4.2, cli@2.0.0, ui@0.9.1, api@1.2.0, @acme/theme@1.0.0
          (docs-site has never been released, so it has no baseline; being private
           does not exempt it from release or tagging, §13.10a)
```

Sections B.4 and B.5 override these tags locally where stated, and B.12 states a workspace of its own, because a
shared-version group is a relationship this one does not have.

### B.1 Parsing

| #    | Input header                             | Expected                                                                                 |
|------|------------------------------------------|------------------------------------------------------------------------------------------|
| 1    | `feat: x`                                | type `feat`, derived scope, bump `minor`                                                 |
| 2    | `fix(core): x`                           | scopes `[core]`, bump `patch`                                                            |
| 3    | `feat(core,cli): x`                      | scopes `[core, cli]`                                                                     |
| 4    | `feat(core, cli): x`                     | scopes `[core, cli]`; space after comma allowed                                         |
| 5    | `feat(core ,cli): x`                     | `E102`                                                                                   |
| 6    | `feat(@acme/theme): x`                   | scopes `[@acme/theme]`; `@` inside parens is literal                                    |
| 7    | `feat(@acme/theme)%beta: x`              | scopes `[@acme/theme]`, channel `beta`                                                   |
| 8    | `feat(*,-docs-site): x`                  | all packages except `docs-site`                                                          |
| 9    | `feat(.,-ui): x`                         | derived set minus `ui`                                                                   |
| 10   | `feat(core)^minor+2: x`                  | propagate `minor`, depth `2`                                                             |
| 11   | `feat(core)+2^minor: x`                  | identical to #10; order-independent                                                     |
| 12   | `feat(core)^minor^patch: x`              | `E110`                                                                                   |
| 13   | `feat(core)^med: x`                      | `E111`                                                                                   |
| 14   | `feat(core)^minor+*!: x`                 | breaking, propagate `minor`, depth `all`                                                 |
| 14a  | `feat(core)^^minor: x`                   | propagate `minor`, depth `all`, identical to #14 without `!`                            |
| 14b  | `feat(core)^^: x`                        | propagate `patch` (default), depth `all`, identical to `+*`                             |
| 14c  | `feat(core)^^!: x`                       | breaking, propagate `patch`, depth `all`                                                 |
| 14d  | `feat(core)^^minor+*: x`                 | as #14a, plus `W110` for the redundant `+*`                                              |
| 14d1 | `feat(core)^^minor+*+*: x`               | `E110` on the second `+*`; one `+N` per header, after `^^` as well (§20.3)               |
| 14e  | `feat(core)^^minor+2: x`                 | `E113`                                                                                   |
| 14f  | `feat(core)+2^^minor: x`                 | `E113`; order-independent                                                               |
| 14g  | `feat(core)^minor^^: x`                  | `E110`; `^` and `^^` are one sigil                                                      |
| 14h  | `feat(core)^^^minor: x`                  | `E110`; third caret                                                                     |
| 14i  | `feat(core)^^med: x`                     | `E111`                                                                                   |
| 14j  | `feat(core)^^%beta: x`                   | propagate `patch`, depth `all`, channel `beta`; channel depth `0`                        |
| 14k  | `feat(core)++2: x`                       | channel depth `2`, `Propagate-Channel` defaults to `inherit`; no bump propagation        |
| 14l  | `feat(core)++: x`                        | `E111`; `++` carries no default depth                                                   |
| 14m  | `feat(core)+++2: x`                      | `E110`; third plus                                                                      |
| 14n  | `feat(core)++1++2: x`                    | `E110`; one `++N` per header                                                            |
| 14o  | `feat(core)%%beta++3: x`                 | channel `beta`, channel depth `3`; `++N` wins over `%%`'s implied 1, no diagnostic      |
| 14p  | `feat(core)++3%%beta: x`                 | identical to 14o; order-independent                                                     |
| 14q  | `feat(core)^^minor%%beta++1: x`          | bump `minor` to all levels, channel `beta` to one. Both axes, independent (§5.3)         |
| 14r  | `feat(core)+2++1: x`                     | depth `2`, channel depth `1`. `+` and `++` are distinct sigils                           |
| 14r1 | `feat(core)+9999: x`                     | depth `all`; saturated at `1024` (§20.3), not `E111`                                    |
| 14r2 | `feat(core)+20000: x`                    | depth `all`; the digit run is unbounded in length; saturation, never rejection          |
| 14r3 | `feat(core)++20000: x`                   | channel depth `all`; identical treatment on the channel axis                            |
| 14r4 | `feat(core)+00: x`                       | `E111`; leading zeros rejected; `0` alone is the only depth that may start with `0`     |
| 14r5 | `feat(core)+007: x`                      | `E111`, not `7`                                                                         |
| 14r6 | `feat(core)+99999x: x`                   | `E111`, not `all`; saturation never excuses the rest of the digit run (§20.3)           |
| 14s  | `feat(core)%beta>rc: x`                  | `Channel` transition, `from` `beta`, `to` `rc`                                           |
| 14t  | `feat(core)%%*>stable++*: x`             | `Propagate-Channel` transition from any prerelease to stable, channel depth `all`        |
| 14u  | `feat(core)%%beta>*: x`                  | `E111`; `*` is a `from`-value only                                                      |
| 14v  | `feat(core)%%a>b>c: x`                   | `E111`; one `>` per value                                                               |
| 14w  | `feat(core)%>stable: x`                  | `E111`; empty `from`                                                                    |
| 14x  | `feat(core)%%beta>inherit: x`            | `E111`; `inherit` is a value, not a channel                                             |
| 15   | `feat(core)!^minor: x`                   | `E120`; `!` must precede the colon                                                      |
| 16   | `Feat: x`                                | `E101`                                                                                   |
| 17   | `feat:x`                                 | `E120`                                                                                   |
| 18   | `feat:  x`                               | `E120`                                                                                   |
| 19   | `feat: `                                 | `E121`                                                                                   |
| 20   | `feat(): x`                              | `E104`                                                                                   |
| 21   | `feat(core: x`                           | `E103`                                                                                   |
| 22   | `feat(core): fix: y`                     | description `fix: y`                                                                     |
| 23   | `cancel(*): reset release state`         | control unit, scope all                                                                  |
| 24   | `cancel(*)!: x`                          | `E170`                                                                                   |
| 25   | `cancel(core)^minor: x`                  | `E171`                                                                                   |
| 26   | `release(cli)%stable: x`                 | control unit, channel stable                                                             |
| 27   | `release(cli)!: x`                       | `E141`                                                                                   |
| 27a  | `BREAKING CHANGE: gone` as a header line | `E100`                                                                                   |
| 27b  | `breaking: x`                            | Valid header, unknown type `breaking`, bump `none`, `W140`. **Not** a breaking change.   |
| 27c  | `feat(a)(b): x`                          | `E103`                                                                                   |
| 27d  | `feat(a,): x`                            | `E104`                                                                                   |
| 27e  | `feat2: x`                               | `E101`; digits are not type characters                                                  |
| 27f  | `: x`                                    | `E100`                                                                                   |
| 27g  | `release(api): Release-As: 3.0.0`        | Valid header, description `Release-As: 3.0.0`, **no** directive set; inert `W141` (§7.2) |

### B.2 Multi-unit messages

**Vector 28**

```
feat(core): a

---

fix(cli): b
```

→ two units: `core` minor, `cli` patch.

**Vector 29**

```
feat(core): a

BREAKING CHANGE: gone

---

fix(cli): b
```

→ `core` major, `cli` patch. The footer does not reach unit 2.

**Vector 30**

```
fix(core): a

---

fix(cli): b

Signed-off-by: A <a@example.com>
```

→ two units; the trailer is message-level and ignored (§4.5).

**Vector 31a**: `cancel` carrying a DCO trailer:

```
cancel(core): reset release state

Signed-off-by: A <a@example.com>
```

→ Valid. The trailer is message-level (§4.5) and exempt from `E171`.

**Vector 31**

```
docs(core): describe the format

The delimiter is:

\---

and it separates units.
```

→ one unit, body contains a literal `---`.

### B.3 Propagation

Given `feat(core)` and the workspace above:

| #   | Header                                               | Result                                                                                                                                                                                               |
|-----|------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 32  | `feat(core): x`                                      | **`core` `1.5.0` only.** Depth is `0` by default, so nothing propagates (§8.3)                                                                                                                       |
| 32a | `feat(core)^: x`                                     | `core` `1.5.0`; `cli` `2.0.1`, `ui` `0.9.2`, `api` `1.2.1`. `@acme/theme` and `docs-site` are at depth 2 and are **untouched**                                                                       |
| 33  | `feat(core)+1: x`                                    | identical to #32a; `+1` and `^` say the same thing                                                                                                                                                  |
| 33b | `feat(core)+*: x`                                    | as #32a, plus `@acme/theme` `1.0.1`; `docs-site` released at `0.1.0` (`initialVersion`), tagged, published to the internal registry (§13.10a)                                                        |
| 33c | `feat(core)^^: x`                                    | identical to #33b                                                                                                                                                                                    |
| 33d | `feat(core)^minor: x`                                | `core` `1.5.0`; `cli` `2.1.0`, `api` `1.3.0`, `ui` `0.9.2` (minor remapped to patch while `0.y.z`, §12.6); bump raised to `minor`, depth `1` from the caret                                         |
| 34  | `feat(core)^none: x`                                 | `core` minor only; `W152`; writing nothing says the same thing                                                                                                                                      |
| 35  | `feat(core)+0: x`                                    | `core` minor only; `W152`; no value was supplied, so this is redundancy, not an inert value                                                                                                         |
| 35a | `feat(core)^minor+0: x`                              | `core` minor only; `W201` **alone**; a `minor` was supplied and the depth discards it (§8.3b). An implementation emitting `W152` here fails this vector                                             |
| 35b | `feat(core)^none+0: x`                               | `core` minor only; `W152`; both parts say nothing                                                                                                                                                   |
| 36  | `feat(core)^inherit+*: x`                            | `core` minor; every dependent minor                                                                                                                                                                  |
| 36a | `feat(core)^^inherit: x`                             | identical to #36                                                                                                                                                                                     |
| 37  | `feat(core)!^inherit+1: x`                           | `core` major; `cli`, `ui`, `api` major                                                                                                                                                               |
| 37a | `feat(core)!: x`                                     | `core` `2.0.0` only. A breaking change propagates no further than any other unit without a caret                                                                                                     |
| 38  | `feat(core)^: x` + `feat(cli): y` in one window      | `cli` = max(minor direct, patch propagated) = minor                                                                                                                                                  |
| 39  | `feat(core)^^: x` with `Propagate-Scope: -docs-site` | As #33b minus `docs-site`, which is **untouched** and stays unreleased. `^^`, not `^`: at depth `1` `docs-site` is out of reach anyway and the vector would pass without the scope being read at all |
| 39a | `feat(core)++1: x`                                   | `core` `1.5.0`; `cli`, `ui`, `api` take `core`'s channel; already stable, so `W199` each and nothing else releases                                                                                  |
| 39b | `feat(core)%beta++1: x`                              | `core` `1.5.0-beta.0`; `cli` `2.0.1-beta.0`, `ui` `0.9.2-beta.0`, `api` `1.2.1-beta.0`; channel-only releases (`W202`, `W204`)                                                                      |
| 39c | `feat(core)^%beta: x`                                | **`core` `1.5.0-beta.0` alone.** The caret reaches all three; each is suppressed by §9.3a with `W208`                                                                                                |
| 39d | `feat(core)^%beta++1: x`                             | `core` `1.5.0-beta.0`; `cli` `2.0.1-beta.0`, `ui` `0.9.2-beta.0`, `api` `1.2.1-beta.0`; bump and channel together, no `W204`                                                                        |
| 39e | `feat(core)^^minor++1: x`                            | `minor` reaches all five dependents; the origin's channel reaches only the three direct consumers. Axes are independent                                                                                          |
| 40  | `feat(ui)^: x`                                       | `ui` minor; `docs-site` and `@acme/theme` patch; `docs-site` released to the internal registry and tagged                                                                                            |

### B.4 Cancel

**Vector 41**: history `A: feat(core)`, `B: cancel(core)`, `C: fix(core)`, linear. → `core` = `1.4.3` (patch from `C`
only).

**Vector 42**: history `A: feat(core)`, `B: cancel(core)`, nothing after. → `core` not released; stays `1.4.2`.

**Vector 43**: one commit containing:

```
cancel(core): reset release state

---

feat(core): new thing
```

→ ancestor-or- **self**: the `feat` is discarded. `core` not released.

**Vector 44**: `A: feat(core)`; branch `C: feat(core)` from `A`; `B: cancel(core)` on main; merge `D`. → `A` discarded,
`C` retained. `core` = `1.5.0`.

**Vector 44a**: hold, then lift. From `core@1.4.2`:

(`«…»` marks a footer; see §8.6.2.)

```
1: feat(core)^: streaming reader
2: release(core): hold        «Release-As: none»
3: fix(core): guard empty input
4: release(core): resume      «Release-As: auto»
```

The caret on commit 1 is load-bearing: propagation depth defaults to `0` (§8.3), so without it this history releases
`core` alone and demonstrates nothing about the hold's effect on dependents.

→ no release at commits 2 and 3 (`W154`, reporting the withheld `1.5.0`). At commit 4, `core` = `1.5.0`, changelog
containing both entries. `cli`, `ui`, `api` are not propagated to at commits 2–3 (held package is not a source, §13.4a,
vector 82b), then receive their patch at commit 4.

**Vector 44b**: the same history with `cancel(core)` at commit 2 instead of the hold. → `core` = `1.4.3` at commit 3,
changelog containing only the fix. The `feat` is unrecoverable.

**Vector 44c**: hold never lifted. → `core` never releases; `W154` on every run; tuples keep accumulating.

**Vector 44d**: `cancel(core)` at commit 3 of vector 44a, before the lift. → The hold and the `feat` are both
discarded. `core` resumes with an empty ledger and is not released until something new lands.

**Vector 44f**: hold at commit 2, then three ordinary commits including a breaking change:

```
1: feat(core)^: streaming reader
2: release(core): hold        «Release-As: none»
3: fix(core): guard empty input
4: feat(core): add codec
5: fix(core)!: drop legacy flag
```

→ `core` is **still held** at commit 5. `W154` on every run; no ordinary commit lifts a hold. The withheld version it
reports rises from `1.5.0` to `2.0.0` once commit 5 lands. `cli`, `ui` and `api` receive nothing at any of the five
commits despite the caret on commit 1: a held package is not a propagation source for as long as the hold stands.

**Vector 44g**: `Release-As: minor` on any unit. → `E151`. `Release-As` has no bump form (§8.6).

**Vector 44e**: `none` at commit 2, `auto` at commit 4, `none` again at commit 6. → Held. The newest package-level
directive wins outright; the engine does not replay the sequence.

**Vector 45**: `cancel(*)` in a repo where `api` is at `1.0.0-beta.3` with pending units. → pending units discarded,
`W171`; `api` stays at `1.0.0-beta.3`, still on channel `beta`.

### B.5 Prereleases

Baseline `api@1.0.0-beta.3`, stable baseline `api@0.9.0`.

| #   | Pending                                                             | Expected                                                                                                                                                                                              |
|-----|---------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 46  | `feat(api)%beta` (window bump: minor, `preserveMajorZero` on)       | target `applyBump(0.9.0, minor)` = `0.9.1` (minor remapped to patch while `0.y.z`); core differs from `1.0.0` → `0.9.1-beta.0`, and `E195` because that is **lower** than the baseline `1.0.0-beta.3` |
| 47  | same, with `preserveMajorZero: false`                               | target `applyBump(0.9.0, minor)` = `0.10.0` → `0.10.0-beta.0`, still `E195`                                                                                                                           |
| 48  | `feat(api)!%beta`, `preserveMajorZero: false`                       | target `applyBump(0.9.0, major)` = `1.0.0`; core matches baseline core → `1.0.0-beta.4`                                                                                                               |
| 49  | `release(api)%stable`, window containing the breaking change of #48 | `1.0.0`                                                                                                                                                                                               |
| 50  | `release(api)%rc`, same window                                      | `1.0.0-rc.0`                                                                                                                                                                                          |
| 51  | Baseline `core@1.4.2`, `feat(core)%beta`                            | `1.5.0-beta.0`                                                                                                                                                                                        |
| 52  | Then another `fix(core)%beta` in the same window                    | `1.5.0-beta.1`                                                                                                                                                                                        |
| 53  | Then a `feat(core)!%beta`                                           | `2.0.0-beta.0`                                                                                                                                                                                        |
| 54  | Then `release(core)%stable`                                         | `2.0.0`                                                                                                                                                                                               |
| 55  | `ui` at `0.9.1`, `feat(ui)`, `preserveMajorZero: true`              | `0.9.2`                                                                                                                                                                                               |
| 56  | `ui` at `0.9.1`, `feat(ui)!`, `preserveMajorZero: true`             | `0.10.0`                                                                                                                                                                                              |
| 57  | `ui` at `0.9.1`, `Release-As: 1.0.0`                                | `1.0.0`                                                                                                                                                                                               |
| 57a | `cli` at `2.0.0`, no pending bumps, moved onto `beta` by `%%beta`   | `2.0.1-beta.0`; the channel-entry patch (§11.4). `W202` for the channel-only release, `W204` for the patch                                                                                           |
| 57b | the same, but `cli`'s window already carries a `fix`                | `2.0.1-beta.0` and **no** `W204`: `applyBump(2.0.0, patch)` already exceeds the baseline, so no extra step is taken                                                                                   |
| 57c | `cli` at `2.1.0-beta.3`, reached by `%%beta`                        | Nothing. `W199`; it is already on `beta`; the directive proposes no change. This is what makes the channel axis converge (§13.7c G7)                                                                 |
| 57d | `cli` at `2.1.0-beta.3`, reached by `%%beta>rc++1`                  | `2.1.0-rc.0`; the transition matches, the counter resets (§11.4)                                                                                                                                     |
| 57e | `cli` at `2.1.0-beta.3`, reached by `%%rc>stable++1`                | Nothing. `cli` is on `beta`, not `rc`; it does not match `<from>` and is untouched. `W206` if no reached dependent matches                                                                            |

Vectors 46 and 47 are retained deliberately: they demonstrate that a hand-created `1.0.0-beta.3` tag on a package whose
last stable release is `0.9.0` produces a version regression under any non-breaking bump, which the engine MUST reject
(`E195`) rather than publish. The fix is `Release-As: 1.0.0-beta.4`, or a stable `api@1.0.0` tag, or a breaking change
as in #48.

### B.6 Tags

| #  | Tag                              | Parsed                                                                           |
|----|----------------------------------|----------------------------------------------------------------------------------|
| 58 | `core@1.4.2`                     | `core`, `1.4.2`                                                                  |
| 59 | `@acme/theme@1.0.0`              | `@acme/theme`, `1.0.0`                                                           |
| 60 | `@acme/theme@1.0.0-rc.1+build.5` | `@acme/theme`, `1.0.0-rc.1`, metadata ignored                                    |
| 61 | `core@v1.4.2`                    | ignored, `W190`                                                                  |
| 62 | `unknown@1.0.0`                  | ignored silently                                                                 |
| 63 | `core@1.4`                       | ignored, `W190`                                                                  |
| 64 | `release-2024`                   | ignored (no `@`)                                                                 |
| 65 | `core@1.5.0-beta3`               | `E182` on use as a prerelease baseline; repository-scoped: the run aborts (§16) |

### B.7 Partial failure and catch-up

These vectors use the workspace above and are stated as **run sequences**, because the property under test is what the
engine does on the *second* run. `run k ✓ P` means package `P` published and was tagged in run `k`; `run k ✗ P` means
its publish failed. Every run computes at the same `HEAD` unless stated otherwise.

**Vector 66**: the orphan. Commit `C1`: `feat(core)^: streaming reader`.

| Run | Event                                                    | Plan                                                             |
|-----|----------------------------------------------------------|------------------------------------------------------------------|
| 1   | ✓ `core@1.5.0`, ✓ `ui@0.9.2`, ✓ `api@1.2.1`, ✗ `cli` | `core` minor; `cli`, `ui`, `api` patch                           |
| 2   | ✓ `cli@2.0.1`                                           | **`cli` patch only**, marked `W193` *catch-up from `core@1.5.0`* |
| 3   | none                                                        | empty                                                            |

An engine that admits propagation against the **source's** window instead of the target's produces an **empty plan at
run 2**, and `cli` is never released, on that run or any later one. This vector is the single most important one in
this appendix: an implementation can pass every other vector and still fail this one.

**Vector 67**: catch-up does not widen depth. Commit `C1`: `feat(core)^: x` (depth `1`).

| Run | Event                    | Plan                                   |
|-----|--------------------------|----------------------------------------|
| 1   | ✓ `core@1.5.0`, rest ✗ | `core` minor; `cli`, `ui`, `api` patch |
| 2   | ✓ `ui@0.9.2`, rest ✗   | `cli`, `ui`, `api` patch               |
| 3   | none                        | `cli`, `api` patch                     |

`@acme/theme` and `docs-site` are at depth 2 and MUST NOT appear in any run, including run 3, where their dependency
`ui` has just republished. Propagation does not cascade (§9.2), and catch-up cannot widen a blast radius (§13.7c G5).

**Vector 68**: mid-chain failure under `^^`. Commit `C1`: `feat(core)^^: x`.

| Run | Event                                   | Plan                                                                   |
|-----|-----------------------------------------|------------------------------------------------------------------------|
| 1   | ✓ `core@1.5.0`, ✓ `ui@0.9.2`, ✗ rest | all six packages                                                       |
| 2   | none                                       | `cli`, `api`, `@acme/theme`, `docs-site`; `@acme/theme` still `patch` |

`@acme/theme` is admitted at depth 2 **from `core`**, not at depth 1 from the republishing `ui`. Depth is measured from
the originating source set in every run (§9.2).

**Vector 69**: publish order. Plan contains every released package.

→ `core`, `api`, `cli`, `ui`, `@acme/theme`, `docs-site`. Dependencies precede dependents; ready sets are ordered
byte-wise by name, and `@acme/theme` precedes `docs-site` because `@` (0x40) sorts below `d` (§19.2). `docs-site`
takes its place in the order like any other package; its private registry does not remove it (§13.10a). Any
implementation emitting `ui` before `core`, or `@acme/theme` before `ui`, fails conformance (`E197`).

**Vector 70**: blocking. Same plan, `ui`'s publish fails.

→ published `core@1.5.0`, `api@1.2.1`, `cli@2.0.1`; failed `ui`; **blocked** `@acme/theme` and `docs-site` (`W194`
each), planned but never attempted, because both depend on `ui`. Run exits non-zero. On resume: `ui@0.9.2`, then
`@acme/theme@1.0.1` and `docs-site@0.1.0`, all at the versions planned in run 1 (§13.7c G3). Run 3 is empty. Note that
`cli` and `api` publish normally in run 1: an unrelated subtree is not punished for `ui`'s failure.

**Vector 71**: `cancel` on the consumer. `C1`: `feat(core)^: x`; `C2`: `cancel(cli): reset release state`. `core`
published at run 1.

→ Run 2 plans `ui` and `api` only. `cli`'s pending propagated contribution is discarded by §13.5a; it is not released
and does not accumulate.

**Vector 72**: `cancel` on the provider, after the provider released. `C1`: `feat(core)^: x`; `C2`:
`cancel(core): reset release state`. `core@1.5.0` published at run 1.

→ Run 2 plans `cli`, `ui`, `api`: the catch-up proceeds. The `cancel` is a no-op for `core` (`W170`): there is nothing
pending left to discard, and cancellation never reaches a published release (§10.3, §13.4a). To drop the consumer's
release, cancel the consumer, as in vector 71.

**Vector 73**: `cancel` on the provider, before it released. Same two commits, nothing published.

→ `core` is not released and propagates to nothing. The unit was still pending for `core`, so `core` is removed from its
source set (§13.4a). Contrast with vector 72: the same `cancel` text, opposite outcome, decided solely by whether the
provider had already released.

**Vector 74**: held consumer. `C1`: `feat(core)^: x`; `C2`: `release(cli): hold «Release-As: none»`. `core@1.5.0`
published at run 1.

→ `cli` is stale but **held**: `W154` reporting the withheld `2.0.1`, no release, on every run. After `C3`
(`release(cli): resume «Release-As: auto»`), `cli` releases `2.0.1`. The catch-up survived the hold intact (§13.7c G2).

**Vector 75**: direct beats catch-up. `C1` contains `feat(core)^: x` and `feat(cli): y`. `core@1.5.0` published at run

1.

→ Run 2: `cli` = `2.1.0`, a **minor**, not the propagated patch. `effective = max(direct, propagated)` is unchanged by
catch-up (§9.1).

**Vector 76**: accumulated staleness. Five commits, `core` released after every one, `cli` never released. Each commit
carries the caret; without it none of them is a propagation source and `cli` is not stale at all (§8.3):

```
C1: feat(core)^: streaming reader
C2: fix(core)^: guard empty input
C3: feat(core)^: add codec
C4: fix(core)^: correct offset
C5: feat(core)^: buffer pooling
```

→ One release: `cli@2.0.1`. Each caret propagates the default `patch`, and five missed propagations collapse under
`max()` into a single patch. Catch-up is not a queue of deferred releases and MUST NOT emit one release per missed
propagation; an implementation producing `cli@2.0.5`, or five separate `cli` releases, fails this vector.

**Vector 77**: private packages converge. Any plan reaching `docs-site`.

→ `docs-site` is released at `0.1.0`, published to the internal registry, and **tagged** `docs-site@0.1.0`. The next
run's plan is empty. An implementation that versions it without tagging it fails this vector: `docs-site` would reappear
in every subsequent plan for ever, and `E199` (§19.6) would fire on every run.

**Vector 77a**: a package with target `none`. Same, with `publishTargets: { "docs-site": "none" }`.

→ Released, tagged `docs-site@0.1.0`, manifest written, **no artefact uploaded**. It converges identically. Tagging is a
function of release, not of publication.

**Vector 78**: publish succeeded, tag write failed. `core` is published to the registry at `1.5.0`; the tag push fails;
the run is re-run.

→ `core` is still in the plan at `1.5.0`; the registry rejects the republish as an existing version. Identity verified
against this run's artefact → tag written, `W196`, run continues. Identity **not** verifiable → `E198`, run stops, no
tag written. An implementation that unconditionally tags on "version already exists" fails this vector.

**Vector 79**: optional dependency fails. `docs-site → ui` declared under `optionalDependencies`, `ui`'s publish fails.

→ `docs-site` is **not** blocked: an optional dependency is installable in its absence (§19.3). Publish *order* still
placed `ui` first (`publish.orderKinds`), but failure does not propagate over that edge (`publish.blockingKinds`).

**Vector 80**: nothing published. Every package in the plan fails.

→ Not a resumable partial failure. The run made no progress, so retrying cannot make any either; the engine MUST fail
loudly rather than report a partial success (§13.7c G6).

**Vector 80a**: ordering and blocking through a package that is not in the plan. Commit `C1`:
`feat(core)^^: x` with `Propagate-Scope: -ui`, so `ui` is excluded from the plan while `core` and `@acme/theme`, which
reaches `core` at depth 2 *through* `ui`, are both in it.

→ The publish order MUST still place `core` before `@acme/theme`, and if `core`'s publish fails `@acme/theme` MUST be
**blocked** (`W194`). Two distinct implementation errors produce a wrong answer here, and both are easy to make:

* ordering the publish over the subgraph induced on the plan: with `ui` absent there is no edge between `core` and
  `@acme/theme` at all, they become mutually unordered, and `@acme/theme` may publish first;
* computing the blocking closure over direct parents in the plan: `@acme/theme`'s only dependency is `ui`, which is not
  in the plan, so the chain to `core` breaks and it publishes against an unpublished `core`.

Compute both over the full workspace graph and filter to the plan afterwards (§19.2, §19.3). This is not an exotic
configuration: any package that merely has no bump in this run sits in exactly the position `ui` occupies here, which
makes this the most commonly hit vector in B.7.

**Vector 80b**: the three build readiness relations on one edge (§19.2a). `api → core`, both in the plan, for an
engine that runs builds.

| Relation of `api → core` | `api`'s build may start                  | `api` publishes      |
|--------------------------|------------------------------------------|----------------------|
| `publish`                | after `core` is published and tagged     | after `core` does    |
| `build`                  | after `core`'s build has succeeded       | after `core` does    |
| `none`                   | at once, beside `core`'s build           | after `core` does    |

→ The plan, its versions and its publication order are identical in all three rows. If `core`'s publication fails and
`api` has no cause of its own, `api` is **blocked** (`W194`) in all three, including the `none` row where `api`'s
build has already finished: that build is not published and discharges nothing. If `api` has a fresh bump of its own
it **proceeds** in all three, its manifest naming `core`'s published baseline, and is owed `core`'s contribution until
`core` publishes (vector 80d). An engine that treats `none` as permission to publish `api` first, or that infers
`none` for an edge nobody declared it on, fails conformance.

**Vector 80c**: build order through a package that does not build. `app → ui → core`; `app` and `core` build in this
run and `ui` does not.

| `app → ui` | `ui → core` | Build constraint between `app` and `core`            |
|------------|-------------|------------------------------------------------------|
| `build`    | `build`     | `core`'s build before `app`'s                        |
| `build`    | `none`      | none: `ui`'s build reads nothing of `core`           |
| `none`     | `build`     | none: `app`'s build reads nothing of `ui`            |

→ In every row `core` publishes before `app` (§19.2, vector 80a). Adding build edges only between a package and its
direct providers in the plan loses the first row, exactly as inducing the publish graph on the plan loses vector 80a.

**Vector 80d**: a consumer proceeds past its failed provider. One commit `C1` carries two units, `feat(core)^: x` and
`feat(cli): y`; `cli` consumes `core`. Run 1: `core@1.5.0` fails to publish; `cli` has a cause of its own and
**proceeds** at its planned `2.1.0`, its manifest naming `core`'s baseline `1.4.0`, and is tagged at `HEAD`.

→ Run 2 plans `core@1.5.0` and, because `core` has not delivered `C1` to `cli` (§13.4a), `cli@2.1.1` as a **catch-up**
(`W193`), ordered after `core` and blocked if `core` fails again. Once both are tagged, `cli`'s baseline reaches
`core`'s release carrying `C1` and nothing is owed. Two implementations fail here: one that never plans `cli` again
because it released past `C1`, leaving it on `core@1.4.0` for ever with no diagnostic; and one that let `cli` publish
in run 1 naming `core@1.5.0`, a version that did not exist.

**Vector 81**: suppressing a catch-up from the consumer. `C1`: `feat(core)^: x`; run 1 publishes `core@1.5.0` and fails
on `cli`. Then a new commit `C2` lands.

| `C2`                                | Run 2 plan for `cli`                                                    |
|-------------------------------------|-------------------------------------------------------------------------|
| *(nothing)*                         | `2.0.1`, `W193` catch-up                                                |
| `cancel(cli): reset release state`  | **not released**; the contribution is discarded (§13.5a)                |
| `release(cli)` + `Release-As: none` | **not released**; `W154` reporting the withheld `2.0.1`                 |
| `release(cli)` + `Release-As: auto` | `2.0.1`; no active hold, so `W158` and an ordinary catch-up            |
| `fix(cli): y`                       | `2.0.1`; `max(patch, patch)`; one release, not two                     |
| `fix(cli)!: y`                      | `3.0.0`; `max(major, patch)`; `HEAD` moved, so G3 does not pin `2.0.1` |
| `cancel(*): reset release state`    | **not released**, and neither is anything else pending                  |

In every row `ui` and `api` (which published successfully in run 1) stay out of the plan. Suppressing one consumer's
catch-up MUST NOT disturb its siblings.

**Vector 82**: acting on the provider instead, after it has published. Same `C1` and same failed run 1.

| `C2`                                 | Run 2 plan for `cli`                                       |
|--------------------------------------|------------------------------------------------------------|
| `cancel(core): reset release state`  | `2.0.1`; still catches up. `W170`: nothing to discard     |
| `release(core)` + `Release-As: none` | `2.0.1`; still catches up; `core` is held for future work |

Both rows are the same rule: suppression reaches only **undischarged** work (§13.4a). `core@1.5.0` is public, so the
obligation it created for `cli` stands. An implementation that strands `cli` in the second row but not the first has
treated a hold as stronger than a `cancel`, inverting the ladder of §7.3, and fails conformance.

**Vector 82a**: the same hold, but on work the provider has **not** released. `C1`: `feat(core)^: x`; `C2`:
`release(core)` + `Release-As: none`; nothing published yet.

→ `core` is held and `cli` is **not** bumped: this work is undischarged, so the hold does suppress it. Together with
vector 82 this pins the boundary exactly at `discharged(P, C)`.

**Vector 82b**: a held provider with both discharged and undischarged work. `C1`: `feat(core)^: x` (published in run
1); `C2`: `release(core)` + `Release-As: none`; `C3`: `feat(core)^minor: y`.

→ `cli` receives `patch` (from `C1`, which `core` published) and **not** `minor` from `C3`, which it has not. One
package, one hold, two opposite answers, decided per unit by whether `core` released it.

**Vector 82c**: a consumer gets ahead of a held provider. `C1`: `feat(core)^: x`; `C2`: `release(core)` +
`Release-As: none`; `C3`: `fix(cli): y`. Run 1 releases `cli@2.0.1` on its own cause, `core` held, `cli`'s manifest
naming `core@1.4.0`. `C4`: `release(core)` + `Release-As: auto`.

→ Run 2 releases `core@1.5.0` and `cli@2.0.2` as a catch-up: `core` never delivered `C1` to `cli`, so `cli`'s release
past `C1` discharged nothing. Together with vector 82b this pins delivery on both sides of a hold: a held source
propagates nothing (82b), and a target that moved on while it was held is still owed the release (82c).

### B.7a Optimisation equivalence

These exist because §13.11 permits a conforming implementation to compute the plan by a faster route. Each is a case
where a plausible transformation of §9.2 or §13.8 changes the answer. An implementation that has not applied the
optimisation passes them trivially; one that has must still pass them.

**Vector 82c**: hoisting `resolvableBy` must not collapse a mixed source set. `core` on `stable` and `legacy` on
`beta`, both in one unit's scope-set: `feat(core,legacy)^: x`. `cli → core`, and a package `old → legacy` where `old` is
on `beta`.

→ Both `cli` and `old` are bumped. `srcChannels` is `{stable, beta}`; `cli` is admitted by the `stable` member and `old`
by the `beta` member. An implementation that hoists by picking a single representative channel from the source set (the
first, or the origin's) instead of the whole set, drops one of the two and fails here. The hoisted form of §9.3a is a
set, and the set may have more than one element.

**Vector 82d**: the hoist must be recomputed per unit, not per run. Two units in one commit, `feat(core)^: x` and
`feat(legacy)^: y`, with `core` on `stable` and `legacy` on `beta`, dependents as above.
Also let stable `app` depend only on `legacy`.

→ Each unit is admitted against its own sources: `cli` from the first, `old` from the second, and no bump for `app`
from the beta-only `legacy` source. A run-wide `{stable, beta}` set wrongly admits `app` because an unrelated unit
contributed `stable`. Graph traversal still stays per unit; a channel-cache defect cannot invent a missing edge.

**Vector 82e**: inverting `resolveChannels` must preserve §11.6 order. `cli` named by two commits, the older
`release(cli)%rc` and the newer `release(cli)%beta`, both in `W(cli)`.

→ `cli` takes `beta`; `W186` is raised because two candidates proposed. An implementation that builds the candidate list
by pushing from units in commit order and then reads it front-to-back takes `rc` and fails. The push MUST be in §11.6
order, or the read MUST sort (§13.8).

**Vector 82f**: the `W186` count is over **proposals**, not over candidates. `ui` is on `beta` and is named by two
commits in `W(ui)`: the newer `release(ui)%beta>stable`, and the older `release(ui)%rc>stable`.

→ `ui` graduates to `stable` from the newer directive, and there is **no** `W186`: the older directive is a candidate
but not a competitor, because `ui`'s baseline channel is `beta` and does not match its `<from>` of `rc` (§11.6). The
pair 82e/82f is what pins the counting rule: same shape, winner first in both, and the diagnostic differs only because
the trailing candidate proposes in one and not the other. An implementation that counts candidates rather than proposals
emits `W186` in both; one that counts only the prefix examined before the winner emits it in neither.

**Vector 82g**: skipping the channel pass must be observationally equivalent. Any workspace where no unit in the union
window sets `Propagate-Channel-Depth` above `0` and `propagation.channelDepth` is `0`.

→ Identical plan whether phase 1 runs or is skipped, provided §13.8 still resolves direct channel directives and uses
the baseline where none applies. Baseline-only initialization is valid only when direct proposals are also absent.
An implementation whose skip path leaves `channel(P)` unset can wrongly suppress propagated bumps; direct bumps need
not disappear. Vector 82h distinguishes the direct-channel case.

**Vector 82h**: skipping channel propagation must retain direct channel resolution. `core` is on `stable`; one pending
unit is `feat(core)^%beta: x`; `cli → core`; channel depth is `0` everywhere.

→ `core` releases on `beta`, while the propagated bump to stable `cli` is suppressed with `W208`. A fast path that
skips all of §13.8 because channel propagation depth is zero leaves `core` on `stable` and incorrectly bumps `cli`.

**Vector 82i**: units with the same directive and target window cannot be merged by unioning their sources. `alpha` is
on `stable`, `beta` is on `rc`, the pending units are `feat(alpha)^%%inherit++1: a` and
`feat(beta)^%%inherit++1: b`, and stable `app` depends on both.

→ The `alpha` unit proposes no channel change to `app`; the `beta` unit moves `app` to `rc`, and the contributions retain
their own origins and diagnostics. A merged source walk whose single inherited channel is chosen from `alpha` proposes
no move and is wrong; choosing `beta` happens to preserve the final channel but still loses per-unit diagnostics and
provenance. Inherited channel, admission and provenance remain per unit even when graph traversal is shared.

**Vector 82j**: equal directives do not imply equal admission. Two `feat(provider)^` units are at commits `C1` and
`C2`; consumer `app` depends on `provider`, and `C1 ∈ Wfresh(app)` while `C2 ∉ Wfresh(app)` because the histories
diverge around `app`'s stable baseline.

→ Only `C1` contributes to `app`. A bucket that admits the union once by window class and directive over-admits `C2`;
admission remains the per-unit test `commitOf(u) ∈ Wfresh(app)`.

**Vector 82k**: a no-op direct channel cannot hide an older proposal. `ui` has baseline channel `beta`, an older
`release(ui)%stable` and a newer `release(ui)%beta`, both in `Wfresh(ui)`.

→ The newer directive proposes nothing (`W199`); the older directive graduates `ui` to `stable`. There is no `W186`
because only one directive proposes a change. Returning the first candidate's value incorrectly leaves `ui` on beta.
Candidate-list and summary-based implementations of §13.8 MUST agree on this result and diagnostics.

**Cost workload (informative): shallow walks on a chain.** Let `p1 → p0`, `p2 → p1`, ..., `p(P-1) → p(P-2)`,
with one depth-one propagation unit for each package. With fixed edge kinds and no admission suppressions, there are
`P - 1` reached targets in total. Computing every unbounded single-source walk instead retains `P(P-1)/2` target
entries. Compare the same plans and provenance while measuring cold and warm query work, peak live memory and cache
metadata. This workload tests §13.11's cache tradeoff, not a wall-clock conformance threshold.

### B.8 Workspace graph constraints

**Vector 100**: a dependency cycle. Add `alpha` and `beta`, each listing the other under `dependencies`.

→ `E200`, repository-scoped, naming both packages and the field carrying each edge. The run aborts at §13.1, before any
plan is computed. Nothing is published, and the diagnostic is identical whether or not either package has a bump.

**Vector 100a**: the same two packages, but the mutual edges are under `devDependencies`.

→ No error. Those edges are in neither `propagation.kinds` nor `publish.orderKinds`, so they are not part of the graph
this rule constrains, and a test fixture depending back on the package it exercises stays legal.

**Vector 100b**: `alpha → beta → gamma → alpha`, a three-package cycle where only `gamma` has a bump.

→ `E200`. Acyclicity is a property of the workspace read at `HEAD`, not of the plan, so a cycle that this run would not
have touched still aborts it.

### B.9 The channel axis: `%%`, `++`, and transitions

Workspace of B.1 unless stated otherwise. Recall that `Propagate-Channel-Depth` defaults to `0`, so **no channel
propagates unless the unit says so**, and that `Propagate-Channel` defaults to `inherit`, so `++N` alone carries the
origin's own channel.

**Vector 94**: propagating a prerelease from a stable origin. `feat(core)^%%beta: x`.

→ `core` releases `1.5.0` on **stable**: its own channel is untouched by `%%`. Its direct dependents enter the beta
line and take the propagated patch: `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`, `api@1.2.1-beta.0`. `@acme/theme` and
`docs-site` are at depth 2 on both axes and are untouched. This is the case the operator exists for: ship the
dependency, let consumers validate the integration on a prerelease first.

**Vector 94a**: the same without the caret. `feat(core)%%beta: x`.

→ `core@1.5.0` stable; the three direct dependents move onto beta with **no** propagated bump, so each is a channel-only
release (`W202`) versioned by the channel-entry patch (`W204`): `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`,
`api@1.2.1-beta.0`. The versions coincide with vector 94 here because a propagated `patch` and a channel-entry `patch`
are the same size; they diverge as soon as the unit propagates anything larger.

**Vector 95**: a prerelease that keeps to itself. `feat(core)^%beta: x`.

→ **`core@1.5.0-beta.0` and nothing else.** The caret reaches `cli`, `ui` and `api`; every one of them is suppressed by
§9.3a and reported as `W208`, because a package on `stable` cannot resolve `core@1.5.0-beta.0` and republishing it would
produce an artefact identical to the one already published. This is the single most important vector in this section: an
implementation that releases the three dependents here has not implemented §9.3a, and will publish stable packages whose
manifests declare a range on a prerelease.

**Vector 95a**: taking the consumers along. `feat(core)^%beta++1: x`.

→ `core@1.5.0-beta.0`, `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`, `api@1.2.1-beta.0`. The channel axis puts them on the beta
line, so §9.3a admits the bump, so there is no `W208` and no `W204`. Compare vector 95: one three-character token is the
whole difference, and it is written in the commit.

**Vector 95b**: an established train needs no directives. Baselines `core@1.5.0-beta.0`, `cli@2.0.1-beta.0`; commit
`fix(core)^: x`.

→ `core@1.5.0-beta.1`, `cli@2.0.1-beta.1`. No channel directive appears anywhere: each package's channel comes from its
own baseline (§11.1), and `cli` is on `beta`, so §9.3a admits the bump. `ui` and `api` are on stable and are suppressed
with `W208`. Directives are needed at the boundaries of a train, not inside it.

**Vector 96**: the reverse. `feat(core)^%beta%%stable: x`.

→ `core@1.5.0-beta.0` and nothing else. `%%stable` proposes `stable` for three dependents that are already on `stable`,
so each is `W199` and nothing changes; the caret is then suppressed by §9.3a with `W208` exactly as in vector 95. Under
a specification where a propagated channel forced a release, this header published stable packages depending on a
prerelease; it now cannot.

**Vector 97**: a propagated `stable` MUST NOT graduate. Let `api` be at `1.2.1-rc.0` with stable baseline `api@1.2.0`.
Commit: `feat(core)^%%stable: x`.

→ `api` is **not** graduated. It keeps channel `rc`, and `W200` reports the suppression. `cli` and `ui`, both on stable
already, get `W199` for the redundant channel and take ordinary stable patches from the caret. An implementation that
graduates `api` here has ended a prerelease train on behalf of a commit that never mentioned it, and fails this vector.

**Vector 97a**: the same, with the `stable` arriving by inheritance rather than by `%%`: `feat(core)++1: x`, where
`core` is on stable and `propagation.channel` is `inherit`.

→ The same outcome for `api` and the same `W200`. The prohibition is on the *propagated value*, not on the syntax that
produced it. This header has no caret, so `cli` and `ui` take `W199` and no release, as in vector 39a.

**Vector 97b**: the deliberate exception. Same baselines; commit `release(core)%%rc>stable++1: x`.

→ `api` **is** graduated, to `1.2.1`. The transition names the train it ends, which is the whole basis on which §9.3
permits it. `cli` and `ui` are on `stable` and do not match `<from>`, so they are untouched: no `W199`, no `W185`, no
release. Note that the unit's type is `release`, whose bump is `none`: the channel axis does not require a bump, which
is what makes this shape possible at all (§7.2).

**Vector 97c**: idempotence. Run vector 97b again, at the same `HEAD`, after it succeeded.

→ Empty plan. `api`'s baseline is now `1.2.1`, so `channelOf(baseline(api))` is `stable` and it no longer matches the
`<from>` of `rc`. The commit is still inside `W(api)`, since the window is measured from the last stable tag and `api` has
just written one, so in fact it is not; but even where a package's window still contains the commit, `W199` and the
`<from>` test are what terminate the axis (§13.7c G7). An implementation that matches transitions against the channel it
computed earlier in the same run re-releases `api` on every run for ever.

**Vector 97d**: partial graduation. `cli` at `2.1.0-beta.4`, `ui` at `0.9.2-beta.1`, `api` already graduated to
`1.2.1`. Commit: `release(core)%stable%%beta>stable++*`.

→ `core` graduates directly; `cli` and `ui` graduate by transition; `api` is on `stable`, does not match, and is not
touched: no error, no redundant release, and no need for the author to know which packages had already been done.
`@acme/theme` and `docs-site` are on stable too and are likewise untouched. This is the case the transition form exists
for.

**Vector 97e**: excluding a package from graduation. Same as 97d plus:

```
Propagate-Channel-Scope: *, -ui
```

→ `core` and `cli` graduate. `ui` stays on `beta` at `0.9.2-beta.1`, unreleased, and will graduate whenever a later
directive names it. Exclusion leaves a package on its line: it does not release, it is not an error, and it produces no
diagnostic beyond the plan simply not containing it.

**Vector 97f**: excluding from the unit's own packages. `release(@acme/*,-@acme/theme)%beta>stable`.

→ Only the matching `@acme/*` packages other than `@acme/theme` are considered at all. The scope-set excludes from the
unit; `Propagate-Channel-Scope` excludes from what the unit reaches (§8.5a). Both use the same `-` operator and the same
scope-set grammar.

**Vector 98**: redundancy. `feat(core)^%%stable: x` where every dependent is already on stable.

→ Ordinary stable patches from the caret, plus `W199` on each redundant channel proposal.

**Vector 98a**: graduating a consumer while its provider stays on a train. `core` at `1.5.0-beta.2` with no directive;
commit `release(cli)%beta>stable`.

→ `cli` graduates. Its manifest is reconciled against `core`'s current version, which is a prerelease, so the published
stable `cli` declares a range admitting `core@1.5.0-beta.2` and `W203` is raised naming both (§9.4). Permitted,
reported, and almost always a mistake; graduate `core` too.

**Vector 99**: parsing.

| #   | Header                                                      | Expected                                                                                                    |
|-----|-------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| 99a | `feat(core)%%beta: x`                                       | `Propagate-Channel: beta`, `Propagate-Channel-Depth: 1`; own channel untouched                              |
| 99b | `feat(core)%beta%%rc: x`                                    | `Channel: beta` **and** `Propagate-Channel: rc`; distinct sigils                                           |
| 99c | `feat(core)%%: x`                                           | `E111`; a bare `%%` has no meaning, unlike `^^`                                                            |
| 99d | `feat(core)%%beta%%rc: x`                                   | `E110`; one `%%` per header                                                                                |
| 99e | `feat(core)%%%beta: x`                                      | `E110`; third percent sign                                                                                      |
| 99f | `feat(core)%%Beta: x`                                       | `E181`; propagated channel names obey §11.2                                                                |
| 99g | `feat(core)%%latest: x`                                     | `E180`; reserved                                                                                           |
| 99h | `feat(core)%%beta: x` + footer `Propagate-Channel: rc`      | `E112`; lenient mode takes the footer with `W112`                                                           |
| 99i | `docs(core)%%beta++1: x`                                    | Channel propagates; the type maps to `none`, so no bump does. Dependents are channel-only releases (`W202`) |
| 99j | `feat(core)++1: x` + footer `Propagate-Channel-Depth: 3`    | `E112`; `++1` and the footer set one key to different values; lenient: footer wins, `W112`                 |
| 99k | `feat(core)%%beta: x` + footer `Propagate-Channel-Depth: 3` | Accepted, channel depth `3`. `%%` supplies a depth only in the absence of an explicit one (§8.3a)           |
| 99l | `feat(core)%%none++*: x`                                    | No channel propagation; `W152` for the redundant pairing                                                    |
| 99m | `feat(core)%%beta++0: x`                                    | No channel propagation; `W201` **alone**, never `W152` (§8.3b). Mirrors #35a on the bump axis               |
| 99n | `feat(core)%beta>stable%%beta>stable++*: x`                 | Legal. Direct transition on `core`, propagated transition on its closure                                    |
| 99o | `feat(core)%%stable>beta++2: x`                             | Dependents within two edges that are on `stable` enter the `beta` line                                      |
| 99p | `feat(core)%%beta>beta: x`                                  | `W207`, inert                                                                                               |

### B.10 Diagnostics not exercised elsewhere

Every diagnostic in §16 is reachable, and the sections above cover most of them in context. The remainder are collected
here so that no code in the registry is left without a test. Each row is a complete input against the workspace of B.1
unless stated otherwise.

| #   | Input                                                                                   | Expected                                                                                                                                                   |
|-----|-----------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 83  | A commit message containing an invalid UTF-8 byte sequence                              | `E001`, message-scoped: the commit contributes nothing (§16).                                                                                              |
| 84  | A commit message that is empty or whitespace only                                       | `E002`, message-scoped.                                                                                                                                    |
| 85  | `feat(core)^minor: x` with a footer `Propagate: major`                                  | `E112`; inline and footer set one key to different values (§5.3).                                                                                         |
| 85a | the same under `lenient: true`                                                          | Accepted, footer wins, `W112`.                                                                                                                             |
| 85b | `feat(core)^minor: x` with a footer `Propagate: minor`                                  | Accepted, `W110`; redundant restatement, not a conflict.                                                                                                  |
| 85c | `feat(core)^: x` with a footer `Propagate-Depth: 3`                                     | `E112`; `^` sets `Propagate-Depth: 1`; the chain of §8.3 does not resolve it.                                                                             |
| 85d | the same under `lenient: true`                                                          | Accepted, depth `3`, `W112`; the footer wins, never the inline (§8.3).                                                                                    |
| 86  | `frobnicate(core): x` with `strictTypes: false`                                         | Bump `none`, `W140`.                                                                                                                                       |
| 86a | the same with `strictTypes: true`                                                       | `E140`.                                                                                                                                                    |
| 87  | `Feat(core): x` with `lenient: false`                                                   | `E101`.                                                                                                                                                    |
| 87a | the same with `lenient: true`                                                           | Type lowercased, `W101`.                                                                                                                                   |
| 87b | `feat(core):x` with `lenient: true`                                                     | Accepted, description `x`, `W121`; the lenient form of `E120` (§5.5).                                                                                     |
| 87c | `feat(core):  x` with `lenient: true`                                                   | `E120` still; two spaces are not recoverable, lenient or not.                                                                                             |
| 88  | `release(core): x` + `Release-As: 5.0.0`, computed `1.5.0`, **no configuration at all** | `E157`; `maxMajorJump` defaults to `1` and is enforced by default (§14.1); the pin exceeds the computed version by more than one major.                   |
| 88c | the same with `maxMajorJump: null`                                                      | Accepted at `5.0.0`; the bound is the one default-enforced limit that may be disabled.                                                                    |
| 88a | the same with `Release-As: 2.0.0`                                                       | Accepted; one major above the computed version is within the bound.                                                                                       |
| 88b | `release(core): x` + `Release-As: 1.5.0`, computed `2.0.0`, `lenient: true`             | Accepted at `1.5.0`, `W159`; the lenient form of `E156` (§8.6). The pin is above the baseline `1.4.2`, so `E153` does not arise.                          |
| 89  | `feat(core)%latest: x`                                                                  | `E180`; `latest` is reserved (§11.2).                                                                                                                     |
| 89a | `feat(core)%Beta: x`                                                                    | `E181`; channel names are lowercase.                                                                                                                      |
| 90  | `feat(core): x` with a footer `X-Internal-Ticket: AB-1`                                 | Footer ignored, `W150`. Unknown keys never block (§17.3).                                                                                                  |
| 91  | `feat(core)%%beta: x`, then a later `feat(core)%%rc: y`, both in `cli`'s window         | Newest commit wins; `cli` enters `rc`; `W160` naming both. Note `%%`, not `%`: `%` sets the unit's **own** channel and never reaches a dependent (§8.3a). |
| 92  | One commit containing `cancel(core)` and `feat(core)` (either order)                    | `W172`, **non-suppressible**: the `feat` is discarded by ancestor-or-self and `core` is not released (§10.3, D.6).                                         |
| 93  | `release(core)%stable: x`, baseline `1.5.0-beta.2`, no pending bumps                    | `E185`, **repository-scoped**; the run aborts; graduation would not raise the version (§11.5, §16).                                                       |
| 93a | `feat(core)^%beta: x`, dependents on stable                                             | `W208` per suppressed dependent, **non-suppressible**; the caret reached and could not oblige (§9.3a).                                                    |
| 93b | `feat(core)%%beta: x`, dependents on stable                                             | `W202` per dependent, **non-suppressible**, plus `W204` for each channel-entry patch.                                                                      |
| 93c | `feat(core)%%beta++0: x`                                                                | `W201` and nothing else; a channel value with depth `0` reaches nobody; `W152` is superseded (§8.3b).                                                     |
| 93h | `feat(core)^minor+0: x`                                                                 | `W201` and nothing else; the bump-axis mirror of 93c.                                                                                                     |
| 93d | `feat(core)%%beta++*: x` with `Propagate-Channel-Scope: -*`                             | `W205`; the channel scope excluded every reached dependent.                                                                                               |
| 93e | `release(core)%%zeta>stable++*: x`, no dependent on `zeta`                              | `W206`; the transition matched nothing; the usual cause is a mistyped `<from>`.                                                                           |
| 93f | `release(core)%beta>beta: x`                                                            | `W207`, inert.                                                                                                                                             |
| 93g | `release(cli)%beta>stable: x` while `core` stays at `1.5.0-beta.2`                      | Graduates `cli`; `W203` naming `cli@2.1.0` and `core@1.5.0-beta.2` (§9.4).                                                                                 |

Vector 92 is the one to implement first of these: `W172` is non-suppressible precisely because the commit looks like it
does something and does nothing, and it is the most likely authoring mistake with `cancel`.

### B.11 Corrections: `Edits`, `Deletes`, and `Reverts`

Parsing vectors. Each message is a header, a blank line, and the footer shown:

| #    | Header + footer                                       | Expected                                                                                   |
|------|-------------------------------------------------------|---------------------------------------------------------------------------------------------|
| 111a | `fix(core): x` + `Edits: 4f2a1c9`                     | Valid; correction target sha `4f2a1c9`, no selector                                        |
| 111b | `fix(core): x` + `Edits: 4f2a1c9#2`                   | Valid; unit selector `2`                                                                   |
| 111c | `feat(core): x` + `Edits: *`                          | Valid; wildcard restatement                                                                |
| 111d | `chore(core): x` + `Deletes: *`                       | Valid; wildcard deletion, carrying bump `none`                                             |
| 111e | `fix(core)!: x` + `Edits: 4f2a1c9`                    | Valid; the restatement is breaking                                                         |
| 111f | `fix(core): x` + `Edits: 4f2a1c9` + `Deletes: abcdef0`| Valid; one unit, two corrections                                                           |
| 111g | `fix(core): x` + `Edits: XYZ`                         | `E151`, not a sha                                                                         |
| 111h | `fix(core): x` + `Edits: 4F2A1C9`                     | `E151`; lowercase hexadecimal only                                                        |
| 111i | `fix(core): x` + `Edits: 4f2a1`                       | `E151`; shorter than 7 characters                                                         |
| 111j | `fix(core): x` + `Edits: 4f2a1c9#0`                   | `E151`; selectors are 1-based                                                             |
| 111k | `fix(core): x` + `Edits: 4f2a1c9#02`                  | `E151`; no leading zeros                                                                  |
| 111l | `fix(core): x` + `Edits: *#1`                         | `E151`; the wildcard takes no selector                                                    |
| 111m | `release(core)%stable: x` + `Edits: 4f2a1c9`          | `E173`; correction footer on a `release` unit                                             |
| 111n | `cancel(core): x` + `Deletes: *`                      | `E171`; `cancel` takes a scope-set and nothing else                                       |
| 111o | `fix(core): x` + `Reverts: not-a-sha`                 | Valid; `W214`, the footer is informational                                                 |
| 111p | `revert(core): undo x` + `Reverts: 4f2a1c9`           | Valid; bump `patch`, sha recorded                                                          |

Run sequences, from `core@1.4.2` in the workspace of B.1. `A` is `feat(core)!: rewrite internals`:

**Vector 112**: restatement before release. `A`, then `B`: `fix(core): rewrite internals` + `Edits: <A>`.

→ `core` releases `1.4.3`, not `2.0.0`; one changelog entry, `B`'s. `A`'s record left the window when the correction
applied (§13.4b).

**Vector 113**: restatement after release. `A` published as `core@2.0.0`, then `B` lands.

→ `W209`, non-suppressible: `A` is discharged, and `2.0.0` is published history. `B`'s own `patch` still applies, so
`core` plans `2.0.1` with `B`'s entry alone.

**Vector 114**: precedence. `A`, then `B`: `chore(core): drop it` + `Deletes: <A>`, then `C`:
`fix(core): rewrite internals` + `Edits: <A>`.

→ The newest correction of `A` wins: `core` releases `1.4.3` with `C`'s entry, and `B` reports `W210`.

**Vector 115**: scope agreement. `A` is scoped `(core)`; `B`: `fix(core,cli): x` + `Edits: <A>`.

→ `E213`; `B` contributes nothing, `A` stands.

**Vector 116**: wildcard reach. Pending records exist for `core` and `cli`; `B`: `chore(core): restate` +
`Deletes: *`.

→ Only `core`'s pending records are discarded; `cli`'s stand. The wildcard's reach is the carrying unit's resolved
scope-set (§7.4.2).

**Vector 117**: revert suppression. `A`: `feat(core): streaming reader`; `B`: `revert(core): undo streaming reader`
+ `Reverts: <A>`; both in `W(core)`.

→ `core` releases at `max(minor, patch)` = `1.5.0` (§7.3), and its changelog carries neither `A`'s entry nor `B`'s;
`W212` accounts for both.

**Vector 118**: partial correction. `A` is `feat(*): platform bump`; `B`: `chore(core): drop it for core` +
`Deletes: <A>`.

→ `A`'s record is discarded for `core` alone. `cli`, `ui`, `api`, `docs-site` and `@acme/theme` keep their pending
`minor` from `A` and release normally; `core` does not release. A scope-set inside the target's set narrows, and
only a package outside it is `E213` (§7.4.2).

**Vector 119**: undo of a correction. `A`: `feat(core)!: rewrite internals`; `B`: `fix(core): rewrite internals` +
`Edits: <A>`; `C`: `chore(core): restore the original` + `Deletes: <B>`.

→ `B` is void (`W215`): its `fix` record is discarded and its discard of `A` never applies, so `core` releases
`2.0.0` with `A`'s original record, exactly as if `B` had never landed.

**Vector 120**: nested deletes. `A`: `feat(core): x`; `B`: `chore(core): drop it` + `Deletes: <A>`; `C`:
`chore(core): restore it` + `Deletes: <B>`.

→ `B` is void (`W215`), so `A`'s record stands and `core` releases `1.5.0`. A delete of a delete restores.

**Vector 121**: editing a correction. History of vector 119, but `C` is `docs(core): note the rewrite` +
`Edits: <B>`.

→ `B` is void: `A`'s record returns, and `C`'s `docs` record stands as the restatement of `B`'s. `core` releases
`2.0.0`, because `A`'s `major` is back in the window and `docs` maps to `none`. To correct the
restatement, use `Edits: <A>` again, which supersedes `B` directly (`W210`, vector 114).

### B.12 Shared-version groups

These vectors exercise §13.9a and are REQUIRED only of an engine that offers shared-version groups. Every one of them
uses one group `G` of three packages, `a`, `b` and `d`, at shared depth `d = 2` unless stated otherwise, and each
states the group's rule as `(counter, channels)`. The numbers are the case numbers of §15.8; a case with no vector of
its own is one the table states completely.

**Vector 139**: `(fixed, fixed)`, the default. Stable baselines `a@1.10.3`, `b@1.10.0`, `d@1.10.0`; commit `C1` is
`feat(a,b,d)%rc: start the train` and every member is tagged `1.11.0-rc.0` at it. Commit `C2` is `fix(a,b,d): a shared
fix`, and only `a` published `1.11.0-rc.1` at it before the run died. Re-run at the same `HEAD`.

→ The group sits on a shared train and its counter is shared, so the train is the group's: **`a`, `b` and `d` all
release `1.11.0-rc.2`**, `a`'s as a ride. The counter advanced, which is what §13.9a says `G3` does not cover under
this axis.

**Vector 140**: the same history and tags under `(independent, fixed)` and under `(independent, independent)`.

→ **`b` and `d` release `1.11.0-rc.1`; `a` releases nothing.** Each member continues its own counter from its own
baseline, and `a` has already published exactly this work. Adding `fix(d): repair` on top changes nothing about `a`.

**Vector 141**: `(independent, *)`, the floor. Same baselines and tags as vector 140. `b`'s own pending window carries
only the shared `fix`, so `applyBump(1.10.0, patch)` is `1.10.1`.

→ `target` is raised to `1.11.0`, the core of the group's line, and `b` releases **`1.11.0-rc.1`**. Without the floor
the computed `1.10.1-rc.0` would raise `E195` against `b`'s own baseline `1.11.0-rc.0`.

**Vector 142**: any rule, the graduation retry. Stable baselines all `1.10.0`; `C1` is `feat(d)%rc: start`, which
takes `d` to `1.11.0-rc.0`, and `a` and `b` ride with it; `C2` is `release(a,b,d)%rc>stable: graduate`, and only `a`
published `1.11.0` before the run died. Re-run at the same `HEAD`.

→ **`b` and `d` release `1.11.0`.** `d`'s own window still carries the feature, so §11.5 computes `1.11.0` for it
directly. `b`'s window carries no bump at all, since the feature was `d`'s, so §11.5 computes `1.10.0` for it and the
floor raises it to the line. **No `E185`.**

**Vector 143**: any rule. `a` and `d` are on `1.11.5` on `stable`; `b` was tagged `1.11.3-rc.0` by hand, a patch above
the group's line `1.11.0`; `C1` is `release(b)%rc` and `C2` is `release(b)%rc>stable`.

→ **`E185` against `b`.** The floor raises the graduation to `1.11.0` and no further, so a baseline nothing in the
group explains still fails. Where the hand-edited tag is itself the group's baseline, the same guard fires against the
group's own computation instead, and being repository-scoped it aborts the run before any member is assigned.

**Vector 144**: `(fixed, fixed)`. `a` and `d` are on `1.11.0-rc.0`; `b` is a sparse member resting on `1.10.0`; the
pending commit is `fix(d): more train work`.

→ **`d` and `a` release `1.11.0-rc.1`**, `a`'s as a ride; **`b` releases nothing**. `b`'s `stable` is where its own
tags put it, not a request to end the train, so the group does not graduate.

**Vector 145**: `(fixed, fixed)` at `d = 3`. `a` and `d` are on `1.11.0`; `b`'s leg failed and left it on
`1.11.0-rc.0`; nothing is pending.

→ **`b` releases `1.11.0` as a ride; `a` and `d` release nothing.** The group stays graduated, and no channel-entry
patch (`W204`) arises, because nothing proposed a channel at all.

**Vector 147**: any rule. The state of vector 139, plus `C3` = `feat(d)!: a breaking change`.

→ **Every member releases `2.0.0-rc.0`**, `a`'s and `b`'s as rides. The shared prefix moved, so the group engages under
every axis, and the new core restarts every counter at `0`.

**Vector 148**: `(independent, independent)`. The state of vector 139, plus `C3` = `release(d)%rc>stable: graduate d`.

→ **`d` releases `1.11.0`; `b` releases `1.11.0-rc.1` for its own failed leg; `a` releases nothing.** No channel
conflict is reported, because no channel is forced on anybody.

**Vector 149**: `(independent, independent)`. `a` and `d` are on `1.11.0-rc.0`; `b` has never left `1.10.0` on
`stable`; the pending commit is `feat(d)!: a breaking change`.

→ **`b` releases `2.0.0-rc.0` on the `rc` line**, not `2.0.0`. A ride is never the first stable publication of a core
the group holds only as a prerelease.

**Vector 150**: `(independent, independent)`. `a` and `d` are on `1.11.0` on `stable`; `b` sits on `1.11.0-rc.5`; the
pending commit is `feat(d): a feature on the stable line`.

→ **`a` and `d` release `1.12.0`; `b` releases `1.12.0-rc.0`.** A ride never graduates a member.

**Vector 153**: `(independent, *)`. `a` and `b` are on `1.11.0-rc.0`, `d` on `1.11.0-rc.1`, and nothing is pending.

→ **Empty plan.** Every member holds the line's shared prefix, so none of them is a laggard, whatever its counter says.
Under `(fixed, fixed)` the same state releases `a` and `b` at `1.11.0-rc.1` instead.

**Vector 154**: `(independent, independent)`. `a` and `d` ran a train two prereleases deep to `1.11.0-rc.1`; `b` never
joined it and is on `1.10.0` on `stable`; the pending commit is `fix(b): b's own first change`.

→ **`b` releases `1.11.0-rc.0`**, on the `rc` line and not as a ride: its own change is what puts it in the plan, the
shared prefix is what decides its core, and its own counter is what starts the line. `a` and `d` release nothing.

---

## 23. Appendix C: Formal grammar (ABNF)

The SHA productions below describe the default Git profile. External revision operands use the exact-token/quoted-string
profile in §25. `rollback` fits the existing `type` production; its footer names fit the general footer-key production,
but §26 imposes additional semantic constraints. `Rollback-Version` uses exact SemVer; `Rollback-Cancel` combines a
full revision operand and the existing one-based `unit-no` selector. Neither is an ordinary bump or correction.

Blank lines adjacent to a separator are discarded before this grammar applies (§4.2), and the input is normalised per
§4.1.

```abnf
message         = unit *( LF separator LF unit )
separator       = 3*VCHAR                    ; configured; default "---"

unit            = header [ LF LF body ] [ LF LF footers ]

header          = type [ "(" scope-set ")" ] inline-directives [ "!" ] ": " description
type            = 1*LOWER
scope-set       = scope-term *( "," [ SP ] scope-term )
scope-term      = [ "-" ] 1*scope-char
scope-char      = %x21-27 / %x2A-2B / %x2D-39 / %x3B-FF   ; excludes SP ( ) , :

inline-directives = *( deep-tok / deep-channel-tok / deep-depth-tok
                       / propagate-tok / depth-tok / channel-tok )
                                              ; the three doubled tokens MUST be
                                              ; attempted before their single-sigil
                                              ; counterparts, and a third repetition
                                              ; of any sigil is E110
deep-tok        = "^^" [ propagate-val ]      ; implies Propagate-Depth: all
propagate-tok   = "^" [ propagate-val ]       ; implies Propagate-Depth: 1
propagate-val   = "none" / "patch" / "minor" / "major" / "inherit"
depth-tok       = "+" depth-val
deep-depth-tok  = "++" depth-val              ; Propagate-Channel-Depth; value REQUIRED
depth-val       = "*" / "all" / "direct" / 1*DIGIT
deep-channel-tok = "%%" deep-channel-val      ; Propagate-Channel; implies depth 1
deep-channel-val = "inherit" / "none" / channel-val
channel-tok     = "%" channel-val
channel-val     = [ from-channel ">" ] to-channel
from-channel    = "*" / "stable" / channel-name   ; "*" is any prerelease, never stable
to-channel      = "stable" / channel-name         ; "*" is NOT a to-channel
channel-name    = LOWER *( LOWER / DIGIT / "-" )  ; ">" is excluded by construction

description     = %x21-FF *( %x20-FF )      ; no LF; never begins with a space (§5.5)

body            = *( TEXT LF )               ; free form, paragraphs separated by LF LF

footers         = footer *( LF footer )
footer          = footer-key ( ": " / " #" ) footer-value *( LF continuation )
footer-key      = "BREAKING CHANGE" / "BREAKING-CHANGE" / 1*( ALPHA / DIGIT / "-" )
                                             ; the two literals are case-SENSITIVE;
                                             ; the generic form is case-insensitive
footer-value    = *( %x20-FF )               ; MAY be empty (BREAKING CHANGE -> W157)
continuation    = 1*WSP 1*( %x20-FF )

correction-value = "*" / sha [ "#" unit-no ] ; the value grammar of Edits and Deletes (§7.4.1)
sha              = 7*64LHEX                  ; full or abbreviated commit id, lowercase
unit-no          = NZDIGIT *DIGIT            ; 1-based, no leading zeros
LHEX             = DIGIT / %x61-66
NZDIGIT          = %x31-39

tag             = package-name "@" semver    ; split at the LAST "@"

LOWER           = %x61-7A
```

---

## 24. Appendix D: Worked examples

### D.1 Ordinary feature with controlled blast radius

```
feat(@acme/core)^: add streaming reader

The buffered reader is retained; the streaming path is opt-in via
`createReader({ stream: true })`.
```

`@acme/core` gets a minor bump; its **direct** consumers get a patch, because `^` sets depth `1` and `Propagate`
defaults to `patch`. Nothing further down the graph moves.

The single caret is the whole decision, and it is worth being deliberate about. Without it the commit releases
`@acme/core` alone: correct when consumers declare a compatible range and will pick the new version up on their next
install, and wrong when they bundle it. Writing `^patch+1` is legal and exactly equivalent; reach for the longer form
only when you mean something other than "bump my direct consumers a patch".

Note what is absent: no channel directive. `@acme/core` is on `stable`, its consumers are on `stable`, and a channel is
derived from each package's own baseline (§11.1), so there is nothing to say. The channel axis is written only at the
boundaries of a prerelease train (§11.7).

### D.2 Breaking change that must reach everything

```
refactor(@acme/core)^^inherit!: remove the v1 plugin interface

BREAKING CHANGE: `registerPlugin` is gone. Use `plugins: []` in the
config object. The codemod at tools/codemods/plugins-v2 handles the
mechanical part.
```

`@acme/core` goes major; every transitive dependent goes major, because `inherit` copies this unit's bump. Every
consumer of the workspace sees an accurate signal.

Both parts are load-bearing here. With a single caret only direct consumers would move, leaving depth-2 packages
advertising compatibility they no longer have; with no caret at all nothing beyond `@acme/core` would move. Without
`inherit` the dependents would take the default `patch`, which understates a removed interface. This is the case the
conservative defaults are designed to make you write out.

`^^inherit` and `^inherit+*` are the same directive. The doubled form is preferred in a header this dense: it is one
character shorter and keeps the depth idea attached to the propagation idea instead of trailing after it.

### D.3 Squash-merged pull request

```
feat(@acme/api): add cursor pagination

---

fix(@acme/api): reject negative page sizes

---

test(@acme/api): cover cursor edge cases

---

docs(docs-site): document pagination
```

`@acme/api` = one minor release (max of minor, patch, none). `docs-site` = no release. One commit, four accurate
changelog entries.

### D.4 Prerelease train

```
# commit 1
feat(@acme/core,@acme/cli)^%beta++1: new config loader

# commit 2
fix(@acme/cli)%beta: handle missing config file

# commit 3
feat(@acme/core)!^%beta: config file format v2

BREAKING CHANGE: `config.json` is replaced by `acme.config.js`.

# commit 4
release(@acme/core,@acme/cli)%beta>stable: ship 2.0
```

From `core@1.4.2`, `cli@2.0.0`:

| Commit | `@acme/core`                                                                 | `@acme/cli`                                               |
|--------|------------------------------------------------------------------------------|-----------------------------------------------------------|
| 1      | `1.5.0-beta.0`                                                               | `2.1.0-beta.0`                                            |
| 2      |; no release; `cli → core`, so a `cli` fix does not reach `core`             | `2.1.0-beta.1`                                            |
| 3      | `2.0.0-beta.0`; target recomputed from `1.4.2` with a `major` in the window | `2.1.0-beta.2`; propagated `patch`, target still `2.1.0` |
| 4      | `2.0.0`                                                                      | `2.1.0`                                                   |

Three things in that sequence are worth reading carefully.

**Commit 1 carries `++1` for the consumers it does not name.** Both packages of its scope-set take `%beta` and their
own `minor` directly, so `core` and `cli` enter the line whatever else is written, and the caret never reaches `cli`,
because a unit does not propagate to its own packages (§9.2). The caret and the `++1` concern every *other* direct
consumer of the two. The caret reaches them, and §9.3a would then suppress the bump, because a consumer still on
`stable` cannot resolve `core@1.5.0-beta.0`; `++1` moves them onto the line in the same commit, and the suppression no
longer applies. This is the boundary of the train and it is the one place a channel directive is needed on the way in.
Written as `feat(@acme/core)^%beta`, with `cli` unnamed and no `++1`, the commit would have released `core` alone
(§11.7).

**Commit 3 needs nothing but the caret.** `cli` is already on `beta`, so its channel comes from its own baseline (§11.1)
and §9.3a admits the propagated bump because origin and target are on the same line. No `%%`, no `++`, no repetition of
`%beta`. An established train is directive-free.

**Commit 4 uses a transition rather than `%stable`.** Both work here, because both packages are on `beta`. The
transition is preferred because it is idempotent: if `cli` had already been graduated by hand, or by a run that failed
after `cli` and before `core`, `%stable` would emit `W185` for it and `%beta>stable` simply would not match it. Written
as `release(@acme/*)%beta>stable`, the same commit graduates whatever is still on the line without naming the packages
at all.

Propagation flows from a dependency to its dependents only; the edge direction is never reversed. Without the caret,
commit 3 would have released `@acme/core` alone.

### D.4a Graduating a train that is already half-graduated

A 2.0 train has been running for six weeks across `@acme/core`, `@acme/cli`, `@acme/ui` and `@acme/theme`. `@acme/ui`
was graduated a fortnight ago to unblock a downstream team, and `@acme/legacy-adapter`, which also depends on `core`,
must stay on `beta` because its replacement ships next quarter.

The commit is one unit:

```
release(@acme/core)%beta>stable%%beta>stable++*: graduate the 2.0 train

Every package still on the beta line moves to stable. `@acme/ui` graduated
already and is untouched; `@acme/legacy-adapter` stays on beta until its
replacement lands.

Propagate-Channel-Scope: @acme/*, -@acme/legacy-adapter
```

What each piece does:

| Piece                     | Effect                                                                                  |
|---------------------------|-----------------------------------------------------------------------------------------|
| `release`                 | No bump. The channel axis does not need one (§7.2); nothing here claims code changed.   |
| `%beta>stable`            | Graduates `core` itself, and only if it is still on `beta`.                             |
| `%%beta>stable`           | Proposes the same transition for the dependents the channel axis reaches.               |
| `++*`                     | Reaches the whole transitive closure of dependents. Without it the reach would be 1.    |
| `Propagate-Channel-Scope` | Excludes `@acme/legacy-adapter`, and confines the whole thing to the `@acme` namespace. |

The plan:

```
  @acme/core        2.0.0-beta.7  -> 2.0.0    beta -> stable   direct
  @acme/cli         2.1.0-beta.4  -> 2.1.0    beta -> stable   channel from @acme/core   W202
  @acme/theme       1.4.0-beta.2  -> 1.4.0    beta -> stable   channel from @acme/core   W202
```

`@acme/ui` is absent because it is on `stable` and does not match `<from>`. `@acme/legacy-adapter` is absent because the
channel scope excludes it. Neither absence produces a diagnostic, neither required the author to know the current state
of either package, and re-running the same commit after a partial failure plans exactly the packages that did not
publish; the transition stops matching the ones that did (§13.7c G7).

Two mistakes this shape avoids. Writing `%%stable` instead of the transition would have graduated nothing at all:
`W200` suppresses every implicit graduation, precisely so that a directive aimed at one package cannot end another
package's train (§9.3). Writing `%stable` on a hand-maintained scope-set, say `release(@acme/core,@acme/cli,@acme/theme)`,
would work today and be wrong next week, because the set that is still on `beta` changes on every run.

### D.5 Adopting CCME on a repository with imported history

```
cancel(*): reset release state

The importer classified 4,100 pre-2024 commits heuristically. Those
classifications are discarded. Published tags remain authoritative;
nothing is rewritten.
```

Then, immediately:

```
release(@acme/core)%stable: re-baseline at current tag
```

The second commit is optional and only needed if a package's manifest and tag disagree.

### D.6 Recovering from a mistaken commit on a protected branch

Three commits ago someone pushed `feat(@acme/core)!: refactor internals`. The change is neither breaking nor a feature,
the branch is protected, and history cannot be rewritten.

**Preferred form, one commit, a correction (§7.4):**

```
fix(@acme/core): refactor internals

The change is a refactor with a defensive fix, not a breaking feature.

Edits: 4f2a1c9
```

The mistaken record is discarded and this unit stands in its place: `@acme/core` gets a patch, not a major, the
changelog carries this description, and the rest of the pending ledger is untouched. Corrections reach only proper
ancestors, so the restatement in the same commit is safe by construction.

**Alternative form, two commits, a barrier:**

```
# commit N  (empty commit)
cancel(@acme/core): reset release state

# commit N+1
fix(@acme/core): refactor internals

Restating the change correctly. The metadata from 4f2a1c9 is cancelled
by the preceding commit.
```

Same outcome for this history, but the barrier discards **everything** pending for `@acme/core`, not just the
mistaken record. Use it when the whole ledger is wrong; use `Edits:` when one record is.

Result of either: `@acme/core` gets a patch, not a major. No history was rewritten and no tag was touched.

**The mistake to avoid, in one commit:**

```
cancel(@acme/core): reset release state

---

fix(@acme/core): refactor internals
```

The barrier is *ancestor- **or-self***, so the `fix` is in the barrier commit and is discarded along with everything
before it: the package is not released at all. Unit order within the commit is irrelevant.

This is the single most likely authoring mistake with `cancel`. Linters MUST emit `W172` when a commit contains a
`cancel` unit alongside any bump-producing unit with an overlapping scope.

### D.7 Excluding an internal app from a workspace-wide change

This repository ships compiled artefacts, so a toolchain upgrade genuinely changes what consumers install. That is a
standing fact about the repository, so it belongs in configuration:

```json
{
  "types": {
    "build": "patch"
  }
}
```

The commit then says what it is, and the release follows:

```
build(*,-docs-site,-e2e): bump TypeScript to 5.6
```

Every package except the two internal ones publishes a patch. No propagation directive is needed, and none should be
written: every package is already being released directly, so a caret would compute a large dependent closure only to
discard all of it under `max()`. Under a non-zero default this commit needed an explicit `+0`; with propagation opt-in
(§8.3) the quiet form is also the correct one.

Note what is *not* used here. There is no `Release-As: patch` (§8.6 has no bump form). A `chore` that must release is a
signal that the repository's `types` mapping disagrees with how it actually ships, and the fix is to correct the mapping
once rather than to override the bump on each commit. If only a single commit genuinely needs to release under a
non-releasing type, the honest options are to pick a releasing type or to pin the outcome with `Release-As: <version>`on
that one package.

### D.8 A release that failed halfway, and what the next run does

The plan below was produced from a single commit, `feat(@acme/core)^^minor: new plugin API`, in a workspace where
`cli`, `ui`, and `api` depend on `core`, and `@acme/theme` depends on `ui`.

```
run 1: plan (publish order)
  core          1.4.2 -> 1.5.0   direct
  api           1.2.0 -> 1.3.0   propagated from core
  cli           2.0.0 -> 2.1.0   propagated from core
  ui            0.9.1 -> 0.9.2   propagated from core   (minor -> patch, 0.y.z)
  @acme/theme   1.0.0 -> 1.1.0   propagated from core

run 1: result
  ✓ core@1.5.0        published, tagged
  ✓ api@1.3.0         published, tagged
  ✓ cli@2.1.0         published, tagged
  ✗ ui                publish failed: 403 from registry
  ⊘ @acme/theme       W194 blocked: dependency ui failed
  exit 1
```

Someone fixes the token and re-runs. Nothing else has changed; `HEAD` is the same commit.

```
run 2: plan (publish order)
  ui            0.9.1 -> 0.9.2   W193 catch-up from core@1.5.0
  @acme/theme   1.0.0 -> 1.1.0   W193 catch-up from core@1.5.0

run 2: result
  ✓ ui@0.9.2          published, tagged
  ✓ @acme/theme@1.1.0 published, tagged
  convergence check: plan empty                                  §19.6
  exit 0
```

Four things in this output are guarantees rather than coincidences, and each is worth recognising when reading a real
plan:

* **The three successes are gone from run 2.** Their tags moved `stableCommit` forward, so their windows no longer
  contain the commit (§13.6, G4). They cannot be republished by a retry.
* **The two failures are still there, at the same version numbers.** `ui` is `0.9.2` in both runs, not `0.9.3`: no tag
  was written for it, so its baseline never moved (G3). Nobody needs to re-review the numbers.
* **`@acme/theme` is at depth 2 from `core`, and it is still at depth 2.** It was admitted in run 1 by `^^`, and it is
  admitted in run 2 by the same traversal from the same source. It is *not* re-derived at depth 1 from the republishing
  `ui`, which would be a different (and wider) release (G5).
* **The catch-up marker names `core@1.5.0`, a version published by a previous run.** That is what `W193` is for: a
  package appearing in a plan with no commits of its own and no releasing dependency is otherwise unexplainable to
  whoever reviews it.

Had `HEAD` moved between the runs, say because someone merged a `fix(ui)`, run 2 would plan `ui` at `0.9.2` still, since `max()`
of a propagated patch and a direct patch is a patch, and the changelog would carry both entries. G3 fixes the version
only against a fixed `HEAD`; new commits legitimately change the outcome.


---

## 25. VCS adapters

[VCS-PROTOCOL.md](./VCS-PROTOCOL.md) is an integral normative part of this specification. It defines Git as the default,
shareable trusted command configuration, exact input/output envelopes, fixed history snapshots, revision operands,
immutable records, conditional locks, failure handling and conformance vectors. Its backend-specific revision operand
rule refines the Git-only SHA operands in §§7, 20–23; the ordinary Git grammar remains unchanged.

The [commit-authoring clarification](./VCS-PROTOCOL.md#8-commit-authoring-and-message-validation-informative) distinguishes
optional native message validation from this protocol: source-commit creation is not a `createRecord` operation, and
Git CLI arguments are not portable adapter requests. This clarification changes no release plan or conformance requirement.

## 26. Explicit rollback

[ROLLBACK.md](./ROLLBACK.md) is an integral normative part of this specification. It defines `rollback(scope)`, the
required `Rollback-Version` footer, activation, package/space handlers, consumer-first withdrawal, durable intent and
completion receipts, retries, version non-reuse, and conformance vectors. Missing handlers are preflight errors.
No part of this protocol is implemented by the current dispat release merely because it is documented here.

## 27. Polyrepository Git profile

This optional profile plans and executes one package graph whose packages are owned by several linked Git
repositories. It changes where history and release records are read; it does not change the message
grammar, bump lattice, train and fresh windows, holds, cancellation, corrections, channel rules, dependency graph, or
partial-publication guarantees defined above. Git is REQUIRED for every participating repository. Activating this
profile does not activate or claim conformance with the external adapter or rollback protocols of §§25–26.

An engine operates in one of three modes, distinguished by which Git histories it reads and how it finds
them:

1. **Single history.** The profile is inactive (§27.1). One repository's history is the only history.
2. **Specified histories.** The entry configuration states which histories take part: linked repositories whose
   packages it declares or whose configurations it imports. Sections 27.1–27.10 specify this mode. They call the
   repository that holds the entry configuration the **control repository**, and the linked repositories its sources.
3. **Discovered histories.** Every repository states its own identity, and the engine discovers the participating
   histories and their configurations by walking fleet links whose shape is a topology, minimal or star. Section 27.11
   specifies this mode. No repository is the control repository, and a star topology does not make its centre one.

Existing configurations of the second mode retain their established behavior.

The optional execution profile (§28) works with all three history modes. Execution links connect worker nodes;
fleet links connect repositories. Neither worker placement nor the run's orchestrator role changes repository
discovery or makes a peer a separate control repository.

The control repository of the second mode is where the fleet's configuration, its fleet-wide intent and its optional
checkpoint evidence live. It is not the place releases come from, and nothing in this profile confines publication to
it. Every package is built and published from its own path and is recorded by a tag in its owning repository (§27.7), a
source-owned package is never published through a control-owned wrapper (§27.3), and a control checkpoint is optional
evidence written after a source record, never a precondition of one. A source repository also remains an ordinary
repository. A release it makes on its own in the first mode is conforming, and a later fleet plan reads its reachable
source tag exactly as it reads any tag-only release: direct source history needs no control boundary, and a
cross-repository boundary across that tag is proven by a checkpoint that satisfies §27.6 or stated by an explicit
tuple. The second mode fixes the inputs of a fleet run, which are the entry configuration, the fixed snapshot of §27.2
and the specified histories. It does not fix the directory or the repository the engine is invoked from, and an
implementation MAY accept a fleet run started anywhere it resolves that same input.

### 27.1 Activation and compatibility

The profile is active when `polyrepo: true`, a global `--polyrepo`, at least one explicitly imported configuration,
or the entry configuration states a non-empty `repository` identity. With none of those inputs, nested repositories
retain the single-repository behavior of the
previous specification: the control history is the only history, gitlink moves are ordinary changed paths, and this
section has no effect. Implementations MUST test that opt-out behavior as part of profile conformance.

Activation is explicit and run-wide. The engine MUST NOT activate the profile merely because it sees `.gitmodules`, a
nested `.git` directory, or a package path inside another repository. A source repository requires no dispat
configuration when the control file declares its packages. A source configuration becomes a normal repository-local
root only when the control file's `configs` list or a repeatable global `--configs PATH` option names it; its ordinary
root, space, and package layering then applies inside that repository. Merely entering a source path from centrally
declared configuration MUST NOT start that discovery.

### 27.2 Fixed fleet snapshot and identity

The control repository has the reserved identity `control`. Each linked source repository has the exact submodule name
declared by `.gitmodules`; paths and remote URLs are not identities. `control` and its case variants MUST NOT be used
as submodule names in this profile. Repository names, package names, tags, and commit IDs retain their original bytes for output and are
matched only under their existing rules.

Before parsing history, the engine resolves one immutable map:

```
fleetHead = { repository identity -> full Git commit object ID }
```

Every configured source MUST be an initialized, non-shallow Git worktree at the commit pinned by the control
repository's current gitlink. A missing gitlink, an uninitialized or shallow source, a source checked out at another
commit, an ambiguous `.gitmodules` name, or a path escaping the control workspace is `E330`. The control repository
itself MUST also have complete history. The fixed input also includes the relevant release-tag refs and control
gitlinks from which those heads were resolved.

The source head captured while validating a control gitlink is the initial `fleetHead` value used by planning. The
engine MUST retain that exact observation; it MUST NOT later reread a moved head and silently substitute it as the
initial pin boundary. A later observation is accepted only by the explicit native-record admission below.

After all fleet `beforeAll` hooks and before the first package task, the engine MUST revalidate every participating
repository against that input. For a package `P`, let `relevantPackages(P)` be the least set containing `P` and closed
under both provider edges and shared-version-group membership. Immediately after `P`'s `beforePublish` hook and before
its publish command, the engine MUST revalidate the owners of every package in that set. It MUST also revalidate the
control repository when an explicit control unit affecting any package in that set was consulted, including a hold or
cancellation, or when an enabled control checkpoint will record `P`. A changed relevant head, release-tag ref, or pin
is `E330`; the engine MUST NOT publish that package from observations spanning two fleet states. A change in an
unrelated repository after the fleet check does not invalidate a package whose input closure excludes it.

An exact new head and release-tag ref produced by a successful native record step for the current owning package MAY
advance the run's expected input. The engine MUST admit only an explicitly exported full lowercase 40- or 64-hex
commit ID and the exact package and alias refs from that success. It MUST serialize publication and recording for
packages in one repository so later packages observe that admitted transition; packages with different owners MAY
retain their normal publication concurrency.
Other Git commits or relevant ref mutations performed during build or hook commands are not admitted automatically:
if they affect a later revalidation, the package fails with `E330` and requires a new plan.
For §28, task transport commits MUST NOT become admitted native heads, intent or settlement evidence. Only
validated release-file writes enter ordinary native recording. Isolated builds may overlap, while publication
and recording retain the owner lane and input-revalidation rules (§§28.4–28.6).

An implementation MAY carry an admitted exact source revision to nested commands through private, transient state for
the current run. That state MUST be bound to the same control root, configuration, source identity, and run; it MUST
authorize only the admitted full commit ID and MUST be removed when the run ends. It is coordination between live
commands, not a baseline, release record, tag payload, recovery ledger, or input to a later plan.

The fleet lock and per-worktree mutation locks coordinate participating CCME/dispat operations. They do not claim to
exclude every external Git writer. The checks above detect relevant changes visible at their validation points; the
implementation MUST NOT claim that they make publication atomic with arbitrary processes after the final check.

A commit identity is always `(repository, full object ID)`. Logs MAY display a unique abbreviation beside the
repository name, but stored keys, correction lookup, caches, diagnostics, and plan provenance MUST use the qualified
full identity. Two identical object-ID byte strings in different repositories are incomparable. Commit dates, author
dates, filesystem modification times, tag creation dates, and fetch order MUST NOT create ancestry or precedence.

The engine reads each repository's reachable DAG once for the fixed snapshot and preserves native parent order. It
MUST NOT manufacture a synthetic fleet DAG or parent edge. Ancestry, changed paths, direct pending windows,
cancellation barriers, corrections, and source-local `Reverts` are evaluated in the repository that owns the commit.

### 27.3 Configuration composition and ownership

The additional configuration surface is:

```yaml
polyrepo: true
configs:
  - services/api/dispat.source.yaml
repositoryOverrides:
  sdk-source:
    commit:
      enabled: true
      push: true
      branch: main
  legacy-source:
    enabled: false
repositoryBaselines:
  - consumer: web
    releaseTag: web@2.4.0
    repository: api-source
    revision: 6f1a9f0d2b90c8f96a4d74dcb6568fd373b22c16
```

`configs` is a control-file array of file paths. Each path is relative to that control file; each `--configs` path is
relative to the control root. Canonical duplicate paths are loaded once. An imported root MUST NOT declare another
fleet `configs` list; every imported source is named by the entry configuration or by the invocation. Imports are
explicit configuration composition: the named file establishes that source's ordinary repository-local root, space,
and package layering, including its explicit references and normal in-folder configuration. Central path traversal
alone establishes no such root and MUST NOT infer source configuration. `--config` continues to select the one control
file and does not become repeatable.

An imported package or space path is relative to the source repository that owns the imported file. A path declared
in the control configuration retains its existing control-relative spelling, and its canonical location determines the
source repository owner. Every package path and every space-expanded package path MUST be wholly inside exactly one
repository. A source-owned package is REQUIRED to use a source-owned path. A wrapper in the control repository whose
`src` or manifests cross into a source repository is `E331` in this profile, even if the same wrapper remains legal in
single-repository mode. The closest Git worktree containing a package path and its `src` MUST be the repository assigned
from the control checkout. A package inside an otherwise valid but unlisted nested Git worktree is `E331`; that
worktree must be registered as a participating source before it can own packages. `..`, symlink, worktree, or
nested-repository traversal MUST NOT bypass this ownership check.

All package names form one case-preserving, case-insensitive workspace namespace. Duplicate package declarations,
incompatible settings for one package, and other imports for which the existing merge rules cannot produce one value
are `E332`. Centrally declared and explicitly imported sources MAY coexist when their package and space declarations
are disjoint. An imported space and every implicit or named version group from its imported configuration are local to
that repository. An unqualified CLI space or group selector selects matching local declarations across all
repositories. A shared-versioning space or `versionGroups` entry declared by the control configuration retains its
ordinary monorepository identity and MAY span repository owners. Dependencies and control-declared shared groups
therefore operate on the combined package graph without repository qualification.

`repositoryOverrides` is keyed by an exact source repository identity from `.gitmodules`. A key naming no declared
source is `E332` whatever the value states. Each value may contain an `enabled` flag, a `commit` object, and no
relocation or package-definition fields.

`enabled` states whether the source takes part in the run. Absence means `true`. An explicit `false` removes that
repository before any repository operation: the engine MUST NOT require its checkout, read its history, verify its
pin, load a configuration imported from under its path, discover its packages, run its scripts, write its records, or
acquire its lock. A control space path whose canonical location is inside an excluded repository contributes no
package, and a space left with no contributing path is dropped with its own package entries. The exclusion does not
release the repository's filesystem boundary: a control-owned package inside it remains `E331`, and a required
dependency on one of its packages retains the existing configuration error, which MUST name the excluded repository.
A `repositoryBaselines` entry naming an excluded repository is `E333`. When the object is absent, the source inherits the
control repository's whole commit policy. When it is present, it **replaces** that policy as one complete
`CommitConfig` and omitted fields take their normal defaults; fields are not overlaid individually. This distinction is
required for plain boolean fields such as `push`. `commit.branch` supplies an explicit branch when a commit-enabled
checkout is detached. Unknown keys are `E332`. The override does not move a package, import a configuration, change
history ownership, or create a source.
The `commit` override applies only to a source configured centrally. An explicitly imported source owns its own commit
policy; a `commit` override for that same repository is `E332` rather than a second precedence layer. `enabled` is
participation rather than policy and applies to a centrally configured and an explicitly imported source alike.

### 27.4 Dependency providers and scope

A dependency provider object MAY set `external: true`. If its named package is absent from the combined workspace,
the edge is inactive and emits `W330`; the missing provider is not synthesized. Only provider-presence validation is
skipped: the consumer, edge kind, and every other field MUST still validate. If the provider is present, the edge
is ordinary and participates in validation, cycle detection, propagation, version reconciliation, publish ordering,
failure blocking, and `--consumers`. A configuration computation or rewrite MUST preserve `external: true`, including
while the provider is absent. A missing provider without `external: true` retains the existing configuration error.

A unit read from a source repository can directly resolve only packages owned by that repository. This applies to an
explicit package scope or glob, `*`, `.`, changed-path fallback, and exclusions. Naming a package owned by another
repository is an error rather than a cross-repository direct change. After the direct source set is resolved,
propagation traverses the combined dependency graph normally and may reach consumers in any repository.

A unit read from the control repository has no source-owned changed paths. Its explicit package names and package globs,
including `*`, resolve across the fleet, so it can state a fleet-wide release, hold, cancellation, or
channel directive. An unscoped control unit is inert unless existing control-owned package paths resolve it. A gitlink
move is snapshot evidence and MUST NOT also become a direct package change while this profile is active; otherwise the
same source change would be counted once from its source commit and again from the pointer update.

Any semantic rule that selects the "newest" of competing directives applies directly within one repository DAG and
within the control history. Source revisions from different repositories have no such order. If incomparable source
records propose outcomes for one package where the existing rule requires a single winner, the engine MUST fail with
`E334`; it MUST NOT pick by date, traversal order, repository name, or SHA. An explicit control directive can resolve
the conflict. It is evaluated against the exact source gitlinks recorded at that control commit, its **causal
snapshot**, so it cannot rewrite or suppress source work that the control revision did not yet observe.

### 27.5 Repository-local windows and fleet propagation

For a package `P`, direct work is collected from its owning repository. Its stable and fresh direct boundaries are the
reachable source release tags defined by §§12–13. A source unit is parsed and stored once, resolves to its repository-
local direct source set, and then propagates through the one combined graph. Admission remains
`commitOf(u) in Wfresh(D)`, but for a consumer `D` in another repository that membership means that `D`'s last release
had not yet incorporated the qualified source revision. Section 27.6 defines how that consumer position is proven.

Stable train aggregation and fresh admission remain separate. A window ranges over one repository's history, which for
a cross-repository consumer is not the repository that owns the package. Packages can share an immutable train window
over a repository only when their stable boundary in that repository is the same qualified revision; they can share a
fresh window over it only when their stable boundary and newest baseline boundary there are both equal. The owners of
the sharing packages need not be equal, because a window is a function of the history it ranges over, that
repository's planned head and the boundary, and of nothing else (§13.11). Holds, cancellation, correction, channel
transition matching, per-target admission, provenance, and warnings remain per unit and package even when a graph walk
is reused. A propagation walk may be shared only for equal source set, depth, and edge kinds (§13.11).

### 27.6 Cross-repository consumer boundaries

The profile adds no ledger, tag payload, metadata ref, or timestamp convention. With specified histories it
reconstructs a consumer's position in a source repository from normal source release tags and ordinary control gitlink
history, or requires an explicit baseline tuple; §27.11 states what a fleet with no control repository reads instead.

Automatic reconstruction is valid only when one ordinary **control release checkpoint** supplies all of this evidence:

1. the control commit's normal release-commit message identifies the consumer's exact source release tag;
2. that same control commit changes the consumer repository's gitlink to the commit peeled from that tag; and
3. that commit's gitlinks pin the exact revision of each other source repository incorporated by the consumer release.

A matching consumer gitlink SHA by itself proves nothing. A tag may have been added later to an already-pinned commit,
or a standalone release may have been imported after provider pointers advanced. Several control commits may also pin
the same consumer SHA while pinning different provider SHAs. The engine MUST NOT choose among them by date or proximity.
If the normal checkpoint message is customized beyond unambiguous parsing, any of the evidence is missing, or several
checkpoints imply different positions, automatic reconstruction fails with `E333`.

`repositoryBaselines` resolves that case explicitly. Every entry contains exactly:

* `consumer`: a package name;
* `releaseTag`: an exact reachable release tag of that consumer in its owning repository;
* `repository`: `control` or an exact source repository identity; and
* `revision`: a revision reachable in that repository.

The tuple states that the named consumer release incorporated that repository through the named revision. The engine
resolves `revision` to one full object ID once and uses ancestry, never lexical comparison. A duplicate or conflicting
tuple with the same `(consumer, releaseTag, repository)` key is `E333`; two entries for different repositories are
independent and commonly required. Stable and prerelease consumer tags remain separate entries because they define
different fresh boundaries. A tuple cannot substitute for a missing consumer release tag or make an unreachable
revision reachable.

Tag-only release recording is conforming and often sufficient for direct source history. It can leave a later
cross-repository boundary ambiguous; the operator must then add the explicit tuple. The engine MUST fail instead of
guessing. Configuration may omit tuples that automatic checkpoint evidence proves.

Boundary resolution is lazy per consumer tag and repository. The mere presence of the control repository does not
require every source tag to have a control boundary. If no applicable control unit affects a tagged package, tag-only
recording remains sufficient for that history. If an applicable explicit control unit affects the package and its
position relative to the tag is required, that control position MUST be proven by the ordinary checkpoint association
above or by an explicit tuple whose `repository` is `control`; otherwise the engine fails with `E333`.

The causal snapshot of §27.4 also bounds the direction a control directive may be projected. A control unit states
intent about the source revisions its own commit pinned. If that unit is still applicable to a package, and the active
checkout of the package's owning repository does not contain the revision that unit's own gitlinks pin for that
repository, the engine MUST fail with `E333` rather than apply the intent. The active checkout and the control
revision differing is not by itself an error: the engine MUST evaluate this only for a `(control commit, package)` pair
the ordinary admission rules still apply, meaning the commit is in that package's pending window, is not already
contained in its baseline, and is neither cancelled nor held for it. A pin the source repository does not hold at all
MUST be reported as this condition and MUST NOT be reported as a repository read failure.

### 27.7 Publication, checkpoints, and recovery

The combined publish sequence is the dependency-first sequence of §19.2 across all repositories. Publication remains
non-atomic. Each successful package MUST receive its immutable release tag in its owning repository at the ordinary
tag-after-publish record point, including on that repository's configured release commit when enabled. A control
checkpoint may follow only after that source record succeeds. A failure to record or durably push the tag is `E335`;
prior successes remain successful, and dependent work is protected by the same blocking closure as §19.3. A retry
reads all source tags again and preserves those successes.

Source and control release commits are OPTIONAL exactly as their effective `commit.enabled` settings say. An engine
MUST NOT force an empty commit, force a source commit merely because the profile is active, or require a control
checkpoint. With commits disabled, tags point at the planned source heads and ordinary tag-only operation is valid.
With a source commit enabled, it is created only when the configured release writes produced something to commit.
Under §28, temporary task branches are never merged or tagged as release evidence. The same optional-commit
and tag-only policies apply; artifact provenance binds the source and admitted release-file state actually used
for the build (§28.4).

When source commit and push are enabled, the engine MUST durably record the source release first. Only after the source
branch/commit and tags are reachable from its configured remote may the control worktree advance that source's gitlink.
This prevents a pushed control pointer to a source commit nobody else can fetch. A normal control checkpoint, when
enabled and non-empty, records those gitlink transitions using the ordinary release-commit message that §27.6 can later
associate with exact source release tags. If control commits are disabled or no gitlink changed, no checkpoint is
created.

A commit-enabled repository at detached `HEAD` that needs to push a branch MUST have an explicit `commit.branch`;
otherwise it fails with `E337` before publication. A detached tag-only or local commit with no branch push does not
require one. The configured branch is pushed without force under the ordinary commit policy. A source-specific
`repositoryOverrides.<name>.commit` may provide this field, and an imported source configuration may provide it in its
own `commit` object.

The release lock MUST coordinate the control repository, every active source repository, and standalone packages as
one fleet. An implementation unable to acquire, verify, or return that shared exclusion fails with `E336`; independent
per-repo locks acquired without a deadlock-free fleet protocol are insufficient. A cleanup failure does not change an
already-published package's outcome, but the run MUST exit nonzero. If a completion event is emitted, it MUST report
`failed` or `interrupted`, never `succeeded`. Fleet cleanup MUST continue through the remaining owned locks. Status and other read-only planning retain their ordinary lock-free
behavior.

Every operation that can hold more than one fleet or worktree lock MUST use one stable total order over the lock
resource identities and release them in reverse order. An acquisition that waits for a held lock needs the order to be
free of deadlock. One that fails instead, releasing what it took, cannot deadlock under any order and needs the order
for progress: the lock a run stopped at sorts after every lock it held, so among contending runs the chain of who
stopped whom never closes, and one of them acquires its whole set. The guarantee reaches only runs that spell the
contended identities alike. Two control repositories that name one source differently may order it differently, and
both runs can then fail; that is lost progress, never lost exclusion, because each repository's lock is still one lock.
Per-worktree mutation locks cover only the complete native Git transaction that reads, commits, tags, pushes, or
checkpoints the affected repositories. Hooks and arbitrary scripts
run outside those mutation locks; their changes remain subject to the fixed-input checks of §27.2.

No rollback is inferred after a partial publish. Record every success that can still be recorded, stop dependent work,
report publication and recording failures separately, and retry from durable source tags. Never delete, move, or
duplicate a successful source tag to make the control checkpoint look atomic.

If a source record succeeds and the control gitlink checkpoint then fails, `E335` MUST name the source repository,
full source revision, and exact release tag and require explicit control-checkpoint repair. The next run remains `E330`
while the source checkout and committed gitlink are unpinned. An operator first verifies the durable source outcome,
then explicitly commits/reconciles the ordinary control gitlink to that revision or restores the intended pin. The
engine MUST NOT auto-commit the repair, force a checkpoint, republish the source, or infer success from the worktree.
Dependent publication remains gated until the fixed fleet snapshot is valid again.

### 27.8 Commands, hooks, and `--since`

Every command sees the same combined workspace and selectors. Package scripts run in their package paths. Root, space,
package, run, and nested hooks retain the repository owner of the triggering package as well as the combined graph and
selection context; entering a nested repository MUST NOT silently reload its configuration or narrow the fleet.

For `--since <control-revision>`, resolve that control revision and project its gitlinks into repository-qualified
source boundaries. A source package is changed when its source history after the projected gitlink addresses it. A
source added after the selected control revision contributes its reachable history according to the ordinary
no-baseline rule. A removed, missing, or ambiguous historical gitlink is an error. `--since all` still selects every
package. The engine MUST NOT treat the control gitlink transition itself as an additional package change.

### 27.9 Performance requirements

The engine MUST index relevant control gitlink transitions once per fixed control snapshot, not once per consumer or
provider pair. It MUST store each parsed `(repository, commit)` record once and share immutable window membership only
under the keys in §27.5. It SHOULD use bounded reachability and ancestry caches; an unbounded pair cache can consume
quadratic memory even when history is walked once. Equal SHA spellings across repositories MUST occupy distinct keys.

With the notation of §13.11, input traversal is realistically `O(G + sum(Hq + Aq))` before distinct-boundary window
work, and window reachability is `O(sum((Hq + Aq) * bw(mq)) + Iw)` with one marker pass per repository,
or `O(sum(Kq * (Hq + Aq)) + Iw)` with a walk per boundary. Parsing, scope
incidences, propagation, provenance, sorting, and output retain their separate `N`, `R`, `I`, `Z`, and `Oout` costs.
This profile makes no globally linear end-to-end claim and permits no synthetic ancestry shortcut.

Under the linked peer topology of §27.11 there is no control index, so `G` is zero and the evidence cost is in the
links. The link graph is a tree, so rooting it once in `O(Q)` fixes every route, and an engine SHOULD NOT search the
graph once per boundary. It SHOULD read the fleet links one `(repository, revision)` tree records at most once per plan
and answer every later hop through that revision from the retained pins: the consumers of one hub ask about the same few
revisions. With the notation of §13.11, boundary resolution is then `O(Q + X · Y)` lookups over `B` tree reads,
against `X · Y` tree reads and `X` graph searches for a literal transcription. Those reads locate boundaries and do not
replace the ancestry walk of the repository the boundary lies in.

Settlement cost is a property of the link tree and not of the package graph. The routes from one consumer to the
repositories its plan read form a subtree, and an engine SHOULD settle that subtree once, with one commit in each
repository that has a next hop in it, not once per route. A package therefore settles with at most `Q - 1` commits,
and with at most `Y` when it reads one repository. Both topologies of §27.11 use `Q - 1` links, the fewest that join
`Q` peers and the only count at which every route is unique. They differ in `Y`: a star bounds every route at two hops
and every settlement at two commits, while another tree can reach `Q - 1` of each. A minimal proposal that joins group
centres (§27.11) has the least `Y` any proposal keeping the existing links can have. For the groups an engine can see,
one linked group and unlinked identities, that is the group's own longest route `d`, against `d + 1` when an identity
is linked to an end of that route; between two linked groups the difference can reach the sum of their radii. A pin
that already records the revision to settle costs no commit, so consecutive packages of one repository that read
unchanged peers settle once.

Publication revalidation has a separate output-sensitive cost. If repository `q` participates in `Jq` fleet or
package checks and its relevant tag snapshot contains `Tq` refs, a full-ref implementation performs
`O(sum(Jq * (1 + Tq)))` comparison work in addition to Git and filesystem access. Let `L` be the total number of
repository memberships across all package input closures; retaining those closures costs `O(L)` memory and `L` can be
`P * Q`. Implementations SHOULD share immutable provider-closure results or repository bitsets where dependencies have
the same suffix, but MUST retain each package's exact closure. Neither this validation cost nor its memory is included
in the one-time history-walk bound above.

### 27.10 Conformance vectors

1. The profile is absent in a repository with initialized submodules. **Use the single-repository algorithm exactly.**
   A gitlink move remains one control changed path; no source history is loaded.
2. `polyrepo: true` with two initialized, pinned, complete sources. One source commit is `feat(core)^: x`, and an app
   in the other source depends on `core`. **Directly bump `core`, then propagate to the app across the combined graph.**
3. The control gitlink also moved to the `feat(core)` commit. **Do not count a second direct change.** The pointer is
   snapshot evidence in this profile.
4. A source commit explicitly scopes a package owned by another source. **Fail that unit.** The same outcome cannot be
   obtained by treating the scope as fleet-wide; propagation is the cross-repository path.
5. A control commit's explicit scope-set names packages from two sources, or uses `*`. **Apply it fleet-wide** at that
   control commit's causal gitlink snapshot.
6. A central wrapper owns `services/api` while `services/api/src` is a source repository and the package's source or
   manifests cross that boundary. **`E331`.** In opt-out single-repository mode, retain the previous wrapper behavior.
7. An imported config uses `path: packages/api`; the file lives in source `api-source`. **Resolve the path within
   `api-source` and apply that root's ordinary local space/package layering.** Do not resolve it against the control
   root. A centrally declared path entering the same source does not implicitly load that config.
8. Two repositories each declare a local space `services`. `--space services` selects both. A local implicit version
   group does not span them; a group with that name declared centrally may span both.
9. `app` names `{provider: optional-runtime, external: true}`. With no such package, emit `W330` and no edge. After an
   imported config supplies it, validate and use the edge for cycles, propagation, ordering, blocking, and consumers.
   An invalid edge kind fails in both states.
10. A control history pins consumer SHA `C` both before and after provider `P1`, and the consumer tag is later attached
    to `C`. **`E333`.** Matching the SHA or choosing the nearer/date-later control commit is non-conforming.
11. A normal control release checkpoint identifies the exact consumer tag, changes its gitlink to the tag's commit,
    and pins provider `P0` in the same commit. **Infer `P0` as the consumer boundary.** If the message association is
    missing or custom and ambiguous, require `repositoryBaselines`.
12. An explicit baseline names `(app, app@2.4.0, core-source, P0)`, and every identity is reachable. **Use `P0`.** A
    second entry with `P1` or `P0` for the same key is `E333`; an entry for another repository is valid and independent.
    Never choose a duplicate by list order.
13. Two incomparable source directives require newest-wins channel or `Release-As` precedence for one consumer.
    **`E334`.** An applicable later control directive resolves the choice against its causal source snapshot.
14. Commits are disabled in every repository. A package publishes and its source tag succeeds. **Accept the tag-only
    release and create no empty commit.** If a future consumer boundary cannot be proven, require an explicit tuple.
15. That tag-only package has no applicable control unit. **Use its source tag without requiring a control boundary.**
    If an applicable explicit control unit must instead be ordered across the tag, require a proven ordinary checkpoint
    or an explicit `(consumer, releaseTag, control, revision)` tuple; otherwise report `E333`.
16. A source publish and tag succeed; an unrelated source fails; its dependents are blocked. **Preserve and report the
    first success, record no tag for the failure, continue only independent work, and recompute from tags on retry.**
17. A source commit is enabled and produces a commit. Its source push fails. **Do not advance or push the control
    gitlink to that unreachable commit; report `E335`.**
18. A commit-enabled source is detached, needs a branch push, and has no `commit.branch`. **`E337` before
    publication.** A local commit or tag-only detached operation that pushes no branch remains valid. With an existing
    explicit branch, push that branch without forcing it.
19. `--since R` names a control revision whose gitlinks point to `A0` and `B0`; current links point to `A2` and `B1`.
    **Evaluate source ranges `A0..A2` and `B0..B1` once each**, then apply normal scope and combined propagation.
20. Ten consumers share one release checkpoint and one provider boundary. **One control gitlink index and one immutable
    provider window class are valid.** Ten control-history scans are non-conforming; merging fresh windows whose newest
    baselines differ is also non-conforming.
21. All repositories are individually lockable but the implementation cannot establish fleet-wide exclusion.
    **`E336` before mutation.** Per-repository success does not prove fleet coordination.
22. A relevant source tag changes in `beforeAll`. **`E330` after the hooks and before the first package task.** A
    provider head changes in a consumer's `beforePublish`; **`E330` before that consumer's publish command.** A head
    change in an unrelated repository after the fleet check does not invalidate that consumer.
23. A successful native record step for one package exports its full new source commit and creates that package's
    exact release tag. **Admit that transition for later packages in the same repository and serialize their publish
    and record work.** A direct Git commit or relevant tag written by arbitrary build or hook code has no such
    admission and is `E330` when a relevant check observes it. Packages owned by different repositories may publish
    concurrently.
24. A centrally declared or imported package path is inside a Git worktree that is nested below its assigned owner but
    is not a listed source. **`E331`.** Merely finding the nested worktree does not register it. With the optional
    profile disabled, retain the existing single-repository discovery behavior.
25. Control commits are disabled, but an explicit control directive contributes to source package `A`'s plan. The
    control head changes in `A`'s `beforePublish`. **`E330` before `A` publishes.** The same change does not invalidate
    package `B` when neither its provider/group closure nor its plan consulted control history.
26. Centrally declared fixed-group members `A` and `B` have different source owners and no dependency edge. Work in
    `A` makes `B` ride the shared version. **`A`'s repository is a publication input for `B`; changing it after `A`'s
    admitted record and before `B` publishes is `E330`.** Group membership cannot be omitted from the input closure.
27. A successful nested native record advances a source while another package script from the same run is already
    active. **Admit only the exact exported full commit ID.** Transient run coordination may make that pin visible to a
    later nested command in the active script; it is removed at run completion and supplies no baseline on a later run.
28. Source `core-source` releases its package `core` on its own in single-history mode, and the control gitlink is later
    moved to that release. **Accept the source tag as `core`'s baseline; no fleet run has to have produced it.**
    Refusing or discounting the tag because the control repository did not start the release is non-conforming. A
    cross-repository boundary across such a tag has no checkpoint and takes an explicit tuple, as in vector 14.

### 27.11 Linked peer topology

This section specifies the linked peer topology of the profile. No repository is the control repository. Each participant
states its own identity, carries its own configuration and release records, and is joined to its neighbours by
ordinary two-sided submodule links, and a release MAY start in any of them. Everything §§27.1–27.10 require of the
profile continues to apply except where this section states otherwise: the message grammar, the bump lattice, the
windows, the one combined dependency graph and the partial-publication guarantees are unchanged.

**Activation.** Peer-owned composition is active when the entry configuration states a non-empty `repository`
identity. That key activates the polyrepository profile automatically; `polyrepo: true` is not required. A
configuration written before this section and containing no identity composes exactly as it did, and
implementations MUST test that compatibility as part of conformance. An explicit `--polyrepo=false` clears peer
composition for the invocation and is the standalone escape hatch: the stating repository releases alone, its
fleet links are not walked, and no other repository is planned, locked, settled or recorded. The engine MUST report
that the fleet was skipped, because a release commit written by such a run carries no cross-repository evidence.

**CI entry points.** Any participating peer MAY be the entry repository for a CI release, including a star leaf
or any node of a minimal tree. Implementations MUST NOT require a hub or any other distinguished peer as the
exclusive entry point. Link topology determines discovery and evidence routes, not permission to initiate a
release. A deployment MAY configure one CI entry point or several; repository-specific entry workflows MAY
call a shared reusable workflow, and workflow definitions need not reside in the entry repository. Neither
topology requires copying package manifests or other peers' package configuration into the entry repository.
The entry checkout MUST satisfy the same link
initialization, history, configuration and release requirements as any other invocation; choosing a different
entry point does not waive those requirements.
Under §28, repository entry points and execution-node roles are distinct: an explicit worker MUST refuse
release initiation, including nested hooks, before acquiring locks or forwarding the request. The CI-starting
orchestrator MUST acquire every participating repository's lock before planning and assigning tasks (§28.3).

**Peer identity.** `repository` is a repository's own identity in its fleet. It is REQUIRED in this topology and is
written as `[A-Za-z0-9._-]+`. `control` and its case variants remain reserved for a central control repository and MUST NOT
be used. A missing, malformed or reserved identity is `E339`. The identity is also the submodule name every link to
that repository is created under, which is what makes one identity readable from either end of a link. A linked
checkout whose own `repository` value is not the name it was linked as is `E339`.

**The roster.** `repositories` is the optional membership list of the fleet a repository belongs to: every other peer, by
identity, with the `url` a link to it is cloned from, the `path` that link occupies inside this repository, and the
`branch` the link follows. An omitted `path` means `.links/<name>`. A roster entry naming the declaring repository,
and two entries whose identities are equal under case folding, are `E339`; so is a `path` that is absolute or that
leaves the declaring repository's root. It MAY be omitted or empty for a fleet containing only the declaring
repository. A non-empty roster without a non-empty `repository` identity is `E339`. The roster states membership only. It does not state which pairs are linked,
and a peer two hops away is named here and reached through somebody else.

**Fleet links and the tree rule.** A submodule is a fleet link exactly when the roster of the repository declaring it
names that submodule's name; every other submodule is an ordinary vendored checkout and takes no part in the fleet. A
submodule whose name differs from a roster entry only by case is `E339` rather than a link. The engine composes the
fleet by walking those links breadth first from the entry repository, in folded-identity order, and MUST enter each
identity exactly once. The link graph over the participating identities MUST be a tree: reaching an identity a second
time through any link other than the one it was entered by is `E338`, because a second route is a second answer to
which repositories lie between two peers, and the link evidence below would then depend on which route a reader
followed. The two ends of one pair are one edge and not a cycle, so the engine MUST NOT descend into the back-link
leading to the repository it arrived from. A link path holding no repository of its own, a peer without complete
history, a `.gitmodules` that cannot be read, and a link path leaving the declaring repository are each `E330`. The
diagnostic for an unmaterialized link MUST name the command that initializes exactly that link, because a run started
inside another repository's linked checkout is the ordinary way to meet it.

**The fixed fleet snapshot.** The snapshot of §27.2 is the head each composed peer holds when the walk reads it,
together with the relevant release-tag refs. A recorded gitlink MUST NOT be required to equal that head: in this
mode a pin is **advisory**. Two peers that link each other cannot both record the other's current revision, so exact
pin equality is unachievable in principle and MUST NOT be reported as `E330`. Everything else §27.2 requires is
unchanged: the engine retains each observed head as planning's initial boundary, revalidates every participating
repository after the fleet `beforeAll` hooks and each package's relevant closure before its publish command, and
treats a relevant head or release-tag change as `E330`. Commit identity remains `(repository, full object ID)`.

**Composition.** Each peer's own configuration file establishes that repository's ordinary repository-local root,
space and package layering, exactly as an explicitly imported configuration does under §27.3. Every participant of a
linked peer fleet, the entry included, is such a root. The combined workspace, the single package-name namespace,
the repository-local spaces and version groups, and the ownership rules of §27.3 apply unchanged, with one
adjustment: a peer's checkout lies inside the repository that links it, so scope containment is compared within one
repository rather than across the fleet. Two repositories declaring the same package name remain `E332`.

The keys only a control repository can own are refused rather than ignored. `configs` and `--configs`,
`repositoryOverrides.<name>.commit`, and a `repositoryBaselines` entry whose `repository` is `control` are each `E332`
in this topology.

`repositoryOverrides.<peer>.enabled` keeps its meaning from §27.3 and is read from the entry repository's
configuration alone: a peer owns its policy, but whether it takes part in this run is the invocation's question. A key
naming no member of the entry's roster is `E332`, and so is excluding the repository the run started in.

**Scope.** Every unit is repository-local, the entry's included: a unit can directly resolve only packages owned by
the repository whose history carries it. This mode has no repository whose units address the whole fleet. Propagation
across the combined graph is unaffected and remains the cross-repository path. The consequence for §27.4 is that the
control directive which resolves incomparable revisions does not exist here: where the existing rule requires a single
winner and the competing revisions are incomparable, the engine MUST report `E334` and MUST NOT pick by date,
traversal order, repository name or SHA. A commit that only moved a fleet link MUST NOT be read as a change to any
package, for the reason §27.4 already gives about gitlink moves.

**Cross-repository boundaries.** Section 27.6 is replaced for this mode; its remedy is not. A consumer's position in
another repository is proven from the links the release itself recorded, and automatic reconstruction is valid only
when all of this holds:

1. the consumer's release tag sits on an ordinary release commit in its own repository whose message names that exact
   tag;
2. following the one route of fleet links between the consumer's repository and the named repository, hop by hop
   from that release commit's own tree, each hop's tree records a fleet link to the next hop; and
3. the revision the last hop records is present in the named repository and is an ancestor of, or equal to, that
   repository's planned head.

A matching object ID proves nothing on its own, exactly as it proves nothing under §27.6: the tag may have been
attached later to a commit whose links were recorded for another release. If the tag is not on a release commit that
names it, if a hop records no link to the next, or if the resulting revision is absent or unreachable, automatic
reconstruction fails with `E333` naming the hop it stopped at. Absence MUST be reported as this condition and MUST NOT
be reported as a repository read failure.

`repositoryBaselines` is the explicit form and is unchanged from §27.6, except that `control` is not an admissible
`repository` value. An entry MAY be declared in any participating repository's configuration, and the engine MUST
merge the entries of every composed peer before it resolves boundaries: the repository that knows a boundary is the
one owning the consumer, and there is no central file to write it in. An explicit tuple wins over link evidence.
Tag-only recording remains conforming and often sufficient, and remains the case that leaves a later boundary to an
explicit tuple.

**Settling fleet links before publication.** Because there is no control checkpoint, that evidence has to exist in the
consumer's own release commit. Before a package whose plan read history from another repository publishes, the engine
MUST record the route: every repository on the route from the consumer to each such repository records the revision of
its next hop, deepest hop first, and the consumer records its own first hop, so the tree of the commit the release tag
will sit on already carries the first link of the chain.

The recording is ordinary commits in ordinary repositories and MUST NOT publish anything. It is all or nothing per
package: if any repository on the route has release commits disabled while the consumer's repository has them
enabled, the engine MUST refuse that package before publication with `E333` rather than publish a release whose
evidence stops halfway. A consumer whose own release commits are disabled records no evidence at all; that is the
tag-only case above, it is not an error, and the engine MUST report it rather than force a commit. No settlement is
created where the recorded pins already equal the revisions to record, and the engine MUST NOT create an empty commit
to mark a settlement.

A settlement moves the head of every repository it commits in, the consumer's own included, and it does so before the
revalidation point of §27.2, which admits only a native record step of the owning package. The exact full commit ID a
successful settlement produced is therefore an admitted transition of the repository it was written in, and it
advances the run's expected head there exactly as an admitted record does. Before it writes, a settlement MUST verify
that the repository still holds the head the run expects. A head that no settlement or admitted record produced remains
`E330`. Without this admission the pre-publish revalidation of every settled consumer would refuse the head its own
settlement wrote.

A settlement commits and pushes in a repository, so that repository's ordinary commit and push hooks bracket it and
run outside the advisory mutation lock, exactly as §27.7 requires of every other native transaction. A repository
whose settlement must be pushed while it is at detached `HEAD` requires `commit.branch` and otherwise fails with
`E337` before publication.

**A pin never outruns its target.** Before a repository that pushes records a revision of a peer, the engine MUST
verify that the peer's own remote already holds that revision on the branch the fleet states for it: the peer's own
`commit.branch` when it has one, and otherwise the `branch` a roster entry states for it. This is §27.7's rule that a
pushed pointer may not name a commit nobody else can fetch. Every linked checkout is detached, so a peer with neither
branch cannot be verified and the settlement MUST fail rather than proceed.

**Interruption.** A settlement that fails partway leaves ordinary commits and nothing published. A later run MUST
converge: it reads what each repository already records, does nothing where those records match, and pushes a head the
remote does not hold yet. Nothing is deleted or rewritten to make a settlement look atomic.

**Locks.** The fleet lock of §27.7 covers every participating peer. The order is the participants' identities sorted
byte-wise by name, with no reserved position for any of them, and the locks are released in reverse. The settlement of
one package takes the publish lanes of every repository on its route in that same name order and gives back all but
the consumer's own before publication begins, which is what keeps two consumers with overlapping routes from waiting on
each other. An unsafe lock bypass stated in a configuration disables the lock of the repository stating it and no
other, because one peer cannot decide another peer's safety; an environment kill switch is the invocation's and
applies to every repository it releases. `W331` names the repositories releasing without a lock.
This bypass does not authorize distributed release execution: §28.3 MUST refuse it and requires every
participating repository to remain locked for the authorized effects.

**`--since`.** `--since <revision>` names a revision of the entry repository. The engine projects it into one range
per repository by following the same routes the boundary evidence uses, and evaluates each repository once. A
repository that revision records no link for projects to its whole reachable history, exactly as an absent gitlink
does under §27.8. `--since all` still selects every package.

**Configuration computation.** `compute --topology minimal` is the default. It MUST preserve existing links and MAY
propose the fewest additional links that connect every identity the rosters name, the half of a link only one of its
two repositories declares, the checkouts a declared link lacks, and roster entries a participant has not heard of.
Every such proposal adds the same number of links, one fewer than the number of groups the existing links leave the
fleet in, so the count does not choose between them; the longest route `Y` does, and §27.9 charges link evidence and
settlement by it. Among the proposals with the fewest links the computation SHOULD choose one with the least `Y`: take
a centre of each group, a repository whose farthest group member is nearest, and link the centre of every other group
to the centre of the group with the greatest radius. With radii `r1 >= r2 >= r3` and `d` the longest route inside any
group, the result has `Y = max(d, r1 + r2 + 1, r2 + r3 + 2)`, no proposal has less, and the computation is `O(Q)`.
Ties, between the two centres a group can have and between groups of equal radius, go to the identity that sorts first
under case folding, so two computations over one fleet propose the same links. A fleet with no links yet has every
radius zero and receives a star on its first identity.

An engine learns links only from the repositories it composed, and composition follows links from the entry. The groups
it can see are therefore the entry's group and the identities no composed repository links, each a group of one, and
the rule reduces to linking each such identity to a centre of the entry's group. That keeps `Y` at the group's own
longest route, where linking the identity to an end of that route adds a hop. A link is written in a composed end.
Where no centre of the group is composed, the computation uses the composed member nearest a centre, ties again to the
identity that sorts first under case folding, and the least `Y` is then not guaranteed; where no end is composed, it
proposes what it can write and reports the rest.
`compute --topology star` MUST propose a direct link from every peer to the entry repository. It is valid only for an
identity-linked fleet and MUST fail when existing links are incompatible with that shape. Both modes retain the same
repository-local policy and record ownership. Their proposed set MUST be a tree over the fleet, so it never creates
the second route `E338` refuses. A proposed missing half joins no pair the links do not already join, so it
adds no route. Applying it creates a checkout for one half of each link and declares the other half inside that
checkout without fetching it, pinned at a revision the declaring repository's remote can serve; a declaring repository
that states no remote has that half withheld and reported rather than pinned at a revision no peer can fetch. A
missing half is proposed only where the computation composed the peer whose declaration is absent, because that
checkout is where the declaration is written. The computation MUST NOT commit, MUST NOT remove an existing
link in either mode, and MUST NOT recurse into a link's own links: a link is history's only record of what a release incorporated,
and the back-link of a pair is deliberately left unmaterialized. A roster entry no participant states a `url` for is
reported rather than written, and a link URL carrying user information MUST be refused, because `.gitmodules` is
committed and pushed.

The engine MUST reject a topology conflict visible in the composed snapshot before writing. Application MAY discover
additional links only after it checks out a previously unavailable peer. If such a link conflicts with the selected
topology, the computation MUST fail and leave already staged edits for operator review; it MUST NOT roll those edits
back or delete any link.

**Recoverable findings.** Two conditions are reported and do not stop a run. `W332` is a fleet link only one of its
two repositories declares: the fleet still composes from the declaring end, and a release started at the other end
would compose a smaller fleet. `W333` is a participant whose roster does not name every member of the composed fleet,
because a repository that has not heard of a peer cannot plan a boundary across it. Both are what a configuration
computation repairs.

### 27.12 Linked peer conformance vectors

1. A configuration states no identity or roster. **Preserve its existing central or single-repository behavior.**
   Nothing in §27.11 has any effect.
2. Peers `api` and `sdk` state their own identities, name each other in their rosters, and link each
   other. A run started in `api` and a run started in `sdk` **compose the same two repositories and plan the same
   packages.** Neither is a control repository.
3. A configuration states a non-empty `repository` and no roster. **Compose a one-member linked fleet.** A configuration
   instead states a non-empty roster without a repository identity. **`E339`.** Ignoring the roster is non-conforming.
4. A linked checkout calls itself `sdk-next` although it is linked as `sdk`, names itself in its
   own roster, or uses the reserved identity `control`. **`E339`** in each case.
5. Three peers are linked in a ring, so two of them are joined by two routes. **`E338` before any package work.**
   Choosing either route is non-conforming.
6. A fresh clone's fleet links were never initialized. **`E330` naming the command that initializes that link.** The
   same checkout composes the whole fleet once they are.
7. A repository holds a submodule its roster does not name. **Take no part in the fleet.** Only the roster makes a
   submodule a link.
8. An identity-linked configuration states `configs`, a `repositoryOverrides.<peer>.commit` object, or a
   `repositoryBaselines` entry naming `control`. **`E332`** in each case.
9. A peer's checkout holds a revision its linker's tree does not record. **Compose at the revision the checkout
   holds.** A pin is advisory here, and the difference is not `E330`.
10. `web@2.4.0` sits on a release commit naming it whose tree pins `api` at `A0`, and `api`'s tree at `A0` pins `sdk`
    at `S0`. **The `web` boundary in `sdk` is `S0`.** Work in `sdk` up to `S0` is already incorporated.
11. That same tag was attached by hand to a commit that is not a release commit. **`E333` naming `repositoryBaselines`
    and the hop it stopped at.** Reading the pin anyway is non-conforming.
12. A tuple `(web, web@2.4.0, sdk, S0)` is declared in `sdk`'s own configuration and the run starts in `api`. **Use
    `S0`.** Every composed peer's tuples are merged before boundaries resolve.
13. A consumer in `api` reads history from `sdk`, two hops away through `core`. **Record `sdk`'s revision in `core`
    and `core`'s revision in `api` before `api` publishes**, deepest hop first.
14. One repository on that route has `commit.enabled: false` while the consumer's repository has it enabled.
    **Refuse the package before publication with `E333`.** Publishing evidence that stops halfway is non-conforming.
15. Release commits are disabled in every repository. **Accept the tag-only release, record no evidence, and create no
    commit.** A later boundary then requires an explicit tuple.
16. A settlement must be pushed from a detached checkout with no `commit.branch`. **`E337` before publication.**
17. The revision a settlement would pin is not yet on its own repository's remote. **Refuse the settlement.** A
    pushed pin naming an unfetchable commit is non-conforming.
18. A settlement's push is rejected. **Leave the ordinary commits, publish nothing, and converge on the next run.**
    Each package is still released exactly once.
19. A commit moves only a fleet link. **Do not count it as a change to any package.**
20. `--since R` names an entry revision. **Evaluate one range per repository**, projected through the fleet routes. A
    repository `R` records no link for contributes its whole reachable history.
21. One peer states `unsafeDisableLock` and the others do not. **Release only that repository without its lock** and
    name it in `W331`. Disabling another peer's lock is non-conforming.
22. Incomparable revisions in two repositories require one winner for a package in a third. **`E334`.** No unit of any
    peer can resolve it, because every unit is repository-local.
23. A roster names four repositories and one compatible link exists. With `--topology minimal`, **preserve that link
    and propose exactly two more**. With `--topology star`, **propose direct links from every peer to the entry**, or
    error if the existing link cannot fit that star. Neither mode removes or converts a link.
24. A proposed link's URL carries user information. **Refuse it.** A committed `.gitmodules` would publish it.
25. A link is declared by one end only, and one peer's roster omits a fleet member. **`W332` and `W333`; the run
    continues.** A configuration computation proposes the missing half and the missing roster entry, and adds no
    route with either.
26. An identity-linked configuration is run with `--polyrepo=false`. **Release that repository alone** and report that
    the fleet was skipped.
27. `compute --topology star` is run without a non-empty `repository` identity. **Configuration error.** Topology
    selection does not turn a central or single-repository configuration into a linked peer fleet.
28. The settlement of vector 13 commits in `core` and in `api` before `api`'s package publishes. **Admit exactly those
    settlement revisions as the expected heads of `core` and `api`, so the pre-publish revalidation succeeds.** A head
    either repository gained from anything else, including between the run's observation and the settlement, is `E330`.
29. Consumers owned by `web` and by `api` have the same boundary `S0` in `sdk`. **One immutable window over `sdk` may
    serve both.** A window is keyed by the repository it ranges over and the boundary revision, not by the owner of
    the package that reads it; two boundaries that differ remain two windows.
30. Composed repositories `a`, `b`, `c`, `d`, `e` are linked in a chain in that order, and the roster also names `z`,
    which nothing links. `--topology minimal` **proposes exactly one link, and SHOULD propose the one between `c` and
    `z`**, which leaves the longest route at 4. Proposing the link between `a` and `z` is conforming and makes it 5;
    proposing two links, or a link between two members of the chain, is not.

---

## 28. Distributed task and release execution

This is an **optional execution profile**, independent of the history-selection modes in §27. It changes no message
syntax and asserts no speedup; what the dispat engine implements of it, and where that engine departs from it, is
stated by date in [DESIGN-HISTORY.md](./DESIGN-HISTORY.md), not here. Git is REQUIRED for the
source/result transport defined by this profile; external VCS adapter support does not imply support for it.
Omitting worker links preserves local execution. Implementations advertising this profile MUST satisfy all
of its safety, output-transfer and conformance requirements, not only remote command dispatch.

### 28.1 Scope and roles

An execution node is a machine or runner executing the same release engine and
configuration schema. It is distinct from a repository peer. One repository
may have work on several nodes; one node may work on several repositories.

Every node MUST support both roles, and one engine provides both. Serving
delegated tasks MAY be a distinct invocation or process of that same engine,
for example a long-running serving command; no separate worker product and no
second configuration format is required. The default role is **orchestrator**;
an explicit **worker** role MUST refuse release initiation, including indirect
initiation from a delegated hook. A worker executes authorized tasks in an
existing run and MUST NOT substitute a locally recomputed plan, acquire competing release
ownership, dispatch to another pool, or independently finalize a release.
An orchestrator-capable node MAY accept delegated tasks; for that assignment it
has only worker authority. Role changes MUST NOT transfer a live run's ownership.

The orchestrator-role node on which CI starts the release is the run's orchestrator. A CI release
invoked on an explicit worker MUST fail before acquiring release locks or forwarding an initiation request.
The orchestrator owns repository lock acquisition, the fixed planning input, task allocation, result admission, and
release finalization. It MAY execute tasks locally under the same rules: a build it keeps is captured, described,
signed and admitted exactly as a worker's is, and its consumers receive the same identity for the result, so nothing
about an output says where it was built. There is one orchestrator per run, not a permanent leader of the
repositories.

Workers MAY be added to **every history mode**: single history (including a
large monorepo), specified histories, or discovered histories. They MUST NOT
require a control repository, a particular peer topology, or polyrepo activation.
Worker endpoint links are execution links, not Git submodule/fleet links, and
MUST NOT alter source discovery, repository identity or release scope.

### 28.2 Configuration and concurrency

The same configuration format and schema MUST serve both roles. The following keys belong to this profile:

| Key | Default | Contract |
| --- | --- | --- |
| `execution.role` | `orchestrator` | `orchestrator` or `worker`; governs release initiation on this node. |
| `execution.concurrency` | `1` | Positive integer bounding simultaneous assigned command tasks on this node, including tasks from different runs. |
| `execution.workers` | `[]` | Orchestrator's execution links, each with a unique nonempty node `name` and authenticated transport `endpoint`. |

These are node-startup settings. A package, space, linked repository, or delegated policy snapshot MUST NOT
change the local role, local capacity, or worker list when execution enters another checkout. A worker MUST
have an empty worker list. An orchestrator receiving a delegated task MUST NOT use its own list for that task.
Endpoint schemes, protocol framing and credential references are implementation-defined and MUST be documented
in this same schema; secrets MUST NOT appear in endpoint URLs, receipts or logs. Node names identify authenticated
execution endpoints, not repository peers. A node may serve both capabilities, but each assignment has exactly
one authority scope. A transport the worker polls, as the Git mailbox of §28.4 is, additionally gives the worker its
own name and the endpoint it polls; those keys belong to the transport and are documented with it.

Example, with the Git mailbox transport of §28.4, where an endpoint is a Git repository the nodes share and
`secretEnv` names the variable holding the secret the mailbox's messages are authenticated with:

```yaml
execution:
  role: orchestrator
  concurrency: 2
  secretEnv: CCME_EXECUTION_SECRET
  workers:
    - name: build-a
      endpoint: https://git.example.invalid/acme/release-mailbox.git
    - name: build-b
      endpoint: https://git.example.invalid/acme/release-mailbox.git
```

The corresponding worker uses the same schema with `execution.role: worker`, its own name, the endpoint it polls, its
own concurrency limit and no worker links. Package configuration, commands and release policy remain the
orchestrator's resolved input.
Node settings select the role, execution concurrency and worker-node links. A worker needs no worker-specific release graph, package
policy, version rules or workflow; it receives the run's resolved configuration.
Transport credentials and node authentication are deployment prerequisites,
not a second release-policy format.

Worker concurrency is a positive local upper bound on simultaneous assigned
tasks. Effective capacity is the intersection of that bound and the run's
existing stage limits and resource exclusions. Existing build/publish limits
MUST be respected across the whole run, not multiplied by the number of workers.
Preparation, test, build and publish command tasks MUST consume node capacity, plus their applicable
run-wide stage budgets. Transfer, recording and control work MUST have separately documented bounded capacity;
recording still takes its owner's publication lane. An in-flight attempt retains its capacity reservation until
completion or acknowledged cancellation, or until execution has been safely fenced. A timeout alone cannot free
capacity for a possibly overlapping attempt. Capacity is reserved from the claim: an assignment no node has claimed
is queued work and not an attempt, holds no capacity, and MAY be withdrawn and offered again without any of the
above. Shared workspace writes MUST serialize or use separate task worktrees.

The orchestrator MUST validate the effective configuration, protocol/toolchain
compatibility and output-transfer capability before dispatch. Worker-local
settings MUST NOT override package policy, planned versions, commands or inputs.
An empty worker list preserves local execution. Malformed links, an invalid
role or nonpositive concurrency MUST be configuration errors. Unsupported protocol versions, target platforms or declared output sizes MUST fail preflight rather than
silently dropping a worker or omitting required bytes. Implementations MUST document their task input/output
schema, transfer limits and workspace integration policy; these are part of the shared configuration contract,
not worker-specific copies of release policy.

### 28.3 Lock, snapshot, plan, assign

Before creating a write-capable run, the orchestrator MUST identify every
participating repository, including any control repository, and acquire all
release locks in one stable total order. It MUST verify ownership before
planning or dispatch; failure releases acquired locks in reverse order and
dispatches no write-capable task. Repository lock resource identities MUST be stable across CI entry points
and endpoint aliases, so runs addressing the same repository compete for the same exclusion. Under the Git
release-lock convention, every participant's lock is its own `dispat-release-lock`; one entry lock does not
cover unlocked sources. It MUST revalidate discovery under those
locks; a changed participant set requires releasing the partial acquisition and acquiring the complete set again
in the stable order, not silent expansion. Discovery and compatibility inspection before locking MUST be
read-only; hooks or task setup that can write run only after the complete locked snapshot is validated.
Distributed release execution MUST refuse a configured or environment-based unsafe lock bypass, including
the bypass described for local peer execution in §27.11. Read-only
planning remains lock-free and does not dispatch side effects.

Let `input` contain the fixed repository-qualified heads and complete relevant history, release records, graph,
resolved semantic configuration and explicit release options, plus the verified withdrawal inventory and
receipts required by §§13 and 26. Its release records are the authoritative stores' records as compared under the
locks (§13.2): the orchestrator's checkout is one clone among several, and a plan fixed from records it never fetched
would be signed, digested and dispatched to every node exactly as a correct one is. The semantic release plan is the
pure function `plan = Plan(input)`, including both forward-release and applicable rollback projections. Node placement
configuration is not semantic input:
worker count, placement, completion order, wall-clock time, run IDs and transport branch names MUST NOT change
`plan`. A plan digest MUST use a documented canonical serialization of semantic input and plan content;
transient execution identity MUST be bound separately. The cost obligations of §13.11 still apply to planning;
parallel execution is not evidence that a planner satisfies them. Native records
and peer settlement retain their narrowly defined admission under §27; they
do not authorize replacing the original planning input with an arbitrary ref.

The orchestrator derives a task DAG from `plan`, including required preparation,
tests, builds, transfers, publications and recording gates. A task can be placed
only on a compatible node and only when its exact prerequisites are available.
Independent ready tasks MAY run concurrently. The assignment schedule need not
be deterministic; selected packages, versions, commands and dependency semantics
MUST remain those of the same fixed plan. Where a package's stages may be placed, on the orchestrator, on a worker
or on either, is execution policy of the package or its space, declared beside the relation of §19.2a and, like it,
no part of `plan`.

Every reconciliation that writes a shared manifest or lockfile MUST be an explicit task executed under the
orchestrator's authority, never implicitly by a worker. Such tasks MUST serialize per shared file, and each
admitted result is one prepared input state. Builds fan out only from an admitted state, and every build MUST
bind, in its own manifest, the exact admitted prepared input state it consumed.
Workers MUST NOT each regenerate and race to merge a workspace-wide lockfile
as an implicit part of otherwise independent package builds. A later required
shared mutation creates a new prepared input state rather than mutating one a running build consumes; it
creates a new dependency gate and invalidates affected outputs, and it MUST NOT force all builds in the
repository onto one node when their prepared inputs and output write sets allow independent execution.

Each assignment MUST identify the run, plan digest, task, attempt, ownership
generation, repository-qualified source object IDs, effective configuration and command environment,
toolchain/platform requirements, input receipts, permitted writes and outputs.
The ownership generation identifies the exclusion the run holds: under the Git
release-lock convention it is a value derived from the identities of the lock objects the run holds, for
example the lock tag object IDs of every participating repository, so that a new acquisition always yields a
new generation. Secret references are separate from logged or transported public metadata. A command
environment derived from secret references travels as those references and is resolved on the executing node
from that node's own environment; resolved secret values MUST NOT be written to transport branches,
manifests, receipts or logs.
Workers MUST authenticate the assigning orchestrator and the assignment's integrity; a matching digest alone
is not proof of authority. Nested hooks and commands retain the assigned policy, package-root mapping and
worker authority; they MUST NOT rediscover policy or source history from a transport checkout.
Duplicate delivery MUST be recognized. Only one attempt may be authorized to
perform a given external side effect; an uncertain previous attempt is reconciled
before another is authorized. A task receipt is execution evidence, never a
package release record.

### 28.4 Git synchronization branches

The orchestrator MUST provision isolated task worktrees and temporary coordination branches named
`dispat-worker-<id>-<workinfo>`, created atomically under `refs/heads/`. `<id>` is the node name of the
execution link the branch is addressed to. `<workinfo>` is `<date>-<kind>-<random>`: the date SHOULD use UTC
`YYYYMMDD`, `<kind>` is a short lowercase label of the work (for example `probe`, `build`, `publish`,
`prepare`, `snapshot` or `relay`), and the random suffix MUST provide at least 128 bits of cryptographic
randomness. The date and the kind are diagnostic labels only. The name is an untrusted routing hint that lets
a node list only the branches addressed to it; the authenticated manifest, not the branch name, identifies
node, run, task and attempt, and a node MUST reject a manifest whose node or branch binding disagrees with
where it was found. A branch is bound to one run, repository and task attempt; parallel attempts MUST NOT
share a mutable branch.

Workers synchronize declared source changes and result manifests through these
branches. Each acknowledged checkpoint MUST identify an exact full object ID;
consumers fetch that object and validate its manifest rather than following a
moving branch tip. A consumer MAY obtain the object by fetching a ref that advertises it and then resolving
the exact object ID locally; it MUST then read only from that object ID and never from a ref whose tip can
still move. A tip a node fetched but could not read or verify has not been seen: the node MUST NOT record it as
observed, or one transient read failure silently costs the attempt its only reading of that message and, with it,
the rest of its deadline. Both the orchestrator and the assigned worker MAY advance an attempt's branch. Ref updates and
deletions MUST check the expected previous object ID and attempt ownership, so that an update racing the
other party's update fails instead of overwriting it. Transport-only credentials MUST NOT authorize a worker
to update native release branches, tags or fleet settlement refs.
Acknowledged checkpoints MUST remain fetchable until all required consumers have retrieved and verified them.
Where concurrency permits several tasks at once, use separate branches/worktrees
and admit their results independently as their dependencies become ready.
Concurrency one still supports the same protocol in sequence.

A source snapshot MAY travel without the history behind it. The assignment names the repository-qualified planned
head the snapshot was taken from, provenance binds to that revision (below), and a task's commands may not read
history from a transport checkout (§28.3), so a parentless commit or a bare tree carrying the same bytes serves every
purpose the snapshot has. A snapshot that descends from the planned head carries, on its first transport to a
mailbox, every object reachable from that head, which for a large history is the dominant cost of the run and is paid
again whenever cleanup removed the objects with the refs. A mailbox that already holds the repository's objects, the
repository's own remote for one, a durable ref the mailbox keeps, or a history-free snapshot each avoid it. An
implementation states which it does.

Transport commits MUST NOT be treated as release intent, a pending-window
boundary, a successful release, or a §27 native-head admission. They MUST NOT
be merged wholesale into a release branch or made ancestors of release tags.
The orchestrator validates declared changes against the task's exact base and
write set, detects overlapping/conflicting writes, and admits only authorized
release-file changes through the ordinary native release transaction. Build
outputs remain transport data. Hooks needing to change release-relevant inputs
beyond the admitted writes require a new plan under the existing rules.

Artifact provenance MUST bind the exact source revision and admitted release-file state actually used
for its build. The ordinary native record MUST identify that publication: tag-only recording retains the
planned source head under §19.1, while an enabled native release commit follows §27.7. Neither policy is
replaced by tagging a transport commit or requiring a source commit solely for distributed execution.
Generated release-file digests remain execution provenance when ordinary tag-only policy does not commit them.

Before authorizing publication, the orchestrator MUST compare the result's full effective input identity
against the current admitted relevant input closure. A native head advance preserves a completed result only
when its effective input state still matches; admitting a head is not permission to ignore changed source
bytes. Integrating one task MUST NOT silently alter another task's inputs. Overlapping writes require explicit
ordering, revalidation and rebuilding when necessary; a change outside permitted native transitions requires
a new plan. Transport branches MUST remain outside the heads, release refs and gitlinks supplied to `Plan(input)`.
This is not an automatic merge policy for arbitrary generated files.

### 28.5 Dependent build outputs are first-class inputs

**Source synchronization alone is insufficient.** A provider's Git revision or
worker branch does not make its ignored/untracked `dist` files, generated
types, assets or package bundle available to a consumer on another machine.
A conforming implementation MUST transport the outputs needed by a local-output
dependency, including declared ignored/generated files. It MUST NOT satisfy
distributed execution merely by recloning sources and rebuilding the provider
closure on every consumer node, or by pinning the entire connected JS graph
to one worker.

A successful build MUST emit a manifest binding its outputs to the run, plan,
task and attempt, repository-qualified source revision, admitted release-file
state, target package/version, toolchain and platform, and input/output digests.
Required metadata includes relative paths, file types, modes and link targets
where relevant. The transfer MUST preserve the package's required layout while
rejecting paths or links escaping the destination. Consumers MUST verify the authenticated manifest, content integrity and compatibility before their stage
becomes ready. Installation MUST be atomic at the task-input boundary: a task cannot observe a partial output
set. A completed build publishes one admitted output set for its exact attempt, not a moving directory shared
between consumers. Metadata size, file count, decompressed byte count and transfer time MUST have documented
bounds. Undeclared or unverifiable required inputs prevent cache reuse; they MUST be declared and validated
before an output can satisfy a task's prerequisite.
Missing, stale, incomplete or incompatible outputs fail that prerequisite;
they MUST NOT be silently substituted with registry contents or a new build
under a different input.

Required output bytes MUST be available through the temporary Git transport
branch (for example a task result tree containing explicitly added outputs),
or through immutable content-addressed bundles whose references and digests are
in that branch. Such bundle transfer is a transient data service, not an
additional authoritative release-state database. References alone are not a
transfer: every receiving node MUST be able to retrieve and verify the bytes.
If no bundle service is configured, Git transport MUST still support the
declared outputs. Ordinary source checkout rules ignoring build output MUST
NOT exclude it from the output manifest.

On the receiving node the engine materializes the consumer's pinned source,
applies its admitted release-file state, restores the provider outputs, and
links/installs them according to the declared workspace integration. An input closure is materialized outermost
first: where one repository's checkout lies inside another's, as a fleet link's does (§27.11), the enclosing
repository is placed before the one inside it, so that the inner checkout lands in its place and is not overwritten by
the outer one. The run's root on a node is a path prefix under which the assigned repositories are placed at their
run-relative paths; it need not itself be a repository the task received. The
integration MUST prevent an install lifecycle from rebuilding already supplied
providers implicitly. A raw `node_modules` copy is neither required nor a
portable default; native outputs require compatible platforms/toolchains.
Task inputs MUST include all required build dependencies, including dependencies whose packages need no
new release and build-only edges outside the release propagation kinds. The task graph MUST itself be acyclic;
an unschedulable build cycle is an execution-configuration error, not a reason to change release intent or to
ignore a required build dependency. Required preparation tasks do not create
new release obligations or change `plan`.

For a local-output dependency where package `B` consumes provider `A` (package edge `B → A` in §2),
readiness is the following **task precedence**, whose arrows denote happens-before rather than package edges:

`build(A) -> export(A) -> verify/materialize(A at B's node) -> build(B)`.

For a registry-availability edge, readiness still requires the provider's
publication and all applicable recording gates; transferring local bytes MUST
NOT replace that condition. These are the `build` and `publish` relations of §19.2a. Under its third relation, `none`,
the edge carries no output and the two builds are independent tasks; only the publications keep their order. A
provider a consumer reaches only across `none` edges is no input of that consumer and is not prepared for it.
Transfers to different ready consumers MAY overlap
and identical immutable blobs SHOULD be reused. Implementations SHOULD retain
incremental Git objects and verified outputs between assignments, but any cache MUST be dispensable and validated against the complete semantic input identity. Cross-run
reuse preserves the original output manifest as provenance and requires a new admission receipt bound to the
current run, plan and task attempt. An old run's receipt never grants current execution or publication authority.

### 28.6 Publication, recording and recovery

Build, test and publish commands MAY execute on workers. Repository ownership
does not change: publication uses the owning package's checkout and policy.
The orchestrator alone admits results and authorizes publication after all
ordinary test, dependency, settlement and relevant-input checks. For this profile it serializes publication and recording within one owner, including a single-history
monorepo, extending §27.2's owner lane to distributed execution; isolated builds
within that owner may still run concurrently. Independent owners retain allowed
publication concurrency. A worker MUST NOT independently move release branches,
create release tags, settle peer links or finalize the fleet. The orchestrator
performs those native transactions under the existing ownership/locking rules.

Hooks that bracket a delegated stage execute with that stage on the executing node, under that assignment's
worker authority. Run-level hooks, native recording, hooks that observe a completed publication, failure
hooks and any restoration of a package's working tree after failure execute under the orchestrator.

Authorization to publish is an explicit, single-use step bound to one task attempt. The orchestrator issues
it only after the package's `beforePublish` hook has completed on the executing node and after the
relevant-input and lock-ownership checks of this section, which is how §27.2's revalidation immediately after
`beforePublish` and before the publish command is preserved when that hook runs on a worker. The publisher
MUST NOT start its publication command before it has observed that authorization for its own attempt, and an
authorization already issued MUST NOT be reused by another attempt or for another effect.

An implementation MAY bound an authorization in time. The authorization then states the instant after which the
publisher MUST NOT start its command, the assignment states the deadline at which the executing node itself ends the
command and everything it started, and the executing node enforces both without waiting to be told. Under a documented
bound on clock disagreement between nodes, the later of those two instants plus that bound is then a proof of
quiescence that needs no reply from the node, which no other rule of this section provides for a node that has become
unreachable. The bound limits when an effect may begin and how long it may run. It is not an expiry of the release
lock, which still never lapses by itself (VCS-PROTOCOL.md §4), and it authorizes no automatic takeover.

The orchestrator MUST retain and verify lock ownership for the lifetime of all
authorized effects. Assignments and results are bound to its ownership
generation; stale results cannot authorize publication or native recording.
After lock loss, no new effect may start: the orchestrator issues no further assignment and no further authorization.
Recording an effect that was authorized before the loss is not a new effect. It MUST still be attempted, as the
create-only record of §19.1, because a publication left unrecorded is a wedge the next owner can clear only by
§19.4, and a create-only record cannot overwrite what a successor wrote. A timed-out worker is not proof its
publisher stopped: cancellation must be acknowledged, a fencing mechanism must
be enforced where the effect occurs, or the outcome must be reconciled before
ownership is safely handed over. A cancellation and its acknowledgement MUST state where the attempt was when it
stopped, and in particular whether the command that performs the effect had started: without that, an attempt
withdrawn before its command is indistinguishable from one whose outcome is unknown, and the run can never report
the former as not published. A generation field alone does not fence an
arbitrary registry command. No automatic takeover may simply expire ownership
and republish while the previous process can still act.

If a worker fails before publication, retry eligible computation under a new
attempt without discharging the package. If publication may have succeeded,
use §19.4 identity reconciliation or a safely repeatable publisher before a
new attempt; neither a missing reply nor a missing tag proves failure. Only the
ordinary durable source release record discharges the release obligation.
Preserve prior successes, block affected dependents, and continue unrelated
safe work under the ordinary failure rules. Worker completion order does not
alter the consumer-owned release boundaries.

Fresh invocations reconstruct pending releases from authoritative repository records. Rollback requests and
withdrawal receipts retain the separate rules of §26; task receipts MUST NOT discharge either rollback or
ordinary release intent. Scheduling a rollback handler remotely does not relax its activation, identity,
consumer-first order or reconciliation obligations. Verified task outputs MAY be reused when their full input identity
still matches, but loss of a worker, branch, bundle or cache cannot erase
pending work or successful release records. Synchronization state is not a
second recovery ledger. Cleanup removes only owned temporary refs and bundles,
after all consumers and uncertain operations are settled. Failed attempts and
receipts retain diagnostic provenance. Cleanup failure is reported without
rewriting successful package outcomes; borrowed nodes' unrelated data is never
deleted. Repository locks are released only after authorized operations have quiesced or been safely fenced, then
in reverse acquisition order. If that cannot be proved, the run MUST fail and retain or restore exclusion
rather than report successful cleanup and permit overlapping effects. Such a run ends holding its exclusion and
holding no record of the package whose outcome it could not establish: the retained lock is a ref and never a
release record, and the next run treats that package as pending until §19.4 says otherwise. An attempt that was
never authorized to
perform an external effect, holding neither publication authorization nor permission to write native refs, is
safely fenced by revoking its coordination ref: its expected-previous-object-ID updates can no longer succeed.
Revocation fences an attempt nothing has been admitted from; a branch a result was admitted from is a checkpoint
its consumers may still need (§28.4) and is removed by cleanup, not by revocation.
Releasing repository locks need not await such an attempt's acknowledgement. An authorized publisher is not
fenced this way and still requires acknowledged cancellation, an effective fence where the effect occurs, or
reconciliation. A changed ownership generation rejects stale receipts but is not, by itself, an external
publication fence.

A run that ended without releasing its locks leaves them for an operator (VCS-PROTOCOL.md §4), and the operator needs
the same proof of quiescence the run would have needed. Synchronization state is not a recovery ledger for releases,
but it is the evidence for this one question: an attempt whose coordination branch carries a publication authorization
and no terminal result may still be publishing. An implementation MUST therefore make an abandoned run findable from
its lock, by recording the run identity in the lock object or an equivalent documented place, and MUST document the
order of recovery: revoke the run's coordination refs first, so that no attempt not yet authorized can begin; then
establish, for every attempt that was authorized and has no result, acknowledged cancellation, an effective fence,
elapsed authorization bounds where the implementation states them, or the reconciled outcome of §19.4; only then remove
the lock. The remedy a lock diagnostic prints MUST NOT tell the operator to delete the lock without naming those steps.

### 28.7 Required example and performance boundary

In one large JS repository, let the task-precedence notation `assets => {ui, docs} => app` mean that `ui` and `docs` consume
`assets`, and `app` consumes both. The package edges of §2 point in the opposite direction. Worker A builds `assets` once and exports its required outputs.
Workers B and C receive verified outputs and build `ui` and `docs` concurrently.
The assigned app worker receives both output sets and builds `app`. Publication
and source recording then obey the existing stage/owner constraints; an isolated
registry-based image consumer still waits for image publication when required.
Neither B nor C rebuilds `assets`, and the app worker does not rebuild their
closures. This example MUST work without converting the monorepo to a polyrepo
or publishing private intermediate JS packages solely for transport.

The required gain is actual distribution and reuse of dependent builds with
parallel ready branches, including large local JS workspaces. Git provides
source/result transport; it does not itself schedule tasks, transfer omitted
build products, guarantee compatible environments, or make effects exactly-once.
A strictly serial dependency chain retains its critical path. Transfer and
setup overhead can outweigh saved compute on small jobs, so universal speedup
is not a semantic guarantee. Before making an implementation performance claim,
compare the same pinned large-JS fixture locally and on multiple nodes, including cold/warm setup, per-node resources, transfer bytes/time, task execution counts, total work,
wall time, repetitions and variability, and artifact equivalence. The fixture and environment MUST be
identified with the measurements; skipped or failed trials MUST NOT be described as successful speedups. Require demonstrated wall-time benefit on a
declared representative parallel fixture; do not call source-only dispatch or
duplicate prerequisite compilation a distributed-build success.

### 28.8 Conformance vectors

These are required cases. Whether an implementation has executed them is that implementation's claim, made where its
status is stated, and none of them is evidence of speedup. Some vectors state a condition on the implementation. A
vector whose condition does not hold for an implementation is not applicable to it: it is neither satisfied nor
violated, and every remaining vector still applies in full.

1. No role or worker list: local orchestrator behavior remains unchanged.
2. A worker receives direct or nested release initiation: refuse before locks
   or side effects. An orchestrator-capable node accepts a delegated task with
   worker authority and does not initiate another run.
3. Add workers independently to each of the three history modes: same fixed
   inputs produce identical package/version plans and source owners.
4. Two CI entry nodes target overlapping repository sets: at most one acquires
   the complete lock set and dispatches effects; failure releases its partial
   set. An unsafe lock bypass refuses distributed execution.
5. Vary worker count, date, randomness and completion order: `Plan(input)` is
   unchanged. Concurrency limits hold locally and across the run.
6. Two ready tasks share a repository: use isolated worktrees and branches;
   conflicting declared release-file writes are detected before admission.
7. Branch tips move after dispatch or a stale receipt arrives: use only the
   assigned full object ID and matching plan/attempt/ownership generation.
8. Ignored JS outputs from `assets` reach `ui` and `docs` on separate workers:
   both builds use those bytes, overlap when ready and never rebuild `assets`.
   The app consumes both output sets without rebuilding either provider.
9. An unselected provider is needed by a selected consumer: supply or prepare
   its verified output without adding a package release to the semantic plan.
10. An output is absent, corrupt, incompatible, or path-escaping: fail the
    prerequisite before consumer execution; preserve its release obligation.
11. A registry-based consumer has local provider bytes but no required
    publication/record: remain blocked. Local transport does not weaken the edge.
12. A worker's Git transport commit is fetched: no new intent, release baseline,
    tag, or admitted native head arises. Temporary commits stay outside release
    ancestry; only validated release-file changes enter native recording.
13. Where publication commands execute on workers, a worker publishes and its
    reply is lost: reconcile artifact identity before reauthorization; never
    infer a failed publish from the timeout.
14. A worker disconnects or the orchestrator loses its lock: no new effects and
    no unsafe handover. Where publication commands execute on workers and a
    publisher is in flight, no unfenced duplicate attempt arises.
15. A source tag succeeds and later checkpoint/cleanup fails: preserve source
    success and report the specific failure; no source republication for cleanup.
16. Delete temporary branches/caches after a completed run and replan unchanged
    inputs: release debt remains discharged by source records. Delete them after
    a prepublication failure: unfinished work remains discoverable and rebuildable.
17. Repeat the JS fixture without a separate bundle service: branch-backed
    transport still carries every declared build output.
18. Shared JS manifest/lockfile preparation executes under the orchestrator only
    and serializes per shared file; each build records the admitted prepared
    state it consumed, consumers of one state see identical bytes, and no worker
    competes to write the lockfile.
19. Independent builds in one repository overlap, but its publication/recording
    transactions serialize; cross-owner publication remains dependency-safe.
20. Where verified outputs are reused across runs, a cached output has matching semantic inputs but belongs to
    an earlier run: verify its original provenance and issue a current admission receipt before reuse. An old
    run's receipt alone authorizes no task or effect.
21. A worker pool accepts two runs: its total active command tasks remain within each node's local capacity,
    and each run's stage budgets remain global. An unacknowledged timed-out attempt still consumes capacity; an
    assignment no node has claimed consumes none and is offered again.
22. A build-only dependency outside propagation kinds supplies ignored output: transfer it before the consumer
    builds. Where the implementation admits execution-only edges that the release graph does not already
    reject, an execution-only cycle fails preflight without changing the semantic release plan.
23. An authenticated assignment declares `worker` authority but its hook invokes release or writes a native
    release ref: refuse the operation. A role or endpoint in a linked repository cannot elevate the assignment.
24. A native record changes an in-flight task's relevant effective input bytes: withhold its publication and
    rebuild or require a new plan as the input rules dictate. An unrelated admitted change alone does not
    invalidate an otherwise matching result.
25. A receiver sees a valid manifest but only part of its file set: remain unready until all required bytes
    have been verified and installed. A digest, filename or branch reference without retrievable bytes fails.
26. Where the implementation advertises the rollback profile of §26, enabled rollback work precedes ordinary
    publication and records only its §26 completion receipt. A worker task receipt never replaces that receipt
    and never discharges a release obligation.
27. The orchestrator's checkout lacks a release tag that its remote holds on a commit reachable from the planned
    head: refuse with `E196` under the locks, before the plan is fixed and before any assignment. Planning that
    package from its older baseline, or replacing the remote tag afterwards, is non-conforming (§13.2, §19.1).
28. The orchestrator loses its lock after a publication was authorized and before its record is written: issue no
    further assignment or authorization, and still attempt the create-only record of that publication. A record
    another owner already wrote is not overwritten.
29. A run ends with its locks held and one attempt authorized without a terminal result: the lock names the run,
    and the documented recovery revokes the run's coordination refs and settles that attempt before the lock is
    removed. A remedy that says only to delete the lock is non-conforming.
30. Where the implementation bounds authorizations in time, a publisher that observes its authorization after the
    stated instant does not start its command, and a command still running at its assignment's deadline is ended
    by the executing node without a request from the orchestrator. The release lock does not lapse in either case.

### 28.9 Operational diagnostics

The following named categories are normative outcome classes, not allocations of new `E` or `W` numbers.
Implementations MUST expose stable machine-readable categories, named by the identifier each row states, and
the applicable run, task, attempt and repository identities without credentials. An implementation MAY
additionally attach its own numbered codes to a category. Existing numbered diagnostics retain their exact
§16 conditions; profile-specific failures MUST NOT be recast as malformed commit units or suppressed by
parser leniency.

| Category | Identifier | Required handling |
| --- | --- | --- |
| Execution configuration | `execution-configuration` | Invalid role, concurrency, endpoint, protocol, task graph or transfer capability fails before dispatch. |
| Execution authority | `execution-authority` | Worker initiation, unauthenticated assignment, stale ownership or unauthorized writes are refused before the affected operation. A duplicate receipt cannot create a second side effect. |
| Input or output integrity | `io-integrity` | Missing, changed, incomplete, incompatible or escaping input/output data fails the affected prerequisite and blocks required consumers. Preserve unrelated successful work. |
| Publication outcome unknown | `publication-unknown` | A lost acknowledgement or unfenced in-flight publisher withholds reauthorization and unsafe lock handover until quiescence, effective fencing or identity reconciliation is proven. Report the run as incomplete and return nonzero. |
| Native recording or lock failure | `native-recording-or-lock` | Apply §§19 and 27, preserve successful publication and fail the run; do not retag transport work or release unsafe exclusion. |
| Transport cleanup | `transport-cleanup` | Report owned temporary refs or bundles that could not be removed. Their existence cannot erase a release record. Harmless retained transport data MAY be a warning; lost exclusion or unresolved effects MUST be an error. |

The task/run summary MUST distinguish completed computation, admitted outputs, successful publication, durable
native recording, blocked dependents and unknown external outcomes. A completed task is not synonymous with a
released package. A prepared provider (vector 9) has the shape completed computation, admitted outputs, no
publication and no record; the summary states it so, and neither omits the provider nor lists it as released.
Diagnostic output order follows the semantic order of §17.2, independently of arrival order.
