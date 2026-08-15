# ccme

A Go parser for **Conventional Commits, Monorepo Extension (CCME) 1.0.0**, a strict superset of Conventional Commits
1.0.0.

No regular expressions. The parser is the single left-to-right index scan described in §20 of the specification: one
byte of lookahead, no backtracking, no recursion, O (n) time and O (1) working space. That is the property that matters
when the parser runs over untrusted commit messages in CI.

The specification is vendored as [SPEC.md](SPEC.md), and every `§n.m` in the code and in this file refers to it. Note
that the parsing chapter is §20; §17 is Conformance.

## Install

```sh
go get github.com/yohimik/dispat/pkg/ccme
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

`ParseSubject` is the narrow entry point for commit-lint checks; it takes the subject line alone:

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

§5.3 splits propagation into two independent axes, each with its own value and its own depth. Both depths default to
`0`, so a unit reaches nobody on either axis until it says otherwise:

| Axis    | Value     | Depth | Footers                                                                   |
|---------|-----------|-------|---------------------------------------------------------------------------|
| bump    | `^`, `^^` | `+N`  | `Propagate`, `Propagate-Depth`, `Propagate-Scope`                         |
| channel | `%%`      | `++N` | `Propagate-Channel`, `Propagate-Channel-Depth`, `Propagate-Channel-Scope` |

`%` sits on neither axis: it sets the unit's own channel.

```go
res, _ := p.ParseSubject("feat(core)^^minor%%beta++2: x")
d := res.Units[0].Directives
// d.Propagate        == ccme.PropagateMinor
// d.Depth            == ccme.DepthAll        // "^^" asserts all
// d.PropagateChannel == ccme.ChannelValue{To: "beta"}
// d.ChannelDepth     == ccme.Depth(2)        // "++2" overrides the 1 that "%%" implies
```

The doubled sigils are fixed two-character tokens, never a repetition count: `^^^`, `%%%` and `+++` are all `E110`. A
bare `^` is legal and means "propagate the default bump one level"; `%%`, `+` and `++` all require a value, because a
channel with no name and a depth with no number carry nothing worth guessing (`E111`).

A caret implies a depth of `1` and an explicit `+N` silently overrides it; `^^` *asserts* `all`, so a disagreeing
`+N` is `E113` and a restating `+*` is `W110`. The same shape applies to `%%` and `++N`, except that `%%` only implies,
never asserts. Every combination is order-independent.

### Channel transitions

A channel value may be a transition, `from>to` (§11.2):

```go
p.ParseSubject("release(core)%%*>stable++*: promote the whole train")
// d.PropagateChannel == ccme.ChannelValue{From: "*", To: "stable"}
// d.ChannelDepth     == ccme.DepthAll
```

`*` matches any prerelease and is a source only. `%%beta>*` is `E111`, because "move them to some prerelease or other"
is not a releasable instruction. `inherit` and `none` are whole values that only `Propagate-Channel` accepts; they are
never a side of a transition. A transition whose sides are equal is inert and warns with `W207`.

A `Parser` is immutable after construction and safe for concurrent use.

## Configuration

Everything lives in one `Config` struct that mirrors §14. **The zero value is the specification default**, so only the
fields you actually want to change need setting:

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
Limits: ccme.Limits{           // §14.1 parser bounds; 0 = default, negative = off
UnitsPerMessage:   64,
ScopeTermsPerUnit: 256,
MessageBytes:      1 << 20,
},
AllowedChannels:      []string{"beta", "rc"}, // nil = unrestricted
MessageLevelTrailers: []string{"Signed-off-by", "Change-Id"},
IssueTrailers:        []string{"Closes", "Fixes"},
})
```

`ccme.DefaultParser()` is shorthand for `ccme.MustNewParser(ccme.Config{})`, and `ccme.DefaultConfig()` returns the same
values fully spelled out when you'd rather start from a populated struct:

```go
cfg := ccme.DefaultConfig()
cfg.Types["deps"] = ccme.BumpPatch
p := ccme.MustNewParser(cfg)
```

Conventions worth knowing:

- **nil vs empty slice.** A nil `Types`, `Propagation.Kinds`, `AllowedChannels`, `MessageLevelTrailers` or
  `IssueTrailers` selects the default; a non-nil empty one means *none*.
- **Both depths default to `0`.** `Propagation.Depth` and `Propagation.ChannelDepth` mean exactly what they say: a
  literal `0` is the spec default, not "unset", so there is no ambiguity to resolve. Repositories that bundle rather
  than declare their dependencies should set `Depth: 1`; use `ccme.DepthAll` for the full transitive closure.
- **`Propagation.Kinds` is configuration only.** §8.4 has no per-unit override, so this list applies to every unit of
  every message.

Anything not stated by the author falls back to this configuration, then to the spec default, and `Directives.*Set`
tells you which it was.

## Performance

The package is built for sweeping large histories: one parser, many messages, often in parallel. Parsing is O (n) in
message length with no backtracking, and the hot path avoids copying the input.

- **Normalisation is a no-op when it can be.** A message that already has LF endings, no trailing whitespace and no
  trailing blank lines (what git hands you) is returned unchanged, with zero allocations. The check is driven by
  `strings.IndexByte` rather than a byte-at-a-time loop, since it runs over every message in a history. The rewrite
  path, when needed, is a single pass into one buffer.
- **A single-unit message needs one allocation for its object graph.** `Result`, the `Unit` and the `[]*Unit` come out
  of one backing struct rather than three separate allocations; multi-unit messages still get one array for all units.
- **Text is sliced, not rebuilt.** `Unit.Raw`, `Unit.Body`, `Header.Raw`, `Header.Description` and every scope term are
  substrings of the normalised message. The one exception is a unit containing an escaped separator (`\---`), which is
  not contiguous and so is reassembled.
- **One allocation for all units,** not one per unit, and none at all for the per-unit checks: scope-overlap,
  propagation-redundancy and `Release-As` scope checks are all scans over a handful of terms rather than temporary maps
  or filtered slices.
- **Footer keys are matched with an ASCII fold-compare** over the nine-entry registry instead of lowercasing the key
  into a fresh string for a map lookup. `BREAKING CHANGE` sits outside it, since §8.1.1 makes it the one key compared
  exactly.
- **Clean parses do not allocate diagnostics.** `Errors()` and `Warnings()` return nil rather than an empty slice, so a
  successful `Parse` allocates nothing for the diagnostic path.

Two consequences follow from the zero-copy design and are worth knowing:

- A `Result` retains the message string. Holding a `Result` holds the whole message alive; if you keep only a
  description from a large message, copy it.
- `Directives.Kinds` always aliases the parser configuration (§8.4 has no per-unit override). Treat it as read-only.
  Everything else a unit exposes is either a value or freshly allocated.

`bench_test.go` covers subject-only, body, directive-heavy, multi-unit, CRLF and error inputs, plus a parallel benchmark
and a `-race` test that hammers one shared parser from sixteen goroutines:

```sh
go test -bench . -benchmem ./...
```

Measured on an Apple M5 Pro, Go 1.26:

| Benchmark                                     | ns/op | B/op | allocs/op |
|-----------------------------------------------|------:|-----:|----------:|
| `ParseSubject` (63 B header only)             |   137 |  640 |         2 |
| `ParseSimple` (217 B, header + body)          |   331 | 1008 |         5 |
| `ParseDirectives` (375 B, sigils + 5 footers) |   876 | 2152 |        15 |
| `ParseMultiUnit` (178 B, 4 units)             |   818 | 3200 |        12 |
| `NormalizeFastPath`                           |  19.8 |    0 |         0 |
| `NormalizeRewrite`                            |   104 |  240 |         1 |

> These figures predate the two-axis grammar, which widened `InlineDirectives` and `Directives` by a few words each.
> The shape of the numbers is unchanged (the work per message is still one pass with no backtracking) but `B/op`
> will have moved. Re-run `go test -bench . -benchmem` on your own hardware before quoting them; the `ns/op` column
> is machine-specific anyway.

Roughly 1.8M simple messages per second on one core. `B/op` is dominated by the `Unit` struct itself (`Header` and
`Directives` are wide value types), not by copies of the input; a message ten times longer costs the same allocations.

`ParseSimple` pays one of its five allocations for normalisation because its input ends in a newline, as
`git log --format=%B` output does. A message already stripped of its trailing newline takes the zero-allocation fast
path.

### Tune `GOGC` for bulk sweeps

A `Parser` holds no mutable state, so parsing scales across goroutines, but past a certain rate the limit is the garbage
collector, not the parser. Each message produces a couple of kilobytes of short-lived garbage, and at a million-plus
messages per second that is gigabytes per second for the collector to sweep. On the same machine as the table above:

| `BenchmarkParseParallel` | ns/op | speedup vs serial |
|--------------------------|------:|------------------:|
| default `GOGC=100`       |   641 |              1.4x |
| `GOGC=800`               |   232 |              3.5x |

Allocation volume is identical in both runs (2152 B/op, 15 allocs/op), so the difference is collection cost alone. A
tool that sweeps a history once and exits should raise `GOGC` (or set a `debug.SetMemoryLimit` and turn `GOGC` off
entirely); it is the single highest-value knob here, and it costs nothing but peak RSS.

## What it covers

Everything in a commit message: normalisation (§4.1), unit splitting and the escaped separator (§4.2), the header
grammar (§5) including scope-sets, the `^` / `^^` / `+` / `++` / `%` / `%%` sigils, channel transitions (§11.2) and the
breaking marker, the body/footer split (§4.4, §20.5), the eleven-entry footer registry (§8.1), inline-versus-footer
reconciliation (§5.3), type-to-bump mapping (§7), the `cancel` / `release` control rules (§7.2, §10.2), and the
correction footers `Edits:` and `Deletes:` (§7.4), whose values are shape-validated here and resolved against history
by the release engine. `Reverts:` values are shape-validated too (`W214` when the value is not a commit sha), because
§7.3 makes the footer suppress reverted changelog entries.

Diagnostics carry a code, a severity and an exact position, so a caller can point a caret at the offending byte:

```
1:18: error E113: '+2' contradicts the depth of all asserted by '^^'
```

### `BREAKING CHANGE` is case-sensitive

It is the one key in the format that is, and getting it wrong is the most dangerous thing a message can do: it parses
cleanly and ships a major change as a minor one. The package refuses to let that happen quietly (§8.1.1):

| Written                                                  | Result                                          |
|----------------------------------------------------------|-------------------------------------------------|
| `BREAKING CHANGE: x` / `BREAKING-CHANGE: x`              | breaking                                        |
| `Breaking change: x`                                     | **not** breaking, not even a footer: `W155`     |
| `breaking-change: x`                                     | **not** breaking, an unknown footer key: `W155` |
| `BREAKING CHANGE: x` in the body, not the last paragraph | no effect: `W156`                               |
| `BREAKING CHANGE:` with no value                         | breaking: `W157`                                |
| `BREAKING CHANGE: x` as the header line                  | `E100`, with a message saying so                |

`W155` and `W156` are exposed as `ccme.SilentFailureCodes()`: they mean the message says something other than what its
author meant, and commit-lint tooling should reject them even though the release engine tolerates them.

### Inert directives: `W152` vs `W201`

§8.3b draws the line by what the author actually asked for, and never emits both for one axis:

| Written                                  | Diagnostic | Why                                                                  |
|------------------------------------------|------------|----------------------------------------------------------------------|
| `^none`, `+0`, `^^none`, `%%none`, `++0` | `W152`     | the whole directive resolves to nothing; deleting it changes nothing |
| `^minor+0`, `^inherit+0`, `%%beta++0`    | `W201`     | a value *was* named and the depth throws it away                     |
| `release(core)^minor`                    | neither    | the directive is fine; what silences it is the type's bump of `none` |

"Supplied" means written by the unit, in the header or a footer. A value inherited from `Config` never triggers
`W201`, because a repository that raises `Propagation.Depth` makes the same footer meaningful.

## What it does not cover

This package parses messages. It does not read git, load a workspace, walk a dependency graph, or compute versions, so
the diagnostics that need any of those are never emitted:

`E130`, `E153`, `E156`, `E157`, `E182`, `E185`, `E191`, `E195`, `E196`, `E197`, `E198`, `E199`,
`E200`, `W130`, `W131`, `W134`, `W135`, `W153`, `W154`, `W158`, `W159`, `W160`, `W170`, `W171`,
`W172`, `W185`, `W186`, `W190`, `W192`, `W193`, `W194`, `W195`, `W196`, `W197`, `W199`, `W200`,
`W202`, `W203`, `W204`, `W205`, `W206`, `W208`.

The two `W2xx` codes that *are* decidable from a message alone are emitted: `W201` for a propagation value on an axis
whose depth is `0`, and `W207` for a channel transition whose sides are equal.

`E154` is enforced for the cases decidable from the message alone: two or more explicit include terms, or an include
term addressing the whole workspace.

The hold machinery of §8.6.1 is split the same way. `Release-As` values are parsed and classified (`4.0.0` is a pin,
`none` a hold and `auto` a resume, all three package-level) but resolving which directive wins over a window belongs to
the engine.

`Release-As` has **no bump form**: `Release-As: minor` is `E151`, with a diagnostic that says why (§8.6). How large a
change is, is declared by the type; if a category of commit should release in your repository, say so once in `Types`
rather than on every commit. And `Release-As: none` does not suppress its own unit's bump: a hold *retains* the pending
work, which is what distinguishes it from `cancel` (§8.6.2, §13.6).

### Parser bounds are always enforced

A commit message is untrusted input (§18), so the §14.1 caps are on by default and exceeding one is `E158`, which is
**message-scoped**: the whole commit contributes nothing.

| `Config.Limits` field | Default |
|-----------------------|--------:|
| `UnitsPerMessage`     |      64 |
| `ScopeTermsPerUnit`   |     256 |
| `MessageBytes`        |   1 MiB |

A zero field takes the default; a negative one disables that bound, which is only appropriate for input you control.

A SemVer 2.0.0 parser (`ParseVersion`, `Version.Compare`) is included because an exact `Release-As` value has to be
validated at parse time; it is also the type a release engine needs on top.

## Test

```sh
go vet ./...
go test -race -cover ./...          # correctness, including the fuzz seed corpus
go test -bench . -benchmem ./...    # throughput and the allocation budget
```

Fuzzing runs one target at a time, and `-fuzz` takes a regexp. Anchor it, or `FuzzParse` will also match
`FuzzParseSubject` and the run is refused:

```sh
go test -fuzz '^FuzzParse$'        -fuzztime 5m
go test -fuzz '^FuzzParseSubject$' -fuzztime 2m
go test -fuzz '^FuzzNormalize$'    -fuzztime 2m
```

The suite reproduces every vector of Appendix B.1 and B.2, and gates six things a release depends on:

- **Every diagnostic code is reachable.** `TestEveryDiagnosticCodeIsReachable` maps all 40 codes to an input that
  produces it, and fails if a code is declared without a test or produced without being declared.
- **No input can panic.** Three fuzz targets cover `Parse`, `ParseSubject` and `Normalize`, checking that diagnostic
  positions stay inside the message, that `Valid` matches the diagnostics attached to a unit, that re-parsing the
  normalised message is indistinguishable from parsing the original, and that the zero-copy substrings really are
  substrings. The seed corpus alone runs under plain `go test`.
- **Normalisation is a fixed point.** `FuzzNormalize` asserts idempotence and that the fast-path predicate agrees with
  the rewriter; a disagreement there would let `Parse` and `Normalize` see different text.
- **Output is deterministic** (§17.2). `TestDiagnosticsAreDeterministic` parses one message fifty times and requires
  byte-identical diagnostics in the same order; nothing may depend on map-iteration order.
- **`W155` and `W156` cannot be switched off** (§14.2). There is no suppression mechanism, and
  `TestSilentFailureWarningsCannotBeSuppressed` checks the most permissive configuration the API allows still emits
  both.
- **Allocations do not regress.** `alloc_test.go` pins the per-message allocation counts and asserts that a body a
  hundred times larger costs no extra allocations. It carries a `!race` build tag, since the race detector allocates on
  its own.

### The fuzz corpus

`go test -fuzz` writes only *failing* inputs to `testdata/fuzz/<Target>/`. Those are regression cases (Go replays them
on every plain `go test`), so **commit them**. Two are checked in, both defects the fuzzer found here:

| File                         | Input            | Defect                                                 |
|------------------------------|------------------|--------------------------------------------------------|
| `FuzzParse/ca2afcdbb1727c84` | `"---"`          | `W001` reported one line past the end of the message   |
| `FuzzParse/d03d5667d745b3ab` | `"\ufeff\ufeff"` | `Normalize` stripped one BOM, so it was not idempotent |

The much larger coverage-guided corpus (several hundred generated inputs per target) lives in the build cache
(`$GOCACHE/fuzz`), not in the repository. It is regenerated by each run and is not something to commit.

## Requirements

Go 1.21 or later. No dependencies.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
