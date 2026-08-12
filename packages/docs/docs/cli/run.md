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

1. **A window** decides which packages are on the table. By default that is the changed packages, the same set a
   release would process. `--since <rev>` instead selects the packages the commits in `rev..HEAD` address: `HEAD~1`
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
selects every package of one. `--group` (`-g`) names [versioning groups](../releasing/versioning.md), and selects every package
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

Nine commands read the same selection: `release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `commit`,
`github` and `compute`. What each of them *does* with it differs (a release additionally has to respect publish
order, described in [Releasing part of the graph](./release.md)), but which packages a term picks out
never does.

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--package`, `-p`     |             | Every package-selecting command (`release`, `status`, `run`, `preview`, `changelog`, `autoversion`, `autowriter`, `autosubstitute`, `commit`, `github`, `compute`): narrow to the named packages. Repeatable and comma-separated, matched case-insensitively, `*` globs (`-p '*'` is every package); see [Choosing the packages](#choosing-the-packages).                     |
| `--space`, `-s`       |             | The same eleven commands: narrow to every package of the named spaces, with the same spellings. A standalone package belongs to no space; see [Choosing the packages](#choosing-the-packages).            |
| `--group`, `-g`       |             | The same eleven commands: narrow to every package of the named [versioning groups](../releasing/versioning.md), with the same spellings. A group is a `versionGroups` entry or a space that versions as one, so it may cross spaces; see [Choosing the packages](#choosing-the-packages).            |
| `--since`             |             | The same seven commands: cover the packages the commits since a git revision address, instead of the release window. `all` covers every package; see [the run command](./run.md).                |
| `--consumers`         |             | The same seven commands: additionally cover every package that transitively depends on a selected one; see [the run command](./run.md).                                                          |
| `--on-error`          | `skip`      | Every sweeping command (`run`, `autowriter`, `autosubstitute`, `changelog`, `autoversion`, `commit`, `github`): what a failed package does to its dependents, `skip` (transitive) or `continue`. Either way the command exits `1` on any failure.                                         |
