import type * as Preset from '@docusaurus/preset-classic';
import type {Config} from '@docusaurus/types';
import {themes as prismThemes} from 'prism-react-renderer';

const GITHUB = 'https://github.com/yohimik/dispat';

const config: Config = {
  title: 'dispat',
  tagline: 'Releases monorepos: conventional commits in, ordered parallel publishes out',
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
      },
    ],
  ],

  themeConfig: {
    image: 'logo.png',
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
            {label: 'ccme parser', href: `${GITHUB}/tree/main/pkg/ccme`},
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
