import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// The docs pages carry no frontmatter: they stay plain markdown so they read
// correctly on GitHub too. Ordering and labels therefore live here, and the
// order mirrors the table in services/dispat/README.md so the two agree.
//
// Doc ids are extensionless paths, which is why the configuration index is
// `configuration/README` (its permalink is /configuration).
const sidebars: SidebarsConfig = {
  docs: [
    'getting-started',
    'cookbook',
    'concepts',
    {type: 'doc', id: 'cli', label: 'CLI'},
    {
      type: 'category',
      label: 'Configuration',
      collapsed: false,
      link: {type: 'doc', id: 'configuration/README'},
      items: [
        'configuration/spaces',
        'configuration/packages',
        'configuration/versions',
        'configuration/records',
        'configuration/parser',
      ],
    },
    {type: 'doc', id: 'commits', label: 'Commit messages'},
    {type: 'doc', id: 'environment', label: 'Script environment'},
    'architecture',
    'coverage',
  ],
};

export default sidebars;
