# Script sequences

Define named commands in `scripts`. The top-level `run` object and each space's `flow` object say **what runs when** by
referencing those names. dispat looks up a `flow` name against the package the stage is running for.

It checks the closest level first: the package's `scripts`, then its space's, then the file's. If a `flow` name is
missing from all three levels, dispat rejects the config with an error naming that package. This catches a script
defined only in some *other* space or package.

The `run` hooks are different. They execute once at the repository root with no package involved, so they only ever see
the file's `scripts`.

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

Naming the script contributes **all** of its commands to the sequence it was named in. A `flow: {build: release}` entry
creates a build stage of three commands. A `flow` entry naming two such scripts creates a sequence of everything both
of them bind.

The two levels of ordering flatten into the single order they run in. A failure behaves the way that sequence's
failures behave. It fails fast under a release-gating stage, and warns and continues under a warn-only hook.

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
