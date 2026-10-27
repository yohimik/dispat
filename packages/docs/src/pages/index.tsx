import Link from '@docusaurus/Link';
import {usePluginData} from '@docusaurus/useGlobalData';
import {README_PLUGIN} from '@site/plugins/readme/name';
import type {Argument, ReadmeData} from '@site/plugins/readme/types';
import {DemoCarousel} from '@site/src/components/DemoCarousel';
import {DispatTheorem} from '@site/src/components/DispatTheorem';
import {DownloadCounter} from '@site/src/components/DownloadCounter';
import {Inlines} from '@site/src/components/Inline';
import {DESCRIPTION, PAGE_TITLE, TAGLINE_NOTE} from '@site/src/site-metadata';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import Layout from '@theme/Layout';
import React from 'react';

import styles from './index.module.css';

// README prose, demos, project examples and inspiration remain build-time data.
// This page supplies the editorial layout and reading path. Internal links use
// Docusaurus Link so the versioned documentation and build-time checks still work.

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

const SECTION_NUMBERS: Record<string, string> = {
  demos: '01', workflows: '02', projects: '03', install: '04',
  documentation: '05', libraries: '06', inspiration: '07', community: '08',
};

function Section({chip, title, id, children}: {chip: string; title: string; id: string; children: React.ReactNode}): React.ReactElement {
  return (
    <section className={styles.section} aria-labelledby={id}>
      <div className={styles.shell}>
        <div className={styles.sectionHeading}>
          <p className={styles.chip}><span>{SECTION_NUMBERS[id]}</span> / {chip}</p>
          <Heading as="h2" id={id} className={styles.sectionTitle}>{title}</Heading>
        </div>
        <div className={styles.sectionBody}>{children}</div>
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
  return (
    <header className={styles.hero}>
      <div className={styles.shell}>
        <div className={styles.heroMasthead}>
          <p>dispat <span>/</span> release orchestration</p>
          <Link to={`${GITHUB}/blob/main/LICENSE`}>Open source <span aria-hidden="true">↗</span></Link>
        </div>
        <div className={styles.heroGrid}>
          <div>
            <Heading as="h1" className={styles.title}>Many packages.<br /><em>One clear plan.</em></Heading>
            <p className={styles.lead}>
              <strong>dispat</strong> turns conventional commits into versions, then builds and publishes changed
              packages in dependency order. One package, a monorepo, or several repositories.
            </p>
            <div className={styles.buttons}>
              <Link className={`button ${styles.installButton}`} to="/getting-started">Get started <span aria-hidden="true">↗</span></Link>
            </div>
            <p className={styles.heroNote}>{TAGLINE_NOTE}</p>
          </div>
          <DispatTheorem />
        </div>
        <div className={styles.heroFootnote}>
          <span>A Go binary. Your shell commands. Your Git history.</span>
          <span>One plan, many independent tasks.</span>
        </div>
      </div>
    </header>
  );
}

function Demos(): React.ReactElement {
  const {cli} = useReadme();
  return (
    <Section id="demos" chip="in practice" title="From a commit to a release.">
      <p className={styles.sectionLead}>
        Choose an example to see how dispat selects packages, orders their work, and handles failures.
      </p>
      <DemoCarousel features={cli.features} />
    </Section>
  );
}

function Workflows(): React.ReactElement {
  return (
    <Section id="workflows" chip="the working parts" title="Your tools stay your tools.">
      <p className={styles.sectionLead}>{PAGE_TITLE}.</p>
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" id="release-across-languages">Release across languages</Heading>
          <p>Build a Go service, publish an npm library, and push a Docker image in one run. dispat calls the shell commands you configure for each package.</p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="dependency-order">Put dependencies in order</Heading>
          <p>A package waits for the packages it needs. Independent packages can build and publish in parallel. Preview the selected packages and next versions with <code>dispat status</code>.</p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="multiple-repositories">Use one or several repositories</Heading>
          <p>Start with a single package or a monorepo. For work spread across repositories, link them as Git submodules in a <Link to="/control-repository">control repository</Link>: it can read each linked repository&apos;s own commits and tags, or hold every release decision in its own history.</p>
        </div>
      </div>
      <div className={styles.recoveryNote}>
        <Heading as="h3" id="recovery">Know what to do when a release stops</Heading>
        <p>dispat writes a Git tag after each successful publish. A package that fails leaves those publications in place, its consumers are held back rather than published from a stale plan, and the next run re-plans the remaining work from the tags. Uploads are never rolled back for you: if a publisher succeeded before its tag was written, check that destination before retrying. <Link to="/reference/releasing/recovery">Read the recovery guide</Link>.</p>
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
    <Section id="libraries" chip="take what you need" title="Useful pieces, on their own.">
      <p className={styles.sectionLead}>
        Use dispat's Go libraries in your own tools. Parse commit messages, inspect dependencies, or update manifest
        versions without running the CLI. The reader and writer share format definitions through{' '}
        <Link to="/go/manifest"><code>pkg/manifest</code></Link>.
      </p>
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" id="ccme-library">
            <Link to="/go/ccme">
              <code>pkg/ccme</code>
            </Link>
            : the commit parser
          </Heading>
          <p>
            Parse Conventional Commits and dispat's package scopes, dependency propagation, and prerelease channels.
            The parser scans the input once. Its formal rules live in{' '}
            <Link to={`${GITHUB}/blob/main/specs/ccme-spec/SPEC.md`}><code>SPEC.md</code></Link>.
          </p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" id="scanner-library">
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
          <Heading as="h3" id="writer-library">
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
      <details className={styles.disclosure}>
        <summary id="supported-manifests">The full list of supported languages and manifests <span>{MANIFESTS.length} ecosystems</span></summary>
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
      </details>
    </Section>
  );
}

function Install(): React.ReactElement {
  return (
    <div className={styles.installSection}>
      <Section id="install" chip="get started" title="Try it in your repository.">
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
    <Section id="projects" chip="used here" title="This project ships itself.">
      <p className={styles.sectionLead}>
        The first monorepo dispat releases is its own: every tag, changelog, GitHub release and container image of this
        project, and this documentation site, ship through a dispat run.
      </p>
      <div className={styles.projectMeta}><Link to={`${GITHUB}/actions`}>Follow the release runs <span aria-hidden="true">↗</span></Link><DownloadCounter /></div>
      <details className={styles.disclosure}>
        <summary>Projects using dispat <span>From the repository README</span></summary>
        <div className={styles.projects}>
          {repository.users.map((user, i) => (
            <div className={styles.feature} key={i}>
              <p><Inlines tokens={user} /></p>
            </div>
          ))}
        </div>
      </details>
    </Section>
  );
}

function Reference(): React.ReactElement {
  return (
    <Section id="documentation" chip="the manual" title="A place for every question.">
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
          <Link to="/monorepo">One repository or many</Link> and <Link to="/control-repository">A control
          repository</Link>: what each layout costs, and the two ways several repositories release from one plan.
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
    <Section id="inspiration" chip="standing on shoulders" title="Good ideas have a history.">
      {/* The repository README's section of the same name, read at build time
          rather than kept in step by hand. Its relative link to pkg/ccme comes
          back as a GitHub URL; see plugins/readme/inline.ts. */}
      <p className={styles.sectionLead}>Git, Unix tools, dependency graphs, and database recovery. dispat brings familiar ideas together around the work of releasing software.</p>
      <details className={styles.disclosure}>
        <summary>The ideas behind dispat <span>A few acknowledgements</span></summary>
        <Argued argument={repository.inspiration} className={styles.reference} />
      </details>
    </Section>
  );
}

function Community(): React.ReactElement {
  return (
    <Section id="community" chip="say hello" title="Made in the open.">
      <p className={styles.sectionLead}>
        Want to share a project you release with dispat? Come and say hello on Discord. Bugs and feature requests are
        welcome as GitHub issues too, whichever suits you better.
      </p>
      <div className={styles.buttons}>
        <Link className={styles.textLink} href="https://discord.gg/83PwVSCCmk">
          Join the Discord
        </Link>
        <Link className={styles.textLink} href="https://github.com/yohimik/dispat/issues">
          Open an issue
        </Link>
      </div>
    </Section>
  );
}

export default function Home(): React.ReactElement {
  return (
    <Layout title={PAGE_TITLE} description={DESCRIPTION}>
      <main className={styles.page}>
        <Hero />
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
