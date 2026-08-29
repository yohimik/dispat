# Release records

A successful release leaves behind the per-package changelog file, the GitHub release, and the optional end-of-run
release commit, besides the tag. `changelog` and `github` are top-level policies. A single package may
[override field by field](./packages.md). You can disable one record for one package, or point one package's releases
at another repository.

## Entry format options (shared by `changelog` and `github`)

| Key                 | Default            | Description                                                                                                                                    |
|---------------------|--------------------|------------------------------------------------------------------------------------------------------------------------------------------------|
| `dateFormat`        | `2006-01-02`       | Go time layout for the entry date.                                                                                                             |
| `breakingTitle`     | `Breaking Changes` | Section title for breaking changes.                                                                                                            |
| `featuresTitle`     | `Features`         | Section title for features.                                                                                                                    |
| `fixesTitle`        | `Fixes`            | Section title for fixes.                                                                                                                       |
| `dependenciesTitle` | `Dependencies`     | Section title for provider updates.                                                                                                            |
| `releaseName`       | none               | What the release is called. On GitHub it replaces the release name (the tag by default). In a changelog it writes a sub-header under the entry's date line. See [Your own words around an entry](#your-own-words-around-an-entry). |
| `header`            | none               | Lines written inside every entry, above the sections.                                                                                          |
| `footer`            | none               | Lines written inside every entry, after the sections.                                                                                          |
| `authors`           | off                | Attributes the entry to the people who wrote it. See [Attributing an entry to its authors](#attributing-an-entry-to-its-authors).               |

`releaseName`, `header` and `footer` are interpolated. See [Variables in record text](#variables-in-record-text).

## Attributing an entry to its authors

`authors` adds the commit authors to a changelog entry and a GitHub release body. It is off by default, so a
repository that says nothing records exactly what it recorded before.

| Key         | Default    | Description                                                                                                                                       |
|-------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `placement` | `off`      | Where the authors appear: `off`, `inline` (a `(by ...)` suffix on each entry line), `section` (one list under its own heading) or `both`.          |
| `format`    | `fullname` | How one author is written: `fullname`, or `username` for the local part of the email address.                                                      |
| `commits`   | `ccme`     | Which commits the section counts: `ccme` for the ones behind the entry's own lines, `all` for every commit in the release window.                  |
| `include`   | none       | Only authors matching one of these globs are listed. An empty list admits everyone.                                                                |
| `exclude`   | none       | Authors matching one of these globs are dropped. Applied after `include`, and it wins.                                                             |
| `title`     | `Authors`  | The heading of the section.                                                                                                                        |

### Where the identity comes from

The identity is git's own: the name and email a commit was authored under, plus everyone its `Co-authored-by`
trailers name. No forge is asked who that is, so the attribution costs no API call, works on a repository that has
never seen GitHub, and cannot change under a record that has already been published.

dispat reads the *author* rather than the committer. A rebase, a cherry-pick or a squash-merge rewrites the committer
and leaves the author alone, so the committer would credit whoever last moved the commit. A `Co-authored-by` trailer
repeating the commit's own author adds nothing, which is the shape a squash-merge of one person's own branch produces.

One person is one entry. Two commits under one address are one author, whatever the name beside each of them says,
and the case of neither an address nor a name is significant.

### `ccme` against `all`

`commits: ccme` counts the commits behind the entry's own lines. The list and the lines above it then describe the
same work, and everything that narrows the lines narrows the list with them: a prerelease is attributed to its own
changeset, a graduation to the whole train it collects, and a reverted entry takes its attribution out with it.

`commits: all` counts every commit in the release window instead, including those whose messages are not release
records at all. This credits the person who wrote a build fix or a dependency bump outside the convention, at the
cost of naming people no line above mentions. It changes the section alone. An inline suffix can only ever name the
authors of a line that exists.

### Filtering with `include` and `exclude`

Both lists hold case-insensitive globs, where `*` matches any run of characters. Each pattern is tried against the
full name, the username and the email address, and matches on any of the three, because an operator writing a filter
is thinking of a person rather than of a field.

`include` runs first and an empty list admits everyone. `exclude` runs afterwards and wins, which is the only order
that lets a wide-open include coexist with a narrow refusal:

```yaml
changelog:
  authors:
    placement: both
    include: ["*"]
    exclude: ["*[bot]*"]
```

An entry whose authors are all filtered away is written without the section rather than with an empty one.

### The section's place in an entry

The section follows the sections it attributes and precedes both the `### Release` details a GitHub release carries
and the entry's `footer`. The footer stays last on purpose: [self-update](../reference/self-update.md) reads release
notes by cutting at the `---` a release footer conventionally opens with, so a block written after it would be cut
away with the footer.

Authors are listed in the order the release collected its commits, newest first, rather than by the size of the
change each of them happened to make.

## `changelog`

| Key       | Default        | Description                                   |
|-----------|----------------|-----------------------------------------------|
| `enabled`    | `true`         | Write a changelog file per published package.                                          |
| `channels`   | every release  | Which releases get an entry. See [Choosing the channels that record](#choosing-the-channels-that-record). |
| `file`       | `CHANGELOG.md` | File name inside the package folder.                                                   |
| `fileTitle`  | `# Changelog`  | Heads the file, above every entry. Takes the [line shapes](#your-own-words-around-an-entry) `header` and `footer` take, so it can be several lines and can differ per package. |
| *format*     |                | All entry format options above.                                                        |

dispat prepends new entries below the title, newest first.

**What an entry contains: the release-notes windowing.** An entry holds the release's notes grouped by bump (breaking
changes, features, fixes) plus the provider-updates section. On the stable channel that is every pending commit since
the package's last release. On a prerelease train each prerelease's entry contains **only its own changeset**. These
are the commits the train's earlier prereleases have not already published, so `beta.1` does not repeat `beta.0`'s
notes. The **graduation** then collects the whole train (everything since the last stable tag) into its one entry,
which is what readers of the stable line actually see. That includes the provider-updates section. A provider that
moved while the train ran appears in the graduation's entry with its movement over the whole train. This happens even
when a prerelease entry already documented it piecewise. The version is still computed over the whole train either way,
because a breaking change shipped in `beta.0` keeps the graduation at the next major, and only the notes narrow. The
same windowing drives the [GitHub release body](#github) and the
[`DISPAT_BREAKING_CHANGES` / `DISPAT_FEATURES` / `DISPAT_FIXES` variables](../reference/environment.md#release-notes-data).
Run [`dispat preview`](../cli/preview.md) to see exactly what the next entry would contain.

The dependencies section lists the providers whose movement this release picks up because they forced it or released
beside it. A range silently reconciled to a provider released in an *earlier* run is deliberately not listed. That is
[the auto-version pickup](./autoversion.md#picking-up-providers-released-without-you), visible in the manifest diff and
as `W197`, and documented by the provider's own release.

**An entry is never empty.** A release can be admitted to the plan with nothing for its notes to group. This happens
for a version set by an exact `Release-As`, a channel transition carrying no new work, pending work its own reverts
cancel out, or a [shared-versioning ride](../reference/releasing/versioning.md#the-changelog-entry-a-passenger-gets).
Each of these renders a single line naming the cause instead of an empty body, in the changelog entry and the GitHub
release body alike:

```markdown
No changes: a version set by Release-As.
No changes: a channel transition, beta -> stable.
No changes: the pending work and its reverts cancel out.
No changes: a version bump to keep the versioning group on one version.
```

A changelog write is idempotent. dispat leaves a file untouched if it already carries the entry for the planned tag (a
line starting `## <tag> (`) and reports the skip as `W226`. That makes the [`dispat changelog`](../cli/changelog.md)
step command safe to run before the release, because the entry it writes lands inside the release commit, and the
release stage's own recorder finds it and skips.

## `github`

| Key        | Default                   | Description                                                                                                                                                                                                                                                            |
|------------|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | `true`                    | Create a GitHub release per published package that exported [`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs).                                                                                                                                                |
| `allPackages`  | `false`                  | Create a release for every published package, even when no script exported `DISPAT_EXPORT_GITHUB`. The export then only adds assets. Default: the export is the per-package opt-in. |
| `draft`    | `false`                   | Create every release as a draft, for a person to publish after reading it. A draft carries no tag ref until it is published, so nothing that resolves a release by its tag sees it meanwhile: [`dispat install`](../cli/install.md), [self-update](../cli/self-update.md) and the alias-tag chain all skip it. A re-run finds the draft through the repository's release listing and skips it (`W224`), so a flow converges on one draft rather than a stack of them. Turning this off again leaves any draft already created where it is: dispat creates the published release beside it, and the stale draft is yours to delete. |
| `channels` | every release             | Which releases get a GitHub release. See [Choosing the channels that record](#choosing-the-channels-that-record). A created release is flagged as a prerelease on GitHub whenever the version is one, whatever this says.             |
| `owner`    | from `$GITHUB_REPOSITORY` | Repository owner.                                                                                                                                                                                                                                                      |
| `repo`     | from `$GITHUB_REPOSITORY` | Repository name.                                                                                                                                                                                                                                                       |
| `apiUrl`   | `https://api.github.com`  | REST endpoint. Set this for GitHub Enterprise.                                                                                                                                                                                                                              |
| `tokenEnv` | `GITHUB_TOKEN`            | Name of the environment variable holding the API token.                                                                                                                                                                                                                |
| *format*   |                           | All entry format options above. The release body contains the sections, with `header` and `footer` around them. The `## pkg@version (date)` header line used in changelog files is omitted, since GitHub shows the release's name and its own date, so `dateFormat` has no effect here. `releaseName` sets the release's name, which otherwise is the tag. |

The release is **opt-in per package and per run**. dispat creates it exactly when one of the package's scripts exported
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs). A published package without the export is skipped
with an info-level notice, so a script decides at run time which packages get a GitHub release.

The release is named after the tag (`pkg@1.3.0`). Its body is the rendered changelog sections under the same
[release-notes windowing](#changelog). A prerelease's release documents only its own changeset, and a graduation
documents the whole train.

dispat handles credentials in two steps. When `enabled` but no repository or token can be resolved at runtime, GitHub
releases are skipped with a warning rather than failing the run. A configuration that *does* resolve is **verified
against the API before any release work starts** (`GET /repos/{owner}/{repo}`). This verification happens whether the
release commit is enabled or not, so misconfigured credentials fail the run before anything is built.

**Which commit the release points at.** A GitHub release hangs off its tag, and GitHub resolves the tag by name on the
*remote*. If the tag already exists there when the release is created, the release attaches to exactly the commit it
marks. If not, GitHub creates the tag ref at the default branch head. Per mode:

| Mode                                                         | Tag ref on GitHub                                   | Release body                                             |
|--------------------------------------------------------------|-----------------------------------------------------|----------------------------------------------------------|
| [`commit`](#commit) disabled (default)                       | Default branch head, until CI pushes the local tag  | The notes alone                                          |
| `commit` enabled, `push` off                                 | Default branch head, until you push                 | Documents the release commit SHA and tag (`### Release`) |
| `commit` enabled, `push` on                                  | Pinned to the release commit via `target_commitish` | Documents the release commit SHA and tag                 |
| [`PACKAGE_<KEY>`](../reference/environment.md#script-outputs) exported | Pinned to the exported hash via `target_commitish`  | Documents the exported hash                              |

In the usual CI setup with `commit` disabled (a job on the default branch, tags pushed right after the run) the branch
head and the released commit coincide. They can differ if the run released another branch or the push never happened.
With `commit` enabled, releases move to the end of the run and, under `push`, are created after the push, when the SHA
exists on the remote.

A draft is the one release that points at nothing yet. GitHub stores the `target_commitish` on the draft and creates
the tag ref when the release is published, so the table above describes where the tag lands at that moment rather than
when dispat created the release. The commit a pushed release commit names is still the commit the published draft
attaches to.

The export's value names the **release assets**. This is a whitespace-separated list of absolute paths to existing
files. dispat uploads each one (named after the file, `application/octet-stream`) right after the release is created,
even in `commit` mode where the release itself moves to the finalize phase. An invalid entry (a relative path, a
missing file, a directory) is skipped with a warning while the release and the remaining files go through. A failed
upload of a valid file still fails the package like any other recording failure.

**Creating a release twice.** A release the repository already carries for the planned tag is a skip (`W224`), not the
API's duplicate-tag rejection. Run a release again after a later stage failed, and the
[`dispat github`](../cli/github.md) step command converges instead of failing. A draft is recognised the same way,
through the repository's release listing rather than the tag it names, which dispat reads only when `draft` is on.

## Your own words around an entry

Everything dispat writes about a release is the notes it read out of your commits. `header`, `footer`, `releaseName`
and `fileTitle` are where you add your own text. You can add an install line, a link back to the package, a horizontal
rule between entries, or a name for the release that reads better than a tag.

Here is where each one lands in a changelog file:

```markdown
# Changelog                      <- fileTitle, once at the top of the file

## core@1.2.0 (2026-08-11)       <- the entry, one per release

### Winter release              <- releaseName, when you set one

Built from the acme monorepo.   <- header

### Features

- add streaming

[Full changelog](...)           <- footer

## core@1.1.0 (2026-07-02)      <- the previous entry, with its own header and footer
```

A GitHub release has no file to head, so `fileTitle` does not apply there. `releaseName` becomes the release's own name
rather than a line in its body. The body reads: `header`, the sections, the `### Release` block when
[commit mode](#commit) is on, then `footer`.

`header` and `footer` are written **inside every entry**, not once per file. Each entry keeps the text it was written
with, so changing a footer today does not rewrite what last month's release said.

### Writing the lines

A list holds three kinds of element, and you can mix them freely:

```json title="dispat.json"
{
  "changelog": {
    "footer": [
      "Thanks for reading.",
      ["", "Questions? Open an issue."],
      { "line": "Published from the acme monorepo.", "space": "libs" }
    ]
  }
}
```

* A **string** is one line. An empty string is a blank line, which spaces a block out.
* An **array of strings** is several lines, written one after another.
* An **object** is one or more lines plus the filters that decide which packages get them. `line` holds the text, as a
  string or an array of strings.

Each block is already separated from what surrounds it by one blank line. You only need an empty string where you want
more space than that.

### Choosing which packages a line reaches

Three optional filters narrow an object to part of the workspace:

| Filter    | Matches against                                                                                          |
|-----------|----------------------------------------------------------------------------------------------------------|
| `package` | The package name.                                                                                        |
| `space`   | The name of the [space](./spaces.md) the package belongs to.                                              |
| `group`   | The package's [versioning group](./spaces.md#versioning-groups). A package that shares its version with nothing belongs to no group, so a `group` filter never selects it. |

Each takes one name or an array of names. They match the same way the `--package`, `--space` and `--group` flags do:
case-insensitively, with `*` standing for any run of characters.

### Choosing which releases a line reaches

A fourth filter, `channels`, asks about the release rather than the package. Use it to put a line into the prereleases
and keep it out of the stable entry, or the other way around:

```json title="dispat.json"
{
  "changelog": {
    "header": [
      { "line": "This is a test build. Do not depend on it.", "channels": "*" }
    ],
    "footer": [
      { "line": "Supported until the next major.", "channels": "stable" }
    ]
  }
}
```

It takes one value or an array of them, matched case-insensitively:

| Value      | Reaches                                                                                             |
|------------|-------------------------------------------------------------------------------------------------------|
| `stable`   | Stable releases.                                                                                    |
| `*`        | Every prerelease, whatever its channel is called, and no stable release.                            |
| a name     | The named [channel](../concepts.md#prereleases-and-channels) alone, such as `beta` or `rc`.         |

A line with no `channels` reaches every release. `channels` combines with the package filters the same way the others
do. A line naming a package and a channel is written where both hold.

It does not apply to `fileTitle`, which is written once at the top of the file and matched against on the next release.
A title that changed with the channel would be written again every time the channel moved. dispat refuses it in the
config rather than producing a file with several titles.

```json title="dispat.json"
{
  "changelog": {
    "footer": [
      { "line": "Internal package, no support promised.", "package": ["@acme/internal-*", "scratch"] },
      { "line": "Released together.", "group": "core-libs" }
    ]
  }
}
```

Several values under one filter mean *any of them*. Several filters together mean *all of them*. A line with both
`space` and `group` reaches only packages that match each. A line with no filters at all reaches every package, which
is what a bare string is.

### Overriding a list

A [package override](./packages.md) that sets a list states that package's whole list. It does not add to the one it
inherited. The filters are how a workspace-wide list reaches some packages and not others, so there is one place to
look for what a package writes.

```json title="dispat.json"
{
  "changelog": { "footer": ["shared"] },
  "packages": {
    "core": { "changelog": { "footer": ["core only"] } }
  }
}
```

Core writes `core only`. Every other package writes `shared`.

There are no command-line flags for `header` and `footer`, because a filtered list of lines is not a flag-shaped thing.
`releaseName` and `fileTitle` do have flags on the step commands, `--release-name` and `--file-title`. Each flag
replaces the configured value for that one invocation.

Every key of `authors` has a flag on both [`dispat changelog`](../cli/changelog.md) and
[`dispat github`](../cli/github.md): `--authors`, `--authors-format`, `--authors-commits`, `--authors-include`,
`--authors-exclude` and `--authors-title`. Each one replaces the configured value for that invocation, field by
field, with the two lists replacing whole. A flag naming a value the setting does not admit is refused before
anything is planned, in the words the configuration file would have been refused in.

## Variables in record text

`releaseName`, `header`, `footer` and `fileTitle` expand `$VAR` and `${VAR}`. One configured line can name the package
and the version it belongs to:

```json title="dispat.json"
{
  "github": {
    "releaseName": "${DISPAT_PACKAGE} ${DISPAT_VERSION}",
    "footer": [
      "",
      "Changelog: https://github.com/acme/monorepo/blob/${DISPAT_TAG}/packages/${DISPAT_PACKAGE}/CHANGELOG.md"
    ]
  }
}
```

Three sources answer, in this order:

1. The releasing package's own [`DISPAT_*` variables](../reference/environment.md): its name, version, channel, tag and
   the rest. These are the same variables your scripts receive, so a footer and a publish script name the release the
   same way.
2. Anything the package's scripts [exported](../reference/environment.md#script-outputs), as `DISPAT_OUTPUT_<NAME>`.
   This is how a footer links an artifact the run itself produced.
3. The process environment.

A name that none of the three defines expands to nothing, the way a shell expands an unset variable. Half-written
`${...}` in a published release reads worse than the gap it would have filled.

Two things worth knowing:

* **Everything in the environment expands, secrets included.** `${GITHUB_TOKEN}` in a footer would publish your token.
  Only name variables you mean to show.
* **A `fileTitle` must not contain anything that changes between releases.** The title is written once and matched
  against on the next release so it is not duplicated. A title holding `${DISPAT_TAG}` looks different every time, the
  match fails, and the old title stays behind in the file. Package and space names are safe; versions and tags are not.

## Choosing the channels that record

Both records write on every channel by default. A `1.3.0-beta.0` earns a changelog entry and a GitHub release just like
a stable version does. `channels` names the ones that do, on either object. It is one of the fields a package may
[override](./packages.md), so the choice can be per package.

The most common setting keeps the changelog a history of the stable line:

```json title="dispat.json"
{
  "changelog": { "channels": ["stable"] },
  "github": { "channels": ["stable"] }
}
```

It takes the same values a line's [`channels`](#choosing-which-releases-a-line-reaches) does: `stable`, `*` for every
prerelease, or a channel name such as `beta`. Naming nothing at all is the default, every release. Set
`["stable", "beta"]` to record the stable line and the betas while an rc leaves nothing behind. Set `["*"]` for the
opposite of the example above.

Nothing else about a held-back release changes. It is still planned, still built, still published and still tagged.
Only the records are held. The betas of a version leave nothing behind, and the **graduation** to stable writes the one
entry and creates the one release covering the whole train, under the [release-notes windowing](#changelog) that
already collects it. Each skipped record is reported at info level with the channel it was on, so a run says what it
held back and why.

Because a list states the whole restriction rather than adding to an inherited one, a package under a restricted
workspace opts back in by naming both halves:

```json title="dispat.json"
{
  "changelog": { "channels": ["stable"] },
  "packages": {
    "core": { "changelog": { "channels": ["stable", "*"] } }
  }
}
```

Every package records its stable releases only; `core` records everything.

On GitHub this decides which releases are **created**. Whether a created release is marked as a prerelease is decided
by the version itself and is not configurable. A `1.3.0-beta.0` that gets a release always gets a prerelease one.

## `commit`

| Key             | Default                  | Description                                                                                                                                                                                                                                                                                                                                                                                       |
|-----------------|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`       | `false`                  | Create one release commit at the end of a successful run.                                                                                                                                                                                                                                                                                                                                         |
| `messageFormat` | `chore(release): {tags}` | Template. `{tags}` and `{packages}` become comma-separated lists.                                                                                                                                                                                                                                                                                                                                 |
| `push`          | `false`                  | Push the release commit and tags. Only applies when `enabled` is true.                                                                                                                                                                                                                                     |
| `force`         | `true`                   | Write tags the repository or the remote already carries, instead of leaving them alone. The branch is never force pushed, and a release tag found at a different commit is still left as it is. See [Force](#force) below. `dispat commit --no-force` turns it off for one invocation.                                                                     |
| `remote`        | `origin`                 | Remote to push to.                                                                                                                                                                                                                                                                                                                                                                                |
| `name`, `email` | unset                    | The git identity every commit and annotated tag dispat creates is authored under, so a CI run needs no `git config` step. Unset values fall back to git's own configuration.  |
| `verify`        | `true`                   | Verify remote access (`git ls-remote`) before any release work when `push` is enabled. Set `false` to skip the check, e.g. for a remote that rejects ls-remote but accepts pushes.                                                                                                                                                                                                                |
| `include`       | none                     | Extra repo-relative paths the release commit stages on top of the published packages' folders. This includes the shared artifacts a version stage or an [`autoVersion.syncLock`](./autoversion.md) regenerates outside every package folder, a workspace-level lock file (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`) first among them. Paths must stay inside the repository. One that does not exist at commit time is simply not staged. |

**Disabled** (the default), dispat creates no commit at all. Each package's annotated tag is created right after its
publish succeeds and points at the commit the run released from. This is `HEAD` of the checkout, which stays put for
the whole run since nothing is committed. Whatever the release changed on disk (changelog files, version-script
manifest edits) is left in the worktree. Pushing the tags is left to CI (`git push origin --tags`).

When **enabled**, the run instead finishes with a *finalize phase*. Every published package's folder is staged and
committed in a single commit. The release tags are created **on that commit** rather than during each publish.

The commit carries changelog files, version-script manifest changes and any `include` paths that exist. Add build
outputs to your `.gitignore` or they get committed too. If nothing changed on disk, because changelogs are disabled for
instance, no empty commit is created but the tags are still placed.

One package is an exception. A package whose scripts exported
[`PACKAGE_<KEY>`](../reference/environment.md#script-outputs) has its tag excluded from the release commit and created
at the exported commit hash instead.

GitHub releases move to the end of the run and document the release commit in their body. What the GitHub side does in
each mode is described under [`github`](#github).

Pushing pushes the branch first and the run's tags after it. It requires a checked-out branch (not a detached HEAD; use
`actions/checkout` with a `ref`). When `push` is enabled, remote access is **verified before any release work starts**
(`git ls-remote`, switched off by `verify: false`), so a misconfigured remote fails the run before anything is built.
An enabled GitHub configuration is likewise verified up front, push or not (see [`github`](#github)). A failure during
the finalize phase itself (commit, tag, push, GitHub release) exits 1 with everything else in the phase still done, and
already-published registry artifacts stay published. See
[After the point of no return](../internals/architecture.md#after-the-point-of-no-return).

### Force

`force` (default `true`) decides what happens when a tag is already there.

With it on, a tag the repository already carries is rewritten (`git tag -f`). One the remote already carries is
replaced (`git push --force` on that one ref, reported with a warning naming it). Without it, both are left as they are
and reported as skipped. This older behaviour means a re-run after a partially pushed release converges instead of
dying on "tag already exists".

The default is on for two reasons. A tag the remote already has is otherwise skipped on every future run, so a *moving*
tag could never move. And a tag appearing between dispat's check and its push would otherwise reject the whole push at
the very end of a release, after every artefact is already out.

Three things `force` deliberately does not do:

- **The branch is never force pushed**, under either setting. A rejected branch push means someone else pushed while
  the run was working. The answer to that is to look, not to overwrite their commits.
- **A release tag found at a different commit is left alone.** That is reported as `E221` and the tag is not written at
  all, because it is a record some earlier run made. A tag moved here would then be force pushed over the copy on the
  remote, turning one local mistake into everyone's. Force means "do not fail because the ref exists", not "overwrite
  whatever is there".
- **The [release lock](../reference/releasing/release-lock.md) is never forced.** Its whole purpose is to fail when the
  name is taken, since a run that took the lock by overwriting somebody else's would be releasing beside them.

The one case force does change for release tags is a tag on a commit the current branch cannot reach. dispat's baseline
query cannot see it, so nothing planned around it, and the write simply succeeds.
