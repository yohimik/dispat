import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// The docs pages carry no frontmatter: they stay plain markdown so they read
// correctly on GitHub too. Ordering and labels therefore live here, and the
// order mirrors the table in services/dispat/README.md so the two agree.
//
// Doc ids are extensionless paths, which is why the configuration index is
// `configuration/README` (its permalink is /configuration). Every folder is a
// category: the reading path stays at the root, everything else is grouped by
// what it is about, and the CLI category carries one page per command.
const sidebars: SidebarsConfig = {
  docs: [
    'getting-started',
    'cookbook',
    'concepts',
    {
      type: 'category',
      label: 'Releasing',
      collapsed: true,
      items: [
        {type: 'doc', id: 'releasing/versioning', label: 'Shared versions'},
        {type: 'doc', id: 'releasing/steps', label: 'Release steps'},
        {type: 'doc', id: 'releasing/partial-releases', label: 'Partial releases'},
        {type: 'doc', id: 'releasing/release-lock', label: 'The release lock'},
      ],
    },
    {
      type: 'category',
      label: 'CLI',
      collapsed: true,
      link: {type: 'doc', id: 'cli/README'},
      items: [
        {type: 'doc', id: 'cli/release', label: 'release'},
        {type: 'doc', id: 'cli/status', label: 'status'},
        {type: 'doc', id: 'cli/run', label: 'run'},
        {type: 'doc', id: 'cli/init', label: 'init'},
        {type: 'doc', id: 'cli/preview', label: 'preview'},
        {type: 'doc', id: 'cli/changelog', label: 'changelog'},
        {type: 'doc', id: 'cli/autoversion', label: 'autoversion'},
        {type: 'doc', id: 'cli/autowriter', label: 'autowriter'},
        {type: 'doc', id: 'cli/autosubstitute', label: 'autosubstitute'},
        {type: 'doc', id: 'cli/commit', label: 'commit'},
        {type: 'doc', id: 'cli/github', label: 'github'},
        {type: 'doc', id: 'cli/compute', label: 'compute'},
        {type: 'doc', id: 'cli/if', label: 'if'},
        {type: 'doc', id: 'cli/exec', label: 'exec'},
        {type: 'doc', id: 'cli/self-update', label: 'self-update'},
        {type: 'doc', id: 'cli/scanner', label: 'scanner'},
        {type: 'doc', id: 'cli/writer', label: 'writer'},
        {type: 'doc', id: 'cli/replacer', label: 'replacer'},
      ],
    },
    {
      type: 'category',
      label: 'Configuration',
      collapsed: false,
      link: {type: 'doc', id: 'configuration/README'},
      items: [
        'configuration/spaces',
        'configuration/packages',
        'configuration/change-scope',
        'configuration/versions',
        'configuration/alias-tags',
        'configuration/records',
        'configuration/parser',
        {type: 'doc', id: 'configuration/dependencies', label: 'dependencies'},
        {type: 'doc', id: 'configuration/scripts', label: 'Script sequences'},
        {type: 'doc', id: 'configuration/run-hooks', label: 'Run-level hooks'},
        {type: 'doc', id: 'configuration/env', label: 'Static env'},
        {type: 'doc', id: 'configuration/custom', label: 'custom'},
      ],
    },
    {
      type: 'category',
      label: 'Editing the monorepo',
      collapsed: true,
      items: [
        {type: 'doc', id: 'editing/manifests', label: 'Manifest tools'},
        {type: 'doc', id: 'editing/autowriter', label: 'Editing across the monorepo'},
        {type: 'doc', id: 'editing/autosubstitute', label: 'Substituting across the monorepo'},
        {type: 'doc', id: 'editing/replacer', label: 'The replacer'},
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      collapsed: true,
      items: [
        {type: 'doc', id: 'reference/commits', label: 'Commit messages'},
        {type: 'doc', id: 'reference/environment', label: 'Script environment'},
        {type: 'doc', id: 'reference/ci', label: 'dispat in CI'},
        {type: 'doc', id: 'reference/self-update', label: 'Updating dispat'},
      ],
    },
    {
      type: 'category',
      label: 'Internals',
      collapsed: true,
      items: [
        {type: 'doc', id: 'internals/architecture', label: 'Architecture'},
        {type: 'doc', id: 'internals/coverage', label: 'Test coverage'},
      ],
    },
  ],
};

export default sidebars;
