# A single package, no monorepo

dispat on a repository with one thing in it: automatic semantic versioning, changelogs and tags for a single package,
with no monorepo required.

dispat is built for monorepos, but nothing requires more than one package. A repository whose whole deliverable is a
single package skips `spaces` entirely and declares one standalone `packages` entry pointing at the folder the code
lives in:

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

One rule to know: `path` must name a folder inside the repository, so the package cannot be the repository root itself.
Keeping the deliverable in a subfolder (`src`, `app`, whatever you already build from) is all it takes; the config, the
changelog and the tags live at the root, the scripts run inside the folder.

Commits scoped with the package name drive it, and a commit with no scope counts too when it touches files inside the
folder:

```console
$ git commit -m "feat(app): first version"
$ dispat status
09:31:07 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=app reason=direct space=app version="0.0.0 -> 0.1.0"
09:31:07 INF release plan ready held=0 packages=1 releasing=1
```

Everything else on this page works unchanged with one package: channels, the changelog, GitHub releases, hooks,
`dispat run`. If the repository grows a second deliverable later, add another entry (or a space) and declare the edge
between them; the single-package setup is just the smallest case of the general one.
