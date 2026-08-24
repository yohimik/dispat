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
baselines that future versions count from. Read [A game, from one package to many](./game.md) to watch a single
deliverable grow into a landing page, a docs site, an SDK, and a server in the same configuration file.
