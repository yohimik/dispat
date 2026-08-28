import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
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
function Section({chip, title, children}: {chip: string; title: string; children: React.ReactNode}): React.ReactElement {
  return (
    <section className={styles.section}>
      <div className="container">
        <p className={styles.chip}>{chip}</p>
        <Heading as="h2" className={styles.sectionTitle}>
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
const INSTALL_WGET = 'wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh';
const INSTALL_WINDOWS = 'irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex';

function Hero(): React.ReactElement {
  const {siteConfig} = useDocusaurusContext();
  const {repository, cli} = useReadme();

  return (
    <header className={styles.hero}>
      <div className="container">
        <img
          className={styles.logo}
          src={useBaseUrl('/logo.svg')}
          alt="dispat logo"
          width={128}
          height={128}
        />
        <Heading as="h1" className={styles.title}>
          {siteConfig.title}
        </Heading>
        {/* No tagline line here, deliberately: the repository README's opening
            paragraph below says what the tagline says, in more words, and one
            under the other read as a stutter. siteConfig.tagline is still the
            navbar's, the page <title>'s and the meta description's. */}
        {/* The same two badges the repository README carries: the tests workflow
            and the coverage endpoint the coverage job publishes to the badges
            branch. The coverage one links to the page that explains the number
            rather than back to the workflow. */}
        <div className={styles.badges}>
          <Link to={`${GITHUB}/actions/workflows/tests.yml`}>
            <img src={`${GITHUB}/actions/workflows/tests.yml/badge.svg`} alt="tests workflow status" height={20} />
          </Link>
          <Link to="/internals/coverage">
            <img
              src="https://img.shields.io/endpoint?style=flat&url=https%3A%2F%2Fraw.githubusercontent.com%2Fyohimik%2Fdispat%2Fbadges%2Fcoverage.json"
              alt="statement coverage"
              height={20}
            />
          </Link>
        </div>
        {/* The repository README's opening, up to its install commands. */}
        {repository.lead.map((paragraph, i) => (
          // Read from a file at build time; the order never changes.
          // eslint-disable-next-line react/no-array-index-key
          <p className={styles.lead} key={i}>
            <Inlines tokens={paragraph} />
          </p>
        ))}
        <div className={styles.buttons}>
          <Link className="button button--primary button--lg" to="/getting-started">
            Get started
          </Link>
          <Link className="button button--secondary button--lg" to="/examples">
            Examples
          </Link>
        </div>
        {/* The CLI README's key features as a deck: one slide per bullet,
            each an animated illustration of its claim with the README's own
            words in a band underneath. The deck replaced the terminal
            transcript and the feature-card grid that used to sit on this
            page: the pretty log's lines play inside the animations, and the
            feature bullets are still read from the README at build time, so
            the evidence moved into the deck rather than out of the page. */}
        <DemoCarousel features={cli.features} />
      </div>
    </header>
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
];

// dispat's pieces are separate Go modules, usable with no dispat in sight. Each
// card links to its page under the API sidebar, which is where the surface and
// the guarantees are written down; those pages link on to pkg.go.dev.
function Libraries(): React.ReactElement {
  return (
    <Section chip="libraries" title="Lightweight libraries, usable on their own">
      <p className={styles.sectionLead}>
        Parsing commit messages and reading and rewriting dependency manifests are problems far older than releases, so
        dispat keeps all three as standalone Go modules with no dependency on the CLI, on git or on a network. The
        manifest pair shares its vocabulary through{' '}
        <Link to="/go/manifest">
          <code>pkg/manifest</code>
        </Link>{' '}
        (dependency kinds, manifest file-name rules, PEP 503 normalisation) so the reader and the writer can never
        drift apart.
      </p>
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" className={styles.featureTitle}>
            <Link to="/go/ccme">
              <code>pkg/ccme</code>
            </Link>
            : the commit parser
          </Heading>
          <p>
            Conventional Commits, Monorepo Extension: a strict superset of Conventional Commits 1.0.0 that adds scopes as
            packages, propagation depth and prerelease channels. No regular expressions: one left-to-right index scan
            with a byte of lookahead, no backtracking, no recursion, O(n) time and O(1) working space, which is what
            matters when the input is untrusted commit messages in CI. The specification is vendored beside it as{' '}
            <Link to={`${GITHUB}/blob/main/pkg/ccme/SPEC.md`}>
              <code>SPEC.md</code>
            </Link>
            , and every section reference in the code points into it.
          </p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" className={styles.featureTitle}>
            <Link to="/go/scanner">
              <code>pkg/scanner</code>
            </Link>
            : the manifest reader
          </Heading>
          <p>
            Thin per-format parsers turning every manifest below into one ecosystem-neutral shape: declared identity,
            dependencies, ranges and local-path signals. No SBOM machinery, no lockfile resolution, no network; bounded
            reads, deterministic order, and a partial result even when one file fails to parse.
          </p>
        </div>
        <div className={styles.feature}>
          <Heading as="h3" className={styles.featureTitle}>
            <Link to="/go/writer">
              <code>pkg/writer</code>
            </Link>
            : the manifest writer
          </Heading>
          <p>
            Format-preserving in-place edits for every manifest the scanner reads: only the version text being changed
            is replaced, and every other byte (indentation, key order, comments) survives verbatim. Writes are atomic
            (temp file, fsync, rename) and skipped when nothing changed, and the result separates what was applied from
            what was deliberately left alone, such as a value that defers to a Maven property or a workspace
            inheritance.
          </p>
        </div>
      </div>
      <Heading as="h3" className={styles.tableTitle}>
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
          The mobile formats also carry a build number beside their marketing version (<code>CFBundleVersion</code>,{' '}
          <code>android:versionCode</code>, <code>CURRENT_PROJECT_VERSION</code>): the scanner reads it, no version
          write ever moves it, and{' '}
          <Link to="/cli/writer">
            <code>--set-build</code>
          </Link>{' '}
          is the write that does. <Link to="/cli/compute">
            <code>dispat compute</code>
          </Link>{' '}
          derives a monorepo&apos;s dependency graph from these files, and{' '}
          <Link to="/configuration/autoversion">
            <code>autoVersion</code>
          </Link>{' '}
          rewrites them at the version stage.
        </p>
      </div>
    </Section>
  );
}

// Install sits after the manifests table on purpose: by here a reader knows
// whether dispat reads their ecosystem, which is the question that decides
// whether the command is worth running.
function Install(): React.ReactElement {
  return (
    <Section chip="install" title="Install">
      <p className={styles.sectionLead}>
        One command, and no runtime to install first. The script downloads the binary for your platform, checks it
        against the checksum GitHub published, and puts it on your <code>PATH</code>.
      </p>
      <div className={styles.install}>
        <CodeBlock language="sh" title="Linux and macOS">
          {INSTALL_CURL}
        </CodeBlock>
        <CodeBlock language="sh" title="Linux and macOS, with wget">
          {INSTALL_WGET}
        </CodeBlock>
        <CodeBlock language="powershell" title="Windows">
          {INSTALL_WINDOWS}
        </CodeBlock>
      </div>
      <p className={styles.sectionLead}>
        After that the binary keeps itself current:{' '}
        <Link to="/cli/self-update">
          <code>dispat self-update</code>
        </Link>{' '}
        replaces it with the latest release and keeps the old one beside it for a week in case you want it back. Every
        command mentions a newer release on its way out, so you find out without going looking.
      </p>
      <p className={styles.sectionLead}>
        More ways to install (<code>go install</code>, the GitHub Action, the container images) and how to pin a
        version are in <Link to="/getting-started">Getting started</Link>.
      </p>
    </Section>
  );
}

// The repository README's `## Projects using dispat` list, read at build time
// like every other README extraction. It sits right after Install on purpose:
// a reader who just saw how to get the binary is deciding whether to trust it,
// and "it releases itself, and a four-level docker chain" is the answer.
function Projects(): React.ReactElement {
  const {repository} = useReadme();

  return (
    <Section chip="in production" title="Projects using dispat">
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
    <Section chip="read on" title="The documentation">
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
    <Section chip="lineage" title="Inspiration">
      {/* The repository README's section of the same name, read at build time
          rather than kept in step by hand. Its relative link to pkg/ccme comes
          back as a GitHub URL; see plugins/readme/inline.ts. */}
      <Argued argument={repository.inspiration} className={styles.reference} />
    </Section>
  );
}

function Community(): React.ReactElement {
  return (
    <Section chip="community" title="Have questions or issues?">
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
      title="The polyglot monorepo release tool"
      description="dispat is a release tool for polyglot monorepos. It reads your conventional commits, works out every package version with propagation to dependants, and builds and publishes each changed package in dependency order, in parallel, with changelogs, git tags and GitHub releases. A package is a folder and a stage is a shell command, so npm, Go, Cargo, Maven, .NET, Python, Ruby, Dart, Docker, iOS and Android live in one dependency graph.">
      <Hero />
      <main>
        <Libraries />
        <Install />
        <Projects />
        <Reference />
        <Inspiration />
        <Community />
      </main>
    </Layout>
  );
}
