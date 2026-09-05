import Link from '@docusaurus/Link';
import BrowserOnly from '@docusaurus/BrowserOnly';
import type {PlayerRef} from '@remotion/player';
import type {Feature} from '@site/plugins/readme/types';
import Inlines from '@site/src/components/Inline';
import React from 'react';
import aquaFixture from '../../../demo/fixtures/aqua/expected.json';
import runFixture from '../../../demo/fixtures/run/expected.json';
import forFixture from '../../../demo/fixtures/for/expected.json';
import computeFixture from '../../../demo/fixtures/compute/expected.json';

import styles from './styles.module.css';

// README features use stable IDs to select shared Remotion compositions.
// Load only the selected scene. Playback follows the user's transport choice,
// document visibility, and viewport intersection; scene timing stays in its source.

type Media = {
  /** Base name retained as the stable scene key and recording file name. */
  asset: string;
  /** The nav button's word or two. */
  name: string;
  /** What the animation shows, for a reader who cannot see it. */
  label: string;
};

const FPS = 20;
const LivePlayer = React.lazy(() => import('./LivePlayer'));
const SCENE_COMMANDS: Record<string, string> = {
  'demo-why': 'dispat status', 'demo-order': 'dispat', 'demo-blast': 'dispat status',
  'demo-heal': 'dispat\n# Repair the failing test, then commit the change.\ngit commit -am "chore(api): repair failing test"\ndispat', 'demo-control': 'dispat status', 'demo-polyglot': 'dispat writer',
  'demo-terminal': 'dispat changelog', 'demo-compute': `${computeFixture.command}\n${computeFixture.writeCommand}`,
  'demo-run': runFixture.command, 'demo-single': 'dispat',
  'demo-hooks': 'dispat', 'demo-polyrepo': 'dispat', 'demo-math': 'dispat status',
  'demo-glue': 'dispat autowriter --link-local', 'demo-lock': 'dispat release',
  'demo-aqua': aquaFixture.commands.join('\n'),
  'demo-for': forFixture.command,
  'demo-progress': 'dispat\n# A publish response was lost. Check the registry before retrying.\nnpm view @acme/api@1.5.0 version',
};

class SceneBoundary extends React.Component<{children: React.ReactNode; fallback: React.ReactNode}, {failed: boolean}> {
  state = {failed: false};
  static getDerivedStateFromError() { return {failed: true}; }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}
type LoadedScene = {component: React.ComponentType; duration: number};
const SCENES: Record<string, () => Promise<LoadedScene>> = {
  'demo-order': () => import('../../../demo/illustration/src/Order').then((m) => ({component: m.Order, duration: m.ORDER_DURATION})),
  'demo-blast': () => import('../../../demo/illustration/src/Blast').then((m) => ({component: m.Blast, duration: m.BLAST_DURATION})),
  'demo-heal': () => import('../../../demo/illustration/src/Master').then((m) => ({component: m.Heal, duration: m.HEAL_DURATION})),
  'demo-control': () => import('../../../demo/illustration/src/Control').then((m) => ({component: m.Control, duration: m.CONTROL_DURATION})),
  'demo-polyglot': () => import('../../../demo/illustration/src/Polyglot').then((m) => ({component: m.Polyglot, duration: m.POLYGLOT_DURATION})),
  'demo-terminal': () => import('../../../demo/illustration/src/Terminal').then((m) => ({component: m.Terminal, duration: m.TERMINAL_DURATION})),
  'demo-why': () => import('../../../demo/illustration/src/Why').then((m) => ({component: m.Why, duration: m.WHY_DURATION})),
  'demo-compute': () => import('../../../demo/illustration/src/Compute').then((m) => ({component: m.Compute, duration: m.COMPUTE_DURATION})),
  'demo-run': () => import('../../../demo/illustration/src/Run').then((m) => ({component: m.Run, duration: m.RUN_DURATION})),
  'demo-single': () => import('../../../demo/illustration/src/Single').then((m) => ({component: m.Single, duration: m.SINGLE_DURATION})),
  'demo-hooks': () => import('../../../demo/illustration/src/Hooks').then((m) => ({component: m.Hooks, duration: m.HOOKS_DURATION})),
  'demo-polyrepo': () => import('../../../demo/illustration/src/Polyrepo').then((m) => ({component: m.Polyrepo, duration: m.POLYREPO_DURATION})),
  'demo-math': () => import('../../../demo/illustration/src/Math').then((m) => ({component: m.Math_, duration: m.MATH_DURATION})),
  'demo-progress': () => import('../../../demo/illustration/src/Progress').then((m) => ({component: m.Progress, duration: m.PROGRESS_DURATION})),
  'demo-glue': () => import('../../../demo/illustration/src/Glue').then((m) => ({component: m.Glue, duration: m.GLUE_DURATION})),
  'demo-lock': () => import('../../../demo/illustration/src/Lock').then((m) => ({component: m.Lock, duration: m.LOCK_DURATION})),
  'demo-for': () => import('../../../demo/illustration/src/For').then((m) => ({component: m.For, duration: m.FOR_DURATION})),
  'demo-aqua': () => import('../../../demo/illustration/src/Aqua').then((m) => ({component: m.Aqua, duration: m.AQUA_DURATION})),
};

// A selected scene stays mounted while the next chunk loads. Cache only scenes
// the reader requests, and evict failed requests so selecting again can retry.
const sceneRequests = new Map<string, Promise<LoadedScene>>();
function loadScene(asset: string): Promise<LoadedScene> {
  const existing = sceneRequests.get(asset);
  if (existing) return existing;
  const request = SCENES[asset]().catch((error) => {
    sceneRequests.delete(asset);
    throw error;
  });
  sceneRequests.set(asset, request);
  return request;
}

const FEATURE_MEDIA = new Map<string, Media>([
  [
    'release-graph',
    {
      asset: 'demo-order',
      name: 'Releases the graph',
      label:
        'The Go module core and npm package utils build and publish independently. The Go API is configured to wait for core’s published version. The npm SDK builds against its local workspace as soon as utils finishes building, while utils is still publishing. The web app follows the API. Five packages publish; docs and mobile remain unchanged.',
    },
  ],
  [
    'blast-radius',
    {
      asset: 'demo-blast',
      name: 'Blast radius',
      label:
        'The same commit planned twice, with a terminal running dispat status after each version: as feat(core) only core releases, and amended to feat(core)^^ the whole consumer closure joins the plan while utils, a provider, stays unchanged.',
    },
  ],
  [
    'self-healing',
    {
      asset: 'demo-heal',
      name: 'Fix and rerun',
      label:
        'The API build fails and web is skipped. Independent packages core, utils, and SDK still publish. After repairing the test, the operator commits the change and runs dispat again. Tags preserve completed work; the API and web finish their pending releases.',
    },
  ],
  [
    'release-control',
    {
      asset: 'demo-control',
      name: 'Release control',
      label:
        'One package card answers commits typed into a terminal: a feat bumps it, %beta starts a prerelease train, a breaking change arriving mid-train moves the whole train to 2.0.0-beta.0, %beta>stable graduates it there, Release-As: none holds it, and Release-As: auto resumes it, with the pretty log confirming each decision.',
    },
  ],
  [
    'polyglot',
    {
      asset: 'demo-polyglot',
      name: 'Polyglot',
      label:
        'One manifest after another opens in the same editor, package.json to go.mod to Cargo.toml to pom.xml to pubspec.yaml to Info.plist to a Dockerfile, and in each one the version value is rewritten in place while the surrounding manifest structure stays recognizable. Go manifests use the standard formatter.',
    },
  ],
  [
    'step-commands',
    {
      asset: 'demo-terminal',
      name: 'Steps as commands',
      label:
        'Three package rows, each with its own step set inside the same run: core takes the release’s default with the records written at the end, api nests changelog and commit before its own publish so the commit contains the changelog, and utils publishes its GitHub release from its own announce script. Then run alone, dispat changelog finds the entry already written and answers W226.',
    },
  ],
]);


/**
 * The slides past the README's bullets: capabilities the docs describe that
 * deserve their own animation. Unlike the bullets these carry their own
 * title and text, kept deliberately close to the pages they restate
 * (compute, run, the single-package example, spaces flow, the control
 * repository, and the release lock).
 */
type ExtraSlide = {title: string; name: string; body: React.ReactNode; media: Media};

/**
 * The deck's opening argument, replacing the landing page's "why one more
 * monorepo tool?" section: the repository README's two situations, drawn.
 */
const WHY_SLIDE: ExtraSlide = {
  title: 'Build and publish one dependency graph',
  name: 'How it works',
  body: (
    <>
      Dispat reads package relationships, plans the affected releases, then builds and publishes each dependency
      before its consumers. It records successful publishes with Git tags so the next run can plan unfinished work.
      <Link to="/concepts"> Concepts</Link> follows that workflow end to end.
    </>
  ),
  media: {
    asset: 'demo-why',
    name: 'Why dispat',
    label:
      'Three beats: under build-all-then-publish-all a Docker consumer fails to build because its base image was never published; a mid-run error leaves core and utils published, api failed, and web unknown, with a registry unable to say what is owed; then dispat runs build and publish as legs of one graph, api waiting for core@1.5.0 and both publishing with their tags.',
  },
};

const EXTRA_SLIDES: ExtraSlide[] = [
  {
    title: 'Aqua manifests, read and rewritten directly',
    name: 'Aqua',
    body: (
      <>The scanner follows Aqua package imports, preserves registry-qualified names, and the writer updates literal versions while reporting dynamic entries it cannot safely rewrite. The scene reads the same deterministic fixture the real CLI integration test drives.</>
    ),
    media: {
      asset: 'demo-aqua',
      name: 'Aqua',
      label: 'The real Aqua fixture is scanned into cli/cli and corp:private/tool. The writer updates cli/cli from v2.0.0 to v2.1.0, preserves the private tool at 1.4.0, and reports one applied change with one dynamic version skipped.',
    },
  },
  {
    title: 'Discover dependencies from your package manifests',
    name: 'Compute',
    body: (
      <>
        <Link to="/cli/compute">
          <code>dispat compute</code>
        </Link>{' '}
        reads manifests in your configured <Link to="/configuration/spaces">spaces</Link> and proposes dependencies
        and starting versions, with manifest evidence for each suggestion. Preview the changes first, then apply
        them with <code>--write</code>. Use <code>--interactive</code> when you want to review each suggestion separately.
      </>
    ),
    media: {
      asset: 'demo-compute',
      name: 'Compute',
      label:
        'A small npm workspace has a web app that depends on core. dispat compute previews that relationship and the two initial versions from package.json files. The preview leaves configuration unchanged; a separate --write command applies the reviewed changes.',
    },
  },
  {
    title: 'Scripts for what changed',
    name: 'dispat run',
    body: (
      <>
        <Link to="/cli/run">
          <code>dispat run tests --since HEAD~1</code>
        </Link>{' '}
        runs scripts for packages changed in the last commit. Without <code>--since</code>, it uses the same
        unreleased commit window as <code>dispat status</code>. Add <code>--consumers</code> to include packages that
        depend on the selected packages, directly or transitively. Scripts run in dependency order; this command
        does not publish packages or create release tags. See <Link to="/reference/pipelines">CI examples</Link>.
      </>
    ),
    media: {
      asset: 'demo-run',
      name: 'dispat run',
      label:
        'A fix lands in utils and dispat run tests --since HEAD~1 --consumers runs utils first, then api and sdk side by side, then the transitive web consumer; core, docs, and mobile stay unselected and the log ends with ran=4 and failed=0.',
    },
  },
  {
    title: 'One package, no monorepo',
    name: 'Single package',
    body: (
      <>
        One standalone <Link to="/configuration/packages">packages entry</Link> pointing at a folder is{' '}
        <Link to="/examples/single-package">the whole setup</Link>: semantic versions from commits, a changelog, an
        annotated tag, and a GitHub release for a single package, with the same channels, hooks, and commands waiting
        when the repository grows.
      </>
    ),
    media: {
      asset: 'demo-single',
      name: 'Single package',
      label:
        'A one-line configuration and a single app card: a scoped commit marks it changed 0.0.0 to 0.1.0, dispat status prints the plan, and the release leaves three records under the card: the app@0.1.0 tag, CHANGELOG.md, and a GitHub release.',
    },
  },
  {
    title: 'Stages, hooks, and one environment',
    name: 'Hooks',
    body: (
      <>
        A release walks <Link to="/configuration/spaces">named stages</Link>, version, build, a per-space login,
        publish, announce, with a hook before each; everything up to <code>beforePublish</code> can still stop it.
        Every script receives the same <Link to="/reference/environment">DISPAT_* environment</Link>: the package,
        both versions, the channel, and the tag about to be created.
      </>
    ),
    media: {
      asset: 'demo-hooks',
      name: 'Hooks',
      label:
        'Three package rows across two spaces, each walking the same stages, version, build, login, publish, announce, with only its own configured hooks appearing above its strip: core’s lint and print-env, utils’s verify-sbom with the libs login shared, api’s check-migrations and notify-slack, while print-env writes the DISPAT_* environment into the terminal.',
    },
  },
  {
    title: 'Many repositories, one release',
    name: 'Polyrepos',
    body: (
      <>
        A small <Link to="/control-repository">control repository</Link> holds the dispat configuration and a git
        submodule per linked repository, which is the single checkout the graph needs. Moving a pointer is an ordinary
        commit, so the fleet releases in dependency order while every team keeps its own repository, permissions, and
        history.
      </>
    ),
    media: {
      asset: 'demo-polyrepo',
      name: 'Polyrepos',
      label:
        'Inside a dashed frame labelled as the control repository, three cards each carry a git submodule pointer; a sync moves sdk\u2019s pointer, a scoped commit marks sdk and api changed, and the release publishes both in order while web stays unchanged.',
    },
  },
  {
    title: 'Deterministic plans and recorded completion',
    name: 'Math',
    body: (
      <>
        <Link to="/internals/architecture">Planning is deterministic</Link>: the same history, package graph, and
        configuration produce the same plan. It needs no persistent release cache or database, and version decisions
        do not depend on the clock. Repeating a completed release skips its recorded versions; a publish without
        a recorded tag still needs a destination check or an idempotent publisher. <Link to="/go/ccme">CCME parsing</Link> reads each commit message from left to right
        without backtracking.
      </>
    ),
    media: {
      asset: 'demo-math',
      name: 'Math',
      label:
        'Two dispat status runs produce the same plan from the same inputs. The planner needs no persistent release cache or database and does not use the clock for version decisions. Recorded release tags make completed versions safe to skip on reruns, while an unrecorded publish needs reconciliation. A cursor then demonstrates the commit parser moving through the input once, without backtracking.',
    },
  },
  {
    title: 'Recorded progress and safe retries',
    name: 'Recorded progress',
    body: (
      <>
        Git tags record completed releases so the next run can skip them. If a publish response is lost before its
        tag is written, check the destination before rerunning, or use an <strong>idempotent publisher</strong> that
        safely accepts the same request again. <Link to="/reference/releasing/recovery">Read about recovery and retries</Link>.
      </>
    ),
    media: {
      asset: 'demo-progress',
      name: 'Recorded progress',
      label:
        'Three cards separate confirmed progress from uncertainty: a tag records a completed release, a lost publish response leaves the outcome unclear, and the operator checks the destination or uses an idempotent publisher before retrying. Recorded versions can be skipped; ambiguous outcomes still need care.',
    },
  },
  {
    title: 'Run a command for each selected package',
    name: 'dispat for',
    body: (
      <>
        <Link to="/cli/for"><code>dispat for</code></Link> runs a command once per item. With <code>--changed</code>,
        it selects changed packages in dependency order and provides each name as <code>DISPAT_ITEM</code>.
        This game workspace adds downstream packages with <code>--consumers</code>. Use <code>dispat run</code>
        when packages already declare the script you want to run.
      </>
    ),
    media: {
      asset: 'demo-for', name: 'dispat for',
      label: 'An engine change selects the engine, game, and native packages. The same shell command runs for each selected package, in dependency order, printing its name through DISPAT_ITEM.',
    },
  },
  {
    title: 'The glue between the steps',
    name: 'if & replacer',
    body: (
      <>
        <Link to="/cli/if">
          <code>dispat if</code>
        </Link>{' '}
        chooses between shell scripts by testing the environment, the filesystem, or what changed, so a stage branches
        without depending on its shell.{' '}
        <Link to="/cli/replacer">
          <code>dispat replacer</code>
        </Link>{' '}
        swaps literal text in the files no manifest parser covers, a Gradle coordinate or a README example, and{' '}
        <Link to="/cli/autowriter">
          <code>autowriter --link-local</code>
        </Link>{' '}
        opens the bracket that points workspace dependencies at their folders for tests, with{' '}
        <code>--unlink-local</code> and <code>scanner --verify-unlinked</code> closing and proving it.
      </>
    ),
    media: {
      asset: 'demo-glue',
      name: 'if & replacer',
      label:
        'Three acts in one terminal: dispat if branches on ENV=prod and only the --then script runs; dispat replacer swaps a Gradle coordinate and a README install line in place; and autowriter --link-local writes a go.mod replace pointing core at its folder, tests pass against the working tree, --unlink-local removes it, and scanner --verify-unlinked exits 0.',
    },
  },
  {
    title: 'One release at a time',
    name: 'Release lock',
    body: (
      <>
        <code>dispat release</code> claims the repository before planning anything by pushing a{' '}
        <Link to="/reference/releasing/release-lock">
          <code>dispat-release-lock</code> tag
        </Link>
        ; a rejected push is the lock. Each attempt has a unique tag object, names its host and process, and removes
        the remote lock only with an object-ID lease, so one run cannot delete another run’s claim. A second run stops
        before planning, and cleanup lets the retry claim it cleanly.
      </>
    ),
    media: {
      asset: 'demo-lock',
      name: 'Release lock',
      label:
        'A ci runner claims the dispat-release-lock tag on origin and releases while a laptop\u2019s dispat release is rejected because the tag already exists; when the runner finishes and returns the lock, the laptop\u2019s retry claims it and plans normally.',
    },
  },
];

/**
 * The fallback nav name for a bullet the map does not know: everything
 * before the title's first comma or colon, which is where a README title
 * finishes naming the feature and starts qualifying it.
 */
function shortName(title: string): string {
  const cut = title.search(/[,:]/);
  return cut < 0 ? title : title.slice(0, cut);
}

/**
 * The deck's one control surface, shaped like a player bar: the transport
 * cluster (play/pause and repeat-one) on the left, a hairline divider, then
 * the named slides. Pause freezes the clip and the rotation; repeat keeps
 * the clip looping with the rotation staying put.
 */
function DeckControls({
  slides,
  active,
  onSelect,
  loop,
  onToggleLoop,
  playing,
  onTogglePlay,
  speed,
  onCycleSpeed,
}: {
  slides: Array<{id: string; title: string; name: string}>;
  active: number;
  onSelect: (i: number) => void;
  loop: boolean;
  onToggleLoop: () => void;
  playing: boolean;
  onTogglePlay: () => void;
  speed: number;
  onCycleSpeed: () => void;
}): React.ReactElement {
  return (
    <div className={styles.controls} role="toolbar" aria-label="Slide controls">
      <div className={styles.transport}>
        <button
          type="button"
          className={loop ? `${styles.iconButton} ${styles.activeIconButton}` : styles.iconButton}
          aria-label="Loop the current slide"
          aria-pressed={loop}
          title="Loop the current slide"
          onClick={onToggleLoop}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path
              fill="currentColor"
              d="M17 2l4 4-4 4V7H7a3 3 0 0 0-3 3v2H2v-2a5 5 0 0 1 5-5h10V2zM7 22l-4-4 4-4v3h10a3 3 0 0 0 3-3v-2h2v2a5 5 0 0 1-5 5H7v3z"
            />
            {/* Repeat-one, the way a player marks it. */}
            {loop && (
              <text x="12" y="15.4" textAnchor="middle" fontSize="9.5" fontWeight="700" fill="currentColor">
                1
              </text>
            )}
          </svg>
        </button>
        <button
          type="button"
          className={styles.iconButton}
          aria-label={playing ? 'Pause the slide' : 'Play the slide'}
          title={playing ? 'Pause' : 'Play'}
          onClick={onTogglePlay}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            {playing ? (
              <path fill="currentColor" d="M6 4h4v16H6zM14 4h4v16h-4z" />
            ) : (
              <path fill="currentColor" d="M8 5v14l11-7z" />
            )}
          </svg>
        </button>
        <button
          type="button"
          className={styles.speedButton}
          aria-label={`Playback speed ${speed}x, click to change`}
          title="Playback speed"
          onClick={onCycleSpeed}>
          {speed}×
        </button>
      </div>
      <span className={styles.divider} />
      <div className={styles.pills} role="group" aria-label="Key features">
        {slides.map(({id, title, name}, i) => (
          <button
            key={id}
            data-demo-feature={id}
            type="button"
            className={i === active ? `${styles.navButton} ${styles.activeNavButton}` : styles.navButton}
            aria-label={title}
            aria-current={i === active}
            onClick={() => onSelect(i)}>
            {name}
          </button>
        ))}
      </div>
    </div>
  );
}

export default function DemoCarousel({features}: {features: Feature[]}): React.ReactElement {
  const slides = React.useMemo(
    () => [
      ...features.map((feature) => {
        const media = FEATURE_MEDIA.get(feature.id);
        return {
          id: feature.id,
          title: feature.title,
          name: media?.name ?? shortName(feature.title),
          media,
          body: <Inlines tokens={feature.body} />,
        };
      }),
      {
        id: 'how-it-works',
        title: WHY_SLIDE.title,
        name: WHY_SLIDE.name,
        media: WHY_SLIDE.media,
        body: WHY_SLIDE.body,
      },
      ...EXTRA_SLIDES.map((extra) => ({
        id: extra.media.asset.replace(/^demo-/, ''),
        title: extra.title,
        name: extra.name,
        media: extra.media,
        body: extra.body,
      })),
    ],
    [features],
  );
  const [requested, setRequested] = React.useState({index: 0, revision: 0});
  const [displayed, setDisplayed] = React.useState<{index: number; scene?: LoadedScene}>({index: 0});
  const active = displayed.index;
  const scene = displayed.scene;
  const selectSlide = React.useCallback((index: number) => {
    setRequested((previous) => ({index, revision: previous.revision + 1}));
  }, []);
  const [loop, setLoop] = React.useState(false);
  // SSR starts on the meaningful still. Hydration opts into motion only
  // after reading the user's preference, avoiding a moving server fallback.
  const [paused, setPaused] = React.useState(true);
  const [speed, setSpeed] = React.useState(1);
  const [transcriptOpen, setTranscriptOpen] = React.useState(true);
  const [onscreen, setOnscreen] = React.useState(false);
  const [pageVisible, setPageVisible] = React.useState(true);
  const transportTouched = React.useRef(false);
  const root = React.useRef<HTMLDivElement>(null);
  const [player, setPlayer] = React.useState<PlayerRef | null>(null);
  const current = slides[active];
  const requestedAsset = slides[requested.index]?.media?.asset;
  const [sceneFailed, setSceneFailed] = React.useState(false);
  React.useEffect(() => {
    let currentLoad = true;
    setSceneFailed(false);
    if (requestedAsset && SCENES[requestedAsset]) {
      void loadScene(requestedAsset)
        .then((loaded) => {
          if (currentLoad) setDisplayed({index: requested.index, scene: loaded});
        })
        .catch(() => { if (currentLoad) setSceneFailed(true); });
    } else {
      setDisplayed({index: requested.index});
    }
    return () => { currentLoad = false; };
  }, [requested, requestedAsset]);

  React.useEffect(() => {
    const node = root.current;
    if (!node) return undefined;
    const observer = new IntersectionObserver(([entry]) => setOnscreen(entry.isIntersecting), {threshold: 0.2});
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  React.useEffect(() => {
    const preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    const changed = () => { if (!transportTouched.current) setPaused(preference.matches); };
    changed();
    preference.addEventListener('change', changed);
    return () => preference.removeEventListener('change', changed);
  }, []);
  React.useEffect(() => {
    const changed = () => setPageVisible(document.visibilityState === 'visible');
    changed();
    document.addEventListener('visibilitychange', changed);
    return () => document.removeEventListener('visibilitychange', changed);
  }, []);

  const startedPlayer = React.useRef<PlayerRef | null>(null);
  const shouldPlay = !paused && onscreen && pageVisible;
  React.useEffect(() => {
    if (shouldPlay && player) {
      // A reduced-motion still is a preview. First playback tells the whole
      // story; subsequent pause/resume keeps the reader’s position.
      if (startedPlayer.current !== player) {
        player.seekTo(0);
        startedPlayer.current = player;
      }
      player.play();
    } else player?.pause();
  }, [shouldPlay, active, scene, player]);

  const slidesCount = React.useRef(slides.length);
  slidesCount.current = slides.length;
  const onClipEnded = React.useCallback(() => {
    if (!loop && requested.index === active && !sceneFailed) selectSlide((active + 1) % slidesCount.current);
  }, [loop, requested.index, active, sceneFailed, selectSlide]);
  React.useEffect(() => {
    if (!player) return undefined;
    player.addEventListener('ended', onClipEnded);
    return () => player.removeEventListener('ended', onClipEnded);
  }, [active, onClipEnded, scene, player]);

  return (
    <div className={styles.carousel} ref={root} data-demo-id="landing-demos">
      <div className={styles.frame}>
        <div className={styles.stack}>
          <div className={styles.slide} data-slide-id={current.id}>
            <h3 id="landing-demo-title" className={styles.slideTitle}>
              {current.title}
            </h3>
            <div key={current.id} className={styles.video}>
              <div className={styles.mobilePanHint}>Swipe to explore the diagram, or read the transcript below.</div>
              <div
                className={styles.sceneScroller}
                data-demo-canvas
                role="region"
                aria-labelledby="landing-demo-title"
                aria-describedby="landing-demo-description"
                tabIndex={0}>
                <div className={styles.sceneViewport} data-demo-duration={scene?.duration} data-demo-fps={FPS} aria-hidden="true">
                  {scene ? (
                    <BrowserOnly fallback={<div className={styles.loading}>Interactive demo</div>}>
                      {() => (
                        <SceneBoundary fallback={<div className={styles.loading}>Demo unavailable. Read the description below.</div>}>
                          <React.Suspense fallback={<div className={styles.loading}>Loading interactive demo…</div>}>
                            <LivePlayer
                              ref={setPlayer}
                              component={scene.component}
                              durationInFrames={scene.duration}
                              compositionWidth={1920}
                              compositionHeight={800}
                              sourceHeight={current.media?.asset === 'demo-aqua' ? 800 : 1080}
                              cropTop={current.media?.asset === 'demo-aqua' ? 0 : 280}
                              fps={FPS}
                              loop={loop}
                              playbackRate={speed}
                              initialFrame={paused ? Math.min(Math.round(scene.duration / 3), scene.duration - 1) : 0}
                              controls={false}
                              acknowledgeRemotionLicense
                              style={{width: '100%', height: '100%'}}
                            />
                          </React.Suspense>
                        </SceneBoundary>
                      )}
                    </BrowserOnly>
                  ) : <div className={styles.loading}>{sceneFailed ? 'Demo unavailable. Read the description below.' : 'Loading interactive demo…'}</div>}
                </div>
              </div>
            </div>
            <DeckControls
              slides={slides}
              active={active}
              onSelect={selectSlide}
              loop={loop}
              onToggleLoop={() => setLoop((l) => !l)}
              playing={!paused}
              onTogglePlay={() => { transportTouched.current = true; setPaused((p) => !p); }}
              speed={speed}
              onCycleSpeed={() => setSpeed((s) => ({1: 1.5, 1.5: 2, 2: 0.5, 0.5: 1}[s] ?? 1))}
            />
            <div className={styles.band}>
              {sceneFailed && scene && (
                <p role="status">The next demo could not load. The current demo is still available.{' '}
                  <button type="button" onClick={() => selectSlide(requested.index)}>Try again</button>
                </p>
              )}
              <p id="landing-demo-description" className={styles.bandBody}>{current.body}</p>
              {current.media && (
                <details
                  className={styles.transcript}
                  open={transcriptOpen}
                  onToggle={(event) => setTranscriptOpen(event.currentTarget.open)}>
                  <summary>Accessible description and transcript</summary>
                  <pre className={styles.command}><code>{SCENE_COMMANDS[current.media.asset]}</code></pre>
                  <p>{current.media.label}</p>
                </details>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
