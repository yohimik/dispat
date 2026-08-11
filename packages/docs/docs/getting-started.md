# Getting started

From zero to a releasing monorepo: install the binary, write one config file, name packages in your commits, and wire
the release into CI.

## Install

The CLI lives in the `services/dispat` module of the repository (the binary installs as `dispat`):

```sh
go install github.com/yohimik/dispat/services/dispat@latest
```

Prebuilt binaries for Linux, macOS and Windows, on Intel and on ARM, are attached to every
[GitHub release](https://github.com/yohimik/dispat/releases); download one and put it on your `PATH` if you prefer not
to build from source.

A downloaded binary keeps itself current: `dispat self-update` replaces it with the latest release and keeps the old
one beside it for a week in case you want it back. Every command mentions a newer release on its way out, so you find
out without going looking. See [Updating dispat](./self-update.md).

## First configuration

Create `dispat.json` at your monorepo root; `dispat init` writes a starter one (`--format yaml` / `--format toml`
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
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Every direct sub-folder of `packages/libs` is now a package named after its folder (a
[`.dispatignore`](./configuration/spaces.md#dispatignore) file in the space folder can exempt some). A space can also be
configured, and its packages adjusted, from a [config file in its own folder](./configuration/spaces.md#the-space-configuration-file). Declare relations
between packages under `dependencies` so bumps propagate and ordering is enforced:

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

That is a complete, working configuration.

If the repository you are pointing it at is not brand new, run [`dispat compute`](cli.md#the-compute-command) once
before anything else. It reads the packages' manifests and offers you two things the config would otherwise need by
hand: the `dependencies` edges between them, and an `initials` entry for every package already at a version, so the
first release continues from where the manifests are instead of starting again at `0.0.1`. Nothing is written until you
add `--write`. The [adoption recipe](cookbook.md#adopt-dispat-in-a-repository-that-already-ships-versions) walks
through a real one.

Everything else is optional and layered on top:

- concurrency budgets, tag formats, log settings: the [top-level options](configuration/README.md);
- build/publish ordering, hooks, login, `scripts` and `dispat run`: [spaces](configuration/spaces.md);
- packages that must share a version, or a major: [shared versions](versioning.md);
- changelogs, GitHub releases, the release commit: [release records](configuration/records.md);
- commit-message policies and parser tweaks: [parsing options](configuration/parser.md).

[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json) and [`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml) show every option in
one annotated file.

## Commit convention

Name the package in the commit scope:

```
fix(core): close leak            # patch, core only
feat(core): add streaming        # minor, core only
feat(core)!: drop the old API    # major, core only
```

**Reaching consumers is opt-in.** A plain `feat(core):` releases `core` and nothing else. Add a caret to say how far the
change reaches:

```
feat(core)^: add streaming       # core + its direct consumers
feat(core)^^: add streaming      # core + every transitive consumer
feat(core)+2: add streaming      # core + consumers up to two edges away
feat(core)^minor: add streaming  # consumers take minor instead of the default patch
```

Consumers reached this way take a `patch` unless the commit says otherwise, and their own commits still win if they
demand more. If you are coming from a tool where propagation was automatic, this is the one habit to change: without a
caret, dependants are not bumped.

A few more forms worth knowing early; the full reference is in
[Commit messages](commits.md):

```
fix(core,utils): shared fix      # several packages
fix(*,-app): workspace-wide      # everything except app
fix: touch up the loader         # no scope: the packages owning the changed files
feat(core)%beta: try it out      # put core on a beta prerelease line
release(core)%stable: promote    # graduate it back
release(app): hold               # with a `Release-As: none` footer: withhold app
cancel(app): drop pending work   # discard app's unreleased metadata
```

A commit whose scope names no known package is an error by default, because a typo would otherwise silently drop a
release. Scopes that are deliberately not packages (like `release`) go in `nonPackageScopes`.

## Commands

```sh
dispat init                 # write a starter dispat.json (--format yaml/toml for the others);
                            # never overwrites an existing file
dispat compute              # read the manifests and offer the dependency edges and the
                            # starting versions they imply; --write applies them, --check
                            # fails CI when the config has fallen behind
dispat status               # print the project graph and planned versions; changes nothing
dispat                      # release (default command): full pipeline
dispat release --root path  # same, explicit
dispat run lint             # run the "lint" script in every changed package that has one,
                            # in dependency order; releases nothing. Where you define the
                            # script decides the reach: at the top it runs everywhere, on a
                            # space in that space, on a package in that package alone
dispat lint                 # the same: an unknown command word means "run <word>"
dispat run lint --on-error continue   # keep running dependents of a failed package
dispat run lint -p core,web # narrow to named packages; -s libs narrows to a space's,
                            # -g platform to a version group's, -p '*' covers every one.
                            # Standing in packages/core, plain "dispat lint" narrows to
                            # core the same way
dispat run build --since all -p core  # run "build" inside packages/core with core's full
                            # DISPAT_* environment whether or not it changed; releases
                            # nothing. A filter narrows the window, --since all opens it
dispat preview              # pending release notes for every package with something pending
dispat preview -p core      # print core's pending release notes (breaking changes,
                            # features, fixes): what its next changelog entry would say
dispat changelog            # write pending changelog entries now (custom flows;
                            # the release skips entries already written)
dispat autoversion          # reconcile manifests to the planned versions
dispat commit --tag --push  # per-package release commit, tag and push
dispat github               # create the pending GitHub releases now (for a flow that
                            # publishes from its own stage; already-published ones skip)
dispat --help               # the command list; "dispat <command> --help" for one
                            # command's own flags
dispat --concurrency 4,2    # override build/publish parallelism
dispat run lint --concurrency 8       # dispat run uses the build (first) value as its budget
dispat --log-format json    # machine-readable logs for CI
dispat --log-level debug    # more verbose output
dispat --quiet-parser       # hide the commit-message parser's diagnostics for this run
dispat --version            # print the version and platform, e.g. 1.4.0 (linux_amd64)
                            # (release binaries carry the release tag's version;
                            # local builds say dev)
```

## Reviewing a plan

`dispat status` prints the plan a release would execute, without touching anything:

```console
$ dispat status
14:02:11 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=2 package=core reason=direct space=libs version="1.2.3 -> 1.3.0"
14:02:11 INF ● changed bump=patch channel=stable dueToProviders=[core] ownCommits=0 package=app reason="propagated from core" space=libs version="0.8.1 -> 0.8.2"
14:02:11 INF unchanged channel=stable package=tools space=libs version=2.0.0
14:02:11 INF release plan ready held=0 packages=3 releasing=2
```

Four things in a plan cannot be explained by reading the commit log alone, so dispat always reports them:

| Marker           | Meaning                                                                                                     |
|------------------|-------------------------------------------------------------------------------------------------------------|
| **catch-up**     | The package is releasing only because a dependency published in an *earlier* run. Carries that version.     |
| **channel-only** | The package is releasing only because its channel changed. Carries both channels.                           |
| **held**         | The package accumulated a bump but `Release-As: none` is withholding it. Carries the version it would have. |
| **blocked**      | The package was planned and never attempted, because a dependency failed to publish.                        |

A caret that reached a consumer and could not oblige it (a stable consumer of a prerelease) is reported too, so a
directive that looks like it should have released something and did not is visible rather than silent.

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

- `fetch-depth: 0` matters. The planner reads tags and commit ranges, so it needs full history. A shallow clone is
  detected and refused (error `E196`) rather than silently planned over.
- By default dispat creates tags locally; push them after a successful run, as above. Alternatively enable the
  [release commit](configuration/records.md#commit) (`"commit": {"enabled": true, "push": true}`): dispat then creates
  one commit carrying the changelogs and manifest changes, places the tags on it and pushes everything
  itself. [GitHub releases](configuration/records.md#github) are created for every published package whose scripts
  exported `DISPAT_EXPORT_GITHUB`, with the export's file paths attached as assets; see
  [script outputs](environment.md#script-outputs) for the export mechanism.
- Concurrent dispat runs on the same checkout are not guarded by a lock, so serialize release jobs in CI.
- The exit code is non-zero when any package fails, so the job fails visibly while unaffected packages still released.
  On pull requests, run `dispat status` to review the plan before it becomes a release.
