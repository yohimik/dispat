# Editing manifests across the monorepo

`dispat writer` edits the manifests you name. That works well for one file, but fails for forty. You would have to know
which packages exist, where each keeps its manifest, and which ones declare the dependency you are changing.

`dispat autowriter` asks the same question the rest of dispat asks. It works out which packages the run covers. It
finds each manifest and applies your edits to all of them:

```console
$ dispat autowriter --set @acme/core=^1.3.0
packages/web/package.json
  applied  dependencies  @acme/core  ^1.3.0
packages/admin/package.json
  applied  dependencies  @acme/core  ^1.3.0
packages/core/package.json
2 package(s): 2 applied, 0 skipped, 0 missing
```

The edits are `dispat writer`'s, spelled identically. The packages are `dispat run`'s, chosen identically. You have
nothing new to learn.

## Which packages it covers

By default, dispat covers the packages the plan is releasing in dependency order. Use the standard dispat flags to
choose differently:

```sh
dispat autowriter --set left-pad=^2.0.0                     # every releasing package
dispat autowriter --set left-pad=^2.0.0 --package web       # just web
dispat autowriter --set left-pad=^2.0.0 --space libs        # every package of the libs space
dispat autowriter --set left-pad=^2.0.0 --group platform    # every package of the platform version group
dispat autowriter --set left-pad=^2.0.0 --since all         # every package in the monorepo
```

Run the command from inside a package folder with no flags to narrow it to that package. The full rule for composing
the window and the filter is in [Choosing the packages](../cli/run.md#choosing-the-packages).

You will reach for `--since all` the most. Bumping a dependency everywhere usually has nothing to do with what is
releasing today.

## Which manifests it edits

dispat scans each covered package and edits every manifest it finds. Pass `--manifests` to decide how far that scan
goes:

| Value            | What it reads                                                            |
|------------------|--------------------------------------------------------------------------|
| `root` (default) | The manifests sitting directly in the package folder                     |
| `all`            | Every manifest anywhere under the package folder                         |

You want `root` almost always. Use `all` for a package that keeps a second manifest inside itself. An example project
or a fixture has to move with the rest.

Under `all`, dispat leaves a manifest belonging to another package to that package. This applies even when its folder
sits inside this one. A file is only ever written by the package that owns it.

dispat quietly skips a folder with no writable manifest. It throws an error if none of the covered packages has one.
Writing nothing without saying so hides a mistyped selection.

## Writing a version the run just worked out

dispat computes the plan before it writes anything. An edit can ask for a version instead of stating one. Write
`{version}` to fill it in:

```sh
dispat autowriter --set @acme/core='^{version}' --set-version '{version}' --since all
```

In a `--set` range, `{version}` is the planned version of the package that edit names. In `--set-version`, it is the
covered package's own version. Both fall back to the package's current version when the run is not releasing it, so a
package with nothing pending keeps the number it had.

Quote the string. `{version}` means something to most shells if you do not.

You cannot template a `--set` whose name belongs to no package of the workspace. dispat refuses the request before it
opens any file. A literal `{version}` written into a manifest is a problem someone finds weeks later.

`--set-version` only ever writes the package's root manifests. A nested example has its own version story. Stamping the
release version into it would be wrong however far the scan went.

## Letting it work out the edits itself

Everything above needs you to name a dependency. That is often the tedious part in a monorepo. You know that every
package depending on something here should follow it, and typing them out is just bookkeeping.

Pass one of three flags to derive the edits instead. Each one looks at what the manifests already declare. It acts on
every dependency that names another package in this workspace:

```sh
dispat autowriter --set-local                 # every workspace range follows its provider
dispat autowriter --set-local --range caret   # and is spelled with a caret
dispat autowriter --link-local                # every workspace dependency points at its folder
dispat autowriter --unlink-local              # and back again
```

Pass `--range` to dictate how to spell what `--set-local` writes. It takes the same words
[`autoVersion.range`](../configuration/autoversion.md) takes: `caret`, `tilde`, `exact`, or a template like
`>={version}`. Leave it out and you get a caret.

The keywords know about ecosystems, so a go.mod gets `v1.3.0`, a Python project gets `==1.3.0`, and a Docker tag gets a
bare `1.3.0`. A registry cannot resolve a caret. A template means you spell the range yourself, so dispat writes it
exactly as given, but prefer a keyword over a workspace that mixes ecosystems.

A dependency you name with `--set` or `--link` keeps what you asked for. The command line is the more specific
instruction. The derived edit steps aside.

`--set-local` and `--link-local` come from one reading of the manifests. You can ask for both at once and each
declaration is visited a single time.

### Working against local folders

Use `--link-local` to work on several packages at once. It writes the directive that points a dependency at its folder
in this repository, so your checkout of one package builds against your checkout of another. Pass `--unlink-local` to
remove exactly what it wrote.

Only five manifest formats have such a directive: `go.mod`, `Cargo.toml`, `pubspec.yaml`, `pyproject.toml` and
`package.json`. See [Redirecting a dependency](./manifests.md) for how each one spells it.

`package.json` is the exception here. npm refuses an override for a dependency the manifest depends on directly unless
the two specs match exactly. A derived link skips npm manifests and says so, but an explicit `--link name=path` still
writes one because you specified the dependency.

In Go, dispat also writes a link for a provider you reach only through another module. Go honours `replace` in the
**main module's** `go.mod` and ignores it everywhere else, so redirecting `core` at its folder is not enough if your
local `core` needs a newer `leaf` than your own `go.mod` records. `go.mod` marks it `// indirect`, which `--set-local`
leaves alone, but `--link-local` writes the redirect and `--unlink-local` removes it.

`--link-local` needs no repair afterwards. Your workspace builds, vets and tests exactly as before, and no `go.sum`
changes.

:::warning
Do not run `go work sync` while the links are in place. A local folder needs no checksum, so the sync deletes the
`go.sum` entries for every linked module, and they are needed again the moment you unlink. The module builds fine for
you but fails for everyone else with `missing go.sum entry`, so turn off the
[`syncLock`](../configuration/autoversion.md) scripts with `--sync-lock=false` around a link.
:::

:::warning
Do not publish a local link. Nothing in a release removes one, so a link left in place ships a manifest your consumers
cannot resolve. Run `dispat autowriter --unlink-local` before you release.
:::

### Building a release against the working tree

A release stage that compiles a binary resolves its workspace dependencies from the versions `go.mod` pins, and those
are only as fresh as the last release. A provider changed without a version bump ships as its published copy, even
though every test in CI ran your working tree, so bracketing the build closes the gap. dispat's own
[`build` script](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.yaml) is exactly this bracket,
opened and closed inside the stage with `flow.onFail` running the unlink as the net, and as a standalone script, the
same shape is:

```sh
link() { dispat autowriter --package dispat --since all --sync-lock=false "$@"; }
trap 'link --unlink-local >/dev/null 2>&1 || true' EXIT INT TERM
link --link-local

# ... cross-compile, with GOWORK=off ...

link --unlink-local
grep -q '^replace github.com/acme' go.mod && exit 1
```

The `trap` closes the bracket on the paths that never reach the end, including an interrupt. The explicit
`--unlink-local` closes it before the stage reports success, which happens before anything is published. The `grep`
fails the build if a directive survived anyway, which costs a release run but saves a bad tag.

`--package` narrows which manifest dispat writes, not which providers are linked. Every provider that package declares
is redirected inside its own `go.mod` either way. Writing the *providers'* manifests as well achieves nothing for the
build for the main-module reason above.

Keep `GOWORK=off` alongside the links. dispat redirects only the intra-repo modules, so the build still proves the
module's own `go.mod` and `go.sum` cover its third-party requirements. A workspace build would satisfy those from some
other module's requires.

### When to use this instead of autoversion

`dispat autoversion` reconciles ranges too, and a release runs it. The difference is where the rules come from.

`autoversion` reads the space's configuration. It checks which dependency kinds count, which ranges are protected, and
which providers are in scope. It is the policy your releases follow.

`--set-local` reads no configuration at all. dispat reconciles every declaration naming a package here, spelled by
`--range` and narrowed only by `--only-updated`. That makes it the tool for a one-off pass by hand, so pass the
`--range` your space configures if you want the two to match.

## Only what this run updates

Pass `--only-updated` to keep only the edits whose name is a package this run is releasing:

```sh
dispat autowriter --set @acme/core='^{version}' --only-updated --since all
```

Without the flag, dispat applies every edit you gave wherever it is declared. It does not matter whether the edit names
a package of this workspace. That lets one invocation pin a third-party range across every package.

With the flag, an invocation on a quiet day does nothing and says so. Use it for a CI job wired to run after every
commit. Reconciling the ranges of whatever you just released is the whole intent.

`dispat autoversion` takes the same flag and means the same thing. It reconciles the declarations pointing at this
run's packages. It leaves a range that had fallen behind an earlier release as it is.

## Applied, skipped and missing across many packages

Each edit ends in one of the three states [the writer reports](./manifests.md#applied-skipped-and-missing). One of them
reads differently once a whole monorepo is involved.

**Missing** means this package's manifest does not declare that dependency. When you name one file, that usually means
a typo. When one invocation covers twenty packages and three declare the dependency, seventeen missing edits are the
shape of the monorepo and none is a problem.

Pass `--strict` to ask the question across the whole run instead of per file. It fails when an edit matched no manifest
anywhere, which is a stale pattern. It stays quiet about an edit that landed somewhere:

```console
$ dispat autowriter --set nowhere=^1.0.0 --strict --since all
...
ERR edit matched no manifest  edit=set:dependencies:nowhere
ERR edits are not clean  error="1 edit(s) matched no manifest"
```

An edit whose declaration already reads exactly as you asked counts as landed. A second run of a command that changed
nothing is still clean.

A derived edit never fails this check. `--strict` asks whether something you asked for found a target. A derived edit
comes from a declaration that already exists, so there is nothing for it to be stale about.

## Lock files

Rewriting a range leaves the lock file next to it out of date. If the space configures
[`syncLock`](../configuration/autoversion.md) scripts, they run afterwards in the packages whose manifests actually
changed, one package at a time. Pass `--sync-lock=false` to leave the lock files to you.

## The pitfall worth knowing

Every step command plans afresh. Once a package has been committed and tagged, the recomputed plan no longer releases
it. The next command over the same package covers nothing:

```console
$ dispat commit --package core --tag
$ dispat autowriter --package core --set left-pad=^2.0.0
INF package is outside the window, nothing to do  package=core
```

That is a success, not a failure. It stops a flow breaking on a quiet day. Name the window yourself when you meant to
reach the package anyway:

```sh
dispat autowriter --package core --set left-pad=^2.0.0 --since all
```

The same applies to `dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github`.

## Failures

dispat writes each file on its own and only when something in it changed. A re-run is a no-op, and a failure halfway
through leaves the files already written in their new state. There is no rollback, so git is the undo.

A failed package skips its dependents, exactly as a failed script does in `dispat run`. Pass `--on-error continue` to
run them anyway. Either way, the command fails at the end.

## Which tool for which job

- One file, one change, no config and no repository needed: [`dispat writer`](./manifests.md#changing-a-manifest).
- One change, every package the plan picks: this command.
- Reconcile every workspace dependency to the versions a release computed, without listing them:
  [`dispat autoversion`](../reference/releasing/steps.md#dispat-autoversion). It reads the graph and works out the
  edits itself, but autowriter applies the edits you name.
- Text no manifest parser understands: [the replacer](./replacer.md).

## Exit codes

| Code | When                                                                              |
|------|-----------------------------------------------------------------------------------|
| `0`  | Everything asked for was applied, skipped or missing                              |
| `1`  | A manifest could not be written, a `{version}` could not be resolved, or `--strict` found a stale edit |
| `2`  | Nothing to write, a malformed `--set` or `--link`, `--link-local` together with `--unlink-local`, or an unknown `--manifests` value |
