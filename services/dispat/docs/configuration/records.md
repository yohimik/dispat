# Release records

What a successful release leaves behind besides the tag: the per-package changelog file, the GitHub release, and the
optional end-of-run release commit. `changelog` and `github` are top-level policies a single package may
[override field by field](./packages.md) — disable one record for one package, or point one
package's releases at another repository.

## Entry format options (shared by `changelog` and `github`)

| Key                 | Default            | Description                         |
|---------------------|--------------------|-------------------------------------|
| `dateFormat`        | `2006-01-02`       | Go time layout for the entry date.  |
| `breakingTitle`     | `Breaking Changes` | Section title for breaking changes. |
| `featuresTitle`     | `Features`         | Section title for features.         |
| `fixesTitle`        | `Fixes`            | Section title for fixes.            |
| `dependenciesTitle` | `Dependencies`     | Section title for provider updates. |

## `changelog`

| Key       | Default        | Description                                   |
|-----------|----------------|-----------------------------------------------|
| `enabled` | `true`         | Write a changelog file per published package. |
| `file`    | `CHANGELOG.md` | File name inside the package folder.          |
| `title`   | `# Changelog`  | First line of the file.                       |
| *format*  |                | All entry format options above.               |

New entries are prepended below the title, newest first.

**What an entry contains: the release-notes windowing.** An entry holds the release's notes grouped by bump (breaking
changes, features, fixes) plus the provider-updates section. On the stable channel that is every pending commit since
the package's last release. On a prerelease train each prerelease's entry contains **only its own changeset**, the
commits the train's earlier prereleases have not already published, so `beta.1` does not repeat `beta.0`'s notes; the
**graduation** then collects the whole train (everything since the last stable tag) into its one entry, which is what
readers of the stable line actually see. The version is still computed over the whole train either way (a breaking
change shipped in `beta.0` keeps the graduation at the next major); only the notes narrow. The same windowing drives
the [GitHub release body](#github) and the
[`DISPAT_BREAKING_CHANGES` / `DISPAT_FEATURES` / `DISPAT_FIXES` variables](../environment.md#release-notes-data), and
[`dispat preview`](../cli.md) shows exactly what the next entry would contain.

## `github`

| Key        | Default                   | Description                                                                                                                                                                                                                                                            |
|------------|---------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | `true`                    | Create a GitHub release per published package that exported [`DISPAT_EXPORT_GITHUB`](../environment.md#script-outputs).                                                                                                                                                |
| `owner`    | from `$GITHUB_REPOSITORY` | Repository owner.                                                                                                                                                                                                                                                      |
| `repo`     | from `$GITHUB_REPOSITORY` | Repository name.                                                                                                                                                                                                                                                       |
| `apiUrl`   | `https://api.github.com`  | REST endpoint; set for GitHub Enterprise.                                                                                                                                                                                                                              |
| `tokenEnv` | `GITHUB_TOKEN`            | Name of the environment variable holding the API token.                                                                                                                                                                                                                |
| *format*   |                           | All entry format options above. The release body contains only the sections; the `## pkg@version (date)` header line used in changelog files is omitted, since the release title is already the tag and GitHub shows its own date, so `dateFormat` has no effect here. |

The release is **opt-in per package and per run**: it is created exactly when one of the package's scripts exported
[`DISPAT_EXPORT_GITHUB`](../environment.md#script-outputs); a published package without the export is skipped (with an
info-level notice), so a script decides at run time which packages get a GitHub release. The release is named after the
tag (`pkg@1.3.0`); its body is the rendered changelog sections, under the same
[release-notes windowing](#changelog): a prerelease's release documents only its own changeset, a graduation the whole
train. When `enabled` but no repository or token can be resolved at runtime, GitHub releases are skipped with a warning
instead of failing the run. A configuration that *does* resolve is **verified against the API before any release work
starts** (`GET /repos/{owner}/{repo}`), with the release commit enabled or disabled alike, so misconfigured credentials
fail the run before anything is built.

**Which commit the release points at.** A GitHub release hangs off its tag, and GitHub resolves the tag by name on the
*remote*: if the tag already exists there when the release is created, the release attaches to exactly the commit it
marks; if not, GitHub creates the tag ref at the **default branch head**. What that means per mode:

- [`commit`](#commit) **disabled** (the default): the release is created right after the package publishes. The tag
  exists only locally at that point (on the commit the run released from, with pushing left to CI), so GitHub creates
  its tag ref at the default branch head. In the usual CI setup (a job on the default branch, tags pushed right after
  the run) the two coincide; they can differ if the run released another branch or the push never happened.
- [`commit`](#commit) **enabled**: GitHub releases move to the end of the run, and every release body documents the
  release commit SHA and the tag in a `### Release` section, whether or not they were pushed. With `commit.push` on,
  releases are created **after** the push and the tag is additionally pinned to the release commit via
  `target_commitish`. Without `push`, the SHA cannot be sent to GitHub (it does not exist on the remote yet), so
  GitHub creates the tag ref at the default branch head until you push; the true commit and tag remain recorded in
  the release body.
- A package whose scripts exported [`PACKAGE_<KEY>`](../environment.md#script-outputs) overrides both modes: its
  release documents the exported hash and sends it as `target_commitish`.

The export's value names the **release assets**: a whitespace-separated list of absolute paths to existing files, each
uploaded (named after the file, `application/octet-stream`) right after the release is created, in `commit`
mode too, where the release itself moves to the finalize phase. An invalid entry (a relative path, a missing file, a
directory) is skipped with a warning while the release and the remaining files go through; a failed upload of a valid
file still fails the package like any other recording failure.

## `commit`

| Key             | Default                  | Description                                                                                                     |
|-----------------|--------------------------|-----------------------------------------------------------------------------------------------------------------|
| `enabled`       | `false`                  | Create one release commit at the end of a successful run.                                                       |
| `messageFormat` | `chore(release): {tags}` | Template; `{tags}` and `{packages}` become comma-separated lists.                                               |
| `push`          | `false`                  | Push the release commit and tags. Tags that already exist on the remote are skipped with a warning; the rest are pushed. Only applies when `enabled` is true. |
| `remote`        | `origin`                 | Remote to push to.                                                                                              |
| `verify`        | `true`                   | Verify remote access (`git ls-remote`) before any release work when `push` is enabled. Set `false` to skip the check, e.g. for a remote that rejects ls-remote but accepts pushes. |
| `include`       | none                     | Extra repo-relative paths the release commit stages on top of the published packages' folders: the shared artifacts a version stage or an [`autoVersion.syncLock`](./spaces.md#autoversion) regenerates outside every package folder, a workspace-level `package-lock.json` first among them. Paths must stay inside the repository; one that does not exist at commit time is simply not staged. |

**Disabled** (the default), dispat creates no commit at all. Each package's annotated tag is created right after its
publish succeeds and points at the commit the run released from: `HEAD` of the checkout, which stays put for the whole
run since nothing is committed. Whatever the release changed on disk (changelog files, version-script manifest edits) is
left in the worktree, and pushing the tags is left to CI (`git push origin --tags`).

When **enabled**, the run instead finishes with a *finalize phase*: all published packages' folders are staged and
committed in a single commit (changelog files, version-script manifest changes, plus any `include` paths that exist;
add build outputs to your `.gitignore` or they get committed too), and the release tags are created **on that commit** instead of during each
publish. A package whose scripts exported [`PACKAGE_<KEY>`](../environment.md#script-outputs) is the exception: its
tag is excluded from the release commit and created at the exported commit hash instead. If nothing changed on disk
(e.g. changelogs disabled), no empty commit is created but tags are still placed.
GitHub releases move to the end of the run and document the release commit in their body; what the GitHub side does in
each mode is described under [`github`](#github).

Pushing pushes the branch first and the run's tags after it, skipping any tag that already exists on the remote
(with a warning naming it), so a re-run after a partially pushed release converges instead of dying on "tag already
exists". It requires a checked-out branch (not a detached HEAD; use `actions/checkout` with a `ref`). When `push` is
enabled, remote access is **verified before any release work starts** (`git ls-remote`, switched off by
`verify: false`), so a misconfigured remote fails the run before anything is built; an enabled GitHub configuration is likewise verified up front, push or not (see [
`github`](#github)). A failure during the finalize phase itself (commit, tag, push, GitHub release) exits 1, but
already-published registry artifacts stay published.
