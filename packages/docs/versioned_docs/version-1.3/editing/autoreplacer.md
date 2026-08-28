# Replacing text across the monorepo

Some versions do not live in a manifest. A Gradle script builds a coordinate by
hand, a Helm chart names a base image, or a README shows an install line. No
manifest parser can reach these files, so no manifest writer can update them.

[The replacer](./replacer.md) handles this for files you name. Run
`dispat autoreplacer` to do the same thing across a whole monorepo. It works
out which packages the run covers, looks in the files you target inside each
one, and replaces the text you requested.

```console
$ dispat autoreplacer --files '*.gradle' --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' --since all
packages/web
  1 file(s) rewritten
2 package(s): 1 file(s), 1 occurrence(s)
```

Two flags do the work. Pass `--replace` to define the find and write pair,
split on `=>`. Pass `--files` to set which files to look in as globs relative
to each package's own folder, and repeat either flag as needed.

## Letting it work out the versions

Typing the versions means editing the command every release. Use placeholders
to write the pattern once:

```sh
dispat autoreplacer --files README.md --replace '{name} {previous}=>{name} {version}'
```

`{name}`, `{version}`, and `{previous}` refer to the covered package. They
represent what it is called, the version it ends the run on, and the version it
is moving from.

Three more placeholders handle the packages it depends on:

```sh
dispat autoreplacer --files '*.gradle' \
  --replace 'com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}'
```

`{provider}`, `{providerVersion}`, and `{providerPrevious}` turn one pattern
into one replacement per dependency. If web depends on core and utils, that
single `--replace` becomes two replacements when web is visited. You do not
have to type either of them.

dispat reads the manifests to see which dependencies count, exactly as
[`dispat autowriter --link-local`](./autowriter.md) reads them. It counts every
declaration naming another package in this workspace. Quote the patterns
because braces mean something to most shells.

## Reaching the packages that need it

The package holding a stale coordinate is usually the one where nothing
changed. Core moved, so web's Gradle file is now out of date. The release
window only covers what the commits touched, so a plain run never visits web at
all.

Pass `--consumers` to reach those dependent packages. This flag adds every
package that depends on a selected one, all the way down the graph:

```sh
dispat autoreplacer --consumers --files '*.gradle' \
  --replace 'com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}'
```

You will usually want the command in this shape. The providers that moved
select the run. Then `--consumers` pulls in everyone carrying a reference to
them.

Everything else about choosing packages works as it does everywhere else, as
described in [Choosing the packages](../cli/run.md#choosing-the-packages). Pass
`--since all` to reach the whole monorepo.

Pass `--only-updated` to narrow the fan-out to the providers this run is
releasing. A dependency released last week is left as it is. A `--replace` with
no `{provider}` placeholder targets the package itself, so this flag does not
affect it.

## Which files it looks in

Write every glob relative to the covered package's folder. dispat walks each
package folder once, no matter how many globs you provide:

```sh
--files README.md          # one file at the package root
--files '*.gradle'         # every gradle file anywhere under the package
--files 'app/*.gradle'     # every gradle file under app/
```

A `*` matches any run of characters, path separators included, so `*.gradle`
already reaches a build script several folders down. There is no `**` form to
reach deeper: spelling it `'**/*.gradle'` requires a literal slash in the
path, which skips the `build.gradle` sitting in the package folder itself.

The folders a workspace scan never enters are skipped here too. A pattern
cannot reach into `node_modules` or a build output tree where the text belongs
to somebody else's code.

A package whose folder contains another package's folder does not touch that
inner package's files. Those files belong to their owner. The inner package
reaches them during its own turn in the run.

dispat steps over files that look binary rather than rewriting them. A version
string that happens to appear inside a PNG is safe.

There is no default for `--files`. A pattern with nowhere to look would
silently do nothing, which is how a typo hides. You must provide the flag.

## It parses nothing

This is both the point and the risk. The replacer does exactly what it is told.
It has no idea whether the text it found is the version you meant or a
coincidence.

Give a replacement enough context to be unambiguous. `com.acme:core:1.2.0` is
safe. A bare `1.2.0` will find things you did not intend.

## Catching a pattern that has gone stale

Pass `--strict` to fail the command when a `--replace` matched nothing in any
covered package:

```console
$ dispat autoreplacer --files '*.gradle' --replace 'com.acme:core:9.9.9=>x' --strict --since all
ERR replacement matched nothing  find=com.acme:core:9.9.9
ERR replacements are not clean  error="1 replacement(s) matched nothing"
```

dispat asks this question across the whole run, not per package. One package
out of twenty carrying the text counts as a match. A pattern that reaches one
file is doing its job.

Running the same command twice is still clean. The text it looked for is gone
after the first run. The command checks whether the file already reads the way
the pattern wanted, and counts that as a match.

## Failures

There is no rollback for this command, so git is your undo. dispat writes each
file on its own and only when something in it changed. A failure halfway
through leaves the files already written in their new state, but a re-run is a
safe no-op.

A failed package skips its dependents, exactly as a failed script does in
`dispat run`. Pass `--on-error continue` to run them anyway. Either way, the
command fails at the end.

## Which tool for which job

- Use [the replacer](./replacer.md) for text in files you can name, with no
  config and no repository needed.
- Use this command for the same text across every package the plan picks.
- Use [`dispat autowriter`](./autowriter.md) for a dependency a manifest
  declares, because it understands the manifest instead of guessing at its
  bytes.
- Use [`autoVersion.replace`](../configuration/autoversion.md) for the same
  replacements on every release, without running a command. This is this
  command's rules written into the configuration.

## Exit codes

| Code | When                                                              |
|------|-------------------------------------------------------------------|
| `0`  | Every file that matched was rewritten                             |
| `1`  | A file could not be written, or `--strict` found a stale pattern  |
| `2`  | No `--replace`, no `--files`, or a malformed `--replace`                  |
