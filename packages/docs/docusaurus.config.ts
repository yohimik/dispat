import type * as Preset from '@docusaurus/preset-classic';
import type {Config} from '@docusaurus/types';
import {themes as prismThemes} from 'prism-react-renderer';

const GITHUB = 'https://github.com/yohimik/dispat';

const config: Config = {
  title: 'dispat',
  tagline: 'Release orchestration for polyglot monorepos: conventional commits in, ordered parallel publishes out',
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
        description:
          'A release manager for polyglot monorepos: reads conventional commits, computes semantic versions with ' +
          'propagation to dependants, and builds and publishes packages in dependency order, in parallel, with ' +
          'changelogs, git tags and GitHub releases. Packages are folders and stages are shell commands, so npm, Go, ' +
          'Cargo, Maven, .NET, Python, Ruby, Dart, Docker, iOS and Android live in one dependency graph.',
        url: 'https://yohimik.github.io/dispat/',
        codeRepository: GITHUB,
        programmingLanguage: 'Go',
        license: 'https://opensource.org/licenses/MIT',
        author: {'@type': 'Person', name: 'yohimik', url: 'https://github.com/yohimik'},
        keywords: [
          'monorepo',
          'polyglot monorepo',
          'release automation',
          'release orchestration',
          'conventional commits',
          'semantic versioning',
          'changelog',
          'npm',
          'docker',
          'go modules',
        ],
      }),
    },
  ],

  themeConfig: {
    image: 'logo.png',
    metadata: [
      {
        name: 'description',
        content:
          'dispat is release orchestration for polyglot monorepos: conventional commits in, ordered parallel ' +
          'publishes out, with changelogs, git tags and GitHub releases.',
      },
      {
        name: 'keywords',
        content:
          'monorepo, polyglot monorepo, release, release orchestration, conventional commits, semantic versioning, ' +
          'changelog, git tags, github releases, npm, docker, go modules, cargo, maven, nuget, pypi, rubygems, ' +
          'pub, ios, android, lerna alternative',
      },
      // og:title/og:image/og:description come from the page title, themeConfig
      // image and each page's description. A 1200x630 social card would render
      // better than the square logo; imgs/ has only the logo today.
      {property: 'og:type', content: 'website'},
      {name: 'twitter:card', content: 'summary_large_image'},
    ],
    navbar: {
      title: 'dispat',
      logo: {alt: 'dispat logo', src: 'logo.png'},
      items: [
        {type: 'docSidebar', sidebarId: 'docs', position: 'left', label: 'Docs'},
        {type: 'docsVersionDropdown', position: 'right'},
        {href: GITHUB, label: 'GitHub', position: 'right'},
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Getting started', to: '/getting-started'},
            {label: 'Cookbook', to: '/cookbook'},
            {label: 'CLI', to: '/cli'},
            {label: 'Configuration', to: '/configuration'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: GITHUB},
            {label: 'Releases', href: `${GITHUB}/releases`},
            // The Go modules are listed on the landing page, beside the
            // manifest reader and writer, rather than one of them here.
            {label: 'License', href: `${GITHUB}/blob/main/LICENSE.md`},
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
      additionalLanguages: ['docker', 'regex', 'abnf', 'shell-session', 'ini', 'diff'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
