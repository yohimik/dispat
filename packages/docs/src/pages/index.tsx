import Link from '@docusaurus/Link';
import {usePluginData} from '@docusaurus/useGlobalData';
import {README_PLUGIN} from '@site/plugins/readme/name';
import type {Argument, ReadmeData} from '@site/plugins/readme/types';
import DemoCarousel from '@site/src/components/DemoCarousel';
import Inlines from '@site/src/components/Inline';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import Layout from '@theme/Layout';
import React from 'react';

import styles from './index.module.css';

// The docs own the root (docs.routeBasePath === '/'), so this page is the site
// entry point: dispat.dev/, the URL every README and every search
// result points at. It carries the same claims as the two READMEs, because a
// redirect gave crawlers and readers nothing to land on.
//
// Those claims are *read* from the READMEs at build time rather than restated
// here: the opening and the inspiration list from the repository README, the
// key-feature slides from the CLI one; the "why one more monorepo tool?"
// argument is drawn as the hero deck's opening slide. They used to be a
// second copy under a comment
// promising to keep them in step, which is not a promise a comment can keep.
// See plugins/readme. What is still written here is the landing page's own:
// the badges, the install blocks, the reading list and the invitation.
//
// Three strings here do restate something written elsewhere, and are the
// whole of what a rewrite has to keep in step by hand:
//   - MANIFESTS mirrors the format tables in pkg/scanner's README;
//   - the three INSTALL_* commands repeat the repository README's install
//     blocks and Getting started's;
//   - <Layout> title and description restate the tagline and description in
//     docusaurus.config.ts.
//
// Every internal link goes through <Link> (or useBaseUrl for assets), never a
// raw path: Docusaurus mounts its router without a `basename` and registers
// routes with baseUrl already in them, so a bare "/getting-started" resolves
// outside the site and hits the 404 page.

const GITHUB = 'https://github.com/yohimik/dispat';

/** The two READMEs, parsed at build time. */
function useReadme(): ReadmeData {
  return usePluginData(README_PLUGIN) as ReadmeData;
}

/**
 * A README paragraph, its list, and the paragraph closing it: the shape of
 * both arguments the repository README makes.
 */
function Argued({argument, className}: {argument: Argument; className: string}): React.ReactElement {
  const List = argument.ordered ? 'ol' : 'ul';
  return (
    <>
      <p className={styles.sectionLead}>
        <Inlines tokens={argument.intro} />
      </p>
      <List className={className}>
        {argument.items.map((item, i) => (
          // The list comes from a file read at build time and never reorders.
          // eslint-disable-next-line react/no-array-index-key
          <li key={i}>
            <Inlines tokens={item} />
          </li>
        ))}
      </List>
    </>
  );
}

/**
 * A landing page section in the deck's own language: a hairline above, a
 * small uppercase chip naming the territory, and a mono title, the same
 * shapes the hero's slides and the clips' chips use.
 */
function Section({chip, title, id, children}: {chip: string; title: string; id: string; children: React.ReactNode}): React.ReactElement {
  return (
    <section className={styles.section}>
      <div className="container">
        <p className={styles.chip}>{chip}</p>
        <Heading as="h2" id={id} className={styles.sectionTitle}>
          {title}
        </Heading>
        {children}
      </div>
    </section>
  );
}

// One command per block, and the platform in the block's title rather than in
// a leading comment: the copy button then hands over exactly what you run, and
// a reader copying the Linux block never carries the wget alternative with it.
const INSTALL_CURL = 'curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh';
const INSTALL_WINDOWS = 'irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex';

function Hero(): React.ReactElement {
  const {repository} = useReadme();
  return (
    <header className={styles.hero}>
      <div className="container">
        <Heading as="h1" className={styles.title}>
          Release your packages together,<br className={styles.desktopBreak} /> across languages.
        </Heading>
        {repository.lead.slice(0, 2).map((paragraph, i) => (
          <p className={styles.lead} key={i}><Inlines tokens={paragraph} /></p>
        ))}
        <div className={styles.buttons}>
          <Link className="button button--primary button--lg" to="#install">Install Dispat</Link>
          <Link className="button button--secondary button--lg" to="/getting-started">Start your first release</Link>
        </div>
        <p className={styles.heroNote}>One binary. Your build commands. A release plan you can inspect first.</p>
      </div>
    </header>
  );
}

function Demos(): React.ReactElement {
  const {cli} = useReadme();
  return (
    <Section id="demos" chip="see it work" title="Follow a release from commit to publish">
      <p className={styles.sectionLead}>
        Choose an example to see how Dispat selects packages, orders their work, and handles failures.
      </p>
      <DemoCarousel features={cli.features} />
    </Section>
  );
}

function Workflows(): React.ReactElement {
  return (
    <Section id="workflows" chip="built for your project" title="Keep the tools your team already uses">
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" id="release-across-languages">Release across languages</Heading>
          <p>Build a Go service, publish an npm library, and push a Docker image in one run. Dispat calls the shell commands you configure for each package.</p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="dependency-order">Put dependencies in order</Heading>
          <p>A package waits for the packages it needs. Independent packages can build and publish in parallel. Preview the selected packages and next versions with <code>dispat status</code>.</p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="multiple-repositories">Use one or several repositories</Heading>
          <p>Start with a single package or a monorepo. For work spread across repositories, bring them together with Git submodules in a <Link to="/control-repository">control repository</Link>.</p>
        </div>
      </div>
      <div className={styles.recoveryNote}>
        <Heading as="h3" id="recovery">Know what to do when a release stops</Heading>
        <p>Dispat records successful publishes with Git tags. A later run uses those records to find unfinished work. If a publisher succeeded before its tag was written, check the destination before retrying. <Link to="/reference/releasing/recovery">Read the recovery guide</Link>.</p>
      </div>
    </Section>
  );
}

// One row per ecosystem, mirroring the tables in pkg/scanner's README. The
// writer covers every manifest the reader does, so the list serves both.
const MANIFESTS: [language: string, files: string][] = [
  ['JavaScript, TypeScript: npm, pnpm, Yarn', 'package.json'],
  ['Go', 'go.mod'],
  ['Rust: Cargo', 'Cargo.toml'],
  ['Python: PEP 621, PEP 735, Poetry, pip', 'pyproject.toml, requirements*.txt'],
  ['PHP: Composer', 'composer.json'],
  ['Java, Kotlin, Scala: Maven', 'pom.xml'],
  ['C#, F#, VB: .NET, NuGet', '*.csproj, *.fsproj, *.vbproj, *.nuspec, Directory.Packages.props, packages.config'],
  ['Dart, Flutter: pub', 'pubspec.yaml'],
  ['Ruby: Bundler, RubyGems', 'Gemfile, *.gemspec'],
  ['Swift, Objective-C: iOS, CocoaPods', 'Info.plist, project.pbxproj, Podfile, *.podspec'],
  ['Kotlin, Java: Android, Gradle', 'AndroidManifest.xml, libs.versions.toml, build.gradle(.kts)'],
  ['Docker: images and Compose', 'Dockerfile, Containerfile, compose.yaml, docker-compose.yml, and their .override spellings'],
  ['Unity', 'Packages/manifest.json, ProjectSettings/ProjectSettings.asset'],
  ['Godot', 'project.godot, plugin.cfg, export_presets.cfg'],
  ['Unreal Engine', '*.uproject, *.uplugin, Config/DefaultGame.ini, Config/DefaultEngine.ini'],
  ['Defold', 'game.project'],
  ['O3DE', 'project.json, gem.json'],
  ['Aqua tool management', 'aqua.yaml, aqua.yml, hidden and directory variants'],
];

// dispat's pieces are separate Go modules, usable with no dispat in sight. Each
// card links to its page under the API sidebar, which is where the surface and
// the guarantees are written down; those pages link on to pkg.go.dev.
function Libraries(): React.ReactElement {
  return (
    <Section id="libraries" chip="libraries" title="Lightweight libraries, usable on their own">
      <p className={styles.sectionLead}>
        Use Dispat's Go libraries in your own tools. Parse commit messages, inspect dependencies, or update manifest
        versions without running the CLI. The reader and writer share format definitions through{' '}
        <Link to="/go/manifest"><code>pkg/manifest</code></Link>.
      </p>
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" id="ccme-library" className={styles.featureTitle}>
            <Link to="/go/ccme">
              <code>pkg/ccme</code>
            </Link>
            : the commit parser
          </Heading>
          <p>
            Parse Conventional Commits and Dispat's package scopes, dependency propagation, and prerelease channels.
            The parser scans the input once. Its formal rules live in{' '}
            <Link to={`${GITHUB}/blob/main/specs/ccme-spec/SPEC.md`}><code>SPEC.md</code></Link>.
          </p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="scanner-library" className={styles.featureTitle}>
            <Link to="/go/scanner">
              <code>pkg/scanner</code>
            </Link>
            : the manifest reader
          </Heading>
          <p>
            Read package names, versions, and dependencies from the formats below. Results have a consistent shape
            and order. Scanning uses bounded local reads and returns valid results alongside any parsing errors.
          </p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="writer-library" className={styles.featureTitle}>
            <Link to="/go/writer">
              <code>pkg/writer</code>
            </Link>
            : the manifest writer
          </Heading>
          <p>
            Update supported version declarations while preserving surrounding formatting where the format allows it.
            Each file is validated before an atomic replacement. The result tells you which edits were applied,
            missing, or skipped because a value is inherited or calculated.
          </p>
        </div>
      </div>
      <Heading as="h3" id="supported-manifests" className={styles.tableTitle}>
        Languages and manifests the reader and the writer support
      </Heading>
      <div className={styles.manifests}>
        <table>
          <thead>
            <tr>
              <th>Language / ecosystem</th>
              <th>Manifests read and rewritten</th>
            </tr>
          </thead>
          <tbody>
            {MANIFESTS.map(([language, files]) => (
              <tr key={language}>
                <td>{language}</td>
                <td>
                  <code>{files}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <p>
          <Link to="/cli/compute"><code>dispat compute</code></Link> reads these manifests to derive package dependencies.
          Enable <Link to="/configuration/autoversion"><code>autoVersion</code></Link> to update supported declarations during
          a release. Mobile build numbers are separate from package versions; change them explicitly with{' '}
          <Link to="/cli/writer"><code>--set-build</code></Link>.
          See <Link to="/editing/manifests#aqua">Aqua manifest support</Link> for supported imports and version declarations.
        </p>
      </div>
    </Section>
  );
}

function Install(): React.ReactElement {
  return (
    <div className={styles.installSection}>
      <Section id="install" chip="get started" title="Install once. Preview your first release.">
        <p className={styles.sectionLead}>
          The installer downloads the binary for your platform and verifies its checksum. No language runtime is required.
        </p>
        <div className={styles.install}>
          <CodeBlock language="sh" title="macOS and Linux">{INSTALL_CURL}</CodeBlock>
          <CodeBlock language="powershell" title="Windows PowerShell">{INSTALL_WINDOWS}</CodeBlock>
        </div>
        <div className={styles.firstRun}>
          <div>
            <Heading as="h3" id="first-release">Start in your Git repository</Heading>
            <p>Generate a starter configuration, edit its package paths and build and publish commands, then inspect the plan. <code>status</code> leaves your project unchanged.</p>
          </div>
          <CodeBlock language="sh" title="Configure and preview">{'dispat init\n# Edit dispat.json for your packages\ndispat status'}</CodeBlock>
        </div>
        <p className={styles.installLinks}>
          <Link to="/getting-started">Installation options and setup guide</Link>
          <span aria-hidden="true"> · </span>
          <Link to="/reference/ci">GitHub Actions and other CI systems</Link>
        </p>
      </Section>
    </div>
  );
}

// The repository README's `## Projects using dispat` list, read at build time
// like every other README extraction.
function Projects(): React.ReactElement {
  const {repository} = useReadme();

  return (
    <Section id="projects" chip="in production" title="Projects using dispat">
      <p className={styles.sectionLead}>
        The first monorepo dispat releases is its own: every tag, changelog, GitHub release and container image of this
        project, and this documentation site, ship through a dispat run.
      </p>
      <div className={styles.projects}>
        {repository.users.map((user, i) => (
          <div className={styles.feature} key={i}>
            <p>
              <Inlines tokens={user} />
            </p>
          </div>
        ))}
      </div>
    </Section>
  );
}

function Reference(): React.ReactElement {
  return (
    <Section id="documentation" chip="read on" title="The documentation">
      <ul className={styles.reference}>
        <li>
          <Link to="/getting-started">Getting started</Link>: install the binary, write one config file, wire the
          release into CI.
        </li>
        <li>
          <Link to="/concepts">Concepts</Link>: packages and spaces, propagation, the plan, the failure and recovery
          model.
        </li>
        <li>
          <Link to="/examples">Examples</Link>: a complete setup per package manager, npm to Docker to Android, and{' '}
          <Link to="/editing/autowriter">editing every package at once</Link>.
        </li>
        <li>
          <Link to="/cli">CLI</Link> and <Link to="/configuration">Configuration</Link>: every command, every option.
        </li>
        <li>
          <Link to="/go">Go packages</Link>: the commit parser, the manifest reader and the manifest writer, importable
          on their own.
        </li>
        <li>
          <Link to="/reference/commits">Commit messages</Link>: the Conventional Commits superset that carries release intent.
        </li>
        <li>
          <Link to="/reference/releasing/versioning">Releasing</Link>: shared versions, running the release&apos;s own
          steps yourself, releasing part of the graph, and the release lock.
        </li>
        <li>
          <Link to="/reference/environment">Script environment</Link>: the <code>DISPAT_*</code> variables a stage receives.
        </li>
        <li>
          <Link to="/internals/architecture">Architecture</Link>, <Link to="/internals/coverage">Coverage</Link> and{' '}
          <Link to="/internals/test-results">Test results</Link>: how it is built, and what its test suite reaches and
          does.
        </li>
      </ul>
    </Section>
  );
}

// Community is last on purpose: a reader who got this far has the shape of the
// tool and is deciding whether to adopt it, which is the moment an invitation
// is worth anything.
function Inspiration(): React.ReactElement {
  const {repository} = useReadme();

  return (
    <Section id="inspiration" chip="lineage" title="Inspiration">
      {/* The repository README's section of the same name, read at build time
          rather than kept in step by hand. Its relative link to pkg/ccme comes
          back as a GitHub URL; see plugins/readme/inline.ts. */}
      <Argued argument={repository.inspiration} className={styles.reference} />
    </Section>
  );
}

function Community(): React.ReactElement {
  return (
    <Section id="community" chip="community" title="Have questions or issues?">
      <p className={styles.sectionLead}>
        Want to share a project you release with dispat? Come and say hello on Discord. Bugs and feature requests are
        welcome as GitHub issues too, whichever suits you better.
      </p>
      <div className={styles.buttons}>
        <Link className="button button--primary button--lg" href="https://discord.gg/83PwVSCCmk">
          Join the Discord
        </Link>
        <Link className="button button--secondary button--lg" href="https://github.com/yohimik/dispat/issues">
          Open an issue
        </Link>
      </div>
    </Section>
  );
}

export default function Home(): React.ReactElement {
  return (
    <Layout
      title="Release automation across languages and repositories"
      description="Dispat turns conventional commits into versions, changelogs, and ordered releases. Automate publishing across languages in one repository or several.">
      <Hero />
      <main>
        <Demos />
        <Workflows />
        <Projects />
        <Install />
        <Reference />
        <Libraries />
        <Inspiration />
        <Community />
      </main>
    </Layout>
  );
}
