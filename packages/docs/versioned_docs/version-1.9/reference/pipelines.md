# Pipeline patterns

These CI/CD shapes get the most out of dispat. You can test only what a commit changed, test against the working tree
with local links, and build a gated release pipeline. Write permission, full tests, and a human trigger sit exactly
where they belong.

This repository releases itself using these exact pipelines. Every pattern here runs on every dispat release.

Read [dispat in CI](./ci.md) to get the binary onto a runner before you start.

## Test only what a commit changed

Run `dispat run` with a `--since` window to test only the packages a commit changed. A per-commit test job runs each
package's `tests` script for the addressed packages and everything that depends on them:

```sh
dispat run tests --since HEAD~1 --consumers
```

Pass `--since HEAD~1` to select the packages the last commit addresses, reading written scopes first and then changed
files for scopeless commits. This single value covers both triggers in a test workflow because a push lands one commit
and a pull request checks out as a merge commit with the base branch tip as its first parent. `HEAD~1` spans exactly
the pull request's changes, so you do not have to compute a base revision.

Pass `--consumers` to expand the selection to every package that transitively depends on a selected one, testing
everything built on top of a shared library. This expansion routes the work through dispat instead of a shell loop. The
run honours the dependency graph and the build concurrency budget, and a failing package skips its dependents unless
you pass `--on-error continue`.

You can use the same window for other scripts with different trade-offs. This repository's build job runs
`dispat run build --since HEAD~1` **without** `--consumers` because every container image depends on the CLI, and
expansion would turn any library change into a set of emulated multi-arch image builds. Choose this per script:
correctness gates want `--consumers`, but expensive artifact builds often do not.

Pass `--since origin/main` to cover everything a branch changed. Pass `--since all` to opt out of windowing entirely,
which a release gate needs for its full run (see [the gated pipeline](#the-gated-release-pipeline) below). A coverage
total must come from a `--since all` run because a windowed run tests only part of the monorepo.

## Testing against the working tree: the link bracket

Tests that exercise the checkout's own libraries need each consumer to resolve its providers from the working tree
instead of the registry. Run [`dispat autowriter`](../cli/autowriter.md) in ecosystems with a redirect directive to
derive those redirects from what the manifests already declare:

```sh
dispat autowriter --link-local --since all --sync-lock=false    # open the bracket
dispat run tests --since HEAD~1 --consumers                     # test against the tree
dispat autowriter --unlink-local --since all --sync-lock=false  # close it, even on failure
dispat scanner --root-only --verify-unlinked                    # prove it closed
```

Four details make the bracket safe:

- **Close it unconditionally.** Put `if: always()` on the unlink step in GitHub Actions. Unlinking is idempotent, so a
  failed test run never leaves redirects behind.
- **`--sync-lock=false` on both sides.** Never run a lock-file regeneration like `go mod tidy` while redirects are in
  place. A local folder needs no checksum, so a sync would delete `go.sum` entries that you need again the moment you
  unlink.
- **Prove it, do not trust it.** Run [`dispat scanner --verify-unlinked`](../cli/scanner.md) to turn a closed bracket
  into a gate. It exits `1` if any manifest still carries a redirect, and its inverse, `--verify-linked`, gates the
  opposite state.
- **A link must never publish.** A release never removes a link. Put the bracket in test jobs, and put the verify gate
  in front of any job that packs or publishes.

Five manifest formats have a redirect directive to manage: `go.mod` (`replace`), `Cargo.toml` (`[patch.crates-io]`),
`pubspec.yaml` (`dependency_overrides`), `pyproject.toml` (`[tool.uv.sources]`), and `package.json` (`overrides` and
its yarn and pnpm spellings). A *derived* link skips `package.json` because npm refuses an override for a directly
declared dependency unless the specs match exactly, but an explicit `--link name=path` still writes one. Read
[Editing across the monorepo](../editing/autowriter.md#working-against-local-folders) for the full walkthrough.

## When your ecosystem has no link directive

Most formats have no redirect to manage. dispat reports a link request against one as skipped instead of performed. Try
these alternatives in this order:

1. **Your package manager's own workspaces.** npm, pnpm, and Yarn workspaces resolve internal dependencies from the
   tree. This means the npm ecosystem rarely needs a link, so let the workspace mechanism cover local resolution where
   it can.
2. **Local specs written into the declaration.** Run [`dispat writer --set`](../cli/writer.md) or
   `dispat autowriter --set` to write a range verbatim. The local redirect *is* a form of the declaration in several
   formats: a Gemfile's `path:`, a Podfile's `:path`, an editable `-e ./pkg` requirements line, or a `workspace:*`
   protocol range. Writing and reverting this spec is an ordinary set, and the scanner reads it back as a local path.
3. **Literal replacement for everything else.** Use [`dispat autoreplacer`](../editing/autoreplacer.md) to rewrite
   literal text across every covered package. Its `--replace` patterns can name `{provider}` and `{providerVersion}`,
   so you can point a hand-assembled Gradle coordinate or a Helm image line at a locally published version and back.
   The [`autoVersion.replace`](../configuration/autoversion.md) field uses the same idea as configuration.
4. **A script pair as the bracket.** Sometimes a redirect is a command instead of a file edit. Bind `link` and `unlink`
   [scripts](../configuration/scripts.md) and run them with [`dispat exec`](../cli/exec.md) or `dispat run` around the
   tests, using the space's `onFail` script as the net. This repository wires its own bracket exactly this way.

## The gated release pipeline

The robust shape has four stages. Each stage answers one question and gates the next. dispat releases itself with this
pipeline.

**Stage 1: is there anything to release?** Run a plan job with `dispat status --require-release` to find out. The job
publishes the answer as an output:

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
          rc=0
          dispat status --require-release || rc=$?
          case "$rc" in
            0) echo "releasing=true" >> "$GITHUB_OUTPUT" ;;
            3) echo "releasing=false" >> "$GITHUB_OUTPUT" ;;
            *) exit "$rc" ;;
          esac
```

Two details carry the safety. Map the exit code through a `case` statement because exit `3` is the *answer*, whereas a
bare command fails the job and a plain `if` reads a broken configuration as an empty release. The job needs only
`contents: read` because `status` takes no release lock, which keeps write permission out of every job except the one
that releases.

**Stage 2: the human gate.** Trigger the release workflow on `workflow_dispatch` so a person decides when a release run
starts, and use a `concurrency` group with `cancel-in-progress: false` to queue a second dispatch behind the first. A
CI system with approval mechanisms, like GitHub environments or GitLab manual jobs, can put the approval between the
plan job and the release job. The [release lock](./releasing/release-lock.md) refuses a concurrent release either way,
so dispat does not care which method you use.

**Stage 3: the full suite, as the release gate.** The per-commit job tested `--since HEAD~1`. The job that gates a
release tests everything inside the link bracket:

```sh
dispat run tests --since all
```

Pass `--since all` deliberately here. This run gates a release of the whole graph. Every package's tests run whether or
not it changed, making this the only run that can produce a complete coverage measurement.

**Stage 4: the release, and its outcome as an output.** Give the release job `contents: write` permissions and run
`dispat --log-format json` to turn what happened into outputs for the jobs after it. The `--require-release` flag
composes here too, answering *before* taking the release lock so an empty run never makes a real release queue behind
it. Use the run hooks and [`dispat if`](../cli/if.md) to export the outcome:

```yaml title="dispat.yaml"
scripts:
  post-release: >-
    dispat if 'DISPAT_RESULT_CORE_STATUS=published'
    --then 'echo "released=true" >> "$GITHUB_OUTPUT"'
    --else 'echo "released=false" >> "$GITHUB_OUTPUT"'
run:
  postAll: post-release
```

A verification job keys on that output. It checks out the tag the release created and exercises the published artifact.
The same pipeline that made a broken release catches it.

## Selecting from the pipeline

Pass selection flags to `dispat release` and `dispat status` so a pipeline that accepts arguments can release part of
the monorepo. Use `-p` to name packages, `-s` for a space's packages, and `-g` for a versioning group's packages. You
can use a `workflow_dispatch` input on GitHub Actions:

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

Leave the input empty to release everything. Pass `-p core,web` to release a subset at exactly the versions a full
release would give it. The safety rails hold when you pass arguments from outside:

- A selection only narrows the plan. A typo in a name causes an error instead of an empty release, and the
  `--require-release` flag still fails a selection that publishes nothing.
- A selected package whose provider is releasing and unselected is **withheld** (`W230`). It waits for the next run
  instead of shipping against a version that is not out yet.
- The `-g` flag releases a whole [versioning group](./releasing/versioning.md). A shared version never splits by the
  flag that names it, and dispat reports any selection that splits a group some other way (`W231`).
- Pass `--strict` to turn both findings into a refusal. This happens after dispat prints the graph and before it builds
  anything, which helps pipelines that would rather stop than defer.

The same holds anywhere. Any CI system that can pass arguments to a command can drive partial releases with `-p`, `-s`,
and `-g`. Read [Partial releases](./releasing/partial-releases.md) for the full description of what a narrowed release
does.
