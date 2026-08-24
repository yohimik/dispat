# ccme: the commit parser

`github.com/yohimik/dispat/pkg/ccme` is a Go parser for Conventional Commits, Monorepo Extension 1.0.0. This format is
a strict superset of Conventional Commits 1.0.0 that adds scopes as packages, propagation depth, and prerelease
channels. The package parses commit messages and nothing else: no git, no workspace, no versions.

The parser uses no regular expressions. It runs a single left-to-right index scan with one byte of lookahead, no
backtracking, and no recursion. This gives O(n) time and O(1) working space, which matters when you feed it untrusted
commit messages from a repository in CI.

Read the vendored specification at [SPEC.md](https://github.com/yohimik/dispat/blob/main/pkg/ccme/SPEC.md). Every
section reference in the package points into this file.

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

A message can hold several units separated by `---`, so the parser returns a list. An error in one unit never
invalidates the others. Read both the result and the error returned by `Parse`.

Call `ParseSubject` to run commit-lint checks on a single subject line.

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

A `Parser` is immutable once constructed. You can safely share one parser across concurrent goroutines for a whole
history sweep.

## Configuring the parser

Set your custom rules in a `Config` struct. The zero value matches the specification default, so you only need to set
the fields you want to change.

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

Call `DefaultParser()` as a shorthand for `MustNewParser(Config{})`. Call `DefaultConfig()` to get the same values
spelled out when you want a populated struct.

A nil slice or map selects the default, but a non-nil empty one forbids all values. Both propagation depths default to
a literal `0`, which is the specification default rather than an unset marker. A unit reaches nobody until it says
otherwise.

## Diagnostics

Every finding carries a code, a severity, and an exact byte position. This lets you point directly at the offending
character.

```
1:18: error E113: '+2' contradicts the depth of all asserted by '^^'
```

Errors use `E` codes and warnings use `W` codes. Call `Result.Errors()` and `Result.Warnings()` to read them. Both
return nil rather than an empty slice on a clean parse, so a successful call allocates nothing.

The package emits only the codes decidable from a message on its own. Anything needing a workspace, a dependency graph,
or git history belongs to the release engine. You can find the full numbered list of both sets in
[Diagnostic codes](../reference/plan-errors.md).

Handle `W155` and `W156` carefully, because they mean the message says something other than what its author meant, like
a miscapitalised `BREAKING CHANGE` footer. You cannot suppress them. `SilentFailureCodes()` returns them so your
commit-lint tooling can reject what the release engine tolerates.

## Versions

The package includes a SemVer 2.0.0 parser. The parser uses this to validate an exact `Release-As` value while it reads
the message.

```go
v, err := ccme.ParseVersion("1.4.0-rc.2")
v.Compare(other)
```

## Bulk parsing

The parser holds no mutable state, so you can scale it across goroutines. Each message produces a couple of kilobytes
of short-lived garbage, making the garbage collector the limit past roughly a million messages per second. Raise `GOGC`
for a tool that sweeps a history once and exits, as this costs nothing but peak memory.

Copy a description to keep it from a large message, because a `Result` retains the whole message string. Treat
`Directives.Kinds` as read-only, because it aliases the parser configuration.

## Further reading

- [Commit messages](../reference/commits.md) describes the same format for the people writing the commits.
- [Diagnostic codes](../reference/plan-errors.md) lists every code the parser and the engine can emit.
- Read the full API on [pkg.go.dev](https://pkg.go.dev/github.com/yohimik/dispat/pkg/ccme) and view the source
  [on GitHub](https://github.com/yohimik/dispat/tree/main/pkg/ccme).
