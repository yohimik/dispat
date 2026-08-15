# ccme: the commit parser

`github.com/yohimik/dispat/pkg/ccme` is a Go parser for Conventional Commits, Monorepo Extension 1.0.0, a strict
superset of Conventional Commits 1.0.0 that adds scopes as packages, propagation depth and prerelease channels. It
parses commit messages and nothing else: no git, no workspace, no versions.

The parser uses no regular expressions. It is a single left-to-right index scan with one byte of lookahead, no
backtracking and no recursion, which gives O(n) time and O(1) working space. That property is what matters when the
input is untrusted commit messages arriving from a repository in CI.

The specification is vendored beside the code as [SPEC.md](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md),
and every section reference in the package points into it.

```sh
go get github.com/yohimik/dispat/pkg/ccme
```

## Parsing a message

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

A message can hold several units separated by `---`, so the result is a list. An error in one unit never invalidates
the others, which is why `Parse` returns both a result and an error and both are worth reading.

`ParseSubject` is the narrow entry point for commit-lint checks, taking the subject line on its own:

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

A `Parser` is immutable once constructed and safe for concurrent use, so one parser serves a whole history sweep.

## Configuring the parser

Everything lives in one `Config` struct whose zero value is the specification default, so only the fields you want to
change need setting:

```go
p, err := ccme.NewParser(ccme.Config{
	Separator:            "%%%",
	StrictTypes:          true,
	MaxDescriptionLength: 72,
	Propagation: ccme.PropagationConfig{
		Bump:  ccme.PropagateInherit,
		Depth: ccme.DepthAll,
	},
})
```

`DefaultParser()` is shorthand for `MustNewParser(Config{})`, and `DefaultConfig()` returns the same values spelled out
when starting from a populated struct reads better.

Two conventions carry most of the surprise. A nil slice or map selects the default while a non-nil empty one means
none, so an empty `AllowedChannels` forbids every channel rather than allowing all of them. And both propagation
depths default to a literal `0`, which is the specification default rather than an unset marker: a unit reaches nobody
until it says otherwise.

## Diagnostics

Every finding carries a code, a severity and an exact byte position, so a caller can point at the offending character:

```
1:18: error E113: '+2' contradicts the depth of all asserted by '^^'
```

Errors are `E` codes and warnings are `W` codes. `Result.Errors()` and `Result.Warnings()` return them, and both return
nil rather than an empty slice on a clean parse, so a successful call allocates nothing for the diagnostic path.

The package emits only the codes decidable from a message on its own. Anything needing a workspace, a dependency graph
or git history belongs to the release engine instead, and the full numbered list of both sets is in
[Diagnostic codes](../reference/plan-errors.md).

Two warnings deserve their own handling. `W155` and `W156` mean the message says something other than what its author
meant, most often a `BREAKING CHANGE` footer miscapitalised so that a major change would ship as a minor one. They
cannot be suppressed by any configuration, and `SilentFailureCodes()` returns them so commit-lint tooling can reject
what the release engine merely tolerates.

## Versions

A SemVer 2.0.0 parser comes with the package, because an exact `Release-As` value has to be validated while the message
is being read:

```go
v, err := ccme.ParseVersion("1.4.0-rc.2")
v.Compare(other)
```

## Bulk parsing

The parser holds no mutable state, so it scales across goroutines. Past roughly a million messages per second the limit
becomes the garbage collector rather than the parser, since each message produces a couple of kilobytes of short-lived
garbage. A tool that sweeps a history once and exits should raise `GOGC`, which costs nothing but peak memory and is by
far the highest-value setting here.

Two consequences of the zero-copy design are worth knowing. A `Result` retains the whole message string, so keeping one
description from a large message means copying it. And `Directives.Kinds` aliases the parser configuration, so treat it
as read-only.

## Further reading

- [Commit messages](../reference/commits.md) is the same format described for the people writing the commits.
- [Diagnostic codes](../reference/plan-errors.md) lists every code the parser and the engine can emit.
- The full API is on
  [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/ccme) and the source is
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/ccme).
