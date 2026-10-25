# The exec command

Run `dispat exec <script>` to execute one declared script once. Choose a
package, space, or the top level for its script and environment. The working
directory is a separate choice and defaults to your current folder.

Use `exec` to call a shared script from another script, or to run a package's
command without starting a release. The related `if` and `for` helpers handle
conditions and loops:

```console
$ dispat if CI --then 'make ci' --else 'make dev'
$ dispat exec build --for pkg:core
$ dispat for core web --do 'make "$DISPAT_ITEM"'
```

These helpers do not start a release. They read package or release information
only when an option requests it, such as `exec --env both` or `for --changed`.

## Which command do I want

| You want to                                        | Use                          |
|----------------------------------------------------|------------------------------|
| run a script in every changed package, in order (`--since all` for every package) | [`dispat run <script>`](./run.md) |
| run one declared script, once, right here           | [`dispat exec <script>`](./exec.md) |
| choose between shell commands based on a condition  | [`dispat if <cond>`](./if.md) |
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

The command needs a configuration file to find the script. It runs once in
the current folder unless you pass `--in`.

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

See [Naming a place](./locations.md) for the location values shared by these flags.

For example, `--for pkg:core` selects core's script and environment together.
It does not change the working directory; use `--in pkg:core` for that.

Use `cwd` when the subject should come from your current folder. Named
subjects select the same package or space wherever you run the command.

### Using the folder you are in

Pass `--for cwd` to opt in. It reads your folder exactly the way `dispat run`
does. It finds the deepest package or space that contains your current
directory:

```console
$ cd packages/core
$ dispat exec build --for cwd
core-build
```

This selects the package you are working in. It scans the workspace to find
the package but does not read Git history.

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

The second command fails even if `deploy` exists at the top level. Without
`--fallback`, only the named level is checked.

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

The subject's configured `env` values are applied automatically, from the
top level through the space to the package.

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

Use this when a standalone script needs values such as `$DISPAT_VERSION`
without running a release.

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

Run `dispat run ci`, and the inner `announce` inherits the release environment.
It does not need another plan to calculate those values.

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

That runs core's script from the repository root and keeps core as the
environment subject. Release variables still depend on `--env` or the inherited
stage environment. Use `--in pkg:core` to run in the package folder too.

A missing folder stops the command with a message naming it. This prevents a
typo from turning into a confusing shell error. `--on-failure` runs in the same
folder as the script it follows.

## Exit codes

`exec` and `if` return the script's exit code. For example,
`dispat if CI --then 'exit 7'` exits `7` when the condition is true, so a
pipeline can check the script's original result.

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

### Spaces in a fleet

A named space uses the entry repository's declaration when it has one. Otherwise,
a space declared by one participating peer uses that peer's scripts, environment,
and primary folder. `--fallback` reads the same owner's top-level scripts.

If several peers declare the same name and the entry does not, a singular space
location is ambiguous and the command stops before running a script. Use a package
subject, or enter the intended space folder and use `--for cwd --in cwd`. A current
folder always resolves its own space owner, even when the entry has an equal name.
