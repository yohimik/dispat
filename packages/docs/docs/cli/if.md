# The if command

Use `dispat if` to choose a shell command based on an environment variable,
a file or directory, or changed packages. It is useful for a conditional step
inside a JSON or YAML configuration.

For related tasks, `exec` calls a declared script and `for` repeats a command:

```console
$ dispat if CI --then 'make ci' --else 'make dev'
$ dispat exec build --for pkg:core
$ dispat for core web --do 'make "$DISPAT_ITEM"'
```

These helpers run the commands you provide without starting a release. Options
such as `--changed` read the repository to decide which work is needed.

## Which command do I want

| You want to                                        | Use                          |
|----------------------------------------------------|------------------------------|
| run a script in every changed package, in order (`--since all` for every package) | [`dispat run <script>`](./run.md) |
| run one declared script, once, right here           | [`dispat exec <script>`](./exec.md) |
| choose between shell commands based on a condition  | [`dispat if <cond>`](./if.md) |
| run one shell command per item of a list            | [`dispat for <item>...`](./for.md) |

`dispat run` knows about your monorepo. It computes a plan, finds which
packages changed, and runs the script in each of them in dependency order;
`--since all` widens the sweep to every package, changed or not.
`dispat exec` ignores the dependency graph. It looks up one script by name and
runs it. Call `dispat exec` when you are inside a stage script and need to run
another script.

[`dispat for`](./for.md) sits between the two. Like `dispat run` it can visit
every changed package, but it runs the shell text you typed rather than a
script each package declares, one item at a time, in the folder you invoked it
from, with the item described by `DISPAT_*` variables. Use `dispat run` when
each package says what the work is; use `dispat for` when the command line
does, or when the list is not packages at all.

## dispat if

```
dispat if <cond>      --then <script> [--elif <cond> --then <script>]... [--else <script>]
dispat if -f <path>   --then <script> ...
dispat if -d <path>   --then <script> ...
dispat if --changed [--since <rev>] [-p <pkg>] [-s <space>] [-g <group>] [--consumers] --then <script> ...
```

The leading condition takes the first `--then`. Each `--elif` takes the next
`--then`. The `--else` script runs when nothing else matches.

```sh
dispat if 'ENV=prod'      --then 'deploy prod' \
       --elif 'ENV=stage' --then 'deploy stage' \
       --else               'echo nothing to deploy'
```

The first condition that holds wins. dispat skips the rest without looking at
them. A chain of `--elif` flags acts as a switch, and `--else` is its default
case.

If nothing matches and you omit `--else`, nothing runs and the command exits `0`.

Write each branch as a full shell command. To call a declared script from a
branch, use `dispat exec <script>`.

Choose one leading condition: positional text, `--changed`, `--file`, or
`--dir`. Combining them is a usage error. Every `--elif` tests an environment
condition.

### Environment conditions

| Condition       | True when                                          |
|-----------------|-----------------------------------------------------|
| `NAME`          | the variable is set and not empty                   |
| `!NAME`         | the variable is unset, or set to nothing            |
| `NAME=value`    | it is exactly that value                            |
| `NAME!=value`   | it is anything else                                 |
| `NAME~glob`     | it matches the pattern, where `*` matches anything  |
| `NAME!~glob`    | it does not match the pattern                       |

Here, "set" means non-empty, like `[ -n "$NAME" ]` in a shell. To test for an
empty value, use `NAME=`. An unset variable also expands to an empty value.

The value can contain anything, including operators. Only the first operator
ends the variable name. The condition `URL=a~b` asks whether `URL` equals the
exact text `a~b`, not whether it matches a glob.

Globs use the same matcher as everywhere else in dispat. A `*` matches any run
of characters, including slashes.

```sh
dispat if 'BRANCH~release/*' --then 'dispat release'
```

These conditions read only the environment given to the command. dispat loads
no config file and requires no repository. You can run `dispat if` anywhere.

### File tests

The `--file` (`-f`) and `--dir` (`-d`) flags ask the filesystem instead.

| Condition     | True when                                       |
|---------------|--------------------------------------------------|
| `-f <path>`   | the path exists and is a regular file            |
| `-d <path>`   | the path exists and is a folder                  |

```sh
dispat if -f data/report.json --then 'npm run build-docs' --else 'echo no report yet'
```

A path that is absent or the wrong kind makes the condition false. This is
never an error. It matches how `[ -f ]` and `[ -d ]` behave in the shell.
Symbolic links are followed, so a link to a file passes `-f`.

A relative path resolves against the folder the chosen script runs in. This is
the invocation folder, or wherever [`--in`](#choosing-the-folder-it-runs-in)
points. The test and a path written inside the script text always mean the same
file. An absolute path resolves exactly as written.

File tests read no config file and need no repository.

### Changed packages

Use `--changed` to run a branch when the selected packages have changes. It
uses [`dispat run`'s](./run.md) change detection and selection rules, with one
difference in how `--consumers` is applied, explained below.

```sh
dispat if --changed -p docs --since origin/main --then 'dispat run build-docs'
```

Pass `--since <rev>` to set the window to what the commits since that revision
address. This uses the same scope semantics as
[`dispat run --since`](./run.md). A commit's written scopes are authoritative,
and only scopeless commits fall back to the files they changed.

Without `--since`, the window is the release window, which contains the
packages with something pending. A bare `dispat if --changed` asks whether a
release would do anything. Pass `--since all` to select every package, changed
or not.

The `--consumers` flag expands the window downstream before the selection
narrows it. The gate asks whether the selection is among what the changes
reach.

```sh
dispat if --changed -p web --consumers --since HEAD~1 --then 'dispat run e2e -p web'
```

This holds when `web` changes, or when anything `web` transitively consumes
changes. This is the only place `--consumers` composes in that order. A sweep
like `dispat run` narrows first and expands after, asking for the selection's
dependents.

A gate doing the same would find `--consumers` unable to change its answer.
Expanding a selection never empties it, and it never fills an empty one.

The `--package`/`-p`, `--space`/`-s`, and `--group`/`-g` flags narrow the
answer the way they narrow every command. This includes the invocation folder.
Run the command inside a package folder with no terms, and the gate asks about
that package alone. An empty selection evaluates to false. A term that matches
no package at all is an error. A gate reading a typo as false would silently
never fire.

Asking about the repository requires work. The `--changed` flag reads the
config file, walks the tags, and parses the commits. This is the same work
`dispat status` does, while every other condition reads nothing.

The command exits `1` when `--changed` cannot be evaluated. This happens when
git cannot resolve a revision or dispat cannot load the configuration. The
selection flags belong to `--changed`. Passing any of them with another
condition is a usage error.

### Nesting

A branch is shell text. You can put another dispat command inside one.

```sh
dispat if CI --then 'dispat if TIER=gold --then "deploy gold" --else "deploy standard"'
```

## Choosing the folder it runs in

The chosen branch runs in your current folder. A relative path inside the
branch resolves from there. Use `--in` to send it somewhere else.

```console
$ dispat if CI --then 'make ci' --in ./build
$ dispat if CI --then 'make ci' --in pkg:core
```

Pass a folder path or any of the [place names](./locations.md) `dispat exec`
takes. A relative `--file` or `--dir` path moves with it. The test asks about
the folder the script actually runs in.

Environment and file conditions need no configuration when `--in` is a path
or `cwd`. Naming `pkg:`, `space:`, or `root` requires the configuration to
resolve that location. The `--changed` condition also reads configuration and
Git history.

A folder that does not exist stops the command and prints a message naming the
folder.

## Exit codes

`if` returns the chosen branch's exit code. For example,
`dispat if CI --then 'exit 7'` exits `7` when `CI` is set and non-empty.
[`dispat exec`](./exec.md) follows the same rule.

The `--on-failure` flag changes this behavior. It runs when the chosen script
fails. Its own exit code becomes the command's exit code.

```console
$ dispat exec deploy --on-failure 'notify-slack "deploy failed"; exit 1'
```

The failure script runs even when you kill the first script with Ctrl-C. Your
cleanup still gets a chance to run.

An exit code of `2` means the command line itself was invalid. Keep this in
mind if your script also exits `2`. End `--on-failure` with an explicit
`exit 1` to remove the ambiguity.

A false condition runs its `--else` branch, or nothing, and exits accordingly.
Being false is not a failure.

The `--changed` condition fails when dispat cannot evaluate it. The command
exits `1` when git cannot resolve a revision or dispat cannot load the
configuration.

## Flags

### `--then <script>`

The script the preceding condition runs. Repeatable, one per condition.

### `--elif <cond>`

Another condition, tried when every earlier one was false. Repeatable.

### `--else <script>`

The script to run when no condition held.

### `--file <path>`, `-f`

The leading condition: the path exists and is a regular file.

### `--dir <path>`, `-d`

The leading condition: the path exists and is a folder.

### `--changed`

The leading condition: changed packages are selected.

### `--since <rev>`

With `--changed`: count changes from this revision instead of the release
window; `all` selects every package.

### `--consumers`

With `--changed`: expand the window to everything downstream of the changes.

### `-p`, `-s`, `-g`

With `--changed`: narrow to the named packages, spaces or groups.

### `--in <folder>`

Run the chosen script in this folder: a path, or any
[place name](./locations.md).

### `--on-failure <script>`

Run this when the chosen script fails, and exit with its code instead.

The command needs no config file and no git repository. This changes only when
`--in` names a package, a space, or the root, or when `--changed` asks about
the repository.

The shell is `/bin/sh -c`. There is no config file to take a `shell` setting
from.
