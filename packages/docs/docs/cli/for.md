# The for command

`dispat for` runs a script once for each item of a list. It is the shell's own
`for x in ...; do ...; done`, spelled so that it means the same thing under
every shell your configuration names. The list is a set of words you type, the
packages, spaces or versioning groups you select, or the packages that changed.

Everything dispat runs is a shell command. A loop written inside one is the
first thing that breaks when the `shell` setting moves, because the loop syntax
belongs to the shell rather than to dispat.

```sh
for pkg in core web api; do npm publish "$pkg"; done
```

That line runs under `sh` and `bash`. It does not run under `cmd /C`, and it
does not survive being pasted into a JSON config file without care. The same
work as a dispat loop reads:

```sh
dispat for core web api --do 'npm publish "$DISPAT_ITEM"'
```

dispat splits nothing and interprets nothing. Each item is one argument, and
each `--do` script is one shell string, run through the shell your config
names.

## Where the list comes from

A loop takes exactly one source. Two sources would leave one of the lists
silently unvisited, so naming two is a usage error.

```
dispat for <item>...          --do <script>
dispat for -p <globs>         --do <script>
dispat for -s <globs>         --do <script>
dispat for -g <globs>         --do <script>
dispat for --changed   [--since <rev>] [--consumers] [-p|-s|-g <globs>] --do <script>
dispat for --unchanged [--since <rev>] [--consumers] [-p|-s|-g <globs>] --do <script>
dispat for --since <rev>      [--consumers] [-p|-s|-g <globs>] --do <script>
```

| Source                | The list is                                                      |
|-----------------------|------------------------------------------------------------------|
| positional items      | the words you typed, in that order                               |
| `--package`, `-p`     | the packages the terms name, in discovery order                  |
| `--space`, `-s`       | the spaces themselves, by name                                   |
| `--group`, `-g`       | the versioning groups themselves, by name                        |
| `--changed`           | the changed packages, in dependency order                        |
| `--unchanged`         | the packages `--changed` leaves out, in dependency order         |
| `--since <rev>`       | `--changed --since <rev>`, spelled as [`dispat run`](./run.md) spells it |

The `-s` and `-g` sources iterate over the spaces and the groups themselves,
not over the packages inside them. A loop over the three packages of a space is
not the job a loop over the space is, and `--since all -s libs` is how you ask
for the first: the window that selects every package, narrowed to the space.

### The selection flags mean two things

Under `--changed`, `--unchanged` or `--since`, the `-p`, `-s` and `-g` flags
stop being the source and become the narrowing they are on every other command.
This is the same composition [`dispat if --changed`](./if.md#changed-packages)
uses.

```sh
dispat for -s libs         --do 'echo "$DISPAT_ITEM"'   # one item: the space
dispat for --changed -s libs --do 'echo "$DISPAT_ITEM"' # the changed packages of libs
```

Without a window flag, naming two of them at once has no meaning and is
refused. With one, they compose as usual.

## The iterator variables

Each iteration exports the item and its position. A package, space or group
item also exports what it is.

| Variable          | In                       | Value                                              |
|-------------------|--------------------------|----------------------------------------------------|
| `DISPAT_ITEM`     | every loop               | The item: the word, or the package, space or group name. |
| `DISPAT_INDEX`    | every loop               | The item's position, counting from `0`.            |
| `DISPAT_TOTAL`    | every loop               | The number of items in the list.                   |
| `DISPAT_PACKAGE`  | packages, changed, unchanged | The package name, the same variable a release stage reads. |
| `DISPAT_SPACE`    | packages, spaces, changed, unchanged | The space the item belongs to, or the space itself. |
| `DISPAT_DIR`      | packages, spaces, changed, unchanged | The item's own folder, absolute.       |
| `DISPAT_GROUP`    | packages, groups, changed, unchanged | The versioning group, or the group itself. |

The names are the [release environment's](../reference/environment.md) own, so
a script written for a stage runs unchanged inside a loop.

`DISPAT_GROUP` is left **unset**, not empty, for a package that versions on its
own. An independent package is not a member of a group called `""`, and
`${DISPAT_GROUP+x}` is how a shell tells the two apart. A group item always
carries it.

These variables are added after everything else, so nothing can shadow them. A
`DISPAT_ITEM` left over in the environment by an enclosing run loses to the
loop's own, which is what makes a loop safe to nest inside a release stage.

## Where each iteration runs

Every iteration runs in the folder you invoked the command from. No item is
entered. A relative path inside the script therefore means one thing for the
whole list.

```sh
dispat for -p '*' --do 'echo "$DISPAT_ITEM" >> covered.txt'
```

That appends to one file, in the invocation folder, once per package. The
item's own folder is exported as `DISPAT_DIR` instead, so a script that wants
it says so.

```sh
dispat for -p '*' --do 'cd "$DISPAT_DIR" && npm pack'
```

Use `--in` to move the whole loop somewhere else. It takes a folder path or any
of the [place names](./locations.md).

```sh
dispat for a b --do 'make "$DISPAT_ITEM"' --in ./build
dispat for a b --do 'make "$DISPAT_ITEM"' --in pkg:core
```

## Running several scripts per item

`--do` is repeatable. The scripts of one item are that item's sequence: they
run in order, and the first one that fails ends the item.

```sh
dispat for core web \
  --do 'npm run build --workspace "$DISPAT_ITEM"' \
  --do 'npm publish --workspace "$DISPAT_ITEM"'
```

The publish runs only if the build succeeded, for each item on its own.

## Failure and exit codes

The loop is sequential. A shell's `for` runs one body at a time, a script
written against one is entitled to assume it, and concurrency over a selection
is what [`dispat run`](./run.md) already is.

The first failing item ends the loop, and its exit code becomes the command's.
This keeps the loop transparent in a pipeline.

```console
$ dispat for a b c --do '[ "$DISPAT_ITEM" != b ] || exit 7'
$ echo $?
7
```

Pass `--keep-going` to run the remaining items anyway. The command still
reports the first failure's code, because a later item succeeding says nothing
about the one that did not.

The `--on-failure` flag runs one script after a failed loop and replaces the
exit code with its own. It runs once for the loop, not once per failing item,
and it runs even when Ctrl-C ended the loop, so your cleanup still gets a
chance.

```sh
dispat for -p '*' --do 'deploy "$DISPAT_ITEM"' \
  --on-failure 'notify-slack "deploy failed"; exit 1'
```

An exit code of `2` means the command line itself was invalid. A `1` means
dispat could not do what you asked: a term matching no package, a folder that
is not there, a `--changed` it cannot evaluate, or `--require-items` refusing
an empty list.

## An empty list

A list with nothing in it runs the body zero times and exits `0`. This is what
`for x in $EMPTY` does in a shell, and it holds for every source. A `--changed`
window with nothing pending is an answer, not a failure.

```console
$ dispat for --do 'echo never'
$ echo $?
0
```

Pass `--require-items` when the list holding something was the point. An empty
iteration then exits `1` instead, which is the CI gate.

```sh
dispat for --changed --require-items --do 'dispat exec smoke --for "pkg:$DISPAT_ITEM"'
```

A term that matches no package at all is always an error, with or without the
flag. A loop that quietly ran zero times is how a typo hides.

## Changed and unchanged packages

The `--changed` source covers what a release would cover. It is the same
window, filter and consumer expansion every sweeping command uses, so a loop
and the run it accompanies never disagree about what changed.

```sh
dispat for --changed --since origin/main --do 'echo "$DISPAT_ITEM changed"'
```

Without `--since`, the window is the release window, which holds the packages
with something pending. Pass `--since all` to select every package. The
`--consumers` flag expands the window downstream, so a provider's consumers
join the list.

The `--unchanged` source is the exact complement. Every package is in one of
the two loops and never in both, whatever narrowing you apply, so the pair
covers the repository between them.

```sh
dispat for --unchanged --do 'echo "$DISPAT_ITEM is up to date"'
```

This is the half a sweep never reaches, which is where a staleness report, a
coverage check or a nightly rebuild belongs.

## What it costs

A literal list needs no config file and no git repository. dispat reads
nothing, asks GitHub nothing, and runs your scripts through `/bin/sh -c`. This
is what makes the command cheap enough to call from inside another script.

Naming `pkg:`, `space:` or `root` in `--in` reads the config file, because only
a configuration knows where a package lives. Every other source reads it too,
because which packages exist is a question about the monorepo. The `--changed`,
`--unchanged` and `--since` sources read the git repository as well, and cost
what [`dispat status`](./status.md) costs.

Once the config file is read, the loop body runs through the shell your config
names rather than through `/bin/sh -c`. That is the whole point of the command:
one loop, one meaning, whichever shell the repository builds with.

## Flags

### `--do <script>`

The script each item runs. Repeatable, run in order per item, stopping at
the first failure.

### `--keep-going`

Run the remaining items after one fails. The exit code is still the first
failure's.

### `--require-items`

Exit `1` when the iteration is empty, instead of `0`.

### `--unchanged`

Iterate over the packages the `--changed` window leaves out.

### `--changed`

Iterate over the changed packages.

### `--since <rev>`

Count changes from this revision instead of the release window; `all`
selects every package. On its own it means `--changed --since`.

### `--consumers`

Expand the window to everything downstream of the changes.

### `-p`, `-s`, `-g`

The list of packages, spaces or groups; under a window flag, the narrowing
instead.

### `--in <folder>`

Run every iteration in this folder: a path, or any
[place name](./locations.md).

### `--on-failure <script>`

Run this once after a failed loop, and exit with its code instead.

The command forwards nothing after `--`. The `--do` scripts are shell text you
already write in full, so there is nothing a forwarded argument would reach
that a script could not say itself.

## A note on the word

`for` is a command word, so `dispat for` never means `dispat run for`. A run
script named `for` is still reachable by its two-word spelling.

```sh
dispat run for
```
