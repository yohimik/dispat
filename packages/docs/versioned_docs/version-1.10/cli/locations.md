# Naming a place in your monorepo

Three flags on the two shell helpers ask where you mean in the monorepo. They
take the same answers except for one. Learn one flag and you know all three.

```console
$ dispat exec build --for pkg:core          # whose script, and whose environment
$ dispat exec build --script-from space:libs # whose script text, only
$ dispat exec build --in pkg:core            # which folder to run in
```

## The five answers

| You write        | You mean                                              |
|------------------|-------------------------------------------------------|
| `pkg:core`       | the package named `core`                              |
| `space:libs`     | the space named `libs`                                |
| `root`           | the top level of your repository                      |
| `cwd`            | the folder you are standing in right now              |
| `packages/core`  | that specific folder, accepted only by `--in`         |

Names are exact. You cannot use globs here. Each flag needs exactly one answer,
and a pattern matching two packages cannot provide that.

## cwd, and what "standing" means

The `cwd` value means the folder you ran the command from. The `dispat run`
command uses this same logic to find your package when you omit `--package`.

```console
$ cd packages/core
$ dispat exec build --for cwd
core-build
```

dispat looks for the deepest container around your folder. A package wins over
the space holding it. A folder inside neither one resolves to the top level.

| Standing in           | `cwd` means                             |
|-----------------------|------------------------------------------|
| `packages/core`       | the package `core`                       |
| `packages/core/src`   | still `core`, since you are inside it    |
| `packages`            | the space `libs`, if that is its folder  |
| `docs`                | the top level                            |

Standing outside a package or space is not an error. The context widens to the
top level. dispat prints this in the log so you know exactly why a specific
script ran.

Pass `--root` to tell dispat where you are standing. The `cwd` value follows
this flag. Running `dispat exec build --for cwd --root apps/web` acts exactly
like running the command from `apps/web`.

## Which folder does my script run in

Your script runs in the folder you are standing in by default. Pass `--in` to
change this location.

```console
$ cd packages/core
$ dispat exec build          # runs here, in packages/core
$ dispat exec build --in root # runs at the repository root instead
```

The `--in` flag accepts a folder path alongside the four reserved words above.
Relative paths resolve from where you are standing. Passing `--in ../api` moves
execution up and into the API folder.

Write `./root` or `./cwd` if you have actual folders with those names. dispat
treats the bare words as reserved values. This matches standard shell
behaviour.

Passing a missing folder stops the command immediately. dispat prints an error
naming the path. This prevents your shell from failing later on a directory it
cannot enter.

## The subject and the folder are separate questions

The `--for` flag sets the script and the environment. The `--in` flag sets the
execution directory. Neither flag implies the other.

```console
$ dispat exec release --for pkg:core --in root
```

That command runs the `release` script belonging to core from the repository
root. It uses core's environment and core's `DISPAT_*` variables. Say so twice
if you want both to be core, or stand in core's folder and pass `--for cwd`.

## A note on symlinks

dispat compares folders exactly as they are written and does not follow
symlinks. The `cwd` value may not recognise your location if you reach your
packages through a symlinked path. The `dispat run` command behaves the same
way to keep the tools consistent, which matters for unusual checkouts.

## What about `dispat if`

The `dispat if` command also accepts `--in` with the exact same values.

```console
$ dispat if CI --then 'make ci' --in pkg:core
```

The `dispat if` command normally skips reading your config file so it stays
cheap enough to call in a loop. Passing a path or `cwd` keeps it fast, while
naming `pkg:`, `space:`, or `root` forces dispat to read your config to locate
the package. The [`--changed`](./if.md#changed-packages) condition also
triggers a read to query the repository, so you only pay for this when you ask
for it.

## See also

- [The exec command](./exec.md), which uses all three flags
- [The if command](./if.md)
- [The run command](./run.md), which reads your folder as a selection
