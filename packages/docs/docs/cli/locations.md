# Naming a place in your monorepo

Three flags on the two shell helpers ask the same question: where in the
monorepo do you mean? They all take the same answers, so once you know one you
know all three.

```console
$ dispat exec build --for pkg:core          # whose script, and whose environment
$ dispat exec build --script-from space:libs # whose script text, only
$ dispat exec build --in pkg:core            # which folder to run in
```

## The five answers

| You write        | You mean                                              |
|------------------|-------------------------------------------------------|
| `pkg:core`       | the package called `core`                             |
| `space:libs`     | the space called `libs`                               |
| `root`           | the top level, meaning your repository root           |
| `cwd`            | wherever you are standing right now                   |
| `packages/core`  | that folder, and only `--in` accepts this             |

Names are exact. There are no globs here, because each of these flags wants one
answer and a pattern matching two packages has no answer to give.

## cwd, and what "standing" means

`cwd` is the folder you ran the command from. It is the same thing `dispat run`
uses when you type no `--package` and it works out which package you are in:

```console
$ cd packages/core
$ dispat exec build --for cwd
core-build
```

dispat looks for the deepest thing containing your folder. A package wins over
the space that holds it, and a folder inside neither one means the top level:

| Standing in           | `cwd` means                             |
|-----------------------|------------------------------------------|
| `packages/core`       | the package `core`                       |
| `packages/core/src`   | still `core`, since you are inside it    |
| `packages`            | the space `libs`, if that is its folder  |
| `docs`                | the top level                            |

That last row is worth knowing. Standing somewhere that is no package and no
space is not an error, it just widens to the top level, and dispat says so in
the log so you are never left wondering why a different script ran.

One subtlety: `cwd` follows `--root` when you pass it. `--root` is how you tell
dispat where you are standing, so `dispat exec build --for cwd --root apps/web`
means the same as running the command from `apps/web`.

## Which folder does my script run in

By default, the one you are standing in. `--in` moves it:

```console
$ cd packages/core
$ dispat exec build          # runs here, in packages/core
$ dispat exec build --in root # runs at the repository root instead
```

`--in` takes a folder path as well as the four words above. A relative path is
relative to where you are standing, so `--in ../api` does what it looks like.

If you have a folder genuinely called `root` or `cwd`, write `./root` and
`./cwd`. The bare words are taken as the reserved ones, the same way a shell
would.

A folder that is not there stops the command with a message naming it, rather
than letting your shell complain about a directory it could not enter.

## The subject and the folder are separate questions

This trips people up once, and then never again. `--for` says whose script and
whose environment. `--in` says where it runs. Neither implies the other:

```console
$ dispat exec release --for pkg:core --in root
```

That runs core's `release` script, with core's environment and core's
`DISPAT_*` variables, from the repository root. If you want both to be core,
say so twice, or stand in core's folder and use `--for cwd`.

## A note on symlinks

dispat compares folders as they are written, without following symlinks. If you
reach your packages through a symlinked path, `cwd` may not recognise where you
are. `dispat run` behaves the same way, so the two stay consistent, but it is
worth knowing if your checkout is unusual.

## What about `dispat if`

`dispat if` takes `--in` too, with the same values:

```console
$ dispat if CI --then 'make ci' --in pkg:core
```

`dispat if` normally reads no config file at all, which is what makes it cheap
enough to call in a loop. A path or `cwd` keeps it that way, since your command
line already said everything needed. Naming `pkg:`, `space:` or `root` does
make it read your config, because there is no other way to find out where a
package lives. So does the [`--changed`](./if.md#changed-packages) condition,
which asks about the repository itself. You pay for that only when you ask for
it.

## See also

- [The exec command](./exec.md), which uses all three flags
- [The if command](./if.md)
- [The run command](./run.md), which reads your folder as a selection
