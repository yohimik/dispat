# docs

The documentation site: <https://yohimik.github.io/dispat/>. A Docusaurus project, and the one Node package in this
repository — everything else here is Go. It is also a released package (`docs` in
[`dispat.json`](../../dispat.json)), so the site ships the same way the binaries do: through a dispat run.

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
link and every `#anchor` is resolved at build time, so a dead one fails the build instead of shipping.
[`.github/workflows/docs.yml`](../../.github/workflows/docs.yml) runs that build on every pull request touching this
folder, and does nothing else — deployment belongs to the release run.

`baseUrl` is `/dispat/`, and Docusaurus mounts its router **without** a `basename`: routes are registered with the base
URL already in them. Internal links in a `.tsx` page must therefore go through `@docusaurus/Link` or `useBaseUrl`, never
a raw router path — a bare `/getting-started` resolves outside the site and lands on the 404 page.

## Search

Search is [`@easyops-cn/docusaurus-search-local`](https://github.com/easyops-cn/docusaurus-search-local): the index is
built with the site and shipped in the artifact, so there is no Algolia account, no crawler and no API key in a public
repository. Its `docsRouteBasePath` must track `docs.routeBasePath` (`/`), or it silently indexes nothing.

That same `/` is why the index covers the doc pages only: the plugin treats every route under `docsRouteBasePath` as a
doc, and `/` is the parent of everything, so the landing page is classified as a doc, matches none, and is dropped —
`indexPages` cannot rescue it. Search engines see it through the sitemap and the page metadata instead.

`robots.txt` and `sitemap.xml` are covered in [`static/robots.txt`](./static/robots.txt), which explains why a project
Pages site can only really rely on the sitemap.

## Release

The `docs` package is released by dispat like any other, with a `keep: true` dependency on `dispat` so the site is
rebuilt after the CLI it documents:

| Stage     | Script                                                          | What it does                                                            |
|-----------|-----------------------------------------------------------------|-------------------------------------------------------------------------|
| `version` | [`scripts/cut-docs-version.sh`](../../scripts/cut-docs-version.sh) | Freezes a `versioned_docs` snapshot, once per **stable minor**; prereleases are skipped. |
| `build`   | `pnpm install --frozen-lockfile && pnpm run build`               | The production build, into `build/`.                                    |
| `publish` | [`scripts/deploy-docs.sh`](../../scripts/deploy-docs.sh)           | Force-pushes `build/` to an orphan `gh-pages` branch. Refuses to run outside CI. |

Tags are `packages/docs/v<version>`. `versioned_docs/`, `versioned_sidebars/` and `versions.json` are **source**, not
build output: a script writes them once and they are edited by hand afterwards, so they are tracked (see
[`.gitignore`](./.gitignore)).

## Requirements

Node 20 or later (CI uses the version in `.nvmrc`) and pnpm, from the root `packageManager` field. No global installs:
`corepack enable` or `pnpm/action-setup` is enough.

## Licence

MIT. See [LICENSE](../../LICENSE.md).
