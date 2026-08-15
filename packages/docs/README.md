# docs

The documentation site: <https://yohimik.github.io/dispat/>. A Docusaurus project, and the one Node package in this
repository; everything else here is Go. It is also a released package (`docs` in
[`dispat.yaml`](../../dispat.yaml)), so the site ships the same way the binaries do: through a dispat run.

The pages live in [`docs/`](./docs) as plain markdown with **no frontmatter**, so they read correctly on GitHub too.
Ordering and labels therefore live in [`sidebars.ts`](./sidebars.ts), and each page's `<meta name="description">` falls
back to its opening paragraph (`frontMatter.description ?? excerpt`, Docusaurus' docs plugin). The landing page is
[`src/pages/index.tsx`](./src/pages/index.tsx); the shared logo comes from the repository's `imgs/` folder, mounted
through `staticDirectories` rather than copied.

## Development

Everything runs from the **repository root**, which is the pnpm workspace root:

```sh
pnpm install                            # once; installs this package's dependencies
pnpm docs:start                         # dev server on http://localhost:3000/dispat/
pnpm docs:build                         # production build into packages/docs/build
pnpm --filter dispat-docs typecheck     # tsc, no emit
```

Node comes from [`.nvmrc`](./.nvmrc) and pnpm from the root `package.json`'s `packageManager` field; CI pins both from
those two files. Any pnpm command can also be run from inside this folder (`pnpm start`, `pnpm build`), since pnpm finds
the workspace root by itself.

## The build is the link checker

`docusaurus.config.ts` sets `onBrokenLinks`, `onBrokenAnchors` and `onBrokenMarkdownLinks` to `throw`: every relative
link and every `#anchor` is resolved at build time, so a dead one fails the build instead of shipping. The build job of
[`.github/workflows/tests.yml`](../../.github/workflows/tests.yml) runs that build for every change that reaches this
package, and does nothing else: deployment belongs to the release run.

`baseUrl` is `/dispat/`, and Docusaurus mounts its router **without** a `basename`: routes are registered with the base
URL already in them. Internal links in a `.tsx` page must therefore go through `@docusaurus/Link` or `useBaseUrl`, never
a raw router path: a bare `/getting-started` resolves outside the site and lands on the 404 page.

## Search

Search is [`@easyops-cn/docusaurus-search-local`](https://github.com/easyops-cn/docusaurus-search-local): the index is
built with the site and shipped in the artifact, so there is no Algolia account, no crawler and no API key in a public
repository. Its `docsRouteBasePath` must track `docs.routeBasePath` (`/`), or it silently indexes nothing.

That same `/` is why the index covers the doc pages only: the plugin treats every route under `docsRouteBasePath` as a
doc, and `/` is the parent of everything, so the landing page is classified as a doc, matches none, and is dropped:
`indexPages` cannot rescue it. Search engines see it through the sitemap and the page metadata instead.

`robots.txt` and `sitemap.xml` are covered in [`static/robots.txt`](./static/robots.txt), which explains why a project
Pages site can only really rely on the sitemap.

## Installable, and offline for those who ask

[`@docusaurus/plugin-pwa`](https://docusaurus.io/docs/api/plugins/@docusaurus/plugin-pwa) makes the site installable and
lets the search index above serve without a network. It is pinned to the same exact version as `@docusaurus/core`, like
every other Docusaurus package here.

**Offline caching is opt-in.** `offlineModeActivationStrategies` is left at the plugin's default trio (`appInstalled`,
`standalone`, `queryString`), so someone who follows a link to one page never downloads the ~6 MB precache, a third of
which is `search-index.json`. Installing the app, or opening it standalone, turns it on. `always` would spend that
bandwidth on everyone. A service worker is registered either way, with a fetch handler that simply does
nothing when offline mode is off: that is what keeps the site installable for everybody.

Two path rules that look alike and are not:

- **`pwaHead` entries are `baseUrl`-aware.** The plugin prefixes any `href`/`content` whose value has a file extension,
  so `/manifest.json` ships as `/dispat/manifest.json` while `#1b1b1d` and `yes` pass through untouched. The top-level
  `headTags` array is *not*: it is emitted verbatim, and the same paths there would 404.
- **[`static/manifest.json`](./static/manifest.json) is a plain static file.** Nothing rewrites its contents, so every
  URL inside it spells `/dispat/` out in full.

`scope` and `start_url` are `/dispat/`, never `/`. `yohimik.github.io` is a shared origin hosting every one of these
project sites, and a `/` scope would make links to unrelated projects open inside the dispat app window. `/dispat/` also
matches the worker's own scope exactly: the plugin emits `build/sw.js`, served at `/dispat/sw.js`, and a worker's scope
defaults to the directory it sits in.

The icons in [`static/img/`](./static/img) are derived from the repository's `imgs/logo.png` (295×295, dark art on
transparency). They are flattened onto white first, because iOS composites `apple-touch-icon` alpha onto black and the
Android splash draws the icon over `background_color`, and a dark mark on transparency disappears in both:

```sh
sips -s format jpeg -s formatOptions best imgs/logo.png --out /tmp/logo-flat.jpg   # JPEG has no alpha
sips -s format png /tmp/logo-flat.jpg --out /tmp/logo-flat.png

sips -z 192 192 /tmp/logo-flat.png --out packages/docs/static/img/icon-192.png
sips -z 512 512 /tmp/logo-flat.png --out packages/docs/static/img/icon-512.png
sips -z 288 288 /tmp/logo-flat.png -p 512 512 --padColor FFFFFF --out packages/docs/static/img/icon-maskable-512.png
sips -z 160 160 /tmp/logo-flat.png -p 180 180 --padColor FFFFFF --out packages/docs/static/img/apple-touch-icon.png
```

The 512 is an upscale from 295 and softens the edges slightly; regenerate from a vector source if one ever lands in
`imgs/`. Chrome requires a 512 for installability, so it cannot simply be dropped. The maskable art is 288 px because
the safe zone is the centred circle at 80% of the canvas, and the largest square inside a 409.6 px circle has side
409.6/√2.

To test any of this: the plugin returns early unless `NODE_ENV=production`, so **nothing appears in `pnpm docs:start`**.
Build, then `pnpm --filter dispat-docs serve` and open `http://localhost:3000/dispat/?offlineMode=true`. Service
workers need a secure context, which `localhost` is and a LAN IP is not. Cache Storage staying empty *without* that
query string is the feature working, not a bug. DevTools → Application → Manifest is the check that matters; Lighthouse
dropped its PWA category in Chrome 129.

One thing to know before ever removing this: a registered service worker outlives the plugin that installed it. Backing
PWA support out means shipping one release whose `swCustom` calls `self.registration.unregister()` and purges the
caches, and only then deleting the plugin.

## Release

The `docs` package is released by dispat like any other, with a `keep: true` dependency on `dispat` so the site is
rebuilt after the CLI it documents:

| Stage     | Script                                                             | What it does                                                                             |
|-----------|--------------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `version` | [`scripts/cut-docs-version.sh`](../../scripts/cut-docs-version.sh) | Freezes a `versioned_docs` snapshot, once per **stable minor**; prereleases are skipped. |
| `build`   | `pnpm install --frozen-lockfile && pnpm run build`                 | The production build, into `build/`.                                                     |
| `publish` | [`scripts/deploy-docs.sh`](../../scripts/deploy-docs.sh)           | Force-pushes `build/` to an orphan `gh-pages` branch. Refuses to run outside CI.         |

Tags are `packages/docs/v<version>`. `versioned_docs/`, `versioned_sidebars/` and `versions.json` are **source**, not
build output: a script writes them once and they are edited by hand afterwards, so they are tracked (see
[`.gitignore`](./.gitignore)).

## Requirements

Node 20 or later (CI uses the version in `.nvmrc`) and pnpm, from the root `packageManager` field. No global installs:
`corepack enable` or `pnpm/action-setup` is enough.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
