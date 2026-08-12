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
    {type: 'doc', id: 'versioning', label: 'Shared versions'},
    {type: 'doc', id: 'cli', label: 'CLI'},
    {type: 'doc', id: 'steps', label: 'Release steps'},
    {type: 'doc', id: 'shell-helpers', label: 'Shell helpers'},
    {type: 'doc', id: 'partial-releases', label: 'Partial releases'},
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
    {type: 'doc', id: 'manifests', label: 'Manifest tools'},
    {type: 'doc', id: 'autoreplace', label: 'Editing across the monorepo'},
    {type: 'doc', id: 'replacer', label: 'The replacer'},
    {type: 'doc', id: 'commits', label: 'Commit messages'},
    {type: 'doc', id: 'environment', label: 'Script environment'},
    {type: 'doc', id: 'ci', label: 'dispat in CI'},
    {type: 'doc', id: 'self-update', label: 'Updating dispat'},
    'architecture',
    'coverage',
  ],
};

export default sidebars;
