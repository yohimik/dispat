# Replacing text across the monorepo

Some versions do not live in a manifest. A Gradle script builds a coordinate by
hand, a Helm chart names a base image, a README shows an install line. No
manifest parser can reach any of them, so no manifest writer can either.

[The replacer](./replacer.md) handles that for files you name. `dispat
autoreplacer` does the same thing for a whole monorepo: it works out which
packages the run covers, looks in the files you point it at inside each one,
and replaces the text you asked for.

```console
$ dispat autoreplacer --files '**/*.gradle' --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' --since all
packages/web
  1 file(s) rewritten
2 package(s): 1 file(s), 1 occurrence(s)
```

Two flags do the work. `--replace` is the find and write pair, split on `=>`.
`--files` says which files to look in, as globs relative to each package's own
folder. Both are repeatable.

## Letting it work out the versions

Typing the versions in means editing the command every release. Placeholders
let you write the pattern once:

```sh
dispat autoreplacer --files README.md --replace '{name} {previous}=>{name} {version}'
```

`{name}`, `{version}` and `{previous}` are the covered package: what it is
called, the version it ends the run on, and the version it is moving from.

Three more placeholders talk about the packages it depends on:

```sh
dispat autoreplacer --files '**/*.gradle' \
  --replace 'com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}'
```

`{provider}`, `{providerVersion}` and `{providerPrevious}` turn one pattern
into one replacement per dependency. If web depends on core and utils, that
single `--replace` becomes two replacements when web is visited, and neither of
them had to be typed.

Which dependencies count is read from the manifests, the same way
[`dispat autowriter --link-local`](./autowriter.md) reads them: every
declaration naming another package in this workspace. Quote the patterns.
Braces mean something to most shells.

## Reaching the packages that need it

The surprise is which package needs the edit. The package holding a stale
coordinate is usually the one where nothing changed. Core moved, and web's Gradle file
is the one now out of date. But the release window covers what the commits
touched, which is core, so a plain run never visits web at all.

`--consumers` is what reaches it. It adds every package that depends on a
selected one, all the way down the graph:

```sh
dispat autoreplacer --consumers --files '**/*.gradle' \
  --replace 'com.acme:{provider}:{providerPrevious}=>com.acme:{provider}:{providerVersion}'
```

That is the shape this command is usually wanted in: the providers that moved
select the run, and `--consumers` pulls in everyone carrying a reference to
them.

Everything else about choosing packages works as it does everywhere else, and
is described in [Choosing the packages](../../cli/run.md#choosing-the-packages).
`--since all` reaches the whole monorepo.

`--only-updated` narrows the fan-out to the providers this run is releasing, so
a dependency released last week is left as it is. A `--replace` with no
`{provider}` placeholder is about the package itself, so this flag does not
affect it.

## Which files it looks in

Every glob is relative to the covered package's folder, and each package folder
is walked once however many globs you give:

```sh
--files README.md          # one file at the package root
--files '*.gradle'         # every gradle file at the root
--files '**/*.gradle'      # every gradle file anywhere under the package
```

The folders a workspace scan never enters are skipped here too, so a pattern
cannot reach into `node_modules` or a build output tree where the text belongs
to somebody else's code.

A package whose folder contains another package's folder does not touch that
package's files. They belong to their owner, and its own turn in the run
reaches them.

Files that look binary are stepped over rather than rewritten, so a version
string that happens to appear inside a PNG is safe.

There is no default for `--files`. A pattern with nowhere to look would silently
do nothing, which is how a typo hides, so the command asks for it.

## It parses nothing

That is the point and the risk. The replacer does exactly what it is told, with
no idea whether the text it found is the version you meant or a coincidence.

Give a replacement enough context to be unambiguous. `com.acme:core:1.2.0` is
safe. A bare `1.2.0` will find things you did not intend.

## Catching a pattern that has gone stale

`--strict` fails the command when a `--replace` matched nothing in any covered
package:

```console
$ dispat autoreplacer --files '*.gradle' --replace 'com.acme:core:9.9.9=>x' --strict --since all
ERR replacement matched nothing  find=com.acme:core:9.9.9
ERR replacements are not clean  error="1 replacement(s) matched nothing"
```

The question is asked across the whole run, not per package. One package out of
twenty carrying the text is a match, because a pattern that reaches one file is
doing its job.

Running the same command twice is still clean. After the first run the text it
looked for is gone, so the command also checks whether the file already reads
the way the pattern wanted, and counts that as a match.

## Failures

Each file is written on its own and only when something in it changed, so a
re-run is a no-op and a failure halfway through leaves the files already written
in their new state. There is no rollback: git is the undo.

A failed package skips its dependents, exactly as a failed script does in
`dispat run`. `--on-error continue` runs them anyway. Either way the command
fails at the end.

## Which tool for which job

- Text in files you can name, no config and no repository needed:
  [the replacer](./replacer.md).
- The same text across every package the plan picks: this command.
- A dependency a manifest declares: [`dispat autowriter`](./autowriter.md),
  which understands the manifest instead of guessing at its bytes.
- The same replacements on every release, without running a command:
  [`autoVersion.replace`](../../configuration/autoversion.md), which is this
  command's rules written into the configuration.

## Exit codes

| Code | When                                                              |
|------|-------------------------------------------------------------------|
| `0`  | Every file that matched was rewritten                             |
| `1`  | A file could not be written, or `--strict` found a stale pattern  |
| `2`  | No `--replace`, no `--files`, or a malformed `--replace`                  |
