# ccme

A Go parser for **Conventional Commits, Monorepo Extension (CCME) 2.0.0**, a strict superset of Conventional Commits
1.0.0.

It uses no regular expressions. The parser executes the single left-to-right index scan from §20 of the specification:
one byte of lookahead, no backtracking, no recursion, O (n) time, and O (1) working space. This design ensures safe
execution when parsing untrusted commit messages in CI.

The specification is [SPEC.md](https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md), and every `§n.m` reference in the code and
documentation points to it. Chapter §20 covers parsing, while §17 covers Conformance.

The parser and specification share major and minor versions through the `ccme` version group. Patch versions remain
package-specific. Version 2 uses the same message grammar and parser behavior as version 1; Go callers must use the
`/v2` import path. This package handles message syntax and message-local diagnostics. Git history, dependency graphs,
fresh admission, propagation, and release planning belong to Dispat's release engine.

## Install

```sh
go get github.com/yohimik/dispat/pkg/ccme/v2
```

## Use

```go
p := ccme.DefaultParser()

res, err := p.Parse(message)
if err != nil {
// err is a *ccme.ParseError listing every error-severity diagnostic.
// res is still populated: an error invalidates only its own unit.
}

for _, u := range res.ValidUnits() {
fmt.Println(u.Header.Type, u.Scopes(), u.Bump, u.Directives.Depth)
}
```

Call `ParseSubject` to check only the subject line during commit-lint runs:

```go
res, err := p.ParseSubject("feat(@acme/core)^^minor%beta!: streaming reader")
u := res.Units[0]
// u.Header.Type          == "feat"
// u.Scopes().String()    == "@acme/core"
// u.Breaking             == true
// u.Bump                 == ccme.BumpMajor
// u.Directives.Propagate == ccme.PropagateMinor
// u.Directives.Depth     == ccme.DepthAll
// u.Directives.Channel   == ccme.ChannelValue{To: "beta"}
```

### Two propagation axes

Section §5.3 divides propagation into two independent axes, each with its own value and depth. Both depths default to
`0`, so a unit reaches no other packages on either axis until you configure it:

| Axis    | Value     | Depth | Footers                                                                   |
|---------|-----------|-------|---------------------------------------------------------------------------|
| bump    | `^`, `^^` | `+N`  | `Propagate`, `Propagate-Depth`, `Propagate-Scope`                         |
| channel | `%%`      | `++N` | `Propagate-Channel`, `Propagate-Channel-Depth`, `Propagate-Channel-Scope` |

`%` sits on neither axis. It sets the channel of the unit itself.

```go
res, _ := p.ParseSubject("feat(core)^^minor%%beta++2: x")
d := res.Units[0].Directives
// d.Propagate        == ccme.PropagateMinor
// d.Depth            == ccme.DepthAll        // "^^" asserts all
// d.PropagateChannel == ccme.ChannelValue{To: "beta"}
// d.ChannelDepth     == ccme.Depth(2)        // "++2" overrides the 1 that "%%" implies
```

Doubled sigils are fixed two-character tokens rather than repetition counts, so `^^^`, `%%%`, and `+++` return `E110`.
A bare `^` is valid and propagates the default bump by one level. The `%%`, `+`, and `++` sigils require an explicit
value, triggering `E111` if you omit the channel name or depth number.

A single caret implies a depth of `1`, which an explicit `+N` overrides. The `^^` sigil *asserts* `all`, so a
conflicting `+N` produces `E113` and a redundant `+*` produces `W110`. The `%%` and `++N` sigils follow the same
pattern, except that `%%` only implies depth and never asserts it.

### Channel transitions

Channel values can specify a transition using `from>to` (§11.2):

```go
p.ParseSubject("release(core)%%*>stable++*: promote the whole train")
// d.PropagateChannel == ccme.ChannelValue{From: "*", To: "stable"}
// d.ChannelDepth     == ccme.DepthAll
```

The `*` token matches any prerelease and acts only as a source. Writing `%%beta>*` triggers `E111` because a target
channel must be explicit. The keywords `inherit` and `none` are standalone values for `Propagate-Channel` and cannot
serve as transition endpoints; equal transition sides trigger `W207`.

A `Parser` instance is immutable after construction and safe for concurrent use.

## Configuration

Configure options through a single `Config` struct that mirrors §14. **The zero value is the specification default**,
so you only need to set fields you want to change:

```go
p, err := ccme.NewParser(ccme.Config{
Separator:            "%%%", // for repos that use format-patch / am
StrictTypes:          true,  // unknown type -> E140 instead of W140
Lenient:              true, // downgrade selected errors to warnings
MaxDescriptionLength: 72,   // 0 = the default 100, negative = no check
Types:                types,   // nil = DefaultTypes(); a non-nil map replaces it
Propagation: ccme.PropagationConfig{
Bump:    ccme.PropagateInherit,
Depth:        ccme.DepthAll,
ChannelDepth: 1,
Kinds:        []ccme.DependencyKind{ccme.KindDependencies, ccme.KindPeerDependencies},
Channel:      ccme.ChannelInherit,
},
Limits: ccme.Limits{           // §14.1 parser bounds; 0 = default, negative rejected
UnitsPerMessage:   64,
ScopeTermsPerUnit: 256,
MessageBytes:      1 << 20,
},
AllowedChannels:      []string{"beta", "rc"}, // nil = unrestricted
MessageLevelTrailers: []string{"Signed-off-by", "Change-Id"},
IssueTrailers:        []string{"Closes", "Fixes"},
})
```

Call `ccme.DefaultParser()` as shorthand for `ccme.MustNewParser(ccme.Config{})`. To start from a fully populated
struct, call `ccme.DefaultConfig()`:

```go
cfg := ccme.DefaultConfig()
cfg.Types["deps"] = ccme.BumpPatch
p := ccme.MustNewParser(cfg)
```

Keep these conventions in mind:

- **nil vs empty slice.** A nil value for `Types`, `Propagation.Kinds`, `AllowedChannels`, `MessageLevelTrailers`, or
  `IssueTrailers` selects the default; an empty slice means *none*.
- **Both depths default to `0`.** The `Propagation.Depth` and `Propagation.ChannelDepth` fields use `0` as the literal
  specification default rather than an unset state. Set `Depth: 1` if your repository bundles dependencies, or use
  `ccme.DepthAll` for full transitive propagation.
- **`Propagation.Kinds` is configuration only.** Section §8.4 provides no per-unit override, so this list applies to
  every unit across every message.

Unset fields fall back to your configuration and then to the specification default. Inspect `Directives.*Set` to see
which source applied.

## Performance

The package is built to scan large commit histories using one parser across many messages in parallel. Parsing runs in
O (n) time relative to message length with no backtracking, and the hot path avoids copying input memory.

- **Normalisation is a no-op when it can be.** Messages formatted with LF line endings, no trailing whitespace, and no
  trailing blank lines return unchanged without allocations. The fast path uses `strings.IndexByte` across the input,
  falling back to a single-pass buffer rewrite only when needed.
- **A single-unit message needs one allocation for its object graph.** The parser allocates `Result`, `Unit`, and
  `[]*Unit` inside a single backing struct, and multi-unit messages share a single array for all units.
- **Text is sliced, not rebuilt.** Fields including `Unit.Raw`, `Unit.Body`, `Header.Raw`, `Header.Description`, and
  individual scope terms are slices of the normalised message. Escaped separators (`\---`) are the only exception,
  requiring reassembly because their content is non-contiguous.
- **One allocation for all units,** with zero allocations for per-unit checks. Validations for scope overlap,
  propagation redundancy, and `Release-As` scopes scan term slices directly without creating temporary maps or slices.
- **Footer keys are matched with an ASCII fold-compare** against the eleven-entry registry without allocating lowercase
  strings for lookups. Section §8.1.1 exempts `BREAKING CHANGE`, which undergoes an exact match.
- **Clean parses do not allocate diagnostics.** The `Errors()` and `Warnings()` methods return nil instead of empty
  slices on clean runs, avoiding allocations on the success path.

The zero-copy design introduces two trade-offs to keep in mind:

- A `Result` references the original message string, keeping it in memory. Copy descriptions into new strings if you
  intend to hold them after parsing large messages.
- The `Directives.Kinds` slice aliases your parser configuration because §8.4 allows no per-unit overrides. Treat this
  slice as read-only; other unit fields are either values or fresh allocations.

Run `bench_test.go` to test subjects, bodies, directive sets, multi-unit inputs, CRLF handling, errors, and parallel
execution across sixteen goroutines:

```sh
go test -bench . -benchmem ./...
```

The figures each release measured are on the [benchmarks page](https://dispat.dev/internals/benchmarks/), injected
there from the run that took them. There are none in this file on purpose: a timing is a fact about a machine and a
toolchain, and a number pasted into a document goes stale the day after it is written while still reading like a
promise.

`alloc_test.go` is where the shape of those numbers is held rather than merely reported. It pins the allocation count
of each parse as a test, because an allocation count is a property of the code rather than of the machine: it moves
only when somebody changes what the parser does, which is exactly when a build should say so.

The allocations come from `Unit` struct allocations rather than from copies of the input, so a message ten times
longer incurs no extra allocation overhead.

`ParseSimple` incurs one allocation during normalisation because its sample input ends in a newline, matching
`git log --format=%B`. Stripping that trailing newline allows the input to take the zero-allocation path.

### Tune `GOGC` for bulk sweeps

A `Parser` stores no mutable state, allowing it to scale across goroutines until garbage collection limits throughput.
Processing a large history creates gigabytes of short-lived allocations for the runtime to collect, and beyond a point
the collector rather than the parser is what sets the pace. `BenchmarkParseParallel` is where that shows: raising
`GOGC` leaves the allocation volume per message exactly as it was and still moves the throughput, which is the whole
evidence that the cost is collection rather than parsing.

If your tool parses a history once and exits, increase `GOGC` or call `debug.SetMemoryLimit` with `GOGC` disabled to
improve throughput at the cost of higher peak RSS.

Run the benchmark yourself rather than trusting a number here; what the release measured is on the
[benchmarks page](https://dispat.dev/internals/benchmarks/).

## What it covers

The package parses the entire commit message structure: normalisation (§4.1), unit splitting with escaped separators
(§4.2), and the header grammar (§5) including scope sets. It handles `^`, `^^`, `+`, `++`, `%`, and `%%` sigils,
channel transitions (§11.2), breaking change indicators, body/footer boundaries (§4.4, §20.5), and the eleven-entry
footer registry (§8.1). It resolves inline versus footer settings (§5.3), maps types to bumps (§7), enforces `cancel`
and `release` rules (§7.2, §10.2), validates `Edits:` and `Deletes:` shapes (§7.4) for the engine, and checks
`Reverts:` values for commit SHAs (§7.3, `W214`).

Diagnostics include an error code, severity level, and byte position so you can display an exact caret pointer:

```
1:18: error E113: '+2' contradicts the depth of all asserted by '^^'
```

### `BREAKING CHANGE` is case-sensitive

This footer is the only case-sensitive key in the grammar. Mismatches parse cleanly but cause release engines to ship
major changes as minor updates, so the parser validates casing strictly (§8.1.1):

| Written                                                  | Result                                          |
|----------------------------------------------------------|-------------------------------------------------|
| `BREAKING CHANGE: x` / `BREAKING-CHANGE: x`              | breaking                                        |
| `Breaking change: x`                                     | **not** breaking, not even a footer: `W155`     |
| `breaking-change: x`                                     | **not** breaking, an unknown footer key: `W155` |
| `BREAKING CHANGE: x` in the body, not the last paragraph | no effect: `W156`                               |
| `BREAKING CHANGE:` with no value                         | breaking: `W157`                                |
| `BREAKING CHANGE: x` as the header line                  | `E100`, with a message saying so                |

The `ccme.SilentFailureCodes()` function exposes `W155` and `W156`. Linters should reject these warnings because they
indicate that the author's intent does not match the parsed result.

### Inert directives: `W152` vs `W201`

Section §8.3b separates warnings based on what the author wrote, never emitting both diagnostics on one axis:

| Written                                  | Diagnostic | Why                                                                  |
|------------------------------------------|------------|----------------------------------------------------------------------|
| `^none`, `+0`, `^^none`, `%%none`, `++0` | `W152`     | the whole directive resolves to nothing; deleting it changes nothing |
| `^minor+0`, `^inherit+0`, `%%beta++0`    | `W201`     | a value *was* named and the depth throws it away                     |
| `release(core)^minor`                    | neither    | the directive is fine; what silences it is the type's bump of `none` |

A directive counts as supplied when written directly in the header or footer. Values inherited from `Config` never
trigger `W201` because increasing `Propagation.Depth` in repository settings restores their effect.

## What it does not cover

This package only parses messages. It does not inspect Git repositories, workspace files, dependency graphs, or version
numbers, so it never emits diagnostics that depend on external context:

`E130`, `E153`, `E156`, `E157`, `E182`, `E185`, `E191`, `E195`, `E196`, `E197`, `E198`, `E199`, `E200`, `E210`, `E211`,
`E212`, `E213`, `W130`, `W131`, `W134`, `W135`, `W153`, `W154`, `W158`, `W159`, `W160`, `W170`, `W171`, `W172`, `W185`,
`W186`, `W190`, `W192`, `W193`, `W194`, `W195`, `W196`, `W197`, `W199`, `W200`, `W202`, `W203`, `W204`, `W205`, `W206`,
`W208`, `W209`, `W210`, `W211`, `W212`, `W213`, `W215`.

The parser emits the three `W2xx` warnings it can evaluate locally: `W201` for zero-depth values, `W207` for equal
transition sides, and `W214` for non-SHA `Reverts` targets. Correction footers receive structural validation here
(`E151`, `E173`), but the release engine resolves their targets against history (§13.4b).

The parser enforces `E154` when a message contains multiple explicit include targets or an include target for the
entire workspace.

The hold mechanism in §8.6.1 operates under the same separation. The parser checks and classifies `Release-As` values
(pinning `4.0.0`, holding with `none`, or resuming with `auto`), leaving conflict resolution across commit windows to
the engine.

`Release-As` supports **no bump form**, meaning `Release-As: minor` triggers `E151` with an explanatory diagnostic
(§8.6). Configure release types once under `Types` instead of specifying bumps on individual commits. Setting
`Release-As: none` does not cancel a unit's bump, since holds preserve pending work rather than clearing it (§8.6.2,
§13.6).

### Parser bounds are always enforced

Commit messages represent untrusted input (§18), so parser limits from §14.1 are enabled by default. Exceeding any cap
triggers `E158`, which is **message-scoped** and invalidates the entire commit.

| `Config.Limits` field | Default |
|-----------------------|--------:|
| `UnitsPerMessage`     |      64 |
| `ScopeTermsPerUnit`   |     256 |
| `MessageBytes`        |   1 MiB |

Zero-valued limit fields adopt their default limits. You cannot disable these limits; `Validate` rejects negative
values and `NewParser` returns an error, so increase the limits if you need larger capacities.

The package includes a SemVer 2.0.0 parser (`ParseVersion`, `Version.Compare`) to validate exact `Release-As` values
during parsing and provide version comparisons for release engines.

## Test

Run the standard test suite to check formatting, correctness, and performance:

```sh
go vet ./...
go test -race -cover ./...          # correctness, including the fuzz seed corpus
go test -bench . -benchmem ./...    # throughput and the allocation budget
```

Run fuzz tests one target at a time using anchored regular expressions to prevent `-fuzz` from matching multiple
functions:

```sh
go test -fuzz '^FuzzParse$'        -fuzztime 5m
go test -fuzz '^FuzzParseSubject$' -fuzztime 2m
go test -fuzz '^FuzzNormalize$'    -fuzztime 2m
```

The test suite validates every vector in Appendix B.1 and B.2, enforcing six release guarantees:

- **Every diagnostic code is reachable.** The `TestEveryDiagnosticCodeIsReachable` test maps all 42 codes to matching
  inputs, failing if an unmapped code is declared or an undeclared code is emitted.
- **No input can panic.** Three fuzz targets test `Parse`, `ParseSubject`, and `Normalize`. They verify diagnostic
  ranges stay within input bounds, `Valid` flags match attached diagnostics, re-parsing matches initial parses, and
  zero-copy substrings remain valid.
- **Normalisation is a fixed point.** The `FuzzNormalize` test verifies idempotence and confirms the fast path matches
  the buffer rewriter output.
- **Output is deterministic** (§17.2). The `TestDiagnosticsAreDeterministic` test parses a message fifty times and
  requires identical diagnostics in matching order, preventing map-iteration variance.
- **`W155` and `W156` cannot be switched off** (§14.2). The `TestSilentFailureWarningsCannotBeSuppressed` test confirms
  permissive configurations still emit both warnings.
- **Allocations do not regress.** The `alloc_test.go` file enforces allocation limits, verifying large message bodies
  add no extra allocations. It uses a `!race` build tag to avoid profiling race detector overhead.

### The fuzz corpus

The `go test -fuzz` command saves only *failing* inputs under `testdata/fuzz/<Target>/`. These files act as regression
tests during normal `go test` runs, so **commit them** when found. The repository tracks two inputs found during
development:

| File                         | Input            | Defect                                                 |
|------------------------------|------------------|--------------------------------------------------------|
| `FuzzParse/ca2afcdbb1727c84` | `"---"`          | `W001` reported one line past the end of the message   |
| `FuzzParse/d03d5667d745b3ab` | `"\ufeff\ufeff"` | `Normalize` stripped one BOM, so it was not idempotent |

The broader coverage-guided corpus lives in your local build cache (`$GOCACHE/fuzz`). The fuzzer regenerates it during
each run, so do not commit it to the repository.

## Requirements

Go 1.21 or later. No dependencies.

## Licence

The parser, meaning all Go source in this package, is licensed under MIT. See [LICENSE](./LICENSE).

The separate CCME specification, [SPEC.md](https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md), is licensed under
GPL-3.0-or-later. See its [LICENSE](https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/LICENSE).
