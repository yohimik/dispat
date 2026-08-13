# Release records

What a successful release leaves behind besides the tag: the per-package changelog file, the GitHub release, and the
optional end-of-run release commit. `changelog` and `github` are top-level policies a single package may
[override field by field](./packages.md): disable one record for one package, or point one package's releases at another
repository.

## Entry format options (shared by `changelog` and `github`)

| Key                 | Default            | Description                                                                                                                                    |
|---------------------|--------------------|------------------------------------------------------------------------------------------------------------------------------------------------|
| `dateFormat`        | `2006-01-02`       | Go time layout for the entry date.                                                                                                             |
| `breakingTitle`     | `Breaking Changes` | Section title for breaking changes.                                                                                                            |
| `featuresTitle`     | `Features`         | Section title for features.                                                                                                                    |
| `fixesTitle`        | `Fixes`            | Section title for fixes.                                                                                                                       |
| `dependenciesTitle` | `Dependencies`     | Section title for provider updates.                                                                                                            |
| `releaseName`       | none               | What the release is called. On GitHub it replaces the release name (the tag by default); in a changelog it writes a sub-header under the entry's date line. See [Your own words around an entry](#your-own-words-around-an-entry). |
| `header`            | none               | Lines written inside every entry, above the sections.                                                                                          |
| `footer`            | none               | Lines written inside every entry, after the sections.                                                                                          |

`releaseName`, `header` and `footer` are interpolated: see
[Variables in record text](#variables-in-record-text).

## `changelog`

| Key       | Default        | Description                                   |
|-----------|----------------|-----------------------------------------------|
| `enabled`    | `true`         | Write a changelog file per published package.                                          |
| `prerelease` | `true`         | Write an entry for a prerelease version too; see [Holding prereleases back](#holding-prereleases-back). |
| `file`       | `CHANGELOG.md` | File name inside the package folder.                                                   |
| `fileTitle`  | `# Changelog`  | Heads the file, above every entry. Takes the [line shapes](#your-own-words-around-an-entry) `header` and `footer` take, so it can be several lines and can differ per package. |
| *format*     |                | All entry format options above.                                                        |

New entries are prepended below the title, newest first.

**What an entry contains: the release-notes windowing.** An entry holds the release's notes grouped by bump (breaking
changes, features, fixes) plus the provider-updates section. On the stable channel that is every pending commit since
the package's last release. On a prerelease train each prerelease's entry contains **only its own changeset**, the
commits the train's earlier prereleases have not already published, so `beta.1` does not repeat `beta.0`'s notes; the
**graduation** then collects the whole train (everything since the last stable tag) into its one entry, which is what
readers of the stable line actually see. The version is still computed over the whole train either way (a breaking
change shipped in `beta.0` keeps the graduation at the next major); only the notes narrow. The same windowing drives
the [GitHub release body](#github) and the
[`DISPAT_BREAKING_CHANGES` / `DISPAT_FEATURES` / `DISPAT_FIXES` variables](../reference/environment.md#release-notes-data), and
[`dispat preview`](../cli/preview.md) shows exactly what the next entry would contain.

A changelog write is idempotent: a file that already carries the entry for the planned tag (a line starting
`## <tag> (`) is left untouched and the skip is reported as `W226`. That is what makes the
[`dispat changelog`](../cli/changelog.md) step command safe to run before the release: the entry it writes
lands inside the release commit, and the release stage's own recorder finds it and skips.

## `github`

| Key        | Default                   | Description                                                                                                                                                                                                                                                            |
|------------|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | `true`                    | Create a GitHub release per published package that exported [`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs).                                                                                                                                                |
| `allPackages`  | `false`                  | Create a release for every published package, even when no script exported `DISPAT_EXPORT_GITHUB`; the export then only adds assets. Default: the export is the per-package opt-in. |
| `prerelease` | `true`                    | Create a release for a prerelease version too (flagged as a prerelease on GitHub); see [Holding prereleases back](#holding-prereleases-back).                                                                                        |
| `owner`    | from `$GITHUB_REPOSITORY` | Repository owner.                                                                                                                                                                                                                                                      |
| `repo`     | from `$GITHUB_REPOSITORY` | Repository name.                                                                                                                                                                                                                                                       |
| `apiUrl`   | `https://api.github.com`  | REST endpoint; set for GitHub Enterprise.                                                                                                                                                                                                                              |
| `tokenEnv` | `GITHUB_TOKEN`            | Name of the environment variable holding the API token.                                                                                                                                                                                                                |
| *format*   |                           | All entry format options above. The release body contains the sections, with `header` and `footer` around them; the `## pkg@version (date)` header line used in changelog files is omitted, since GitHub shows the release's name and its own date, so `dateFormat` has no effect here. `releaseName` sets the release's name, which otherwise is the tag. |

The release is **opt-in per package and per run**: it is created exactly when one of the package's scripts exported
[`DISPAT_EXPORT_GITHUB`](../reference/environment.md#script-outputs); a published package without the export is skipped (with an
info-level notice), so a script decides at run time which packages get a GitHub release. The release is named after the
tag (`pkg@1.3.0`); its body is the rendered changelog sections, under the same
[release-notes windowing](#changelog): a prerelease's release documents only its own changeset, a graduation the whole
train. When `enabled` but no repository or token can be resolved at runtime, GitHub releases are skipped with a warning
instead of failing the run. A configuration that *does* resolve is **verified against the API before any release work
starts** (`GET /repos/{owner}/{repo}`), with the release commit enabled or disabled alike, so misconfigured credentials
fail the run before anything is built.

**Which commit the release points at.** A GitHub release hangs off its tag, and GitHub resolves the tag by name on the
*remote*: if the tag already exists there when the release is created, the release attaches to exactly the commit it
marks; if not, GitHub creates the tag ref at the default branch head. Per mode:

| Mode                                                         | Tag ref on GitHub                                   | Release body                                             |
|--------------------------------------------------------------|-----------------------------------------------------|----------------------------------------------------------|
| [`commit`](#commit) disabled (default)                       | Default branch head, until CI pushes the local tag  | The notes alone                                          |
| `commit` enabled, `push` off                                 | Default branch head, until you push                 | Documents the release commit SHA and tag (`### Release`) |
| `commit` enabled, `push` on                                  | Pinned to the release commit via `target_commitish` | Documents the release commit SHA and tag                 |
| [`PACKAGE_<KEY>`](../reference/environment.md#script-outputs) exported | Pinned to the exported hash via `target_commitish`  | Documents the exported hash                              |

In the usual CI setup with `commit` disabled (a job on the default branch, tags pushed right after the run) the branch
head and the released commit coincide; they can differ if the run released another branch or the push never happened.
With `commit` enabled, releases move to the end of the run and, under `push`, are created after the push, when the SHA
exists on the remote.

The export's value names the **release assets**: a whitespace-separated list of absolute paths to existing files, each
uploaded (named after the file, `application/octet-stream`) right after the release is created, in `commit`
mode too, where the release itself moves to the finalize phase. An invalid entry (a relative path, a missing file, a
directory) is skipped with a warning while the release and the remaining files go through; a failed upload of a valid
file still fails the package like any other recording failure.

**Creating a release twice.** A release the repository already carries for the planned tag is a skip (`W224`), not the
API's duplicate-tag rejection, so a run repeated after a later stage failed, and the
[`dispat github`](../cli/github.md) step command run twice, converge instead of failing.

## Your own words around an entry

Everything dispat writes about a release is the notes it read out of your commits. `header`, `footer`, `releaseName`
and `fileTitle` are where you add your own text: an install line, a link back to the package, a horizontal rule between
entries, a name for the release that reads better than a tag.

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

A GitHub release has no file to head, so `fileTitle` does not apply there, and `releaseName` becomes the release's own
name rather than a line in its body. The body reads: `header`, the sections, the `### Release` block when
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

* A **string** is one line. An empty string is a blank line, which is how you space a block out.
* An **array of strings** is several lines, written one after another.
* An **object** is one or more lines plus the filters that decide which packages get them. `line` holds the text, as a
  string or an array of strings.

Each block is already separated from what surrounds it by one blank line, so you only need an empty string where you
want more space than that.

### Choosing which packages a line reaches

Three optional filters narrow an object to part of the workspace:

| Filter    | Matches against                                                                                          |
|-----------|----------------------------------------------------------------------------------------------------------|
| `package` | The package name.                                                                                        |
| `space`   | The name of the [space](./spaces.md) the package belongs to.                                              |
| `group`   | The package's [versioning group](./spaces.md#versioning-groups). A package that shares its version with nothing belongs to no group, so a `group` filter never selects it. |

Each takes one name or an array of names, and matches the same way the `--package`, `--space` and `--group` flags do:
case-insensitively, with `*` standing for any run of characters.

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

Several values under one filter mean *any of them*. Several filters together mean *all of them*: a line with both
`space` and `group` reaches only packages that match each. A line with no filters at all reaches every package, which
is what a bare string is.

### Overriding a list

A [package override](./packages.md) that sets a list states that package's whole list; it does not add to the one it
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
`releaseName` and `fileTitle` do have flags on the step commands, `--release-name` and `--file-title`, each replacing
the configured value for that one invocation.

## Variables in record text

`releaseName`, `header`, `footer` and `fileTitle` expand `$VAR` and `${VAR}`, so one configured line can name the
package and the version it belongs to:

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

1. The releasing package's own [`DISPAT_*` variables](../reference/environment.md): its name, version, channel, tag and the rest.
   These are the same variables your scripts receive, so a footer and a publish script name the release the same way.
2. Anything the package's scripts [exported](../reference/environment.md#script-outputs), as `DISPAT_OUTPUT_<NAME>`. This is how
   a footer links an artifact the run itself produced.
3. The process environment.

A name that none of the three defines expands to nothing, the way a shell expands an unset variable. Half-written
`${...}` in a published release reads worse than the gap it would have filled.

Two things worth knowing:

* **Everything in the environment expands, secrets included.** `${GITHUB_TOKEN}` in a footer would publish your token.
  Only name variables you mean to show.
* **A `fileTitle` must not contain anything that changes between releases.** The title is written once and matched
  against on the next release so it is not duplicated. A title holding `${DISPAT_TAG}` looks different every time, the
  match fails, and the old title stays behind in the file. Package and space names are safe; versions and tags are not.

## Holding prereleases back

Both records write on every channel by default: a `1.3.0-beta.0` earns a changelog entry and a GitHub release just
like a stable version does. `prerelease: false` on either object stops that for the version it applies to, and it is
one of the fields a package may [override](./packages.md), so the choice can be per package.

```json title="dispat.json"
{
  "changelog": { "prerelease": false },
  "github": { "prerelease": false }
}
```

Nothing else about the prerelease changes. It is still planned, still built, still published and still tagged; the
flow does not notice. Only the records are held: the betas of a version leave nothing behind, and the
**graduation** to stable writes the one entry and creates the one release covering the whole train, under the
[release-notes windowing](#changelog) that already collects it. A repository whose changelog is meant to read as the
history of its stable releases, with the beta traffic staying in the tags, wants exactly this.

## `commit`

| Key             | Default                  | Description                                                                                                                                                                                                                                                                                                                                                                                       |
|-----------------|--------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`       | `false`                  | Create one release commit at the end of a successful run.                                                                                                                                                                                                                                                                                                                                         |
| `messageFormat` | `chore(release): {tags}` | Template; `{tags}` and `{packages}` become comma-separated lists.                                                                                                                                                                                                                                                                                                                                 |
| `push`          | `false`                  | Push the release commit and tags. Only applies when `enabled` is true.                                                                                                                                                                                                                                     |
| `force`         | `true`                   | Write tags the repository or the remote already carries, instead of leaving them alone. The branch is never force pushed, and a release tag found at a different commit is still left as it is; see [Force](#force) below. `dispat commit --no-force` turns it off for one invocation.                                                                     |
| `remote`        | `origin`                 | Remote to push to.                                                                                                                                                                                                                                                                                                                                                                                |
| `name`, `email` | unset                    | The git identity every commit and annotated tag dispat creates is authored under, so a CI run needs no `git config` step. Unset values fall back to git's own configuration.  |
| `verify`        | `true`                   | Verify remote access (`git ls-remote`) before any release work when `push` is enabled. Set `false` to skip the check, e.g. for a remote that rejects ls-remote but accepts pushes.                                                                                                                                                                                                                |
| `include`       | none                     | Extra repo-relative paths the release commit stages on top of the published packages' folders: the shared artifacts a version stage or an [`autoVersion.syncLock`](./spaces.md#autoversion) regenerates outside every package folder, a workspace-level lock file (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`) first among them. Paths must stay inside the repository; one that does not exist at commit time is simply not staged. |

**Disabled** (the default), dispat creates no commit at all. Each package's annotated tag is created right after its
publish succeeds and points at the commit the run released from: `HEAD` of the checkout, which stays put for the whole
run since nothing is committed. Whatever the release changed on disk (changelog files, version-script manifest edits) is
left in the worktree, and pushing the tags is left to CI (`git push origin --tags`).

When **enabled**, the run instead finishes with a *finalize phase*. All published packages' folders are staged and
committed in a single commit, and the release tags are created **on that commit** instead of during each publish. The
commit carries changelog files, version-script manifest changes, and any `include` paths that exist. Add build outputs
to your `.gitignore` or they get committed too. A package whose scripts exported [`PACKAGE_<KEY>`](../reference/environment.md#script-outputs) is
the exception: its tag is excluded from the release commit and created at the exported commit hash instead. If nothing
changed on disk (e.g. changelogs disabled), no empty commit is created but tags are still placed. GitHub releases move
to the end of the run and document the release commit in their body; what the GitHub side does in each mode is described
under [`github`](#github).

Pushing pushes the branch first and the run's tags after it. It
requires a checked-out branch (not a detached HEAD; use `actions/checkout` with a `ref`). When `push` is enabled, remote
access is **verified before any release work starts** (`git ls-remote`, switched off by
`verify: false`), so a misconfigured remote fails the run before anything is built; an enabled GitHub configuration is
likewise verified up front, push or not (see [
`github`](#github)). A failure during the finalize phase itself (commit, tag, push, GitHub release) exits 1 with
everything else in the phase still done, and already-published registry artifacts stay published; see
[After the point of no return](../internals/architecture.md#after-the-point-of-no-return).

### Force

`force` (default `true`) decides what happens when a tag is already there.

With it on, a tag the repository already carries is rewritten (`git tag -f`), and one the remote already carries is
replaced (`git push --force` on that one ref, reported with a warning naming it). Without it, both are left as they are
and reported as skipped, which is the older behaviour: a re-run after a partially pushed release converges instead of
dying on "tag already exists".

The default is on for two reasons. A tag the remote already has is otherwise skipped on every future run, so a *moving*
tag could never move. And a tag appearing between dispat's check and its push would otherwise reject the whole push at
the very end of a release, after every artefact is already out.

Two things `force` deliberately does not do:

- **The branch is never force pushed**, under either setting. A rejected branch push means someone else pushed while
  the run was working, and the answer to that is to look, not to overwrite their commits.
- **A release tag found at a different commit is left alone.** That is reported as `E211` and the tag is not written at
  all, because it is a record some earlier run made, and a tag moved here would then be force pushed over the copy on
  the remote, turning one local mistake into everyone's. Force means "do not fail because the ref exists", not
  "overwrite whatever is there".
- **The [release lock](../cookbook/releasing/release-lock.md) is never forced.** Its whole purpose is to fail when the name is taken, since
  a run that took the lock by overwriting somebody else's would be releasing beside them.

The one case force does change for release tags is a tag on a commit the current branch cannot reach: dispat's baseline
query cannot see it, so nothing planned around it, and the write simply succeeds.
