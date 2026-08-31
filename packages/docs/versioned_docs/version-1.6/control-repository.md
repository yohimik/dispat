# A control repository for many repositories

When your code lives in many repositories, one small repository can hold every dispat configuration and link the others
in as git submodules. Run dispat in that one repository to treat the whole fleet as a single monorepo. You get one
dependency graph, releases in the right order, and a version and a changelog for each linked repository. Nobody working
in those repositories has to learn dispat, change how they commit, or maintain a release pipeline.

This page shows the pattern in full. It covers the shape, why it works, the two layouts it comes in, a complete
configuration, and what it costs.

## The problem it solves

[One repository or many](./monorepo.md) explains why dispat cannot order releases across repositories. The graph is the
packages in one checkout, and separate repositories have no shared history to read.

Create one more repository to get around this without merging anyone's code. This repository contains no product code
at all. It holds the dispat configuration, the build and publish commands, and a git submodule for each repository you
want to release. That repository provides the single checkout dispat needs. The teams keep their own repositories,
permissions, and review rules. The release machinery lives in exactly one place.

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

Set up three parts, and only three:

- **The control repository** is a normal dispat monorepo. `libs` and `services` are spaces, `sdk`, `api` and `web` are
  packages, and each package is a folder like any other.
- **The linked repositories** are untouched. They hold source code and nothing else. They need no `dispat.yaml`, no
  commit convention, and no release job.
- **The pointer** is what a git submodule actually is: a single entry in the control repository recording which commit
  of the linked repository this checkout uses.

## Why it works

Git does not store a submodule's files in the parent repository. It stores one entry, at the submodule's path, holding
a commit hash. Move a linked repository forward by making an ordinary commit in the control repository that changes
exactly one path:

```console
$ git -C services/api/src fetch origin && git -C services/api/src checkout origin/main
$ git add services/api/src
$ git commit -m "feat(api): a health endpoint"
$ git show --name-only --format="%s" HEAD
feat(api): a health endpoint

services/api/src
```

That last line is the whole trick. dispat decides which package a commit touched by looking at the paths it changed.
`services/api/src` sits inside the `api` package folder, so a pointer bump is a change to `api`. It works exactly as if
someone had edited a file there. Everything dispat does with an ordinary change now applies: the version, the
changelog, the tag, the propagation to consumers, and the ordering of the build.

Scopes work the way they do everywhere else. The commit above did not need its `(api)` scope at all. A commit with no
scope is attributed to the packages whose folders it touched, so a bare `fix: pick up the latest api` reaches the same
package. Writing the scope is still worth it because it makes the log readable.

## What you get back

- **One dependency graph across repositories.** Declare an edge from `api` to `sdk` once in the control repository,
  whatever repositories the two live in.
- **Releases in topological order.** The library publishes before the service that consumes it, and unrelated packages
  publish in parallel.
- **Propagation.** Bump every repository that depends on a breaking change in one repository, in the same run.
- **A version, a changelog and a tag per linked repository**, all independent of each other.
- **One pipeline** instead of one per repository.
- **Selective work.** Run [`dispat run --since`](./cli/run.md) and [`dispat if --changed`](./cli/if.md) to narrow a job
  to the packages a pointer bump reached. The pipeline cost follows the change and not the size of the fleet.

Here is the payoff. `api` depends on `sdk`, they live in different repositories, and one commit moved the `sdk`
pointer. The `^` on the type is the [reach directive](./reference/commits.md). It says "and the packages that depend on
this one":

```console
$ git commit -m "feat(sdk)^: a retry policy for every client call"
$ dispat status
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=sdk reason=direct space=libs version="0.0.0 -> 0.1.0"
09:31:07 INF ● changed bump=patch channel=stable dependsOn=["sdk"] dueToProviders=["sdk"] ownCommits=0 package=api reason="propagated from sdk" space=services version="0.0.0 -> 0.0.1"
09:31:07 INF unchanged channel=stable package=web space=services version=0.0.0
09:31:07 INF release plan ready held=0 packages=3 releasing=2
```

Two different repositories are releasing in order from one commit. A third correctly stayed out of it.

## What stays separate, and why that is the point

The versions live in the control repository. The tags are its tags, the changelog files are its files, and its history
is the release history. The linked repositories keep their own history and their own tags untouched.

That separation is the reason to do this rather than a drawback of it. The people working in the linked repositories
open pull requests, review them, and merge them exactly as they do today. They never write a conventional commit, never
edit a `dispat.yaml`, and never wonder why a release did not fire. All of that lives in one repository, usually owned
by one team.

It has a real cost, and you should be clear about it. **The control repository sees one commit per bump, not the
history behind it.** A pointer moving from one commit to another is a single event. The release notes are only as good
as the message on that one commit. [Where the version comes from](#where-the-version-comes-from) is entirely about
writing that message well. This includes a script that generates it from the linked repository's own log.

## Two layouts

There are two places the submodule can sit. The difference matters more than it looks.

### A wrapper folder

Make the package a folder the control repository owns, and place the submodule inside it, conventionally at `src`:

```text
services/api/            the package
  CHANGELOG.md           the control repository's file
  src/                   submodule -> github.com/acme/api
```

This is the layout to prefer. The reason is one git rule: **a parent repository cannot commit files that live inside a
submodule.** Run `git add services/api/src` to stage the pointer and nothing else. Anything dispat writes inside the
submodule is left behind as a modification in the linked repository's working copy. Use a wrapper folder so the
changelog, any per-package config file, and anything else generated during a release land in the wrapper. The release
commit stages them normally:

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

- The wrapper has no manifest of its own. Set [`autoVersion.manifests: "all"`](./configuration/autoversion.md) when you
  want the manifests inside the submodule reconciled. The default, `root`, looks only at the wrapper's own folder and
  finds nothing there.
- State the package name with [`manifestNames`](./configuration/packages.md) if a package needs to be known by the name
  its manifest uses rather than its folder name.

Optionally add `src: src` to the package. That narrows what counts as a change to the submodule pointer alone. Editing
the wrapper's own files does not trigger a release. The pointer still counts because its path is exactly the folder
`src` names.

### The submodule as the package

Mount the submodule directly as the package folder:

```text
services/api/            submodule -> github.com/acme/api
```

This layout is flatter and easier to explain to someone reading the repository for the first time. It costs two things.

**The changelog would be written inside the linked repository.** dispat writes a package's changelog in the package
folder, which is now the submodule. The file is created in the linked repository's working copy, and no commit in the
control repository can pick it up:

```console
$ dispat release
09:31:07 INF summary channel=stable package=api status=published tag=api@0.1.0 took=0.4s version="0.0.0 -> 0.1.0"
$ git status --short
 ? services/api
$ git -C services/api status --short
?? CHANGELOG.md
```

The file exists, nothing tracks it, and the next checkout throws it away. This layout means setting
`changelog: {enabled: false}` and using the GitHub release as the record instead.

**A file in the linked repository can change dispat's behaviour.** dispat reads a `dispat.yaml` from a package's own
folder as a configuration layer. Under this layout that folder belongs to another team, who do not know they own a
dispat config file. dispat reads a file that happens to be named that way, and refuses outright one declaring `spaces`
or `packages`.

### Choosing

|                                            | A wrapper folder                     | The submodule as the package        |
|--------------------------------------------|--------------------------------------|-------------------------------------|
| Where the changelog lands                  | the control repository, committed    | inside the submodule, uncommitted   |
| What the release commit can stage          | everything the release wrote         | the pointer only                    |
| Can a file in the linked repository affect dispat | no                            | yes, its own folder is a config layer |
| Extra folder to explain                    | yes, one per package                 | no                                  |
| Manifest discovery                         | needs `manifests: "all"`             | works by default                    |

Take the wrapper unless you have decided you do not want changelog files at all. The rest of this page uses it and
notes where the flat layout differs.

## The configuration

Write one file for the tree at the top of this page:

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

Nothing here is special to the pattern. It is an ordinary dispat configuration. The only sign that these packages live
in other repositories is `--prefix src`, which points npm at the submodule inside each package folder. Under the flat
layout the commands would be plain `npm ci` and `npm publish` because the package folder is the submodule.

Set `commit.enabled` to write the release commit that carries the changelog entries. Set `commit.push` to send it and
the tags to the control repository's remote. The pointers themselves were committed earlier by whoever moved them.

## Where the version comes from

The pointer bump commit is the only input dispat has. How that commit gets written decides how good this pattern feels.
Choose from three ways, from simplest to most automated. They combine freely.

### Someone moves the pointer

A person, or a bot such as Renovate, opens a pull request in the control repository. This pull request moves one
submodule forward and describes it:

```sh
git -C services/api/src fetch origin
git -C services/api/src checkout origin/main
git add services/api/src
git commit -m "feat(api): a health endpoint"
```

This is worth starting with. It reads well, puts the release decision in front of a reviewer, and needs no tooling at
all. It stops scaling when the fleet is large enough that nobody wants to write those messages.

### The linked repository asks for a release

The linked repository's own CI tells the control repository what happened on merge to its main branch. It sends a
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

A workflow in the control repository receives it, moves the pointer, and commits `feat(api): a health endpoint`. This
is the only option that puts anything in the linked repository. It is a few lines that never mention dispat.

### A sync job writes the commit

Fetch every linked repository on a schedule. The control repository writes the commits itself, deriving each one from
the linked repository's own log. This scales best, and it is the version most fleets end up on.

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
[many units in one commit](./reference/commits.md), each with its own type and scope. A single pointer bump covering
three upstream commits becomes three units. It versions as if all three had been made in the control repository.

Here is the script against a linked repository whose team writes conventional commits sometimes and plain sentences the
rest of the time:

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

Three commits were written by people who have never heard of dispat, in a repository dispat has never been installed
in, and the changelog reads properly.

Run one job per package, or loop over them and let a single commit cover several packages. The units in one commit may
carry different scopes, so one sync commit can release the whole fleet.

### Settings this implies

Configure three settings for a control repository that copies text from elsewhere, whichever way the message gets
written:

```yaml
parser:
  maxDescriptionLength: -1
  propagation:
    depth: 1

nonPackageScopes: [release, sync]
```

- `parser.maxDescriptionLength: -1` turns off the long-description warning. Upstream subject lines were not written to
  dispat's 100 character limit, and there is no point being told so on every release.
- `parser.propagation.depth: 1` makes a bump reach the packages that depend on it without anyone writing the `^`
  directive. This matters more here than in an ordinary monorepo. A generated commit message cannot know how far a
  change should reach, and the person who wrote the upstream commit was never asked. Setting the default in the
  configuration is the honest place for that decision. Use `all` for the whole transitive closure. Leave it at the
  default `0` if you would rather each bump stay put until someone asks for more.
- `nonPackageScopes` should name whatever scope your own bookkeeping commits use. This ensures a `chore(sync):` commit
  is not reported as naming a package that does not exist. `release` is in the default list and must stay.
- Leave `commitErrors` at its default, `warn`. A message assembled from someone else's subject lines will occasionally
  produce something dispat cannot parse. That unit dropping out is a far better outcome than the whole release refusing
  to run.

## Building and publishing

Choose between two models. The first is why most people adopt the pattern, and the second is for fleets with pipelines
they cannot retire.

### The control repository builds

The `flow` in the control repository holds the build and publish commands. They run against the checked-out submodule.
This is the configuration shown above: `npm --prefix src ci`, `npm --prefix src publish`.

Everything in [Examples](./examples/README.md) applies unchanged because from dispat's point of view these are ordinary
packages in ordinary folders. `flow.login` runs once per space, so one registry login covers every package in it.
`DISPAT_NEW_VERSION` and the rest of the [script environment](./reference/environment.md) are there as usual.

Choose this model when the repositories build in similar enough ways that a handful of shared scripts covers them. It
gets the linked repositories down to source code and nothing else.

### The control repository triggers each pipeline

Compute the version and ordering in the control repository and then hand off if a linked repository has a build that
only it knows how to run:

```yaml
scripts:
  publish: |
    gh workflow run release.yml --repo acme/$DISPAT_PACKAGE \
      --field version=$DISPAT_NEW_VERSION
    gh run watch "$(gh run list --repo acme/$DISPAT_PACKAGE \
      --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')" \
      --repo acme/$DISPAT_PACKAGE --exit-status
```

The `publish` script must not return until the publish has actually happened. Everything downstream of it treats a
successful publish as the point of no return. This includes the tag, the changelog, and any consumer waiting on this
package.

You keep the ordering, the versions, and the records. You give up two things. Each linked repository still has a
pipeline to maintain, and something has to sit and poll for the result.

### Making the release visible in the linked repository

The tag and the changelog live in the control repository, which is not where a user of the linked repository looks. Add
three optional settings, each independent of the others:

- **Put the GitHub release on the linked repository.** Set `github.owner` and `github.repo` as per-package overrides:

  ```yaml
  packages:
    api:
      github:
        owner: acme
        repo: api
  ```

  The release, its notes, and its assets then appear on `acme/api`, created with the same token.

- **Tag the linked repository too**, from a publish script:

  ```sh
  git -C src tag -a "v$DISPAT_NEW_VERSION" -m "v$DISPAT_NEW_VERSION"
  git -C src push origin "v$DISPAT_NEW_VERSION"
  ```

  Those tags are the linked repository's own. dispat never reads them, so they cannot confuse the version it computes.

- **Point dispat's own tag at a commit a script produced.** Write `PACKAGE_API=<hash>` to `$DISPAT_OUTPUT` (the package
  name uppercased) if a release script creates a commit somewhere. This moves the tag and the GitHub release onto that
  commit instead of the release commit. See [script outputs](./reference/environment.md#script-outputs).

## Dependencies across repositories

This is the part that no arrangement of separate repositories can give you. Declare edges in the control repository. A
dependency that crosses a repository boundary is written exactly like one that does not:

```yaml
dependencies:
  api: [sdk]
  web: [sdk]
```

You do not have to write them by hand. Run [`dispat compute`](./cli/compute.md) to read the manifests inside the
submodules and find the edges automatically:

```console
$ dispat compute
+ add     api -> sdk (dependencies)  services/api/src/package.json dependencies "@acme/sdk": "^0.0.0"

1 suggestion(s); apply all with --write, choose with --interactive
```

It found a `package.json` two levels inside a wrapper, in a different repository. It derived an edge that dispat now
orders releases by.

### One caveat: writes inside a submodule

[Automatic version reconciliation](./configuration/autoversion.md) updates a consumer's manifest so its range points at
the provider's new version. Under a wrapper layout it needs `manifests: "all"` to see manifests inside the submodule at
all.

Once it does, there is a wrinkle. That manifest lives in a linked repository, and the control repository cannot commit
it. The write still happens in the working tree, so the build uses the right version, but it is not recorded anywhere.

Handle that in two reasonable ways. They are genuinely both fine:

- **Let it be a build-time edit.** Keep `autoVersion` on so the build resolves against the version being released. Let
  each linked repository update its own ranges the way it already does, usually with Renovate or Dependabot. This keeps
  the linked repositories in charge of their own manifests.
- **Push the edit back.** Have a publish script commit the manifest change in the submodule and push it:

  ```sh
  git -C src commit -am "chore: reconcile dependency ranges" && git -C src push origin HEAD:main
  ```

  Choose this when the manifests should be the source of truth and the linked repository has no bot of its own.

## Starting from versions already published

The linked repositories have probably shipped versions already. The control repository has no tags, so its first run
would start everything from scratch. State the baselines instead:

```yaml
initials:
  sdk: 2.3.1
  api: 1.8.0
  web: 0.14.2
```

The next release of `sdk` then continues from `2.3.1`. Run `dispat compute --write` to fill this block in from the
manifests it finds. [Adopting dispat](./examples/adopting.md) walks the whole procedure with transcripts.

## The release job

Configure the one pipeline the whole fleet needs. Check out the submodules, and fetch the complete history, because
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

Two details are specific to this pattern. `submodules: recursive` is required. Without it, the package folders are
empty and nothing builds. `token` has to be a token that can read the linked repositories. The automatic `GITHUB_TOKEN`
is scoped to the repository running the job, so private submodules fail to clone with it.

The [release lock](./reference/releasing/release-lock.md) is a tag on the control repository's own remote. Two runs of
this job cannot overlap, and nothing else in the organisation is affected by it.

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

The identity lines are not optional. A runner has no git user configured, and the script commits. The push lands on
`main`, which starts the release job above.

## What to watch for

- **The control repository sees the pointer, not the history.** Everything a release knows comes from the bump commit
  message. Get that message right and the rest follows.
- **A checkout without submodules produces empty folders.** The pointers are in the history either way, so the plan
  comes out identical and nothing warns. The failure arrives later when a build finds nothing to build. Use
  `submodules: recursive` in every job, including the ones that only run `dispat run` or `dispat if`.
- **A shallow clone breaks the plan.** dispat counts commits since a tag, so the control repository needs its full
  history.
- **`revertOnFail` does not clean inside a submodule.** `git clean` refuses to descend into one. A failed package can
  leave the linked repository's working copy modified. This does not matter on an ephemeral CI runner. Reset the
  submodules at the start of the job on a long-lived runner.
- **A space `path` cannot point outside the repository.** Relative paths that escape the root are refused. This is
  exactly why the link is a submodule. It brings the code inside the checkout rather than reaching outside it.
- **A linked repository that is itself a dispat monorepo does not merge.** Put it in a wrapper where its config file is
  never read. Alternatively, exclude the folder with [`.dispatexclude`](./examples/layout.md). Do not mount it as a
  package and hope the two configurations agree.
- **Relative submodule URLs resolve against the control repository's own remote.** `git submodule add ../api.git` is
  convenient on GitHub, where every repository sits under the same owner. It is confusing anywhere else. Absolute URLs
  are never wrong.

## See also

- [One repository or many](./monorepo.md) for the underlying decision, and for what changes if you ever do merge the
  repositories properly.
- [Adopting dispat](./examples/adopting.md) for deriving the graph and the starting versions from manifests.
- [Keeping configuration beside the code](./examples/layout.md) for splitting a growing control repository into per
  space and per package files.
- [dependencies](./configuration/dependencies.md) and [the compute command](./cli/compute.md) for the edges.
- [Examples](./examples/README.md) for the build and publish commands of your ecosystem, all of which apply unchanged.
