# Manifest tools

Every package in a repository carries a file that says what it is called, what version it is at, and what it depends
on. npm calls it `package.json`, Go calls it `go.mod`, Rust calls it `Cargo.toml`, and roughly twenty other formats do
the same job under other names. This page calls all of them **manifests**.

dispat reads and writes manifests in several places already. `dispat compute` reads them to work out which package
depends on which, and auto-versioning writes them so a released package and the packages that consume it agree on the
new version. Two commands expose that machinery on its own:

- **`dispat scanner`** answers "what does this folder actually declare?"
- **`dispat writer`** changes a declaration without disturbing anything else in the file.

Neither one needs a config file, a git repository or a release plan. They read the files you point them at and nothing
else, so they work on any checkout, including one that has never heard of dispat.

## Reading a folder

Point the scanner at a folder and it prints every manifest it finds under it:

```console
$ dispat scanner packages/web
package.json  npm  @acme/web@1.2.0
  dependencies     @acme/core  ^1.2.0
  dependencies     react       19.0.0
  devDependencies  typescript  ~5.4.0
1 manifest(s), 3 dependency declaration(s)
```

The first line of each block is the manifest's identity: where the file is, which ecosystem it belongs to, and the name
and version it declares. Formats that carry a separate build counter, like an Android `versionCode`, show it as
`build 42` at the end of that line. The indented lines are the dependencies, one per line, with the manifest field
they sit in and the version range exactly as written.

With no folder argument the scanner covers the whole repository, which is a quick way to see a monorepo's ecosystems at
a glance:

```console
$ dispat scanner
packages/core/package.json  npm  @acme/core@1.2.0
packages/web/package.json  npm  @acme/web@1.2.0
  dependencies     @acme/core  ^1.2.0
  dependencies     react       19.0.0
  devDependencies  typescript  ~5.4.0
services/api/go.mod  gomod  github.com/acme/api
  dependencies  github.com/acme/core  v1.2.0
3 manifest(s), 4 dependency declaration(s)
```

The walk skips places where a manifest describes somebody else's code: `node_modules`, `vendor`, `target`, `dist`,
`build`, virtual environments and every dot-folder. If you want only the folder's own identity and none of its
sub-folders, add `--root-only`.

### Dependencies that point at a folder

A dependency can name a version, or it can name a place on disk. An npm `"file:../tsconfig"`, a `path =` in
`Cargo.toml` and a relative `replace` in `go.mod` all say "use the copy next door rather than the published one". The
scanner reports these with an arrow, because they are the strongest evidence that two folders in the same repository
belong to one workspace:

```
  devDependencies  @acme/tsconfig  file:../tsconfig  -> ../tsconfig
```

### Machine-readable output

`--log-format json` swaps the listing for one JSON object per manifest, followed by a summary object. This is the same
event format the rest of dispat writes, so a CI step can pipe the scanner's output into whatever already reads it:

```console
$ dispat scanner packages/core --log-format json
{"level":"info","path":"package.json","ecosystem":"npm","root":true,"name":"@acme/core","version":"1.2.0","deps":[],"message":"manifest"}
{"level":"info","manifests":1,"dependencies":0,"failed":0,"message":"scan complete"}
```

### When a manifest will not parse

A file that cannot be read is reported and skipped, and everything that did parse is still printed. That is deliberate:
one broken `package.json` should not hide the twenty healthy ones next to it, and the answer you get is honest about
what it could not include.

By default this is a warning and the command still succeeds. Add `--strict` to make it fail instead, which is what you
want in a CI job that is supposed to catch a malformed manifest before it reaches a release.

## Changing a manifest

The writer edits a manifest in place and preserves its formatting. Only the version text you asked to change moves.
Indentation, key order, comments and blank lines all survive exactly as they were, so the result is a one-line diff
rather than a reformatted file.

```console
$ dispat writer packages/web/package.json --set-version 1.3.0 --set @acme/core=^1.3.0
packages/web/package.json
  version written
  applied  dependencies  @acme/core  ^1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Three flags decide what happens, and each may be repeated:

| Flag             | What it does                                                                     |
|------------------|----------------------------------------------------------------------------------|
| `--set-version`  | Rewrites the manifest's own version field                                        |
| `--set`          | Sets one dependency's declared range                                             |
| `--replace`      | Points one dependency at a local folder, or removes that redirect                |

You can name several manifests in one invocation, including manifests of different ecosystems. Each file is written
on its own and only if something in it actually changed, so re-running the same command a second time is a no-op.

### Spelling a `--set`

The full form is `kind:name=range`, and both halves of that have a reason to be careful:

- The **range** starts after the first `=`, because names never contain one and ranges frequently do. `--set
  requests=>=1.0,<2.0` sets `requests` to `>=1.0,<2.0`.
- The **kind** is the manifest field to edit: `dependencies` (the default), `devDependencies`, `peerDependencies` or
  `optionalDependencies`. A prefix is only read as a kind when it is one of those four words, so a Maven coordinate
  keeps its own colon: `--set com.acme:core=1.3.0` edits the artifact `com.acme:core`, not a field called `com.acme`.

```console
$ dispat writer packages/web/package.json \
    --set @acme/core=^1.3.0 \
    --set devDependencies:typescript=~5.5.0
```

### Pointing a dependency at a folder

`--replace name=path` writes the directive that redirects a dependency to a local folder, in whichever way the format
spells it. A `go.mod` gets a `replace` line, a `package.json` gets a `file:` range, and so on:

```console
$ dispat writer services/api/go.mod --replace github.com/acme/core=../../packages/core
services/api/go.mod
  applied  replace  github.com/acme/core  ../../packages/core
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Leaving the path empty removes the redirect and lets the declaration resolve normally again, which is what has to
happen before anything is published:

```console
$ dispat writer services/api/go.mod --replace 'github.com/acme/core='
services/api/go.mod
  applied  replace  github.com/acme/core  (removed)
```

Only formats with a redirect of their own can do this: `package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml` and
`pubspec.yaml`. On any other manifest the request is reported as skipped and the file is left alone.

### Applied, skipped and missing

Each edit ends in exactly one of three states, and telling them apart is the whole point of the report.

**Applied** means the file changed.

**Skipped** means the dependency is there but its version cannot be written as a literal, because it defers to
something outside the file. A Maven `${property}`, a Cargo workspace inheritance and an Xcode `$(MARKETING_VERSION)`
are all indirections that exist on purpose, and overwriting them with a number would break the thing they were set up
to do. Skipped is the normal, healthy state of a lot of manifests, so it never fails the command.

**Missing** means the manifest does not declare that dependency in that field at all. Usually it means you and the file
disagree about what is in it: a typo in the name, or the right name in the wrong field.

By default a missing edit is reported and the command still succeeds, because a batch aimed at ten manifests is
allowed to overshoot on some of them. `--strict` turns it into a failure for the runs where overshooting is a bug.

A path that no writer covers at all is always an error, `--strict` or not. The other manifests named in the same
command are still written, and the command exits `1` at the end.

## Which tool for which job

- Deriving a monorepo's dependency graph into the config file is [`dispat compute`](./cli.md#the-compute-command).
  It uses the scanner underneath and understands your packages; the scanner alone only reports files.
- Reconciling manifests to the versions a release just computed is
  [auto-versioning](./configuration/spaces.md#autoversion), or `dispat autoversion` on its own. It uses the writer
  underneath and knows what the new versions are; the writer alone only writes what you tell it.
- Looking at what is declared, or making one specific change, is what these two commands are for.

Both are also available as Go libraries, [`pkg/scanner`](https://github.com/yohimik/dispat/tree/main/pkg/scanner) and
[`pkg/writer`](https://github.com/yohimik/dispat/tree/main/pkg/writer), if you would rather import them than shell out.

## Supported formats

The two libraries share one list of file names, so anything the scanner reads has a writer, and both commands cover the
same set:

npm (`package.json`), Go (`go.mod`), Cargo (`Cargo.toml`), Python (`pyproject.toml`, `requirements*.txt`), Composer
(`composer.json`), Maven (`pom.xml`), NuGet (`*.csproj`, `*.fsproj`, `*.vbproj`, `*.nuspec`,
`Directory.Packages.props`, `packages.config`), Dart and Flutter (`pubspec.yaml`), Ruby (`Gemfile`, `*.gemspec`),
CocoaPods (`Podfile`, `*.podspec`), Xcode (`project.pbxproj`), Apple bundles (`Info.plist`), Android
(`AndroidManifest.xml`) and Gradle (`libs.versions.toml`, `build.gradle`, `build.gradle.kts`).

The per-format detail, including which fields each one reads and which of them can be written back, is in the
[scanner](https://github.com/yohimik/dispat/tree/main/pkg/scanner) and
[writer](https://github.com/yohimik/dispat/tree/main/pkg/writer) module documentation.

## Exit codes

`0` when everything asked for was done, `1` for a folder that cannot be read, a manifest no writer covers, a failed
write, or a `--strict` run with a parse failure or a missing edit, and `2` for a command line that does not make sense
(no manifest named, nothing to write, a malformed `--set`).
