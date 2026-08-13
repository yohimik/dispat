# Script sequences

`scripts` defines named commands; the top-level `run` object and each space's `flow` object say **what runs when**,
referencing those names. A `flow` name is looked up against the package the stage is running for, closest level first:
the package's `scripts`, then its space's, then the file's. The `run` hooks are different, because they execute once at
the repository root with no package involved, so they only ever see the file's `scripts`. If a `flow` name is missing
from all three of a package's levels, the config is rejected with an error naming that package, which is how a script
defined only in some *other* space or package gets caught. Every entry of either object accepts a single script name or
an array of names executed
**sequentially, in order**; a scalar is simply a one-element sequence. How a failure inside a sequence behaves depends
on what the sequence gates:

- **Release-gating scripts** (the stage scripts, the login, every hook up to `beforePublish`, and the run-level
  `beforeAll`) are fail-fast: the first failing command stops the sequence and fails the package's release, or, for the
  run-level `beforeAll`, the whole run.
- **Warn-only scripts** (`postPublish`, the whole announce frame of `beforeAnnounce`, `announce` and `postAnnounce`, the
  outcome scripts `onFail` / `onSkip`, and every other run-level hook) never fail anything: a failing command is logged
  as a warning and **the remaining commands of the sequence still run**. These hooks observe work that has already
  happened, so stopping the sequence could not undo it.

A script named here is a fixed command, but it does not have to be the whole of one:
[`dispat run`](../cli/run.md#passing-arguments-to-the-script) and [`dispat exec`](../cli/exec.md) append anything
typed after `--` to it, so `dispat run test -- --watch` runs the `test` script with `--watch` without the config
changing. The release stages never take arguments this way; what a release runs is what the file says.

## One name, several commands

A `scripts` entry accepts the same two shapes its references do: one command, or an array of commands run **in order**.

```yaml
scripts:
  build: "npm run build"        # one command, which is what most scripts are
  release:                      # several, run in the order written
    - npm ci
    - npm run build
    - npm run bundle
```

Naming the script contributes **all** of its commands to the sequence it was named in, so `flow: {build: release}` is a
build stage of three commands, and a `flow` entry naming two such scripts is a sequence of everything both of them
bind. The two levels of ordering flatten into the one order they run in, and a failure behaves the way that sequence's
failures behave — fail-fast under a release-gating stage, warn-and-continue under a warn-only hook, per the rules
above.

Three things follow from the commands being separate invocations of the [configured shell](./README.md#top-level-options)
rather than one string dispat joined together:

- **Each command reaches the shell exactly as it was written.** Nothing is inserted between them, so a command carrying
  `&&`, a trailing comment or a `;` means what it would mean on its own line.
- **No shell state carries between them.** A `cd`, an `export` or a `set -e` in one command does not reach the next:
  every command starts in the package folder, with the same environment. A sequence that genuinely needs one shell is
  still one command — write the `&&` yourself.
- **Every command is reported on its own.** A failure names the command that failed rather than the whole string.

An entry that binds no command at all — `build: []`, or `build: ""` — is a config error, not a way to switch off an
inherited name. Redefining a name at a nearer level replaces what it binds **whole**, so a package restating a
two-command script as one command gets one command, not a merge of the two.
