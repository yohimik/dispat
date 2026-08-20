# The run command

`dispat run <script>` plans, then runs the named
[script](../configuration/spaces.md#scripts-and-dispat-run) inside each changed package that has one, honouring the
dependency graph. Nothing is released or tagged. A failing script's dependents are skipped or kept running per
`--on-error`.

Each package looks the name up in its own `scripts`, then its space's, then the file's. The level you define a name at
is therefore what decides the reach: a file-level script runs in every changed package, a space's in that space's
packages, a package's in that package alone. A selected package with no command for the name does nothing. Exit `1`
means either that no level defines the name, or that none of the selected packages have it.

Selection happens in three steps, in this order:

1. **A window** decides which packages are on the table. By default that is the changed packages: the set a release
   would process, plus every changed package of a
   [`versioning: none` space](../reference/releasing/versioning.md#packages-that-never-release-none), which runs
   scripts without ever releasing. `--since <rev>` instead selects the packages the commits in `rev..HEAD` address: `HEAD~1`
   for the last commit (per-commit CI), `origin/main` for this branch's own commits (PR pipelines), a release tag, or
   the reserved `all` for every package, changed or not. Selection follows the planner's
   [scope semantics](../reference/commits.md#scope-sets): a commit's written scopes are authoritative, and only scopeless units
   fall back to the files they changed.
2. **The filter** picks from that window: `--package` / `--space` / `--group`, or the folder you are standing in, as described in
   [Choosing the packages](#choosing-the-packages). It only ever narrows, so `dispat run build -p core` runs core when
   core changed and nothing at all when it did not. `--since all -p core` is how you run a script in a package
   regardless, and the way to try one script under the exact input its stage would give it, without releasing anything.
   An unchanged package carries its baseline as both the old and the new version.
3. **`--consumers`** then expands the result with every package that transitively depends on a selected one (a
   consumer pulled in brings its own consumers), so downstream packages re-run with a change the window alone would
   not reach. The expansion is deliberately not filtered back out: asking for a package's consumers is asking for
   packages you did not name. The added packages run whether or not they changed, after their selected providers, with
   the ordinary `--on-error` cascade.

`dispat <script>` is a shorthand whenever `<script>` is not a command name. Both spellings take the same flags and
narrow to the same folders.

## Choosing the packages

Three flags name the same thing three ways. `--package` (`-p`) names packages. `--space` (`-s`) names spaces, and
selects every package of one. `--group` (`-g`) names [versioning groups](../reference/releasing/versioning.md), and selects every package
that versions with the rest of the group. All three are repeatable and comma-separated (`-p core,web`,
`-p core -p web`), matched case-insensitively, and all three accept `*` globs: `-p '@acme/*'` for a prefix, `-p '*'`
for every package, `-s '*'` for every space, `-g '*'` for every group. Quote a glob, or the shell expands it first. No
word is reserved: a package named `all` is selected by `all` and by nothing else. Terms combine by union, so a package
named twice over is still selected once.

A space is a folder and a group is a versioning relationship, which is why they are separate flags. A group may hold
packages from several spaces, or a single package out of one, and a
[standalone package](../configuration/packages.md#standalone-packages-path) that joined a group is reachable by
`--group` although it belongs to no space at all. A package that versions on its own belongs to no group, so `-g '*'`
never reaches it.

A term that matches nothing is an error, never an empty selection, because a command that quietly acts on nothing is
how a typo hides. The error names what was discovered, and looks across the other two flags: naming a space in
`--package`, a group in `--space`, or a package in `--group` says so and points at the flag that reaches it. A
standalone package belongs to no space, so `--package` (or `-p '*'`) is the only way to name one unless it joined a
group; `-s '*'` means every configured space and leaves it out.

With no terms at all, the folder the command was invoked from is the selection: inside a package folder (or any
subdirectory of it) that package, inside a space folder that space, anywhere else, the monorepo root included,
nothing, so the command covers its usual set. The deepest match wins, so a standalone package nested inside another
package's folder still selects itself. A term on the command line always beats the folder it was typed in. A group is
never inferred this way, because no folder is a group; `--group` is the only way to name one.

Eleven commands read the same selection: `release`, `status`, `run`, `preview`, `changelog`, `autoversion`,
`autowriter`, `autoreplacer`, `commit`, `github` and `compute`. What each of them *does* with it differs (a release
additionally has to respect publish order, described in [Releasing part of the graph](./release.md)), but which
packages a term picks out never does.

## Passing arguments to the script

Everything after `--` goes to the script instead of to dispat:

```sh
dispat run test -- --watch
dispat test -- --watch          # the shorthand does the same
```

With `"test": "vitest run"` in your config, both of those run `vitest run --watch`. The arguments are **appended to
the command text**, which is the same thing `npm run test -- --watch` does, so nothing in your config has to be
rewritten to accept them.

Three things follow from that, and all of them are worth knowing before you rely on it.

**Every covered package gets them.** A run is one intent about a selection, so `dispat run test -- --watch` puts
`--watch` on the test script of every package the run covers, not just the first. Narrow it with `--package` if that
is not what you meant.

**They land at the end of the command.** A script that is a single command takes them where you expect. One that ends
in something else does not:

```json
{
  "scripts": {
    "test": "vitest run",              // dispat run test -- --watch  →  vitest run --watch
    "check": "npm run lint; vitest run" // →  npm run lint; vitest run --watch
  }
}
```

The second still works, but only because the argument happened to land on the command it was meant for. If a script
ends in something that should not receive them, wrap the part that should: `sh -c 'vitest run "$@"' _`.

**On a [multi-command script](../configuration/scripts.md#one-name-several-commands), only the last command takes
them.** The last command is the script's work; the ones before it are what had to happen first, and would break if they
took the arguments too:

```json
{
  "scripts": {
    "test": ["npm ci", "vitest run"]   // dispat run test -- --watch
  }                                    //   →  npm ci
}                                      //   →  vitest run --watch
```

If the command that should receive them is not the last one, reorder the script or split it in two.

A `--` is required. A bare word after the script name is still an error, because packages are chosen with flags:

```sh
dispat run test core        # error: the selection is a flag
dispat run test -p core     # this is how you narrow it
dispat run test -- core     # and this passes "core" to the script
```

Arguments carrying spaces or shell characters are quoted for you, so `dispat run test -- --filter 'my suite'` arrives
as one argument. Only `run` and `exec` forward; every other command refuses a `--` rather than ignoring it.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`), and `if --changed`: narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](#choosing-the-packages).            |
| `--group`, `-g`       |             | The same commands: narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](#choosing-the-packages).            |
| `--since`             |             | The seven sweeping commands and `if --changed`: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The seven sweeping commands and `if --changed`: additionally cover every package that transitively depends on a selected one. For `if`, the expansion runs before the selection narrows; see [the if command](./if.md#changed-packages).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
