# An npm monorepo

Save this configuration to define the smallest real npm monorepo release setup. It contains one space of packages, a
build and a publish command, and versions computed from your commits.

```json
{
  "scripts": {
    "build": "npm ci --silent && npm run build --silent",
    "publish": "npm publish --access public"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Commit your work with the package name in the scope. Run `dispat status` to check the plan before you release anything:

```console
$ git commit -m "feat(logger): first version of the logger"
$ dispat status
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
```

Run `dispat` to execute the release. The `status` command you just ran changes nothing on disk.

```console
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
12:04:05 INF build started package=logger stage=build version=0.1.0
12:04:05 INF added 42 packages in 1s package=logger stage=build version=0.1.0
12:04:05 INF build succeeded package=logger stage=build version=0.1.0
12:04:05 INF publish started package=logger stage=publish version=0.1.0
12:04:05 INF + logger@0.1.0 package=logger stage=publish version=0.1.0
12:04:05 INF published package=logger stage=publish tag=logger@0.1.0 version=0.1.0
12:04:05 INF summary channel=stable package=logger status=published tag=logger@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.2s unchanged=0

$ git tag
logger@0.1.0
```

Push the annotated tag to finish the release. This tag is the record that the publish happened, but you can also
configure dispat to push it for you (see [release records](../configuration/records.md)).

Stamp the computed version into your package before packing. Your `package.json` version field does not drive anything.
dispat computes the version from commits and tags, and hands it to your scripts as `$DISPAT_NEW_VERSION`:

```sh
npm version "$DISPAT_NEW_VERSION" --no-git-tag-version && npm ci && npm run build
```

## Verify uploads and channel updates

Treat publication of an exact version and an npm dist-tag update as distinct operations. If a later destination fails,
reconcile the already-published version outside the publish command before retrying it. See
[release recovery](../reference/releasing/recovery.md).

Do not use the presence of `.changeset/*.md` as a proxy for publication readiness. Ignored packages can retain change
files while other packages are ready. If Changesets remains the native coordinator, account for its ignore policy and
reconcile already-versioned packages before retrying. dispat does not make two coordinators share completion state.

Verify Git records independently too. A configured script can report success without creating every expected tag.
Check each ref and push result; dispat sees the script's process status but cannot correct a false success receipt.

### Compare the planned components with the parsed release record

dispat computes its package plan from Git and does not parse or validate another tool's release record. When a release
script emits a multi-component record, compare the intended component names with the names parsed back from that record
before publication. Reconcile registry versions outside the publish command when recovering an ambiguous result.

## Moving release intent out of Changeset files

Changesets maintains a separate release-intent file for each submitted change, then generates versions and changelogs.
It also supports [commit-message generators](https://github.com/changesets/changesets/blob/main/docs/config-file-options.md)
and third-party adapters. Commit-derived input and separate change files are authoring choices; preserve explicit
dependency policy when choosing between them.

With dispat, put the reviewed note in the Git commit message. For example, when `app` exposes `core`'s breaking API:

```text
feat(core)!: require explicit authentication

Pass the token explicitly when creating a client.

Propagate: major
Propagate-Depth: 1
```

With an `app` → `core` dependency and both baselines at 1.0.0, this commit plans 2.0.0 for both.
The preview includes core's description/body and app's dependency transition. Major propagation is explicit; a provider's breaking change does not imply that every consumer has broken its own API.
Keep the footer lines together in one final block. See [commit syntax](../reference/commits.md).

Before changing the production release path:

1. Preserve published tags and starting versions using the [adoption procedure](./adopting.md).
2. Review outstanding `.changeset` entries and retain their unreleased notes and bump decisions. dispat does not
   automatically import those files; do not consume the same intent through two coordinators.
3. Compare versions, dependency ranges, prerelease transitions and `dispat preview --changelog --github` output.
   Preserve the reviewed commit text through the repository's merge method.
4. Retain the existing build/publish commands and test partial publication plus downstream finalization separately.

Separate Changeset summaries remain useful when maintainers want release prose reviewed independently of commit
history. Choosing commit-based input moves that responsibility into commit review; it does not remove it. A single
package can use the same approach: see [single-package recovery and notes](./single-package.md).

## Compare the published manifest, not only the source manifest

Some release scripts rewrite versions only in the build workspace. Compare the plan with the packed manifest and its
internal dependency versions after those rewrites. Then publish the artifact that passed that check. A different
version in the untouched source manifest is not sufficient evidence of a release-identity defect.

## Edit pending notes without rewriting shared history

Changesets' commit-message generation is a one-time output. Editing a Changeset file later does not update a previous
Git commit message. A normal follow-up commit can record that file edit; amend/rebase is only needed if the old
message itself must change. Generated messages and editable release-intent files are separate records.

In dispat, an ordinary correction commit can replace a pending release record:

```text
fix(core): handle empty input and preserve result shape

Edits: <original-commit-sha>
```

Use the real SHA and the original package scope. An empty correction commit is allowed. Preview the result before
release: the old Git commit remains unchanged, while the pending version plan and notes use the correction. Corrections
reach only unreleased ancestor records; they cannot revise an already-published release. See
[correcting release records](../reference/corrections.md).

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.
