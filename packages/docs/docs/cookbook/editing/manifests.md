# Manifest tools

Every package in a repository carries a file that says what it is called, what version it is at, and what it depends
on. npm calls it `package.json`, Go calls it `go.mod`, Rust calls it `Cargo.toml`, and roughly twenty other formats do
the same job under other names. This page calls all of them **manifests**.

dispat reads and writes manifests in several places already. `dispat compute` reads them to work out which package
depends on which and which version each package starts from, and auto-versioning writes them so a released package and
the packages that consume it agree on the new version. Two commands expose that machinery on its own:

- **`dispat scanner`** answers "what does this folder actually declare?"
- **`dispat writer`** changes a declaration without disturbing anything else in the file.

Neither one needs a config file, a git repository or a release plan. They read the files you point them at and nothing
else, so they work on any checkout, including one that has never heard of dispat.

For the versions that do not live in a manifest at all, a Gradle coordinate or a Helm chart or a README example,
there is a third command that parses nothing and replaces literal text: see [the replacer](./replacer.md).

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

A manifest event also carries a `dropped` array when the parser met a declared entry it could not read, one line per
entry (`service db: not a mapping`), and a `buildNumber` field where the format keeps a counter. At `--log-level
debug` the scan narrates itself too: where it starts, what each manifest held, and each dropped entry as its own
event.

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
| `--link`      | Points one dependency at a local folder, or removes that redirect                |

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

`--link name=path` writes the directive that redirects a dependency to a local folder, in whichever way the format
spells it. A `go.mod` gets a `replace` line, a `package.json` gets a `file:` range, and so on:

```console
$ dispat writer services/api/go.mod --link github.com/acme/core=../../packages/core
services/api/go.mod
  applied  replace  github.com/acme/core  ../../packages/core
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Leaving the path empty removes the redirect and lets the declaration resolve normally again, which is what has to
happen before anything is published:

```console
$ dispat writer services/api/go.mod --link 'github.com/acme/core='
services/api/go.mod
  applied  replace  github.com/acme/core  (removed)
```

Only formats with a redirect of their own can do this: `package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml` and
`pubspec.yaml`. On any other manifest the request is reported as skipped and the file is left alone.

### Sweeping every redirect away

`--drop-links` removes every local-link directive the named manifests carry, and you do not have to know the
dependencies' names to ask for it. That is its point: the step that has to clean up after a build does not always know
what the build linked.

```console
$ dispat writer go.mod Cargo.toml --drop-links
go.mod
  applied  replace  github.com/acme/core  (removed)
Cargo.toml
  applied  patch  core  (removed)
```

A manifest carrying no directive is left alone and the command still succeeds, so the sweep is safe to run
unconditionally. `--link` and `--drop-links` ask for opposite things and cannot share an invocation.

### Verifying the tree

Four scanner flags turn the same scan into a CI gate. Each failure is reported as one error event per finding, with a
diagnostic code a pipeline can assert on, and the command exits `1`.

`--verify-unlinked` fails when any manifest still carries a local-link directive (code `E215`). Its scope is exactly
what `--link-local` can inject: a `go.mod` filesystem `replace` in any spelling, including the parenthesised block a
line-based grep misses, a Cargo `[patch.crates-io]` or uv `[tool.uv.sources]` path entry, a pubspec
`dependency_overrides` path, and an npm `file:` or `link:` override. A dependency *declared* with a local path is not
a link and does not trip the gate; declarations are the manifest's own business.

`--verify-linked` is the same gate pointed the other way: it fails when no manifest in the selection carries a
directive (code `E216`). After a link step, it proves the step actually landed. The question is asked of the selection
as a whole, because a single manifest with no workspace dependencies legitimately carries nothing.

```console
$ dispat writer go.mod --drop-links && dispat scanner --verify-unlinked
```

`--forbid-range` and `--require-range` gate declared dependency ranges instead, and have nothing to do with links.
Both take a literal pattern with `*` as a wildcard and repeat freely. Forbid fails for every declared range that
matches (code `E217`); require fails when nothing matches its pattern (code `E218`). The canonical use is a pnpm
workspace: `--forbid-range 'workspace:*'` proves no placeholder range is about to reach a registry, and
`--require-range 'workspace:*'` proves a checkout is back in its development state.

```console
$ dispat scanner packages --forbid-range 'workspace:*'
ERR forbidden range  manifest=web/package.json dependency=@acme/core range=workspace:* code=E217
```

The link gates and the range gates answer unrelated questions, so they combine freely; only a gate and its own
inverse cannot be asked together.

### Writing the build counter

The mobile formats keep a build counter beside their version: `CFBundleVersion` in an Info.plist,
`android:versionCode` in an Android manifest, `CURRENT_PROJECT_VERSION` in an Xcode project, `versionCode` in a Gradle
build script, and the `+` suffix a pubspec version carries (`1.2.3+4`). Version writes never touch them, because a
counter is not a version: it moves once per build, whatever the release plans. `--set-build` is the write that moves
it.

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" ios/Info.plist android/app/build.gradle pubspec.yaml
```

A counter the file does not declare is left undeclared; the pubspec suffix is the one exception, appended to the
version it annotates, because that is where pub keeps it. A plist counter deferring to a build setting like
`$(CURRENT_PROJECT_VERSION)` is an indirection to keep, so it is skipped the same way version writes skip
`$(MARKETING_VERSION)`. Android and Gradle counters must be integers, and a value that is not one is refused before
the file is touched.

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

- Deriving a monorepo's dependency graph, and the baselines its packages start from, into the config file is
  [`dispat compute`](../../cli/compute.md). It uses the scanner underneath and understands your packages; the
  scanner alone only reports files.
- Making the same change in every package the plan picks, instead of in the files you name, is
  [`dispat autowriter`](./autowriter.md). Same three flags, same outcomes; it finds the manifests itself.
- Reconciling manifests to the versions a release just computed is
  [auto-versioning](../../configuration/autoversion.md), or `dispat autoversion` on its own. It uses the writer
  underneath and knows what the new versions are; the writer alone only writes what you tell it.
- Replacing a version in a file no parser understands is [the replacer](./replacer.md), which does exactly what it is
  told and nothing more.
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
(`AndroidManifest.xml`), Gradle (`libs.versions.toml`, `build.gradle`, `build.gradle.kts`) and Docker (`Dockerfile`,
`compose.yaml`).

The per-format detail, including which fields each one reads and which of them can be written back, is in the
[scanner](https://github.com/yohimik/dispat/tree/main/pkg/scanner) and
[writer](https://github.com/yohimik/dispat/tree/main/pkg/writer) module documentation.

## Docker

Docker fits the same two commands as everything else, but it spells "version" differently enough to be worth its own
section.

A Dockerfile's dependencies are the images it names. Every `FROM`, every `COPY --from` and every
`RUN --mount=...,from=` counts, because each one pulls a real image:

```dockerfile
FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.1.0 AS builder
FROM ghcr.io/acme/base:1.2.3
COPY --from=builder /app /usr/local/bin/app
COPY --from=ghcr.io/acme/certs:3.0.0 /certs /etc/ssl/certs
```

Three of those four are dependencies. `COPY --from=builder` is not: it names a stage defined earlier in this same
file, which is part of the build rather than something outside it. `scratch` is skipped for the same reason, and so is
a stage named by its position (`--from=0`). An alias only shadows an image from the line that defines it onwards, so a
real image called `tools:1.0` on line one and a stage called `tools` on line two are told apart the way the builder
tells them apart.

The version of a dependency is its tag, and a tag is not a range. There is no such thing as `^1.2.3` in a registry, so
a caret policy writes the plain version. A `{version}` template still passes through, which is how you get
`{version}-alpine`.

### Which service names a compose file

A Dockerfile has no identity of its own: what it builds is named on the `docker build` command line, not in the file.
A compose file usually does have one, and dispat reads it off the services:

```yaml
services:
  api:
    build:
      context: .
      tags:
        - ghcr.io/acme/api:1.4.2
    image: ghcr.io/acme/api:1.4.2
  cache:
    image: redis:7.2
```

The service that both declares a `build` section and carries a tagged `image` is the one producing an image here, so
`ghcr.io/acme/api` at `1.4.2` is what this file is called and what version it is at. Every other service's image,
`redis:7.2`, is a dependency. When nothing builds, the tagged image the most services share wins instead, since a
scaled service appears several times under one image while the third-party ones beside it appear once each. Ties go to
the lowest service name, so the answer never depends on the order the file happened to be written in. A compose file
that only wires third-party services together has no identity, and that is reported as no identity rather than as a
guess.

Writing the version back goes to the same places: the `image:` of that service and every entry of its `build.tags:`,
because those are all names for the one image the build produces. Nothing else in the file is looked at, so a
`ports: ["8080:80"]` or a `DATABASE_URL: "postgres:5432"` is never mistaken for a reference.

### What is left alone

Three kinds of reference are reported as skipped and never rewritten:

| Reference                      | Why                                                                            |
|--------------------------------|--------------------------------------------------------------------------------|
| `FROM redis`                   | there is no tag, and adding one would override the default you chose            |
| `FROM redis@sha256:...`        | the digest is what gets pulled, so a new tag beside it would name nothing real  |
| `FROM ${REGISTRY}/base:${TAG}` | the value comes from a build argument, and a literal would break that link      |

None of these is an error. They are how a careful Dockerfile is written, and a run that meets them still succeeds.

### Naming a Docker package

The name a Docker manifest declares is an image repository (`ghcr.io/acme/api`), and your package is almost certainly
a folder called `api`. Two ways to connect them: state the repository under
[`manifestNames`](../../configuration/packages.md), or set `autoVersion.nameMatch` to `substring`, whose last-segment rule
maps `ghcr.io/acme/api` onto `api` on its own. The first is explicit and worth preferring when the repository name and
the folder name really do differ.

## Exit codes

`0` when everything asked for was done. `1` for a folder that cannot be read, a manifest no writer covers, a failed
write, a verify or range gate that found something, or a `--strict` run with a parse failure or a missing edit. `2`
for a command line that does not make sense: no manifest named, nothing to write, a malformed `--set`, or a gate asked
together with its own inverse.
