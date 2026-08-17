import type {Options as PwaOptions} from '@docusaurus/plugin-pwa';
import type * as Preset from '@docusaurus/preset-classic';
import type {Config} from '@docusaurus/types';
import {themes as prismThemes} from 'prism-react-renderer';

import readme from './plugins/readme';
import testReport from './plugins/test-report';

const GITHUB = 'https://github.com/yohimik/dispat';
const DISCORD = 'https://discord.gg/83PwVSCCmk';

// One description and one keyword list, used by the meta tags and by both
// structured-data blocks below, so a crawler is never told two different things
// about the same project.
const DESCRIPTION =
  'dispat is a release tool for polyglot monorepos. It reads your conventional commits, works out every ' +
  'package version with propagation to dependants, and builds and publishes each changed package in dependency ' +
  'order, in parallel, with changelogs, git tags and GitHub releases. A package is a folder and a stage is a shell ' +
  'command, so npm, Go, Cargo, Maven, .NET, Python, Ruby, Dart, Docker, iOS and Android live in one dependency graph.';

const KEYWORDS = [
  'monorepo release tool',
  'monorepo',
  'polyglot monorepo',
  'release automation',
  'monorepo versioning',
  'semantic release monorepo',
  'conventional commits',
  'semantic versioning',
  'changelog',
  'lerna alternative',
  'npm',
  'docker',
  'go modules',
];

const config: Config = {
  title: 'dispat',
  tagline: 'The polyglot monorepo release tool: conventional commits in, ordered parallel publishes out',
  favicon: 'logo.png',

  url: 'https://yohimik.github.io',
  baseUrl: '/dispat/',
  organizationName: 'yohimik',
  projectName: 'dispat',

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
    // The two places this site states something measured rather than written.
    // Both are imported as functions rather than named by path: the config is
    // TypeScript, and importing them means they are compiled by the same
    // loader and type-checked by `pnpm typecheck` along with everything else.
    readme,
    testReport,
    [
      // Installable, and readable offline once installed. The plugin emits
      // build/sw.js, served at /dispat/sw.js, so the worker's scope is the
      // directory it sits in: it cannot touch the other projects sharing the
      // yohimik.github.io origin. None of this exists in `docusaurus start` --
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
        // href/content values carrying a file extension are baseUrl-prefixed by
        // the plugin itself, so '/manifest.json' ships as
        // '/dispat/manifest.json'. Colours and 'yes' have no extension and pass
        // through untouched. These cannot move to the headTags array below:
        // that one is emitted verbatim, and the paths would 404.
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
          // README tables link to /dispat/<page>, not /dispat/docs/<page>.
          routeBasePath: '/',
          sidebarPath: './sidebars.ts',
          editUrl: ({docPath}) =>
            `${GITHUB}/edit/main/packages/docs/docs/${docPath}`,
          editCurrentVersion: true,
          showLastUpdateTime: true,
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
        sitemap: {
          // The sitemap is the only crawl entry point a GitHub Pages *project*
          // site really has: robots.txt is served under /dispat/, which no
          // crawler reads (see static/robots.txt). lastmod is off by default.
          lastmod: 'date',
          changefreq: 'weekly',
          priority: 0.5,
          // Patterns match full route paths, baseUrl included. /search is a
          // client-side view of the index, not a page.
          ignorePatterns: ['/dispat/search'],
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
    {
      tagName: 'script',
      attributes: {type: 'application/ld+json'},
      innerHTML: JSON.stringify({
        '@context': 'https://schema.org',
        '@type': 'SoftwareSourceCode',
        name: 'dispat',
        description: DESCRIPTION,
        url: 'https://yohimik.github.io/dispat/',
        codeRepository: GITHUB,
        programmingLanguage: 'Go',
        license: 'https://opensource.org/licenses/MIT',
        author: {'@type': 'Person', name: 'yohimik', url: 'https://github.com/yohimik'},
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
        description: DESCRIPTION,
        url: 'https://yohimik.github.io/dispat/',
        applicationCategory: 'DeveloperApplication',
        operatingSystem: 'Linux, macOS, Windows',
        softwareRequirements: 'git',
        license: 'https://opensource.org/licenses/MIT',
        author: {'@type': 'Person', name: 'yohimik', url: 'https://github.com/yohimik'},
        offers: {'@type': 'Offer', price: '0', priceCurrency: 'USD'},
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
          `${KEYWORDS.join(', ')}, release orchestration, git tags, github releases, cargo, maven, nuget, pypi, ` +
          'rubygems, pub, ios, android, npm workspaces, pnpm workspaces',
      },
      // og:title/og:image/og:description come from the page title, themeConfig
      // image and each page's description. The card type is `summary` because
      // the image is the square logo, and that is what `summary` is for:
      // `summary_large_image` reserves a 1.91:1 banner and crops or letterboxes
      // anything that is not one. Drawing a 1200x630 card and pointing
      // themeConfig.image at it is what would earn the larger type.
      {property: 'og:type', content: 'website'},
      {name: 'twitter:card', content: 'summary'},
    ],
    navbar: {
      title: 'dispat',
      logo: {alt: 'dispat logo', src: 'logo.png'},
      // Two sidebars, two items. Docs is what a reader works through, API is
      // what a reader looks something up in; see the comment in sidebars.ts.
      // A docSidebar item lands on its sidebar's first page, so Docs opens on
      // Getting started and API on the API overview.
      items: [
        {type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs'},
        {type: 'docSidebar', sidebarId: 'api', position: 'left', label: 'API'},
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
