# A single package, no monorepo

Set up automatic semantic versioning, changelogs, and tags for a single package without a monorepo. Skip `spaces`
entirely and declare one standalone `packages` entry. Point this entry to the folder where your code lives:

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

Put your deliverable in a subfolder like `src` or `app`. The `path` field must name a folder inside the repository, so
the package cannot be the repository root itself. Your config, changelog, and tags will live at the root, while your
scripts run inside the folder.

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
external writes when designing recovery. [release-it's discussion](https://github.com/release-it/release-it/issues/1230)
records npm publication followed by a rejected Git push. Its maintainer notes that release-it 20 and later preserve
the local release commit and tag for recovery. Keep that existing safeguard in any comparison; automatic pulling
before tagging can associate the release with code that was never built.

In dispat, a successful package publish is recorded by a tag. A rerun uses recorded completion when planning pending
work. This is forward recovery inspired by sagas: completed external writes remain in place. It is not a rollback
transaction across npm, GitHub and a website.

| Where the run stops | Integration requirement |
|---|---|
| Before an upload | Retry after fixing the build or publisher. |
| After the registry accepted the version but before its completion was recorded | Query that exact version and compare the intended artifact before retrying. |
| Between two destinations inside one publish script | Make the script reconcile each destination, or model independently recoverable deliverables as explicit packages. |
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

A dispat 1.10.0 disposable planning fixture confirmed that an unscoped `fix` under the sole package moved its tagged
baseline from 2.0.0 to 2.0.1 and rendered the same fix entry for the changelog and GitHub. No registry was contacted by
the fixture. Preserve the package-folder requirement above when evaluating an existing root-level npm repository;
this example does not claim automatic migration of that layout.

## Correct a pending note

Add a later commit with `Edits: <original-commit-sha>` to replace an unreleased record without rebasing shared history.
The original Git message stays unchanged. Once the record has shipped, publish a new correction instead. See
[the editing example](./npm.md#edit-pending-notes-without-rewriting-shared-history) and
[correction rules](../reference/corrections.md).
