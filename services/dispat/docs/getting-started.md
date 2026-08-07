# Getting started

## Install

```sh
go install github.com/yohimik/dispat@latest
```

## First configuration

Create `dispat.json` at your monorepo root — `dispat init` writes a starter one (`--format yaml` / `--format toml`
for the other formats). No flag is needed afterwards: every command finds the first of `dispat.json`, `dispat.yaml`,
`dispat.yml`, `dispat.toml` that exists (`--config` names a different file explicitly). Minimal example:

```json
{
  "scripts": {
    "build": "npm ci && npm run build",
    "publish": "npm publish"
  },
  "spaces": {
    "libs": {
      "path": "packages/libs",
      "run": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Every direct sub-folder of `packages/libs` is now a package named after its folder. Declare relations between packages
under `dependencies` so bumps propagate and ordering is enforced:

```json
{
  "dependencies": [
    {
      "consumer": "app",
      "provider": "core"
    }
  ]
}
```

Beyond scripts and spaces there are optional top-level knobs — `concurrency` (`[build, publish]` budgets), `tagFormat`
(the release tag template), `commitErrors` (whether a bad commit message stops the run), `nonPackageScopes`,
`changelog` and `github` (customise or disable the release records), `initials` (baseline versions for packages without
a usable tag), `commit` and `push` (end-of-run release commit and pushing, disabled by default), the run-level hooks
(the gating `run.beforeAll`, `run.postAll` and the commit/push hooks) — and per-space options `isBuildWaitingPublish`,
`revertOnFail`,
`versioning` (`independent`, `fixed` — one shared version for the whole space — or `fixedSparse`),
`run.version`, `run.login` (once-per-space authentication before the first publish), `run.announce` (a warn-only stage
after the publish for pushing the release to update channels), the stage hooks (`run.beforeAll` … `run.postAnnounce`),
the outcome scripts (`run.onFail` / `run.onSkip`, run when a package fails or is skipped), `runScripts` (named shell
commands for `dispat run <name>`) and `tagFormat` — plus the top-level `parser` object (custom commit types, a default
propagation depth, strict types, parser limits; unset fields keep the specification defaults). Every script option takes
one script name or an array run in order. All are covered in
[Configuration & CLI](configuration.md). Annotated full examples:
[`dispat.example.json`](../dispat.example.json), [`dispat.example.yaml`](../dispat.example.yaml).

## Commit convention

Name the package in the commit scope:

```
fix(core): close leak            # patch, core only
feat(core): add streaming        # minor, core only
feat(core)!: drop the old API    # major, core only
```

**Reaching consumers is opt-in.** A plain `feat(core):` releases `core` and nothing else — add a caret to say how far
the change reaches:

```
feat(core)^: add streaming       # core + its direct consumers
feat(core)^^: add streaming      # core + every transitive consumer
feat(core)+2: add streaming      # core + consumers up to two edges away
feat(core)^minor: add streaming  # consumers take minor instead of the default patch
```

Consumers reached this way take a `patch` unless the commit says otherwise, and their own commits still win if they
demand more. If you are coming from a tool where propagation was automatic, this is the one habit to change: without a
caret, dependants are not bumped.

A few more forms worth knowing early — the full reference is in
[Configuration & CLI](configuration.md#commit-message-reference):

```
fix(core,utils): shared fix      # several packages
fix(*,-app): workspace-wide      # everything except app
fix: touch up the loader         # no scope: the packages owning the changed files
feat(core)@beta: try it out      # put core on a beta prerelease line
release(core)@stable: promote    # graduate it back
release(app): hold               # with a `Release-As: none` footer: withhold app
cancel(app): drop pending work   # discard app's unreleased metadata
```

A commit whose scope names no known package is an error by default — a typo would otherwise silently drop a release.
Scopes that are deliberately not packages (like `release`) go in `nonPackageScopes`.

## Commands

```sh
dispat init                 # write a starter dispat.json (--format yaml/toml for the others);
                            # never overwrites an existing file
dispat status               # print the project graph and planned versions; changes nothing
dispat                      # release (default command): full pipeline
dispat release --root path  # same, explicit
dispat run lint             # run the "lint" runScripts entry inside each changed
                            # package, honouring the dependency graph; releases nothing
dispat lint                 # the same — an unknown command word means "run <word>"
dispat run lint --on-error continue   # keep running dependents of a failed package
dispat test build core      # run the top-level "build" script once inside packages/core
                            # with core's full DISPAT_* environment; releases nothing
dispat preview core         # print core's pending release notes (breaking changes,
                            # features, fixes) — what its next changelog entry would say
dispat --concurrency 4,2    # override build/publish parallelism
dispat run lint --concurrency 8       # dispat run uses the build (first) value as its budget
dispat --log-format json    # machine-readable logs for CI
dispat --log-level debug    # more verbose output
dispat --version            # print the dispat version (release binaries carry
                            # the release tag's version; local builds say dev)
```

## Running in CI (GitHub Actions example)

```yaml
jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create tags and releases
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0      # full history: dispat reads tags and commit ranges
      - uses: actions/setup-go@v5
      - run: go run . --log-format json
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # GITHUB_REPOSITORY is set automatically by Actions
      - run: git push origin --tags   # publish the tags dispat created
```

Notes:

- `fetch-depth: 0` matters — without full history and tags the planner cannot find previous releases, and a shallow
  clone is not detected.
- By default dispat creates tags locally; push them after a successful run (as above). Alternatively enable
  `"commit": {"enabled": true, "push": true}` in the config: dispat then creates one release commit (changelogs +
  manifest changes), places the tags on it and pushes everything itself — drop the manual `git push` step. This requires
  a checked-out branch (`actions/checkout` with a `ref`), and remote access is verified before any work starts.
- Known limitations: shallow clones are not detected (always use `fetch-depth: 0`), and concurrent dispat runs on the
  same checkout are not guarded by a lock — serialize release jobs in CI.
- GitHub releases are created via the API for every published package whose scripts exported `DISPAT_EXPORT_GITHUB` —
  the export is the per-package opt-in, and its value (a list of absolute paths) names the files attached as release
  assets: `echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz" >> "$DISPAT_OUTPUT"` (an empty value releases without assets;
  an invalid path is skipped with a warning). In release-commit mode the release body always documents the release
  commit SHA and tag. With `commit.push` enabled releases are created after the push and pinned to the release commit
  via `target_commitish`; without it, GitHub creates the tag ref at the default branch head until you push (the true
  commit/tag stay recorded in the body) — or disable releases entirely with `"github": {"enabled": false}`.
- Any script — the login included — can export values for the later scripts `GITHUB_OUTPUT`-style through
  `$DISPAT_OUTPUT`: append `DISPAT_OUTPUT_<NAME>=value` lines and later scripts read `$DISPAT_OUTPUT_<NAME>`, with
  `$DISPAT_OUTPUT_SOURCE_<NAME>` naming the script that exported it. See
  [Script outputs](configuration.md#script-outputs).
- Exit code is non-zero when any package fails, so the job fails visibly while unaffected packages still released.
- Run `dispat status` on pull requests to review the plan before it is a release: it prints the diagnostics, the graph
  in publish order, and every version and channel transition, without touching anything.

## Reviewing a plan

Four things in a plan cannot be explained by reading the commit log alone, so dispat always reports them:

| Marker           | Meaning                                                                                                     |
|------------------|-------------------------------------------------------------------------------------------------------------|
| **catch-up**     | The package is releasing only because a dependency published in an *earlier* run. Carries that version.     |
| **channel-only** | The package is releasing only because its channel changed. Carries both channels.                           |
| **held**         | The package accumulated a bump but `Release-As: none` is withholding it. Carries the version it would have. |
| **blocked**      | The package was planned and never attempted, because a dependency failed to publish.                        |

A caret that reached a consumer and could not oblige it — a stable consumer of a prerelease — is reported too, so a
directive that looks like it should have released something and did not is visible rather than silent.
