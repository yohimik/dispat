# Conventional Commits — Monorepo Extension (CCME)

**Version:** 1.0.0 **Status:** Normative specification **Extends:** Conventional Commits 1.0.0 **Versioning model:**
Semantic Versioning 2.0.0 **Version store:** git tags of the form `<package>@<version>`

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
17. [Parsing without regular expressions](#17-parsing-without-regular-expressions)
18. [Appendix A — Regular expressions](#18-appendix-a--regular-expressions)
19. [Appendix B — Conformance test vectors](#19-appendix-b--conformance-test-vectors)
20. [Appendix C — Formal grammar (ABNF)](#20-appendix-c--formal-grammar-abnf)
21. [Appendix D — Worked examples](#21-appendix-d--worked-examples)

---

## 1. Summary

CCME adds five capabilities to Conventional Commits, chosen so that a single commit can fully describe its release
intent across a workspace of many packages:

| # | Capability                                                                                                     | Syntax                                                    |
|---|----------------------------------------------------------------------------------------------------------------|-----------------------------------------------------------|
| 1 | **Multiple units per commit** — one commit message describes several independent changes                       | blocks separated by `---`                                 |
| 2 | **Dependent propagation** — declare whether and how far a change bumps its consumers                           | `^minor+2`, `^^minor` / `Propagate:` + `Propagate-Depth:` |
| 3 | **Cancellation** — discard accumulated, unreleased release metadata without touching code                      | type `cancel`                                             |
| 4 | **Explicit or derived targeting** — scope names a package, or is omitted to derive packages from changed files | `feat(api,web)` / `feat`                                  |
| 5 | **Prerelease channels** — enter, iterate on, and graduate from a prerelease line                               | `@beta` / `release(api)@stable`                           |

Everything is designed so that a conforming parser can be written with a linear index scan and no regular-expression
engine (§17). Appendix A gives regular expressions for implementers who prefer them.

**Example carrying all five:**

```
feat(@acme/core)^^minor@beta: streaming reader

Replaces the buffered reader with an incremental one.

Propagate-Kind: dependencies, peerDependencies

---

fix(@acme/cli): correct exit code on SIGINT

---

cancel(@acme/legacy-adapter): reset release state
```

---

## 2. Conventions and terminology

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHOULD**, **SHOULD NOT**, **MAY**, and **OPTIONAL** are
to be interpreted as described in RFC 2119.

| Term                     | Definition                                                                                                            |
|--------------------------|-----------------------------------------------------------------------------------------------------------------------|
| **Workspace**            | The repository, containing one or more packages.                                                                      |
| **Package**              | An independently versioned unit with a name and a root directory. Names are compared byte-for-byte, case-sensitively. |
| **Manifest**             | The file declaring a package's name and dependencies (`package.json`, `Cargo.toml`, `pyproject.toml`, …).             |
| **Graph**                | The directed graph of packages; an edge `A → B` means "A depends on B".                                               |
| **Dependent / consumer** | `A` is a dependent of `B` if there is an edge `A → B`.                                                                |
| **Depth**                | The number of edges traversed from a changed package to a dependent. Direct consumers are at depth 1.                 |
| **Unit**                 | One `<header>[body][footers]` block inside a commit message. A commit contains one or more units.                     |
| **Directive**            | A machine-readable instruction attached to a unit (propagation, channel, release override).                           |
| **Bump**                 | One of `none` \| `patch` \| `minor` \| `major`, ordered `none < patch < minor < major`.                               |
| **Release tag**          | A git tag `<package>@<version>` marking a published version.                                                          |
| **Baseline**             | The highest-precedence release tag for a package that is reachable from `HEAD`.                                       |
| **Stable baseline**      | The highest-precedence *non-prerelease* release tag reachable from `HEAD`.                                            |
| **Pending window**       | The set of commits used to compute a package's next version (§13.3).                                                  |
| **Release engine**       | The tool implementing this specification.                                                                             |
| **Inert**                | Syntactically valid but resolving to zero packages; produces a warning, never an error.                               |

`max(a, b)` over bumps returns the higher of the two in the ordering above.

---

## 3. Relationship to Conventional Commits 1.0.0

CCME is a **strict superset**. Every message valid under Conventional Commits 1.0.0 is valid under CCME and MUST produce
the same release outcome, with one clarification and one added default:

* **Clarification.** A message with no `---` separator is a single-unit message. The base spec's structure is the
  one-unit case of §4.
* **Added default.** When no scope is given, the base spec leaves the affected component unspecified. CCME defines it as
  the set of packages owning the commit's changed files (§6.2). In a single-package repository this set is always that
  package, so behaviour is unchanged.

Base-spec elements retained without modification: `type`, `(scope)`, `!`, `description`, body, footer/trailer form,
`BREAKING CHANGE:` and `BREAKING-CHANGE:` footers, and the `revert` convention.

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
  footer continuation (§17.5). Otherwise the unit has no footers and that paragraph is body text.
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
* A scope term MUST NOT contain: `(`, `)`, `,`, `:`, or whitespace. It MAY contain `@`, `/`, `.`, `*`, `-`, `+`, `^` and
  any other printable character.

Because `@` inside parentheses is an ordinary character, npm-style scoped names work unmodified: `feat(@acme/ui)`. The
`@` sigil for prerelease channels is only recognised **outside** the parentheses.

Scope-term forms:

| Form                     | Meaning                                                                                             |
|--------------------------|-----------------------------------------------------------------------------------------------------|
| `name`                   | Include the package named `name`.                                                                   |
| `-name`                  | Exclude the package named `name`.                                                                   |
| `pattern` containing `*` | Include every package whose name matches the glob. `*` matches any run of characters including `/`. |
| `-pattern`               | Exclude every matching package.                                                                     |
| `*`                      | Include every package in the workspace.                                                             |
| `global`                 | Reserved alias for `*`. Implementations MUST accept it.                                             |
| `.`                      | Include the file-derived set for this commit (§6.2).                                                |
| `-.`                     | Exclude the file-derived set.                                                                       |

Reserved-word collisions: a package literally named `global` is shadowed by the alias and can only be reached through a
glob that is not itself the bare alias — `globa*` works, `global` does not. A package literally named `*` or `.` cannot
be addressed at all. These are accepted as deliberate limitations; no package in any ecosystem this specification
targets uses those names.

### 5.3 `<inline-directives>`

OPTIONAL. A run of directive tokens, each introduced by a sigil, with no separators between them. Order is not
significant. Each sigil MAY appear at most once per header (`E110` on repeat).

| Sigil | Token                                              | Desugars to                                         |
|-------|----------------------------------------------------|-----------------------------------------------------|
| `^`   | `^none` `^patch` `^minor` `^major` `^inherit`      | `Propagate: <value>`                                |
| `^^`  | `^^`                                               | `Propagate-Depth: all`                              |
| `^^`  | `^^none` `^^patch` `^^minor` `^^major` `^^inherit` | `Propagate: <value>` **and** `Propagate-Depth: all` |
| `+`   | `+0` … `+N`, `+*`                                  | `Propagate-Depth: <N \| all>`                       |
| `@`   | `@<channel>`, `@stable`                            | `Channel: <channel>`                                |

Examples: `feat(api)^minor+2: …`, `fix^^inherit: …`, `feat(api)^^: …`, `feat(api)@beta!: …`.

`^`, `^^`, and `+` values are matched byte-for-byte against the enumerated words; abbreviations such as `^min` are
`E111`.

**The doubled caret.** `^^` means "propagate all the way down". It exists because `^<bump>+*` is by far the most common
two-token combination — a breaking or interface-level change whose consequences do not stop at depth 1 — and doubling
the sigil reads as intensification of the same idea. `^^minor` is exactly `^minor+*`; `^^` on its own is exactly `+*`,
leaving the bump at its default.

`^` and `^^` are the **same sigil** for the purposes of the once-per-header rule: `^minor^^major` is `E110`.

Because `^^` carries a depth, combining it with an explicit `+N` where `N` is not `all` is `E113` — two spellings of
depth disagreeing in one header. `^^minor+*` is legal but redundant and emits `W110`.

Inline directives are **exactly equivalent** to their footer forms. If a header and a footer both set the same key:

* identical values → accepted, `W110`;
* different values → `E112` (in lenient mode, the footer wins with `W112`).

### 5.4 `!`

OPTIONAL. Immediately precedes the colon. Marks the unit as a breaking change for its resolved package set. Equivalent
to a `BREAKING CHANGE:` footer on the same unit; both MAY be present.

### 5.5 `: ` and `<description>`

The prefix ends at the **first `:` that is not inside parentheses**. It MUST be followed by exactly one space, then a
non-empty description.

* Zero spaces or two or more spaces after the colon → `E120`.
* Empty description → `E121`.
* The description is the remainder of the line; it MAY contain further colons (`feat(api): fix: nested` has description
  `fix: nested`).
* The description SHOULD be imperative mood and SHOULD NOT end with a period. Neither is enforced.
* A description exceeding `maxDescriptionLength` (default 100 characters, counted in Unicode scalar values) → `W120`.

### 5.6 Header examples

| Header                                | Type       | Scopes                 | Inline               | Breaking |
|---------------------------------------|------------|------------------------|----------------------|----------|
| `feat: add retry`                     | `feat`     | derived                | —                    | no       |
| `fix(api): null guard`                | `fix`      | `api`                  | —                    | no       |
| `feat(api,web,@acme/ui): unify theme` | `feat`     | 3 packages             | —                    | no       |
| `feat(*,-docs-site): bump runtime`    | `feat`     | all but `docs-site`    | —                    | no       |
| `feat(.,-legacy): new codec`          | `feat`     | derived minus `legacy` | —                    | no       |
| `perf(core)^patch+1: faster hash`     | `perf`     | `core`                 | prop=patch depth=1   | no       |
| `refactor(core)^^major!: drop v1 API` | `refactor` | `core`                 | prop=major depth=all | yes      |
| `feat(core)^^: broad internal change` | `feat`     | `core`                 | prop=patch depth=all | no       |
| `feat(cli)@beta: experimental watch`  | `feat`     | `cli`                  | channel=beta         | no       |
| `release(cli)@stable: graduate 2.0`   | `release`  | `cli`                  | channel=stable       | no       |
| `cancel(*): reset release state`      | `cancel`   | all                    | —                    | n/a      |

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
            else if t == '*' or t == 'global':
                                    base |= workspace.allPackages
            else if t contains '*': base |= workspace.matching(t)
            else if t in workspace: base |= { t }
            else:                   raise E130(t)

    out = {}
    for t in excludes:
        if t == '.':            out |= derived(commit)
        else if t == '*' or t == 'global':
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
* Private/unpublishable packages remain in the graph for propagation purposes but MUST NOT receive tags; see §13.10.

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
* Paths owned by no package (repo-root config, CI files, shared tooling) contribute nothing, unless a `rootPathMap`
  entry maps a glob to an explicit package list (§14).
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
release(@acme/cli)@stable: graduate to 2.0.0
```

```
release(@acme/core,@acme/cli)@rc: move the release train to rc
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
* A `release` unit whose only effect would be `Channel: stable` triggers **graduation** (§11.4) even though its own bump
  is `none`.
* A `release` unit carrying a `Release-As` footer holds, resumes, or pins the version per §8.6.
* A `release` unit with no directive at all is inert (`W141`).

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

There is a third, weaker option that is often the one actually wanted: `Release-As: none` holds a release without
discarding anything (§8.6.1). The three form a ladder — **`revert`** undoes the code, **`Release-As: none`** defers the
release, **`cancel`** erases the metadata.

---

## 8. Directives: footers and inline shorthand

### 8.1 Footer registry

Footers are git trailers: `Key: value`, one per line, in the unit's final paragraph. Keys are matched
**case-insensitively**, but not hyphen-insensitively (`Propagate-Depth` and `propagate-depth` match; `PropagateDepth`
does not). **`BREAKING CHANGE` is the sole exception and is case-sensitive** — see §8.1.1.

| Footer                                | Inline      | Values                                                                                             | Default                                                | Scope                                       |
|---------------------------------------|-------------|----------------------------------------------------------------------------------------------------|--------------------------------------------------------|---------------------------------------------|
| `BREAKING CHANGE` / `BREAKING-CHANGE` | `!`         | free text                                                                                          | —                                                      | unit                                        |
| `Propagate`                           | `^x`        | `none` \| `patch` \| `minor` \| `major` \| `inherit`                                               | `patch`                                                | unit                                        |
| `Propagate-Depth`                     | `+N` / `+*` | non-negative integer \| `direct` \| `all`                                                          | `1` (`direct`)                                         | unit                                        |
| `Propagate-Kind`                      | —           | comma list of `dependencies`, `devDependencies`, `peerDependencies`, `optionalDependencies`, `all` | `dependencies, peerDependencies, optionalDependencies` | unit                                        |
| `Propagate-Scope`                     | —           | scope-set                                                                                          | `*`                                                    | unit                                        |
| `Propagate-Channel`                   | —           | `inherit` \| `stable` \| `<channel>`                                                               | `inherit`                                              | unit                                        |
| `Channel`                             | `@x`        | `<channel>` \| `stable`                                                                            | inherited from baseline                                | unit                                        |
| `Release-As`                          | —           | exact semver \| `patch` \| `minor` \| `major` \| `none` \| `auto`                                  | —                                                      | unit for bumps, package for the rest (§8.6) |
| `Reverts`                             | —           | commit sha                                                                                         | —                                                      | unit, informational                         |

Unknown footer keys are ignored with `W150` — this keeps CCME compatible with organisation-specific trailers.

A footer value MAY span multiple lines: a continuation line is any line in the footer block that is not itself a footer
start (§17.5). Multi-line values are only meaningful for `BREAKING CHANGE`; for other keys the continuation is joined
with a single space before parsing, and a resulting invalid value is `E151`.

### 8.1.1 `BREAKING CHANGE` — the exception to four rules

`BREAKING CHANGE` breaks more of this grammar's regularities than any other token, so its handling is collected here
rather than scattered.

**1. It is not a type.** Uppercase and containing a space, it fails §5.1 twice. A header line beginning with
`BREAKING CHANGE` is `E100` with a dedicated message (§5.1).

**2. It is the only footer key containing a space.** Every other key is `[A-Za-z0-9-]+`. The scanner therefore
special-cases the literal string before falling through to the generic key loop (§17.5). Implementations MUST NOT
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
`BREAKING CHANGE` footer consumes subsequent non-footer lines to the end of the block (§17.5). The value is never parsed
and never validated — an empty value is legal, though `W157` notes that a breaking change with no explanation is
unhelpful to consumers.

**Bump.** `major`, overriding whatever the type would have produced, subject to §12.6 for `0.y.z` packages. A
`BREAKING CHANGE` footer on a `cancel` unit is `E171`; on a `release` unit it is `E141`.

### 8.2 `Propagate`

Declares the bump given to **dependents** of this unit's packages.

* `none` — do not touch dependents.
* `patch` / `minor` / `major` — give every reached dependent exactly that bump.
* `inherit` — give every reached dependent the same bump this unit produces (`feat` → `minor`, `!` → `major`, …).

Default is `patch`: a dependent whose dependency changed must be re-published so that lockfiles and bundled artefacts
pick up the new code, but its own public API did not change, so `patch` is the honest signal.

### 8.3 `Propagate-Depth`

* `0` — no propagation. Equivalent to `Propagate: none`.
* `1` (or `direct`) — direct consumers only.
* `N` — up to N edges away.
* `all` — the full transitive closure of dependents.

**Default is `1` (direct consumers only).** Rationale: depth 1 is the smallest blast radius that is still correct for
the common case — a consumer whose dependency changed must be republished so its declared range and lockfile pick up the
new code. Beyond depth 1 the argument weakens: a depth-2 package is only stale if the depth-1 package *bundles* rather
than *declares* its dependency, which is a property of the build, not of the commit. Making `all` the default would
silently version the entire reverse-closure of every `feat` in a large workspace — an outcome the author cannot see from
the message they wrote. Deep propagation is therefore opt-in, and the author states it explicitly with `+N` or `+*`.

**Interaction with `^`.** `Propagate` and `Propagate-Depth` are independent keys with independent defaults. Writing
`^minor` alone sets the bump and leaves the depth at its default of `1`; adding `+N` overrides that `1`:

| Written     | Bump to dependents | Depth                                 |
|-------------|--------------------|---------------------------------------|
| *(nothing)* | `patch` (default)  | `1` (default)                         |
| `^minor`    | `minor`            | `1` (default)                         |
| `+3`        | `patch` (default)  | `3`                                   |
| `^minor+3`  | `minor`            | `3`                                   |
| `^minor+1`  | `minor`            | `1` — explicit, identical to `^minor` |
| `+*`        | `patch` (default)  | all levels                            |
| `^^`        | `patch` (default)  | all levels — identical to `+*`        |
| `^^minor`   | `minor`            | all levels — identical to `^minor+*`  |
| `^^minor+*` | `minor`            | all levels — legal, redundant, `W110` |
| `^^minor+2` | —                  | `E113`: `^^` and `+2` both set depth  |

Repositories that want the old transitive behaviour set `propagation.depth: "all"` in configuration (§14); an inline
`+N` still overrides the configured value. Precedence for depth is always: **inline `+N` → footer `Propagate-Depth` →
configured `propagation.depth` → spec default `1`.** The same precedence chain applies to every other directive key.

`^none+*` propagates nothing (the bump wins); `^minor+0` propagates nothing (the depth wins). Both are legal and mean
"no propagation"; `W152` is emitted for the redundant pairing.

### 8.4 `Propagate-Kind`

Selects which manifest dependency fields are traversed as graph edges for **this unit's** propagation.

`devDependencies` is excluded by default: a package that only uses another for its test suite does not need republishing
when that other package changes. `all` is shorthand for every field.

### 8.5 `Propagate-Scope`

Restricts propagation to a subset of the workspace. The reached-dependent set is intersected with the resolved scope-set
of this footer before bumps are applied.

```
feat(@acme/core)^^minor: new plugin API
Propagate-Scope: @acme/*, -@acme/experimental-*
```

Useful when a workspace contains both published packages and internal apps that must not be versioned.

### 8.6 `Release-As`

Overrides computation for this unit's packages. The four values do not all operate at the same level, and the
distinction is load-bearing:

| Value                         | Applies to                   | Meaning                                                                                                                         |
|-------------------------------|------------------------------|---------------------------------------------------------------------------------------------------------------------------------|
| `patch` \| `minor` \| `major` | **this unit**                | Replace this unit's own bump, regardless of its type. Other units for the same package still contribute; `max()` still applies. |
| `4.0.0` (exact semver)        | **the package, this window** | Publish exactly this version. MUST be strictly greater than the baseline (`E153`).                                              |
| `none`                        | **the package, this window** | **Hold.** Do not release these packages in this window. Pending units are *retained*, not discarded.                            |
| `auto`                        | **the package, this window** | **Resume.** Lift an active hold and return to normal computation.                                                               |

The unit-level and package-level values share one key because they answer the same question — "what should this
become?" — at two granularities. A bump value describes a *change*; a version, a hold, or a resume describes a
*release*.

`Release-As` with an exact version applied to a scope-set of more than one package is `E154` — two packages cannot both
become `4.0.0` unless they happen to share a baseline, and allowing it invites accidents. Use one `release` unit per
package.

**Precedence for the package-level values.** For each package, consider every surviving unit in the window carrying
`none`, `auto`, or an exact version whose resolved scope includes that package. The directive from the **newest commit**
wins; within a commit, the **last unit** wins. `W153` is emitted when this discards a competing directive. Unit-level
bump values do not participate; they are local to their unit.

This one rule gives the hold/resume sequence its behaviour for free — no separate state machine is needed, because "is
this package held?" is answered by looking at a single winning directive rather than by replaying history.

### 8.6.1 Holds

`Release-As: none` is a **pause on publishing**, not an erasure of history.

```
release(@acme/core): hold pending disclosure

Embargoed until the coordinated disclosure on the 14th.

Release-As: none
```

While a package is held:

* it is excluded from the release plan (`W154`);
* it is **not** a propagation source — its dependents are not bumped on its behalf, because publishing `cli` against an
  unpublished `core@1.5.0` would produce a broken artefact;
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

So that a named version does not have to be derived by hand, implementations MUST include the version the package
*would* have received in the `W154` diagnostic and in the release plan, so it can be read off the previous run's output.

Guard-rail: an exact `Release-As` that is **lower than the computed version** is `E156`. Naming `1.5.0` when a breaking
change has landed and `2.0.0` was computed would publish an incompatible release under a compatible number — the one
mistake the named-version form invites. `auto` cannot make this mistake, which is why it is the default choice. Lenient
mode downgrades `E156` to a warning.

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

Propagation is a bump assigned to a package *because one of its dependencies changed*, as opposed to a **direct** bump
assigned because the package itself changed.

The effective bump for a package is:

```
effective(P) = max( direct(P), propagated(P) )
```

This single rule delivers the intended behaviour: a package that has its own `feat` in the window is unaffected by an
incoming `patch` propagation, while a package with no changes of its own receives the propagated bump. Propagation can
only ever raise a version, never lower or replace a direct decision.

### 9.2 Computation

Propagation is computed after all direct bumps are known, after cancellation (§10) has removed discarded units, and
after holds (§13.6a) are resolved.

```
propagate(units, graph, held):
    prop = {}                                  # package -> bump
    for u in units where bumpOf(u) != none:
        b    = (u.propagate == 'inherit') ? bumpOf(u) : u.propagate
        if b == none or u.depth == 0: continue
        sources = u.packages - held            # §13.6a: a held package is not a source
        if sources is empty: continue
        edges = graph.restrictedTo(u.propagateKind)
        frontier = sources
        seen     = set(sources)
        for level in 1 .. u.depth:              # 'all' => until frontier empty
            next = {}
            for p in frontier:
                for d in edges.dependentsOf(p):
                    if d in seen: continue
                    seen.add(d)
                    next.add(d)
            if next is empty: break
            for d in next:
                if d in resolve(u.propagateScope):
                    prop[d] = max(prop[d], b)
            frontier = next
    return prop
```

Properties:

* **Per unit.** Each unit propagates independently from its own package set with its own settings. Results merge by
  `max`, so units never conflict.
* **Cycle-safe.** `seen` guarantees each package is visited once per unit, so dependency cycles terminate. A package in
  a cycle with a changed package is reached at its shortest depth.
* **Depth is shortest-path.** A package reachable at both depth 1 and depth 3 is treated as depth 1 and is therefore
  included by `+1`.
* **Propagation does not cascade its own propagation.** A propagated bump on `B` does not itself trigger a fresh
  propagation pass from `B`; the depth parameter of the originating unit is the only control. This keeps the result a
  pure function of the units and prevents surprising blast radius.
* **Ranges are ignored by default.** Whether a dependent's declared range (`^1.2.0`) already admits the new version does
  not affect propagation, because range satisfaction is not stable across lockfile regeneration. Setting
  `propagation.respectRanges: true` (§14) skips propagation to dependents whose range still admits the new version —
  this is a deployment choice, not a spec default.

### 9.3 Propagated packages and channels

A propagated package's channel is governed by `Propagate-Channel` on the originating unit:

* `inherit` (default) — the dependent is released on the same channel as the originating unit's packages. A `@beta`
  feature in `core` produces `cli@…-beta.N`, keeping the prerelease train self-consistent.
* `stable` — the dependent is released on the stable channel even though the origin is a prerelease. Rarely correct; it
  publishes a stable package that depends on a prerelease.
* `<channel>` — an explicit channel.

If a package receives conflicting propagated channels from two units, the unit in the newest commit wins (`W160`).

If a package has a **direct** channel directive in the window, it always wins over any propagated channel.

### 9.4 Propagation and manifest ranges

When a package is released, the release engine MUST update, in each dependent's manifest, the declared range for every
workspace dependency being released in the same run, according to `rangeStrategy` (§14). This is an implementation
obligation, not part of the commit syntax, but it is normative: a released dependent MUST NOT be published with a range
that excludes the version it was propagated from.

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
* Cancelling does not graduate. To leave a prerelease line, use `release(pkg)@stable`.
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

### 11.2 Channel names

* Charset: `a`–`z`, `0`–`9`, `-`. MUST begin with a letter. Length 1–32.
* Reserved and MUST NOT be used as a prerelease identifier: `stable`, `latest`. `stable` is accepted as a **value**
  meaning "graduate"; `latest` is `E180`.
* Uppercase is `E181` — SemVer prerelease identifiers are case-sensitive and mixed case makes precedence comparisons
  hostile.

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

### 11.5 Graduation

Graduation is `Channel: stable` (inline `@stable`) applied to a package whose baseline is a prerelease.

```
release(@acme/core,@acme/cli)@stable: promote the 2.0 train
```

* The published version is `applyBump(S, E)` — the same `target` as §11.4, with no prerelease suffix.
* Graduation never lowers a version: if `target` is not greater than the baseline core, `E185` is raised (this can only
  happen if tags were hand-edited).
* Graduating a package already on `stable` is a no-op with `W185`, unless the window contains bumps, in which case it is
  an ordinary stable release.
* A `feat(cli)@stable:` unit both adds a feature and graduates; this is legal and equivalent to the two-unit form.

### 11.6 Channel conflicts

If a package's pending window contains units setting two different channels, the unit in the **newest commit** wins;
within one commit, the **last unit** wins. `W186` is emitted with both values. Determinism is chosen over rejecting the
commit, because a channel conflict is usually the result of a merge and blocking the release is worse than picking the
later intent.

### 11.7 Channels and propagation

See §9.3. The default (`Propagate-Channel: inherit`) keeps a prerelease train internally consistent: a `@beta` change in
`core` produces beta versions for every dependent it propagates to, so the beta line can be installed and tested as a
set.

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

Build the package list (name, root path, publishable flag) and the dependency graph from manifests **at `HEAD`**.
Packages deleted before `HEAD` are not in the graph even if history mentions them; units scoping them resolve to `E130`/
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

### 13.4 Parse and resolve

For every commit in the union of all pending windows: parse into units (§17), resolve scopes (§6), yielding a set of
`(package, commit, unitIndex, unit)` tuples. Retain each tuple only for packages whose window contains that commit.

### 13.5 Apply cancellation

Compute the ancestor closure of each `cancel` commit. Discard tuples per §10.3. `cancel` units themselves are then
dropped.

### 13.6 Direct bumps

```
direct(P) = max over surviving tuples for P of bumpOf(unit)
```

`Release-As: <bump>` replaces `bumpOf(unit)` for that unit only.

### 13.6a Holds

For each package, resolve the package-level `Release-As` directives by the precedence rule of §8.6. A package whose
effective directive is `none` is **held**: it is recorded in `held`, excluded from the release plan in §13.10, and
excluded as a propagation source in §13.7. Its tuples are retained — they will be counted by whichever future run
releases it. A newer `auto` or exact version clears a hold; so does a `cancel`, by discarding the unit that carried it.

The engine MUST compute the would-be version for every held package anyway, and report it (`W154`), so that the value
needed to lift the hold is available without hand computation.

Holds are resolved **before** propagation, so that a held package cannot bump its dependents.

### 13.7 Propagation

Run §9.2 over the surviving tuples, skipping any unit whose package set is entirely held, and removing held packages
from every unit's source set. This produces `propagated(P)`. Then `effective(P) = max(direct(P), propagated(P))`.

A held package MAY still *receive* a propagated bump from an unheld dependency; it is recorded but not released, because
§13.10 excludes it. The bump is not lost — it is recomputed from the same tuples on the run that lifts the hold.

### 13.8 Channels

Determine `channel(P)` per §11.1, §11.6, §9.3. Direct directives beat propagated ones; newer commits beat older; later
units beat earlier within a commit.

### 13.9 Versions

For each `P` with `effective(P) != none`, or with a channel differing from its baseline's channel, or with an exact
`Release-As`:

```
if exact Release-As present:              next = that version           # must exceed baseline
else if channel(P) == 'stable':
        next = applyBump(stableBaseline(P), effective(P))               # §12.5, §12.6
else:   next = prerelease per §11.4
```

`applyBump` on a virtual `0.0.0` baseline (no stable tag ever) returns `initialVersion`.

`next` MUST be strictly greater than `baseline(P)` by SemVer precedence; otherwise `E195`.

### 13.10 Emit

Packages with `effective(P) == none` and no channel change and no `Release-As` are **not** released. **Held** packages
are not released regardless of their bump (`W154`). Non-publishable packages are versioned in the plan but MUST NOT be
tagged or published; their propagation to further dependents still applies.

The output is a release plan: package, baseline, next version, channel, contributing units, and for each package the
reason (`direct` or `propagated from X`).

---

## 14. Configuration

Defaults are chosen so that an unconfigured repository behaves conservatively and predictably.

| Key                         | Default                                                      | Meaning                                                                         |
|-----------------------------|--------------------------------------------------------------|---------------------------------------------------------------------------------|
| `separator`                 | `"---"`                                                      | Unit separator line (§4.3).                                                     |
| `tagFormat`                 | `"{name}@{version}"`                                         | Only `{name}@{version}` is normative; other formats are implementation-defined. |
| `initialVersion`            | `"0.1.0"`                                                    | First version for an untagged package.                                          |
| `preserveMajorZero`         | `true`                                                       | Remap bumps while `0.y.z` (§12.6).                                              |
| `types`                     | table in §7.1                                                | Type → bump mapping.                                                            |
| `strictTypes`               | `false`                                                      | Unknown types error instead of warn.                                            |
| `lenient`                   | `false`                                                      | Downgrade selected errors to warnings (§16).                                    |
| `maxDescriptionLength`      | `100`                                                        | `W120` threshold.                                                               |
| `propagation.bump`          | `"patch"`                                                    | Default `Propagate`.                                                            |
| `propagation.depth`         | `1`                                                          | Default `Propagate-Depth`. Set to `"all"` for transitive propagation.           |
| `propagation.kinds`         | `["dependencies","peerDependencies","optionalDependencies"]` | Default `Propagate-Kind`.                                                       |
| `propagation.channel`       | `"inherit"`                                                  | Default `Propagate-Channel`.                                                    |
| `propagation.respectRanges` | `false`                                                      | Skip dependents whose declared range still admits the new version (§9.2).       |
| `rangeStrategy`             | `"caret"`                                                    | How dependent manifests are rewritten (§9.4).                                   |
| `rootPathMap`               | `{}`                                                         | Glob → package list for files owned by no package (§6.2).                       |
| `ignoredPaths`              | `[]`                                                         | Globs removed before file-derived resolution.                                   |
| `channels.allowed`          | `null`                                                       | If set, restricts channel names to a list.                                      |
| `branchChannels`            | `{}`                                                         | Branch glob → default channel; an explicit `Channel` directive always wins.     |

Configuration MUST NOT be able to change the meaning of `cancel`, the ancestry rule, or the `max()` combination rule.
Those are the guarantees the format rests on.

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
| 11  | No space after colon (`feat:x`)                     | `E120`. Lenient mode accepts with `W120`.                                                                   |
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

| #   | Case                                              | Resolution                                                           |
|-----|---------------------------------------------------|----------------------------------------------------------------------|
| 21  | Explicit scope names a nonexistent package        | `E130`.                                                              |
| 22  | Exclusion names a nonexistent package             | `W130`, ignored.                                                     |
| 23  | Scope-set resolves to zero packages               | Unit is inert, `W131`.                                               |
| 24  | Same package included and excluded (`(api,-api)`) | Excluded. Excludes always win, `W133`.                               |
| 25  | Glob matches nothing                              | `W134`, inert contribution.                                          |
| 26  | Commit changes only root files, no scope given    | Derived set empty → inert (`W131`), unless `rootPathMap` applies.    |
| 27  | Nested packages (`ui` and `ui/theme`)             | Longest prefix wins; only `ui/theme` is derived for its files.       |
| 28  | Merge commit, no scope                            | Diff against first parent. Empty diff → empty set.                   |
| 29  | Commit renames a file across packages             | Both source and destination packages are derived.                    |
| 30  | Package deleted between the commit and `HEAD`     | Not in the graph; explicit scope → `E130`; derived → not produced.   |
| 31  | Multi-unit commit with only some units scoped     | `W132`; unscoped units use the derived set.                          |
| 32  | Package literally named `global`                  | Shadowed by the alias; reachable via a glob such as `globa*` (§5.2). |
| 32a | Package literally named `*` or `.`                | Unaddressable; documented limitation (§5.2).                         |
| 33  | `(*)` in a workspace of 400 packages              | Legal; releases everything with a bump.                              |

### 15.3 Propagation

| #   | Case                                                      | Resolution                                                                                                                                      |
|-----|-----------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------|
| 34  | Dependency cycle `A → B → A`                              | Terminates; each package visited once per unit at its shortest depth.                                                                           |
| 35  | Package reachable at depth 1 and depth 3                  | Treated as depth 1.                                                                                                                             |
| 36  | Dependent has its own `feat`, receives propagated `patch` | `max` → `minor`. Direct wins.                                                                                                                   |
| 37  | Dependent has its own `fix`, receives propagated `major`  | `max` → `major`.                                                                                                                                |
| 38  | `^none+*`, `^^none`, or `^minor+0`                        | No propagation; `W152` for redundancy. `^^none` is legal: "all levels" of nothing is nothing.                                                   |
| 38a | `^^` on a unit whose type maps to `none`                  | No propagation — §9.2 skips units with no bump. Depth is irrelevant.                                                                            |
| 39  | Propagation reaches a non-publishable package             | Versioned in the plan, not tagged or published; still propagates onward.                                                                        |
| 40  | Propagation reaches a package with no baseline            | Gets `initialVersion` (§12.5).                                                                                                                  |
| 41  | Two units propagate different bumps to one package        | `max` of the two. No conflict.                                                                                                                  |
| 42  | `Propagate-Scope` excludes everything                     | No propagation; `W135`.                                                                                                                         |
| 43  | Dev-only dependent                                        | Not traversed by default (§8.4).                                                                                                                |
| 44  | Optional dependent                                        | Traversed by default.                                                                                                                           |
| 45  | Depth exceeds graph diameter                              | Equivalent to `all`.                                                                                                                            |
| 46  | A held package would receive a propagated bump            | Recorded, not released (§13.7). It is recomputed when the hold lifts.                                                                           |
| 46a | A held package is a dependency of an unheld one           | The held package is removed as a propagation source; the dependent is not bumped on its behalf. It still releases if it has changes of its own. |

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
| 61  | Graduation with no pending bumps                                       | Publishes the accumulated `target`; if that equals the baseline core, `E185`.                                                                             |
| 62  | Graduating a package already stable                                    | `W185` no-op, or an ordinary release if bumps are pending.                                                                                                |
| 63  | Prerelease with no stable baseline ever                                | Virtual stable baseline `0.0.0` → `target` is `initialVersion`; e.g. `0.1.0-beta.0`.                                                                      |
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
| 73b | Exact `Release-As` lower than the computed version                     | `E156` (lenient: warning). Guards against lifting a hold at a stale number after a breaking change landed.                                                |
| 73c | `Release-As: none` and `auto` (or an exact version) in the same commit | Last unit wins, `W153`.                                                                                                                                   |
| 73d | `none` → `auto` → `none` across three commits                          | Held. The newest directive wins; no replay of the sequence is needed.                                                                                     |
| 73e | `Release-As: auto` with no active hold                                 | No-op, `W158`.                                                                                                                                            |
| 73f | `auto` in a commit that is an ancestor of a later `none`               | Held — the `none` is newer.                                                                                                                               |
| 73g | A `cancel` whose barrier covers the commit carrying a hold             | The hold is discarded along with everything else before the barrier; the package resumes, with an empty ledger.                                           |
| 73h | `Release-As: none` on a unit that also carries `^minor`                | Legal but pointless while held; propagation is suppressed at the source (§13.7).                                                                          |
| 73i | `Release-As: minor` (unit-level) alongside another unit's `feat`       | `max()` still applies across units; the override is local to its own unit.                                                                                |
| 73j | Held package whose channel directive changes                           | Not released; the channel directive is re-evaluated when the hold lifts.                                                                                  |
| 73k | Hold on a package that is also `cancel`-ed in a *later* commit         | Cancel discards the units; the hold survives (it is newer than nothing) only if its own commit is after the barrier.                                      |
| 74  | `0.4.1` with a breaking change, `preserveMajorZero: true`              | `0.5.0`.                                                                                                                                                  |
| 75  | Untagged package with a breaking change                                | `initialVersion` (`0.1.0`), not `1.0.0`.                                                                                                                  |
| 76  | Shallow clone missing tags or ancestry                                 | The engine MUST detect a shallow repository and fail with `E196` rather than compute from partial history.                                                |
| 77  | Squash-merged PR containing many units                                 | Parsed as a multi-unit message — the primary reason the separator exists.                                                                                 |
| 78  | Commit reachable by two merge paths                                    | Counted once (§13.3).                                                                                                                                     |
| 79  | Empty commit (`--allow-empty`) carrying only directives                | Fully supported; this is the normal shape of a `release` or `cancel` commit.                                                                              |
| 80  | Two packages, one on `beta` and one stable, in one commit              | Independent; channel is per package.                                                                                                                      |

---

## 16. Diagnostics registry

Errors (`E`) MUST be reported. Their blast radius depends on the code:

* **Unit-scoped** (`E100`–`E182`) — the offending unit contributes nothing; other units in the same commit still apply,
  and other commits are unaffected.
* **Message-scoped** (`E001`, `E002`) — the commit contributes nothing.
* **Repository-scoped** (`E191`, `E195`, `E196`) — the run cannot produce a correct plan and MUST abort. These are
  integrity failures, not authoring mistakes, and no partial release may be emitted.

Warnings (`W`) never block a release. Commit-lint implementations SHOULD reject a commit at authoring time on any unit-
or message-scoped `E`, and SHOULD additionally reject `W155`, `W156`, and `W172`, which are silent-wrong-answer warnings
rather than style notes.

### Errors

| Code   | Condition                                                                                                                           |
|--------|-------------------------------------------------------------------------------------------------------------------------------------|
| `E001` | Message is not valid UTF-8.                                                                                                         |
| `E002` | Message is empty.                                                                                                                   |
| `E100` | Unit header does not match the grammar.                                                                                             |
| `E101` | Type contains uppercase or illegal characters.                                                                                      |
| `E102` | Whitespace inside a scope-set other than after a comma.                                                                             |
| `E103` | Unbalanced or nested parentheses.                                                                                                   |
| `E104` | Empty scope-set `()`.                                                                                                               |
| `E110` | Duplicate inline directive sigil (including `^` with `^^`, and a third caret).                                                      |
| `E111` | Unknown inline directive value, or an empty value after a sigil other than `^^`.                                                    |
| `E112` | Inline and footer set the same key to different values.                                                                             |
| `E113` | `^^` combined with an explicit `+N` where `N` is not `all`.                                                                         |
| `E120` | Missing or malformed `": "` separator.                                                                                              |
| `E121` | Empty description.                                                                                                                  |
| `E130` | Explicit include names an unknown package.                                                                                          |
| `E140` | Unknown type under `strictTypes`.                                                                                                   |
| `E141` | `release` unit with `!`.                                                                                                            |
| `E151` | Footer value is not valid for its key.                                                                                              |
| `E153` | `Release-As` version not greater than baseline.                                                                                     |
| `E154` | Exact `Release-As` on a multi-package scope-set.                                                                                    |
| `E156` | Exact `Release-As` lower than the computed version (lenient: warning).                                                              |
| `E170` | `cancel` unit with `!`.                                                                                                             |
| `E171` | `cancel` unit with inline directives or a §8.1 release-directive footer. Message-level trailers (§4.5) and unknown keys are exempt. |
| `E180` | Reserved channel name `latest`.                                                                                                     |
| `E181` | Channel name contains uppercase or illegal characters.                                                                              |
| `E182` | Existing prerelease tag uses a non-numeric counter (§15.5 #64).                                                                     |
| `E185` | Graduation would not increase the version.                                                                                          |
| `E191` | Two reachable tags carry the same version for one package on different commits.                                                     |
| `E195` | Computed version not greater than baseline.                                                                                         |
| `E196` | Repository is shallow or grafted; history is incomplete.                                                                            |

### Warnings

| Code   | Condition                                                                                                                                                  |
|--------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `W001` | Empty unit discarded.                                                                                                                                      |
| `W101` | Type lowercased under lenient mode.                                                                                                                        |
| `W110` | Redundant restatement of a directive: inline and footer setting the same key to the same value, or `^^` combined with `+*`.                                |
| `W112` | Footer overrode inline under lenient mode.                                                                                                                 |
| `W120` | Description exceeds `maxDescriptionLength`.                                                                                                                |
| `W130` | Exclusion names an unknown package.                                                                                                                        |
| `W131` | Unit resolved to zero packages (inert).                                                                                                                    |
| `W132` | Multi-unit commit with unscoped units.                                                                                                                     |
| `W133` | Package both included and excluded.                                                                                                                        |
| `W134` | Glob matched nothing.                                                                                                                                      |
| `W135` | `Propagate-Scope` excluded every reached dependent.                                                                                                        |
| `W140` | Unknown type mapped to `none`.                                                                                                                             |
| `W141` | `release` unit with no directives.                                                                                                                         |
| `W150` | Unknown footer key ignored.                                                                                                                                |
| `W151` | Trailing paragraph nearly footer-shaped but treated as body.                                                                                               |
| `W152` | Redundant no-op propagation pairing.                                                                                                                       |
| `W153` | Conflicting package-level `Release-As`; newest won.                                                                                                        |
| `W154` | Package held by `Release-As: none`; not released. The message MUST carry the withheld version.                                                             |
| `W155` | Footer key matches `BREAKING CHANGE` case-insensitively but not exactly; **not** treated as breaking.                                                      |
| `W156` | A `BREAKING CHANGE:` line appears in a body rather than the footer block; no effect.                                                                       |
| `W157` | `BREAKING CHANGE` with an empty value.                                                                                                                     |
| `W158` | `Release-As: auto` with no active hold.                                                                                                                    |
| `W160` | Conflicting propagated channels; newest won.                                                                                                               |
| `W170` | `cancel` had nothing to discard.                                                                                                                           |
| `W171` | `cancel` discarded units already reflected in a published prerelease.                                                                                      |
| `W172` | A commit contains a `cancel` unit alongside a bump-producing unit with an overlapping scope; the latter is discarded by the ancestor-or-self rule (§10.3). |
| `W185` | Graduating a package already stable.                                                                                                                       |
| `W186` | Conflicting channels; newest won.                                                                                                                          |
| `W190` | Tag ignored: version is not valid SemVer.                                                                                                                  |
| `W192` | Manifest version disagrees with baseline.                                                                                                                  |

---

## 17. Parsing without regular expressions

The grammar is designed so that a conforming parser is a single left-to-right index scan with a fixed lookahead of one
character. No backtracking, no regular-expression engine, no recursion. This section is normative for behaviour and
illustrative for structure.

### 17.1 Primitives

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

### 17.2 Splitting a message into units

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

### 17.3 Parsing a header

Single pass, five phases:

```
parseHeader(line):
    sc = Scanner(line)
    h  = { type:'', scopes:[], inline:{}, breaking:false, description:'' }

    # 1. type
    if line.startsWith('BREAKING CHANGE') or line.startsWith('BREAKING-CHANGE'):
        raise E100   # dedicated message: this is a footer, not a type (§5.1)

    h.type = sc.readWhile(isLower)
    if h.type == '':
        if not sc.eof and isUpper(sc.peek): raise E101      # 'Feat: x'
        raise E100                                          # '123: x', ': x', ''
    if not sc.eof and sc.peek not in '(^+@!:': raise E101    # 'feat2: x', 'feat_x: y'

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
    while not sc.eof and sc.peek in '^+@':
        sigil = sc.next()

        if sigil == '^' and not sc.eof and sc.peek == '^':
            sc.next()                                  # doubled caret
            if 'propagate' in h.inline: raise E110      # ^ and ^^ are one sigil
            if not sc.eof and sc.peek == '^': raise E110   # ^^^ — third caret
            value = sc.readUntilAny('^+@!:')
            if value != '':
                h.inline['propagate'] = validateInline('propagate', value)
            if 'depth' in h.inline:
                if h.inline['depth'] != ALL: raise E113
                warn W110
            h.inline['depth']     = ALL
            h.inline['depthFrom'] = '^^'                # for the E113/W110 check below
            continue

        value = sc.readUntilAny('^+@!:')
        if value == '': raise E111
        key = { '^':'propagate', '+':'depth', '@':'channel' }[sigil]
        if key == 'depth' and h.inline.get('depthFrom') == '^^':
            if validateInline('depth', value) != ALL: raise E113
            warn W110
            continue
        if key in h.inline: raise E110
        h.inline[key] = validateInline(key, value)

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

`validateInline` is a table lookup plus, for `depth`, a digit loop:

```
validateInline('propagate', v):
    if v in ['none','patch','minor','major','inherit']: return v
    raise E111

validateInline('depth', v):
    if v == '*' or v == 'all': return ALL
    if v == 'direct':          return 1
    n = 0
    for c in v:
        if not isDigit(c): raise E111
        n = n * 10 + (c - '0')
        if n > 1024: return ALL          # saturate; no graph is deeper
    return n

validateInline('channel', v):
    if v == 'stable': return STABLE
    if v == 'latest': raise E180
    if not isLower(v[0]): raise E181
    for c in v: if not isChannel(c): raise E181
    if len(v) > 32: raise E181
    return v
```

**Why phase 3 is unambiguous.** The scope-set has already been consumed at phase 2, so any `@` remaining is outside
parentheses and can only be a channel sigil. `readUntilAny('^+@!:')` stops at the next sigil, the breaking marker, or
the colon — none of which may appear in a directive value. No lookahead beyond one character is needed.

`^^` does not change that bound. A caret is followed by either another caret or a value character, and those are
distinguished by a single `peek`. The doubled form is also why a directive value may be empty *only* after `^^`: an
empty value elsewhere is `E111`, but `^^` carries meaning on its own.

A third caret needs an explicit guard. Without it, `^^^minor` would tokenise as `^^` (empty value, depth only) followed
by `^minor`, silently parsing as `^^minor`. The `peek == '^'` check after the doubled caret rejects it as `E110` at a
known index instead. Carets are never a repetition count; `^^` is a fixed two-character token and `^^^` is not a token
at all.

`depthFrom` in the listing is scanner scratch state, not part of the parsed result; it exists so the `^^`/`+N` conflict
is detected in either order (`^^minor+2` and `+2^^minor` both yield `E113`).

### 17.4 Splitting a unit into header, body, footers

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

### 17.5 Footer detection without patterns

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

### 17.6 Parsing tags without patterns

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

### 17.7 Complexity and determinism

* Time: O (n) in message length, one pass, no backtracking.
* Space: O (1) beyond the parsed result.
* No input can cause superlinear behaviour — the property that motivates avoiding regular expressions in code that runs
  over untrusted commit messages in CI.
* Every error is raised at a known index, so implementations can render a caret pointing at the offending character.

---

## 18. Appendix A — Regular expressions

Provided for implementers who prefer patterns. These are **equivalent to §17 for well-formed input**; §17 is normative
for error positions and for the diagnostics in §16.

All patterns are PCRE, anchored, and free of nested quantifiers, so they cannot backtrack catastrophically.

**Header (single pattern):**

```regex
^(?<type>[a-z]+)(?:\((?<scopes>[^()\r\n]+)\))?(?<inline>(?:\^\^[^\^+@!:\r\n]*|[\^+@][^\^+@!:\r\n]+)*)(?<breaking>!)?: (?<description>\S[^\r\n]*)$
```

Group notes: `scopes` still requires splitting on `,` and per-term validation; `inline` still requires tokenising by
sigil. The pattern recognises shape, not validity.

The description group opens with `\S`, not `[^\r\n]`, so that the two-space form `feat:  x` is rejected rather than
parsed with a leading space in the description (`E120`, vector 18). A `+` quantifier over `[^\r\n]` silently accepts it.

**Inline directive tokens (apply with a global match to `inline`):**

```regex
(\^\^|[\^+@])([^\^+@!:]*)
```

The `\^\^` alternative MUST come first — with `[\^+@]` first, `^^minor` tokenises as a bare `^` with an empty value
followed by `^minor`. Note also that the value quantifier is `*`, not `+`, because `^^` may stand alone; an empty value
after any other sigil is `E111`, which the pattern does not catch and the caller MUST check.

**Directive value validation:**

```regex
^(?:none|patch|minor|major|inherit)$          # ^ propagate
^(?:\*|all|direct|0|[1-9][0-9]{0,3})$         # + depth
^(?:stable|[a-z][a-z0-9-]{0,31})$             # @ channel
```

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

**Release tag — note the greedy prefix, which is what makes the last-`@` rule work:**

```regex
^(?<name>.+)@(?<version>(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$
```

Using `(?<name>.+?)` (lazy) here is a conformance bug: `@acme/ui@1.2.3` would yield the name `` and fail, or split at
the wrong `@`.

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

| Pitfall                                  | Consequence                                                              | Avoidance                                                               |
|------------------------------------------|--------------------------------------------------------------------------|-------------------------------------------------------------------------|
| `[\^+@]` before `\^\^` in the tokeniser  | `^^minor` silently becomes `^minor` at depth 1                           | Order the alternation longest-first                                     |
| `\^+` to match the caret run             | `^^^minor` accepted as `^^minor`; carets read as a repetition count      | Match the literal two-character token, then guard against a third caret |
| Lazy name in the tag pattern             | Scoped package names split at the wrong `@`                              | Greedy `.+` before the final `@`                                        |
| `[a-zA-Z]+` for type                     | Accepts `Feat`, diverging from `E101`                                    | `[a-z]+`, or lowercase explicitly                                       |
| `[^\r\n]+` for the description           | `feat:  x` parses with a leading space instead of `E120`                 | Anchor the group with `\S`                                              |
| `.*` for scope contents                  | Swallows the `)` and the colon                                           | `[^()\r\n]+`                                                            |
| Matching the separator with `^-{3,}$`    | `----` becomes a separator; a Markdown rule in a body truncates the unit | Exact equality with the configured string                               |
| Multiline mode on the whole message      | Header patterns match mid-body lines                                     | Split into units and lines first                                        |
| `[\s\S]*` around footers                 | Quadratic on long bodies                                                 | Split into paragraphs first                                             |
| The `i` flag on the footer-start pattern | `Breaking change:` becomes a real breaking change, inverting `W155`      | Never case-fold that alternative; fold at key resolution instead        |
| Unicode-unaware `.`                      | Breaks on emoji in descriptions                                          | Enable `u` mode, or use §17                                             |

---

## 19. Appendix B — Conformance test vectors

Each vector is `input → expected`. An implementation is conforming if it reproduces every one. Workspace for all
vectors:

```
packages: core, cli, ui, api, docs-site (private), @acme/theme
edges:    cli -> core, ui -> core, api -> core, docs-site -> ui, @acme/theme -> ui
tags:     core@1.4.2, cli@2.0.0, ui@0.9.1, api@1.2.0, @acme/theme@1.0.0
          (docs-site is private and therefore never tagged — §13.10)
```

Sections B.4 and B.5 override these tags locally where stated.

### B.1 Parsing

| #   | Input header                             | Expected                                                                                 |
|-----|------------------------------------------|------------------------------------------------------------------------------------------|
| 1   | `feat: x`                                | type `feat`, derived scope, bump `minor`                                                 |
| 2   | `fix(core): x`                           | scopes `[core]`, bump `patch`                                                            |
| 3   | `feat(core,cli): x`                      | scopes `[core, cli]`                                                                     |
| 4   | `feat(core, cli): x`                     | scopes `[core, cli]` — space after comma allowed                                         |
| 5   | `feat(core ,cli): x`                     | `E102`                                                                                   |
| 6   | `feat(@acme/theme): x`                   | scopes `[@acme/theme]` — `@` inside parens is literal                                    |
| 7   | `feat(@acme/theme)@beta: x`              | scopes `[@acme/theme]`, channel `beta`                                                   |
| 8   | `feat(*,-docs-site): x`                  | all packages except `docs-site`                                                          |
| 9   | `feat(.,-ui): x`                         | derived set minus `ui`                                                                   |
| 10  | `feat(core)^minor+2: x`                  | propagate `minor`, depth `2`                                                             |
| 11  | `feat(core)+2^minor: x`                  | identical to #10 — order-independent                                                     |
| 12  | `feat(core)^minor^patch: x`              | `E110`                                                                                   |
| 13  | `feat(core)^med: x`                      | `E111`                                                                                   |
| 14  | `feat(core)^minor+*!: x`                 | breaking, propagate `minor`, depth `all`                                                 |
| 14a | `feat(core)^^minor: x`                   | propagate `minor`, depth `all` — identical to #14 without `!`                            |
| 14b | `feat(core)^^: x`                        | propagate `patch` (default), depth `all` — identical to `+*`                             |
| 14c | `feat(core)^^!: x`                       | breaking, propagate `patch`, depth `all`                                                 |
| 14d | `feat(core)^^minor+*: x`                 | as #14a, plus `W110` for the redundant `+*`                                              |
| 14e | `feat(core)^^minor+2: x`                 | `E113`                                                                                   |
| 14f | `feat(core)+2^^minor: x`                 | `E113` — order-independent                                                               |
| 14g | `feat(core)^minor^^: x`                  | `E110` — `^` and `^^` are one sigil                                                      |
| 14h | `feat(core)^^^minor: x`                  | `E110` — third caret                                                                     |
| 14i | `feat(core)^^med: x`                     | `E111`                                                                                   |
| 14j | `feat(core)^^@beta: x`                   | propagate `patch`, depth `all`, channel `beta`                                           |
| 15  | `feat(core)!^minor: x`                   | `E120` — `!` must precede the colon                                                      |
| 16  | `Feat: x`                                | `E101`                                                                                   |
| 17  | `feat:x`                                 | `E120`                                                                                   |
| 18  | `feat:  x`                               | `E120`                                                                                   |
| 19  | `feat: `                                 | `E121`                                                                                   |
| 20  | `feat(): x`                              | `E104`                                                                                   |
| 21  | `feat(core: x`                           | `E103`                                                                                   |
| 22  | `feat(core): fix: y`                     | description `fix: y`                                                                     |
| 23  | `cancel(*): reset release state`         | control unit, scope all                                                                  |
| 24  | `cancel(*)!: x`                          | `E170`                                                                                   |
| 25  | `cancel(core)^minor: x`                  | `E171`                                                                                   |
| 26  | `release(cli)@stable: x`                 | control unit, channel stable                                                             |
| 27  | `release(cli)!: x`                       | `E141`                                                                                   |
| 27a | `BREAKING CHANGE: gone` as a header line | `E100`                                                                                   |
| 27b | `breaking: x`                            | Valid header, unknown type `breaking`, bump `none`, `W140`. **Not** a breaking change.   |
| 27c | `feat(a)(b): x`                          | `E103`                                                                                   |
| 27d | `feat(a,): x`                            | `E104`                                                                                   |
| 27e | `feat2: x`                               | `E101` — digits are not type characters                                                  |
| 27f | `: x`                                    | `E100`                                                                                   |
| 27g | `release(api): Release-As: 3.0.0`        | Valid header, description `Release-As: 3.0.0`, **no** directive set; inert `W141` (§7.2) |

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

| #   | Header                                          | Result                                                                                                                                                                                      |
|-----|-------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 32  | `feat(core): x`                                 | defaults `patch`, depth `1` → `core` `1.5.0`; `cli` `2.0.1`, `ui` `0.9.2`, `api` `1.2.1`. `@acme/theme` and `docs-site` are at depth 2 and are **untouched**                                |
| 33  | `feat(core)+1: x`                               | identical to #32 — `+1` restates the default                                                                                                                                                |
| 33b | `feat(core)+*: x`                               | as #32, plus `@acme/theme` `1.0.1`; `docs-site` planned at `0.1.0` (`initialVersion`) but, being private, neither tagged nor published                                                      |
| 33c | `feat(core)^^: x`                               | identical to #33b                                                                                                                                                                           |
| 33d | `feat(core)^minor: x`                           | `core` `1.5.0`; `cli` `2.1.0`, `api` `1.3.0`, `ui` `0.9.2` (minor remapped to patch while `0.y.z`, §12.6) — bump raised to `minor`, depth still the default `1`; depth-2 packages untouched |
| 34  | `feat(core)^none: x`                            | `core` minor only                                                                                                                                                                           |
| 35  | `feat(core)+0: x`                               | `core` minor only; `W152` if combined with `^minor`                                                                                                                                         |
| 36  | `feat(core)^inherit+*: x`                       | `core` minor; every dependent minor                                                                                                                                                         |
| 36a | `feat(core)^^inherit: x`                        | identical to #36                                                                                                                                                                            |
| 37  | `feat(core)!^inherit+1: x`                      | `core` major; `cli`, `ui`, `api` major                                                                                                                                                      |
| 38  | `feat(core): x` + `feat(cli): y` in one window  | `cli` = max(minor direct, patch propagated) = minor                                                                                                                                         |
| 39  | `feat(core)` with `Propagate-Scope: -docs-site` | `docs-site` untouched                                                                                                                                                                       |
| 40  | `feat(ui): x`                                   | `ui` minor; `docs-site` and `@acme/theme` patch; `docs-site` versioned but not published                                                                                                    |

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
1: feat(core): streaming reader
2: release(core): hold        «Release-As: none»
3: fix(core): guard empty input
4: release(core): resume      «Release-As: auto»
```

→ no release at commits 2 and 3 (`W154`, reporting the withheld `1.5.0`). At commit 4, `core` = `1.5.0`, changelog
containing both entries. `cli`, `ui`, `api` are not propagated to at commits 2–3 (held package is not a source), then
receive their patch at commit 4.

**Vector 44b** — the same history with `cancel(core)` at commit 2 instead of the hold. → `core` = `1.4.3` at commit 3,
changelog containing only the fix. The `feat` is unrecoverable.

**Vector 44c** — hold never lifted. → `core` never releases; `W154` on every run; tuples keep accumulating.

**Vector 44d** — `cancel(core)` at commit 3 of vector 44a, before the lift. → The hold and the `feat` are both
discarded. `core` resumes with an empty ledger and is not released until something new lands.

**Vector 44e** — `none` at commit 2, `auto` at commit 4, `none` again at commit 6. → Held. The newest package-level
directive wins outright; the engine does not replay the sequence.

**Vector 45** — `cancel(*)` in a repo where `api` is at `1.0.0-beta.3` with pending units. → pending units discarded,
`W171`; `api` stays at `1.0.0-beta.3`, still on channel `beta`.

### B.5 Prereleases

Baseline `api@1.0.0-beta.3`, stable baseline `api@0.9.0`.

| #  | Pending                                                             | Expected                                                                                                                                                                                              |
|----|---------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 46 | `feat(api)@beta` (window bump: minor, `preserveMajorZero` on)       | target `applyBump(0.9.0, minor)` = `0.9.1` (minor remapped to patch while `0.y.z`); core differs from `1.0.0` → `0.9.1-beta.0`, and `E195` because that is **lower** than the baseline `1.0.0-beta.3` |
| 47 | same, with `preserveMajorZero: false`                               | target `applyBump(0.9.0, minor)` = `0.10.0` → `0.10.0-beta.0`, still `E195`                                                                                                                           |
| 48 | `feat(api)!@beta`, `preserveMajorZero: false`                       | target `applyBump(0.9.0, major)` = `1.0.0`; core matches baseline core → `1.0.0-beta.4`                                                                                                               |
| 49 | `release(api)@stable`, window containing the breaking change of #48 | `1.0.0`                                                                                                                                                                                               |
| 50 | `release(api)@rc`, same window                                      | `1.0.0-rc.0`                                                                                                                                                                                          |
| 51 | Baseline `core@1.4.2`, `feat(core)@beta`                            | `1.5.0-beta.0`                                                                                                                                                                                        |
| 52 | Then another `fix(core)@beta` in the same window                    | `1.5.0-beta.1`                                                                                                                                                                                        |
| 53 | Then a `feat(core)!@beta`                                           | `2.0.0-beta.0`                                                                                                                                                                                        |
| 54 | Then `release(core)@stable`                                         | `2.0.0`                                                                                                                                                                                               |
| 55 | `ui` at `0.9.1`, `feat(ui)`, `preserveMajorZero: true`              | `0.9.2`                                                                                                                                                                                               |
| 56 | `ui` at `0.9.1`, `feat(ui)!`, `preserveMajorZero: true`             | `0.10.0`                                                                                                                                                                                              |
| 57 | `ui` at `0.9.1`, `Release-As: 1.0.0`                                | `1.0.0`                                                                                                                                                                                               |

Vectors 46 and 47 are retained deliberately: they demonstrate that a hand-created `1.0.0-beta.3` tag on a package whose
last stable release is `0.9.0` produces a version regression under any non-breaking bump, which the engine MUST reject
(`E195`) rather than publish. The fix is `Release-As: 1.0.0-beta.4`, or a stable `api@1.0.0` tag, or a breaking change
as in #48.

### B.6 Tags

| #  | Tag                              | Parsed                                        |
|----|----------------------------------|-----------------------------------------------|
| 58 | `core@1.4.2`                     | `core`, `1.4.2`                               |
| 59 | `@acme/theme@1.0.0`              | `@acme/theme`, `1.0.0`                        |
| 60 | `@acme/theme@1.0.0-rc.1+build.5` | `@acme/theme`, `1.0.0-rc.1`, metadata ignored |
| 61 | `core@v1.4.2`                    | ignored, `W190`                               |
| 62 | `unknown@1.0.0`                  | ignored silently                              |
| 63 | `core@1.4`                       | ignored, `W190`                               |
| 64 | `release-2024`                   | ignored (no `@`)                              |
| 65 | `core@1.5.0-beta3`               | `E182` on use as a prerelease baseline        |

---

## 20. Appendix C — Formal grammar (ABNF)

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

inline-directives = *( deep-tok / propagate-tok / depth-tok / channel-tok )
                                              ; deep-tok MUST be attempted first
deep-tok        = "^^" [ propagate-val ]      ; implies Propagate-Depth: all
propagate-tok   = "^" propagate-val
propagate-val   = "none" / "patch" / "minor" / "major" / "inherit"
depth-tok       = "+" depth-val
depth-val       = "*" / "all" / "direct" / 1*DIGIT
channel-tok     = "@" channel-val
channel-val     = "stable" / channel-name
channel-name    = LOWER *( LOWER / DIGIT / "-" )

description     = 1*( %x20-FF )              ; no LF

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

## 21. Appendix D — Worked examples

### D.1 Ordinary feature with controlled blast radius

```
feat(@acme/core): add streaming reader

The buffered reader is retained; the streaming path is opt-in via
`createReader({ stream: true })`.
```

No directives are needed. `@acme/core` gets a minor bump; its **direct** consumers get a patch, because `Propagate`
defaults to `patch` and `Propagate-Depth` defaults to `1`. Nothing further down the graph moves.

Writing `^patch+1` here would be legal and exactly equivalent — it restates both defaults. Reach for the sigils only
when you mean something other than "bump my consumers a patch".

### D.2 Breaking change that must reach everything

```
refactor(@acme/core)^^inherit!: remove the v1 plugin interface

BREAKING CHANGE: `registerPlugin` is gone. Use `plugins: []` in the
config object. The codemod at tools/codemods/plugins-v2 handles the
mechanical part.
```

`@acme/core` goes major; every transitive dependent goes major, because `inherit` copies this unit's bump. Every
consumer of the workspace sees an accurate signal.

Both parts are load-bearing here. Without the doubled caret only direct consumers would move, leaving depth-2 packages
advertising compatibility they no longer have; without `inherit` the dependents would take the default `patch`, which
understates a removed interface. This is the case the conservative defaults are designed to make you write out.

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
feat(@acme/core,@acme/cli)@beta: new config loader

# commit 2
fix(@acme/cli)@beta: handle missing config file

# commit 3
feat(@acme/core)!@beta: config file format v2

BREAKING CHANGE: `config.json` is replaced by `acme.config.js`.

# commit 4
release(@acme/core,@acme/cli)@stable: ship 2.0
```

From `core@1.4.2`, `cli@2.0.0`:

| Commit | `@acme/core`                                                                 | `@acme/cli`                                               |
|--------|------------------------------------------------------------------------------|-----------------------------------------------------------|
| 1      | `1.5.0-beta.0`                                                               | `2.1.0-beta.0`                                            |
| 2      | — no release; `cli → core`, so a `cli` fix does not reach `core`             | `2.1.0-beta.1`                                            |
| 3      | `2.0.0-beta.0` — target recomputed from `1.4.2` with a `major` in the window | `2.1.0-beta.2` — propagated `patch`, target still `2.1.0` |
| 4      | `2.0.0`                                                                      | `2.1.0`                                                   |

`cli` stays on `beta` in commit 3 without repeating `@beta`, because `Propagate-Channel` defaults to `inherit`. Note
that propagation flows from a dependency to its dependents only; the edge direction is never reversed.

### D.5 Adopting CCME on a repository with imported history

```
cancel(*): reset release state

The importer classified 4,100 pre-2024 commits heuristically. Those
classifications are discarded. Published tags remain authoritative;
nothing is rewritten.
```

Then, immediately:

```
release(@acme/core)@stable: re-baseline at current tag
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

```
chore(*,-docs-site,-e2e)+0: bump TypeScript to 5.6

Release-As: patch
```

`chore` maps to `none`, so `Release-As: patch` forces the release. Every package except the two internal ones publishes
a patch, with `+0` suppressing propagation because every package is already being released directly — without it, the
engine would compute a large dependent closure and discard all of it under `max()`.