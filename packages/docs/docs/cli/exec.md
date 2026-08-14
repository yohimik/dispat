# The exec command

Everything dispat runs is a shell command. Stages, hooks and `run` scripts are
all strings handed to `/bin/sh -c`, which works well until a script needs to do
one of two ordinary things: branch on a variable, or call another script you
already wrote.

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
| choose between shell commands based on a variable   | `dispat if <cond>`           |

`dispat run` is the one that knows about your monorepo. It computes a plan,
works out which packages changed and runs the script in each of them in
dependency order. `dispat exec` does none of that. It looks up one script by
name and runs it, which is what you want when you are already inside a stage
script and just need to call something else.

## dispat exec

```
dispat exec <script> [--for pkg:<name> | space:<name> | root | cwd] [--fallback]
                     [--script-from pkg:<name> | space:<name> | root | cwd]
                     [--env static|dispat|both]
                     [--in <folder> | pkg:<name> | space:<name> | root | cwd]
```

`dispat exec` runs one script your config declares, in the current folder, once.

### One subject decides everything

`--for pkg:core` names the subject of the invocation. The subject decides two
things at once: which `scripts` map the name is looked up in, and whose
environment the script runs with.

| Flag              | Subject                          |
|-------------------|-----------------------------------|
| none              | the top level                     |
| `--for pkg:core`  | that package                      |
| `--for space:libs`| that space                        |
| `--for root`      | the top level, said out loud      |
| `--for cwd`       | whatever you are standing in      |

Those values are the same everywhere dispat asks you where something is. They
have [a page of their own](./locations.md).

This is how workspace tools generally behave. `npm -w core run build` is core's
script with core's environment, not two separate decisions, and `dispat exec` is
the same idea.

The folder you are standing in is consulted only when you ask for it with `cwd`.
Every other invocation means the same thing whether you run it from the
repository root, from inside a package, or from a CI job that starts somewhere
unpredictable.

### Using the folder you are in

`--for cwd` is the opt-in. It reads your folder exactly the way `dispat run`
does, finding the deepest package or space that contains it:

```console
$ cd packages/core
$ dispat exec build --for cwd
core-build
```

This is the short way to say "build the thing I am looking at". It costs a
scan of your workspace and nothing more, so it still reads no git history.

If you are standing somewhere that is no package and no space, you get the top
level, and dispat logs a line saying so rather than leaving you guessing.

### Finding the script

By default only the level you named is read:

```console
$ dispat exec build --for pkg:core
core-build
$ dispat exec deploy --for pkg:core
error: no script "deploy" in package "core"
```

That second failure is on purpose. If `deploy` is declared at the top level and
you asked for core's, running the top-level one quietly would hide a mistake
that is easy to make and hard to notice.

When you do want a name to be found further up, `--fallback` resolves it the way
`dispat run` does, walking from the package to its space to the top level:

| Invocation                                         | Order tried                              |
|----------------------------------------------------|------------------------------------------|
| `dispat exec build --for pkg:core --fallback`  | core, then core's space, then the top level |
| `dispat exec build --for space:libs --fallback`    | the space, then the top level            |
| `dispat exec build --fallback`                     | the top level, same as without it        |

The nearer level still wins, so a package that declares its own `build` gets its
own. If nothing in the chain has the name, the error lists every level it looked
in, which is what tells a missing script apart from a misplaced one.

### Taking the script from somewhere else

Once in a while the script you want to run belongs to one package and the thing
you want to run it against is another. `--script-from` says so:

```sh
dispat exec verify --for pkg:api --script-from pkg:core
```

That runs core's `verify` text with api's environment. It accepts everything
`--for` does, `cwd` included, and it moves the lookup only. The environment
always stays with the subject.

### What the script gets

Your config's `env` block belongs to the script, so it is always applied, layered
from the file up through the space to the package. Nothing to switch on.

The `DISPAT_*` release variables are different. They describe one package's
release, so working them out means computing a plan, and that reads git tags and
history. `--env` is where you ask for that:

| `--env`            | The script also gets                                                    | Cost               |
|--------------------|-------------------------------------------------------------------------|--------------------|
| `static` (default) | your declared `env`                                                     | nothing            |
| `dispat`           | `DISPAT_VERSION`, `DISPAT_TAG`, `DISPAT_PACKAGE`, and the rest           | a plan, so git     |
| `both`             | both of the above, which is exactly what `dispat run` gives a script     | the same plan      |

```console
$ dispat exec announce --for pkg:core --fallback --env both
announcing core at 1.4.0
```

That is the useful part: a script written against `$DISPAT_VERSION` can now be
run on its own, without running a release to get at it.

The release variables need a package, since a space has no version of its own to
report, so `--env dispat` without a package subject is refused rather than
quietly handing you a smaller environment. With `--for cwd` that check happens
once your folder has been read, so standing in a package is enough.

**No `dispat exec` reads git unless `--env` asked it to.** Worth remembering if
you are calling it in a loop.

### Inside a stage or a run script

When `dispat exec` is called from somewhere dispat already set up, the `DISPAT_*`
variables are in the process environment already, and the script inherits them
with no flag at all:

```json title="dispat.json"
{
  "scripts": {
    "announce": "echo announcing $DISPAT_PACKAGE at $DISPAT_VERSION",
    "ci": "dispat exec announce"
  }
}
```

Run under `dispat run ci`, the inner `announce` sees the whole release
environment. This is the case `dispat exec` was written for, and it costs
nothing.

### Passing arguments to the script

Everything after `--` goes to the script instead of to dispat, appended to the
command the configuration declares:

```console
$ dispat exec deploy -- --dry-run
```

With `"deploy": "./deploy.sh"` that runs `./deploy.sh --dry-run`, so a script
in the config takes a value from the terminal without being edited. Arguments
carrying spaces or shell characters are quoted for you.

They reach the script and nothing else: `--on-failure` never receives them,
because that script is about the failure rather than about the work. And as
with [`dispat run`](./run.md#passing-arguments-to-the-script), they land at the
*end* of the command text, so a script that ends in something other than the
program you meant should wrap it: `sh -c './deploy.sh "$@"' _`. On a
[multi-command script](../configuration/scripts.md#one-name-several-commands)
that end is the *last* command; the setup commands before it run as the config
wrote them.

`dispat if` forwards nothing. Its branches are already shell text you write in
full, so there is nothing a forwarded argument would reach that the branch
cannot say itself.

### Choosing the folder it runs in

Your script runs where you are standing. `--in` sends it somewhere else:

```console
$ dispat exec build --in pkg:core
$ dispat exec build --in ./dist
```

It takes a folder path, or any of the [place names](./locations.md) `--for`
takes. A relative path is relative to where you are standing.

This is a separate question from `--for`, and the two are free to disagree:

```console
$ dispat exec release --for pkg:core --in root
```

That is core's script, with core's environment and core's `DISPAT_*` variables,
run from your repository root. If you want a package's folder as well as its
environment, `--in pkg:core` alongside says so.

A folder that does not exist stops the command with a message naming it, so a
typo does not turn into a confusing shell error. `--on-failure` runs in the same
folder as the script it follows.

## Exit codes

Both commands hand back the exit code of the script they ran. `dispat if CI
--then 'exit 7'` exits `7`. That keeps them transparent in a pipeline: whatever
you were gating on still works with a helper in the middle.

A script bound to [several commands](../configuration/scripts.md#one-name-several-commands)
stops at the first one that fails, and that command's code is the script's; the
commands after it do not run.

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

| Flag                    | Effect                                                                                |
|-------------------------|-----------------------------------------------------------------------------------------|
| `--for <place>`         | Run that level's script, in its environment: `pkg:<name>`, `space:<name>`, `root` or `cwd`. One exact name, no globs. |
| `--fallback`            | Resolve the name the way `dispat run` does, walking up to the top level.                |
| `--script-from <place>` | Take the script text from somewhere else, leaving the environment with the subject.     |
| `--env <scope>`         | What the subject adds: `static` (default), `dispat` or `both`.                          |
| `--in <folder>`         | Run the script in this folder: a path, or any [place name](./locations.md).             |
| `--on-failure <script>` | Run this when the script fails, and exit with its code instead.                         |

Needs a config file, since the script name comes from it. Uses the configured
`shell` if you set one.
