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
