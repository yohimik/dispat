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
        plugs in with zero integration work: npm next to Docker next to Go in one dependency graph. Each{' '}
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
        own, and every unaffected subgraph keeps releasing. Failed and skipped consumers are never lost — the next run
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
        deterministic replay — the plan is a pure function of history, graph and configuration.{' '}
        <Link to="/architecture">How it works.</Link>
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
        Every major monorepo tool can topologically sort a dependency graph. Two situations break that model in practice:
        an error in the middle of a run leaves half the packages published, and a consumer that can only be{' '}
        <em>built</em> after its provider is <em>published</em> (a Docker image pulling its base) contradicts
        &ldquo;build all, then publish all&rdquo;. dispat is built for that case.
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
          <Link to="/cookbook">Cookbook</Link>: real setups — npm, Docker, Go, Python, mobile, and the pnpm workspace
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
          <Link to="/commits">Commit messages</Link>: the Conventional Commits superset that carries release intent.
        </li>
        <li>
          <Link to="/environment">Script environment</Link>: the <code>DISPAT_*</code> variables a stage receives.
        </li>
        <li>
          <Link to="/architecture">Architecture</Link> and <Link to="/coverage">Coverage</Link>: how it is built and how
          it is tested.
        </li>
      </ul>
    </section>
  );
}

export default function Home(): React.ReactElement {
  return (
    <Layout
      title="Monorepo releases from conventional commits"
      description="dispat releases monorepos: it reads conventional commits, computes semantic versions with propagation to dependants, and builds and publishes packages in graph order, in parallel, with changelogs, git tags and GitHub releases.">
      <Hero />
      <main>
        <Features />
        <Reference />
      </main>
    </Layout>
  );
}
