# The if command

Everything dispat runs is a shell command. Stages, hooks and `run` scripts are
all strings handed to `/bin/sh -c`, which works well until a script needs to do
one of two ordinary things: branch on a variable, or call another script you
already wrote.

Two small commands cover those.

```console
$ dispat if CI --then 'make ci' --else 'make dev'
$ dispat exec build --for-package core
```

Neither one plans a release, sweeps your packages or touches the dependency
graph. They run one script and get out of the way.

## Which command do I want

| You want to                                        | Use                          |
|----------------------------------------------------|------------------------------|
| run a script in every changed package, in order     | `dispat run <script>`        |
| run one declared script, once, right here           | `dispat exec <script>`       |
| choose between shell commands based on a variable   | `dispat if <cond>`           |

`dispat run` is the one that knows about your monorepo. It computes a plan,
works out which packages changed and runs the script in each of them in
dependency order. `dispat exec` does none of that. It looks up one script by
name and runs it, which is what you want when you are already inside a stage
script and just need to call something else.

## dispat if

```
dispat if <cond> --then <script> [--elif <cond> --then <script>]... [--else <script>]
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

### Conditions

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

Conditions read the environment the command was given, and nothing else. There
is no config file to load and no repository to be standing in, so `dispat if`
works anywhere.

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
exec` takes.

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

## Flags

| Flag                  | Effect                                                                        |
|-----------------------|--------------------------------------------------------------------------------|
| `--then <script>`     | The script the preceding condition runs. Repeatable, one per condition.        |
| `--elif <cond>`       | Another condition, tried when every earlier one was false. Repeatable.         |
| `--else <script>`     | The script to run when no condition held.                                      |
| `--in <folder>`       | Run the chosen script in this folder: a path, or any [place name](./locations.md). |
| `--on-failure <script>` | Run this when the chosen script fails, and exit with its code instead.       |

Needs no config file and no git repository, unless `--in` names a package, a
space or the root. The shell is `/bin/sh -c`, since there is no config here to
take a `shell` setting from.
