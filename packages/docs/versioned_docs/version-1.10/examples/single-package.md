# A single package, no monorepo

Set up semantic versioning, changelogs, and tags for a single package without a monorepo. Skip `spaces` entirely and
declare one standalone `packages` entry pointing to your source or package folder. An npm project can keep its only
`package.json` at the repository root.

## One root manifest

Install dispat once with `npm install --save-dev @dispat/bin` and commit the npm lockfile. Follow
[npm installation](../getting-started.md#install-with-npm) if your package manager requires installation-script approval.

```text
project/
  package.json             # your package, scripts, and @dispat/bin devDependency
  package-lock.json
  dispat.yaml
  src/
    index.js
```

The source folder needs no manifest of its own. Save this configuration at the repository root:

```yaml title="dispat.yaml"
scripts:
  version: cd .. && npm version "$DISPAT_NEW_VERSION" --no-git-tag-version --ignore-scripts --allow-same-version
  tests: cd .. && npm test
  build: cd .. && npm run build
  publish: cd .. && npm publish --access public

packages:
  app:
    path: src
    tagFormat: v{version}
    autoVersion:
      enabled: true
      manifests: none
    flow:
      version: version
      beforeBuild: tests
      build: build
      publish: publish

commit:
  enabled: true
  include: [package.json, package-lock.json]
```

Use `path: lib` instead for a root-level `lib/` folder. Scripts start inside that folder, and `cd ..` selects the root
manifest for every npm command. Keep your actual test and build scripts; omit the build script and its flow entry if
you publish directly from source. Keep the manifest publishable and select the shipped files with its `files` field.
A private application can use its deployment command in the publish stage instead.

The version stage updates `package.json` and its existing npm lockfile. It skips npm's version lifecycle scripts and
Git commit/tag creation, leaving release recording to dispat. `autoVersion.enabled` schedules the version stage;
`manifests: none` disables native manifest scanning. Keep this block so a direct release runs `flow.version`.
`compute` does not discover the parent manifest.

Each new package starts versioning from `0.0.0`. You choose how it first releases through its commits:

| Commit | First version |
| --- | --- |
| `fix(app): handle empty input` | `0.0.1` |
| `feat(app): add the first feature` | `0.1.0` |
| `feat(app)!: release the first stable API` | `1.0.0` |
| `feat(app)%beta: try the first feature` | `0.1.0-beta.0` |
| `feat(app)%beta!: try the first stable API` | `1.0.0-beta.0` |

These examples are alternatives for a new package with no earlier release tags. An unscoped `feat!: ...` also
selects `1.0.0` when its changed files belong to the source folder. For a beta release, add `--tag beta` to the npm
publish command so it publishes on the matching npm dist-tag. See [Channels and prereleases](../reference/commits.md#channels-and-prereleases)
for continuing a prerelease or graduating it to stable.

Once the package has a release tag, that tag supplies the baseline for subsequent versions. When adopting an already
published package, preserve its existing `tagFormat` and history; see [Adopting dispat](./adopting.md).

Commit the setup, then inspect the release from the repository root:

```sh
npm exec -- dispat status
npm exec -- dispat preview
```

An unscoped `fix: handle empty input` counts when it changes `src/`. Use `fix(app): update runtime dependencies` for a
release change confined to the root manifest, lockfile, tests, or other files outside `src/`. The scope is the dispat
key `app`, even when the npm name differs. Files outside the package folder do not gain change ownership through
`commit.include`; that list controls release-commit staging. See [What counts as a change](../configuration/change-scope.md).

When enabled, changelog recording writes `src/CHANGELOG.md` (or `lib/CHANGELOG.md`). Run actual releases through
[CI](../reference/ci.md), with npm authentication and Git permissions for your destination. These npm publish commands
target stable releases; add your intended npm dist-tag when releasing prereleases.

If versioning succeeds and a later stage fails, inspect the root manifest and lockfile before retrying.
`revertOnFail` restores the configured package folder, not these parent files. The version command accepts the same
planned version on retry, but existing release-input edits must still be resolved before dispat's clean-path check
allows another run. Use a fresh CI checkout of the reviewed revision for a retry after a failure before publication;
after any accepted publication, reconcile its records first as described below.

## A manifest inside the package folder

If your manifest already lives beside the code in a subfolder, commands can run there directly:

```json
{
  "scripts": {
    "build": "npm ci && npm run build",
    "publish": "npm publish --access public"
  },
  "packages": {
    "app": {
      "path": "src",
      "flow": { "build": "build", "publish": "publish" }
    }
  }
}
```

The `path` field must name a folder inside the repository; `path: .` is not supported. In this layout, the folder
contains its own manifest, and its scripts and changelog use that folder. The dispat configuration lives at the root,
and tags belong to the repository.

Write commits scoped with the package name to drive releases. A commit with no scope also counts when it touches files
inside the folder. Watch how dispat handles a new feature:

```console
$ git commit -m "feat(app): first version"
$ dispat status
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=app reason=direct space=app version="0.0.0 -> 0.1.0"
09:31:07 INF release plan ready held=0 packages=1 releasing=1
```

Everything else works unchanged with one package. You can use channels, the changelog, GitHub releases, hooks, and
`dispat run` exactly as documented. If the repository grows a second deliverable later, add another entry or a space
and declare the edge between them.

## When it grows

Add new packages without breaking your history. Nothing moves or gets renamed, and your published tags remain the
baselines that future versions count from. Read [From one package to many](./one-to-many.md) to watch a single
deliverable grow into a landing page, a docs site, an SDK, and a server in the same configuration file.

## One package can still have a partial release

A single npm package may also publish a binary archive, a GitHub release and a documentation site. Count those
external writes when designing recovery. Preserve the exact source revision and artifact identity across publication
and recording; updating the checkout after a build can associate release records with code that was never built.

In dispat, a successful package publish is recorded by a tag. A rerun uses recorded completion when planning pending
work. This is forward recovery inspired by sagas: completed external writes remain in place. It is not a rollback
transaction across npm, GitHub and a website.

| Where the run stops | Integration requirement |
|---|---|
| Before an upload | Retry after fixing the build or publisher. |
| After the registry accepted the version but before its completion was recorded | Reconcile that exact version and artifact outside the publish command before retrying. |
| Between two destinations inside one publish script | Reconcile each destination before a retry, or model independently recoverable deliverables as explicit packages. |
| After a tag exists but a separate announcement or document deployment failed | Give that operation an explicit retry path; the tag alone is not its delivery receipt. |

## Write release notes with the change

For a commit changing only this package, an unscoped message can derive its package from the changed path:

```text
fix: handle empty input without throwing

Return an empty result when no records are supplied.
```

Run `dispat status` and `dispat preview --changelog --github` to inspect the planned version and rendered notes.
The description and body supply the prose; a separate per-change release-intent file is unnecessary. Keep the reviewed
message when squash-merging. Issue and PR discussion comments are not release input.

For the root-manifest layout above, scope changes outside the source folder with `app` when they should release it.

## Correct a pending note

Add a later commit with `Edits: <original-commit-sha>` to replace an unreleased record without rebasing shared history.
The original Git message stays unchanged. Once the record has shipped, publish a new correction instead. See
[the editing example](./npm.md#edit-pending-notes-without-rewriting-shared-history) and
[correction rules](../reference/corrections.md).
