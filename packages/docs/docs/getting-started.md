---
description: Install dispat, discover your package graph, and preview an ordered release plan before anything is published.
---

# Getting started

This guide takes you from installation to a release plan you can inspect before publishing. You will configure the
package folders and commands, discover their dependencies, and review the versions with `dispat status`.

You need Git with full history and tags, plus the tools your build and publish commands use. Different languages can
share a release: each package is a folder, and its stages run shell commands. Automatic manifest discovery and updates
cover a [defined set of formats](./editing/manifests.md).

The workflow below shows the whole sequence. `init` and `compute --write` create or update configuration; `status`
only displays the plan. Run the final `dispat` command when you are ready to publish.

```sh
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
dispat init             # create dispat.json
# Edit package paths and build and publish commands before continuing
dispat compute --write  # read the manifests: the dependency graph and the current versions
dispat status           # the plan, with nothing touched
dispat                  # release
```

## Install

The installer downloads the binary for your platform and verifies its published checksum. The binary does not need a
language runtime. If the installation directory is not on your `PATH`, follow the note the installer prints.

Use either curl or wget on Linux and macOS.

```sh title="Linux and macOS"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```sh title="Linux and macOS, with wget"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

Use PowerShell on Windows.

```powershell title="Windows"
irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex
```

Pass options to pin a version or to install somewhere else.

```sh title="Pin a version"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh -s -- --version 1.2.3
```

```sh title="Install somewhere else"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh -s -- --bin-dir ~/.local/bin
```

Pass either `1.2.3` or `services/dispat/v1.2.3` to `--version`. The script installs the latest stable release when you
omit a version. Run the script with `--help` to see the rest of the options.

Download the script first because piping into `iex` leaves no way to pass flags. You can then pass `-Version` and
`-BinDir` to the Windows script for the same options.

```powershell
irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 -OutFile install.ps1
.\install.ps1 -Version 1.2.3
```

## First configuration

Run `dispat init` at the repository root to create `dispat.json`. Use `--format yaml` or `--format toml` if you prefer.
Later commands discover the first existing `dispat.json`, `dispat.yaml`, `dispat.yml`, or `dispat.toml`; `--config`
selects a different file.

This example configures npm packages under `packages/libs`. Replace its paths and commands with the ones your project
uses:

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

Every direct sub-folder of `packages/libs` is now a package named after its folder. Create a
[`.dispatexclude`](./configuration/spaces.md#dispatexclude) file in the space folder to exempt some packages. You can
also configure a space and adjust its packages from a
[config file in its own folder](./configuration/spaces.md#the-space-configuration-file).

Declare dependencies so dispat can order the releases. Here, `app` consumes `core`. The edge also lets an explicit
propagation directive reach `app`; declaring the edge alone does not make every change propagate.

```json
{
  "dependencies": {
    "app": ["core"]
  }
}
```

After configuring the package paths, run [`dispat compute`](./cli/compute.md). It reads supported manifests and
proposes dependency edges and `initials` for versions your packages already have. Those starting versions help an
existing project continue from its published releases.

Review the proposal, then add `--write` to save it. Without that flag, the command writes nothing. See the
[adoption recipe](./examples/adopting.md#adopt-dispat-in-a-repository-that-already-ships-versions) for a worked example.

Once the basic plan is right, configure the additional behavior you need:

- concurrency budgets, tag formats and log settings: the [top-level options](./configuration/README.md);
- build and publish ordering, hooks, login, `scripts` and `dispat run`: [spaces](./configuration/spaces.md);
- packages that must share a version, or a major: [shared versions](./reference/releasing/versioning.md);
- changelogs, GitHub releases and the release commit: [release records](./configuration/records.md);
- commit-message policies and parser settings: [parsing options](./configuration/parser.md).

Open [`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json) and
[`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml) to see every
option in one annotated file. Four smaller files annotate one arrangement each: a
[control repository](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.control.yaml), a
[peer of a choreographed fleet](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.peer.yaml),
and the [orchestrator](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.orchestrator.yaml)
and [worker](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.worker.yaml) of a release
whose builds run on several machines.

## Commit convention

Name the package in the commit scope.

```
fix(core): close leak            # patch, core only
feat(core): add streaming        # minor, core only
feat(core)!: drop the old API    # major, core only
```

**Reaching consumers is opt-in.** Write a plain `feat(core):` to release `core` and nothing else. Add a caret to say
how far the change reaches.

```
feat(core)^: add streaming       # core + its direct consumers
feat(core)^^: add streaming      # core + every transitive consumer
feat(core)+2: add streaming      # core + consumers up to two edges away
feat(core)^minor: add streaming  # consumers take minor instead of the default patch
```

Consumers reached this way take a `patch` unless the commit requests another bump. A consumer's own commits can
require a larger bump. Propagation is explicit: use `^`, `^^`, `+N`, or the equivalent footers when consumers should
release too.

These forms cover multiple packages, prereleases, and held releases. The [commit reference](./reference/commits.md)
explains each directive and its limits.

```
fix(core,utils): shared fix      # several packages
fix(*,-app): workspace-wide      # everything except app
fix: touch up the loader         # no scope: the packages owning the changed files
feat(core)%beta: try it out      # put core on a beta prerelease line
release(core)%stable: promote    # graduate it back
release(app): hold               # with a `Release-As: none` footer: withhold app
cancel(app): drop pending work   # discard app's unreleased metadata
```

A commit whose scope names no known package is an error by default. This prevents a typo from silently dropping a
release. Put scopes that are deliberately not packages, such as `release`, in `nonPackageScopes`.

## Reviewing a plan

Run `dispat status` to print the plan a release would execute. This command touches nothing on disk.

```console
$ dispat status
14:02:11 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=2 package=core reason=direct space=libs version="1.2.3 -> 1.3.0"
14:02:11 INF ● changed bump=patch channel=stable dueToProviders=[core] ownCommits=0 package=app reason="propagated from core" space=libs version="0.8.1 -> 0.8.2"
14:02:11 INF unchanged channel=stable package=tools space=libs version=2.0.0
14:02:11 INF release plan ready held=0 packages=3 releasing=2
```

Use the reasons and status markers to understand packages that do not have an ordinary direct change:

| Marker           | Meaning                                                                                                     |
|------------------|-------------------------------------------------------------------------------------------------------------|
| **catch-up**     | The package is releasing only because a dependency published in an *earlier* run. Carries that version.     |
| **channel-only** | The package is releasing only because its channel changed. Carries both channels.                           |
| **held**         | The package accumulated a bump but `Release-As: none` is withholding it. Carries the version it would have. |
| **blocked**      | The package was planned and never attempted, because a dependency failed to publish.                        |

The plan also explains when a propagation directive reaches a consumer that cannot release, such as a stable
consumer of a prerelease. Check that explanation if you expected a consumer to appear in the release.

## Running in CI

Once the local plan looks right, move the release into CI. This GitHub Actions example checks out full history and
runs dispat with permission to write release records:

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
      - uses: yohimik/dispat@v1
      - run: dispat --log-format json
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # GITHUB_REPOSITORY is set automatically by Actions
      - run: git push origin --tags   # publish the tags dispat created
```

Read [dispat in CI](./reference/ci.md) to see the action's inputs. That page also covers the container images for every
other CI system and what a job needs beyond this.

Check these four details when adapting the job:

- Set `fetch-depth: 0` because the planner reads tags and commit ranges. It needs the full history. dispat detects and
  refuses a shallow clone with error `E196` rather than silently planning over it.
- Push tags after a successful run as above, because dispat creates tags locally by default. Alternatively, enable the
  [release commit](./configuration/records.md#commit) by setting `"commit": {"enabled": true, "push": true}`. dispat
  then creates one commit carrying the changelogs and manifest changes, places the tags on it, and pushes everything
  itself. It creates [GitHub releases](./configuration/records.md#github) for every published package whose scripts
  exported `DISPAT_EXPORT_GITHUB`, and attaches the export's file paths as assets. Read
  [script outputs](./reference/environment.md#script-outputs) to see the export mechanism.
- dispat refuses two releases at once rather than racing them. A run claims the repository before it plans anything by
  pushing a `dispat-release-lock` tag to your remote. The push is rejected and the run stops without doing anything if
  the name is already taken. dispat gives the tag back on every way out, including a failed package and a Ctrl-C. This
  tells a second job triggered while the first is still going to come back later instead of publishing the same
  versions twice. Read [The release lock](./reference/releasing/release-lock.md).
- The exit code is non-zero when any package fails. The job fails visibly while unaffected packages still release. Run
  `dispat status` on pull requests to review the plan before it becomes a release.

See [From one package to many](./examples/one-to-many.md) to add deliverables while preserving existing package
identities and release history.

## Other installation options

Use these instructions for PATH setup, source builds, or an npm-managed installation.

### Putting dispat on your PATH permanently

The script installs to `/usr/local/bin` when it is writable, which is already on `PATH` on most systems. It falls back
to `~/.local/bin` otherwise. A plain `export PATH=...` lasts only for the current shell, so append the line to your
shell profile and open a new terminal if the script prints the PATH note.

```sh title="zsh (the macOS default)"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

```sh title="bash"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

```powershell title="Windows, for your user account"
[Environment]::SetEnvironmentVariable('Path', "$env:LOCALAPPDATA\dispat\bin;" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')
```

Check `dispat --version` after an install. An older binary earlier on `PATH` is shadowing the new one if the command
answers with an older version. The install script warns about this. Run `which -a dispat` (or `Get-Command dispat` on
Windows) to list every copy in `PATH` order, then remove the stale copy or reorder the directories in your profile.

You can also install from Go source:

```sh
go install github.com/yohimik/dispat/services/dispat@latest
```

Alternatively, download one of the prebuilt binaries for Linux, macOS and Windows, on Intel and on ARM. You can find
them attached to every [GitHub release](https://github.com/yohimik/dispat/releases). Put the binary on your `PATH`
yourself.

For a repeatable CI setup, use the [GitHub Action](./reference/ci.md#the-github-action), a
[container image](./reference/ci.md#the-container-images), or the install script with a pinned version.

For a directly installed binary, `dispat self-update` installs a newer release and keeps the old binary for a week.
Update checks can report available versions after a command. See [Updating dispat](./reference/self-update.md) for
rollback, version selection, and installation methods that use a different update path.

To install another tool from GitHub release assets, use
`dispat install https://github.com/owner/repo --asset '<file>'`. The [install reference](./cli/install.md) explains asset
selection, verification, and where the executable is placed.

### Install with npm

Node projects can install the same native CLI through npm:

```sh
npm install --global @dispat/bin --allow-scripts=@dispat/bin
dispat --version
```

The package supports Node `^20.17.0 || >=22.9.0` and Linux, macOS, and Windows on x64 and ARM64.
Installation downloads the package's pinned GitHub release and checks the size, SHA-256 digest, and binary version.
It needs HTTPS access to GitHub and its release-asset hosts. npm proxy and custom certificate settings apply.

For a local development dependency, run `npm install --save-dev @dispat/bin`, then `npm exec -- dispat status`.
The npm and native CLI versions share a major/minor line but have independent patches. `dispat --version` reports
the native version; inspect the installed npm package to see its distribution version.

npm 12 requires script approval. For a global registry install, use
`npm install --global @dispat/bin --allow-scripts=@dispat/bin`. For local installs, retain the project's
`allowScripts` policy or use the explicit installer below.

If scripts were disabled, ordinary commands explain how to repair the installation without downloading anything:

```sh
npm explore @dispat/bin -- node build/bin/postinstall.js
# For a global install:
npm explore --global @dispat/bin -- node build/bin/postinstall.js
```

The global form works from any directory. Do not run the local
`node_modules/@dispat/bin/...` path after a global install; it resolves under the current directory. A failed
`dispat` launch prints a repair command with the exact installed package path, including safe quoting for spaces and
shell metacharacters.

With pnpm, approve this package through [its script policy](https://pnpm.io/cli/approve-builds), or run
`node node_modules/@dispat/bin/build/bin/postinstall.js` explicitly. Set `DISPAT_NPM_DEBUG=1` when diagnosing an install;
installer diagnostics go to stderr. See [npm updates](./reference/self-update.md#npm-installations) and
[npm in CI](./reference/ci.md#npm-in-node-pipelines).

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
