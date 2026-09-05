# Manifest tools

Every package in a repository carries a file that declares its name, its version, and its dependencies. npm uses
`package.json`, Go uses `go.mod`, and Rust uses `Cargo.toml`. Roughly twenty other formats do the same job, and this
page calls all of them **manifests**.

dispat reads and writes manifests in several places already. Run `dispat compute` to read them, build the dependency
graph, and find the starting version for each package. Auto-versioning writes them so a released package and its
consumers agree on the new version. Two commands expose this machinery directly:

- **`dispat scanner`** answers what a folder actually declares.
- **`dispat writer`** changes a declaration without disturbing anything else in the file.

Neither command needs a config file, a git repository, or a release plan. They read only the files you point them at.
You can run them on any checkout, including one that has never used dispat.

Some versions do not live in a manifest at all, like a Gradle coordinate, a Helm chart, or a README example. A third
command parses nothing and replaces literal text instead. See [the replacer](./replacer.md).

## Reading a folder

Point the scanner at a folder to print every manifest under it:

```console
$ dispat scanner packages/web
package.json  npm  @acme/web@1.2.0
  dependencies     @acme/core  ^1.2.0
  dependencies     react       19.0.0
  devDependencies  typescript  ~5.4.0
1 manifest(s), 3 dependency declaration(s)
```

The first line of each block is the manifest's identity. It shows the file path, the ecosystem, the declared name, and
the declared version. Formats with a separate build counter, like an Android `versionCode`, append `build 42` to that
line. The indented lines are the dependencies. You see one dependency per line, its manifest field, and the exact
version range.

Run the scanner with no folder argument to cover the whole repository. This gives you a quick look at a monorepo's
ecosystems:

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

The walk skips places where a manifest describes third-party code. It ignores `node_modules`, `vendor`, `target`,
`dist`, `build`, virtual environments, and every dot-folder. Add `--root-only` to scan the folder's own identity
without its sub-folders.

### Dependencies that point at a folder

A dependency can name a version, or it can name a place on disk. An npm `"file:../tsconfig"`, a `path =` in
`Cargo.toml`, and a relative `replace` in `go.mod` all point to a local copy instead of a published one. The scanner
reports these with an arrow. They are the strongest evidence that two folders in the same repository belong to one
workspace:

```
  devDependencies  @acme/tsconfig  file:../tsconfig  -> ../tsconfig
```

### Machine-readable output

Pass `--log-format json` to swap the listing for one JSON object per manifest, followed by a summary object. This is
the same event format the rest of dispat writes. A CI step can pipe the scanner's output into whatever already reads
it:

```console
$ dispat scanner packages/core --log-format json
{"level":"info","path":"package.json","ecosystem":"npm","root":true,"name":"@acme/core","version":"1.2.0","deps":[],"message":"manifest"}
{"level":"info","manifests":1,"dependencies":0,"failed":0,"message":"scan complete"}
```

A manifest event carries a `dropped` array when the parser meets a declared entry it cannot read. It logs one line per
entry, like `service db: not a mapping`. It also includes a `buildNumber` field for formats that keep a counter. Pass
`--log-level debug` to make the scan narrate itself. It logs where it starts, what each manifest holds, and each
dropped entry as a separate event.

### When a manifest will not parse

The scanner reports and skips any file it cannot read. It still prints everything that did parse. This ensures one
broken `package.json` does not hide twenty healthy ones. The output tells you exactly what it could not include.

This failure is a warning by default, and the command still succeeds. Add `--strict` to make the command fail instead.
Use this flag in a CI job to catch a malformed manifest before it reaches a release.

## Changing a manifest

The writer edits a manifest in place and preserves its formatting. Only the version text you ask to change actually
moves. Indentation, key order, comments, and blank lines survive exactly as they were. The result is a one-line diff
rather than a reformatted file.

```console
$ dispat writer packages/web/package.json --set-version 1.3.0 --set @acme/core=^1.3.0
packages/web/package.json
  version written
  applied  dependencies  @acme/core  ^1.3.0
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Three flags decide what happens, and you can repeat each one:

| Flag             | What it does                                                                     |
|------------------|----------------------------------------------------------------------------------|
| `--set-version`  | Rewrites the manifest's own version field                                        |
| `--set`          | Sets one dependency's declared range                                             |
| `--link`      | Points one dependency at a local folder, or removes that redirect                |

You can name several manifests in one invocation, including manifests from different ecosystems. The writer updates
each file individually, and only when something actually changes. Re-running the exact same command is a no-op.

### Spelling a `--set`

The full form is `kind:name=range`. Both halves require care:

- The **range** starts after the first `=`. Names never contain an equals sign, but ranges frequently do. Pass
  `--set requests=>=1.0,<2.0` to set `requests` to `>=1.0,<2.0`.
- The **kind** is the manifest field to edit, like `dependencies` (the default), `devDependencies`, `peerDependencies`,
  or `optionalDependencies`. A prefix acts as a kind only when it matches one of those four words. A Maven coordinate
  keeps its own colon. Pass `--set com.acme:core=1.3.0` to edit the artifact `com.acme:core`, not a field called
  `com.acme`.

```console
$ dispat writer packages/web/package.json \
    --set @acme/core=^1.3.0 \
    --set devDependencies:typescript=~5.5.0
```

### Pointing a dependency at a folder

Pass `--link name=path` to redirect a dependency to a local folder. The writer uses whichever spelling the format
requires. A `go.mod` gets a `replace` line, and a `package.json` gets a `file:` range:

```console
$ dispat writer services/api/go.mod --link github.com/acme/core=../../packages/core
services/api/go.mod
  applied  link     github.com/acme/core  ../../packages/core
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Leave the path empty to remove the redirect. This lets the declaration resolve normally again. You must do this before
you publish anything:

```console
$ dispat writer services/api/go.mod --link 'github.com/acme/core='
services/api/go.mod
  applied  link     github.com/acme/core  (removed)
1 manifest(s): 1 applied, 0 skipped, 0 missing
```

Only formats with a native redirect can do this. Supported files include `package.json`, `go.mod`, `Cargo.toml`,
`pyproject.toml`, and `pubspec.yaml`. The writer reports the request as skipped and leaves any other manifest alone.

### Sweeping every redirect away

Pass `--drop-links` to remove every local-link directive from the named manifests. You do not need to know the
dependencies' names. A cleanup step rarely knows exactly what the build linked, so this flag handles the sweep
automatically.

```console
$ dispat writer go.mod Cargo.toml --drop-links
go.mod
  applied  link     github.com/acme/core  (removed)
Cargo.toml
  applied  link     core  (removed)
2 manifest(s): 2 applied, 0 skipped, 0 missing
```

The writer leaves a manifest alone when it carries no directive, and the command still succeeds. This makes the sweep
safe to run unconditionally. Do not pass `--link` and `--drop-links` together, because they ask for opposite things.

### Verifying the tree

Four scanner flags turn a scan into a CI gate. The scanner reports each failure as one error event per finding. These
events include a diagnostic code your pipeline can assert on, and the command exits `1`.

Pass `--verify-unlinked` to fail the command when any manifest still carries a local-link directive (code `E215`). Its
scope matches exactly what `--link-local` can inject. It catches a `go.mod` filesystem `replace` in any spelling,
including the parenthesised block a line-based grep misses. It catches a Cargo `[patch.crates-io]` or uv
`[tool.uv.sources]` path entry, a pubspec `dependency_overrides` path, and an npm `file:` or `link:` override. A
dependency *declared* with a local path is not a link and does not trip the gate. Declarations are the manifest's own
business.

Pass `--verify-linked` to point the gate the other way. It fails when no manifest in the selection carries a directive
(code `E216`). Run this after a link step to prove the step actually landed. The scanner evaluates the selection as a
whole, because a single manifest with no workspace dependencies legitimately carries nothing.

```console
$ dispat writer go.mod --drop-links && dispat scanner --verify-unlinked
```

Pass `--forbid-range` and `--require-range` to gate declared dependency ranges. These flags have nothing to do with
links. Both take a literal pattern with `*` as a wildcard, and you can repeat them freely. Forbid fails for every
declared range that matches (code `E217`). Require fails when nothing matches its pattern (code `E218`). The canonical
use is a pnpm workspace. Pass `--forbid-range 'workspace:*'` to prove no placeholder range reaches a registry. Pass
`--require-range 'workspace:*'` to prove a checkout is back in its development state.

```console
$ dispat scanner packages --forbid-range 'workspace:*'
ERR forbidden range  manifest=web/package.json dependency=@acme/core range=workspace:* code=E217
```

The link gates and the range gates answer unrelated questions. You can combine them freely. You only cannot ask for a
gate and its own inverse together.

### Writing the build counter

Mobile formats keep a build counter beside their version. You see `CFBundleVersion` in an Info.plist,
`android:versionCode` in an Android manifest, `CURRENT_PROJECT_VERSION` in an Xcode project, and `versionCode` in a
Gradle build script. A pubspec version carries a `+` suffix like `1.2.3+4`. Version writes never touch them, because a
counter is not a version. It moves once per build, regardless of the release plans. Pass `--set-build` to move the
counter.

```console
$ dispat writer --set-build "$GITHUB_RUN_NUMBER" ios/Info.plist android/app/build.gradle pubspec.yaml
```

The writer leaves an undeclared counter undeclared. The pubspec suffix is the one exception. The writer appends it to
the version it annotates, because that is where pub keeps it. A plist counter deferring to a build setting like
`$(CURRENT_PROJECT_VERSION)` is a deliberate indirection. The writer skips it, just as version writes skip
`$(MARKETING_VERSION)`. Android and Gradle counters must be integers. The writer refuses a non-integer value before it
touches the file.

### Applied, skipped and missing

Each edit ends in exactly one of three states. Telling them apart is the whole point of the report.

**Applied** means the writer changed the file.

**Skipped** means the dependency is present, but its version cannot be written as a literal. The version defers to
something outside the file. A Maven `${property}`, a Cargo workspace inheritance, and an Xcode `$(MARKETING_VERSION)`
are intentional indirections. Overwriting them with a number breaks their intended behavior. Skipped is the normal,
healthy state for many manifests, so it never fails the command.

**Missing** means the manifest does not declare that dependency in that field at all. Usually, you and the file
disagree about its contents. You might have typed the name wrong, or put the right name in the wrong field.

The writer reports a missing edit and the command still succeeds by default. A batch aimed at ten manifests is allowed
to overshoot on some of them. Pass `--strict` to fail the command when overshooting is a bug.

A path that no writer covers is always an error, whether you pass `--strict` or not. The command still writes the other
manifests you named, and then exits `1`.

## Which tool for which job

- Run [`dispat compute`](../cli/compute.md) to derive a monorepo's dependency graph and package baselines into the
  config file. It uses the scanner underneath and understands your packages. The scanner alone only reports files.
- Run [`dispat autowriter`](./autowriter.md) to make the same change in every package the plan picks, instead of naming
  files yourself. It uses the same three flags and produces the same outcomes, but it finds the manifests
  automatically.
- Use [auto-versioning](../configuration/autoversion.md), or `dispat autoversion`, to reconcile manifests to the
  versions a release just computed. It uses the writer underneath and knows the new versions. The writer alone only
  writes what you tell it.
- Use [the replacer](./replacer.md) to replace a version in a file no parser understands. It does exactly what it is
  told and nothing more.
- Use the scanner and writer commands to look at what is declared or to make one specific change.

You can also use both tools as Go libraries. Import [`pkg/scanner`](../go/scanner.md) and
[`pkg/writer`](../go/writer.md) if you prefer code over shell commands.

## Supported formats

The two libraries share one list of file names. Anything the scanner reads has a writer, and both commands cover the
same set:

| Ecosystem | Manifests | Worked example |
|-----------|-----------|----------------|
| npm | `package.json` | [npm](../examples/npm.md), [pnpm](../examples/pnpm.md) |
| Go | `go.mod` | [Go](../examples/go.md) |
| Cargo | `Cargo.toml` | [Rust](../examples/rust.md) |
| Python | `pyproject.toml`, `requirements*.txt` | [Python](../examples/python.md) |
| Composer | `composer.json` | [PHP](../examples/php.md) |
| Maven | `pom.xml` | [Maven](../examples/java.md) |
| NuGet | `*.csproj`, `*.fsproj`, `*.vbproj`, `*.nuspec`, `Directory.Packages.props`, `packages.config` | [.NET](../examples/dotnet.md) |
| Dart and Flutter | `pubspec.yaml` | [Flutter](../examples/flutter.md) |
| Ruby | `Gemfile`, `*.gemspec` | [Ruby](../examples/ruby.md) |
| CocoaPods, Xcode, Apple bundles | `Podfile`, `*.podspec`, `project.pbxproj`, `Info.plist` | [Apple](../examples/apple.md) |
| Android and Gradle | `AndroidManifest.xml`, `build.gradle`, `build.gradle.kts`, `libs.versions.toml` | [Android](../examples/android.md), [Gradle](../examples/gradle.md) |
| Docker | `Dockerfile`, `Containerfile`, `compose.yaml` | [Docker](../examples/docker.md) |
| Unity | `Packages/manifest.json`, `ProjectSettings/ProjectSettings.asset` | [Unity](../examples/unity.md) |
| Godot | `project.godot`, `plugin.cfg`, `export_presets.cfg` | [Godot](../examples/godot.md) |
| Unreal | `*.uproject`, `*.uplugin`, `Config/DefaultGame.ini`, `Config/DefaultEngine.ini` | [Unreal](../examples/unreal.md) |
| Defold | `game.project` | [Games](../examples/game.md) |
| O3DE | `project.json`, `gem.json` | [Games](../examples/game.md) |
| Aqua | `aqua.yaml`, `aqua.yml`, hidden and `aqua/` variants | This page |

### Aqua

The scanner follows `import` and `import_dir` entries that stay inside the scan root. It handles cycles, sorts the
result, and treats imported files as local input only. Package entries can spell a pin as `name@version` or with
separate `name` and `version` fields; the inline value wins. Names from Aqua's standard registry remain bare, while a
different registry is reported as `registry:name`.

The writer changes literal pins only. It reports `version_expr` and `go_version_file` entries as skipped rather than
evaluating them. Neither side fetches a registry, installs a package, or mutates a checksum. Aqua has no package-level
version or build number for dispat to write. For an imported file with an arbitrary name, pass
`dispat writer --manifest-format aqua <file>`.

For example, this file pins a tool from Aqua's standard registry:

```yaml title="aqua.yaml"
packages:
  - name: cli/cli@v2.69.0 # GitHub CLI
```

```sh
dispat writer aqua.yaml --set cli/cli=v2.70.0
```

To connect a tool to a package in your Dispat graph, add its exact Aqua identity to that package's `manifestNames`.
For a custom registry, include the registry prefix, such as `internal:acme/tool`. Automatic versioning writes exact
pins while retaining a configured version prefix such as `v`.

Normal block mappings preserve comments, quote style, key order, and CRLF. The writer refuses flow-style package
maps, anchors or aliases anywhere in the package tree, and block or multiline name and version scalars. Those forms do
not expose an unambiguous byte span for a format-preserving edit, so the file is left untouched. The scanner can still
read valid YAML aliases.

### Formats identified by their directory

Four of those names only act as manifests in the right folder. A `manifest.json` is a web app manifest nearly
everywhere else. An `.asset` file is any serialised Unity object, and an Unreal config file is generic configuration
outside its specific directory. The scanner recognises these four by their path. It reads `Packages/manifest.json` as
Unity's manifest, but leaves `public/manifest.json` alone.

The writer can write everything the scanner reads. This parity lets [auto-versioning](../configuration/autoversion.md)
reconcile any supported format without custom scripts. Read the [scanner](../go/scanner.md) and
[writer](../go/writer.md) module documentation for per-format details. Those pages explain which fields each tool reads
and which shapes they deliberately ignore.

## Docker

Docker fits the same two commands as everything else. It spells "version" differently enough to need its own section.

A Dockerfile's dependencies are the images it names. Every `FROM`, `COPY --from`, and `RUN --mount=...,from=` counts.
Each one pulls a real image:

```dockerfile
FROM --platform=$BUILDPLATFORM ghcr.io/acme/toolchain:2.1.0 AS builder
FROM ghcr.io/acme/base:1.2.3
COPY --from=builder /app /usr/local/bin/app
COPY --from=ghcr.io/acme/certs:3.0.0 /certs /etc/ssl/certs
```

Three of those four are dependencies. `COPY --from=builder` is not a dependency. It names a stage defined earlier in
the same file, making it part of the build rather than an external image. The scanner skips `scratch` for the same
reason, and it ignores stages named by position like `--from=0`. An alias only shadows an image from the line that
defines it onwards. The scanner tells a real image called `tools:1.0` on line one apart from a stage called `tools` on
line two, exactly as the builder does.

A dependency's version is its tag, and a tag is not a range. Registries do not understand `^1.2.3`. A caret policy
writes the plain version instead. A `{version}` template still passes through, which lets you write `{version}-alpine`.

### Which service names a compose file

A Dockerfile has no identity of its own. You name what it builds on the `docker build` command line, not in the file. A
compose file usually does have an identity, and dispat reads it from the services:

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

The service declaring a `build` section and carrying a tagged `image` produces an image. This file is called
`ghcr.io/acme/api` and its version is `1.4.2`. Every other service's image, like `redis:7.2`, is a dependency. When
nothing builds, the tagged image shared by the most services wins. A scaled service appears several times under one
image, while third-party services appear once each. Ties go to the lowest service name alphabetically. The answer never
depends on the order the file was written in. A compose file wiring only third-party services together has no identity.
The scanner reports this as no identity rather than guessing.

The writer puts the version back in the same places. It updates the `image:` of that service and every entry in its
`build.tags:`. These are all names for the single image the build produces. The writer ignores everything else in the
file. It never mistakes a `ports: ["8080:80"]` or a `DATABASE_URL: "postgres:5432"` for a reference.

### What is left alone

The writer reports three kinds of reference as skipped and never rewrites them:

| Reference                      | Why                                                                            |
|--------------------------------|--------------------------------------------------------------------------------|
| `FROM redis`                   | there is no tag, and adding one would override the default you chose            |
| `FROM redis@sha256:...`        | the digest is what gets pulled, so a new tag beside it would name nothing real  |
| `FROM ${REGISTRY}/base:${TAG}` | the value comes from a build argument, and a literal would break that link      |

None of these is an error. They represent a carefully written Dockerfile. A run that meets them still succeeds.

### Naming a Docker package

A Docker manifest declares an image repository as its name, like `ghcr.io/acme/api`. Your package is almost certainly a
folder called `api`. You have two ways to connect them. You can state the repository under
[`manifestNames`](../configuration/packages.md). Alternatively, set `autoVersion.nameMatch` to `substring`. The
substring rule maps the last segment of `ghcr.io/acme/api` onto `api` automatically. The first approach is explicit.
Prefer it when the repository name and the folder name genuinely differ.

## Exit codes

Expect `0` when the command completes everything you asked for. Expect `1` for an unreadable folder, an unsupported
manifest, a failed write, or a triggered verify or range gate. A `--strict` run with a parse failure or a missing edit
also returns `1`. Expect `2` for a command line that does not make sense. This includes naming no manifest, providing
nothing to write, passing a malformed `--set`, or asking for a gate alongside its own inverse.
