# Commit parsing options

Read how dispat parses commit messages and handles errors. You can find the message format in the
[commit message reference](../reference/commits.md).

## `commitErrors`

This setting controls how dispat handles a commit message error.

| Value              | Effect                                                                                                    |
|--------------------|-----------------------------------------------------------------------------------------------------------|
| `warn` *(default)* | The offending unit contributes nothing, but the run continues. Other units in the same commit still apply. |
| `error`            | Any commit error stops the run before dispat builds, publishes, or tags anything.                             |

The spec assigns `warn` as the blast radius for unit-scoped and message-scoped errors. A malformed header or a scope
naming an unknown package is an authoring mistake in *one unit*, so the rest of the history remains unaffected. Choose
the stricter `error` reading when a mistyped scope silently dropping a package from a release is the worse failure of
the two.

Neither value affects **repository-scoped** failures like a dependency cycle or a prerelease tag with no numeric
counter. A computed version that fails to exceed the baseline or a graduation that goes backwards also means no correct
plan exists. The run always aborts before releasing anything, so you must fix the repository (usually a tag) and re-run
instead of editing a commit.

You see diagnostics printed either way with their code (`E130`, `W193`, ...), the package, and the commit. Set
[`parser.quiet`](#quiet) to hide the parser's own output. This changes what you read but alters nothing about what the
run does.

## `nonPackageScopes`

List scope names that are deliberately not packages. This tells dispat not to flag them as typos with the
unknown-package error. A unit scoping only these resolves to nothing, silently, and with no diagnostic.

The default `["release"]` is load-bearing rather than cosmetic. The tool's own release commit is
`chore(release): {tags}`. Without the exemption, every run in [`commit`](./records.md#commit) mode leaves an error
behind for the next run to trip over. Under `commitErrors: "error"`, that breaks the repository on the second release.
Add your own conventions (`deps`, `ci`, ...) as needed, or set it to `[]` to disable the exemption entirely.

## `parser`

Configure the commit-message parser options here. Everything is optional. An absent `parser` object or any unset field
keeps the default, so existing configurations parse exactly as before. An invalid value fails the config load before
dispat does any planning.

Read about one setting before the table. **`propagation.depth` changes propagation from opt-in to on-by-default.** The
default `0` means a plain `feat(core):` releases `core` alone, and you must write reach per commit (`^`, `^^`, `+N`).
Set it to `1` so every bump reaches its direct consumers with no caret written, or use `all` to reach every transitive
consumer. A directive on the unit always wins over the default. Use `1` if you want automatic propagation, or keep `0`
if you want blast radius readable from each commit.

| Key                        | Default                            | Description                                                                                                                                                                                                                                                          |
|----------------------------|------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `separator`                | `---`                              | The unit separator line requires at least three ASCII-printable characters and no whitespace. It must not begin like a type. Repositories exchanging patches by mail often use `%%%`.                                                                                              |
| `types`                    | the standard table                 | Map a commit type to a bump (`none`, `patch`, `minor`, `major`). A non-empty map **replaces** the standard table (`feat`=minor, `fix`/`perf`/`revert`=patch, the rest none) wholesale, so you must list every type you keep. Names use a-z only, and dispat lowercases keys anyway. |
| `quiet`                    | `false`                            | Hide the parser's own diagnostics from the log. See [Quiet](#quiet).                                                                                                                                                                                                 |
| `strictTypes`              | `false`                            | Turn an unknown commit type into an error (E140) instead of a warning. The [`commitErrors`](#commiterrors) policy decides whether that stops the run.                                                                                                                |
| `lenient`                  | `false`                            | Downgrade selected authoring errors to warnings. This lowercases an uppercase type, accepts a missing space after `:`, and lets a footer contradicting an inline directive win.                                                                                            |
| `maxDescriptionLength`     | `100`                              | Set the long-description warning threshold in Unicode scalar values. A negative value disables it.                                                                                                                                                                              |
| `propagation.bump`         | `patch`                            | Choose the bump consumers take when a unit propagates without saying which. Options are `none`, `patch`, `minor`, `major`, or `inherit` (copy the unit's own bump).                                                                                                                      |
| `propagation.depth`        | `0`                                | Set the default propagation depth as a number of edges or `all`. See the note above the table.                                                                                                                                                                             |
| `propagation.channelDepth` | `0`                                | Set the channel-axis counterpart. This controls how far a channel travels by default.                                                                                                                                                                                                  |
| `propagation.kinds`        | all but `devDependencies`          | Choose the dependency edges propagation follows. Options are `dependencies`, `peerDependencies`, `optionalDependencies`, `devDependencies`, or the wildcard `*` for every kind.                                                                                                                                    |
| `propagation.channel`      | `inherit`                          | Set the default propagated channel value.                                                                                                                                                                                                                                |
| `limits.unitsPerMessage`   | `64`                               | Set the most `---`-separated units one commit message may carry. The three `limits.*` keys are always-enforced parser bounds, and exceeding one voids the whole message (E158). A negative value disables that bound for trusted input only.                                   |
| `limits.scopeTermsPerUnit` | `256`                              | Set the most scope terms (names, globs, exclusions) one unit's scope-set may carry.                                                                                                                                                                                          |
| `limits.messageBytes`      | `1048576`                          | Set the largest commit message parsed, in bytes (1 MiB).                                                                                                                                                                                                                     |
| `allowedChannels`          | unrestricted                       | Restrict prerelease channel names. This throws E181 for names outside the list, though `stable` is always accepted.                                                                                                                                                                              |
| `messageLevelTrailers`     | Signed-off-by, Co-authored-by, ... | List authorship and review trailers ignored wherever they appear. Setting the key replaces the list.                                                                                                                                                                          |
| `issueTrailers`            | Closes, Fixes, Refs, Resolves      | List issue-reference trailers. These are ignored for versioning but surfaced for changelogs, and setting the key replaces the list.                                                                                                                                                     |

```yaml
parser:
  types: { feat: minor, fix: patch, perf: patch, revert: patch, docs: patch }
  strictTypes: true
  propagation:
    depth: 1        # bundled dependencies: a bump reaches direct consumers by default
```

### Quiet

A repository whose history predates the convention earns a diagnostic on nearly every old commit. This noise buries the
findings that matter. Set `parser.quiet: true` to hide the parser's own findings about the text of a commit message,
specifically codes `E0xx`/`E1xx` and `W0xx`/`W1xx`.

```yaml
parser:
  quiet: true
```

This is a display decision and only that. Every diagnostic is still raised, counted, and does whatever it did before.
Under `commitErrors: "error"`, a hidden error still refuses the release, and a [repository-scoped](#commiterrors)
failure still aborts the run. Read the plan-diagnostics summary line to see how many lines went unprinted. This ensures
"nothing is wrong" and "you asked not to see it" never look the same:

```console
INF plan diagnostics warnings=12 errors=1 hidden=13
```

You always see findings about the workspace rather than the message. These include an unknown scope (`E130`), a
catch-up (`W193`), a blocked package (`W194`), or a package a [selection](../reference/releasing/partial-releases.md)
could not release yet (`W230`). They exist to explain a release outcome that a reader of the commit log alone cannot
account for.

Pass the [`--quiet-parser` flag](../cli/README.md#global-flags) to override the config in both directions:

```sh
dispat status --quiet-parser         # hide them for this invocation
dispat status --quiet-parser=false   # show them again, whatever the config says
```
