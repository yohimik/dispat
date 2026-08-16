# Getting started

Set up monorepo release automation in four steps: install the binary, write one config file, name packages in your
commits, and wire the release into CI. Nothing below assumes a particular language.

dispat is built for polyglot monorepos. A package is a folder and a stage is a shell command, so it does not care
whether a package is npm, Go, Cargo, Maven, .NET, Python, Ruby, Dart, Docker, iOS or Android. They all go in the same
dependency graph and release in the same run.

If you want the shape of it before the detail, this is the whole path:

```sh
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
dispat init             # a starter dispat.json
dispat compute --write  # read the manifests: the dependency graph and the current versions
dispat status           # the plan, with nothing touched
dispat                  # release
```

## Install

The install script downloads the release binary for your platform, checks it against the checksum GitHub published,
and puts it on your `PATH`. There is no runtime to install first.

On Linux and macOS, with either curl or wget:

```sh title="Linux and macOS"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

```sh title="Linux and macOS, with wget"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
```

On Windows, in PowerShell:

```powershell title="Windows"
irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex
```

It takes options if you want them: a version to pin, or somewhere else to install.

```sh title="Pin a version"
curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh -s -- --version 1.2.3
```

```sh title="Install somewhere else"
wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh -s -- --bin-dir ~/.local/bin
```

`--version` accepts either spelling, `1.2.3` or `services/dispat/v1.2.3`. With no version it installs the latest
stable release, and `--help` lists the rest.

The Windows script takes the same options as `-Version` and `-BinDir`. Piping into `iex` leaves no way to pass them,
so download the script first:

```powershell
irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 -OutFile install.ps1
.\install.ps1 -Version 1.2.3
```

### Putting dispat on your PATH permanently

The script installs to `/usr/local/bin` when it is writable, which is already on `PATH` on most systems, and to
`~/.local/bin` otherwise. A plain `export PATH=...` lasts only for the shell it runs in, so if the script printed the
PATH note, append the line to your shell's own profile once and open a new terminal:

```sh title="zsh (the macOS default)"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
```

```sh title="bash"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

```powershell title="Windows, for your user account"
[Environment]::SetEnvironmentVariable('Path', "$env:LOCALAPPDATA\dispat\bin;" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')
```

If `dispat --version` still answers with an older version after an install, an older binary earlier on `PATH` is
shadowing the new one; the install script warns about this. `which -a dispat` (or `Get-Command dispat` on Windows)
lists every copy in `PATH` order, so remove the stale one or reorder the directories in your profile.

Two other ways, if they suit you better:

```sh
go install github.com/yohimik/dispat/services/dispat@latest
```

or download one of the prebuilt binaries for Linux, macOS and Windows, on Intel and on ARM, attached to every
[GitHub release](https://github.com/yohimik/dispat/releases), and put it on your `PATH` yourself.

In CI you usually want neither: GitHub Actions has [a composite action](./reference/ci.md#the-github-action), and every
other system can use [the container images](./reference/ci.md#the-container-images) or the install script above.

A downloaded binary keeps itself current. `dispat self-update` replaces it with the latest release and keeps the old
one beside it for a week in case you want it back, and every command mentions a newer release on its way out, so you
find out without going looking. See [Updating dispat](./reference/self-update.md).

## First configuration

Create `dispat.json` at your monorepo root. `dispat init` writes a starter one, with `--format yaml` or
`--format toml` for the other formats. No flag is needed afterwards: every command finds the first of `dispat.json`,
`dispat.yaml`, `dispat.yml` or `dispat.toml` that exists, and `--config` names a different file explicitly.

Here is a complete, working configuration:

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

Every direct sub-folder of `packages/libs` is now a package named after its folder. A
[`.dispatexclude`](./configuration/spaces.md#dispatexclude) file in the space folder can exempt some, and a space can
also be configured, and its packages adjusted, from a
[config file in its own folder](./configuration/spaces.md#the-space-configuration-file).

Declare relations between packages under `dependencies` so bumps propagate and publish order is enforced:

```json
{
  "dependencies": {
    "app": ["core"]
  }
}
```

If the repository you are pointing it at is not brand new, run [`dispat compute`](./cli/compute.md) once before
anything else. It reads the packages' manifests and offers two things the config would otherwise need by hand: the
`dependencies` edges between them, and an `initials` entry for every package already at a version, so the first release
continues from where the manifests are instead of starting again at `0.0.1`. Nothing is written until you add
`--write`. The [adoption recipe](./examples/adopting.md#adopt-dispat-in-a-repository-that-already-ships-versions) walks
through a real one.

Everything else is optional and layered on top:

- concurrency budgets, tag formats and log settings: the [top-level options](./configuration/README.md);
- build and publish ordering, hooks, login, `scripts` and `dispat run`: [spaces](./configuration/spaces.md);
- packages that must share a version, or a major: [shared versions](./reference/releasing/versioning.md);
- changelogs, GitHub releases and the release commit: [release records](./configuration/records.md);
- commit-message policies and parser settings: [parsing options](./configuration/parser.md).

[`dispat.example.json`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.json) and
[`dispat.example.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.example.yaml) show every
option in one annotated file.

## Commit convention

Name the package in the commit scope:

```
fix(core): close leak            # patch, core only
feat(core): add streaming        # minor, core only
feat(core)!: drop the old API    # major, core only
```

**Reaching consumers is opt-in.** A plain `feat(core):` releases `core` and nothing else. Add a caret to say how far
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

A few more forms are worth knowing early, and the full reference is in
[Commit messages](./reference/commits.md):

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
release. Scopes that are deliberately not packages, such as `release`, go in `nonPackageScopes`.

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

A caret that reached a consumer and could not oblige it, such as a stable consumer of a prerelease, is reported too, so
a directive that looks like it should have released something and did not is visible rather than silent.

## Running in CI

This is a complete GitHub Actions job:

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

[dispat in CI](./reference/ci.md) covers the action's inputs, the container images for every other CI system, and what
a job needs beyond this.

Four things about that job are worth knowing:

- `fetch-depth: 0` matters. The planner reads tags and commit ranges, so it needs full history. A shallow clone is
  detected and refused (error `E196`) rather than silently planned over.
- By default dispat creates tags locally, so push them after a successful run as above. Alternatively enable the
  [release commit](./configuration/records.md#commit) (`"commit": {"enabled": true, "push": true}`): dispat then
  creates one commit carrying the changelogs and manifest changes, places the tags on it and pushes everything itself.
  [GitHub releases](./configuration/records.md#github) are created for every published package whose scripts exported
  `DISPAT_EXPORT_GITHUB`, with the export's file paths attached as assets; see
  [script outputs](./reference/environment.md#script-outputs) for the export mechanism.
- Two releases at once are refused rather than raced. Before it plans anything, a run claims the repository by pushing
  a `dispat-release-lock` tag to your remote, and if the name is already taken the push is rejected and the run stops
  having done nothing. The tag is given back on every way out, a failed package and a Ctrl-C included, so a second job
  triggered while the first is still going is told to come back later instead of publishing the same versions twice.
  See [The release lock](./reference/releasing/release-lock.md).
- The exit code is non-zero when any package fails, so the job fails visibly while unaffected packages still released.
  On pull requests, run `dispat status` to review the plan before it becomes a release.
