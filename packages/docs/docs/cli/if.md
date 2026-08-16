# The if command

`dispat if` chooses between shell scripts by testing a condition, so a stage can branch without depending on the
shell it is running under. The condition asks the environment (`CI`, `ENV=prod`), the filesystem (`-f
data/report.json`, `-d build`) or the repository (`--changed`, did the selected packages change). It plans no
release and sweeps no packages.

It exists because everything dispat runs is a shell command. Stages, hooks and `run` scripts are all strings handed to
`/bin/sh -c`, which works well until a script needs to do one of two ordinary things: branch on a variable, or call
another script you already wrote.

Two small commands cover those.

```console
$ dispat if CI --then 'make ci' --else 'make dev'
$ dispat exec build --for pkg:core
```

Neither one plans a release, sweeps your packages or touches the dependency
graph. They run one script and get out of the way.

## Which command do I want

| You want to                                        | Use                          |
|----------------------------------------------------|------------------------------|
| run a script in every changed package, in order     | `dispat run <script>`        |
| run one declared script, once, right here           | `dispat exec <script>`       |
| choose between shell commands based on a condition  | `dispat if <cond>`           |

`dispat run` is the one that knows about your monorepo. It computes a plan,
works out which packages changed and runs the script in each of them in
dependency order. `dispat exec` does none of that. It looks up one script by
name and runs it, which is what you want when you are already inside a stage
script and just need to call something else.

## dispat if

```
dispat if <cond>      --then <script> [--elif <cond> --then <script>]... [--else <script>]
dispat if -f <path>   --then <script> ...
dispat if -d <path>   --then <script> ...
dispat if --changed [--since <rev>] [-p <pkg>] [-s <space>] [-g <group>] [--consumers] --then <script> ...
```

The leading condition takes the first `--then`. Each `--elif` takes the next
one. `--else` runs when nothing else matched.

```sh
dispat if 'ENV=prod'      --then 'deploy prod' \
       --elif 'ENV=stage' --then 'deploy stage' \
       --else               'echo nothing to deploy'
```

The first condition that holds wins, and the rest are skipped without being
looked at. So a chain of `--elif` is a switch, and `--else` is its default case.

If nothing matches and you gave no `--else`, nothing runs and the command exits
`0`. That is deliberate: a guard that finds nothing to do has done its job.

The scripts are shell text, not script names. This is the shell's own
if/elif/else, spelled so it fits on one line inside a JSON or YAML config file
where a real `if` block would be unreadable.

The leading condition comes from exactly one place: the positional condition,
`--changed`, `--file` or `--dir`. Giving two is a usage error, because two
answers to "what does the first `--then` guard" would leave one silently
ignored. Every `--elif` is an environment condition.

### Environment conditions

| Condition       | True when                                          |
|-----------------|-----------------------------------------------------|
| `NAME`          | the variable is set and not empty                   |
| `!NAME`         | the variable is unset, or set to nothing            |
| `NAME=value`    | it is exactly that value                            |
| `NAME!=value`   | it is anything else                                 |
| `NAME~glob`     | it matches the pattern, where `*` matches anything  |
| `NAME!~glob`    | it does not match the pattern                       |

"Set" means set and not empty, the same thing `[ -n "$NAME" ]` means in the
shell. CI systems export empty variables all the time, and an empty value is
almost never a yes. If you specifically want to ask whether a variable is empty,
`NAME=` is the way, because an unset variable expands to nothing exactly as it
would in a shell.

The value can contain anything, operators included. Only the first operator ends
the variable name, so `URL=a~b` asks whether `URL` is the text `a~b`.

Globs use the same matcher as everywhere else in dispat, where `*` matches any
run of characters including slashes:

```sh
dispat if 'BRANCH~release/*' --then 'dispat release'
```

These conditions read the environment the command was given, and nothing else.
There is no config file to load and no repository to be standing in, so
`dispat if` works anywhere.

### File tests

`--file` (`-f`) and `--dir` (`-d`) ask the filesystem instead:

| Condition     | True when                                       |
|---------------|--------------------------------------------------|
| `-f <path>`   | the path exists and is a regular file            |
| `-d <path>`   | the path exists and is a folder                  |

```sh
dispat if -f data/report.json --then 'npm run build-docs' --else 'echo no report yet'
```

A path that is absent, or there but the wrong kind, makes the condition false.
It is never an error, exactly as `[ -f ]` and `[ -d ]` behave in the shell:
the question was "is it there", and it is not. Symbolic links are followed, so
a link to a file passes `-f`.

A relative path resolves against the folder the chosen script runs in, which
is the invocation folder, or wherever [`--in`](#choosing-the-folder-it-runs-in)
points. The test and a path written inside the script text therefore always
mean the same file. An absolute path is used as it is.

Like the environment conditions, file tests read no config file and need no
repository.

### Changed packages

`--changed` asks the repository: it holds when changed packages are selected.
The selection works exactly like [`dispat run`'s](./run.md), so a gate and the
run it guards can never disagree about what changed.

```sh
dispat if --changed -p docs --since origin/main --then 'dispat run build-docs'
```

`--since <rev>` sets the window to what the commits since that revision
address, with the same scope semantics [`dispat run --since`](./run.md) uses:
a commit's written scopes are authoritative, and only scopeless commits fall
back to the files they changed. Without `--since`, the window is the
release window, the packages with something pending, so a bare `dispat if
--changed` asks "would a release do anything". `--since all` selects every
package, changed or not.

`--consumers` expands the window downstream before the selection narrows it,
so the gate asks whether the selection is among what the changes reach:

```sh
dispat if --changed -p web --consumers --since HEAD~1 --then 'dispat run e2e -p web'
```

This holds when `web`, or anything `web` transitively consumes, changed. Note
that this is the one place `--consumers` composes in that order. A sweep like
`dispat run` narrows first and expands after, asking for the selection's
dependents; a gate that did the same would find `--consumers` unable to ever
change its answer, since expanding a selection never empties it and never
fills an empty one.

`--package`/`-p`, `--space`/`-s` and `--group`/`-g` narrow the answer the way
they narrow every command, invocation folder included: run inside a package
folder with no terms, the gate asks about that package alone. An empty
selection is an honest false. A term that matches no package at all is an
error, never a false, because a gate reading a typo as "no" would silently
never fire.

Asking about the repository costs finding out: `--changed` reads the config
file, walks the tags and parses the commits, the same work `dispat status`
does, where every other condition reads nothing. A `--changed` that cannot be
evaluated, a revision git cannot resolve or a configuration that cannot be
loaded, exits `1`. The selection flags belong to `--changed`, so giving any of
them with another condition is a usage error.

### Nesting

A branch is just shell text, so another dispat command is a perfectly ordinary
thing to put in one:

```sh
dispat if CI --then 'dispat if TIER=gold --then "deploy gold" --else "deploy standard"'
```

## Choosing the folder it runs in

The chosen branch runs where you are standing, so a relative path in it means
what you meant. `--in` sends it somewhere else:

```console
$ dispat if CI --then 'make ci' --in ./build
$ dispat if CI --then 'make ci' --in pkg:core
```

It takes a folder path, or any of the [place names](./locations.md) `dispat
exec` takes. A relative `--file` or `--dir` path moves with it, so the test
asks about the folder the script actually runs in.

There is one thing worth knowing here. `dispat if` reads no config file, which
is what makes it cheap enough to call in a loop, and a path or `cwd` keeps it
that way because your command line already said everything needed. Naming
`pkg:`, `space:` or `root` does make it read your config, since there is no
other way to find out where a package lives. You pay for that only when you ask.

A folder that does not exist stops the command with a message naming it.

## Exit codes

Both commands hand back the exit code of the script they ran. `dispat if CI
--then 'exit 7'` exits `7`. That keeps them transparent in a pipeline: whatever
you were gating on still works with a helper in the middle.

`--on-failure` changes that. It runs when the chosen script fails, and its own
exit code becomes the command's:

```console
$ dispat exec deploy --on-failure 'notify-slack "deploy failed"; exit 1'
```

The failure script runs even when the first one was killed by Ctrl-C, so a
cleanup still gets its chance.

`2` still means the command line itself did not make sense, which is worth
knowing if your script also exits `2`. Ending `--on-failure` with an explicit
`exit 1` removes the ambiguity.

A condition that is false runs its `--else`, or nothing, and exits
accordingly; being false is not a failure. The one condition that can fail is
`--changed`, when it cannot be evaluated at all: a revision git cannot
resolve, or a configuration that cannot be loaded, exits `1`.

## Flags

| Flag                  | Effect                                                                        |
|-----------------------|--------------------------------------------------------------------------------|
| `--then <script>`     | The script the preceding condition runs. Repeatable, one per condition.        |
| `--elif <cond>`       | Another condition, tried when every earlier one was false. Repeatable.         |
| `--else <script>`     | The script to run when no condition held.                                      |
| `--file <path>`, `-f` | The leading condition: the path exists and is a regular file.                   |
| `--dir <path>`, `-d`  | The leading condition: the path exists and is a folder.                         |
| `--changed`           | The leading condition: changed packages are selected.                          |
| `--since <rev>`       | With `--changed`: count changes from this revision instead of the release window; `all` selects every package. |
| `--consumers`         | With `--changed`: expand the window to everything downstream of the changes.   |
| `-p`, `-s`, `-g`      | With `--changed`: narrow to the named packages, spaces or groups.               |
| `--in <folder>`       | Run the chosen script in this folder: a path, or any [place name](./locations.md). |
| `--on-failure <script>` | Run this when the chosen script fails, and exit with its code instead.       |

Needs no config file and no git repository, unless `--in` names a package, a
space or the root, or `--changed` asks about the repository itself. The shell
is `/bin/sh -c`, since there is no config here to take a `shell` setting from.
