# Pipeline patterns

The CI/CD shapes that get the most out of dispat: testing only what a commit changed, testing against the working tree
with local links, and a gated release pipeline where write permission, full tests and a human trigger each sit exactly
where they belong. Every pattern here is lifted from the pipelines this repository releases itself with, so each one is
exercised on every dispat release.

[dispat in CI](./ci.md) covers the step before any of this: getting the binary onto a runner.

## Test only what a commit changed

`dispat run` takes a `--since` window, so a per-commit test job runs each package's `tests` script only for the
packages the last commit addressed, and for everything that depends on them:

```sh
dispat run tests --since HEAD~1 --consumers
```

`--since HEAD~1` selects the packages the last commit's units address: written scopes first, changed files for
scopeless commits. One value covers both triggers a test workflow has. A push lands one commit, and a pull request is
checked out as a merge commit whose first parent is the base branch tip, so `HEAD~1` spans exactly the pull request's
changes. No base revision has to be computed in the workflow.

`--consumers` is the reason to route this through dispat instead of a shell loop: it expands the selection to every
package that transitively depends on a selected one, so a change to a shared library also tests everything built on
top of it. The run honours the dependency graph and the build concurrency budget, and a failing package skips its
dependents unless `--on-error continue` says otherwise.

The same window fits other scripts with other trade-offs. This repository's build job runs
`dispat run build --since HEAD~1` **without** `--consumers`, because every container image depends on the CLI and the
expansion would turn any library change into a set of emulated multi-arch image builds. Choose per script: correctness
gates want `--consumers`, expensive artifact builds often do not.

Two related windows are worth knowing. `--since origin/main` covers everything a branch has changed, and
`--since all` opts out of windowing entirely, which is what a release gate wants (see
[the gated pipeline](#the-gated-release-pipeline) below). A coverage total in particular has to come from a
`--since all` run, because a windowed run tests only part of the monorepo.

## Testing against the working tree: the link bracket

Tests that exercise the checkout's own libraries need each consumer to resolve its providers from the working tree
rather than from the registry. In ecosystems with a redirect directive, [`dispat autowriter`](../cli/autowriter.md)
derives those redirects from what the manifests already declare:

```sh
dispat autowriter --link-local --since all --sync-lock=false    # open the bracket
dispat run tests --since HEAD~1 --consumers                     # test against the tree
dispat autowriter --unlink-local --since all --sync-lock=false  # close it, even on failure
dispat scanner --root-only --verify-unlinked                    # prove it closed
```

Four details make the bracket safe:

- **Close it unconditionally.** In GitHub Actions the unlink step carries `if: always()`, and unlinking is idempotent,
  so a failed test run never leaves redirects behind.
- **`--sync-lock=false` on both sides.** A lock-file regeneration such as `go mod tidy` must not run while the
  redirects are in place: a local folder needs no checksum, so the sync would delete `go.sum` entries that are needed
  again the moment you unlink.
- **Prove it, do not trust it.** [`dispat scanner --verify-unlinked`](../cli/scanner.md) exits `1` if any manifest
  still carries a redirect, which turns "the bracket closed" from a hope into a gate. Its inverse, `--verify-linked`,
  gates the opposite state.
- **A link must never publish.** Nothing in a release removes one, so the bracket belongs in test jobs, and the verify
  gate belongs in front of any job that packs or publishes.

Five manifest formats have a redirect directive to manage: `go.mod` (`replace`), `Cargo.toml` (`[patch.crates-io]`),
`pubspec.yaml` (`dependency_overrides`), `pyproject.toml` (`[tool.uv.sources]`) and `package.json` (`overrides` and
its yarn and pnpm spellings). One caveat inside that list: a *derived* link skips `package.json`, because npm refuses
an override for a directly declared dependency unless the specs match exactly; an explicit
`--link name=path` still writes one. The full walkthrough is
[Editing across the monorepo](../editing/autowriter.md#working-against-local-folders).

## When your ecosystem has no link directive

Most formats have no redirect to manage, and a link request against one is reported as skipped rather than performed.
The alternatives, in the order worth trying:

1. **Your package manager's own workspaces.** npm, pnpm and Yarn workspaces already resolve internal dependencies from
   the tree, which is why the npm ecosystem rarely needs a link at all. Where the workspace mechanism covers local
   resolution, let it.
2. **Local specs written into the declaration.** [`dispat writer --set`](../cli/writer.md) and
   `dispat autowriter --set` write a range verbatim, and in several formats the local redirect *is* a form of the
   declaration: a Gemfile's `path:`, a Podfile's `:path`, an editable `-e ./pkg` requirements line, a `workspace:*`
   protocol range. Writing and reverting such a spec is an ordinary set, and the scanner reads it back as a local
   path.
3. **Literal replacement for everything else.** [`dispat autoreplacer`](../editing/autoreplacer.md) rewrites literal
   text across every covered package, and its `--replace` patterns may name `{provider}` and `{providerVersion}`, so a
   hand-assembled Gradle coordinate or a Helm image line can be pointed at a locally published version and back.
   [`autoVersion.replace`](../configuration/autoversion.md) is the same idea as configuration.
4. **A script pair as the bracket.** Where the redirect is a command rather than a file edit, bind `link` and `unlink`
   [scripts](../configuration/scripts.md) and run them with [`dispat exec`](../cli/exec.md) or `dispat run` around the
   tests, with the space's `onFail` script as the net. That is exactly how this repository's own bracket is wired.

## The gated release pipeline

The robust shape has four stages, each answering one question, each gating the next. This is the pipeline dispat
releases itself with.

**Stage 1: is there anything to release?** A plan job runs `dispat status --require-release` and publishes the answer
as a job output:

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    permissions:
      contents: read        # status takes no lock and writes nothing
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: yohimik/dispat@v1
      - id: plan
        run: |
          if dispat status --require-release; then
            echo "releasing=true" >> "$GITHUB_OUTPUT"
          else
            echo "releasing=false" >> "$GITHUB_OUTPUT"
          fi
```

Two details carry the safety. The command is wrapped in `if` because the exit `1` is the *answer*, and a bare command
under `set -e` would fail the job instead of answering. And the job needs only `contents: read`: `status` takes no
release lock and creates no tag, so write permission stays out of every job except the one that releases. That is the
permission gate.

**Stage 2: the human gate.** The release workflow triggers on `workflow_dispatch`, so a person decides when a release
run starts, and a `concurrency` group with `cancel-in-progress: false` queues a second dispatch behind the first
instead of racing it. A CI system with approval mechanisms, such as GitHub environments or GitLab manual jobs, can put
the approval between the plan job and the release job instead; dispat does not care which, because the
[release lock](./releasing/release-lock.md) refuses a concurrent release either way.

**Stage 3: the full suite, as the release gate.** The per-commit job tested `--since HEAD~1`; the job that gates a
release tests everything, inside the link bracket:

```sh
dispat run tests --since all
```

`--since all` is deliberate here. This run gates a release of the whole graph, so every package's tests run whether or
not it changed, and it is the one run that can produce a complete coverage measurement.

**Stage 4: the release, and its outcome as an output.** The release job alone gets `contents: write`, runs
`dispat --log-format json`, and turns what happened into outputs for the jobs after it. `--require-release` composes
here too: on `release` it is answered *before* the release lock is taken, so a run that would publish nothing never
makes a real release queue behind it. The outcome export uses the run hooks and [`dispat if`](../cli/if.md):

```yaml title="dispat.yaml"
scripts:
  post-release: >-
    dispat if 'DISPAT_RESULT_CORE_STATUS=published'
    --then 'echo "released=true" >> "$GITHUB_OUTPUT"'
    --else 'echo "released=false" >> "$GITHUB_OUTPUT"'
run:
  postAll: post-release
```

A verification job then keys on that output, checks out the tag the release created, and exercises the published
artifact, which is how a broken release is caught by the same pipeline that made it.

## Selecting from the pipeline

`dispat release` and `dispat status` take the selection flags, so a pipeline that can pass arguments can release part
of the monorepo: `-p` names packages, `-s` a space's, `-g` a versioning group's. On GitHub Actions that is a
`workflow_dispatch` input:

```yaml
on:
  workflow_dispatch:
    inputs:
      only:
        description: packages to release (empty = everything)
        required: false
        default: ''

# ...in the release step:
- run: dispat release --log-format json ${{ inputs.only && format('-p {0}', inputs.only) }}
```

An empty input releases everything, and `-p core,web` releases a subset at exactly the versions a full release would
have given it. The safety rails hold with arguments coming from outside:

- A selection only ever narrows the plan, so a typo'd name is an error rather than an empty release, and
  `--require-release` still fails a selection that would publish nothing.
- A selected package whose provider is releasing and unselected is **withheld** (`W230`): it waits for the next run
  instead of shipping against a version that is not out yet.
- `-g` releases a whole [versioning group](./releasing/versioning.md), so a shared version can never be split by the
  flag that names it, and a selection that splits a group some other way is reported (`W231`).
- `--strict` turns both findings into a refusal, after the graph is printed and before anything is built, for
  pipelines that would rather stop than defer.

The same holds anywhere: any CI system that can pass arguments to a command can drive partial releases with `-p`,
`-s` and `-g`, and [Partial releases](./releasing/partial-releases.md) is the full description of what a narrowed
release does.
