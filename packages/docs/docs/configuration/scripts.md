# Script sequences

Define a shell command once in `scripts`, then refer to its name from a `flow` stage or a top-level `run` hook. A name
can hold one command or several commands to run in order.

## Where script names are resolved

For a `flow` entry, dispat resolves the script name for the package whose stage is running.

It checks the closest level first: the package's `scripts`, then its space's, then the file's. If a `flow` name is
missing from all three levels, dispat rejects the config with an error naming that package. This catches a script
defined only in some *other* space or package.

The `run` hooks are different. They execute once at the repository root with no package involved, so they only ever see
the file's `scripts`.

## Execution order and failures

Every entry in either object accepts a single script name or an array of names. dispat executes them **sequentially, in
order**. How a failure behaves depends on what the sequence gates:

- **Release-gating scripts** (the stage scripts, the login, every hook up to `beforePublish`, and the run-level
  `beforeAll`) are fail-fast. The first failing command stops the sequence. This fails the package's release, or the
  whole run for the run-level `beforeAll`.
- **Warn-only scripts** (`postPublish`, the whole announce frame of `beforeAnnounce`, `announce` and `postAnnounce`,
  the outcome scripts `onFail` / `onSkip`, and every other run-level hook) never fail anything. dispat logs a failing
  command as a warning, and **the remaining commands of the sequence still run**. These hooks observe work that has
  already happened, so stopping the sequence could not undo it.

A script named here is a fixed command, but it does not have to be the whole command. You can append arguments to it
using [`dispat run`](../cli/run.md#passing-arguments-to-the-script) and [`dispat exec`](../cli/exec.md). Type your
arguments after `--`.

For example, `dispat run test -- --watch` runs the `test` script with `--watch` without changing the config. The
release stages never take arguments this way. What a release runs is what the file says.

## One name, several commands

A `scripts` entry accepts the same two shapes its references do. You can provide one command, or an array of commands
run **in order**.

```yaml
scripts:
  build: "npm run build"        # one command, which is what most scripts are
  release:                      # several, run in the order written
    - npm ci
    - npm run build
    - npm run bundle
```

Referencing a script runs all of its commands. In this example, `flow: {build: release}` runs the three commands as the
build stage. If a `flow` entry names two scripts, dispat runs all commands from the first, then all commands from the
second.

The stage or hook determines how failures are handled: release-gating sequences stop at the first failure; warn-only
hooks log the failure and continue.

These commands are separate invocations of the [configured shell](./README.md#top-level-options). dispat does not join
them into one string. Three things follow from this:

- **Each command reaches the shell exactly as it was written.** dispat inserts nothing between them, so a command
  carrying `&&`, a trailing comment or a `;` means what it would mean on its own line.
- **No shell state carries between them.** A `cd`, an `export` or a `set -e` in one command does not reach the next.
  Every command starts in the package folder with the same environment. A sequence that genuinely needs one shell is
  still one command, so write the `&&` yourself.
- **Every command is reported on its own.** A failure names the command that failed rather than the whole string.

An entry that binds no command at all (`build: []`, or `build: ""`) is a config error. It is not a way to switch off an
inherited name. Redefining a name at a nearer level replaces what it binds **whole**.

A package restating a two-command script as one command gets one command, not a merge of the two.
