import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import Layout from '@theme/Layout';
import React from 'react';

import styles from './index.module.css';

// The docs own the root (docs.routeBasePath === '/'), so this page is the site
// entry point: yohimik.github.io/dispat/, the URL every README and every search
// result points at. It carries the same claims as the repository README, in
// prose, because a redirect gave crawlers and readers nothing to land on.
//
// Every internal link goes through <Link> (or useBaseUrl for assets), never a
// raw path: Docusaurus mounts its router without a `basename` and registers
// routes with baseUrl already in them, so a bare "/getting-started" resolves
// outside the site and hits the 404 page.

const GITHUB = 'https://github.com/yohimik/dispat';

const TRANSCRIPT = `$ dispat
12:04:05 INF ● changed bump=minor package=core version="1.2.3 -> 1.3.0"
12:04:05 INF ● changed bump=patch package=app dueToProviders=[core] version="0.8.1 -> 0.8.2"
12:04:05 INF release plan ready packages=3 releasing=2
12:04:05 INF published package=core tag=core@1.3.0
12:04:05 INF published package=app tag=app@0.8.2
12:04:05 INF done published=2 failed=0 skipped=0`;

type Feature = {
  title: string;
  body: React.ReactElement;
};

const FEATURES: Feature[] = [
  {
    title: 'Polyglot by construction',
    body: (
      <>
        Packages are folders and stages are plain shell commands, so any language, build system, registry, CI or cache
        plugs in with zero integration work. dispat reads and rewrites fifteen manifest families: npm, Go, Cargo,
        Python, Composer, Maven, .NET, Dart, Ruby, Docker, and iOS and Android. Each{' '}
        <Link to="/concepts">space</Link> states through <code>isBuildWaitingPublish</code> whether a consumer&apos;s
        build needs its provider merely <em>built</em> (node) or already <em>published</em> (docker), so a four-level
        npm-to-docker chain schedules correctly out of the box.
      </>
    ),
  },
  {
    title: 'Built around an error model, not a happy path',
    body: (
      <>
        A failure never aborts the run: the broken package&apos;s consumers are skipped unless they have changes of their
        own, and every unaffected subgraph keeps releasing. Failed and skipped consumers are never lost. The next run
        catches them up at the exact version they were owed, with no state file and no double release.{' '}
        <Link to="/concepts">Recovery is just re-running.</Link>
      </>
    ),
  },
  {
    title: 'The graph can come from the manifests themselves',
    body: (
      <>
        <Link to="/cli">
          <code>dispat compute</code>
        </Link>{' '}
        reads package.json, go.mod, Cargo.toml, pyproject.toml, composer.json, pom.xml, .csproj and pubspec.yaml and
        derives the consumer/provider graph from them; <code>--check</code> gates CI on a drifted graph. A space with an{' '}
        <Link to="/configuration/spaces#autoversion">
          <code>autoVersion</code>
        </Link>{' '}
        block goes further: dispat rewrites the manifests at the version stage, reconciling declared ranges to
        end-of-run versions format-preservingly.
      </>
    ),
  },
  {
    title: 'A release is a distributed transaction',
    body: (
      <>
        Publishing a graph means irreversible writes across independent services with no rollback. Each package&apos;s
        leg commits by durably recording its completion: the annotated git tag, written only after the publish
        succeeded. No state files, no registry queries, so nothing can drift from what happened. Recovery is
        deterministic replay: the plan is a pure function of history, graph and configuration.{' '}
        <Link to="/internals/architecture">How it works.</Link>
      </>
    ),
  },
  {
    title: 'Edit every package at once',
    body: (
      <>
        <Link to="/cli/autowriter">
          <code>dispat autowriter</code>
        </Link>{' '}
        applies one manifest edit across every package the plan selects, finding each package&apos;s manifests itself,
        and{' '}
        <Link to="/cli/autosubstitute">
          <code>dispat autosubstitute</code>
        </Link>{' '}
        does the same for literal text, so hand-written coordinates in READMEs and install snippets follow a release
        too. Both take the same selection flags as <code>dispat run</code>.{' '}
        <Link to="/editing/autowriter">Editing across the monorepo.</Link>
      </>
    ),
  },
  {
    title: 'Every release step is also a command',
    body: (
      <>
        <code>dispat changelog</code>, <code>autoversion</code>, <code>commit</code> and <code>github</code> run one
        thing the release normally does, at the moment your own flow needs it, and the release stage then finds the
        work done and skips it. <code>dispat if</code> branches on an environment variable and <code>dispat exec</code>{' '}
        runs one declared script once.{' '}
        <Link to="/releasing/steps">Release steps.</Link>
      </>
    ),
  },
];

function Hero(): React.ReactElement {
  const {siteConfig} = useDocusaurusContext();

  return (
    <header className={styles.hero}>
      <div className="container">
        <img className={styles.logo} src={useBaseUrl('/logo.png')} alt="dispat logo" width={128} height={128} />
        <Heading as="h1" className={styles.title}>
          {siteConfig.title}
        </Heading>
        <p className={styles.tagline}>{siteConfig.tagline}</p>
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
        <p className={styles.lead}>
          dispat reads conventional commits to track changed packages, computes their next semantic versions
          (propagating bumps to dependants), and builds and publishes them in the right order, in parallel, with
          changelogs, git tags and GitHub releases on the way out.
        </p>
        <div className={styles.buttons}>
          <Link className="button button--primary button--lg" to="/getting-started">
            Get started
          </Link>
          <Link className="button button--secondary button--lg" to="/cookbook">
            Cookbook
          </Link>
        </div>
        <CodeBlock language="console" className={styles.transcript}>
          {TRANSCRIPT}
        </CodeBlock>
      </div>
    </header>
  );
}

function Features(): React.ReactElement {
  return (
    <section className="container margin-vert--xl">
      <Heading as="h2" className={styles.sectionTitle}>
        Why one more monorepo tool?
      </Heading>
      <p className={styles.sectionLead}>
        Every major monorepo tool can topologically sort a dependency graph: build everything in order, then publish
        everything, or publish only what changed, sequentially. Two situations break that model in practice.
      </p>
      <ol className={styles.problems}>
        <li>
          <strong>An error in the middle of a run.</strong> Half the packages are published, half are not. Most tools
          either abort the whole run or plough on and leave you to reconstruct what shipped. Re-running tends to
          re-release things that are already out, or you end up writing recovery scripts by hand.
        </li>
        <li>
          <strong>
            A consumer that can only be <em>built</em> after its provider is <em>published</em>.
          </strong>{' '}
          A Node package can be built before its consumers publish, but a Docker image is often buildable only by pulling
          its base image from a registry, which means the provider must already be published. &ldquo;Build all, then
          publish all&rdquo; assumes every ecosystem behaves like npm, and mixed graphs break it.
        </li>
      </ol>
      <p className={styles.sectionLead}>
        Modern projects are exactly that mix: many packages on different infrastructure (npm next to Docker next to Go)
        wired into one dependency graph. dispat is built for that case, and{' '}
        <Link to="/concepts">Concepts</Link> works both situations through end to end.
      </p>
      <div className={styles.features}>
        {FEATURES.map((feature) => (
          <div className={styles.feature} key={feature.title}>
            <Heading as="h3" className={styles.featureTitle}>
              {feature.title}
            </Heading>
            <p>{feature.body}</p>
          </div>
        ))}
      </div>
    </section>
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
  ['Docker: images and Compose', 'Dockerfile, compose.yaml, docker-compose.yml, and their .override spellings'],
];

// dispat's pieces are separate Go modules, usable with no dispat in sight, and
// nothing on the site said so. Links point at GitHub because these are packages
// rather than pages.
function Libraries(): React.ReactElement {
  return (
    <section className="container margin-bottom--xl">
      <Heading as="h2" className={styles.sectionTitle}>
        Lightweight libraries, usable on their own
      </Heading>
      <p className={styles.sectionLead}>
        Parsing commit messages and reading and rewriting dependency manifests are problems far older than releases, so
        dispat keeps all three as standalone Go modules with no dependency on the CLI, on git or on a network. The
        manifest pair shares its vocabulary through{' '}
        <Link to={`${GITHUB}/tree/main/pkg/manifest`}>
          <code>pkg/manifest</code>
        </Link>{' '}
        (dependency kinds, manifest file-name rules, PEP 503 normalisation) so the reader and the writer can never
        drift apart.
      </p>
      <div className={styles.libraries}>
        <div className={styles.feature}>
          <Heading as="h3" className={styles.featureTitle}>
            <Link to={`${GITHUB}/tree/main/pkg/ccme`}>
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
            <Link to={`${GITHUB}/tree/main/pkg/scanner`}>
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
            <Link to={`${GITHUB}/tree/main/pkg/writer`}>
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
          <code>android:versionCode</code>, <code>CURRENT_PROJECT_VERSION</code>): the scanner reads it, and no writer
          ever rewrites it. <Link to="/cli">
            <code>dispat compute</code>
          </Link>{' '}
          derives a monorepo&apos;s dependency graph from these files, and{' '}
          <Link to="/configuration/spaces#autoversion">
            <code>autoVersion</code>
          </Link>{' '}
          rewrites them at the version stage.
        </p>
      </div>
    </section>
  );
}

function Reference(): React.ReactElement {
  return (
    <section className="container margin-bottom--xl">
      <Heading as="h2" className={styles.sectionTitle}>
        The documentation
      </Heading>
      <ul className={styles.reference}>
        <li>
          <Link to="/getting-started">Getting started</Link>: install the binary, write one config file, wire the
          release into CI.
        </li>
        <li>
          <Link to="/cookbook">Cookbook</Link>: real setups: npm, Docker, Go, Python, mobile, and the pnpm workspace
          case.
        </li>
        <li>
          <Link to="/concepts">Concepts</Link>: packages and spaces, propagation, the plan, the failure and recovery
          model.
        </li>
        <li>
          <Link to="/cli">CLI</Link> and <Link to="/configuration">Configuration</Link>: every command, every option.
        </li>
        <li>
          <Link to="/reference/commits">Commit messages</Link>: the Conventional Commits superset that carries release intent.
        </li>
        <li>
          <Link to="/reference/environment">Script environment</Link>: the <code>DISPAT_*</code> variables a stage receives.
        </li>
        <li>
          <Link to="/internals/architecture">Architecture</Link> and <Link to="/internals/coverage">Coverage</Link>: how it is built and how
          it is tested.
        </li>
      </ul>
    </section>
  );
}

// Community is last on purpose: a reader who got this far has the shape of the
// tool and is deciding whether to adopt it, which is the moment an invitation
// is worth anything.
function Community(): React.ReactElement {
  return (
    <section className="container margin-bottom--xl text--center">
      <Heading as="h2" className={styles.sectionTitle}>
        Have questions or issues?
      </Heading>
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
    </section>
  );
}

export default function Home(): React.ReactElement {
  return (
    <Layout
      title="Release orchestration for polyglot monorepos"
      description="dispat is release orchestration for polyglot monorepos: it reads conventional commits, computes semantic versions with propagation to dependants, and builds and publishes packages in graph order, in parallel, with changelogs, git tags and GitHub releases. Packages are folders and stages are shell commands, so npm, Go, Cargo, Maven, .NET, Python, Ruby, Dart, Docker, iOS and Android live in one dependency graph.">
      <Hero />
      <main>
        <Features />
        <Libraries />
        <Reference />
        <Community />
      </main>
    </Layout>
  );
}
