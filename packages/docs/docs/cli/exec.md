# The exec command

Run `dispat exec <script>` to execute one declared script once. It runs in the
environment of a package, a space, or your current folder. It plans no release
and sweeps no packages.

Everything dispat runs is a shell command. Stages, hooks, and `run` scripts are
strings handed to `/bin/sh -c`. That works well until a script needs to branch
on a variable or call another script you already wrote.

Two small commands cover those needs.

```console
$ dispat if CI --then 'make ci' --else 'make dev'
$ dispat exec build --for pkg:core
```

Neither command plans a release. They ignore your packages and the dependency
graph. They run one script and get out of the way.

## Which command do I want

| You want to                                        | Use                          |
|----------------------------------------------------|------------------------------|
| run a script in every changed package, in order (`--since all` for every package) | `dispat run <script>` |
| run one declared script, once, right here           | `dispat exec <script>`       |
| choose between shell commands based on a variable   | `dispat if <cond>`           |
| run one shell command per item of a list            | [`dispat for <item>...`](./for.md) |

Use `dispat run` to compute a plan across your monorepo. It finds changed
packages and runs the script in dependency order. `dispat exec` ignores the
graph and runs one script by name. Call it when you are inside a stage script
and need to trigger another command. Use [`dispat for`](./for.md) when one
command line loops over a list, of packages or of anything else.

## dispat exec

```
dispat exec <script> [--for pkg:<name> | space:<name> | root | cwd] [--fallback]
                     [--script-from pkg:<name> | space:<name> | root | cwd]
                     [--env static|dispat|both]
                     [--in <folder> | pkg:<name> | space:<name> | root | cwd]
```

Run `dispat exec` to execute one script your config declares. It runs once in
the current folder.

### One subject decides everything

Pass `--for pkg:core` to name the subject of the invocation. The subject
decides which `scripts` map to read. It also decides whose environment the
script runs with.

| Flag              | Subject                          |
|-------------------|-----------------------------------|
| none              | the top level                     |
| `--for pkg:core`  | that package                      |
| `--for space:libs`| that space                        |
| `--for root`      | the top level, said out loud      |
| `--for cwd`       | whatever you are standing in      |

These values are the same everywhere dispat asks for a location. Read
[a page of their own](./locations.md) for details.

Workspace tools generally behave this way. `npm -w core run build` runs core's
script with core's environment as a single decision. `dispat exec` shares the
same idea.

Pass `cwd` to consult the folder you are standing in. Every other invocation
means the same thing from any directory. A command behaves identically from the
repository root, inside a package, or a random CI folder.

### Using the folder you are in

Pass `--for cwd` to opt in. It reads your folder exactly the way `dispat run`
does. It finds the deepest package or space that contains your current
directory:

```console
$ cd packages/core
$ dispat exec build --for cwd
core-build
```

This is the short way to build the thing you are looking at. It costs a scan of
your workspace and nothing more. It still reads no git history.

Stand outside any package or space and you get the top level. dispat logs a
line saying so rather than leaving you guessing.

### Finding the script

By default, dispat reads only the level you named:

```console
$ dispat exec build --for pkg:core
core-build
$ dispat exec deploy --for pkg:core
error: no script "deploy" in package "core"
```

That second failure is on purpose. If `deploy` is declared at the top level and
you asked for core's script, running the top-level one quietly hides a mistake.

Pass `--fallback` to find a name further up. It resolves the script the way
`dispat run` does. It walks from the package to its space to the top level:

| Invocation                                         | Order tried                              |
|----------------------------------------------------|------------------------------------------|
| `dispat exec build --for pkg:core --fallback`  | core, then core's space, then the top level |
| `dispat exec build --for space:libs --fallback`    | the space, then the top level            |
| `dispat exec build --fallback`                     | the top level, same as without it        |

The nearer level still wins. A package that declares its own `build` gets its
own script. If nothing in the chain has the name, the error lists every level
it checked.

### Taking the script from somewhere else

Sometimes the script you want belongs to one package, but you want to run it
against another. Pass `--script-from` to do this:

```sh
dispat exec verify --for pkg:api --script-from pkg:core
```

That command runs core's `verify` text with api's environment. It accepts
everything `--for` does, including `cwd`. It moves the script lookup only,
because the environment always stays with the subject.

### What the script gets

Your config's `env` block belongs to the script. dispat always applies it,
layered from the file up through the space to the package. You have nothing to
switch on.

The `DISPAT_*` release variables are different. They describe one package's
release, so working them out means computing a plan. That plan reads git tags
and history. Pass `--env` to ask for those variables:

| `--env`            | The script also gets                                                    | Cost               |
|--------------------|-------------------------------------------------------------------------|--------------------|
| `static` (default) | your declared `env`                                                     | nothing            |
| `dispat`           | `DISPAT_VERSION`, `DISPAT_TAG`, `DISPAT_PACKAGE`, and the rest           | a plan, so git     |
| `both`             | both of the above, which is exactly what `dispat run` gives a script     | the same plan      |

```console
$ dispat exec announce --for pkg:core --fallback --env both
announcing core at 1.4.0
```

A script written against `$DISPAT_VERSION` can now run on its own. You do not
need to run a release to get at it.

The release variables need a package, because a space has no version of its
own. dispat refuses `--env dispat` without a package subject, rather than
quietly handing you a smaller environment. With `--for cwd`, that check happens
after reading your folder. Standing in a package is enough.

**No `dispat exec` reads git unless `--env` asked it to.** Remember this when
you call it in a loop.

### Inside a stage or a run script

Call `dispat exec` from somewhere dispat already set up, and the `DISPAT_*`
variables are in the process environment already. The script inherits them with
no flag at all:

```json title="dispat.json"
{
  "scripts": {
    "announce": "echo announcing $DISPAT_PACKAGE at $DISPAT_VERSION",
    "ci": "dispat exec announce"
  }
}
```

Run `dispat run ci`, and the inner `announce` sees the whole release
environment. This is the exact case `dispat exec` was written for. It costs
nothing.

### Passing arguments to the script

Put arguments after `--` to send them to the script instead of to dispat. They
append to the command the configuration declares:

```console
$ dispat exec deploy -- --dry-run
```

With `"deploy": "./deploy.sh"`, that runs `./deploy.sh --dry-run`. A script in
the config takes a value from the terminal without being edited. dispat quotes
arguments carrying spaces or shell characters for you.

These arguments reach the script and nothing else. `--on-failure` never
receives them, because that script handles the failure rather than the work. As
with [`dispat run`](./run.md#passing-arguments-to-the-script), arguments land
at the *end* of the command text. If your script ends in something other than
the target program, wrap it: `sh -c './deploy.sh "$@"' _`. On a
[multi-command script](../configuration/scripts.md#one-name-several-commands),
that end is the *last* command. The setup commands before it run exactly as
written.

`dispat if` forwards nothing. You write its branches in full as shell text.
There is nothing a forwarded argument would reach that the branch cannot say
itself.

### Choosing the folder it runs in

Your script runs where you are standing. Pass `--in` to send it somewhere else:

```console
$ dispat exec build --in pkg:core
$ dispat exec build --in ./dist
```

The flag takes a folder path or any of the [place names](./locations.md)
`--for` takes. A relative path is relative to where you are standing.

This is a separate question from `--for`. The two are free to disagree:

```console
$ dispat exec release --for pkg:core --in root
```

That runs core's script from your repository root. It keeps core's environment
and core's `DISPAT_*` variables. Pass `--in pkg:core` alongside it if you want
a package's folder as well as its environment.

A missing folder stops the command with a message naming it. This prevents a
typo from turning into a confusing shell error. `--on-failure` runs in the same
folder as the script it follows.

## Exit codes

Both commands hand back the exit code of the script they ran. Run
`dispat if CI --then 'exit 7'` and it exits `7`. That keeps them transparent in
a pipeline, so whatever you were gating on still works.

A script bound to
[several commands](../configuration/scripts.md#one-name-several-commands) stops
at the first failure. That command's code becomes the script's exit code. The
commands after it do not run.

Pass `--on-failure` to change that. It runs when the chosen script fails. Its
own exit code becomes the command's exit code:

```console
$ dispat exec deploy --on-failure 'notify-slack "deploy failed"; exit 1'
```

The failure script runs even when Ctrl-C kills the first script. This
guarantees a cleanup still gets its chance.

An exit code of `2` means the command line itself did not make sense. Keep this
in mind if your script also exits `2`. End `--on-failure` with an explicit
`exit 1` to remove the ambiguity.

## Flags

### `--for <place>`

Run that level's script, in its environment: `pkg:<name>`, `space:<name>`,
`root` or `cwd`. One exact name, no globs.

### `--fallback`

Resolve the name the way `dispat run` does, walking up to the top level.

### `--script-from <place>`

Take the script text from somewhere else, leaving the environment with the
subject.

### `--env <scope>`

What the subject adds: `static` (default), `dispat` or `both`.

### `--in <folder>`

Run the script in this folder: a path, or any [place name](./locations.md).

### `--on-failure <script>`

Run this when the script fails, and exit with its code instead.

The command needs a config file to look up the script name. It uses the
configured `shell` if you set one.
