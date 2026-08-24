# The run command

Run `dispat run <script>` to plan and execute a named [script](../configuration/spaces.md#scripts-and-dispat-run)
inside each changed package that has one, honouring the dependency graph. Nothing is released or tagged. If a script
fails, dispat skips its dependents or keeps them running according to `--on-error`.

Each package looks the name up in its own `scripts`, then its space's, then the file's. The level you define a name at
decides its reach, so a file-level script runs in every changed package, a space's script runs in that space's
packages, and a package's script runs in that package alone. A selected package with no command for the name does
nothing, while a missing name exits `1` because either no level defines it or none of the selected packages have it.

Selection happens in three steps, in this order:

1. **A window** decides which packages are on the table, defaulting to the changed packages and every changed package
   of a [`versioning: none` space](../reference/releasing/versioning.md#packages-that-never-release-none). Pass
   `--since <rev>` to select the packages the commits in `rev..HEAD` address, using `HEAD~1` for the last commit,
   `origin/main` for a branch, a release tag, or `all` for every package. Selection follows the planner's
   [scope semantics](../reference/commits.md#scope-sets), so a commit's written scopes are authoritative and only
   scopeless units fall back to the files they changed.
2. **The filter** picks from that window using `--package`, `--space`, `--group`, or your current folder, as described
   in [Choosing the packages](#choosing-the-packages). It only ever narrows, so `dispat run build -p core` runs core
   when core changed and nothing at all when it did not. Pass `--since all -p core` to run a script regardless of
   changes, which lets you try a script under its exact stage input while an unchanged package carries its baseline as
   both the old and the new version.
3. **`--consumers`** then expands the result with every package that transitively depends on a selected one, so
   downstream packages re-run with a change the window alone would not reach. A consumer pulled in brings its own
   consumers, and this expansion is deliberately not filtered back out because asking for a package's consumers means
   asking for packages you did not name. The added packages run whether or not they changed, executing after their
   selected providers with the ordinary `--on-error` cascade.

Run `dispat <script>` as a shorthand whenever `<script>` is not a command name. Both spellings take the same flags and
narrow to the same folders.

## Choosing the packages

Three flags name the same thing three ways. Use `--package` (`-p`) to name packages, `--space` (`-s`) to name spaces
and select every package inside them, and `--group` (`-g`) to name
[versioning groups](../reference/releasing/versioning.md) and select every package that versions with the group. All
three are repeatable, comma-separated (`-p core,web`, `-p core -p web`), and matched case-insensitively.

All three accept `*` globs, like `-p '@acme/*'` for a prefix, `-p '*'` for every package, `-s '*'` for every space, or
`-g '*'` for every group. Quote a glob, or the shell expands it first. No word is reserved, so a package named `all` is
selected by `all` and by nothing else. Terms combine by union, so a package named twice over is still selected once.

A space is a folder and a group is a versioning relationship, which is why they are separate flags. A group may hold
packages from several spaces, or a single package out of one. A
[standalone package](../configuration/packages.md#standalone-packages-path) that joined a group is reachable by
`--group` although it belongs to no space at all.

A package that versions on its own belongs to no group. This means `-g '*'` never reaches it.

A term that matches nothing is an error, never an empty selection, because a command that quietly acts on nothing hides
typos. The error names what dispat discovered and looks across the other two flags. Naming a space in `--package`, a
group in `--space`, or a package in `--group` triggers a message pointing to the correct flag.

A standalone package belongs to no space. This makes `--package` (or `-p '*'`) the only way to name one unless it
joined a group. The `-s '*'` flag means every configured space and leaves it out.

With no terms at all, dispat selects the folder you invoked the command from. Inside a package folder it selects that
package, inside a space folder it selects that space, and anywhere else it selects nothing so the command covers its
usual set. The deepest match wins, so a standalone package nested inside another package's folder still selects itself.

A term on the command line always beats the folder you typed it in. A group is never inferred this way because no
folder is a group. You must use `--group` to name one.

Eleven commands read the same selection: `release`, `status`, `run`, `preview`, `changelog`, `autoversion`,
`autowriter`, `autoreplacer`, `commit`, `github` and `compute`. What each command *does* with the selection differs. A
release additionally respects publish order, described in [Releasing part of the graph](./release.md), but which
packages a term picks out never changes.

## Passing arguments to the script

Pass arguments after `--` to send them to the script instead of to dispat:

```sh
dispat run test -- --watch
dispat test -- --watch          # the shorthand does the same
```

With `"test": "vitest run"` in your config, both of those commands run `vitest run --watch`. The arguments are
**appended to the command text**, matching what `npm run test -- --watch` does. You do not have to rewrite anything in
your config to accept them.

Three things follow from that. Know them before you rely on this feature.

**Every covered package gets them.** A run is one intent about a selection, so `dispat run test -- --watch` puts
`--watch` on the test script of every covered package. Narrow the selection with `--package` if you only want to target
one.

**They land at the end of the command.** A script that is a single command takes them where you expect. A script that
ends in something else does not:

```json
{
  "scripts": {
    "test": "vitest run",              // dispat run test -- --watch  →  vitest run --watch
    "check": "npm run lint; vitest run" // →  npm run lint; vitest run --watch
  }
}
```

The second script still works, but only because the argument landed on the command it was meant for. If a script ends
in a command that should not receive arguments, wrap the part that should. Use `sh -c 'vitest run "$@"' _` to control
where the arguments go.

**On a [multi-command script](../configuration/scripts.md#one-name-several-commands), only the last command takes
them.** The last command does the script's work. The commands before it are prerequisites, and they would break if they
took the arguments too:

```json
{
  "scripts": {
    "test": ["npm ci", "vitest run"]   // dispat run test -- --watch
  }                                    //   →  npm ci
}                                      //   →  vitest run --watch
```

If the command that should receive the arguments is not the last one, reorder the script or split it in two.

You must include the `--`. A bare word after the script name is an error, because you choose packages with flags:

```sh
dispat run test core        # error: the selection is a flag
dispat run test -p core     # this is how you narrow it
dispat run test -- core     # and this passes "core" to the script
```

dispat quotes arguments carrying spaces or shell characters for you, so `dispat run test -- --filter 'my suite'`
arrives as one argument. Only `run` and `exec` forward arguments. Every other command refuses a `--` rather than
ignoring it.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Use with every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autoreplacer`, `commit`, `github`, `compute`) and `if --changed` to narrow to the named packages. The flag is repeatable, comma-separated, matched case-insensitively, and accepts `*` globs (`-p '*'` is every package). See [Choosing the packages](#choosing-the-packages).                     |
| `--space`, `-s`       |             | Use with the same commands to narrow to every package of the named spaces, using the same spellings. A standalone package belongs to no space. See [Choosing the packages](#choosing-the-packages).            |
| `--group`, `-g`       |             | Use with the same commands to narrow to every package of the named [versioning groups](../reference/releasing/versioning.md), using the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces. See [Choosing the packages](#choosing-the-packages).            |
| `--since`             |             | Use with the seven sweeping commands and `if --changed` to cover the packages the commits since a git revision address, instead of the release window. Pass `all` to cover every package. See [the run command](./run.md).                |
| `--consumers`         |             | Use with the seven sweeping commands and `if --changed` to additionally cover every package that transitively depends on a selected one. For `if`, the expansion runs before the selection narrows. See [the if command](./if.md#changed-packages).                                                          |
| `--on-error`          | `skip`      | Use with every sweeping command (`run`, `autowriter`, `autoreplacer`, `changelog`, `autoversion`, `commit`, `github`) to control what a failed package does to its dependents. Choose `skip` (transitive) or `continue`. Either way, the command exits `1` on any failure.                                         |
