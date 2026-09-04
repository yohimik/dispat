import type {Options as PwaOptions} from '@docusaurus/plugin-pwa';
import type * as Preset from '@docusaurus/preset-classic';
import type {Config} from '@docusaurus/types';
import {themes as prismThemes} from 'prism-react-renderer';
import path from 'node:path';

import readme from './plugins/readme';
import testReport from './plugins/test-report';

const GITHUB = 'https://github.com/yohimik/dispat';
const DISCORD = 'https://discord.gg/83PwVSCCmk';
const SITE = 'https://dispat.dev';

// Shared metadata for search results and social previews.
const TITLE = 'Dispat: release automation across languages and repositories';
const TAGLINE = 'Release your packages together, across languages';
const DESCRIPTION =
  'Dispat turns conventional commits into versions, changelogs, and ordered releases. ' +
  'Automate publishing across languages in one repository or several.';

// What a reader types into a search engine when they are looking for this tool,
// in the three shapes it is looked for under: the monorepo one, the
// many-repositories one, and the pattern the run is built on. The list is one
// place because the meta tag and both structured-data blocks read it.
const KEYWORDS = [
  'monorepo release tool',
  'monorepo',
  'polyglot monorepo',
  'polyrepo',
  'polyrepo release tool',
  'multi-repo release',
  'control repository',
  'release automation',
  'release orchestration',
  'monorepo versioning',
  'monorepo changelog',
  'monorepo tagging',
  'semantic release monorepo',
  'conventional commits',
  'semantic versioning',
  'changelog',
  'github releases automation',
  'lerna alternative',
  'npm',
  'docker',
  'go modules',
  'cargo',
  'maven',
  'saga pattern',
  'saga',
  'forward recovery',
  'idempotent release planning',
  'distributed transaction',
  'release lock',
];

// What the project is *about*, as things rather than as search terms: the
// structured-data blocks below carry both, and a crawler reads this one as the
// subject and the keyword list as the phrasing.
const SUBJECTS = [
  {'@type': 'Thing', name: 'Release automation'},
  {'@type': 'Thing', name: 'Saga pattern'},
  {'@type': 'Thing', name: 'Distributed transaction'},
  {'@type': 'Thing', name: 'Monorepo'},
  {'@type': 'Thing', name: 'Polyrepo'},
  {'@type': 'Thing', name: 'Conventional Commits'},
  {'@type': 'Thing', name: 'Semantic versioning'},
];

// Sitemap priorities, by what a page is for. The value is relative within this
// site and means nothing outside it, so a sitemap that gives every page 0.5
// states no preference at all, which is where this one started. Google ignores
// priority and changefreq outright, as the sitemap plugin's own source says
// twice; Bing and the smaller crawlers still read them.
//
// A route is classified by its site-relative path, with baseUrl and the
// trailing slash taken off, which leaves no version segment either. A versioned
// copy of `getting-started` therefore lands on the default rather than on 0.9.
// That costs nothing while the unreleased version is noIndex and never reaches
// the sitemap at all (see `versions` below), and it is the first thing to
// revisit if that ever changes.
const ENTRY_PAGES = new Set(['getting-started', 'concepts', 'monorepo', 'control-repository']);
const SECTION_INDEXES = new Set(['api', 'cli', 'configuration', 'examples', 'go', 'faq']);
const LONG_TAIL_PREFIXES = ['examples/', 'internals/'];

/**
 * The priority a route takes in the sitemap, from the path it is served at:
 * '' for the landing page, 'cli/release' for a command.
 *
 * The default is the reference leaf, which is most of the site: everything a
 * reader looks something up in. The two sets are the exceptions above it, the
 * reading path and the page each section opens on. The prefixes are the long
 * tail below it, one page per ecosystem and one figure per suite, worth
 * publishing and not worth crawling first.
 */
function sitemapPriority(path: string): number {
  if (path === '') {
    return 1.0;
  }
  if (ENTRY_PAGES.has(path)) {
    return 0.9;
  }
  if (SECTION_INDEXES.has(path)) {
    return 0.8;
  }
  if (LONG_TAIL_PREFIXES.some((prefix) => path.startsWith(prefix))) {
    return 0.4;
  }
  return 0.6;
}

const config: Config = {
  title: 'dispat',
  tagline: TAGLINE,
  // The SVG carries its own prefers-color-scheme rule, so the glyph stays
  // visible on a dark tab strip; the .ico in headTags is the fallback for
  // whatever cannot read it.
  favicon: 'favicon.svg',

  url: 'https://dispat.dev',
  baseUrl: '/',
  organizationName: 'yohimik',
  projectName: 'dispat',

  // Every route is written as <route>/index.html, which is the file the
  // bucket's main_page_suffix answers a directory request from. Behind the
  // load balancer the bucket never redirects /page to /page/ the way its own
  // website endpoint would, so the slash must already be in every URL the
  // site spells: left unset, Docusaurus writes the sitemap and the site's
  // own hrefs without it, and all but a handful of URLs would land on the
  // 404 page instead of a page.
  //
  // `false` is the trap rather than the alternative: it writes cli.html beside
  // the cli/ directory that holds the command pages, a directory request
  // resolves to the folder's missing index, and the four folder index pages
  // 404.
  trailingSlash: true,

  // Link rot is the whole reason this site exists as a build step: every
  // relative link and every #anchor is resolved at build time, so a bad one
  // fails CI instead of shipping.
  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  // imgs/ lives at the repository root and is shared with the READMEs, so it
  // is mounted rather than copied. Anything added there is published too.
  staticDirectories: ['static', '../../imgs'],

  plugins: [
    (context) => ({
      name: 'single-react-remotion-runtime',
      configureWebpack: () => ({
        resolve: {
          alias: {
            react: path.join(context.siteDir, 'node_modules/react'),
            'react-dom': path.join(context.siteDir, 'node_modules/react-dom'),
            remotion: path.join(context.siteDir, 'node_modules/remotion'),
          },
        },
      }),
    }),
    // The two places this site states something measured rather than written.
    // Both are imported as functions rather than named by path: the config is
    // TypeScript, and importing them means they are compiled by the same
    // loader and type-checked by `pnpm typecheck` along with everything else.
    readme,
    testReport,
    [
      // Installable, and readable offline once installed. The plugin emits
      // build/sw.js, served at /sw.js, so the worker's scope is the directory
      // it sits in, which on this dedicated domain is the whole origin the
      // site owns. None of this exists in `docusaurus start` --
      // the plugin returns null outside NODE_ENV=production, so a service
      // worker only appears in a real build.
      '@docusaurus/plugin-pwa',
      {
        debug: false,
        // The plugin's own default set, spelled out. Offline mode is what
        // decides whether the worker precaches the whole site (~6 MB, a third
        // of it the search index) and starts answering fetches from the cache;
        // without it the worker installs and does nothing. 'always' would push
        // that download onto every drive-by reader, so offline stays opt-in:
        // installed, running standalone, or ?offlineMode=true for debugging.
        offlineModeActivationStrategies: ['appInstalled', 'queryString', 'standalone'],
        // The cast is the plugin's type being wrong, not this config: it types
        // this field as a full workbox InjectManifestOptions, which demands a
        // swSrc -- while the plugin sets swSrc, swDest and globDirectory itself
        // *after* spreading this object, under the comment "not overrideable".
        injectManifestConfig: {
          // search-index.json is 1.8 MB, 86% of workbox's 2 MiB default, and
          // anything over that default is dropped from the precache silently:
          // offline search would switch itself off, without a word, once the
          // docs grow another 16%.
          maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        } as PwaOptions['injectManifestConfig'],
        // href/content values carrying a file extension are baseUrl-prefixed
        // by the plugin itself; with the base at '/' the prefix is invisible,
        // but the distinction still matters if the base ever changes. Colours
        // and 'yes' have no extension and pass through untouched. The
        // headTags array below is emitted verbatim instead.
        pwaHead: [
          {tagName: 'link', rel: 'manifest', href: '/manifest.json'},
          {tagName: 'meta', name: 'theme-color', content: '#101713'},
          {tagName: 'meta', name: 'mobile-web-app-capable', content: 'yes'},
          {tagName: 'meta', name: 'apple-mobile-web-app-capable', content: 'yes'},
          {tagName: 'meta', name: 'apple-mobile-web-app-title', content: 'dispat'},
          // Safari accepts default | black | black-translucent here and nothing
          // else; a hex colour silently degrades to 'default'.
          {tagName: 'meta', name: 'apple-mobile-web-app-status-bar-style', content: 'black'},
          {tagName: 'link', rel: 'apple-touch-icon', href: '/img/apple-touch-icon.png'},
          {tagName: 'meta', name: 'msapplication-TileImage', content: '/img/icon-192.png'},
          {tagName: 'meta', name: 'msapplication-TileColor', content: '#101713'},
        ],
        // swRegister is left at its default: that file is what implements the
        // strategies above. reloadPopup is not an option at all in v3 -- it is
        // Joi.forbidden() and fails the build; the reload prompt is
        // @theme/PwaReloadPopup, pinned by the re-export in
        // src/theme/PwaReloadPopup (see the comment there for why), which is
        // also the file to grow into a custom prompt if one is ever wanted.
      } satisfies PwaOptions,
    ],
  ],

  presets: [
    [
      'classic',
      {
        docs: {
          // ./docs is the default; the pages are plain markdown with no
          // frontmatter, so ordering and labels live in sidebars.ts.
          // The site is nothing but the docs, so they own the root: the
          // README tables link to /<page>/, not /docs/<page>/.
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: ({docPath}) =>
            `${GITHUB}/edit/main/packages/docs/docs/${docPath}`,
          editCurrentVersion: true,
          showLastUpdateTime: true,
          // The unreleased version is a copy of the released docs, and
          // indexing it would only split the site against itself. noIndex
          // marks those pages, and the sitemap plugin drops every route it
          // marks, which keeps /next/ out of the sitemap with no pattern to
          // keep in step. The version dropdown still lists it as "Next" for
          // a reader who wants the unreleased line; crawlers stay away.
          //
          // Its own root, /next/, is a 404 and stays one: routeBasePath is '/',
          // so a version's root is whatever page sits at that path, and the
          // landing page exists for the released version only. Nothing indexed
          // links to it.
          versions: {
            current: {noIndex: true},
          },
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
        sitemap: {
          // robots.txt at the domain root names this sitemap, and the sitemap
          // is what actually enumerates the pages for a crawler (see
          // static/robots.txt). lastmod is off by default.
          lastmod: 'date',
          // Patterns match full route paths, baseUrl included, and the path
          // is spelled the way trailingSlash spells it: with the slash on, the
          // bare `/search` matches nothing and the page comes back into the
          // sitemap. The brace matches the route under either setting, and
          // matches nothing else. /search is a client-side view of the index,
          // not a page.
          ignorePatterns: ['/search{,/}'],
          // A priority and a changefreq per page, which is why the flat
          // `priority` and `changefreq` options are absent: every item gets
          // both from here, and a default underneath would be a second value
          // nothing reads. The tiers are sitemapPriority above. The path comes off the item's own
          // URL, which the default already built with baseUrl and the trailing
          // slash applied, rather than out of `routes`, which would be a scan
          // per item. The default runs exactly once: it resolves lastmod
          // through git per route, and calling it twice repeats every one of
          // those.
          //
          // The values reach the XML with one decimal, 1.0 included: the
          // library the plugin writes through formats them, so nothing here
          // has to carry a number as a string to keep its shape.
          createSitemapItems: async ({defaultCreateSitemapItems, ...rest}) => {
            const items = await defaultCreateSitemapItems(rest);
            return items.map((item) => {
              const path = new URL(item.url).pathname
                .slice(rest.siteConfig.baseUrl.length)
                .replace(/\/$/, '');
              const priority = sitemapPriority(path);
              return {
                ...item,
                priority,
                // The pages a reader arrives on are the ones worth revisiting
                // weekly. A command reference changes when a release changes it.
                changefreq: priority >= 0.8 ? 'weekly' : 'monthly',
              };
            });
          },
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: [
    [
      // Offline search: indexed at build time, shipped with the artifact.
      // No Algolia account, no crawler, no API key in a public repo.
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        indexBlog: false,
        // Must track docs.routeBasePath above. The default assumes '/docs'
        // and silently indexes nothing when it is wrong.
        docsRouteBasePath: '/',
        highlightSearchTermsOnTargetPage: true,
        searchResultLimits: 8,
        // `indexPages: true` is deliberately absent: it cannot work here. The
        // plugin classifies a route as a doc when it sits under
        // docsRouteBasePath, and '/' is the parent of everything
        // (processDocInfos.js: `parentRoute === "" || ...`), so the landing page
        // is treated as a doc, found in no version's doc list, and dropped
        // before the pages branch is ever reached. The offline index therefore
        // covers the docs only; the landing page is indexed by search engines,
        // through the sitemap and the metadata below.
      },
    ],
  ],

  // Structured data for the site root, so a crawler gets the project's identity
  // and its feature list without having to infer them from the prose.
  headTags: [
    // The legacy favicon, beside the SVG the `favicon` option emits: older
    // consumers pick the .ico, everything current prefers the vector.
    {tagName: 'link', attributes: {rel: 'icon', href: '/favicon.ico', sizes: '32x32'}},
    {
      tagName: 'script',
      attributes: {type: 'application/ld+json'},
      innerHTML: JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'SoftwareSourceCode',
        name: 'dispat',
        alternateName: TITLE,
        description: DESCRIPTION,
        url: `${SITE}/`,
        codeRepository: GITHUB,
        programmingLanguage: 'Go',
        license: 'https://opensource.org/licenses/MIT',
        author: {'@type': 'Person', name: 'yohimik', url: 'https://github.com/yohimik'},
        about: SUBJECTS,
        keywords: KEYWORDS,
      }),
    },
    {
      // The same project stated as an application rather than as source, which
      // is the type a search engine reads a "free developer tool" answer out of.
      tagName: 'script',
      attributes: {type: 'application/ld+json'},
      innerHTML: JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'SoftwareApplication',
        name: 'dispat',
        alternateName: TITLE,
        description: DESCRIPTION,
        url: `${SITE}/`,
        applicationCategory: 'DeveloperApplication',
        operatingSystem: 'Linux, macOS, Windows',
        softwareRequirements: 'git',
        license: 'https://opensource.org/licenses/MIT',
        author: {'@type': 'Person', name: 'yohimik', url: 'https://github.com/yohimik'},
        offers: {'@type': 'Offer', price: '0', priceCurrency: 'USD'},
        about: SUBJECTS,
        keywords: KEYWORDS,
      }),
    },
  ],

  themeConfig: {
    // Dark for everyone, on every first visit. respectPrefersColorScheme is
    // off on purpose: with it on, defaultMode only applies to visitors whose
    // OS says nothing, so the site would still open light on a light desktop.
    // The toggle stays, and a visitor's own choice is remembered as before.
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: false,
    },
    image: 'logo.png',
    metadata: [
      {name: 'description', content: DESCRIPTION},
      {
        name: 'keywords',
        content:
          `${KEYWORDS.join(', ')}, git tags, github releases, nuget, pypi, rubygems, pub, ios, android, ` +
          'npm workspaces, pnpm workspaces, git submodules',
      },
      // og:title, og:description and og:image are emitted per page as well,
      // from the page's own title, description and themeConfig.image, and the
      // page's own win where they differ. These are the site-wide defaults, so
      // a share of a page that states none of them still carries the project's
      // own name, sentence and mark rather than nothing.
      {property: 'og:type', content: 'website'},
      {property: 'og:site_name', content: 'dispat'},
      {property: 'og:title', content: TITLE},
      {property: 'og:description', content: DESCRIPTION},
      {property: 'og:image', content: `${SITE}/logo.png`},
      {property: 'og:image:alt', content: 'dispat logo'},
      // twitter:title and twitter:description have no per-page emitter, so
      // without these a card falls back to the og:* pair above, which says the
      // same thing. The card type is `summary` because the image is the square
      // logo, and that is what `summary` is for: `summary_large_image` reserves
      // a 1.91:1 banner and crops or letterboxes anything that is not one.
      // Drawing a 1200x630 card and pointing themeConfig.image at it is what
      // would earn the larger type.
      {name: 'twitter:card', content: 'summary'},
      {name: 'twitter:title', content: TITLE},
      {name: 'twitter:description', content: DESCRIPTION},
      {name: 'twitter:image', content: `${SITE}/logo.png`},
      {name: 'twitter:image:alt', content: 'dispat logo'},
    ],
    navbar: {
      title: 'dispat',
      // The black mark in both themes, no srcDark switching, by choice: one
      // variant everywhere over per-theme swaps.
      logo: {alt: 'dispat logo', src: 'logo.svg'},
      // Two sidebars, two items. Docs is what a reader works through, API is
      // what a reader looks something up in; see the comment in sidebars.ts.
      // A docSidebar item lands on its sidebar's first page, so Docs opens on
      // Getting started and API on the API overview.
      items: [
        {type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs'},
        {type: 'docSidebar', sidebarId: 'api', position: 'left', label: 'API'},
        // The way into the older lines: without this item the versioned docs
        // build but nothing on the page reaches them.
        {type: 'docsVersionDropdown', position: 'right', dropdownActiveClassDisabled: true},
        {href: GITHUB, label: 'GitHub', position: 'right'},
        {href: DISCORD, label: 'Discord', position: 'right'},
      ],
    },
    footer: {
      style: 'dark',
      links: [
        // The columns follow the two sidebars, so the footer answers the same
        // question the navbar does: work through it, or look something up in it.
        {
          title: 'Docs',
          items: [
            {label: 'Getting started', to: '/getting-started'},
            {label: 'Concepts', to: '/concepts'},
            {label: 'Examples', to: '/examples'},
            {label: 'Releasing', to: '/reference/releasing/versioning'},
            {label: 'dispat in CI', to: '/reference/ci'},
            {label: 'Internals', to: '/internals/architecture'},
          ],
        },
        {
          title: 'API',
          items: [
            {label: 'Overview', to: '/api'},
            {label: 'CLI', to: '/cli'},
            {label: 'Configuration', to: '/configuration'},
            {label: 'Go packages', to: '/go'},
            {label: 'Commit messages', to: '/reference/commits'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: GITHUB},
            {label: 'Discord', href: DISCORD},
            {label: 'Releases', href: `${GITHUB}/releases`},
            {label: 'License', href: `${GITHUB}/blob/main/LICENSE`},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} yohimik. MIT licensed.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      // sh/json/go/yaml ship with prism-react-renderer. `console` is neither a
      // Prism language nor an alias, so it is aliased onto shell-session in
      // src/theme/prism-include-languages.js instead of listed here.
      // powershell is here for the Windows install command, which the landing
      // page and Getting started both show; without it those blocks render
      // with no highlighting at all beside their highlighted sh counterpart.
      additionalLanguages: ['docker', 'regex', 'abnf', 'shell-session', 'ini', 'diff', 'powershell'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
