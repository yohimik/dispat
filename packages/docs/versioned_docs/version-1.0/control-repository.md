# A control repository for many repositories

When your code lives in many repositories, one small repository can hold every dispat configuration and link the
others in as git submodules. dispat runs in that one repository and treats the whole fleet as a single monorepo: one
dependency graph, releases in the right order, a version and a changelog for each linked repository. Nobody working in
those repositories has to learn dispat, change how they commit, or maintain a release pipeline.

This page is the pattern in full: the shape, why it works, the two layouts it comes in, a complete configuration, and
what it costs.

## The problem it solves

[One repository or many](./monorepo.md) explains why dispat cannot order releases across repositories: the graph is the
packages in one checkout, and separate repositories have no shared history to read.

The way around that is not to merge anyone's code. It is to create one more repository, small and boring, that
contains no product code at all. It holds the dispat configuration, the build and publish commands, and a git
submodule for each repository you want to release. That repository is the single checkout dispat needs. The teams keep
their own repositories, their own permissions and their own review rules, and the release machinery lives in exactly
one place.

Teams usually call it the control repository, the platform repository, or just `release`. This page calls it the
**control repository** and calls the repositories it points at **linked repositories**.

## The shape

```text
platform/                       the control repository
  dispat.yaml                   every configuration, in one file
  tools/sync.sh                 the script that moves the pointers
  libs/
    sdk/
      CHANGELOG.md              written here, by dispat
      src/                      submodule -> github.com/acme/sdk
  services/
    api/
      CHANGELOG.md
      src/                      submodule -> github.com/acme/api
    web/
      CHANGELOG.md
      src/                      submodule -> github.com/acme/web
```

Three parts, and only three:

- **The control repository** is a normal dispat monorepo. `libs` and `services` are spaces, `sdk`, `api` and `web` are
  packages, and each package is a folder like any other.
- **The linked repositories** are untouched. They hold source code and nothing else. No `dispat.yaml`, no commit
  convention, no release job.
- **The pointer** is what a git submodule actually is: a single entry in the control repository recording which commit
  of the linked repository this checkout uses.

## Why it works

Git does not store a submodule's files in the parent repository. It stores one entry, at the submodule's path, holding
a commit hash. Moving a linked repository forward is therefore an ordinary commit in the control repository that
changes exactly one path:

```console
$ git -C services/api/src fetch origin && git -C services/api/src checkout origin/main
$ git add services/api/src
$ git commit -m "feat(api): a health endpoint"
$ git show --name-only --format="%s" HEAD
feat(api): a health endpoint

services/api/src
```

That last line is the whole trick. dispat decides which package a commit touched by looking at the paths it changed,
and `services/api/src` sits inside the `api` package folder. So a pointer bump is a change to `api`, exactly as if
someone had edited a file there. Everything dispat does with an ordinary change now applies: the version, the
changelog, the tag, the propagation to consumers, the ordering of the build.

Scopes work the way they do everywhere else, which means the commit above did not need its `(api)` scope at all. A
commit with no scope is attributed to the packages whose folders it touched, so a bare `fix: pick up the latest api`
reaches the same package. Writing the scope is still worth it, because it is what makes the log readable.

## What you get back

- **One dependency graph across repositories.** An edge from `api` to `sdk` is declared once, in the control
  repository, whatever repositories the two live in.
- **Releases in topological order.** The library publishes before the service that consumes it, and unrelated
  packages publish in parallel.
- **Propagation.** A breaking change in one repository can bump every repository that depends on it, in the same run.
- **A version, a changelog and a tag per linked repository**, all independent of each other.
- **One pipeline** instead of one per repository.
- **Selective work.** [`dispat run --since`](./cli/run.md) and [`dispat if --changed`](./cli/if.md) narrow a job to the
  packages a pointer bump reached, so the pipeline cost follows the change and not the size of the fleet.

Here is the payoff. `api` depends on `sdk`, they live in different repositories, and one commit moved the `sdk`
pointer. The `^` on the type is the [reach directive](./reference/commits.md), which says "and the packages that
depend on this one":

```console
$ git commit -m "feat(sdk)^: a retry policy for every client call"
$ dispat status
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=sdk reason=direct space=libs version="0.0.0 -> 0.1.0"
09:31:07 INF ● changed bump=patch channel=stable dependsOn=["sdk"] dueToProviders=["sdk"] ownCommits=0 package=api reason="propagated from sdk" space=services version="0.0.0 -> 0.0.1"
09:31:07 INF unchanged channel=stable package=web space=services version=0.0.0
09:31:07 INF release plan ready held=0 packages=3 releasing=2
```

Two different repositories are releasing, in order, from one commit, and a third correctly stayed out of it.

## What stays separate, and why that is the point

The versions live in the control repository. The tags are its tags, the changelog files are its files, and its history
is the release history. The linked repositories keep their own history and their own tags, untouched.

That separation is the reason to do this rather than a drawback of it. The people working in the linked repositories
open pull requests, review them and merge them exactly as they do today. They never write a conventional commit, never
edit a `dispat.yaml`, and never wonder why a release did not fire. All of that lives in one repository, usually owned
by one team.

It has a real cost, and it is worth being clear about it. **The control repository sees one commit per bump, not the
history behind it.** A pointer moving from one commit to another is a single event, so the release notes are only as
good as the message on that one commit. [Where the version comes from](#where-the-version-comes-from) is entirely
about writing that message well, including a script that generates it from the linked repository's own log.

## Two layouts

There are two places the submodule can sit, and the difference matters more than it looks.

### A wrapper folder

The package is a folder the control repository owns, and the submodule sits inside it, conventionally at `src`:

```text
services/api/            the package
  CHANGELOG.md           the control repository's file
  src/                   submodule -> github.com/acme/api
```

This is the layout to prefer. The reason is one git rule: **a parent repository cannot commit files that live inside a
submodule.** `git add services/api/src` stages the pointer and nothing else, so anything dispat writes inside the
submodule is left behind as a modification in the linked repository's working copy. With a wrapper folder, the
changelog, any per-package config file and anything else generated during a release land in the wrapper, where the
release commit stages them normally:

```console
$ git show --stat HEAD
commit 4b30761ce873ec6b9608eea50b982be86e7de126

    chore(release): sdk@0.1.0, api@0.0.1, web@0.1.0

 libs/sdk/CHANGELOG.md     |  8 ++++++++
 services/api/CHANGELOG.md |  7 +++++++
 services/web/CHANGELOG.md | 15 +++++++++++++++
 3 files changed, 30 insertions(+)
```

Two things to know about a wrapper:

- The wrapper has no manifest of its own, so set
  [`autoVersion.manifests: "all"`](./configuration/autoversion.md) when you want the manifests inside the submodule
  reconciled. The default, `root`, looks only at the wrapper's own folder and finds nothing there.
- If a package needs to be known by the name its manifest uses rather than its folder name, state it with
  [`manifestNames`](./configuration/packages.md).

Optionally add `src: src` to the package. That narrows what counts as a change to the submodule pointer alone, so
editing the wrapper's own files does not trigger a release. The pointer still counts, because its path is exactly the
folder `src` names.

### The submodule as the package

The submodule is mounted directly as the package folder:

```text
services/api/            submodule -> github.com/acme/api
```

Flatter, and easier to explain to someone reading the repository for the first time. It costs two things.

**The changelog would be written inside the linked repository.** dispat writes a package's changelog in the package
folder, which is now the submodule, so the file is created in the linked repository's working copy and no commit in
the control repository can pick it up:

```console
$ dispat release
09:31:07 INF summary channel=stable package=api status=published tag=api@0.1.0 took=0.4s version="0.0.0 -> 0.1.0"
$ git status --short
 ? services/api
$ git -C services/api status --short
?? CHANGELOG.md
```

The file exists, nothing tracks it, and the next checkout throws it away. So this layout means
`changelog: {enabled: false}` and using the GitHub release as the record instead.

**A file in the linked repository can change dispat's behaviour.** dispat reads a `dispat.yaml` from a package's own
folder as a configuration layer. Under this layout that folder belongs to another team, who do not know they own a
dispat config file. A file that happens to be named that way is read, and one declaring `spaces` or `packages` is
refused outright.

### Choosing

|                                            | A wrapper folder                     | The submodule as the package        |
|--------------------------------------------|--------------------------------------|-------------------------------------|
| Where the changelog lands                  | the control repository, committed    | inside the submodule, uncommitted   |
| What the release commit can stage          | everything the release wrote         | the pointer only                    |
| Can a file in the linked repository affect dispat | no                            | yes, its own folder is a config layer |
| Extra folder to explain                    | yes, one per package                 | no                                  |
| Manifest discovery                         | needs `manifests: "all"`             | works by default                    |

Take the wrapper unless you have decided you do not want changelog files at all. The rest of this page uses it, and
notes where the flat layout differs.

## The configuration

One file, for the tree at the top of this page:

```yaml
scripts:
  npm-login: npm config set //registry.npmjs.org/:_authToken $NPM_TOKEN
  build: npm --prefix src ci && npm --prefix src run build
  publish: npm --prefix src publish --access public

spaces:
  libs:
    path: libs
    flow:
      login: [npm-login]
      build: [build]
      publish: [publish]
  services:
    path: services
    flow:
      login: [npm-login]
      build: [build]
      publish: [publish]

dependencies:
  api: [sdk]

commit:
  enabled: true
  push: true
```

Nothing here is special to the pattern. It is an ordinary dispat configuration, and the only sign that these packages
live in other repositories is `--prefix src`, which points npm at the submodule inside each package folder. Under the
flat layout the commands would be plain `npm ci` and `npm publish`, because the package folder is the submodule.

`commit.enabled` writes the release commit that carries the changelog entries, and `commit.push` sends it and the
tags to the control repository's remote. The pointers themselves were committed earlier, by whoever moved them.

## Where the version comes from

The pointer bump commit is the only input dispat has, so how that commit gets written decides how good this pattern
feels. Three ways, from simplest to most automated. They combine freely.

### Someone moves the pointer

A person, or a bot such as Renovate, opens a pull request in the control repository that moves one submodule forward
and describes it:

```sh
git -C services/api/src fetch origin
git -C services/api/src checkout origin/main
git add services/api/src
git commit -m "feat(api): a health endpoint"
```

This is worth starting with. It reads well, it puts the release decision in front of a reviewer, and it needs no
tooling at all. It stops scaling when the fleet is large enough that nobody wants to write those messages.

### The linked repository asks for a release

The linked repository's own CI, on merge to its main branch, tells the control repository what happened. It sends a
`repository_dispatch` event carrying the package name and the bump it wants:

```yaml
- name: Ask the control repository to release
  run: |
    gh api repos/acme/platform/dispatches \
      --field event_type=bump \
      --field 'client_payload[package]=api' \
      --field 'client_payload[bump]=minor' \
      --field 'client_payload[summary]=a health endpoint'
  env:
    GH_TOKEN: ${{ secrets.PLATFORM_TOKEN }}
```

A workflow in the control repository receives it, moves the pointer, and commits
`feat(api): a health endpoint`. This is the only option that puts anything in the linked repository, and it is a few
lines that never mention dispat.

### A sync job writes the commit

The control repository fetches every linked repository on a schedule and writes the commits itself, deriving each one
from the linked repository's own log. This scales best, and it is the version most fleets end up on.

```sh title="tools/sync.sh"
#!/bin/sh
# Move one linked repository forward and write the commit dispat reads.
#
#   tools/sync.sh <package> <folder> [branch]
#
# Exits 0 without committing when the linked repository has not moved.
set -eu

name=$1
dir=$2
branch=${3:-main}

old=$(git rev-parse "HEAD:$dir")
git -C "$dir" fetch --quiet origin "$branch"
new=$(git -C "$dir" rev-parse FETCH_HEAD)

if [ "$old" = "$new" ]; then
	echo "$name: already up to date"
	exit 0
fi

git -C "$dir" checkout --quiet "$new"
git add "$dir"

# One dispat unit per upstream commit, units separated by a --- line. A
# conventional commit keeps its type and gets the package name as its scope;
# anything else becomes a fix, so it still lands in the changelog.
message=$(
	git -C "$dir" log --no-merges --reverse --format=%s "$old..$new" |
		awk -v name="$name" '
			NR > 1 { print "---" }
			{
				if (match($0, /^[a-z]+(\([^)]*\))?!?: /)) {
					header = substr($0, 1, RLENGTH)
					type = header
					sub(/[(!:].*$/, "", type)
					bang = (header ~ /!/) ? "!" : ""
					print type "(" name ")" bang ": " substr($0, RLENGTH + 1)
				} else {
					print "fix(" name "): " $0
				}
			}
		'
)

git commit --quiet --message "$message"
echo "$name: $(git rev-parse --short "$old") -> $(git rev-parse --short "$new")"
```

The part that makes this work is the `---` line. A dispat commit message can carry
[many units in one commit](./reference/commits.md), each with its own type and scope, so a single pointer bump
covering three upstream commits becomes three units and versions as if all three had been made in the control
repository. Here is the script against a linked repository whose team writes conventional commits sometimes and plain
sentences the rest of the time:

```console
$ ./tools/sync.sh web services/web/src
web: cc906dd -> 8ecceef
$ git log -1 --format=%B
feat(web): a keyboard shortcut menu
---
fix(web): fix broken links in the footer
---
perf(web): preload the hero image
```

The middle commit was not written to any convention. It became a fix, which is the safe reading, and it still reaches
the changelog:

```console
$ dispat status
09:31:07 INF unchanged channel=stable package=sdk space=libs version=0.1.0
09:31:07 INF unchanged channel=stable dependsOn=["sdk"] package=api space=services version=0.0.1
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=3 package=web reason=direct space=services version="0.1.0 -> 0.2.0"
09:31:07 INF release plan ready held=0 packages=3 releasing=1
$ dispat preview --changelog
## web@0.2.0 (stable)

### Features

- a keyboard shortcut menu


### Fixes

- fix broken links in the footer

- preload the hero image
```

Three commits, written by people who have never heard of dispat, in a repository dispat has never been installed in,
and the changelog reads properly.

Run one job per package, or loop over them and let a single commit cover several packages. The units in one commit may
carry different scopes, so one sync commit can release the whole fleet.

### Settings this implies

Whichever way the message gets written, a control repository that copies text from elsewhere wants three settings:

```yaml
parser:
  maxDescriptionLength: -1
  propagation:
    depth: 1

nonPackageScopes: [release, sync]
```

- `parser.maxDescriptionLength: -1` turns off the long-description warning. Upstream subject lines were not written to
  dispat's 100 character limit and there is no point being told so on every release.
- `parser.propagation.depth: 1` makes a bump reach the packages that depend on it without anyone writing the `^`
  directive. This matters more here than in an ordinary monorepo: a generated commit message cannot know how far a
  change should reach, and the person who wrote the upstream commit was never asked. Setting the default in the
  configuration is the honest place for that decision. Use `all` for the whole transitive closure, and leave it at
  the default `0` if you would rather each bump stay put until someone asks for more.
- `nonPackageScopes` should name whatever scope your own bookkeeping commits use, so a `chore(sync):` commit is not
  reported as naming a package that does not exist. `release` is in the default list and must stay.
- `commitErrors` should be left at its default, `warn`. A message assembled from someone else's subject lines will
  occasionally produce something dispat cannot parse, and that unit dropping out is a far better outcome than the
  whole release refusing to run.

## Building and publishing

Two models. The first is why most people adopt the pattern; the second is for fleets with pipelines they cannot
retire.

### The control repository builds

The `flow` in the control repository holds the build and publish commands, and they run against the checked-out
submodule. This is the configuration shown above: `npm --prefix src ci`, `npm --prefix src publish`.

Everything in [Examples](./examples/README.md) applies unchanged, because from dispat's point of view these are
ordinary packages in ordinary folders. `flow.login` runs once per space, so one registry login covers every package in
it. `DISPAT_NEW_VERSION` and the rest of the [script environment](./reference/environment.md) are there as usual.

This is the model that gets the linked repositories down to source code and nothing else, and it is the one to choose
when the repositories build in similar enough ways that a handful of shared scripts covers them.

### The control repository triggers each pipeline

If a linked repository has a build that only it knows how to run, the control repository can compute the version and
ordering and then hand off:

```yaml
scripts:
  publish: |
    gh workflow run release.yml --repo acme/$DISPAT_PACKAGE \
      --field version=$DISPAT_NEW_VERSION
    gh run watch "$(gh run list --repo acme/$DISPAT_PACKAGE \
      --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')" \
      --repo acme/$DISPAT_PACKAGE --exit-status
```

The `publish` script must not return until the publish has actually happened, because everything downstream of it,
the tag, the changelog and any consumer waiting on this package, treats a successful publish as the point of no
return.

You keep the ordering, the versions and the records, and you give up two things: each linked repository still has a
pipeline to maintain, and something has to sit and poll for the result.

### Making the release visible in the linked repository

The tag and the changelog live in the control repository, which is not where a user of the linked repository looks.
Three optional additions, each independent of the others:

- **Put the GitHub release on the linked repository.** `github.owner` and `github.repo` are per-package overrides:

  ```yaml
  packages:
    api:
      github:
        owner: acme
        repo: api
  ```

  The release, its notes and its assets then appear on `acme/api`, created with the same token.

- **Tag the linked repository too**, from a publish script:

  ```sh
  git -C src tag -a "v$DISPAT_NEW_VERSION" -m "v$DISPAT_NEW_VERSION"
  git -C src push origin "v$DISPAT_NEW_VERSION"
  ```

  Those tags are the linked repository's own. dispat never reads them, so they cannot confuse the version it computes.

- **Point dispat's own tag at a commit a script produced.** If a release script creates a commit somewhere, writing
  `PACKAGE_API=<hash>` to `$DISPAT_OUTPUT` (the package name uppercased) moves the tag and the GitHub release onto
  that commit instead of the release commit. See [script outputs](./reference/environment.md#script-outputs).

## Dependencies across repositories

This is the part that no arrangement of separate repositories can give you. Edges are declared in the control
repository, and a dependency that crosses a repository boundary is written exactly like one that does not:

```yaml
dependencies:
  api: [sdk]
  web: [sdk]
```

You do not have to write them by hand. [`dispat compute`](./cli/compute.md) reads the manifests inside the submodules
and finds the edges itself:

```console
$ dispat compute
+ add     api -> sdk (dependencies)  services/api/src/package.json dependencies "@acme/sdk": "^0.0.0"

1 suggestion(s); apply all with --write, choose with --interactive
```

It found a `package.json` two levels inside a wrapper, in a different repository, and derived an edge that dispat now
orders releases by.

### One caveat: writes inside a submodule

[Automatic version reconciliation](./configuration/autoversion.md) updates a consumer's manifest so its range points
at the provider's new version. Under a wrapper layout it needs `manifests: "all"` to see manifests inside the
submodule at all. Once it does, there is a wrinkle: that manifest lives in a linked repository, and the control
repository cannot commit it. The write still happens in the working tree, so the build uses the right version, but it
is not recorded anywhere.

Two reasonable ways to handle that, and they are genuinely both fine:

- **Let it be a build-time edit.** Keep `autoVersion` on so the build resolves against the version being released,
  and let each linked repository update its own ranges the way it already does, usually with Renovate or Dependabot.
  This keeps the linked repositories in charge of their own manifests.
- **Push the edit back.** Have a publish script commit the manifest change in the submodule and push it:

  ```sh
  git -C src commit -am "chore: reconcile dependency ranges" && git -C src push origin HEAD:main
  ```

  Choose this when the manifests should be the source of truth and the linked repository has no bot of its own.

## Starting from versions already published

The linked repositories have probably shipped versions already, and the control repository has no tags, so its first
run would start everything from scratch. State the baselines instead:

```yaml
initials:
  sdk: 2.3.1
  api: 1.8.0
  web: 0.14.2
```

The next release of `sdk` then continues from `2.3.1`. `dispat compute --write` fills this block in from the manifests
it finds, and [Adopting dispat](./examples/adopting.md) walks the whole procedure with transcripts.

## The release job

The one pipeline the whole fleet needs. The submodules must be checked out, and the history must be complete, because
dispat reads tags and commits:

```yaml
name: release
on:
  push:
    branches: [main]

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
          submodules: recursive
          token: ${{ secrets.FLEET_TOKEN }}
      - uses: yohimik/dispat@v1
      - run: dispat release
        env:
          GITHUB_TOKEN: ${{ secrets.FLEET_TOKEN }}
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
```

Two details specific to this pattern. `submodules: recursive` is required, and without it the package folders are
empty and nothing builds. `token` has to be a token that can read the linked repositories: the automatic
`GITHUB_TOKEN` is scoped to the repository running the job, so private submodules fail to clone with it.

The [release lock](./reference/releasing/release-lock.md) is a tag on the control repository's own remote, so two runs
of this job cannot overlap, and nothing else in the organisation is affected by it.

Add the sync job beside it if you are generating the bump commits:

```yaml
name: sync
on:
  schedule:
    - cron: '0 * * * *'
  workflow_dispatch:

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
          submodules: recursive
          token: ${{ secrets.FLEET_TOKEN }}
      - run: |
          git config user.name "dispat sync"
          git config user.email "sync@acme.example"
          ./tools/sync.sh sdk libs/sdk/src
          ./tools/sync.sh api services/api/src
          ./tools/sync.sh web services/web/src
          git push
```

The identity lines are not optional: a runner has no git user configured, and the script commits. The push lands on
`main`, which starts the release job above.

## What to watch for

- **The control repository sees the pointer, not the history.** Everything a release knows comes from the bump commit
  message. Get that message right and the rest follows.
- **A checkout without submodules produces empty folders.** The pointers are in the history either way, so the plan
  comes out identical and nothing warns. The failure arrives later, when a build finds nothing to build. Use
  `submodules: recursive` in every job, including the ones that only run `dispat run` or `dispat if`.
- **A shallow clone breaks the plan.** dispat counts commits since a tag, so the control repository needs its full
  history.
- **`revertOnFail` does not clean inside a submodule.** `git clean` refuses to descend into one, so a failed package
  can leave the linked repository's working copy modified. On an ephemeral CI runner this does not matter. On a
  long-lived one, reset the submodules at the start of the job.
- **A space `path` cannot point outside the repository.** Relative paths that escape the root are refused, which is
  exactly why the link is a submodule: it brings the code inside the checkout rather than reaching outside it.
- **A linked repository that is itself a dispat monorepo does not merge.** Put it in a wrapper, where its config file
  is never read, or exclude the folder with [`.dispatexclude`](./examples/layout.md). Do not mount it as a package
  and hope the two configurations agree.
- **Relative submodule URLs resolve against the control repository's own remote.** `git submodule add ../api.git` is
  convenient on GitHub, where every repository sits under the same owner, and confusing anywhere else. Absolute URLs
  are never wrong.

## See also

- [One repository or many](./monorepo.md) for the underlying decision, and for what changes if you ever do merge the
  repositories properly.
- [Adopting dispat](./examples/adopting.md) for deriving the graph and the starting versions from manifests.
- [Keeping configuration beside the code](./examples/layout.md) for splitting a growing control repository into per
  space and per package files.
- [dependencies](./configuration/dependencies.md) and [the compute command](./cli/compute.md) for the edges.
- [Examples](./examples/README.md) for the build and publish commands of your ecosystem, all of which apply unchanged.
