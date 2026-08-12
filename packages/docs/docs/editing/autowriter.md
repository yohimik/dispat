# Editing manifests across the monorepo

`dispat writer` edits the manifests you name. That is exactly what you want for one file, and exactly what you do not
want for forty: you would have to know which packages exist, where each of them keeps its manifest, and which of them
even declare the dependency you are changing.

`dispat autowriter` asks the same question the rest of dispat asks. It works out which packages the run covers, finds
each one's manifests itself, and applies your edits to all of them:

```console
$ dispat autowriter --set @acme/core=^1.3.0
packages/web/package.json
  applied  dependencies  @acme/core  ^1.3.0
packages/admin/package.json
  applied  dependencies  @acme/core  ^1.3.0
packages/core/package.json
2 package(s): 2 applied, 0 skipped, 0 missing
```

The edits are `dispat writer`'s, spelled identically. The packages are `dispat run`'s, chosen identically. Nothing new
to learn on either side.

## Which packages it covers

By default, the packages the plan is releasing, in dependency order. To choose differently, use the flags every dispat
command uses:

```sh
dispat autowriter --set left-pad=^2.0.0                     # every releasing package
dispat autowriter --set left-pad=^2.0.0 --package web       # just web
dispat autowriter --set left-pad=^2.0.0 --space libs        # every package of the libs space
dispat autowriter --set left-pad=^2.0.0 --group platform    # every package of the platform version group
dispat autowriter --set left-pad=^2.0.0 --since all         # every package in the monorepo
```

Running it from inside a package folder with no flags narrows it to that package. The full rule, including how the
window and the filter compose, is in [Choosing the packages](../cli/run.md#choosing-the-packages).

`--since all` is the one you will reach for most, because "bump this dependency everywhere" usually has nothing to do
with what is releasing today.

## Which manifests it edits

Each covered package is scanned, and every manifest that turned up is edited. `--manifests` decides how far that scan
goes:

| Value            | What it reads                                                            |
|------------------|--------------------------------------------------------------------------|
| `root` (default) | The manifests sitting directly in the package folder                     |
| `all`            | Every manifest anywhere under the package folder                         |

`root` is what you want almost always. `all` is for a package that keeps a second manifest inside itself, an example
project or a fixture, that has to move with the rest.

Under `all`, a manifest belonging to another package is left to that package, even when its folder sits inside this
one. A file is only ever written by the package that owns it.

A folder with no manifest anything can write is skipped quietly. If none of the covered packages has one, that is an
error, because writing nothing without saying so is how a mistyped selection hides.

## Writing a version the run just worked out

The plan is already computed by the time anything is written, so an edit can ask for a version instead of stating one.
Write `{version}` and it is filled in:

```sh
dispat autowriter --set @acme/core='^{version}' --set-version '{version}' --since all
```

In a `--set` range, `{version}` is the planned version of the package that edit names. In `--set-version`, it is the
covered package's own. Both fall back to the version the package already has when the run is not releasing it, so a
package with nothing pending keeps the number it had.

Quote it. `{version}` means something to most shells if you do not.

A `--set` whose name belongs to no package of the workspace cannot be templated, and asking for one is refused before
any file is opened. That is deliberate: a literal `{version}` written into a manifest is a problem someone finds weeks
later.

`--set-version` only ever writes the package's root manifests. A nested example has its own version story, and stamping
the release version into it would be wrong however far the scan went.

## Letting it work out the edits itself

Everything above needs you to name a dependency. In a monorepo that is often
the tedious part: you know that every package depending on something here
should follow it, and typing them out is just bookkeeping.

Three flags derive the edits instead. Each one looks at what the manifests
already declare and acts on every dependency that names another package in this
workspace:

```sh
dispat autowriter --set-local                 # every workspace range follows its provider
dispat autowriter --set-local --range caret   # and is spelled with a caret
dispat autowriter --link-local                # every workspace dependency points at its folder
dispat autowriter --unlink-local              # and back again
```

`--range` says how to spell what `--set-local` writes. It takes the same words
[`autoVersion.range`](../configuration/spaces.md#autoversion) takes: `caret`,
`tilde`, `exact`, or a template like `>={version}`. Leave it out and you get a
caret.

The keywords know about ecosystems. A go.mod gets `v1.3.0`, a Python project
gets `==1.3.0`, and a Docker tag gets a bare `1.3.0`, because a caret is not
something a registry can resolve. A template is you spelling the range
yourself, so it is written exactly as given. Over a workspace that mixes
ecosystems, prefer a keyword.

A dependency you name with `--set` or `--link` keeps what you asked for. The
command line is the more specific instruction, so the derived edit steps aside.

`--set-local` and `--link-local` come from one reading of the manifests, so you
can ask for both at once and each declaration is visited a single time.

### Working against local folders

`--link-local` is for working on several packages at once. It writes the
directive that points a dependency at its folder in this repository, so your
checkout of one package builds against your checkout of another instead of
against the registry. `--unlink-local` removes exactly what it wrote.

Only five manifest formats have such a directive: `go.mod`, `Cargo.toml`,
`pubspec.yaml`, `pyproject.toml` and `package.json`. See
[Redirecting a dependency](./manifests.md) for how each one spells it.

`package.json` is the exception here. npm refuses an override for a dependency
the manifest depends on directly unless the two specs match exactly, and that
is precisely the case these flags produce, so a derived link skips npm
manifests and says so. An explicit `--link name=path` still writes one, because
there you have said which dependency you mean.

:::warning
A local link must not be published. Nothing in a release removes one, so a link
left in place ships a manifest your consumers cannot resolve. Run
`dispat autowriter --unlink-local` before you release.
:::

### When to use this instead of autoversion

`dispat autoversion` reconciles ranges too, and it is what a release runs. The
difference is where the rules come from.

`autoversion` reads the space's configuration: which dependency kinds count,
which ranges are protected, which providers are in scope. It is the policy your
releases follow.

`--set-local` reads no configuration at all. Every declaration naming a package
here is reconciled, spelled by `--range`, narrowed only by `--only-updated`.
That makes it the tool for a one-off pass by hand, and it means the two can
disagree: if you want them to match, pass the `--range` your space configures.

## Only what this run updates

`--only-updated` keeps only the edits whose name is a package this run is releasing:

```sh
dispat autowriter --set @acme/core='^{version}' --only-updated --since all
```

Without it, every edit you gave is applied wherever it is declared, whether or not it names a package of this workspace
at all. That is what lets one invocation pin a third-party range across every package.

With it, an invocation on a quiet day simply does nothing and says so. That makes it the flag for a CI job wired to run
after every commit, where "reconcile the ranges of whatever we just released" is the whole intent.

`dispat autoversion` takes the same flag, meaning the same thing: reconcile the declarations pointing at this run's
packages, and leave a range that had fallen behind an earlier release as it is.

## Applied, skipped and missing across many packages

Each edit ends in one of the three states [the writer reports](./manifests.md#applied-skipped-and-missing), and one of
them reads differently once a whole monorepo is involved.

**Missing** means this package's manifest does not declare that dependency. When you name one file, that usually means
a typo. When one invocation covers twenty packages and three of them declare the dependency, seventeen missing edits
are simply the shape of the monorepo, and none of them is a problem.

So `--strict` asks the question across the whole run instead of per file. It fails when an edit matched no manifest
anywhere, which is a pattern that has gone stale, and stays quiet about an edit that landed somewhere:

```console
$ dispat autowriter --set nowhere=^1.0.0 --strict --since all
...
ERR edit matched no manifest  edit=set:dependencies:nowhere
ERR edits are not clean  error="1 edit(s) matched no manifest"
```

An edit whose declaration already reads exactly as you asked counts as landed, so a second run of a command that
changed nothing is still clean.

A derived edit never fails this check. `--strict` asks whether something you
asked for found a target, and a derived edit comes from a declaration that
already exists, so there is nothing for it to be stale about.

## Lock files

Rewriting a range leaves the lock file next to it out of date. If the space configures
[`syncLock`](../configuration/spaces.md#autoversion) scripts, they run afterwards, in the packages whose manifests
actually changed, one package at a time. Pass `--sync-lock=false` to leave the lock files to you.

## The pitfall worth knowing

Every step command plans afresh. Once a package has been committed and tagged, the recomputed plan no longer releases
it, so the next command over the same package covers nothing:

```console
$ dispat commit --package core --tag
$ dispat autowriter --package core --set left-pad=^2.0.0
INF package is outside the window, nothing to do  package=core
```

That is a success, not a failure, and it is what stops a flow breaking on a quiet day. When you meant to reach the
package anyway, name the window yourself:

```sh
dispat autowriter --package core --set left-pad=^2.0.0 --since all
```

The same applies to `dispat changelog`, `dispat autoversion`, `dispat commit` and `dispat github`.

## Failures

Each file is written on its own and only when something in it changed, so a re-run is a no-op and a failure halfway
through leaves the files already written in their new state. There is no rollback: git is the undo.

A failed package skips its dependents, exactly as a failed script does in `dispat run`. `--on-error continue` runs them
anyway. Either way the command fails at the end.

## Which tool for which job

- One file, one change, no config and no repository needed: [`dispat writer`](./manifests.md#changing-a-manifest).
- One change, every package the plan picks: this command.
- Reconcile every workspace dependency to the versions a release computed, without listing them:
  [`dispat autoversion`](../releasing/steps.md#dispat-autoversion). It reads the graph and works out the edits itself; autowriter
  applies the edits you name.
- Text no manifest parser understands: [the replacer](./replacer.md).

## Exit codes

| Code | When                                                                              |
|------|-----------------------------------------------------------------------------------|
| `0`  | Everything asked for was applied, skipped or missing                              |
| `1`  | A manifest could not be written, a `{version}` could not be resolved, or `--strict` found a stale edit |
| `2`  | Nothing to write, a malformed `--set` or `--link`, `--link-local` together with `--unlink-local`, or an unknown `--manifests` value |
