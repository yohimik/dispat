# Conventional Commits — Monorepo Extension (CCME)

**Version:** 1.0.0 **Status:** Normative specification — stable, ready for implementation **Extends:** Conventional
Commits 1.0.0 **Versioning model:** Semantic Versioning 2.0.0 **Version store:** git tags of the form
`<package>@<version>`
**Conformance:** §17 · **Security considerations:** §18 · **Test vectors:** Appendix B

Requirement levels follow RFC 2119 (§2). This specification is itself versioned under SemVer; see §17.3 for what
constitutes a patch, minor, and major revision of the document.

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
21. [Appendix A — Regular expressions](#21-appendix-a--regular-expressions)
22. [Appendix B — Conformance test vectors](#22-appendix-b--conformance-test-vectors)
23. [Appendix C — Formal grammar (ABNF)](#23-appendix-c--formal-grammar-abnf)
24. [Appendix D — Worked examples](#24-appendix-d--worked-examples)

---

## 1. Summary

CCME adds five capabilities to Conventional Commits, chosen so that a single commit can fully describe its release
intent across a workspace of many packages:

| # | Capability                                                                                                     | Syntax                                                         |
|---|----------------------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| 1 | **Multiple units per commit** — one commit message describes several independent changes                       | blocks separated by `---`                                      |
| 2 | **Dependent propagation** — opt in, and declare how far a change bumps its consumers                           | `^`, `^minor+2`, `^^minor` / `Propagate:` + `Propagate-Depth:` |
| 3 | **Cancellation** — discard accumulated, unreleased release metadata without touching code                      | type `cancel`                                                  |
| 4 | **Explicit or derived targeting** — scope names a package, or is omitted to derive packages from changed files | `feat(api,web)` / `feat`                                       |
| 5 | **Prerelease channels** — enter, iterate on, graduate, and carry consumers along, each under its own depth     | `%beta`, `%%beta++1`, `%beta>stable`                           |

Capabilities 2 and 5 are two **independent axes** of the same idea. A commit says separately how far a *version bump*
travels (`^`, `^^`, `+N`) and how far a *channel* travels (`%%`, `++N`), because the answers differ: a change usually
needs its consumers rebuilt, and much less often needs them moved onto a prerelease line. Both axes default to depth
`0`, so a commit that says nothing releases its own packages and nothing else (§8.3, §8.3a).

Everything is designed so that a conforming parser can be written with a linear index scan and no regular-expression
engine (§20). Appendix A gives regular expressions for implementers who prefer them.

Alongside these five sits one guarantee that is not syntax: **a release is durable under partial failure.** Publishing
is a sequence of independent, individually-fallible registry operations, and any of them may fail. The engine therefore
computes propagation against the *consumer's* release position rather than the provider's (§13.7a), publishes in
dependency order and skips the dependents of anything that failed (§19), and converges to an empty plan by re-running
(§13.7c). A half-finished release is a resumable state, not a lost one.

**Example carrying all five:**

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
| **Pending window**       | The set of commits used to compute a package's next version (§13.3). Written `W(P)`.                                    |
| **Release engine**       | The tool implementing this specification.                                                                               |
| **Inert**                | Syntactically valid but resolving to zero packages; produces a warning, never an error.                                 |
| **Run**                  | One execution of the engine against a fixed `HEAD`: compute a plan (§13), then publish it (§19).                        |
| **Admission**            | The test deciding whether a unit propagates to a dependent. Evaluated against the **dependent's** window.               |
| **Stale**                | A package that would receive a non-`none` propagated bump — it has not released past a commit that propagates to it.    |
| **Catch-up release**     | A release whose entire cause is a propagation from a dependency that has **already** been published (§13.7a).           |
| **Publish graph**        | The graph used to order publication, and to decide what a failed publish blocks (§19.2).                                |
| **Blocked**              | Planned, but not attempted in this run because a dependency's publish failed (`W194`).                                  |
| **Convergence**          | The property that re-running the engine at a fixed `HEAD` eventually yields an empty plan (§13.7c).                     |
| **Publish target**       | The registry a package's artefact is uploaded to, or `none`. Does not affect whether the package is released (§13.10a). |
| **Released**             | Assigned a version, tagged, and its manifest written. Every released package is tagged, whatever its target.            |
| **Bump axis**            | Propagation of a version bump to dependents. Governed by `Propagate`, `Propagate-Depth`, `Propagate-Scope`.             |
| **Channel axis**         | Propagation of a channel to dependents. Governed by `Propagate-Channel`, `-Depth`, `-Scope`. Independent of the bump.   |
| **Channel**              | `stable`, or the prerelease identifier a package's baseline carries. Derived from tags, never stored (§11.1).           |
| **Transition**           | A channel value of the form `<from>><to>`: move only packages whose baseline channel is `<from>` (§11.2).               |
| **Resolvable**           | A released version an installer will select for a given consumer. A prerelease is resolvable only on its own line.      |
| **Graduation**           | Ending a package's prerelease line by releasing it on `stable` (§11.5). Never happens implicitly.                       |

`max(a, b)` over bumps returns the higher of the two in the ordering above.

---

## 3. Relationship to Conventional Commits 1.0.0

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
| Any casing of `type` — `Feat: x`                     | `E101`; types are lowercase (§5.1)               | `lenient: true` lowercases it, `W101`       |
| Spaces and commas inside a scope — `feat(my api): x` | `E102`; and a comma separates scope terms (§5.2) | Rename the scope; there is no CCME spelling |
| A bare `---` line in a body                          | Splits the message into units (§4.2)             | Escape as `\---`, or configure `separator`  |

The base spec makes only `BREAKING CHANGE` case-sensitive; CCME extends that to `type`, so `Feat: x` — valid CC 1.0.0,
and accepted by much CC tooling — is an error here. `feat(my api): x` has no CCME spelling at all, because whitespace
inside a scope-set is exactly how the grammar detects a malformed header, and the comma is load-bearing in a multi-term
scope-set. The third differs in kind from the other two: a `---` in a body does not make the message *invalid*, it
changes the *outcome*, since what was one unit becomes several and each subsequent paragraph head is parsed as a header.

**Lenient mode is the migration path for the first of these.** A repository adopting CCME over existing CC 1.0.0 history
SHOULD start with `lenient: true`, which downgrades the casing error to `W101` and the separator error of §5.5 to
`W121`, and tighten once the log is clean. Lenient mode does not rescue the other two: a scope containing a space must
be renamed, and a `---` in a body must be escaped or the separator reconfigured.

Base-spec elements retained without modification: `type` (modulo casing), `(scope)`, `!`, `description`, body,
footer/trailer form, `BREAKING CHANGE:` and `BREAKING-CHANGE:` footers, and the `revert` convention.

CCME adds: the `---` separator, multi-term scope-sets, inline directives, the types `cancel` and `release`, and the
footer keys listed in §8.1.

---

## 4. Message structure

### 4.1 Input normalisation

The release engine consumes the **cleaned** commit message — the output of `git log --format=%B`, i.e. after git has
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
message — that is, into the final unit — regardless of unit boundaries. Treating them as unit directives would attach
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

Breaking-ness is a property of a unit, so in a multi-unit message each spelling binds only to its own unit — see §8.1.1
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

* **how far does the version bump travel?** — the bump axis: `^`, `^^`, `+N`, and the `Propagate*` keys;
* **how far does the channel travel?** — the channel axis: `%%`, `++N`, and the `Propagate-Channel*` keys.

**Both axes are opt-in, and both default to depth `0`.** A unit with no propagation directive touches only its own
packages. One sigil reaches the direct consumers, and the depth token extends the reach:

| Written     | Bump reaches               | Bump to dependents |
|-------------|----------------------------|--------------------|
| *(nothing)* | nobody                     | —                  |
| `^`         | direct consumers           | `patch` (default)  |
| `^minor`    | direct consumers           | `minor`            |
| `^^`        | every transitive dependent | `patch` (default)  |
| `^^minor`   | every transitive dependent | `minor`            |
| `+N`        | up to `N` edges away       | `patch` (default)  |
| `^minor+3`  | up to 3 edges away         | `minor`            |

| Written         | Channel reaches            | Channel they take                  |
|-----------------|----------------------------|------------------------------------|
| *(nothing)*     | nobody                     | —                                  |
| `%%beta`        | direct consumers           | `beta`                             |
| `%%beta++3`     | up to 3 edges away         | `beta`                             |
| `%%beta++*`     | every transitive dependent | `beta`                             |
| `++1`           | direct consumers           | `inherit` (default) — the origin's |
| `++*`           | every transitive dependent | `inherit` (default) — the origin's |
| `%%none`, `++0` | nobody                     | —                                  |

The bump ladder reads `nothing` / `^` / `^^`, with `+N` when the answer is neither 1 nor all. The channel ladder reads
`nothing` / `%%x` / `%%x++*`, with `++N` for the same reason. `^^minor` is exactly `^minor+*`; `^^` on its own is
exactly `+*`. There is no doubled spelling of "channel to every level" — write `++*`.

A bare `^` and a bare `^^` are both legal: the value is the bump, and it has a default. This is the one place where an
empty value after a sigil is not `E111` (§16). A bare `%%` or `++` is `E111`: neither has a default worth guessing, and
`++` with no number is not a depth at all.

**The two axes never constrain one another.** A channel may be propagated further than a bump, less far, or with no bump
propagation at all — `feat(core)%%beta` puts the direct consumers on the beta line without giving them a bump, and
`feat(core)^^minor++1` bumps the whole closure while moving only the direct consumers onto the origin's channel. The
channel axis in particular does **not** require the unit to produce a bump, which is what lets a `release` unit — whose
bump is always `none` (§7.2) — carry a graduation to its dependents.

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
with an explicit `+N` where `N` is not `all` is `E113` — two spellings of depth disagreeing in one header;
`^^minor+*` is legal but redundant and emits `W110`. `%%` behaves like `^`, not like `^^`: it implies
`Propagate-Channel-Depth: 1` only in the absence of an explicit `++N`, so `%%beta++3` is channel depth `3` with no
diagnostic.

**Why `%%` is not a reach.** `%` concerns the unit's own packages; `%%` changes the *audience* to the dependents.
`^`/`^^` keep one audience and change the *distance*. A channel has no distance dimension of its own to intensify — how
far it goes is what `++N` says — so a doubled `%` could not have meant what a doubled `^` means. Given that, the two
candidate readings were a doubled sigil (`%%`) or a compound one (`^%`), and `%%` was preferred because it keeps `%`
reading as "channel" throughout, whereas `^%` would make `^` a namespace prefix in one form and a bump sigil taking bump
words in every other.

#### Channel transitions

Every channel value — on `%` and on `%%` alike — MAY be written as a **transition**:

```
<from>><to>
```

It means: *move only the packages whose current channel is `<from>`, and move them to `<to>`.* Packages that are on some
other channel are left entirely alone — no channel change, and no release on the channel axis.

* `<from>` is a channel name, `stable`, or `*`. `*` matches **any prerelease channel**, and never matches `stable`.
* `<to>` is a channel name or `stable`. `inherit` is **not** a valid `<to>` (`E111`): a transition names the train it is
  ending, and "end it, onto whatever the origin happens to be on" is exactly the vagueness the form exists to remove.
* A package's channel for matching purposes is the channel of its **baseline** (§11.1) — the tag it currently sits at,
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
not match `beta`, and is simply not touched — no error, no redundant release, no need to hand-maintain the scope-set. It
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
| `feat: add retry`                       | `feat`     | derived                | —                                  | no       |
| `fix(api): null guard`                  | `fix`      | `api`                  | —                                  | no       |
| `feat(api,web,@acme/ui): unify theme`   | `feat`     | 3 packages             | —                                  | no       |
| `feat(*,-docs-site): bump runtime`      | `feat`     | all but `docs-site`    | —                                  | no       |
| `feat(.,-legacy): new codec`            | `feat`     | derived minus `legacy` | —                                  | no       |
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
| `cancel(*): reset release state`        | `cancel`   | all                    | —                                  | n/a      |

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

* Unknown **include** names are `E130` — a typo would silently drop a release otherwise.
* Unknown **exclude** names are `W130` — excluding a package that was deleted or renamed is harmless and common during
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
* Paths matching `ignoredPaths` are removed before ownership resolution. The default list is **empty** — notably,
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
| **any type** with `!` or `BREAKING CHANGE:` | `major`             | Overrides the row above.                |
| `cancel`                                    | *control*           | §10. Never produces a bump.             |
| `release`                                   | *control*           | §7.2. Never produces a bump on its own. |

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
`Release-As: 3.0.0`; it sets nothing and is inert (`W141`). This applies to every footer in §8.1 — the inline sigils of
§5.3 are the only directives that live on the header line.

* `release` contributes bump `none`. It cannot be combined with `!` (`E141`).
* A `release` unit whose only effect would be `Channel: stable`, or a transition ending on `stable`, triggers
  **graduation** (§11.5) even though its own bump is `none`.
* A `release` unit carrying a `Release-As` footer holds, resumes, or pins the version per §8.6. It cannot change a
  bump — `release` contributes `none` and no footer alters that.
* A `release` unit with no directive at all is inert (`W141`).

**`release` and the two axes.** The bump axis requires a bump: a unit whose own bump is `none` propagates none, so
`release(core)^minor` reaches nobody and is inert for propagation (#38a). This is neither `W152` nor `W201`: the
directive resolves to a real value at a real depth, and what silences it is the type's bump of `none`, not anything
written in the directive (§8.3b). The **channel axis does not**, and that asymmetry is the point of the type. A channel
is a statement about *where a package publishes*, not about whether it changed, so it is coherent — and necessary — for
a package that changed nothing to move its consumers off a prerelease line. `release(core)%stable%%beta>stable++*` is
therefore the canonical graduation of a whole train, and it is the only shape in which a unit with no bump releases
other packages.

### 7.3 `revert` versus `cancel`

Both undo something; they operate on different layers and are not interchangeable.

|                           | `revert`                                   | `cancel`                                                                        |
|---------------------------|--------------------------------------------|---------------------------------------------------------------------------------|
| Layer                     | Source code                                | Release metadata                                                                |
| Changes files             | Yes — the commit contains the inverse diff | No — typically an empty or unrelated commit                                     |
| References a target       | Yes, `Reverts: <sha>` per the base spec    | **No.** Deliberately carries no target and no reason                            |
| Own version bump          | Yes (`patch` by default)                   | None, ever                                                                      |
| Effect on history reading | None                                       | Truncates the pending window for its scopes (§10)                               |
| Effect on published tags  | None                                       | None — never deletes or rewrites a tag                                          |
| Typical use               | "We shipped a bug; undo the code"          | "The migration/importer invented bumps that don't exist; start the ledger over" |

`revert` remains defined exactly as in the base specification; CCME adds nothing to it beyond the scope-set and
directive syntax available to every type.

**The `revert` trap.** Reverting a commit does **not** remove the reverted commit's bump from the window. If a `feat!`
and a `revert` of it both sit in one window, the package still takes a `major` — the `feat!` contributed `major`, the
`revert` contributed `patch`, and `max()` gives `major`. This is correct: the release will contain neither the feature
nor its removal, but consumers may already have seen the `feat!` in a prerelease, and silently downgrading the bump
would be worse. `Reverts:` is informational precisely because acting on it would require the engine to reason about
whether the revert was complete. To also drop the version signal, pair the revert with a `cancel`.

There is a third, weaker option that is often the one actually wanted: `Release-As: none` holds a release without
discarding anything (§8.6.1). The three form a ladder — **`revert`** undoes the code, **`Release-As: none`** defers the
release, **`cancel`** erases the metadata.

---

## 8. Directives: footers and inline shorthand

### 8.1 Footer registry

Footers are git trailers: `Key: value`, one per line, in the unit's final paragraph. Keys are matched
**case-insensitively**, but not hyphen-insensitively (`Propagate-Depth` and `propagate-depth` match; `PropagateDepth`
does not). **`BREAKING CHANGE` is the sole exception and is case-sensitive** — see §8.1.1.

| Footer                                | Inline            | Values                                                     | Default                      | Scope                       |
|---------------------------------------|-------------------|------------------------------------------------------------|------------------------------|-----------------------------|
| `BREAKING CHANGE` / `BREAKING-CHANGE` | `!`               | free text                                                  | —                            | unit                        |
| `Propagate`                           | `^x` / `^^x`      | `none` \| `patch` \| `minor` \| `major` \| `inherit`       | `patch`                      | unit                        |
| `Propagate-Depth`                     | `^` / `^^` / `+N` | non-negative integer \| `direct` \| `all`                  | `0` (no propagation)         | unit                        |
| `Propagate-Scope`                     | —                 | scope-set                                                  | `*`                          | unit                        |
| `Propagate-Channel`                   | `%%x`             | `inherit` \| `none` \| `stable` \| `<ch>` \| `<from>><to>` | `inherit`                    | unit                        |
| `Propagate-Channel-Depth`             | `%%` / `++N`      | non-negative integer \| `direct` \| `all`                  | `0` (no channel propagation) | unit                        |
| `Propagate-Channel-Scope`             | —                 | scope-set                                                  | the unit's `Propagate-Scope` | unit                        |
| `Channel`                             | `%x`              | `<channel>` \| `stable` \| `<from>><to>`                   | inherited from baseline      | unit                        |
| `Release-As`                          | —                 | exact semver \| `none` \| `auto`                           | —                            | package, this window (§8.6) |
| `Reverts`                             | —                 | commit sha                                                 | —                            | unit, informational         |

Unknown footer keys are ignored with `W150` — this keeps CCME compatible with organisation-specific trailers.

A footer value MAY span multiple lines: a continuation line is any line in the footer block that is not itself a footer
start (§20.5). Multi-line values are only meaningful for `BREAKING CHANGE`; for other keys the continuation is joined
with a single space before parsing, and a resulting invalid value is `E151`.

### 8.1.1 `BREAKING CHANGE` — the exception to four rules

`BREAKING CHANGE` breaks more of this grammar's regularities than any other token, so its handling is collected here
rather than scattered.

**1. It is not a type.** Uppercase and containing a space, it fails §5.1 twice. A header line beginning with
`BREAKING CHANGE` is `E100` with a dedicated message (§5.1).

**2. It is the only footer key containing a space.** Every other key is `[A-Za-z0-9-]+`. The scanner therefore
special-cases the literal string before falling through to the generic key loop (§20.5). Implementations MUST NOT
generalise this into "keys may contain spaces" — that would make ordinary body prose like `Note this is important: ...`
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
multi-unit message it does not reach the other units — see vector 29. The `!` marker (§5.4) is exactly equivalent; both
MAY appear on one unit, and doing so is not an error, since the footer carries prose the marker cannot.

**Value.** The value is free text and MAY span several lines; continuations need no indentation, because a
`BREAKING CHANGE` footer consumes subsequent non-footer lines to the end of the block (§20.5). The value is never parsed
and never validated — an empty value is legal, though `W157` notes that a breaking change with no explanation is
unhelpful to consumers.

**Bump.** `major`, overriding whatever the type would have produced, subject to §12.6 for `0.y.z` packages. A
`BREAKING CHANGE` footer on a `cancel` unit is `E171`; on a `release` unit it is `E141`.

### 8.2 `Propagate`

Declares the bump given to **dependents** of this unit's packages.

* `none` — do not touch dependents.
* `patch` / `minor` / `major` — give every reached dependent exactly that bump.
* `inherit` — give every reached dependent the same bump this unit produces (`feat` → `minor`, `!` → `major`, …).

Default is `patch`: when a unit does propagate, the dependents it reaches have had a dependency change under them but no
change to their own public API, so `patch` is the honest signal. The default applies only once propagation has been
asked for — `Propagate-Depth` is `0` unless a caret or `+N` says otherwise (§8.3).

### 8.3 `Propagate-Depth`

* `0` — no propagation. Equivalent to `Propagate: none`.
* `1` (or `direct`) — direct consumers only.
* `N` — up to N edges away.
* `all` — the full transitive closure of dependents.

**Default is `0` — a unit does not propagate unless it says so.** A commit with no propagation directive releases its
own packages and nothing else.

Rationale: the blast radius of a commit should be legible from the commit. Any non-zero default versions packages the
author never named, in numbers that grow with the workspace, and the author cannot see it from the message they wrote —
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
| *(nothing)* | —                  | `0` — no propagation                         |
| `^`         | `patch` (default)  | `1`                                          |
| `^minor`    | `minor`            | `1`                                          |
| `+3`        | `patch` (default)  | `3`                                          |
| `^minor+3`  | `minor`            | `3` — the explicit depth wins over `^`'s 1   |
| `^minor+1`  | `minor`            | `1` — explicit, identical to `^minor`        |
| `+*`        | `patch` (default)  | all levels                                   |
| `^^`        | `patch` (default)  | all levels — identical to `+*`               |
| `^^minor`   | `minor`            | all levels — identical to `^minor+*`         |
| `^^minor+*` | `minor`            | all levels — legal, redundant, `W110`        |
| `^^minor+2` | —                  | `E113`: `^^` and `+2` both assert depth      |
| `+0`        | —                  | `0` — explicit, identical to writing nothing |

`^` implies depth 1 only in the absence of an explicit depth; `+N` supplies one and wins, without error. `^^` differs:
it exists to assert "all", so disagreeing with an explicit `+N` is `E113` rather than a silent override.

**Precedence, and what it is not.** The chain below says which source *supplies* a value for a key that no
higher-priority source has set at all. It is **not** a conflict rule: two sources setting one key to *different* values
is `E112` (§5.3), and the chain is never consulted for that case.

Precedence for depth is: **footer `Propagate-Depth` → inline `+N` → inline `^`/`^^` → configured `propagation.depth` →
spec default `0`.** Channel depth has the exactly parallel chain: **footer `Propagate-Channel-Depth` → inline `++N` →
inline `%%` → configured `propagation.channelDepth` → spec default `0`.** The same chain, minus the sigil-implication
step, applies to every other directive key.

Footer sits above inline because that is the only order consistent with §5.3. In strict mode the question never arises —
a header and a footer that disagree are `E112` — and in lenient mode §5.3 resolves it by letting the **footer** win with
`W112`. There is no configuration under which an inline directive overrides a footer.

Worked: `feat(core)^: x` carrying `Propagate-Depth: 3` sets `Propagate-Depth` twice, to `1` and to `3`, so it is
`E112` — not depth `1`, and not depth `3`. Under `lenient: true` the footer wins: depth `3`, `W112` (vector 85c). By
contrast `feat(core)^minor: x` carrying `Propagate-Depth: 1` sets it twice to the *same* value, which is `W110`, and
`feat(core)^: x` carrying `Propagate: minor` sets two *different* keys, which is neither.

Within one header the caret and an explicit `+N` are not two sources but one: `^` states a depth only in the absence of
an explicit `+N`, so `^minor+2` and `+2^minor` both mean depth `2` with no diagnostic (§20.3). `^^` is the exception,
because it exists to assert "all" — `E113` in either order.

`^none` propagates nothing (the bump wins); `^minor+0` propagates nothing (the depth wins). Both are legal and mean "no
propagation".

The two are **not** the same diagnostic. `^none` and `+0` ask for nothing and get nothing: that is `W152`, redundancy,
because writing nothing says the same thing. `^minor+0` asks for something — a `minor` — and a depth of `0` throws it
away: that is `W201`, an inert value, because the author plainly wanted a bump to travel and it reaches nobody. See
§8.3b for the rule that separates them, which is the same on both axes.

### 8.3a `Propagate-Channel-Depth`

* `0` — no channel propagation. Equivalent to `Propagate-Channel: none`.
* `1` (or `direct`) — direct consumers only.
* `N` — up to N edges away.
* `all` — the full transitive closure of dependents.

**Default is `0` — a unit does not move anybody else's channel unless it says so.** Writing `%%<value>` is itself the
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
| `%%none`    | —             | no channel propagation whatever the depth          |
| `%%none++*` | —             | legal, redundant, `W152`                           |
| `%%beta++0` | `0`           | legal, inert, `W201` — a value that reaches nobody |

`%%none` propagates nothing (the value wins); `++0` propagates nothing (the depth wins). Both are legal, both mean "no
channel propagation", and both are `W152`. `%%beta++0` is `W201` instead, by §8.3b. This mirrors `^none`, `+0` and
`^minor+0` on the bump axis exactly.

### 8.3b Which diagnostic an inert directive earns

Both axes obey one rule, stated once here and referenced from §8.3 and §8.3a:

* **`W152` — redundancy.** Every part of the directive resolves to "nothing", so deleting the whole directive changes no
  behaviour: `^none`, `+0`, `^none+*`, `^^none`, `%%none`, `++0`, `%%none++*`.
* **`W201` — an inert value.** A **value** other than `none` is supplied on either axis and the depth on that axis
  resolves to `0`, so the value reaches nobody: `^minor+0`, `%%beta++0`, and the footer forms `Propagate: minor` or
  `Propagate-Channel: beta` where nothing sets the corresponding depth above `0`.

Where the rule selects `W201`, `W152` is **not** also emitted: `W201` is the more specific finding and names the actual
mistake, and two diagnostics for one token would only obscure which part of it was wrong.

`W201` is a warning rather than an error because a repository that sets `propagation.depth` or
`propagation.channelDepth` above `0` makes the same footer meaningful. The unit is not wrong in itself, only inert here.

"Supplied" means written by the unit — in the header or in one of its footers — never taken from configuration.
`inherit` written by the unit counts; the `propagation.channel` default does not. So `^inherit+0` is `W201`, because the
unit named a bump and then discarded it, while a bare `++0` is `W152`, because the unit named no channel at all and the
`inherit` it would otherwise have used came from `propagation.channel` (§14).

**Why this defaults to `0`.** The rationale of §8.3 applies with more force here, not less. A propagated bump changes a
package's version; a propagated channel changes *which line it publishes on*, which decides what installers resolve by
default and can end or begin a release train. That is a larger consequence, it is invisible in the message unless the
message says it, and it grows with the workspace. `%%beta++*` on a widely-depended package moves an entire reverse
closure onto a prerelease line in one commit (§18.1), so the reach belongs in the commit that asks for it.

The default costs less than it appears to, because **a channel is derived from a tag** (§11.1). A package already on a
prerelease line stays on it whatever this unit says, so depth `0` does not fragment an existing train — it only stops
the train recruiting packages that were not on it. Where consumers genuinely should be dragged along, `++1` or `++*`
says so in four characters, and a repository whose answer is always the same says it once with
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
radius of a change unreadable from the message and unreviewable in the plan — the same argument that keeps
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

If the intersection is empty, no channel propagates and `W205` is emitted — the channel-axis counterpart of `W135`.

### 8.6 `Release-As`

`Release-As` decides **whether and at what version a package is released in this window**. It takes exactly three
values, all of which operate at the same level — the package, for the current window:

| Value                  | Meaning                                                                                                                                      |
|------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `4.0.0` (exact semver) | **Pin.** Publish exactly this version. MUST be strictly greater than the baseline (`E153`) and not lower than the computed version (`E156`). |
| `none`                 | **Hold.** Do not release these packages in this window. Pending units are *retained*, not discarded.                                         |
| `auto`                 | **Resume.** Lift an active hold and return to normal computation.                                                                            |

`Release-As` does **not** override a bump. There is deliberately no `Release-As: minor`.

**Why there is no bump form.** How large a change is, is a property of the change — and the type already declares it. A
commit that warrants a `minor` should say `feat`. If a whole *category* of change should release in a given repository —
say `build` commits, because that repository ships compiled artefacts — that is a standing property of the repository,
expressed once in `types` (§14), not restated as a footer on every commit. Allowing a per-unit override would let a
commit's type and its release effect disagree, so the changelog would say one thing and the version another. Keeping
`Release-As` to whole-release decisions leaves exactly one place to look for "what does this commit do to versions": the
type, plus `!`.

`Release-As` with an exact version applied to a scope-set of more than one package is `E154` — two packages cannot both
become `4.0.0` unless they happen to share a baseline, and allowing it invites accidents. Use one `release` unit per
package.

**Precedence.** For each package, consider every surviving unit in the window carrying a `Release-As` whose resolved
scope includes that package. The directive from the **newest commit** wins; within a commit, the **last unit** wins.
`W153` is emitted when this discards a competing directive.

Because all three values live at one level, that single rule gives the hold/resume sequence its behaviour for free — no
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
* it is **not** a propagation source *for work it has not yet released* — its dependents are not bumped on its behalf,
  because publishing `cli` against an unpublished `core@1.5.0` would produce a broken artefact. Work the package
  published **before** the hold keeps propagating: `core@1.5.0` being public is precisely what makes `cli`'s release
  safe, and a hold applied afterwards does not un-publish it (§13.4a);
* its pending units remain pending. They accumulate. Nothing is lost.

A hold persists across release runs until it is lifted, because it is a fact in history like any other directive. There
are two ways to lift it.

**Resume with the computed version — `Release-As: auto`:**

```
release(@acme/core): embargo lifted

Release-As: auto
```

The window becomes active again and everything that accumulated releases at the `max()` of all of it, including the
units that predate the hold. This is the default choice: the embargo is over, ship what the ledger says.

**Resume at a named version — `Release-As: <version>`:**

```
release(@acme/core): ship the embargoed release

Release-As: 1.5.0
```

Same effect, but the version is stated rather than derived. Use it when a coordinated launch has already published the
number somewhere a human can read.

Both are ordinary package-level directives, so the precedence rule above applies unchanged: whichever of `none`, `auto`,
or an exact version sits in the newest commit wins. A hold followed by `auto` followed by another `none` holds again —
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
a deliberate, reviewable act — the diff shows either `auto`, a version number, or a `cancel`.

The corollary is that a forgotten hold blocks a package indefinitely (#73a). That is intentional, but it is also why
`W154` MUST be reported on every run rather than only on the run that creates the hold: a held package should be visible
in CI output for as long as it is held.

So that a named version does not have to be derived by hand, implementations MUST include the version the package
*would* have received in the `W154` diagnostic and in the release plan, so it can be read off the previous run's output.

Guard-rail: an exact `Release-As` that is **lower than the computed version** is `E156`. Naming `1.5.0` when a breaking
change has landed and `2.0.0` was computed would publish an incompatible release under a compatible number — the one
mistake the named-version form invites. `auto` cannot make this mistake, which is why it is the default choice. Lenient
mode downgrades `E156` to `W159`.

A `cancel` also ends a hold, by discarding the unit that carried it — but it discards the accumulated work along with
it. That is the difference the next section is about.

### 8.6.2 `Release-As: none` versus `cancel`

Both stop a release from happening. They differ in what survives.

|                                        | `Release-As: none`                                                                       | `cancel`                                                                                         |
|----------------------------------------|------------------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------|
| Nature                                 | Pause — "not yet"                                                                        | Erasure — "never"                                                                                |
| Pending units                          | Retained, still accumulating                                                             | Discarded permanently                                                                            |
| When lifted / afterwards               | Everything accumulated releases, at the `max()` of all of it                             | Only post-barrier commits count; pre-barrier work is unrecoverable                               |
| Changelog entries                      | Deferred, then published                                                                 | Dropped                                                                                          |
| Reversible                             | Yes — `Release-As: auto`, or a newer exact version                                       | No. Restoring the intent means rewriting the commits                                             |
| Ends by                                | An explicit newer directive                                                              | Nothing; the barrier is permanent                                                                |
| Affects the baseline of interpretation | No                                                                                       | Yes — history before it is invisible                                                             |
| Propagation from these packages        | Suppressed while held                                                                    | Suppressed permanently, because there is no bump to propagate                                    |
| Typical use                            | Embargo, coordinated launch, waiting on a downstream consumer, a broken publish pipeline | Migration artefacts, a mistyped commit on a protected branch, restating a changeset from scratch |

The one-line test: **if you would be upset to lose the changelog, you want a hold, not a cancel.**

Worked contrast, from `core@1.4.2`. Each line below summarises one commit — `«…»` marks a footer that in the real
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
the code* — nothing was reverted — it simply no longer counts as a release event, and no changelog entry will ever
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

**Channels are chosen.** `max()` is meaningless over channel names — `beta` and `rc` are alternatives, not quantities —
so a package's channel is selected rather than accumulated: a direct directive beats a propagated one, and among equals
the newest commit wins, then the last unit within it (§13.8).

The two axes are computed by the same traversal machinery and never mix. A propagated channel neither raises nor lowers
a bump, and a propagated bump never implies a channel. What one axis *does* constrain is what the other is allowed to
mean: a bump propagated from a release the target cannot resolve is not a real obligation, which is the rule of §9.3a.

### 9.2 Computation

Propagation is computed after all direct bumps are known, after cancellation (§10) has removed discarded units, and
after holds (§13.6a) are resolved.

Propagation is admitted **per target**. A unit remains a propagation source for exactly as long as some potential target
has not yet released past it — *not* merely for as long as the unit's own packages have unreleased work. This
distinction is what makes a release resumable, and it is stated normatively in §13.4a; §13.7a explains why the
alternative reading loses releases.

It runs in **three phases**, in this order:

1. the **channel axis** — every unit's `Propagate-Channel*` directives, producing a proposed channel per package;
2. **channel resolution** (§13.8) — direct directives beat propagated ones, yielding `channel(P)` for every package;
3. the **bump axis** — every unit's `Propagate*` directives, admitted only where the origin's release is resolvable by
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
  happen once, before §9.2 runs, and both passes read their results (§13.3, §13.4). The expensive part of a run — going
  to the object store — is unaffected by the phase split.
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
        if u.channelDepth == 0:                     continue      # §8.3a — default
        if u.propagateChannel == none:              continue
        sources = src[u]
        if sources is empty:                        continue
        c      = commitOf(u)                                      # loop-invariant
        pscope = resolve(u.propagateChannelScope)                 # §8.5a — resolved once,
        for (d, level) in reach(edges, sources, u.channelDepth):  #   never per target
            if d not in pscope:                           continue
            if c not in W(d):                             continue # admission — §13.4a
            if cancelledFor(c, d):                        continue # §13.5a
            v = propagatedChannelFor(u, d)                         # §9.3
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
        pscope  = resolve(u.propagateScope)                        # §8.5 — once per unit
        # §9.3a resolvability depends only on the unit's sources, so it is decided here,
        # once, and reduces to a membership test on a set of 1-3 channels per target.
        srcChan   = { channel[P] : P in sources }                  # §9.3a
        anyStable = 'stable' in srcChan
        for (d, level) in reach(edges, sources, u.depth):
            if d not in pscope:                           continue
            if c not in W(d):                             continue
            if cancelledFor(c, d):                        continue
            if not (anyStable or channel[d] in srcChan):  continue # §9.3a — W208
            prop[d] = max(prop[d], b)
            prov[d] = prov[d] | sources
    return prop, prov, chan
```

Everything hoisted above the target loop is loop-invariant, so this is the same computation written to evaluate each
invariant once. It matters because the target loop is the one that scales: a unit written `^^` over half the workspace
runs it thousands of times, and `resolve(<scope>)` and `resolvableBy` are not `O(1)`. Written literally — resolving the
scope-set and re-scanning the source set inside the loop — a single such unit costs
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
  a package reachable by several paths — a diamond — is visited once, at its shortest depth.
* **Depth is shortest-path.** A package reachable at both depth 1 and depth 3 is treated as depth 1 and is therefore
  included by `+1` or `++1`.
* **Depth is measured from the originating source set, always.** It is never recomputed from an intermediate package,
  and never re-based on a package that happens to be republishing. A unit written `+1` reaches exactly the direct
  consumers of its own packages, in this run and in every later catch-up run, whatever released in between.
* **Admission is the target's window.** A dependent is reached if and only if it has not itself released past the commit
  carrying the unit. Every guarantee in §13.7c rests on this one test, and on it being the target's window rather than
  the source's.
* **Propagation does not cascade its own propagation.** A propagated bump on `B` does not itself trigger a fresh
  propagation pass from `B`, and a propagated channel on `B` does not carry onward to `B`'s dependents; the depth
  parameters of the originating unit are the only controls. This keeps the result a pure function of the units and
  prevents surprising blast radius. In particular, a package republishing as a *catch-up* does not propagate onward
  either — otherwise a failed publish would enlarge the blast radius of a commit after the fact, which §18 forbids.
* **The bump axis requires a bump; the channel axis does not.** A unit whose type maps to `none` propagates no bump at
  any depth (#38a), but MAY still propagate a channel — this is what makes `release` usable for graduation (§7.2).
* **Ranges are ignored by default.** Whether a dependent's declared range (`^1.2.0`) already admits the new version does
  not affect propagation, because range satisfaction is not stable across lockfile regeneration. Setting
  `propagation.respectRanges: true` (§14) skips the bump axis for dependents whose range still admits the new version —
  this is a deployment choice, not a spec default, and it does not affect the channel axis.

### 9.3 The channel axis

`Propagate-Channel` decides what channel the dependents reached by `Propagate-Channel-Depth` are released on:

* `inherit` (default) — the same channel as the originating unit's own packages;
* `none` — nothing propagates, whatever the depth (§8.3a);
* `stable` — the stable channel, subject to the non-graduation rule below;
* `<channel>` — an explicit channel;
* `<from>><to>` — a **transition** (§5.3): only dependents whose baseline channel is `<from>` move, and they move to
  `<to>`.

```
propagatedChannelFor(u, d):
    spec = u.propagateChannel                    # default config.propagation.channel
    cur  = channelOf(baseline(d))                # §11.1 — from d's tag, not from this run

    if spec is a transition (from, to):
        if from == to:              warn W207; return NONE
        if not matchesFrom(cur, from):           return NONE      # W206 if nothing matches
        target = to
    else:
        target = (spec == 'inherit') ? originChannel(u) : spec
        if target == 'stable' and cur != 'stable':
            warn W200;                           return NONE      # never graduates — below

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
reviewability property `W200` exists to protect — not "graduation never propagates", but "graduation never happens by
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
2. otherwise `channelOf(baseline(origin))` — the channel the origin was last published on;
3. if a unit's source packages disagree, the value from the byte-wise least package name, with `W160`.

Every input is read from the unit and from tags at `HEAD`, so the result is deterministic, does not depend on which
earlier run published the origin, and — importantly — does not depend on any other unit. The practical effect is the
intended one: a consumer dragged along by a dependency that shipped on `beta` also ships on `beta`, and the prerelease
train stays installable as a set.

**Convergence of the channel axis.** The bump axis discharges through the window: once `d` releases, the commit leaves
`W(d)` and the contribution is gone (§13.7c G4). The channel axis discharges differently, and it matters that it does:
`W(d)` is measured from the last *stable* tag (§13.3), so a dependent that has just been moved onto `beta` still has the
commit in its window on the next run. What stops it re-releasing for ever is the final test in `propagatedChannelFor` —
`target == cur` — evaluated against the dependent's new baseline. Having arrived on `beta`, it is already there, `W199`
fires, and no channel change is proposed. Transitions converge by the same argument one step earlier: having graduated,
the dependent no longer matches `<from>`. This is stated as a guarantee in §13.7c (G7) and is the property an
implementation is most likely to get wrong by comparing against the *proposed* channel instead of the baseline.

### 9.3a Propagation follows resolvability

> A propagated bump is applied to dependent `d` only if at least one of the unit's source packages will be released in
> this run on a channel `d` can resolve — that is, on `stable`, or on `d`'s own resolved channel. Otherwise the bump is
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
unit and rarely more than three — a repository does not run many trains at once. The per-target test is a membership
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
a **stable** `cli` would ship declaring a dependency on a **prerelease** — the one outcome §9.4 exists to prevent.

The consequence is the one to hold on to: **`feat(core)^%beta` releases `core` alone.** The caret is honoured, the
dependents are reached, and every one of them is suppressed because none of them is on the beta line. To take the
consumers along, put them on the line — `feat(core)^%beta++1` — and the suppression does not apply, because they are
then released on `beta` themselves.

Four details, each a place an implementation drifts:

* **`stable` is resolvable by everyone.** A dependent on `beta` whose dependency releases a stable version is bumped
  normally; prereleases are the asymmetric case, not channels in general.
* **The test uses `channel(d)` as resolved in this run**, not `d`'s baseline channel. A dependent that the channel axis
  has just moved onto `beta` is on `beta` for this purpose, which is exactly what makes `^%beta++1` work in one commit.
* **Any one source suffices.** A unit whose scope-set spans packages on different channels propagates its bump if any of
  them releases something the target can resolve.
* **A source that has already released still counts, on the channel it released on.** In a catch-up run (§13.7a) the
  origin is typically not in this run's plan, and §13.8 assigns it the channel of its baseline — which is precisely the
  channel its published artefact sits on. The test therefore asks the right question in a catch-up run without any
  special case: *can the target resolve what the origin actually published?*

`W208` is reported per suppressed `(unit, dependent)` pair. It is a warning rather than an error because the commit is
not wrong — a prerelease that deliberately does not disturb its consumers is a normal and good thing to write — but the
reader of a plan should be able to see that a caret was written and did not reach.

### 9.4 Propagation and manifest ranges

When a package is released, the release engine MUST update, in that package's manifest, the declared range for **every
workspace dependency**, according to `rangeStrategy` (§14) — not only for those dependencies released in the same run.
This is an implementation obligation, not part of the commit syntax, but it is normative: a released package MUST NOT be
published with a range that excludes the version its workspace dependency carries at the end of the run — its planned
version where it is being released in the same run, its baseline otherwise (§19.5).

The breadth of that obligation is load-bearing, and narrowing it is a tempting mistake. Restricting reconciliation to
"released in the same run" is correct only if every propagation is discharged in the run that creates it. It is not:
a dependency may have been published by an *earlier* run whose dependent leg failed (§13.7a), or a dependent may already
have been published earlier in this same run before its dependency was — which is why §19.2 also fixes the publish
order. Reconciling against the dependency's current version closes both holes with one rule, and is a no-op whenever the
narrow rule would already have been correct. `W197` reports a range reconciled against a dependency that was released by
an earlier run.

**Reconciliation across a channel boundary.** A package released on `stable` whose workspace dependency currently
carries a **prerelease** version is the one case this rule cannot make safe, because there is no range that both admits
the prerelease and is honest about it. §9.3a prevents the common way of arriving there — a prerelease no longer drags
its stable consumers into a release — but it remains reachable deliberately, most often by graduating a consumer while
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

**When not to use `cancel`.** If the changes are real and you intend to ship them later — an embargo, a coordinated
launch, a broken publish pipeline — use `Release-As: none` (§8.6.1). A hold defers; a cancel destroys. `cancel` discards
changelog entries irrecoverably, and the only way to get them back is to rewrite the commits that produced them. Reach
for it when the metadata was never meaningful in the first place.

### 10.2 Syntax

```
cancel[(<scope-set>)]: <description>
```

* The scope-set follows the ordinary rules of §6. Omitted → file-derived, which for an otherwise-empty commit is the
  empty set; authors SHOULD therefore always write an explicit scope, most often `cancel(*)`.
* `!` on a `cancel` unit is `E170`.
* Inline directives and the footers of §8.1 on a `cancel` unit are `E171`. `cancel` takes a scope-set and nothing else.
* **Message-level trailers (§4.5) are exempt from `E171`.** `git commit -s` appends `Signed-off-by:` to the end of the
  message, which is the last unit — often the `cancel`. Rejecting that would make `cancel` unusable on any repository
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
  version will be computed without the cancelled units — which is precisely the "restate the changeset from scratch"
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
* Uppercase is `E181` — SemVer prerelease identifiers are case-sensitive and mixed case makes precedence comparisons
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
  is `E111` — "move them to some prerelease or other" is not a releasable instruction.
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
| `1.3.0-beta.1` | minor + a new `fix`     | `beta`   | `1.3.0-beta.2` — target unchanged             |
| `1.3.0-beta.2` | a breaking change lands | `beta`   | `2.0.0-beta.0` — target moved, counter resets |
| `1.3.0-beta.2` | —                       | `rc`     | `1.3.0-rc.0` — channel switch, counter resets |
| `1.3.0-rc.1`   | —                       | `stable` | `1.3.0` — graduation                          |

Because `target` is recomputed from the stable baseline on every run, a breaking change arriving mid-train correctly
moves the whole train, and the counter resets rather than continuing under a version that no longer describes the
content.

**The channel-entry patch.** A package can be released for a channel change alone — that is what the channel axis does
to a dependent with no bump of its own (§9.3). Entering a train from a clean stable baseline then computes a version
that is *lower* than the baseline: from `1.2.0` with `E = none`, `target` is `1.2.0` and `next` is `1.2.0-beta.0`, which
SemVer ranks below `1.2.0`. The engine MUST therefore apply one further step:

> If `effective(P) == none`, and `P` is being released only because its channel changed, and `next` is not greater than
> `baseline(P)`, recompute `next` with `E = patch`. Report `W204`. If `next` is *still* not greater than `baseline(P)`,
> raise `E195` as usual.

From `1.2.0` this yields `1.2.1-beta.0`, the lowest version that both sorts above the baseline and sits on the new line.
The step is deliberately narrow — one patch, only for a channel-only release, and only when the computed version would
otherwise regress — so that it can never mask the genuine regression `E195` exists to catch (vectors 46 and 47), and so
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

* The published version is `applyBump(S, E)` — the same `target` as §11.4, with no prerelease suffix.
* Graduation never lowers a version: if `target` is not greater than the baseline core, `E185` is raised (this can only
  happen if tags were hand-edited).
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
directly — *whatever is still on beta, finish it* — and is idempotent, so the same directive is correct on the first run
and on the fifth. `%*>stable` says the same across several trains at once.

**Excluding packages from graduation.** Three levels, from narrowest to broadest:

| Written                                                | Excludes                                           |
|--------------------------------------------------------|----------------------------------------------------|
| `release(@acme/*,-@acme/legacy)%beta>stable`           | from the unit's own packages (§5.2)                |
| `Propagate-Channel-Scope: @acme/*, -@acme/legacy`      | from the dependents the graduation reaches (§8.5a) |
| `channels.allowed`, `requireCodeownerFor` (§14, §14.1) | from what may be written at all, repository-wide   |

An excluded package simply stays on its prerelease line. It is not an error, it produces no release, and it will
graduate whenever a later directive names it — which is the behaviour that makes staged graduation of a large workspace
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

* `feat(core)%beta` — `core` enters the beta line. Nothing else moves and nothing else releases.
* `feat(core)^%beta` — the same. The caret reaches the direct consumers, but every one of them is suppressed by §9.3a,
  because a stable consumer cannot resolve a beta release. `W208` reports each suppression.
* `feat(core)^%beta++1` — `core` and its direct consumers all enter the beta line together, the consumers taking the
  propagated `patch`. This is the form that keeps a train installable as a set, and it is what has to be written.
* `feat(core)^: x` where `core` and its consumers are **already** on `beta` — everything stays on `beta` and takes its
  bump, with no channel directive anywhere, because a channel is derived from each package's own baseline (§11.1). An
  established train needs no directives to stay together.
* `release(core)%stable%%beta>stable++*` — the train ends, for `core` and for every transitive dependent still on
  `beta`.

The pattern to read out of that list is that directives are needed at the **boundaries** of a train — entering it and
leaving it — and nowhere in between. See §9.3 for the full rules and §24 D.4 for a worked train.

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

A tag is created for **every released package**, whatever its publish target — a package on a private registry, or one
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

### 12.5 Unreleased packages

A package with no reachable tag has no baseline. Its first computed version is `initialVersion` (default `0.1.0`),
**regardless of the pending bump** — a breaking change in a never-published package does not produce `1.0.0`.

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

The complete, normative procedure. It is a pure function of (repository history, workspace graph, configuration) and
MUST be deterministic.

### 13.1 Load the workspace

Build the package list (name, root path, publish target) and the dependency graph from manifests **at `HEAD`**. Publish
targets are resolved per §13.10a.

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
this purpose and does not trigger `E200` — which is the common and legitimate case, since test fixtures routinely depend
back on the packages they exercise. Packages deleted before `HEAD` are not in the graph even if history mentions them;
units scoping them resolve to `E130`/
`W130` per §6.1.

### 13.2 Load tags

Enumerate tags reachable from `HEAD`, parse per §12.1, and compute `baseline`, `stableBaseline`, `stableCommit` per
§12.3.

### 13.3 Pending window

```
pendingWindow(P) = { c : c reachable from HEAD } - { c : c reachable from stableCommit(P) }
```

If `P` has no stable baseline, `pendingWindow(P)` is every commit reachable from `HEAD`.

The window is measured from the last **stable** tag, not the last tag of any kind. This single definition serves both
cases:

* For a package on `stable`, last stable = last release, so the window is "changes since the last release".
* For a package on a prerelease, the window spans every commit since the last stable release, which is exactly what
  §11.4 needs to compute `target` across an entire prerelease train.

Traversal MUST visit each commit exactly once (a commit reachable by two paths contributes its units once).

The window answers exactly one question — *what has package `P` not yet released?* — and it is used for two different
purposes that must not be conflated:

| Purpose                                  | Window consulted               | Section |
|------------------------------------------|--------------------------------|---------|
| Does this unit bump `P` itself?          | `W(P)`, the unit's own package | §13.6   |
| Does this unit set `P`'s own channel?    | `W(P)`, the unit's own package | §13.8   |
| Does this unit bump dependent `D`?       | `W(D)`, the **dependent's**    | §13.7   |
| Does this unit re-channel dependent `D`? | `W(D)`, the **dependent's**    | §13.7   |

Reading the second row against the source's window silently loses releases; §13.7a is about exactly that.

### 13.4 Parse and resolve

For every commit in the union of all pending windows: parse into units (§20), resolve scopes (§6), yielding a set of
`(package, commit, unitIndex, unit)` tuples.

Retention is **purpose-dependent**. A single retention rule serving both purposes cannot be correct, for the reason
given in §13.7a. A tuple `(P, C, i, u)` is retained:

* for the **direct** computation of §13.6 — only if `C ∈ W(P)`;
* for the **propagation-source** computation of §13.7 — regardless of `W(P)`, per §13.4a.

### 13.4a Source packages

For a unit `u` in commit `C`, `sourcePackages(u)` is `u`'s resolved scope-set (§6) minus every package whose
contribution has been **suppressed**. There are two suppressors — cancellation (§10) and holds (§8.6.1) — and both are
**window-scoped** in exactly the same way:

```
discharged(P, C) =  C not in W(P)          # P has already released the work in C

sourcePackages(u) =
    { P in resolve(u) :  discharged(P, C)
                         or not ( cancelledFor(C, P) or held(P) ) }
```

`cancelledFor(C, X)` is true when `C` is an ancestor-or-self of some `cancel` commit whose resolved scope contains `X`
(§10.3). `held(P)` is true when `P`'s effective `Release-As` directive is `none` (§13.6a).

**Suppression applies only to undischarged work, and this is normative.** Once `P` has published the version that
carries `u`, the artefact its consumers are owed is public. Nothing landing afterwards can retract that obligation:

* A `cancel(P)` after `P` released the unit is a no-op for `P` (`W170`, §15.4 #47) — there is nothing pending left to
  discard — so it MUST NOT retroactively strand `P`'s consumers.
* A `Release-As: none` on `P` after `P` released the unit stops `P`'s *future* releases. It MUST NOT strand `P`'s
  consumers either. The rationale given in §8.6.1 for excluding a held package as a propagation source is that
  publishing a dependent against an *unpublished* dependency version would produce a broken artefact — and that
  reasoning simply does not apply to a version that is already published.

Treating the two suppressors differently would be indefensible: §7.3 presents `revert`, `Release-As: none`, and
`cancel` as a deliberate ladder from weakest to strongest, and it would be perverse for the *weaker* of the two to
destroy an obligation the stronger one leaves intact.

Suppressing a catch-up that is genuinely unwanted is done where the pending contribution actually lives — in the
consumer's ledger — with `cancel(<consumer>)` or a hold on the consumer. §13.7d is the operator-facing summary.

Every operand is computed from tags and ancestry at `HEAD`, so the rule is deterministic (§17.2) and independent of
which run published what.

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
| `cancel(P)` while `P` still has it pending | Discards `P`'s direct bump and removes `P` as a source | Never bumped — the source is gone         |
| `cancel(P)` after `P` released it          | No-op, `W170`                                          | Unaffected; still catches up (§13.7a)     |
| `Release-As: none` on `P`, work pending    | `P` held; removed as a source for that work            | Not bumped until the hold lifts           |
| `Release-As: none` on `P`, work released   | `P` held for future work only                          | Unaffected; still catches up (§13.4a)     |
| `cancel(D)`                                | None                                                   | Pending propagated contribution discarded |
| `Release-As: none` on `D`                  | None                                                   | Recorded, not released, until lifted      |

Consequently `cancel` retains its 1.0.0 guarantee unchanged: it affects only what has not been released, and never
reaches a published tag (§10.3).

### 13.6 Direct bumps

```
direct(P) = max over surviving tuples for P of bumpOf(unit)
```

`bumpOf(unit)` comes from the type mapping (§7.1) and `!` alone. No footer overrides it; `Release-As` acts on the
release, not on the bump (§8.6).

### 13.6a Holds

For each package, resolve the package-level `Release-As` directives by the precedence rule of §8.6. A package whose
effective directive is `none` is **held**: it is recorded in `held`, excluded from the release plan in §13.10, and
excluded as a propagation source in §13.7. Its tuples are retained — they will be counted by whichever future run
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
§13.10 excludes it. Nothing is lost — both are recomputed from the same tuples on the run that lifts the hold.

### 13.7a Catch-up

**The failure this prevents.** Publishing is not atomic. A run publishes each package to a registry independently and
tags it on success (§19.1), so a run can end with some packages published and others not. The obvious expectation is
that re-running finishes the job, because packages already tagged fall out of the plan and the rest remain in it. For a
package released by its *own* commits that is exactly what happens. For a package released by **propagation** it does
not — not unless admission is defined as §13.4a defines it.

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
grows with the fan-out. Worse, the same shape arises without any failure at all — a package held by
`Release-As: none` past its provider's release reaches exactly the same state when the hold lifts.

The root cause is that 1.0.0 tested a *dependent's* eligibility against the *source's* release position. Those are
different packages with different tags, and after a partial failure they are exactly the packages whose positions have
diverged.

**The rule.** Per §13.4a and §9.2, a unit propagates to a dependent `D` whenever `D`'s own window still contains the
unit's commit. No separate catch-up pass exists, and none is needed: catch-up is not a repair mode bolted onto the
algorithm, it is what the algorithm does when the ordinary rule is evaluated against the right window. A "catch-up
release" is therefore only a *label* — a release whose entire cause is a propagation from a package that is not itself
in this run's plan. Implementations MUST report it as such (`W193`), because a package appearing in a plan with no
commits of its own and no releasing dependency is otherwise baffling to whoever reviews the plan.

**What catch-up does not do.** It does not re-run, re-time, or re-scope anything:

* it never widens depth — targets come from the same depth-bounded traversal from the same source set (§9.2);
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
    W     = pendingWindow(D)                              # §13.3 — the *target's* window
    edges = graph.restrictedTo(config.propagation.kinds)  # §8.4 — the same edges as §9.2
    dist  = shortestPathsUp(D, edges)   # P -> edge count of the shortest path P → … → D
    out   = {}
    for u in unitsIn(W):
        if bumpOf(u) == none:                             continue
        b = (u.propagate == 'inherit') ? bumpOf(u) : u.propagate    # as §9.2
        if b == none or u.depth == 0:                     continue
        sources = sourcePackages(u)                       # §13.4a
        if sources is empty:                              continue
        if D in sources:                                  continue   # §9.2 seeds seen = sources
        reaching = { P in sources : P in dist }
        if reaching is empty:                             continue
        level = min({ dist[P] for P in reaching })        # measured from the whole source set
        if level > u.depth:                               continue
        if D not in resolve(u.propagateScope):            continue
        if cancelledFor(commitOf(u), D):                  continue
        for P in reaching:
            out |= { (P, u, level, b) }
    return out
```

Three details carry the duality, and all three are places an implementation drifts:

* **`D` is excluded from its own unit's sources.** §9.2 seeds `seen = set(sources)`, so a unit never propagates to a
  package it already bumps directly; the `D in sources` test is that seeding, read from the other end. Without it a
  package that both changes and consumes a sibling in one unit audits as behind itself.
* **`level` is measured from the whole source set, not from one ancestor.** §9.2 walks outward from `sources` together,
  so a dependent reachable at depth 1 from one source and depth 3 from another is at depth 1 (§9.2, "depth is
  shortest-path", "depth is measured from the originating source set, always"). Taking `dist[P]` per ancestor instead of
  the minimum over `reaching` under-reports exactly the diamond cases.
* **`b` is the effective propagated bump**, computed the same way as in §9.2 — `inherit` resolves to the unit's own
  bump, and a unit whose propagation resolves to `none`, or whose depth is `0`, is not a source at all (§8.3). Such a
  unit warns — `W152` where it asked for nothing, `W201` where it named a value the depth then discarded (§8.3b) — but
  the diagnostic does not change the computation here: either way it contributes no tuples.

`shortestPathsUp(D, edges)` is a BFS up the dependency edges; it excludes `D` itself and terminates because the graph is
acyclic (§13.1). It needs no depth bound — the `level > u.depth` test is the bound — though an implementation MAY stop
the BFS at the largest `u.depth` in `W`, treating `all` as unbounded, since no unit can reach further.

`staleSources(D)` is non-empty exactly when §9.2 assigns `D` a non-`none` propagated bump, and `max({ b })` over its
rows equals that bump. The two formulations are duals over the same relation and MUST agree; disagreement is an
implementation bug, and testing one against the other over a random workspace is a cheap and effective conformance
check.

**The cheap tag-level screen.** Walking units is `O(|window|)`. A far cheaper *screening* test, adequate for a warning
banner or a CI dashboard, compares release positions directly:

> `D` is possibly behind `P` if `tagCommit(baseline(P))` is **not** an ancestor-or-self of `tagCommit(baseline(D))`.

That is the "the provider's tag is newer than the consumer's tag" intuition, expressed as ancestry so that it stays
deterministic under merges, rebases, and equal commit dates (§10.4). It MUST NOT be used as the authoritative test: it
is *necessary but not sufficient*. A provider can legitimately be ahead of a consumer with nothing owed — the units
between them may all be `^none`, or `+0`, or scoped away by `Propagate-Scope`, or reach `D` only beyond their declared
depth, or be `devDependencies`-only edges. Using the screen to decide releases would manufacture bumps that no commit
asked for. Use it to *find* candidates; use §9.2 to decide.

Note also the case the screen cannot see at all: `P` and `D` released at the **same commit**, in the same run, but with
`D` published first. Ancestry cannot distinguish them, and no rule over tags can. That case is prevented rather than
detected, by publishing in dependency order (§19.2).

### 13.7c Guarantees

For a fixed `HEAD` and configuration, an implementation conforming to §13.4a, §9.2, and §19 satisfies the following.
These are normative and testable; Appendix B.7 exercises G1–G6 and Appendix B.9 exercises G7 and G8.

**G1 — Termination.** Propagation halts. Each unit traverses a BFS that marks every package `seen` at most once, so it
performs at most `|V|` expansions regardless of depth, cycles, or `+*`; the outer loop is over a finite set of units.

**G2 — Completeness (no orphans).** If unit `u` in commit `C` admits dependent `D`, then `D` receives at least `u`'s
propagated bump in **every** run until `D` releases at a commit containing `C`. Proof: admission is
`C ∈ W(D) = reach(HEAD) − reach(stableCommit(D))`, and the only operation that removes `C` from that set is advancing
`stableCommit(D)` to a commit whose ancestry includes `C` — that is, `D` releasing. Nothing else can drop the
contribution, and in particular the source's own release cannot.

**G3 — Version stability.** A package caught up in run *k* receives the same version it was planned at in run 1,
provided `HEAD` has not moved. Proof: its version is `applyBump(stableBaseline(D), effective(D))`; the failed run wrote
no tag for `D`, so `stableBaseline(D)` is unchanged, and `effective(D)` is a `max()` over the same surviving tuples. A
re-run after a partial failure therefore publishes the *same numbers* the operator already reviewed — an operational
property as much as a formal one.

**G4 — No double release.** Once `D` is tagged at commit `T` with `C ∈ reach(T)`, `C ∉ W(D)`, so the contribution is not
re-admitted. Combined with G2 this makes the propagated bump **exactly-once**: it survives until it is discharged, and
does not survive discharge.

**G5 — No blast-radius widening.** For every unit, the set of targets admitted in any later run is a **subset** of the
set admitted in the first. Proof: the traversal is identical (same sources, same depth, same graph at `HEAD`), and
admission only ever removes targets as their windows advance. A failed publish can therefore never enlarge what a commit
releases — the property §18.1 depends on, since the alternative would let an attacker widen a blast radius by inducing a
publish failure.

**G6 — Convergence.** Repeated running at a fixed `HEAD` reaches an empty plan in at most `n` runs, where `n` is the
number of publishable packages in the first plan, provided each run publishes at least one package. Proof: by G4 a
published package leaves the plan; by G5 no package enters it; so the plan strictly shrinks. If a run publishes nothing,
the failure is not partial and is a hard error to be surfaced, not retried.

G6 is **unconditional** for packages that are not held. Every released package is tagged, whatever its publish target
(§13.10a), so every released package's window advances and it leaves the plan. The only packages that persist across
runs are those held by `Release-As: none`, which are excluded from publication by §13.6a and are expected to persist
until the hold lifts.

**Why tagging is universal.** It is tempting to version a private or artefact-less package in the plan but not tag it —
there is, after all, nothing in a registry for the tag to correspond to. Such a package can never converge: its window
never advances, so it reappears in every plan for ever, and `E199` (§19.6) would have to be weakened to exclude it —
which in turn blunts the one check that detects the failure of §13.7a, and blunts it precisely for the internal-only
packages where nobody is watching a registry. A permanent exception to convergence is not a caveat worth documenting; it
is a defect. Hence §13.10a: tag whatever is released.

**G7 — Channel discharge.** A propagated channel is proposed for `d` at most until `d` is released on it. Proof: the
final test of `propagatedChannelFor` (§9.3) compares the proposed channel to `channelOf(baseline(d))`, and `d`'s
baseline advances to the released version on success; a non-transition value then equals the baseline channel and
returns `NONE` (`W199`), and a transition no longer matches its `<from>`. This is what makes the channel axis converge
even though `W(d)` — measured from the last *stable* tag (§13.3) — still contains the commit. An implementation that
compares against the channel it computed earlier in the same run, rather than against the baseline, loses this property
and re-releases the package on every run for ever.

G7 is why the channel axis needs no window bookkeeping of its own. The bump axis discharges through `W(d)` and the
channel axis discharges through `d`'s baseline channel; both are read from tags at `HEAD`, so both are deterministic and
both survive a partial failure unchanged.

**G8 — Axis independence.** Suppressing a bump under §9.3a never suppresses a channel, and vice versa. Proof: the two
passes share only `channel(P)`, which phase 3 reads and never writes. Two operational consequences: a `W208` in a plan
means a caret did not reach, not that a channel directive failed; and a package may legitimately appear in a plan with a
channel change and no bump (`W202`), or with a bump and no channel change, and neither shape indicates a defect.

### 13.7d Suppressing a catch-up

A catch-up is a pending release like any other, so it is stopped by the ordinary mechanisms of §8.6.1 and §10 — applied
**to the consumer**. This matters most in the situation that produces catch-ups in the first place: a run failed, the
operator has looked at what is now owed, and has decided not to ship it after all. That decision is expressed as a new
commit on top, and it behaves exactly as it would for any other pending work.

| Intent                                  | Write                                       | Result                                                                   |
|-----------------------------------------|---------------------------------------------|--------------------------------------------------------------------------|
| Drop the owed release permanently       | `cancel(<consumer>)`                        | The pending propagated contribution is discarded (§13.5a). Irreversible. |
| Defer it — ship later, keep the ledger  | `release(<consumer>)` + `Release-As: none`  | Held; `W154` reports the withheld version every run until lifted.        |
| Ship it now at a stated version         | `release(<consumer>)` + `Release-As: <ver>` | Pinned, subject to the usual guards `E153`/`E156` (§8.6).                |
| Resume after a hold                     | `release(<consumer>)` + `Release-As: auto`  | Releases at the `max()` of everything accumulated, catch-up included.    |
| Drop everything pending, workspace-wide | `cancel(*)`                                 | Discards every pending contribution, direct and propagated alike.        |

What does **not** work is acting on the provider. Once the provider has published, neither `cancel(<provider>)` nor a
hold on it retracts what its consumers are owed (§13.4a) — the version is public, and cancellation never reaches a
published release (§10.3). A `cancel(<provider>)` in that position reports `W170`, "nothing to discard", which is the
signal that it addressed the wrong package.

Two consequences worth stating plainly:

* **The barrier still only reaches backwards.** `cancel(<consumer>)` discards the contributions from commits that are
  ancestors-or-self of the cancel. Work landing afterwards accumulates normally, and the consumer releases again on the
  next run (§10.3, §10.4).
* **A suppressed catch-up leaves the consumer's manifest unreconciled.** It goes on declaring the range it was published
  with until its next release, whenever that comes. This is usually harmless — a caret range admits the dependency's new
  version — but it is a real consequence of choosing not to release, and §9.4 reconciles it only at publish time.

### 13.8 Channels

Determine `channel(P)` for **every** package in the workspace, whether or not it will be released:

```
resolveChannels(chan, units):                   # chan from §9.2 phase 1
    # ---- pass 1: push from units to the packages they resolve to ----
    # Units are visited in §11.6 precedence order — newest commit first, and within a
    # commit the last unit first — so each cands[P] is built already ordered and is
    # never sorted per package.
    cands = {}                                  # package -> list of units, in §11.6 order
    for u in units in §11.6 order where u sets Channel:
        c = commitOf(u)
        for P in resolve(u.scopeSet):           # §13.4 has already resolved this scope-set
            if c not in W(P):   continue        # §13.4a
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
    for u in cands:
        v = u.channel
        if v is a transition (from, to):
            if from == to:                  warn W207; continue
            if not matchesFrom(base, from):            continue     # not a competitor
            if len(cands with a proposal) > 1: warn W186
            return to
        if len(cands with a proposal) > 1:   warn W186
        return v
    return NONE
```

The precedence body is unchanged; only the way `cands` is obtained has been inverted. Written the other way — `cands` as
a comprehension over `W(P)`, evaluated inside a loop over every package — it rescans the union window once per package,
which is `O(P · U)`, and it does that work for the great majority of packages that no `Channel` directive names at all.
Pushing from units costs one pass over the units plus the scope-set resolutions §13.4 has already paid for, and one flat
pass over the packages: `O(U + P)` on top of work the run does anyway. The two agree row for row, because a package
appears in `cands[P]` under the inverted form exactly when `u sets Channel and P in resolve(u) and commitOf(u) in W(P)`,
which is the comprehension's condition read in the other direction.

Two obligations come with the inversion. The push MUST visit units in §11.6 order, or `cands[P]` arrives unordered and
the first-match-wins loop below it silently picks a different directive — a determinism bug that shows up only when one
package is named by two commits. And `len(cands with a proposal)` for `W186` counts over the whole candidate list, not
over the prefix examined before the winner was found, so the list MUST be complete before `directChannelFor` runs; an
implementation that fuses the two passes and evaluates a package as soon as its first candidate arrives loses `W186`.

Three precedence rules, in force order: a **direct** directive beats every propagated one regardless of age; among
direct directives, and separately among propagated ones, the newest commit beats the older, and the last unit within a
commit beats the earlier; a directive that proposes nothing — a transition that does not match, or a value equal to the
package's current channel — is not a competitor at all (§11.6, §9.3).

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
release like any other — versioned, tagged, manifest written, artefact published — and it MUST be reported as `W202`,
for the same reason `W193` exists: a package in a plan with no commits of its own and no bump is otherwise unexplainable
to whoever reviews it. Its version comes from §11.4, including the channel-entry patch and `W204` where that applies.

`next` MUST be strictly greater than `baseline(P)` by SemVer precedence; otherwise `E195`.

### 13.10 Emit

Packages with `effective(P) == none` and no channel change and no `Release-As` are **not** released. **Held** packages
are not released regardless of their bump (`W154`). Every other package with a bump is released.

Every package that **is** released is versioned, tagged, and has its manifest written — including packages that are
private, internal, or published to a restricted registry. Where its artefact goes is a separate question, answered by
§13.10a and acted on in §19.

### 13.10a Publish targets

Where a package's artefact goes is independent of whether it is released. Conflating the two costs convergence, for the
reason given in §13.7c.

**`publishTarget(P)`** — where the artefact goes:

| Target   | Meaning                                                                    | Versioned | Tagged | Artefact uploaded     |
|----------|----------------------------------------------------------------------------|-----------|--------|-----------------------|
| `public` | The default public registry.                                               | yes       | yes    | yes                   |
| `<name>` | A named registry from `registries` (§14) — private, internal, or per-team. | yes       | yes    | yes, to that registry |
| `none`   | The package produces no installable artefact.                              | yes       | yes    | no                    |

Resolved in precedence order:

1. an explicit `publishTargets` glob match (§14);
2. the manifest's own registry declaration — `publishConfig.registry` in npm, and its equivalents elsewhere;
3. `none`, if the manifest marks the package private and no target is configured for it;
4. otherwise `public`.

A manifest's `private` flag is a statement about **where** a package may be published, not about whether it is released.
A private package pointed at an internal registry is released, tagged, published there, and propagated from, exactly
like a public one. A package with target `none` is still versioned and tagged — its version is real, it is what §9.4
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
  of the graph either strands everything behind it — nothing reaches those packages at any depth, a permanent version of
  the orphan in §13.7a — or, if its dependents are re-pointed past it, shortens every path that crossed it, so a
  reviewed `+2` releases packages its author did not include.
* **It moves file ownership.** Ownership is by longest matching path prefix (§6.2), so a nested directory that stops
  being a package has its files absorbed by the enclosing one. An `examples/` directory under `packages/ui` that is
  removed from the workspace does not become inert — every change inside it starts releasing `ui`. Use `ignoredPaths`
  for that, which suppresses file-derived resolution without touching the graph.

The output is a release plan: package, baseline, next version, channel, contributing units, and for each package the
reason (`direct`, `propagated from X`, `channel from X` where the package has no bump of its own, or `catch-up from X@V`
where `X` is not itself in this plan). Where the channel differs from the baseline's, the plan MUST show both — the
transition a reader needs to see is `beta → stable`, not the word `stable` alone. Packages whose only reason is catch-up
MUST be marked as such (`W193`) and MUST carry the origin's **published** version, so that a reviewer can see at a
glance that the plan is discharging an earlier run's unfinished work rather than releasing something new.

The plan MUST additionally be emitted in the publish order of §19.2, so that the order in which packages will actually
be published is visible before the run starts rather than inferred afterwards.

### 13.11 Complexity and scale

This section is **normative for cost bounds and informative for technique**. A conforming implementation MUST produce
the plan §13 defines; it is free to compute it any way that yields an identical result (§17.2). The bounds below exist
because the algorithm is specified in the clearest form rather than the fastest one, and a literal transcription does
not survive a large workspace.

Notation: `P` packages, `E` graph edges, `C` commits in the union of all pending windows, `U` units in those commits,
`T` reachable tags, `k` **distinct** commits carrying a stable baseline, `G` distinct directive combinations in use. In
the per-target row, and there only, `D` is the number of targets one unit reaches, `S` its source-set size, and `Σ`
its resolved scope-set size. (`T` is already tags; the per-unit quantities are deliberately given separate letters
because they are bounded by the *unit*, not by the run.)

| Phase                      | Literal transcription | Achievable            | Note                                        |
|----------------------------|-----------------------|-----------------------|---------------------------------------------|
| Load workspace (§13.1)     | `O(P + E)`            | `O(P + E)`            |                                             |
| Load tags (§13.2)          | `O(T log T)`          | `O(T log T)`          |                                             |
| Pending windows (§13.3)    | **`O(P · C)`**        | `O(k · C)`            | `k ≪ P` — see below                         |
| Parse and resolve (§13.4)  | `O(U)` + file lookup  | `O(U)` amortised      | Parse results are cacheable by commit SHA   |
| Cancellation (§13.5)       | `O(cancels · C)`      | `O(cancels · C)`      | `cancels` is tiny; precompute ancestor sets |
| Direct bumps (§13.6)       | `O(U)`                | `O(U)`                |                                             |
| Holds (§13.6a)             | `O(U)`                | `O(U)`                |                                             |
| Propagation (§13.7)        | **`O(2U · (P + E))`** | `O(2k · G · (P + E))` | The dominant cost; two passes, see below    |
| — per-target predicates    | **`O(D · (S + Σ))`**  | `O(D + S + Σ)`        | Per unit. Hoist `resolvableBy`, `resolve()` |
| Channel resolution (§13.8) | **`O(P · U)`**        | `O(U + P)`            | Invert: push from units, do not scan `W(P)` |
| Versions (§13.9)           | `O(P)`                | `O(P)`                |                                             |
| Publish order (§19.2)      | `O(P + E)`            | `O(P + E)`            | Topological sort over the full graph        |
| Blocking closure (§19.3)   | `O(P + E)`            | `O(P + E)`            | One reverse traversal per failure           |

The bold rows are the ones that matter. Each is a product of two large quantities, and on a workspace with thousands of
packages and a long history a literal implementation becomes the bottleneck for the whole run. The first two are
structural and want the bucketing described below; the last two are ordinary loop-invariant and loop-inversion mistakes,
and cost nothing to avoid if the code is written the right way round the first time.

**Windows: group by distinct baseline commit.** `W(P)` is defined per package, but it is a function only of
`stableCommit(P)`, and packages released together share one. The number of *distinct* baseline commits is therefore the
number of release runs still in flight — in practice a handful, not `P`. Computing `reach(s)` once per distinct
`s` and testing membership by lookup replaces `P` traversals with `k`. Where the history has a commit-graph with
generation numbers, ancestry is an `O(1)` comparison and no explicit commit sets are needed at all. Note also that
storing `W(P)` as an explicit set per package costs `O(P · C)` **memory**, which on a large repository is the binding
constraint before time is. Representing each distinct window as a bitset over commit indices — one bit per commit in
`reach(HEAD)`, `k` bitsets in total — bounds it at `O(k · C)` bits and makes the admission test of §13.4a a single bit
lookup.

**Propagation: bucket by (window class, directive).** Admission depends on the target's window, and there are only `k`
distinct windows; traversal depends only on the directive tuple — `(bump, depth, Propagate-Scope)` for the bump pass and
`(Propagate-Channel, channelDepth, Propagate-Channel-Scope)` for the channel pass — and real repositories use a handful
of combinations because most commits carry no directives at all. Units sharing both can be traversed together from the
union of their source packages: BFS depth is shortest-path *from the source set*, so a merged traversal admits exactly
what the individual ones admit between them. This replaces `U` traversals with `k · G` **per pass**.

The two passes bucket separately and usually very differently: the channel pass sees only the units that carry a channel
directive at all, which in a repository not running a train is none of them, and it can be skipped outright when no unit
in the union window sets `Propagate-Channel-Depth` above `0` and `propagation.channelDepth` is `0`. Skipping it is
observationally equivalent, because §13.8 then assigns every package its baseline channel.

The `2` in that row is the phase split, and it does not factor out: phase 3 reads what phase 2 produced, so the passes
are genuinely sequential and MUST NOT be fused (§9.2). What the factor buys is §9.3a, and what it costs is a second walk
of the unit list and the graph — **not** a second walk of history, which is shared and paid once before §9.2 begins. On
the repositories where the channel pass is skipped entirely, the factor is `1`.

> **A correctness trap in that optimisation.** Merging source sets is sound for reachability but **not** for the
> per-unit self-exclusion of §9.2. A single unit never propagates to its own source packages — `seen = set(sources)` —
> but a package that is a source of one unit may legitimately be a *target* of another unit in the same bucket, and
> subtracting the merged source set silently withholds those bumps. The symptom is a package that should have taken a
> propagated `major` releasing as its own `patch`, with no diagnostic. Admit any node reached across **at least one
> edge** within the depth bound, rather than subtracting the merged sources. Implementations SHOULD test a bucketed
> propagation against a literal per-unit one over randomised workspaces; the two MUST agree exactly.

**Per-target predicates: hoist everything that does not read the target.** Inside the target loop of §9.2, three things
are loop-invariant and one is not. `commitOf(u)`, `resolve(u.propagateScope)`, and the source-channel set of §9.3a
depend only on the unit; only `W(d)`, `cancelledFor(·, d)` and the final `channel[d]` membership read the target. A
transcription that resolves the scope-set and re-scans the source set once per target turns an `O(D + S + Σ)` unit into
an `O(D · (S + Σ))` one, and the unit where that bites is precisely the one an author reaches for when they mean it: a
`^^` across a wide scope-set has a large `D`, and re-deriving a two-element channel set thousands of times to answer a
question whose inputs never changed is pure waste. Hoisting is not an optimisation to be justified by profiling; it is
what the predicate's own dependency structure already says (§9.3a).

The saving compounds with bucketing rather than competing with it. Bucketed traversal reduces the *number* of target
loops; hoisting reduces the cost of each iteration of the ones that remain. Neither subsumes the other, and the wide-
`^^`
unit is the case where both apply at once.

**Channel resolution: push from units, do not pull from packages.** `directChannelFor(P)` reads naturally as "find the
directives in `P`'s window that name `P`", and written that way it scans the union window once per package: `O(P · U)`,
almost all of it spent confirming that no directive names the package at all. Inverting it — one pass over the units in
§11.6 order, appending each to the packages its scope-set resolves to — costs `O(U + P)` on top of the scope resolution
§13.4 has already performed, and touches only the packages some directive actually names. This is the same shape as
§13.4, which resolves units to packages for exactly the same reason, and an implementation that has already built that
mapping can often reuse it directly rather than rebuilding it here. The ordering and `W186` obligations that come with
the inversion are stated in §13.8 and are not optional.

**Parsing: cache by commit SHA.** The union window spans all history whenever any package is unreleased (§13.3), so a
workspace that has just gained a new package re-parses every commit on that run. Commits are immutable and parsing is a
pure function of the message and the parsing configuration, so results are safely cacheable keyed on
`(commit SHA, digest of separator + types + limits)`. Steady-state runs then cost `O(new commits)`. The cache MUST be
keyed on the configuration digest as well as the SHA, or a configuration change will be read through a stale cache.

**What none of this may change.** These are all internal representations. The plan, the diagnostics, and their order
MUST be identical to the literal reading (§17.2), and an implementation that trades a different plan for speed does not
conform however fast it is. Determinism in particular forbids introducing parallelism whose scheduling affects iteration
order of any collection that reaches the output.

**Appendix B.7a tests exactly this.** Each of its vectors is a case where one of the transformations above is *almost*
equivalence-preserving — a hoist that collapses a multi-channel source set, an inversion that loses §11.6 order, a fused
pass that miscounts `W186`, a skipped channel pass that leaves `channel(P)` unset. An implementation that applies none
of these optimisations passes them without effort; one that applies them and has not thought about the edges fails them,
which is the point. The general obligation stands on its own — B.7a is not exhaustive, and an optimisation it does not
name is still the implementer's to justify.

---

## 14. Configuration

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
| `channels.allowed`          | `null`                                                       | If set, restricts every channel value — both sides of a transition — to a list (§11.2).   |
| `publish.orderKinds`        | `["dependencies","peerDependencies","optionalDependencies"]` | Edges defining the publish order (§19.2).                                                 |
| `publish.blockingKinds`     | `["dependencies","peerDependencies"]`                        | Edges over which a failed publish blocks dependents (§19.3).                              |
| `publish.onFailure`         | `"skip-dependents"`                                          | `"skip-dependents"` or `"abort"` — what a failed publish does to the rest.                |
| `publish.adoptPublished`    | `true`                                                       | Tag a version the registry already holds, when identity is verified (§19.4).              |
| `publish.verifyConvergence` | `true`                                                       | Re-plan after a fully successful run and assert it is empty (§19.6).                      |
| `registries`                | `{}`                                                         | Registry name → URL/credentials handle, referenced by `publishTargets` (§13.10a).         |
| `publishTargets`            | `{}`                                                         | Package glob → registry name or `none`. Highest-precedence target source.                 |

### 14.1 Safety limits

Referenced by §18. These divide into two groups, and the division is normative.

**Enforced by default.** These have concrete defaults and fire without any configuration. A repository that has never
written a config file is still subject to them; they may be raised or lowered, and `maxMajorJump` may be disabled by
setting it to `null`, but the `limits.*` bounds MUST NOT be disabled — they are the parser bounds of §18.3, and an
implementation that lets a message opt out of them is not conforming.

| Key                        | Default   | Disableable  | Meaning                                                                                                        |
|----------------------------|-----------|--------------|----------------------------------------------------------------------------------------------------------------|
| `maxMajorJump`             | `1`       | yes (`null`) | Reject an exact `Release-As` raising the major version more than this far above the computed version (`E157`). |
| `limits.unitsPerMessage`   | `64`      | **no**       | Cap on units in one commit message (`E158`).                                                                   |
| `limits.scopeTermsPerUnit` | `256`     | **no**       | Cap on scope terms in one scope-set (`E158`).                                                                  |
| `limits.messageBytes`      | `1048576` | **no**       | Cap on message length (`E158`).                                                                                |

**Null until configured.** These are inert as shipped. Nothing about a default run consults them, and no diagnostic can
originate from them until an operator sets a value.

| Key                     | Default | Meaning                                                                                      |
|-------------------------|---------|----------------------------------------------------------------------------------------------|
| `maxPackagesPerRun`     | `null`  | Refuse or gate a run releasing more than this many packages (§18.2).                         |
| `maxMajorsPerRun`       | `null`  | Refuse or gate a run applying more than this many `major` bumps.                             |
| `maxChannelMovesPerRun` | `null`  | Refuse or gate a run moving more than this many packages between channels (§18.2).           |
| `requireCodeownerFor`   | `[]`    | Directives (`cancel`, `Release-As`, `^^`, `++`, `>` transitions) needing CODEOWNER approval. |

`maxMajorJump` sits in the first group deliberately, and it is the row most often misread: a fresh repository that
writes `Release-As: 5.0.0` against a computed `1.5.0` gets `E157` with no configuration involved. It is a default, not
an opt-in.

### 14.2 What configuration may not change

Configuration MUST NOT be able to change:

* the meaning of `cancel`, or the ancestor-or-self rule (§10.3);
* the `max()` combination rule (§9.1);
* the requirement that tags are the sole state store (§12.4);
* the non-suppressibility of `W155`, `W156`, `W172`, `W193`, `W194`, `W202`, and `W208` (§17.1);
* determinism, idempotency, or convergence (§17.2);
* the admission rule of §13.4a — that a dependent's eligibility is tested against the **dependent's** window;
* that a propagated channel never graduates a dependent unless written as a transition naming the train (§9.3);
* that a transition matches against the dependent's **baseline** channel, not against a value computed in the same run;
* that both propagation axes default to depth `0`, and that neither bounds the other (§8.3, §8.3a);
* the requirement that dependencies are published before their dependents (§19.2);
* the requirement that a tag is written only after that package's publish succeeds (§19.1);
* the requirement that **every** released package is tagged, whatever its publish target (§13.10a);
* the fact that every workspace package is a release unit (§13.10a).

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
| 10  | Colon inside scope `feat(a:b): x`                   | `E103` — `:` is not a legal scope-term character, so the scanner reaches it before the closing parenthesis. |
| 11  | No space after colon (`feat:x`)                     | `E120`. Lenient mode accepts with `W121`.                                                                   |
| 12  | Two spaces after colon                              | `E120`.                                                                                                     |
| 13  | Header longer than the terminal likes               | No limit enforced beyond `W120` on the description.                                                         |
| 14  | Emoji / non-ASCII in description                    | Legal. Length counted in Unicode scalar values.                                                             |
| 15  | Non-ASCII in a package name                         | Legal if the manifest says so; compared byte-for-byte.                                                      |
| 16  | Unit with header only, no body                      | Legal.                                                                                                      |
| 17  | Body that looks like footers but is mid-message     | Only the **last** paragraph is considered for footers.                                                      |
| 18  | Footer block where one line is not footer-shaped    | The whole paragraph is body; `W151` since this is usually a typo.                                           |
| 19  | `Breaking change: x`                                | **Not** breaking, and not even a footer — the generic key loop halts at the space. `W155`.                  |
| 19a | `breaking-change: x`                                | A valid footer with an unrecognised key. **Not** breaking. `W155`.                                          |
| 19b | `BREAKING-CHANGE: x`                                | Accepted, breaking, per base spec.                                                                          |
| 19c | `BREAKING CHANGE` as the header line                | `E100` with a dedicated message (§5.1).                                                                     |
| 19d | `BREAKING CHANGE:` mid-body, footer block elsewhere | Body text, no effect, `W156`.                                                                               |
| 19e | `BREAKING CHANGE: ` with an empty value             | Breaking, `W157`.                                                                                           |
| 19f | `!` and a `BREAKING CHANGE` footer on one unit      | Legal, not redundant — the footer carries prose the marker cannot. One major bump.                          |
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
| 38g | `^minor+0`                                                                  | No propagation; `W201` alone — a bump value was supplied and the depth discards it (§8.3b). Not `W152`.                                                                                |
| 38a | `^^` on a unit whose type maps to `none`                                    | No **bump** propagation — §9.2 phase 3 skips units with no bump, and depth is irrelevant. The channel axis is unaffected and still runs (§7.2).                                        |
| 38b | A unit with no propagation directive at all                                 | Releases only its own packages. Both depths are `0` by default (§8.3, §8.3a); no dependent is touched.                                                                                 |
| 38d | `++0`, `%%none`, or `%%none++*`                                             | No channel propagation; `W152` for redundancy.                                                                                                                                         |
| 38h | `%%beta++0`                                                                 | No channel propagation; `W201` alone — a channel value was supplied and the depth discards it (§8.3b). Not `W152`. Exactly mirrors #38g.                                               |
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
| 46e | Origin releases on `stable`; a dependent on `beta` reached by `^`           | Bumped normally — a stable release is resolvable by everyone (§9.3a).                                                                                                                  |
| 46f | A unit whose sources sit on two different channels                          | The bump propagates if **any** source releases something the target can resolve (§9.3a).                                                                                               |

### 15.4 Cancel

| #  | Case                                                     | Resolution                                                                                                          |
|----|----------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------|
| 47 | `cancel` with nothing pending                            | No-op, `W170`.                                                                                                      |
| 48 | `cancel` on a branch not merged into `HEAD`              | No effect.                                                                                                          |
| 49 | `cancel` and a `feat` in the *same* commit, cancel first | Ancestor-or-**self** — the `feat` is discarded, regardless of unit order. Same-commit units are all at the barrier. |
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
| 60a | `^%%beta` on a unit whose own packages are stable                      | Origin releases stable; direct consumers enter the `beta` line and take the propagated patch. Legal — the stable baseline is untouched (§9.3).            |
| 60b | `%%stable` reaching a dependent whose baseline is a prerelease         | **Not** graduated. The dependent keeps its channel; `W200`. Only a direct directive or a transition graduates (§11.5).                                    |
| 60c | `%%stable` reaching a dependent already on stable                      | No-op; `W199` for the redundant directive.                                                                                                                |
| 60d | `%%` with no value, e.g. `feat(core)%%: x`                             | `E111`. Unlike `^^`, a bare `%%` has no meaning (§5.3).                                                                                                   |
| 60e | `%%` twice, or `%%%`                                                   | `E110`.                                                                                                                                                   |
| 60f | `%beta%%rc`                                                            | Legal: origin on `beta`, dependents on `rc`. `%` and `%%` are distinct sigils (§5.3).                                                                     |
| 60g | `%%Beta` / `%%latest`                                                  | `E181` / `E180` — propagated channel names obey §11.2 exactly as direct ones do.                                                                          |
| 60h | `%%beta` with no `++N` on the unit                                     | Channel depth `1` — the sigil supplies it (§8.3a). The direct consumers move; no bump moves unless a caret says so.                                       |
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
| 60u | `%%beta>inherit`, `%%none>beta`, `%%beta>*`                            | `E111` — `inherit` and `none` are values, not channels, and `*` is legal only as a `<from>` (§11.2).                                                      |
| 60v | `%%a>b>c`                                                              | `E111` — a transition has exactly one `>`.                                                                                                                |
| 60w | Graduating a consumer whose provider stays on `beta`                   | Permitted and reported: the stable consumer's range admits a prerelease, `W203` (§9.4). Graduate the provider too.                                        |
| 61  | Graduation with no pending bumps                                       | Publishes the accumulated `target`; if that equals the baseline core, `E185`.                                                                             |
| 62  | Graduating a package already stable                                    | `W185` no-op, or an ordinary release if bumps are pending.                                                                                                |
| 63  | Prerelease with no stable baseline ever                                | Virtual stable baseline `0.0.0` → `target` is `initialVersion`; e.g. `0.1.0-beta.0`.                                                                      |
| 63a | Channel-only entry from a clean stable baseline `1.2.0`                | `1.2.1-beta.0` — the channel-entry patch, `W204` (§11.4). Without it the computed `1.2.0-beta.0` would rank **below** the baseline.                       |
| 63b | Channel-only entry where the window already carries a bump             | Ordinary §11.4; no channel-entry patch and no `W204`, because the computed version already exceeds the baseline.                                          |
| 63c | Channel-only entry that is still not greater after the patch           | `E195`. The patch is one step and never loops, so a hand-edited baseline ahead of the stable line still fails loudly (vectors 46–47).                     |
| 64  | Hand-written tag `pkg@1.3.0-beta10`                                    | Parsed as a prerelease with a single alphanumeric identifier; a computed `-beta.0` would compare **lower**. `E182` — refuse rather than regress.          |
| 65  | Tag with build metadata `pkg@1.2.3+abc`                                | Accepted; metadata ignored and not carried forward.                                                                                                       |
| 66  | Two tags with the same version on different commits                    | `E191`.                                                                                                                                                   |
| 67  | Tag `pkg@1.2.3` where `pkg` is unknown                                 | Ignored silently.                                                                                                                                         |
| 68  | Tag `pkg@not-semver`                                                   | Ignored, `W190`.                                                                                                                                          |
| 69  | `@acme/ui@1.2.3` split at first `@`                                    | Conformance failure; MUST split at last `@`.                                                                                                              |
| 70  | Manifest version disagrees with baseline                               | `W192`; tags win.                                                                                                                                         |
| 71  | `Release-As` lower than baseline                                       | `E153`.                                                                                                                                                   |
| 72  | `Release-As` exact on a multi-package scope                            | `E154`.                                                                                                                                                   |
| 73  | Two conflicting package-level `Release-As` for one package             | Newest commit, then last unit; `W153`.                                                                                                                    |
| 73a | `Release-As: none`, never lifted                                       | The package is held indefinitely. `W154` on every run, carrying the withheld version. This is intended — a hold is a fact in history, not a per-run flag. |
| 73b | Exact `Release-As` lower than the computed version                     | `E156` (lenient: `W159`). Guards against lifting a hold at a stale number after a breaking change landed.                                                 |
| 73c | `Release-As: none` and `auto` (or an exact version) in the same commit | Last unit wins, `W153`.                                                                                                                                   |
| 73d | `none` → `auto` → `none` across three commits                          | Held. The newest directive wins; no replay of the sequence is needed.                                                                                     |
| 73e | `Release-As: auto` with no active hold                                 | No-op, `W158`.                                                                                                                                            |
| 73f | `auto` in a commit that is an ancestor of a later `none`               | Held — the `none` is newer.                                                                                                                               |
| 73g | A `cancel` whose barrier covers the commit carrying a hold             | The hold is discarded along with everything else before the barrier; the package resumes, with an empty ledger.                                           |
| 73h | `Release-As: none` on a unit that also carries `^minor`                | Legal but pointless while held; propagation is suppressed at the source (§13.7).                                                                          |
| 73i | `Release-As: patch` / `minor` / `major`                                | `E151` — `Release-As` has no bump form (§8.6). Change the type, or configure `types`.                                                                     |
| 73j | Held package whose channel directive changes                           | Not released; the channel directive is re-evaluated when the hold lifts.                                                                                  |
| 73l | Held package receives ordinary `feat`/`fix` commits afterwards         | Still held. They accumulate; only `auto`, an exact version, or a `cancel` lifts it (§8.6.1).                                                              |
| 73m | Held package receives a `feat!` after the hold                         | Still held. The withheld version reported by `W154` rises to the new `max()`, but nothing publishes.                                                      |
| 73k | Hold on a package that is also `cancel`-ed in a *later* commit         | Cancel discards the units; the hold survives (it is newer than nothing) only if its own commit is after the barrier.                                      |
| 74  | `0.4.1` with a breaking change, `preserveMajorZero: true`              | `0.5.0`.                                                                                                                                                  |
| 75  | Untagged package with a breaking change                                | `initialVersion` (`0.1.0`), not `1.0.0`.                                                                                                                  |
| 76  | Shallow clone missing tags or ancestry                                 | The engine MUST detect a shallow repository and fail with `E196` rather than compute from partial history.                                                |
| 77  | Squash-merged PR containing many units                                 | Parsed as a multi-unit message — the primary reason the separator exists.                                                                                 |
| 78  | Commit reachable by two merge paths                                    | Counted once (§13.3).                                                                                                                                     |
| 79  | Empty commit (`--allow-empty`) carrying only directives                | Fully supported; this is the normal shape of a `release` or `cancel` commit.                                                                              |
| 80  | Two packages, one on `beta` and one stable, in one commit              | Independent; channel is per package.                                                                                                                      |
| 81  | `feat!` and a `revert` of it in one window                             | Still `major` — `max()` of `major` and `patch`. Pair with `cancel` to drop the signal (§7.3).                                                             |
| 82  | Engine run twice with no new commits                                   | Identical plan (§17.2).                                                                                                                                   |
| 83  | Engine run again after a successful publish                            | Empty plan; the new tags moved `stableCommit` forward.                                                                                                    |
| 84  | Run fails after publishing 3 of 7 packages                             | The 3 are tagged; re-running publishes exactly the remaining 4, at the same versions (§13.7c G3, §19.3).                                                  |
| 85  | Bot commit rewriting dependent manifests                               | MUST use a type mapping to `none`, or be excluded from the window, or the release loops (§19.5).                                                          |
| 86  | Message exceeding `limits.*`                                           | `E158`, message-scoped; never a crash (§18.3).                                                                                                            |

### 15.6 Partial failure, catch-up, and publishing

| #   | Case                                                                     | Resolution                                                                                                                                                                                          |
|-----|--------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 87  | Provider published, consumer's publish failed                            | Consumer is released on the next run at the **same** version (§13.7c G3), marked `W193`. The case §13.7a exists for.                                                                                |
| 88a | Scheduled staleness audit over a workspace that has never run the engine | Reports every package behind a dependency as `W195` (§13.7b). Reporting only — it never blocks and never releases.                                                                                  |
| 88  | The same, five runs later, with no new commits                           | Still released, still at the same version. Admission depends on the consumer's window, which has not moved (G2).                                                                                    |
| 89  | Provider published, consumer succeeded, run re-run                       | Empty plan. The contribution is discharged exactly once (G4).                                                                                                                                       |
| 90  | Consumer released in the interim for its own `feat`                      | No catch-up: its window no longer contains the commit, and its own release already picked up the dependency. Its range for that dependency is reconciled at publish time and reports `W197` (§9.4). |
| 91  | Mid-chain failure under `^^` (`core`→`ui`→`theme`, `ui` fails)           | `theme` is **blocked**, not published (`W194`). On resume, `ui` then `theme`, both at their originally planned versions.                                                                            |
| 92  | The same, but with `+1` instead of `^^`                                  | `theme` was never in the plan and never enters it. Catch-up cannot widen depth (G5).                                                                                                                |
| 93  | Consumer would be published before its provider in one run               | Impossible: §19.2 orders dependencies first. An implementation that emits this order fails conformance (`E197`).                                                                                    |
| 94  | Publish succeeded but the tag write failed                               | Next run re-attempts, registry reports the version exists; identity verified → tag adopted, `W196` (§19.4). Unverifiable → `E198`, abort.                                                           |
| 95  | `cancel(consumer)` after the provider released                           | The pending propagated contribution is discarded (§13.5a); the consumer is not released.                                                                                                            |
| 96  | `cancel(provider)` after the provider released                           | No-op for the provider (`W170`); the consumer still catches up (§13.4a). Cancel never reaches a published release.                                                                                  |
| 97  | Consumer held by `Release-As: none` when its provider releases           | Recorded, not released (`W154`). Catches up on the run that lifts the hold, at the then-current `max()`.                                                                                            |
| 98  | Private package in the plan, run fully successful                        | Plan is empty. Private packages are tagged like any other (§13.10a), so they converge; `E199` is unconditional.                                                                                     |
| 99  | Dependency cycle over runtime edges, anywhere in the workspace           | `E200`, repository-scoped. The run aborts before planning, whether or not the cycle is in this run's plan (§13.1).                                                                                  |
| 99a | Cycle existing only through `devDependencies`                            | Not a cycle for this purpose — those edges are in neither `propagation.kinds` nor `publish.orderKinds` (§13.1). Common and legitimate.                                                              |
| 99b | A cycle among packages none of which have a bump this run                | Still `E200`. Acyclicity is a property of the workspace, checked at load, not of the plan.                                                                                                          |
| 99c | A cycle is introduced by the commit being released                       | `E200`; nothing is published. The graph is read at `HEAD` (§13.1), so the offending commit is the one that must be fixed.                                                                           |
| 100 | Optional dependency fails to publish                                     | Dependents are **not** blocked — an optional dependency is installable in its absence. Ordering still respects the edge (`publish.blockingKinds`, §19.3).                                           |
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

---

## 16. Diagnostics registry

Errors (`E`) MUST be reported. Their blast radius depends on the code:

* **Unit-scoped** (`E100`–`E181`, except `E158`) — the offending unit contributes nothing; other units in the same
  commit still apply, and other commits are unaffected.
* **Message-scoped** (`E001`, `E002`, `E158`) — the commit contributes nothing.
* **Repository-scoped** (`E182`, `E185`, `E191`, `E195`, `E196`, `E200`) — the run cannot produce a correct plan and
  MUST abort. These are integrity failures, not authoring mistakes, and no partial release may be emitted.
* **Run-scoped** (`E197`, `E198`, `E199`) — the run's *publication* cannot be completed or trusted. Packages already
  published and tagged before the error remain published and tagged; the run MUST stop, report what was completed, and
  exit non-zero. These are recoverable by a later run, unlike repository-scoped errors.

Every code is in exactly one bucket. `E182` and `E185` are repository-scoped although each is discovered while computing
one package's version: neither has an offending unit — both are properties of a tag that already exists, found during
baseline computation — so "the offending unit contributes nothing" has nothing to name, and neither can be resolved by
anything in the commit log. Like `E191` and `E195`, they are resolved by a human correcting the tag, after which the run
is simply repeated.

Warnings (`W`) never block a release. Commit-lint implementations SHOULD reject a commit at authoring time on any unit-
or message-scoped `E`, and SHOULD additionally reject `W155`, `W156`, and `W172`, which are silent-wrong-answer warnings
rather than style notes. `W193`, `W194`, `W202` and `W208` are release-time, not authoring-time, and are
non-suppressible for the same reason: each reports a release outcome that a reader of the commit log alone cannot
account for. `W193` and `W202` explain a package's *presence* in a plan — a catch-up and a channel-only release both
appear with no commits of their own — while `W194` and `W208` explain an *absence*: a package that was planned and not
attempted, and a dependent a caret reached but could not oblige. The complete non-suppressible set is therefore
`W155`, `W156`, `W172`, `W193`, `W194`, `W202`, and `W208` (§14.2, §17.1 #8).

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
| `E151` | Footer value is not valid for its key — including `Release-As: patch\|minor\|major`, which has no bump form (§8.6).                                                                                       |
| `E153` | `Release-As` version not greater than baseline.                                                                                                                                                           |
| `E154` | Exact `Release-As` on a multi-package scope-set.                                                                                                                                                          |
| `E156` | Exact `Release-As` lower than the computed version (lenient: downgraded to `W159`).                                                                                                                       |
| `E157` | Exact `Release-As` exceeds the computed version by more than `maxMajorJump` majors (§14.1).                                                                                                               |
| `E158` | A `limits.*` cap was exceeded (§14.1). Message-scoped.                                                                                                                                                    |
| `E170` | `cancel` unit with `!`.                                                                                                                                                                                   |
| `E171` | `cancel` unit with inline directives or a §8.1 release-directive footer. Message-level trailers (§4.5) and unknown keys are exempt.                                                                       |
| `E180` | Reserved channel name `latest`.                                                                                                                                                                           |
| `E181` | Channel name contains uppercase or illegal characters, or is outside `channels.allowed`. Applies to both sides of a transition.                                                                           |
| `E182` | Existing prerelease tag uses a non-numeric counter (§15.5 #64). Repository-scoped; no offending unit.                                                                                                     |
| `E185` | Graduation would not increase the version (§11.5). Repository-scoped; only reachable from hand-edited tags.                                                                                               |
| `E191` | Two reachable tags carry the same version for one package on different commits.                                                                                                                           |
| `E195` | Computed version not greater than baseline.                                                                                                                                                               |
| `E196` | Repository is shallow or grafted; history is incomplete.                                                                                                                                                  |
| `E197` | Publish order violation: a package was published before a workspace dependency also in this run's plan (§19.2). Run-scoped.                                                                               |
| `E198` | The registry already holds this version and its identity could not be verified as this run's artefact (§19.4). Run-scoped.                                                                                |
| `E199` | Convergence check failed: a package remains stale after a fully successful run (§19.6). Run-scoped.                                                                                                       |
| `E200` | The dependency graph contains a cycle over runtime edge kinds (§13.1). Repository-scoped; names the members.                                                                                              |

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
| `W152` | A propagation directive in which **every** part resolves to no propagation, on either axis — `^none`, `+0`, `^none+*`, `^^none`, `%%none`, `++0`, `%%none++*`. Writing nothing says the same thing. Where a value was supplied and the depth is `0`, `W201` is emitted instead and `W152` is not (§8.3b). |
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
| `W201` | A propagation **value** was supplied on either axis while that axis's depth resolves to `0`, so it reaches nobody and is inert — `^minor+0`, `%%beta++0`, or the footer equivalents (§8.3b). Supersedes `W152`.                                                                                           |
| `W202` | Channel-only release: the package is in the plan solely because its channel changed. Carries the old and new channel (§13.9).                                                                                                                                                                             |
| `W203` | A package released on `stable` declares a range on a workspace dependency whose current version is a prerelease (§9.4). Names both packages and versions.                                                                                                                                                 |
| `W204` | Channel-entry patch applied: the computed prerelease would not have exceeded the baseline, so the target was advanced by one patch (§11.4).                                                                                                                                                               |
| `W205` | `Propagate-Channel-Scope` excluded every reached dependent. The channel-axis counterpart of `W135`.                                                                                                                                                                                                       |
| `W206` | A channel transition matched no package; usually a mistyped `<from>` (§5.3).                                                                                                                                                                                                                              |
| `W207` | A channel transition whose `<from>` equals its `<to>`; inert.                                                                                                                                                                                                                                             |
| `W208` | Propagated bump suppressed: no source package releases on a channel this dependent can resolve (§9.3a). Names the unit, the origin's channel and the target's.                                                                                                                                            |

---

## 17. Conformance

### 17.1 What a conforming implementation must do

An implementation conforms to CCME 1.0.0 if and only if it:

1. Parses messages per §4 and §5, producing exactly the units, scopes, and directives those sections define.
2. Resolves scopes per §6, including file-derived resolution (§6.2).
3. Applies the bump mapping of §7.1 and the `max()` combination rule of §9.1.
4. Implements `cancel` per §10, **including the ancestor-or-self rule of §10.3**.
5. Implements holds per §8.6.1 and §13.6a.
6. Computes versions per §11 and §13, reading state exclusively from git tags (§12.4), including the channel-entry patch
   of §11.4.
7. Computes the two propagation axes independently and in the phase order of §9.2, with both defaulting to depth `0`;
   graduates a dependent only through an explicit transition (§9.3); and suppresses a propagated bump the target cannot
   resolve (§9.3a).
8. Reproduces every vector in Appendix B.
9. Emits every diagnostic in §16 under the stated conditions, with `W155`, `W156`, `W172`, `W193`, `W194`, `W202`, and
   `W208` non-suppressible.
10. Admits propagation per §13.4a — against the **dependent's** window — and therefore satisfies G1–G8 of §13.7c.
11. Publishes per §19: dependency-first order, tag-after-publish, dependents blocked on failure, ranges reconciled
    against current versions.
12. Produces the plan of §13 exactly, whatever internal representation or optimisation it uses (§13.11).

An implementation that computes correct plans but publishes them in an arbitrary order does **not** conform. The
guarantees of §13.7c are joint properties of the computation and the publish protocol; either alone is insufficient,
because a plan that is correct when computed can still be executed into an inconsistent registry state.

An implementation MAY additionally support configuration beyond §14, extra footer keys, and richer output — none of
which affect conformance, provided §14's final paragraph is respected.

### 17.2 Determinism and idempotency

Both are normative, and both are testable:

* **Determinism.** For a fixed (repository state at `HEAD`, configuration), the release plan MUST be byte-identical
  across runs, machines, and implementations. Nothing may depend on wall-clock time, commit dates, tag creation order,
  filesystem iteration order, hash-map iteration order, or locale.
* **Idempotency.** Running the engine twice with no intervening commits MUST produce the same plan the second time.
  Running it after a successful publish MUST produce an empty plan, because the tags written by the first run move each
  package's `stableCommit` forward (§13.3).
* **Convergence.** Running the engine after a *partially* successful publish MUST produce a plan containing exactly the
  packages that did not publish, at the versions they were originally planned at (§13.7c G2–G4, G6). Both idempotency
  and convergence are scoped to packages that are not held (§13.7c); no other exclusion exists.

Idempotency is the special case of convergence in which nothing failed. Stating only the special case is what leaves the
general one unimplemented, so both are required here explicitly.

Implementations MUST sort every collection that reaches the output — packages, contributing units, diagnostics — by a
total order (package name byte-wise; then commit topological index; then unit index). Iteration order of an unordered
container MUST NOT be observable.

### 17.3 Versioning of this specification

This document is CCME **1.0.0** and is itself versioned under SemVer:

* **Patch** — clarifications and editorial fixes that cannot change any release plan.
* **Minor** — new types, footers, or inline sigils; new diagnostics; new configuration keys. A 1.x implementation MUST
  ignore unknown footer keys (`W150`) and MAY ignore unknown types (`W140`), so minor additions are forward-compatible
  by construction.
* **Major** — any change that alters the release plan for a message that was already valid.

The escape hatches that make minor versions safe are `W140` and `W150`. Implementations MUST NOT convert either into an
error by default; `strictTypes` is opt-in for exactly this reason.

---

## 18. Security considerations

Commit messages are **untrusted input**. In any repository that accepts contributions, the message text is
attacker-controlled, and under CCME that text directs version numbers, release scope, and publication. This section is
normative.

### 18.1 Threat model

The release engine typically runs in CI with credentials to publish packages. An attacker who can land a commit — or
merely open a pull request, if CI runs the engine on PR branches with those credentials — controls the input to that
engine.

| Attack                         | Vector                                                                | Effect                                                                                                                                                                                                                                                           |
|--------------------------------|-----------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **Mass version burn**          | `feat(*)^^major!: x`                                                  | Every package in the workspace takes a major bump. Irreversible: published versions cannot be recalled, and the version space below them is gone forever.                                                                                                        |
| **Version-space exhaustion**   | `Release-As: 999999.0.0`                                              | Burns the package's version space permanently. No subsequent release can ever be lower.                                                                                                                                                                          |
| **Ledger wipe**                | `cancel(*): x`                                                        | Discards every pending change, silently dropping the release and its changelog.                                                                                                                                                                                  |
| **Silent release freeze**      | `Release-As: none` on a wide scope                                    | Blocks releases indefinitely; visible only as `W154`, easily lost in CI logs.                                                                                                                                                                                    |
| **Channel hijack**             | `%beta` on a package expected to be stable                            | Diverts a release to a prerelease channel, or graduates a prerelease that was not ready.                                                                                                                                                                         |
| **Prerelease flood**           | `%%beta++*` on a widely-depended package                              | Moves an entire reverse closure onto a prerelease line in one commit. Reversible — stable baselines are untouched — but noisy, and it is `maxPackagesPerRun` and `maxChannelMovesPerRun` that bound it.                                                          |
| **Forced graduation**          | `%%*>stable++*` on a widely-depended package                          | Ends every dependent's prerelease train at once, publishing versions consumers resolve by default. Irreversible, since the stable versions cannot be recalled. Requires an explicit transition to write, which is why `W200` blocks every implicit route (§9.3). |
| **Blast-radius amplification** | `^^inherit` on a leaf package                                         | Turns a one-package change into a workspace-wide major release. Bounded by propagation being opt-in (§8.3): the reach is always written in the message.                                                                                                          |
| **Resource exhaustion**        | Pathological scope globs or very large depths across a huge workspace | CPU/memory pressure in CI. Bounded by §18.3.                                                                                                                                                                                                                     |
| **Induced-failure widening**   | Causing one publish to fail, hoping the retry releases more           | **Not possible.** §13.7c G5: a later run's target set is a subset of the first's. A retry can only discharge work already planned and reviewed.                                                                                                                  |
| **Stale-consumer wedge**       | A hold left in place on a widely-depended package                     | Consumers accumulate unreleased propagation indefinitely. Visible as `W154` plus a growing `W193` set; surfaced by the audit of §13.7b.                                                                                                                          |
| **Artefact adoption**          | Pre-publishing a version the engine is about to publish               | Blocked by the identity check of §19.4: an unverifiable pre-existing version is `E198`, never silently tagged.                                                                                                                                                   |

Note that none of these require unusual syntax. They are ordinary, valid CCME — which is the point: **the format's
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

The blast-radius review of mitigation 2 is what makes G5 useful in practice: because the target set can only shrink
across runs, the plan a reviewer approves before the first attempt is an upper bound on everything every subsequent
retry can publish. A reviewer never has to re-approve a resume.

Implementations SHOULD additionally offer, and repositories accepting external contributions SHOULD enable:

5. **A blast-radius gate** — refuse or require explicit approval when a single run would release more than
   `maxPackagesPerRun` packages, or apply a `major` bump to more than `maxMajorsPerRun` (§14).
6. **A directive allowlist** — restrict which directives may originate from outside a trusted path. `cancel`,
   `Release-As`, `^^`, `++*`, and any transition ending in `>stable` are the high-privilege ones;
   `requireCodeownerFor` names the directives that must be approved by a CODEOWNER.
7. **A channel-move gate** — refuse or require approval when a single run would move more than
   `maxChannelMovesPerRun` packages between channels, in either direction. Channel moves are the cheapest broad effect
   to write (`++*` is three characters) and, in the graduating direction, the least reversible.
8. **Version sanity bounds** — reject an exact `Release-As` that raises the major version by more than `maxMajorJump`
   (default 1) beyond the computed version. This closes version-space exhaustion without blocking legitimate
   `Release-As: 2.0.0`.

### 18.3 Parser hardening

The parser is a bounded, single-pass scan by construction (§20.7): O (n) time, O (1) working space, no backtracking, no
recursion. A hostile message cannot induce superlinear parsing — this is the concrete reason §20 exists alongside
Appendix A, since a careless regex implementation reintroduces the risk it was designed to remove.

Remaining bounds implementations MUST enforce:

* Depth values saturate at 1024 (§20.3); a graph is never deeper.
* Scope-set length, unit count per message, and message length **MUST** be capped, and are, by the defaults of
  `limits.*` (§14.1) — these are the parser bounds, they are on without configuration, and they cannot be disabled. An
  operator may raise or lower the numbers; setting them to `null` or to zero is not conforming. Exceeding a cap is a
  diagnostic (`E158`), never a crash.
* Glob evaluation MUST be linear in workspace size; patterns are matched, never compiled to a backtracking engine.

Parser hardening bounds one message. It says nothing about the cost of the run as a whole, which is bounded in §13.11; a
workspace large enough for that section to matter is also a workspace where a hostile commit has more leverage, and
`maxPackagesPerRun` (§14.1) is the control that limits it.

### 18.4 Supply-chain notes

* Tags are the state store, so **tag write access is release authority**. Restrict tag creation to CI.
* `E191` (duplicate version on different commits) is an integrity signal, not a nuisance — it is what a tag-rewriting
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

### 19.1 Tagging

* A release tag MUST be created at the exact commit the plan was computed from — `HEAD` of the run — for every released
  package, **whatever its publish target** (§13.10a).
* Tags MUST be created **after** a successful publish of that package, never before. A tag for an unpublished version
  makes the version unrecoverable, since §13.3 will treat it as released.
* Tags MUST be created **immediately** after that package publishes, not batched to the end of the run (§19.3).
* Tags SHOULD be annotated, and MUST NOT be moved or deleted once pushed.
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
    return [ P for P in seq if P in plan ]                   # filter LAST — see below
```

**Order over the whole graph, then filter — never over the induced subgraph.** The traversal MUST run on every package
in the workspace and drop the unplanned ones only at the end. Inducing the graph on the planned packages first deletes
the paths that run *through* unplanned ones, and those paths still constrain the order. Given `A → B → C` where `B`
has no bump this run — by far the common case in an incremental release — the induced subgraph on `{A, C}` has **no edge
at all**, so `A` and `C` become mutually unordered and `A` may publish first — against a `C` that has not published yet.
The constraint is real even though `B` is not being released, because `A` still resolves `C` through `B` at install
time. The same applies to a package that is merely up to date, which is the common case in any incremental release.

* The order exists and is total because the graph is acyclic (§13.1). No condensation step is needed, and an
  implementation that finds itself needing one has a workspace that should have been rejected by `E200`.
* Ties are broken byte-wise by name so that the order is deterministic (§17.2). Two implementations MUST produce the
  same sequence.
* Ordering uses `publish.orderKinds`, which includes `optionalDependencies`: an optional dependency should still be
  available before its dependent, even though its absence does not block (§19.3).

Order matters for a reason that tags cannot express. If a dependent is published before its dependency, it is published
carrying a range reconciled against the dependency's *old* version — and both land at the same commit, so no later run
can tell from ancestry that anything is wrong (§13.7b). The inconsistency is undetectable after the fact, which is why
it is prevented rather than diagnosed. An implementation that detects it MUST raise `E197`.

`E197` is scoped to the rule above, not to propagation. Every workspace dependency edge between two packages in the plan
constrains the order, whether or not either package's bump arrived by propagation — a dependent that is in the plan for
its own unrelated `feat` still resolves its dependency at install time (vector 80a). An `E197` that fires only for
propagated pairs is under-reporting.

### 19.3 Partial failure

A run that publishes several packages MAY fail partway. Implementations MUST:

* tag each package immediately after that package publishes, so a partial run leaves a consistent, resumable state;
* on a failure, **block** every package in the plan that transitively depends on the failed one over
  `publish.blockingKinds`, marking each `W194`, and not attempt it. The closure is computed over the **full** workspace
  graph, so it traverses packages that are not in the plan: a package with no bump this run is still a path from a
  dependent to a failed dependency;
* continue publishing packages that are not blocked — an unrelated subtree has no reason to be punished for another's
  failure;
* report a completion summary naming what published, what failed, and what was blocked, and exit non-zero;
* on re-run, recompute from tags. Packages already tagged fall out of the plan by §13.6; packages that failed or were
  blocked remain in it by §13.4a, at their original versions (§13.7c G3).

```
run(plan):
    published, failed, blocked = {}, {}, {}
    for P in publishOrder(plan, graph):            # a flat sequence, §19.2
        if transitiveBlockingDeps(P, graph) & failed:   # full graph, §19.3
            blocked[P] = W194; continue
        reconcileRanges(P)                         # §9.4
        try:
            if publishTarget(P) != none:           # §13.10a
                publishTo(publishTarget(P), P, plan[P].version)   # or adopt, §19.4
            tag(P, plan[P].version, HEAD)          # §19.1 — always, immediately
            published[P] = plan[P].version
        except PublishError as e:
            failed[P] = e
            if config.publish.onFailure == 'abort': return report(...)
    return report(published, failed, blocked)
```

`publishOrder` returns a **flat, totally ordered sequence**, not levels, so `run` iterates it directly. Publishing
strictly one at a time is the normative reading; an implementation MAY publish concurrently, but only if it preserves
the same happens-before relation — every dependency in the plan completes before its dependent starts — and reports in
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

1. verify that the published artefact is the one this run would have published — by registry digest, provenance
   attestation, or an equivalent content identity check;
2. if it matches, treat the publish as successful, write the tag, and emit `W196`;
3. if it does not match, or identity cannot be established, raise `E198` and stop.

Step 3 is not pedantry. A mismatch means the version was published by something other than this run — a hand publish, a
competing branch, a compromised token — and tagging it would enrol a foreign artefact into the release lineage as if
this repository had produced it. Verification is what makes step 2 safe; without it, adoption is a supply-chain
vulnerability rather than a convenience. `publish.adoptPublished: false` disables adoption entirely for repositories
whose registry cannot support such a check.

### 19.5 Manifest writes

Per §9.4, each released package's manifest MUST be updated so that no released package declares a range excluding the
version each workspace dependency will carry **at the end of this run** — that is, its **planned** version if it is in
this run's plan, and its current baseline otherwise (`W197` for the latter when the baseline came from an earlier run).

Because the graph is acyclic (§13.1) and §19.2 publishes every dependency before its dependents, the planned version and
the currently-published one coincide at the moment each package is reconciled. Stating the rule in terms of the
**planned** version is nonetheless the better formulation: it is order-independent, so reconciliation can be computed
once up front and audited against the plan, and it does not silently become wrong if an implementation's publish order
is ever perturbed.

These writes are part of the publish step, not the plan, and MUST NOT be committed back in a way that creates a release
loop — a bot commit that itself parses as a bump-producing unit. Bot commits SHOULD use a type mapped to `none`, and
repositories SHOULD exclude the bot's commits from the pending window by convention.

### 19.6 Convergence verification

After a run in which **nothing** failed or was blocked, implementations SHOULD re-plan and assert that the resulting
plan is empty. A non-empty result means some package remains stale despite a fully successful run, which can only be an
implementation defect — most likely admission tested against the wrong window (§13.4a). It MUST be reported as
`E199`.

Exactly one exclusion applies: **held packages**, which are excluded from publication by `Release-As: none` (§13.6a)
and are expected to persist until the hold lifts. There is no exclusion for private, internal, or artefact-less
packages, because §13.10a tags all of them.

That the check is unconditional is the point. If any class of package could legitimately linger in the plan for ever,
"the plan is not empty" could not be treated as an error at all, and the check would be worthless precisely where it is
needed most.

The check is cheap — one extra planning pass, no registry traffic — and it converts the failure mode of §13.7a from a
silent one into a loud one. Repositories running the engine on a schedule SHOULD also run the staleness audit of §13.7b
and surface `W195`, which catches the same class of problem in a workspace whose packages were already behind their
dependencies before CCME was adopted.

### 19.7 Adopting CCME where packages are already stale

A workspace arriving from another release tool — or from a hand-run process that failed partway at some point — may hold
packages that are already behind their dependencies. The engine does not treat these as a special case: their
propagating commits are still inside their pending windows, precisely because those packages never released, so the
first run finds them and releases them like any other catch-up (§13.7a). The backlog may nonetheless be large enough to
be worth looking at before it publishes:

1. Run the engine in plan-only mode (`--dry-run`, §18.2) and read the `W193` entries. These are the stale packages.
2. Confirm the versions look right. By G3 they are the versions those packages were owed, which may be lower than an
   operator expects if the workspace has moved on considerably. They are still correct: the bump is `max()` over
   everything pending, so nothing is under-counted.
3. Release normally. One run discharges the whole backlog — one release per package, not one per missed propagation.

If an accumulated catch-up is genuinely unwanted — the dependency change no longer matters and the operator prefers not
to publish at all — the supported way to drop it is `cancel(<consumer>)` (§13.5a), not tag surgery. A blanket
`cancel(*)` as the first commit after adoption (§10.6) drops all of them at once.

## 20. Parsing without regular expressions

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
    depthFrom  = none      # scratch: which token supplied h.inline['depth'] — '^', '^^' or '+'
    cdepthFrom = none      # scratch: which token supplied h.inline['channelDepth'] — '%%' or '++'

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
            if not sc.eof and sc.peek == '^': raise E110   # ^^^ — third caret
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
            if not sc.eof and sc.peek == '%': raise E110   # %%% — third percent sign
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
            if not sc.eof and sc.peek == '+': raise E110   # +++ — third plus
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
            if depthFrom == '^^':                      # '^^' asserts 'all' — disagreement is
                if validateInline('depth', value) != ALL: raise E113
                warn W110                              # '^^…+*' — redundant, not wrong
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
        if not isDigit(c): raise E111
        n = n * 10 + (c - '0')
        if n > 1024: return ALL          # saturate; no graph is deeper
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

`isChannel` (§20.1) does not admit `>`, so the split is unambiguous: a channel name can never contain the separator, and
`indexOf` needs no lookahead. `readUntilAny('^+%!:')` does not stop at `>`, so the whole transition arrives as one
value.

**Why phase 3 is unambiguous.** The scope-set has already been consumed at phase 2, so any `%` remaining is outside
parentheses and can only be a channel sigil. `readUntilAny('^+%!:')` stops at the next sigil, the breaking marker, or
the colon — none of which may appear in a directive value. No lookahead beyond one character is needed.

The three doubled tokens do not change that bound. Each is a fixed two-character token distinguished from its single
form by one `peek`, and each is followed by a guard against a third repetition, because without it `^^^minor` would
tokenise as `^^` (empty value) followed by `^minor` and parse silently as `^^minor`; `%%%rc` and `+++2` have the same
shape. Repeated sigils are never a count.

An empty value is legal **only** after `^` and `^^`. `%%`, `++`, `%` and `+` all raise `E111` on an empty value: a
channel with no name and a depth with no number carry no default worth guessing, whereas a caret's value is a bump and
bumps have one.

`sawCaret`, `depthFrom` and `cdepthFrom` in the listing are scanner scratch state, not part of the parsed result.
`sawCaret` enforces the once-per-header rule across both caret spellings, so `^minor^^` and `^+2^^` are alike `E110`
rather than one of them falling through to a depth check. `depthFrom` and `cdepthFrom` record *which token* supplied
each depth, which is what keeps every combination order-independent:

| Header             | transitions                      | Result                                                   |
|--------------------|----------------------------------|----------------------------------------------------------|
| `^minor+2`         | `depthFrom: none → '^' → '+'`    | depth `2` — the `+N` overrides `^`'s implied 1           |
| `+2^minor`         | `depthFrom: none → '+'`          | depth `2` — `^` supplies nothing, none needed            |
| `^^minor+2`        | `depthFrom: none → '^^'`         | `E113`                                                   |
| `+2^^minor`        | `depthFrom: none → '+'`          | `E113`                                                   |
| `^^minor+*`        | `depthFrom: none → '^^'`         | depth `all`, `W110`                                      |
| `^minor+2+3`       | `depthFrom: none → '^' → '+'`    | `E110` on `+3` — one `+N` per header                     |
| `%%beta++3`        | `cdepthFrom: none → '%%' → '++'` | channel depth `3` — the `++N` overrides `%%`'s implied 1 |
| `++3%%beta`        | `cdepthFrom: none → '++'`        | channel depth `3` — `%%` supplies nothing, none needed   |
| `%%beta++1++2`     | `cdepthFrom: none → '%%' → '++'` | `E110` on `++2` — one `++N` per header                   |
| `%%beta%%rc`       | —                                | `E110` — one `%%` per header                             |
| `^^minor%%beta++1` | both, independently              | bump all levels, channel one level — legal (§5.3)        |

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

1. it begins with `BREAKING CHANGE` followed by `: ` — a literal string comparison; or
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

* `Breaking change: ...` — the generic loop halts at the space, `startsWith(': ')` fails, and the line is **not a footer
  at all**. `footerKeyEnd` catches it explicitly and warns `W155` before returning `-1`.
* `breaking-change: ...` — the hyphenated form contains only footer-key characters, so it **is** a well-formed footer
  with the key `breaking-change`. `footerKeyEnd` cannot catch this one; the check belongs at key resolution, where the
  key is compared to `BREAKING-CHANGE` exactly (breaking) and then case-insensitively (`W155`, not breaking) before
  falling through to `W150` for unknown keys.

Every other key is resolved case-insensitively at that same point, so `BREAKING CHANGE` is the one place where the
resolver must compare twice.

A `nearlyFooterBlock` is one where the first line is a footer start but a later line is not — the common typo of a body
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

* Time: O (n) in message length, one pass, no backtracking.
* Space: O (1) beyond the parsed result.
* No input can cause superlinear behaviour — the property that motivates avoiding regular expressions in code that runs
  over untrusted commit messages in CI.
* Every error is raised at a known index, so implementations can render a caret pointing at the offending character.

---

## 21. Appendix A — Regular expressions

Provided for implementers who prefer patterns. The equivalence claimed is exact and holds over **all** input, not merely
well-formed input: for every byte string, a pattern here matches if and only if §20 accepts. §20 remains normative for
*which* diagnostic is raised, for error positions, and for every check the patterns cannot express — repetition guards,
`latest`, scope-term semantics, and the saturation of out-of-range depths.

Where a pattern and §20 appear to disagree, §20 is wrong or the pattern is wrong; neither is licensed to be laxer than
the other. The depth patterns below are where the two formulations most easily diverge, so they deserve particular
care when either side is changed.

All patterns are PCRE, anchored, and free of nested quantifiers, so they cannot backtrack catastrophically.

**Header (single pattern):**

```regex
^(?<type>[a-z]+)(?:\((?<scopes>[^()\r\n]+)\))?(?<inline>(?:\^\^[^\^+%!:\r\n]*|%%[^\^+%!:\r\n]+|\+\+[^\^+%!:\r\n]+|\^[^\^+%!:\r\n]*|[+%][^\^+%!:\r\n]+)*)(?<breaking>!)?: (?<description>\S[^\r\n]*)$
```

Group notes: `scopes` still requires splitting on `,` and per-term validation; `inline` still requires tokenising by
sigil. The pattern recognises shape, not validity.

The description group opens with `\S`, not `[^\r\n]`, so that the two-space form `feat:  x` is rejected rather than
parsed with a leading space in the description (`E120`, vector 18). A `+` quantifier over `[^\r\n]` silently accepts it.

**Inline directive tokens (apply with a global match to `inline`):**

```regex
(\^\^|%%|\+\+|[\^+%])([^\^+%!:]*)
```

All three doubled alternatives MUST come first — with `[\^+%]` first, `^^minor` tokenises as a bare `^` with an empty
value followed by `^minor`, `%%rc` as `%` followed by `%rc`, and `++2` as `+` with an empty value followed by `+2`,
which is `E111` where the correct answer is a channel depth of 2. Note also that the value quantifier is `*`, not `+`,
so that `^` and `^^` may stand alone and so that a valueless `%%` or `++` still tokenises: an empty value is legal after
`^` and `^^`, and is `E111` after `+`, `%`, `%%`, and `++` — which the pattern does not catch and the caller MUST check.
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
breaks the equivalence this appendix claims. Length is bounded elsewhere — `limits.messageBytes` (§14.1) caps the whole
message — so an unbounded digit run is not an attack surface, and the pattern still cannot backtrack: `[0-9]*`
follows a `[1-9]` that no other alternative can match, so there is exactly one way to parse any input.

The leading-zero exclusion is real and normative — `00` and `007` are `E111`, not `0` and `7` — and §20.3's digit loop
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
key is resolved case-insensitively — but at key resolution, not in this pattern.

**Release tag — note the greedy prefix, which is what makes the last-`%` rule work:**

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

## 22. Appendix B — Conformance test vectors

Each vector is `input → expected`. An implementation is conforming if it reproduces every one. Workspace for all
vectors:

```
packages: core, cli, ui, api, docs-site (private -> internal registry), @acme/theme
edges:    cli -> core, ui -> core, api -> core, docs-site -> ui, @acme/theme -> ui
tags:     core@1.4.2, cli@2.0.0, ui@0.9.1, api@1.2.0, @acme/theme@1.0.0
          (docs-site has never been released, so it has no baseline; being private
           does not exempt it from release or tagging — §13.10a)
```

Sections B.4 and B.5 override these tags locally where stated.

### B.1 Parsing

| #    | Input header                             | Expected                                                                                 |
|------|------------------------------------------|------------------------------------------------------------------------------------------|
| 1    | `feat: x`                                | type `feat`, derived scope, bump `minor`                                                 |
| 2    | `fix(core): x`                           | scopes `[core]`, bump `patch`                                                            |
| 3    | `feat(core,cli): x`                      | scopes `[core, cli]`                                                                     |
| 4    | `feat(core, cli): x`                     | scopes `[core, cli]` — space after comma allowed                                         |
| 5    | `feat(core ,cli): x`                     | `E102`                                                                                   |
| 6    | `feat(@acme/theme): x`                   | scopes `[@acme/theme]` — `@` inside parens is literal                                    |
| 7    | `feat(@acme/theme)%beta: x`              | scopes `[@acme/theme]`, channel `beta`                                                   |
| 8    | `feat(*,-docs-site): x`                  | all packages except `docs-site`                                                          |
| 9    | `feat(.,-ui): x`                         | derived set minus `ui`                                                                   |
| 10   | `feat(core)^minor+2: x`                  | propagate `minor`, depth `2`                                                             |
| 11   | `feat(core)+2^minor: x`                  | identical to #10 — order-independent                                                     |
| 12   | `feat(core)^minor^patch: x`              | `E110`                                                                                   |
| 13   | `feat(core)^med: x`                      | `E111`                                                                                   |
| 14   | `feat(core)^minor+*!: x`                 | breaking, propagate `minor`, depth `all`                                                 |
| 14a  | `feat(core)^^minor: x`                   | propagate `minor`, depth `all` — identical to #14 without `!`                            |
| 14b  | `feat(core)^^: x`                        | propagate `patch` (default), depth `all` — identical to `+*`                             |
| 14c  | `feat(core)^^!: x`                       | breaking, propagate `patch`, depth `all`                                                 |
| 14d  | `feat(core)^^minor+*: x`                 | as #14a, plus `W110` for the redundant `+*`                                              |
| 14e  | `feat(core)^^minor+2: x`                 | `E113`                                                                                   |
| 14f  | `feat(core)+2^^minor: x`                 | `E113` — order-independent                                                               |
| 14g  | `feat(core)^minor^^: x`                  | `E110` — `^` and `^^` are one sigil                                                      |
| 14h  | `feat(core)^^^minor: x`                  | `E110` — third caret                                                                     |
| 14i  | `feat(core)^^med: x`                     | `E111`                                                                                   |
| 14j  | `feat(core)^^%beta: x`                   | propagate `patch`, depth `all`, channel `beta`; channel depth `0`                        |
| 14k  | `feat(core)++2: x`                       | channel depth `2`, `Propagate-Channel` defaults to `inherit`; no bump propagation        |
| 14l  | `feat(core)++: x`                        | `E111` — `++` carries no default depth                                                   |
| 14m  | `feat(core)+++2: x`                      | `E110` — third plus                                                                      |
| 14n  | `feat(core)++1++2: x`                    | `E110` — one `++N` per header                                                            |
| 14o  | `feat(core)%%beta++3: x`                 | channel `beta`, channel depth `3` — `++N` wins over `%%`'s implied 1, no diagnostic      |
| 14p  | `feat(core)++3%%beta: x`                 | identical to 14o — order-independent                                                     |
| 14q  | `feat(core)^^minor%%beta++1: x`          | bump `minor` to all levels, channel `beta` to one. Both axes, independent (§5.3)         |
| 14r  | `feat(core)+2++1: x`                     | depth `2`, channel depth `1`. `+` and `++` are distinct sigils                           |
| 14r1 | `feat(core)+9999: x`                     | depth `all` — saturated at `1024` (§20.3), not `E111`                                    |
| 14r2 | `feat(core)+20000: x`                    | depth `all` — the digit run is unbounded in length; saturation, never rejection          |
| 14r3 | `feat(core)++20000: x`                   | channel depth `all` — identical treatment on the channel axis                            |
| 14r4 | `feat(core)+00: x`                       | `E111` — leading zeros rejected; `0` alone is the only depth that may start with `0`     |
| 14r5 | `feat(core)+007: x`                      | `E111` — not `7`                                                                         |
| 14s  | `feat(core)%beta>rc: x`                  | `Channel` transition, `from` `beta`, `to` `rc`                                           |
| 14t  | `feat(core)%%*>stable++*: x`             | `Propagate-Channel` transition from any prerelease to stable, channel depth `all`        |
| 14u  | `feat(core)%%beta>*: x`                  | `E111` — `*` is a `from`-value only                                                      |
| 14v  | `feat(core)%%a>b>c: x`                   | `E111` — one `>` per value                                                               |
| 14w  | `feat(core)%>stable: x`                  | `E111` — empty `from`                                                                    |
| 14x  | `feat(core)%%beta>inherit: x`            | `E111` — `inherit` is a value, not a channel                                             |
| 15   | `feat(core)!^minor: x`                   | `E120` — `!` must precede the colon                                                      |
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
| 27e  | `feat2: x`                               | `E101` — digits are not type characters                                                  |
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

**Vector 31a** — `cancel` carrying a DCO trailer:

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
| 33  | `feat(core)+1: x`                                    | identical to #32a — `+1` and `^` say the same thing                                                                                                                                                  |
| 33b | `feat(core)+*: x`                                    | as #32a, plus `@acme/theme` `1.0.1`; `docs-site` released at `0.1.0` (`initialVersion`), tagged, published to the internal registry (§13.10a)                                                        |
| 33c | `feat(core)^^: x`                                    | identical to #33b                                                                                                                                                                                    |
| 33d | `feat(core)^minor: x`                                | `core` `1.5.0`; `cli` `2.1.0`, `api` `1.3.0`, `ui` `0.9.2` (minor remapped to patch while `0.y.z`, §12.6) — bump raised to `minor`, depth `1` from the caret                                         |
| 34  | `feat(core)^none: x`                                 | `core` minor only; `W152` — writing nothing says the same thing                                                                                                                                      |
| 35  | `feat(core)+0: x`                                    | `core` minor only; `W152` — no value was supplied, so this is redundancy, not an inert value                                                                                                         |
| 35a | `feat(core)^minor+0: x`                              | `core` minor only; `W201` **alone** — a `minor` was supplied and the depth discards it (§8.3b). An implementation emitting `W152` here fails this vector                                             |
| 35b | `feat(core)^none+0: x`                               | `core` minor only; `W152` — both parts say nothing                                                                                                                                                   |
| 36  | `feat(core)^inherit+*: x`                            | `core` minor; every dependent minor                                                                                                                                                                  |
| 36a | `feat(core)^^inherit: x`                             | identical to #36                                                                                                                                                                                     |
| 37  | `feat(core)!^inherit+1: x`                           | `core` major; `cli`, `ui`, `api` major                                                                                                                                                               |
| 37a | `feat(core)!: x`                                     | `core` `2.0.0` only. A breaking change propagates no further than any other unit without a caret                                                                                                     |
| 38  | `feat(core)^: x` + `feat(cli): y` in one window      | `cli` = max(minor direct, patch propagated) = minor                                                                                                                                                  |
| 39  | `feat(core)^^: x` with `Propagate-Scope: -docs-site` | As #33b minus `docs-site`, which is **untouched** and stays unreleased. `^^`, not `^`: at depth `1` `docs-site` is out of reach anyway and the vector would pass without the scope being read at all |
| 39a | `feat(core)++1: x`                                   | `core` `1.5.0`; `cli`, `ui`, `api` take `core`'s channel — already stable, so `W199` each and nothing else releases                                                                                  |
| 39b | `feat(core)%beta++1: x`                              | `core` `1.5.0-beta.0`; `cli` `2.0.1-beta.0`, `ui` `0.9.2-beta.0`, `api` `1.2.1-beta.0` — channel-only releases (`W202`, `W204`)                                                                      |
| 39c | `feat(core)^%beta: x`                                | **`core` `1.5.0-beta.0` alone.** The caret reaches all three; each is suppressed by §9.3a with `W208`                                                                                                |
| 39d | `feat(core)^%beta++1: x`                             | `core` `1.5.0-beta.0`; `cli` `2.0.1-beta.0`, `ui` `0.9.2-beta.0`, `api` `1.2.1-beta.0` — bump and channel together, no `W204`                                                                        |
| 39e | `feat(core)^^minor++1: x`                            | `minor` reaches all six; the origin's channel reaches only the three direct consumers. Axes are independent                                                                                          |
| 40  | `feat(ui)^: x`                                       | `ui` minor; `docs-site` and `@acme/theme` patch; `docs-site` released to the internal registry and tagged                                                                                            |

### B.4 Cancel

**Vector 41** — history `A: feat(core)`, `B: cancel(core)`, `C: fix(core)`, linear. → `core` = `1.4.3` (patch from `C`
only).

**Vector 42** — history `A: feat(core)`, `B: cancel(core)`, nothing after. → `core` not released; stays `1.4.2`.

**Vector 43** — one commit containing:

```
cancel(core): reset release state

---

feat(core): new thing
```

→ ancestor-or- **self**: the `feat` is discarded. `core` not released.

**Vector 44** — `A: feat(core)`; branch `C: feat(core)` from `A`; `B: cancel(core)` on main; merge `D`. → `A` discarded,
`C` retained. `core` = `1.5.0`.

**Vector 44a** — hold, then lift. From `core@1.4.2`:

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

**Vector 44b** — the same history with `cancel(core)` at commit 2 instead of the hold. → `core` = `1.4.3` at commit 3,
changelog containing only the fix. The `feat` is unrecoverable.

**Vector 44c** — hold never lifted. → `core` never releases; `W154` on every run; tuples keep accumulating.

**Vector 44d** — `cancel(core)` at commit 3 of vector 44a, before the lift. → The hold and the `feat` are both
discarded. `core` resumes with an empty ledger and is not released until something new lands.

**Vector 44f** — hold at commit 2, then three ordinary commits including a breaking change:

```
1: feat(core)^: streaming reader
2: release(core): hold        «Release-As: none»
3: fix(core): guard empty input
4: feat(core): add codec
5: fix(core)!: drop legacy flag
```

→ `core` is **still held** at commit 5. `W154` on every run; no ordinary commit lifts a hold. The withheld version it
reports rises from `1.5.0` to `2.0.0` once commit 5 lands. `cli`, `ui` and `api` receive nothing at any of the five
commits despite the caret on commit 1 — a held package is not a propagation source for as long as the hold stands.

**Vector 44g** — `Release-As: minor` on any unit. → `E151`. `Release-As` has no bump form (§8.6).

**Vector 44e** — `none` at commit 2, `auto` at commit 4, `none` again at commit 6. → Held. The newest package-level
directive wins outright; the engine does not replay the sequence.

**Vector 45** — `cancel(*)` in a repo where `api` is at `1.0.0-beta.3` with pending units. → pending units discarded,
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
| 57a | `cli` at `2.0.0`, no pending bumps, moved onto `beta` by `%%beta`   | `2.0.1-beta.0` — the channel-entry patch (§11.4). `W202` for the channel-only release, `W204` for the patch                                                                                           |
| 57b | the same, but `cli`'s window already carries a `fix`                | `2.0.1-beta.0` and **no** `W204`: `applyBump(2.0.0, patch)` already exceeds the baseline, so no extra step is taken                                                                                   |
| 57c | `cli` at `2.1.0-beta.3`, reached by `%%beta`                        | Nothing. `W199` — it is already on `beta`; the directive proposes no change. This is what makes the channel axis converge (§13.7c G7)                                                                 |
| 57d | `cli` at `2.1.0-beta.3`, reached by `%%beta>rc++1`                  | `2.1.0-rc.0` — the transition matches, the counter resets (§11.4)                                                                                                                                     |
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
| 65 | `core@1.5.0-beta3`               | `E182` on use as a prerelease baseline — repository-scoped: the run aborts (§16) |

### B.7 Partial failure and catch-up

These vectors use the workspace above and are stated as **run sequences**, because the property under test is what the
engine does on the *second* run. `run k ✓ P` means package `P` published and was tagged in run `k`; `run k ✗ P` means
its publish failed. Every run computes at the same `HEAD` unless stated otherwise.

**Vector 66** — the orphan. Commit `C1`: `feat(core)^: streaming reader`.

| Run | Event                                                    | Plan                                                             |
|-----|----------------------------------------------------------|------------------------------------------------------------------|
| 1   | ✓ `core@1.5.0`, ✓ `ui@0.9.2`, ✓ `api@1.2.1`, ✗ `cli` | `core` minor; `cli`, `ui`, `api` patch                           |
| 2   | ✓ `cli@2.0.1`                                           | **`cli` patch only**, marked `W193` *catch-up from `core@1.5.0`* |
| 3   | —                                                        | empty                                                            |

An engine that admits propagation against the **source's** window instead of the target's produces an **empty plan at
run 2**, and `cli` is never released — on that run or any later one. This vector is the single most important one in
this appendix: an implementation can pass every other vector and still fail this one.

**Vector 67** — catch-up does not widen depth. Commit `C1`: `feat(core)^: x` (depth `1`).

| Run | Event                    | Plan                                   |
|-----|--------------------------|----------------------------------------|
| 1   | ✓ `core@1.5.0`, rest ✗ | `core` minor; `cli`, `ui`, `api` patch |
| 2   | ✓ `ui@0.9.2`, rest ✗   | `cli`, `ui`, `api` patch               |
| 3   | —                        | `cli`, `api` patch                     |

`@acme/theme` and `docs-site` are at depth 2 and MUST NOT appear in any run, including run 3, where their dependency
`ui` has just republished. Propagation does not cascade (§9.2), and catch-up cannot widen a blast radius (§13.7c G5).

**Vector 68** — mid-chain failure under `^^`. Commit `C1`: `feat(core)^^: x`.

| Run | Event                                   | Plan                                                                   |
|-----|-----------------------------------------|------------------------------------------------------------------------|
| 1   | ✓ `core@1.5.0`, ✓ `ui@0.9.2`, ✗ rest | all six packages                                                       |
| 2   | —                                       | `cli`, `api`, `@acme/theme`, `docs-site` — `@acme/theme` still `patch` |

`@acme/theme` is admitted at depth 2 **from `core`**, not at depth 1 from the republishing `ui`. Depth is measured from
the originating source set in every run (§9.2).

**Vector 69** — publish order. Plan contains every released package.

→ `core`, `api`, `cli`, `ui`, `@acme/theme`, `docs-site`. Dependencies precede dependents; ready sets are ordered
byte-wise by name, and `@acme/theme` precedes `docs-site` because `@` (0x40) sorts below `d` (§19.2). `docs-site`
takes its place in the order like any other package — its private registry does not remove it (§13.10a). Any
implementation emitting `ui` before `core`, or `@acme/theme` before `ui`, fails conformance (`E197`).

**Vector 70** — blocking. Same plan, `ui`'s publish fails.

→ published `core@1.5.0`, `api@1.2.1`, `cli@2.0.1`; failed `ui`; **blocked** `@acme/theme` (`W194`) — planned but never
attempted. Run exits non-zero. On resume: `ui@0.9.2` then `@acme/theme@1.0.1`, both at the versions planned in run 1
(§13.7c G3). Run 3 is empty. Note that `cli` and `api` publish normally in run 1: an unrelated subtree is not punished
for `ui`'s failure.

**Vector 71** — `cancel` on the consumer. `C1`: `feat(core)^: x`; `C2`: `cancel(cli): reset release state`. `core`
published at run 1.

→ Run 2 plans `ui` and `api` only. `cli`'s pending propagated contribution is discarded by §13.5a; it is not released
and does not accumulate.

**Vector 72** — `cancel` on the provider, after the provider released. `C1`: `feat(core)^: x`; `C2`:
`cancel(core): reset release state`. `core@1.5.0` published at run 1.

→ Run 2 plans `cli`, `ui`, `api` — the catch-up proceeds. The `cancel` is a no-op for `core` (`W170`): there is nothing
pending left to discard, and cancellation never reaches a published release (§10.3, §13.4a). To drop the consumer's
release, cancel the consumer, as in vector 71.

**Vector 73** — `cancel` on the provider, before it released. Same two commits, nothing published.

→ `core` is not released and propagates to nothing. The unit was still pending for `core`, so `core` is removed from its
source set (§13.4a). Contrast with vector 72: the same `cancel` text, opposite outcome, decided solely by whether the
provider had already released.

**Vector 74** — held consumer. `C1`: `feat(core)^: x`; `C2`: `release(cli): hold «Release-As: none»`. `core@1.5.0`
published at run 1.

→ `cli` is stale but **held**: `W154` reporting the withheld `2.0.1`, no release, on every run. After `C3`
(`release(cli): resume «Release-As: auto»`), `cli` releases `2.0.1`. The catch-up survived the hold intact (§13.7c G2).

**Vector 75** — direct beats catch-up. `C1` contains `feat(core)^: x` and `feat(cli): y`. `core@1.5.0` published at run

1.

→ Run 2: `cli` = `2.1.0`, a **minor**, not the propagated patch. `effective = max(direct, propagated)` is unchanged by
catch-up (§9.1).

**Vector 76** — accumulated staleness. Five commits, `core` released after every one, `cli` never released. Each commit
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
propagation — an implementation producing `cli@2.0.5`, or five separate `cli` releases, fails this vector.

**Vector 77** — private packages converge. Any plan reaching `docs-site`.

→ `docs-site` is released at `0.1.0`, published to the internal registry, and **tagged** `docs-site@0.1.0`. The next
run's plan is empty. An implementation that versions it without tagging it fails this vector: `docs-site` would reappear
in every subsequent plan for ever, and `E199` (§19.6) would fire on every run.

**Vector 77a** — a package with target `none`. Same, with `publishTargets: { "docs-site": "none" }`.

→ Released, tagged `docs-site@0.1.0`, manifest written, **no artefact uploaded**. It converges identically. Tagging is a
function of release, not of publication.

**Vector 78** — publish succeeded, tag write failed. `core` is published to the registry at `1.5.0`; the tag push fails;
the run is re-run.

→ `core` is still in the plan at `1.5.0`; the registry rejects the republish as an existing version. Identity verified
against this run's artefact → tag written, `W196`, run continues. Identity **not** verifiable → `E198`, run stops, no
tag written. An implementation that unconditionally tags on "version already exists" fails this vector.

**Vector 79** — optional dependency fails. `docs-site → ui` declared under `optionalDependencies`, `ui`'s publish fails.

→ `docs-site` is **not** blocked: an optional dependency is installable in its absence (§19.3). Publish *order* still
placed `ui` first (`publish.orderKinds`), but failure does not propagate over that edge (`publish.blockingKinds`).

**Vector 80** — nothing published. Every package in the plan fails.

→ Not a resumable partial failure. The run made no progress, so retrying cannot make any either; the engine MUST fail
loudly rather than report a partial success (§13.7c G6).

**Vector 80a** — ordering and blocking through a package that is not in the plan. Commit `C1`:
`feat(core)^^: x` with `Propagate-Scope: -ui`, so `ui` is excluded from the plan while `core` and `@acme/theme` — which
reaches `core` at depth 2 *through* `ui` — are both in it.

→ The publish order MUST still place `core` before `@acme/theme`, and if `core`'s publish fails `@acme/theme` MUST be
**blocked** (`W194`). Two distinct implementation errors produce a wrong answer here, and both are easy to make:

* ordering the publish over the subgraph induced on the plan — with `ui` absent there is no edge between `core` and
  `@acme/theme` at all, they become mutually unordered, and `@acme/theme` may publish first;
* computing the blocking closure over direct parents in the plan — `@acme/theme`'s only dependency is `ui`, which is not
  in the plan, so the chain to `core` breaks and it publishes against an unpublished `core`.

Compute both over the full workspace graph and filter to the plan afterwards (§19.2, §19.3). This is not an exotic
configuration: any package that merely has no bump in this run sits in exactly the position `ui` occupies here, which
makes this the most commonly hit vector in B.7.

**Vector 81** — suppressing a catch-up from the consumer. `C1`: `feat(core)^: x`; run 1 publishes `core@1.5.0` and fails
on `cli`. Then a new commit `C2` lands.

| `C2`                                | Run 2 plan for `cli`                                                    |
|-------------------------------------|-------------------------------------------------------------------------|
| *(nothing)*                         | `2.0.1`, `W193` catch-up                                                |
| `cancel(cli): reset release state`  | **not released**; the contribution is discarded (§13.5a)                |
| `release(cli)` + `Release-As: none` | **not released**; `W154` reporting the withheld `2.0.1`                 |
| `release(cli)` + `Release-As: auto` | `2.0.1` — no active hold, so `W158` and an ordinary catch-up            |
| `fix(cli): y`                       | `2.0.1` — `max(patch, patch)`; one release, not two                     |
| `fix(cli)!: y`                      | `3.0.0` — `max(major, patch)`; `HEAD` moved, so G3 does not pin `2.0.1` |
| `cancel(*): reset release state`    | **not released**, and neither is anything else pending                  |

In every row `ui` and `api` — which published successfully in run 1 — stay out of the plan. Suppressing one consumer's
catch-up MUST NOT disturb its siblings.

**Vector 82** — acting on the provider instead, after it has published. Same `C1` and same failed run 1.

| `C2`                                 | Run 2 plan for `cli`                                       |
|--------------------------------------|------------------------------------------------------------|
| `cancel(core): reset release state`  | `2.0.1` — still catches up. `W170`: nothing to discard     |
| `release(core)` + `Release-As: none` | `2.0.1` — still catches up; `core` is held for future work |

Both rows are the same rule: suppression reaches only **undischarged** work (§13.4a). `core@1.5.0` is public, so the
obligation it created for `cli` stands. An implementation that strands `cli` in the second row but not the first has
treated a hold as stronger than a `cancel`, inverting the ladder of §7.3, and fails conformance.

**Vector 82a** — the same hold, but on work the provider has **not** released. `C1`: `feat(core)^: x`; `C2`:
`release(core)` + `Release-As: none`; nothing published yet.

→ `core` is held and `cli` is **not** bumped: this work is undischarged, so the hold does suppress it. Together with
vector 82 this pins the boundary exactly at `discharged(P, C)`.

**Vector 82b** — a held provider with both discharged and undischarged work. `C1`: `feat(core)^: x` (published in run
1); `C2`: `release(core)` + `Release-As: none`; `C3`: `feat(core)^minor: y`.

→ `cli` receives `patch` — from `C1`, which `core` published — and **not** `minor` from `C3`, which it has not. One
package, one hold, two opposite answers, decided per unit by whether `core` released it.

### B.7a Optimisation equivalence

These exist because §13.11 permits a conforming implementation to compute the plan by a faster route. Each is a case
where a plausible transformation of §9.2 or §13.8 changes the answer. An implementation that has not applied the
optimisation passes them trivially; one that has must still pass them.

**Vector 82c** — hoisting `resolvableBy` must not collapse a mixed source set. `core` on `stable` and `legacy` on
`beta`, both in one unit's scope-set: `feat(core,legacy)^: x`. `cli → core`, and a package `old → legacy` where `old` is
on `beta`.

→ Both `cli` and `old` are bumped. `srcChannels` is `{stable, beta}`; `cli` is admitted by the `stable` member and `old`
by the `beta` member. An implementation that hoists by picking a single representative channel from the source set — the
first, or the origin's — instead of the whole set, drops one of the two and fails here. The hoisted form of §9.3a is a
set, and the set may have more than one element.

**Vector 82d** — the hoist must be recomputed per unit, not per run. Two units in one commit, `feat(core)^: x` and
`feat(legacy)^: y`, with `core` on `stable` and `legacy` on `beta`, dependents as above.

→ Each unit is admitted against its own sources: `cli` from the first, `old` from the second. An implementation that
lifts `srcChannels` out of the *unit* loop as well as the target loop — computing one set for the whole run — admits
`old` from `core`'s unit and fails §9.2's per-unit property.

**Vector 82e** — inverting `resolveChannels` must preserve §11.6 order. `cli` named by two commits, the older
`release(cli)%rc` and the newer `release(cli)%beta`, both in `W(cli)`.

→ `cli` takes `beta`; `W186` is raised because two candidates proposed. An implementation that builds the candidate list
by pushing from units in commit order and then reads it front-to-back takes `rc` and fails. The push MUST be in §11.6
order, or the read MUST sort (§13.8).

**Vector 82f** — the `W186` count is over **proposals**, not over candidates. `ui` is on `beta` and is named by two
commits in `W(ui)`: the newer `release(ui)%beta>stable`, and the older `release(ui)%rc>stable`.

→ `ui` graduates to `stable` from the newer directive, and there is **no** `W186`: the older directive is a candidate
but not a competitor, because `ui`'s baseline channel is `beta` and does not match its `<from>` of `rc` (§11.6). The
pair 82e/82f is what pins the counting rule — same shape, winner first in both, and the diagnostic differs only because
the trailing candidate proposes in one and not the other. An implementation that counts candidates rather than proposals
emits `W186` in both; one that counts only the prefix examined before the winner emits it in neither.

**Vector 82g** — skipping the channel pass must be observationally equivalent. Any workspace where no unit in the union
window sets `Propagate-Channel-Depth` above `0` and `propagation.channelDepth` is `0`.

→ Identical plan whether phase 1 runs or is skipped, because §13.8 then assigns every package its baseline channel and
§9.3a reads those baselines. An implementation whose skip path also skips §13.8 — leaving `channel(P)` unset rather than
set to the baseline — suppresses every propagated bump under §9.3a and produces an empty plan.

### B.8 Workspace graph constraints

**Vector 100** — a dependency cycle. Add `alpha` and `beta`, each listing the other under `dependencies`.

→ `E200`, repository-scoped, naming both packages and the field carrying each edge. The run aborts at §13.1, before any
plan is computed. Nothing is published, and the diagnostic is identical whether or not either package has a bump.

**Vector 100a** — the same two packages, but the mutual edges are under `devDependencies`.

→ No error. Those edges are in neither `propagation.kinds` nor `publish.orderKinds`, so they are not part of the graph
this rule constrains, and a test fixture depending back on the package it exercises stays legal.

**Vector 100b** — `alpha → beta → gamma → alpha`, a three-package cycle where only `gamma` has a bump.

→ `E200`. Acyclicity is a property of the workspace read at `HEAD`, not of the plan, so a cycle that this run would not
have touched still aborts it.

### B.9 The channel axis — `%%`, `++`, and transitions

Workspace of B.1 unless stated otherwise. Recall that `Propagate-Channel-Depth` defaults to `0`, so **no channel
propagates unless the unit says so**, and that `Propagate-Channel` defaults to `inherit`, so `++N` alone carries the
origin's own channel.

**Vector 94** — propagating a prerelease from a stable origin. `feat(core)^%%beta: x`.

→ `core` releases `1.5.0` on **stable** — its own channel is untouched by `%%`. Its direct dependents enter the beta
line and take the propagated patch: `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`, `api@1.2.1-beta.0`. `@acme/theme` and
`docs-site` are at depth 2 on both axes and are untouched. This is the case the operator exists for: ship the
dependency, let consumers validate the integration on a prerelease first.

**Vector 94a** — the same without the caret. `feat(core)%%beta: x`.

→ `core@1.5.0` stable; the three direct dependents move onto beta with **no** propagated bump, so each is a channel-only
release (`W202`) versioned by the channel-entry patch (`W204`): `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`,
`api@1.2.1-beta.0`. The versions coincide with vector 94 here because a propagated `patch` and a channel-entry `patch`
are the same size; they diverge as soon as the unit propagates anything larger.

**Vector 95** — a prerelease that keeps to itself. `feat(core)^%beta: x`.

→ **`core@1.5.0-beta.0` and nothing else.** The caret reaches `cli`, `ui` and `api`; every one of them is suppressed by
§9.3a and reported as `W208`, because a package on `stable` cannot resolve `core@1.5.0-beta.0` and republishing it would
produce an artefact identical to the one already published. This is the single most important vector in this section: an
implementation that releases the three dependents here has not implemented §9.3a, and will publish stable packages whose
manifests declare a range on a prerelease.

**Vector 95a** — taking the consumers along. `feat(core)^%beta++1: x`.

→ `core@1.5.0-beta.0`, `cli@2.0.1-beta.0`, `ui@0.9.2-beta.0`, `api@1.2.1-beta.0`. The channel axis puts them on the beta
line, so §9.3a admits the bump, so there is no `W208` and no `W204`. Compare vector 95: one four-character token is the
whole difference, and it is written in the commit.

**Vector 95b** — an established train needs no directives. Baselines `core@1.5.0-beta.0`, `cli@2.0.1-beta.0`; commit
`fix(core)^: x`.

→ `core@1.5.0-beta.1`, `cli@2.0.1-beta.1`. No channel directive appears anywhere: each package's channel comes from its
own baseline (§11.1), and `cli` is on `beta`, so §9.3a admits the bump. `ui` and `api` are on stable and are suppressed
with `W208`. Directives are needed at the boundaries of a train, not inside it.

**Vector 96** — the reverse. `feat(core)^%beta%%stable: x`.

→ `core@1.5.0-beta.0` and nothing else. `%%stable` proposes `stable` for three dependents that are already on `stable`,
so each is `W199` and nothing changes; the caret is then suppressed by §9.3a with `W208` exactly as in vector 95. Under
a specification where a propagated channel forced a release, this header published stable packages depending on a
prerelease; it now cannot.

**Vector 97** — a propagated `stable` MUST NOT graduate. Let `api` be at `1.2.1-rc.0` with stable baseline `api@1.2.0`.
Commit: `feat(core)^%%stable: x`.

→ `api` is **not** graduated. It keeps channel `rc`, and `W200` reports the suppression. `cli` and `ui`, both on stable
already, get `W199` for the redundant channel and take ordinary stable patches from the caret. An implementation that
graduates `api` here has ended a prerelease train on behalf of a commit that never mentioned it, and fails this vector.

**Vector 97a** — the same, with the `stable` arriving by inheritance rather than by `%%`: `feat(core)++1: x`, where
`core` is on stable and `propagation.channel` is `inherit`.

→ Identical outcome and identical `W200`. The prohibition is on the *propagated value*, not on the syntax that produced
it.

**Vector 97b** — the deliberate exception. Same baselines; commit `release(core)%%rc>stable++1: x`.

→ `api` **is** graduated, to `1.2.1`. The transition names the train it ends, which is the whole basis on which §9.3
permits it. `cli` and `ui` are on `stable` and do not match `<from>`, so they are untouched — no `W199`, no `W185`, no
release. Note that the unit's type is `release`, whose bump is `none`: the channel axis does not require a bump, which
is what makes this shape possible at all (§7.2).

**Vector 97c** — idempotence. Run vector 97b again, at the same `HEAD`, after it succeeded.

→ Empty plan. `api`'s baseline is now `1.2.1`, so `channelOf(baseline(api))` is `stable` and it no longer matches the
`<from>` of `rc`. The commit is still inside `W(api)` — the window is measured from the last stable tag and `api` has
just written one, so in fact it is not; but even where a package's window still contains the commit, `W199` and the
`<from>` test are what terminate the axis (§13.7c G7). An implementation that matches transitions against the channel it
computed earlier in the same run re-releases `api` on every run for ever.

**Vector 97d** — partial graduation. `cli` at `2.1.0-beta.4`, `ui` at `0.9.2-beta.1`, `api` already graduated to
`1.2.1`. Commit: `release(core)%stable%%beta>stable++*`.

→ `core` graduates directly; `cli` and `ui` graduate by transition; `api` is on `stable`, does not match, and is not
touched — no error, no redundant release, and no need for the author to know which packages had already been done.
`@acme/theme` and `docs-site` are on stable too and are likewise untouched. This is the case the transition form exists
for.

**Vector 97e** — excluding a package from graduation. Same as 97d plus:

```
Propagate-Channel-Scope: *, -ui
```

→ `core` and `cli` graduate. `ui` stays on `beta` at `0.9.2-beta.1`, unreleased, and will graduate whenever a later
directive names it. Exclusion leaves a package on its line: it does not release, it is not an error, and it produces no
diagnostic beyond the plan simply not containing it.

**Vector 97f** — excluding from the unit's own packages. `release(@acme/*,-@acme/theme)%beta>stable`.

→ Only the matching `@acme/*` packages other than `@acme/theme` are considered at all. The scope-set excludes from the
unit; `Propagate-Channel-Scope` excludes from what the unit reaches (§8.5a). Both use the same `-` operator and the same
scope-set grammar.

**Vector 98** — redundancy. `feat(core)^%%stable: x` where every dependent is already on stable.

→ Ordinary stable patches from the caret, plus `W199` on each redundant channel proposal.

**Vector 98a** — graduating a consumer while its provider stays on a train. `core` at `1.5.0-beta.2` with no directive;
commit `release(cli)%beta>stable`.

→ `cli` graduates. Its manifest is reconciled against `core`'s current version, which is a prerelease, so the published
stable `cli` declares a range admitting `core@1.5.0-beta.2` and `W203` is raised naming both (§9.4). Permitted,
reported, and almost always a mistake — graduate `core` too.

**Vector 99** — parsing.

| #   | Header                                                      | Expected                                                                                                    |
|-----|-------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------|
| 99a | `feat(core)%%beta: x`                                       | `Propagate-Channel: beta`, `Propagate-Channel-Depth: 1`; own channel untouched                              |
| 99b | `feat(core)%beta%%rc: x`                                    | `Channel: beta` **and** `Propagate-Channel: rc` — distinct sigils                                           |
| 99c | `feat(core)%%: x`                                           | `E111` — a bare `%%` has no meaning, unlike `^^`                                                            |
| 99d | `feat(core)%%beta%%rc: x`                                   | `E110` — one `%%` per header                                                                                |
| 99e | `feat(core)%%%beta: x`                                      | `E110` — third percent sign                                                                                      |
| 99f | `feat(core)%%Beta: x`                                       | `E181` — propagated channel names obey §11.2                                                                |
| 99g | `feat(core)%%latest: x`                                     | `E180` — reserved                                                                                           |
| 99h | `feat(core)%%beta: x` + footer `Propagate-Channel: rc`      | `E112`; lenient mode takes the footer with `W112`                                                           |
| 99i | `docs(core)%%beta++1: x`                                    | Channel propagates; the type maps to `none`, so no bump does. Dependents are channel-only releases (`W202`) |
| 99j | `feat(core)++1: x` + footer `Propagate-Channel-Depth: 3`    | `E112` — `++1` and the footer set one key to different values; lenient: footer wins, `W112`                 |
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
| 85  | `feat(core)^minor: x` with a footer `Propagate: major`                                  | `E112` — inline and footer set one key to different values (§5.3).                                                                                         |
| 85a | the same under `lenient: true`                                                          | Accepted, footer wins, `W112`.                                                                                                                             |
| 85b | `feat(core)^minor: x` with a footer `Propagate: minor`                                  | Accepted, `W110` — redundant restatement, not a conflict.                                                                                                  |
| 85c | `feat(core)^: x` with a footer `Propagate-Depth: 3`                                     | `E112` — `^` sets `Propagate-Depth: 1`; the chain of §8.3 does not resolve it.                                                                             |
| 85d | the same under `lenient: true`                                                          | Accepted, depth `3`, `W112` — the footer wins, never the inline (§8.3).                                                                                    |
| 86  | `frobnicate(core): x` with `strictTypes: false`                                         | Bump `none`, `W140`.                                                                                                                                       |
| 86a | the same with `strictTypes: true`                                                       | `E140`.                                                                                                                                                    |
| 87  | `Feat(core): x` with `lenient: false`                                                   | `E101`.                                                                                                                                                    |
| 87a | the same with `lenient: true`                                                           | Type lowercased, `W101`.                                                                                                                                   |
| 87b | `feat(core):x` with `lenient: true`                                                     | Accepted, description `x`, `W121` — the lenient form of `E120` (§5.5).                                                                                     |
| 87c | `feat(core):  x` with `lenient: true`                                                   | `E120` still — two spaces are not recoverable, lenient or not.                                                                                             |
| 88  | `release(core): x` + `Release-As: 5.0.0`, computed `1.5.0`, **no configuration at all** | `E157` — `maxMajorJump` defaults to `1` and is enforced by default (§14.1); the pin exceeds the computed version by more than one major.                   |
| 88c | the same with `maxMajorJump: null`                                                      | Accepted at `5.0.0` — the bound is the one default-enforced limit that may be disabled.                                                                    |
| 88a | the same with `Release-As: 2.0.0`                                                       | Accepted — one major above the computed version is within the bound.                                                                                       |
| 88b | `release(core): x` + `Release-As: 1.4.0`, computed `2.0.0`, `lenient: true`             | Accepted at `1.4.0`, `W159` — the lenient form of `E156` (§8.6).                                                                                           |
| 89  | `feat(core)%latest: x`                                                                  | `E180` — `latest` is reserved (§11.2).                                                                                                                     |
| 89a | `feat(core)%Beta: x`                                                                    | `E181` — channel names are lowercase.                                                                                                                      |
| 90  | `feat(core): x` with a footer `X-Internal-Ticket: AB-1`                                 | Footer ignored, `W150`. Unknown keys never block (§17.3).                                                                                                  |
| 91  | `feat(core)%%beta: x`, then a later `feat(core)%%rc: y`, both in `cli`'s window         | Newest commit wins — `cli` enters `rc`; `W160` naming both. Note `%%`, not `%`: `%` sets the unit's **own** channel and never reaches a dependent (§8.3a). |
| 92  | One commit containing `cancel(core)` and `feat(core)` (either order)                    | `W172`, **non-suppressible**: the `feat` is discarded by ancestor-or-self and `core` is not released (§10.3, D.6).                                         |
| 93  | `release(core)%stable: x`, baseline `1.5.0-beta.2`, no pending bumps                    | `E185`, **repository-scoped** — the run aborts; graduation would not raise the version (§11.5, §16).                                                       |
| 93a | `feat(core)^%beta: x`, dependents on stable                                             | `W208` per suppressed dependent, **non-suppressible** — the caret reached and could not oblige (§9.3a).                                                    |
| 93b | `feat(core)%%beta: x`, dependents on stable                                             | `W202` per dependent, **non-suppressible**, plus `W204` for each channel-entry patch.                                                                      |
| 93c | `feat(core)%%beta++0: x`                                                                | `W201` and nothing else — a channel value with depth `0` reaches nobody; `W152` is superseded (§8.3b).                                                     |
| 93h | `feat(core)^minor+0: x`                                                                 | `W201` and nothing else — the bump-axis mirror of 93c.                                                                                                     |
| 93d | `feat(core)%%beta++*: x` with `Propagate-Channel-Scope: -*`                             | `W205` — the channel scope excluded every reached dependent.                                                                                               |
| 93e | `release(core)%%zeta>stable++*: x`, no dependent on `zeta`                              | `W206` — the transition matched nothing; the usual cause is a mistyped `<from>`.                                                                           |
| 93f | `release(core)%beta>beta: x`                                                            | `W207`, inert.                                                                                                                                             |
| 93g | `release(cli)%beta>stable: x` while `core` stays at `1.5.0-beta.2`                      | Graduates `cli`; `W203` naming `cli@2.1.0` and `core@1.5.0-beta.2` (§9.4).                                                                                 |

Vector 92 is the one to implement first of these: `W172` is non-suppressible precisely because the commit looks like it
does something and does nothing, and it is the most likely authoring mistake with `cancel`.

---

## 23. Appendix C — Formal grammar (ABNF)

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

tag             = package-name "@" semver    ; split at the LAST "@"

LOWER           = %x61-7A
```

---

## 24. Appendix D — Worked examples

### D.1 Ordinary feature with controlled blast radius

```
feat(@acme/core)^: add streaming reader

The buffered reader is retained; the streaming path is opt-in via
`createReader({ stream: true })`.
```

`@acme/core` gets a minor bump; its **direct** consumers get a patch, because `^` sets depth `1` and `Propagate`
defaults to `patch`. Nothing further down the graph moves.

The single caret is the whole decision, and it is worth being deliberate about. Without it the commit releases
`@acme/core` alone — correct when consumers declare a compatible range and will pick the new version up on their next
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

`^^inherit` and `^inherit+*` are the same directive. The doubled form is preferred in a header this dense — it is two
characters shorter and keeps the depth idea attached to the propagation idea instead of trailing after it.

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
| 2      | — no release; `cli → core`, so a `cli` fix does not reach `core`             | `2.1.0-beta.1`                                            |
| 3      | `2.0.0-beta.0` — target recomputed from `1.4.2` with a `major` in the window | `2.1.0-beta.2` — propagated `patch`, target still `2.1.0` |
| 4      | `2.0.0`                                                                      | `2.1.0`                                                   |

Three things in that sequence are worth reading carefully.

**Commit 1 needs `++1`, and would release `core` alone without it.** `%beta` puts `core` on the prerelease line; the
caret reaches `cli`; and §9.3a then suppresses the bump, because a `cli` still on `stable` cannot resolve
`core@1.5.0-beta.0`. `++1` moves `cli` onto the line in the same commit, and the suppression no longer applies. This is
the boundary of the train and it is the one place a channel directive is needed on the way in. (`cli` is also named
directly in the scope-set here, so it would have entered the line anyway; the `++1` is what carries any *other* direct
consumer along, and is written for that reason.)

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
was graduated a fortnight ago to unblock a downstream team, and `@acme/legacy-adapter` — which also depends on `core` —
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
publish — the transition stops matching the ones that did (§13.7c G7).

Two mistakes this shape avoids. Writing `%%stable` instead of the transition would have graduated nothing at all:
`W200` suppresses every implicit graduation, precisely so that a directive aimed at one package cannot end another
package's train (§9.3). Writing `%stable` on a hand-maintained scope-set — `release(@acme/core,@acme/cli,@acme/theme)`
— would work today and be wrong next week, because the set that is still on `beta` changes on every run.

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

**Correct form — two commits:**

```
# commit N  (empty commit)
cancel(@acme/core): reset release state

# commit N+1
fix(@acme/core): refactor internals

Restating the change correctly. The metadata from 4f2a1c9 is cancelled
by the preceding commit.
```

Result: `@acme/core` gets a patch, not a major. No history was rewritten and no tag was touched.

**The mistake to avoid — one commit:**

```
cancel(@acme/core): reset release state

---

fix(@acme/core): refactor internals
```

The barrier is *ancestor- **or-self***, so the `fix` is in the barrier commit and is discarded along with everything
before it — the package is not released at all. Unit order within the commit is irrelevant.

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

Every package except the two internal ones publishes a patch. No propagation directive is needed — and none should be
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
run 1  —  plan (publish order)
  core          1.4.2 -> 1.5.0   direct
  api           1.2.0 -> 1.3.0   propagated from core
  cli           2.0.0 -> 2.1.0   propagated from core
  ui            0.9.1 -> 0.9.2   propagated from core   (minor -> patch, 0.y.z)
  @acme/theme   1.0.0 -> 1.1.0   propagated from core

run 1  —  result
  ✓ core@1.5.0        published, tagged
  ✓ api@1.3.0         published, tagged
  ✓ cli@2.1.0         published, tagged
  ✗ ui                publish failed: 403 from registry
  ⊘ @acme/theme       W194 blocked: dependency ui failed
  exit 1
```

Someone fixes the token and re-runs. Nothing else has changed; `HEAD` is the same commit.

```
run 2  —  plan (publish order)
  ui            0.9.1 -> 0.9.2   W193 catch-up from core@1.5.0
  @acme/theme   1.0.0 -> 1.1.0   W193 catch-up from core@1.5.0

run 2  —  result
  ✓ ui@0.9.2          published, tagged
  ✓ @acme/theme@1.1.0 published, tagged
  convergence check: plan empty                                  §19.6
  exit 0
```

Four things in this output are guarantees rather than coincidences, and each is worth recognising when reading a real
plan:

* **The three successes are gone from run 2.** Their tags moved `stableCommit` forward, so their windows no longer
  contain the commit (§13.6, G4). They cannot be republished by a retry.
* **The two failures are still there, at the same version numbers.** `ui` is `0.9.2` in both runs, not `0.9.3` — no tag
  was written for it, so its baseline never moved (G3). Nobody needs to re-review the numbers.
* **`@acme/theme` is at depth 2 from `core`, and it is still at depth 2.** It was admitted in run 1 by `^^`, and it is
  admitted in run 2 by the same traversal from the same source. It is *not* re-derived at depth 1 from the republishing
  `ui`, which would be a different — and wider — release (G5).
* **The catch-up marker names `core@1.5.0`, a version published by a previous run.** That is what `W193` is for: a
  package appearing in a plan with no commits of its own and no releasing dependency is otherwise unexplainable to
  whoever reviews it.

Had `HEAD` moved between the runs — someone merged a `fix(ui)` — run 2 would plan `ui` at `0.9.2` still, since `max()`
of a propagated patch and a direct patch is a patch, and the changelog would carry both entries. G3 fixes the version
only against a fixed `HEAD`; new commits legitimately change the outcome.