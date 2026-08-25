# docs

Open the documentation site at <https://yohimik.github.io/dispat/>. This Docusaurus project is the only Node package in
the repository. Everything else is Go.

The site is a released package. dispat discovers it as a folder of the `packages` space declared in the root
[`dispat.yaml`](../../dispat.yaml), and everything it says about itself lives in [`dispat.yaml`](./dispat.yaml) here.
dispat ships it through a release run just like the binaries.

Edit the pages in [`docs/`](./docs). They are plain markdown with **no frontmatter**, so they read correctly on GitHub.
Ordering and labels live in [`sidebars.ts`](./sidebars.ts).

The Docusaurus docs plugin falls back to the opening paragraph for each page's `<meta name="description">`
(`frontMatter.description ?? excerpt`). The landing page is [`src/pages/index.tsx`](./src/pages/index.tsx). The shared
logo comes from the repository's `imgs/` folder, mounted through `staticDirectories` instead of copied.

## Development

Run everything from the **repository root**. This directory is the pnpm workspace root.

```sh
pnpm install                            # once; installs this package's dependencies
pnpm docs:start                         # dev server on http://localhost:3000/dispat/
pnpm docs:build                         # production build into packages/docs/build
pnpm --filter dispat-docs typecheck     # tsc, no emit
```

Node comes from [`.nvmrc`](./.nvmrc). The root `package.json` sets pnpm in its `packageManager` field, and CI pins both
tools from these two files. You can also run any pnpm command like `pnpm start` or `pnpm build` from inside this
folder, because pnpm finds the workspace root automatically.

## The build is the link checker

The `docusaurus.config.ts` file sets `onBrokenLinks`, `onBrokenAnchors`, and `onBrokenMarkdownLinks` to `throw`. Every
relative link and every `#anchor` resolves at build time, so a dead link fails the build instead of shipping. The build
job in [`.github/workflows/tests.yml`](../../.github/workflows/tests.yml) runs this check for every change to this
package and does nothing else, because deployment belongs to the release run.

The `baseUrl` is `/dispat/`, and Docusaurus mounts its router **without** a `basename`. Routes are registered with the
base URL already included, so route internal links in a `.tsx` page through `@docusaurus/Link` or `useBaseUrl`. Never
use a raw router path, because a bare `/getting-started` resolves outside the site and lands on the 404 page.

## Search

Search uses [`@easyops-cn/docusaurus-search-local`](https://github.com/easyops-cn/docusaurus-search-local). The index
builds with the site and ships in the artifact, so you need no Algolia account, no crawler, and no API key in a public
repository. Match its `docsRouteBasePath` to `docs.routeBasePath` (`/`), or it silently indexes nothing.

That same `/` is why the index covers only the doc pages. The plugin treats every route under `docsRouteBasePath` as a
doc, and `/` is the parent of everything. The landing page is classified as a doc, matches none, and drops out, so
`indexPages` cannot rescue it and search engines see it through the sitemap instead.

Read [`static/robots.txt`](./static/robots.txt) for details on `robots.txt` and `sitemap.xml`. It explains why a
project Pages site relies entirely on the sitemap.

## Installable, and offline for those who ask

Use [`@docusaurus/plugin-pwa`](https://docusaurus.io/docs/api/plugins/@docusaurus/plugin-pwa) to make the site
installable and let the search index serve without a network. This plugin is pinned to the exact same version as
`@docusaurus/core`, just like every other Docusaurus package here.

**Offline caching is opt-in.** The `offlineModeActivationStrategies` field uses the plugin's default trio
(`appInstalled`, `standalone`, `queryString`), so a visitor following a link never downloads the ~6 MB precache, a
third of which is `search-index.json`. Install the app or open it standalone to turn caching on, because setting this
to `always` spends bandwidth on everyone. A service worker registers either way, and its fetch handler does nothing
when offline mode is off to keep the site installable for everybody.

Two path rules look alike but act differently:

- **`pwaHead` entries are `baseUrl`-aware.** The plugin prefixes any `href` or `content` value that has a file
  extension, so a path like `/manifest.json` ships as `/dispat/manifest.json` while `#1b1b1d` and `yes` pass through
  untouched. The top-level `headTags` array is *not* aware, so it emits verbatim and the same paths there would 404.
- **[`static/manifest.json`](./static/manifest.json) is a plain static file.** Nothing rewrites its contents. Every URL
  inside it spells `/dispat/` out in full.

Set `scope` and `start_url` to `/dispat/`, never `/`. The `yohimik.github.io` domain is a shared origin hosting every
project site, and a `/` scope makes links to unrelated projects open inside the dispat app window. The `/dispat/` path
also matches the worker's own scope exactly, because the plugin emits `build/sw.js` at `/dispat/sw.js` and a worker's
scope defaults to its directory.

The icons in [`static/img/`](./static/img) derive from the repository's `imgs/logo.png`, which is 295×295 dark art on
transparency. Flatten them onto white first. iOS composites `apple-touch-icon` alpha onto black and the Android splash
draws the icon over `background_color`, so a dark mark on transparency disappears in both:

```sh
sips -s format jpeg -s formatOptions best imgs/logo.png --out /tmp/logo-flat.jpg   # JPEG has no alpha
sips -s format png /tmp/logo-flat.jpg --out /tmp/logo-flat.png

sips -z 192 192 /tmp/logo-flat.png --out packages/docs/static/img/icon-192.png
sips -z 512 512 /tmp/logo-flat.png --out packages/docs/static/img/icon-512.png
sips -z 288 288 /tmp/logo-flat.png -p 512 512 --padColor FFFFFF --out packages/docs/static/img/icon-maskable-512.png
sips -z 160 160 /tmp/logo-flat.png -p 180 180 --padColor FFFFFF --out packages/docs/static/img/apple-touch-icon.png
```

The 512 image is an upscale from 295 and softens the edges slightly, so regenerate this from a vector source if one
ever lands in `imgs/`. Chrome requires a 512 image for installability, so you cannot drop it. The maskable art is 288
px because the safe zone is the centred circle at 80% of the canvas, and the largest square inside a 409.6 px circle
has side 409.6/√2.

The plugin returns early unless `NODE_ENV=production`, so **nothing appears in `pnpm docs:start`**. Run a build, then
run `pnpm --filter dispat-docs serve` and open `http://localhost:3000/dispat/?offlineMode=true` to test this. Service
workers need a secure context, which `localhost` provides and a LAN IP does not.

Cache Storage stays empty *without* that query string, which means the feature is working. Check DevTools → Application
→ Manifest to verify, because Lighthouse dropped its PWA category in Chrome 129.

A registered service worker outlives the plugin that installed it. Back PWA support out by shipping one release where
`swCustom` calls `self.registration.unregister()` and purges the caches. Delete the plugin only after that release
ships.

## Demo animations

The animated demos on the landing page, in the repository README, and on the commit-messages page come from
[`demo/`](./demo). Each clip is a [Remotion](https://www.remotion.dev) composition rendered into the committed assets
in the repository's `imgs/` folder, which the site serves statically at `/dispat/<name>`. Run `demo/render.sh` to
regenerate them after editing a scene. Read [`demo/README.md`](./demo/README.md) for the composition list and the size
budget the script enforces.

## Release

dispat releases the `docs` package like any other. It has a `keep: true` dependency on `dispat`, so the site rebuilds
after the CLI it documents:

| Stage     | Script                                                   | What it does                                                                             |
|-----------|----------------------------------------------------------|------------------------------------------------------------------------------------------|
| `build`   | the `build` script in [`dispat.yaml`](./dispat.yaml)     | This runs the containerised production build via [`Dockerfile`](./Dockerfile) and exports it back into the package folder. It freezes a `versioned_docs` snapshot on the way, once per **stable minor** release. The build uses the `DOCS_VERSION` build arg, and prereleases cut nothing. |
| `publish` | the `deploy-docs` script in [`dispat.yaml`](./dispat.yaml) | This force-pushes `build/` to an orphan `gh-pages` branch. It refuses to run outside CI.         |

Tags use the format `packages/docs/v<version>`. The files `versioned_docs/`, `versioned_sidebars/`, and `versions.json`
are **source**, not build output. A script writes them once, and you edit them by hand afterwards, so they remain
tracked in [`.gitignore`](./.gitignore).

## Requirements

Install Node 20 or later and pnpm from the root `packageManager` field. CI uses the Node version in `.nvmrc`. You need
no global installs, because `corepack enable` or `pnpm/action-setup` is enough.

## Licence

This is MIT licensed. See [LICENSE](../../LICENSE) for details.
