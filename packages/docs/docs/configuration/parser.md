# Commit parsing options

How commit messages are parsed and what an error in one does to the run. The message format itself is in the
[commit message reference](../commits.md).

## `commitErrors`

What an error in a commit message does to the run.

| Value              | Effect                                                                                                    |
|--------------------|-----------------------------------------------------------------------------------------------------------|
| `warn` *(default)* | The offending unit contributes nothing and the run continues. Other units in the same commit still apply. |
| `error`            | Any commit error stops the run before anything is built, published or tagged.                             |

`warn` is the blast radius the spec assigns to unit- and message-scoped errors: a malformed header or a scope naming an
unknown package is an authoring mistake in *one unit*, and the rest of the history is unaffected. `error` is the
stricter reading, and the one to choose when a mistyped scope silently dropping a package from a release is the worse
failure of the two.

Neither value affects **repository-scoped** failures: a prerelease tag with no numeric counter, a computed version that
would not exceed the baseline, a graduation that would go backwards, a dependency cycle. Those mean no correct plan
exists, so the run always aborts before releasing anything. They are fixed by correcting the repository (usually a tag)
and re-running, not by editing a commit.

Diagnostics are printed either way, with their code (`E130`, `W193`, ...), the package and the commit.

## `nonPackageScopes`

Scope names that are deliberately not packages, so naming one is not the typo the unknown-package error exists to catch.
A unit scoping only these resolves to nothing, silently and with no diagnostic.

The default is `["release"]`, and it is load-bearing rather than cosmetic: dispat's own release commit is
`chore(release): {tags}`, so without the exemption every run in [`commit`](./records.md#commit) mode would leave an
error behind for the next run to trip over; under `commitErrors: "error"` that would be a tool that breaks its own
repository on the second release. Add your own conventions (`deps`, `ci`, ...) as needed; setting it to `[]`
disables the exemption entirely.

## `parser`

The commit-message parser options. Everything is optional: an absent
`parser` object (or any unset field) keeps the default, so existing configurations parse exactly as before. An invalid
value fails the config load, before any planning.

One setting deserves calling out before the table. **`propagation.depth` changes propagation from opt-in to
on-by-default.** With the default `0`, a plain `feat(core):` releases `core` alone and reach must be written per commit
(`^`, `^^`, `+N`). With `1`, every bump reaches its direct consumers with no caret written; `all` reaches every
transitive consumer. A directive on the unit always wins over the default. Teams coming from tools with automatic
propagation usually want `1` here; teams that want blast radius readable from each commit keep `0`.

| Key                        | Default                            | Description                                                                                                                                                                                                                                                          |
|----------------------------|------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `separator`                | `---`                              | The unit separator line. At least three ASCII-printable characters, no whitespace, must not begin like a type. Repositories exchanging patches by mail often use `%%%`.                                                                                              |
| `types`                    | the standard table                 | Map of commit type → bump (`none`, `patch`, `minor`, `major`). A non-empty map **replaces** the standard table (`feat`=minor, `fix`/`perf`/`revert`=patch, the rest none) wholesale, so list every type you keep. Names are a-z only (viper lowercases keys anyway). |
| `strictTypes`              | `false`                            | Turn an unknown commit type into an error (E140) instead of a warning; the [`commitErrors`](#commiterrors) policy decides whether that stops the run.                                                                                                                |
| `lenient`                  | `false`                            | Downgrade selected authoring errors to warnings: an uppercase type is lowercased, a missing space after `:` is accepted, a footer contradicting an inline directive wins.                                                                                            |
| `maxDescriptionLength`     | `100`                              | The long-description warning threshold, in Unicode scalar values; negative disables it.                                                                                                                                                                              |
| `propagation.bump`         | `patch`                            | The bump consumers take when a unit propagates without saying which: `none`, `patch`, `minor`, `major` or `inherit` (copy the unit's own bump).                                                                                                                      |
| `propagation.depth`        | `0`                                | The default propagation depth: a number of edges or `all`. See the note above the table.                                                                                                                                                                             |
| `propagation.channelDepth` | `0`                                | The channel-axis counterpart: how far a channel travels by default.                                                                                                                                                                                                  |
| `propagation.kinds`        | all but `devDependencies`          | The dependency edges propagation follows: `dependencies`, `peerDependencies`, `optionalDependencies`, `devDependencies` or `all`.                                                                                                                                    |
| `propagation.channel`      | `inherit`                          | The default propagated channel value.                                                                                                                                                                                                                                |
| `limits.unitsPerMessage`   | `64`                               | Most `---`-separated units one commit message may carry. The three `limits.*` keys are always-enforced parser bounds: exceeding one voids the whole message (E158), and a negative value disables that bound (trusted input only).                                   |
| `limits.scopeTermsPerUnit` | `256`                              | Most scope terms (names, globs, exclusions) one unit's scope-set may carry.                                                                                                                                                                                          |
| `limits.messageBytes`      | `1048576`                          | Largest commit message parsed, in bytes (1 MiB).                                                                                                                                                                                                                     |
| `allowedChannels`          | unrestricted                       | Restrict prerelease channel names (E181 outside the list); `stable` is always accepted.                                                                                                                                                                              |
| `messageLevelTrailers`     | Signed-off-by, Co-authored-by, ... | Authorship/review trailers ignored wherever they appear. Setting the key replaces the list.                                                                                                                                                                          |
| `issueTrailers`            | Closes, Fixes, Refs, Resolves      | Issue-reference trailers, ignored for versioning but surfaced for changelogs. Setting the key replaces the list.                                                                                                                                                     |

```yaml
parser:
  types: { feat: minor, fix: patch, perf: patch, revert: patch, docs: patch }
  strictTypes: true
  propagation:
    depth: 1        # bundled dependencies: a bump reaches direct consumers by default
```
