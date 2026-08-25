import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import type {Feature} from '@site/plugins/readme/types';
import Inlines from '@site/src/components/Inline';
import Heading from '@theme/Heading';
import React from 'react';

import styles from './styles.module.css';

// The landing page hero's deck: one slide per key feature, playing round
// robin. Each slide is an animated illustration of its claim, diagram over
// terminal, with the claim's title over the clip's top strip and its words
// in a band underneath. The compositions keep a taller strip empty (the
// master's scene titles live there), so the video is shown cropped to the
// bottom 840 of its 1080 rows, which leaves just enough strip for the one
// title line.
//
// The features are the CLI README's `## Key features` bullets, read at build
// time like every claim on this page, in the README's order. FEATURE_MEDIA
// pairs each bullet with the clip that illustrates it (rendered by
// packages/docs/demo/render.sh from the compositions in
// packages/docs/demo/illustration); a bullet without a clip still gets a
// slide, its title on the canvas, until an animation is made for it.
//
// The player: a clip plays once and hands over the moment it ends, straight
// round robin. Repeat-one replays the current slide instead, pause freezes
// clip and rotation together, the speed cycler drives playbackRate, and a
// pointer resting on the slide replays it rather than rotating a reader
// away mid-paragraph. The nav names every slide, so a reader who wants one
// claim again does not have to sit through the rest of the deck.

type Media = {
  /** Base name of the webm and mp4 pair in imgs/, served at the site root. */
  asset: string;
  /** The nav button's word or two. */
  name: string;
  /** What the animation shows, for a reader who cannot see it. */
  label: string;
};

const FEATURE_MEDIA = new Map<string, Media>([
  [
    'Releases the graph, not a list',
    {
      asset: 'demo-order',
      name: 'Releases the graph',
      label:
        'An animated dependency graph over a terminal running dispat: core and utils build and publish side by side, api waits for core@1.5.0, rewrites its Dockerfile, builds and publishes, web follows, and the run ends with all four published, the path lit end to end, and the log reading done published=4 while three unchanged packages sit out.',
    },
  ],
  [
    'Blast radius written in the commit',
    {
      asset: 'demo-blast',
      name: 'Blast radius',
      label:
        'The same commit planned twice, with a terminal running dispat status after each version: as feat(core) only core releases, and amended to feat(core)^^ the whole consumer closure joins the plan while utils, a provider, stays unchanged.',
    },
  ],
  [
    'Self-healing runs, because a release is a distributed transaction',
    {
      asset: 'demo-heal',
      name: 'Self-healing runs',
      label:
        'api’s build fails, web is skipped because its provider failed, and core and utils still ship; the re-run marks api for catch-up at the same version, publishes it, releases web behind it, and ends with nothing failed and nothing skipped.',
    },
  ],
  [
    'Release control from commits',
    {
      asset: 'demo-control',
      name: 'Release control',
      label:
        'One package card answers commits typed into a terminal: a feat bumps it, %beta starts a prerelease train, a breaking change arriving mid-train moves the whole train to 2.0.0-beta.0, %beta>stable graduates it there, Release-As: none holds it, and Release-As: auto resumes it, with the pretty log confirming each decision.',
    },
  ],
  [
    'Polyglot by construction: any language, any registry, any tooling',
    {
      asset: 'demo-polyglot',
      name: 'Polyglot',
      label:
        'One manifest after another opens in the same editor, package.json to go.mod to Cargo.toml to pom.xml to pubspec.yaml to Info.plist to a Dockerfile, and in each one the version value is rewritten in place while every other byte sits still.',
    },
  ],
  [
    'Every release step is also a command, with the records built in',
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
  title: 'Why one more monorepo tool?',
  name: 'Why dispat',
  body: (
    <>
      Every major tool can topologically sort a build, but the language-agnostic ones stop before the release and the
      ones that publish serve a single ecosystem. Two situations break build-all-then-publish-all: an error in the
      middle of a run leaves half the packages shipped with recovery as a script you write, and a Docker consumer can
      only be built once its provider is published. dispat is built for exactly that mix;{' '}
      <Link to="/concepts">Concepts</Link> works both situations through end to end.
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
    title: 'The graph comes from the manifests',
    name: 'Compute',
    body: (
      <>
        <Link to="/cli/compute">
          <code>dispat compute</code>
        </Link>{' '}
        reads every manifest in the folders your <Link to="/configuration/spaces">spaces</Link> declare and proposes
        the edges and starting versions it finds, each suggestion carrying the manifest line as its evidence. Print
        them, confirm them one by one with <code>--interactive</code>, or apply them with <code>--write</code>: nobody
        transcribes a dependency graph by hand.
      </>
    ),
    media: {
      asset: 'demo-compute',
      name: 'Compute',
      label:
        'A terminal prints the spaces from dispat.yaml, then dispat compute proposes four edges and a starting version with manifest evidence, each edge drawing itself into the graph as its line prints, before --interactive confirms two suggestions and --write applies them.',
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
        executes each changed package’s script in graph order, and <code>--consumers</code> adds the changed
        packages’ consumers, so a shared-library fix tests its dependents without a shell loop, the{' '}
        <Link to="/reference/pipelines">pipeline patterns</Link> this repository tests itself with. Nothing is
        released and nothing is tagged.
      </>
    ),
    media: {
      asset: 'demo-run',
      name: 'dispat run',
      label:
        'A fix lands in utils and dispat run tests --since HEAD~1 --consumers runs the tests script on utils, then its consumers api and sdk side by side, in graph order, while web, core, docs, and mobile stay unselected and the log ends with ok=3 released=0.',
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
    title: 'Mathematics, not machinery',
    name: 'The math',
    body: (
      <>
        <Link to="/internals/architecture">The plan is a pure function</Link> of history, graph, and configuration,
        with no clocks and no state files, so the same inputs always print the same plan. A re-run recomputes the same
        transaction and executes only the legs whose record is missing, which makes released state a fixed point. Even{' '}
        <Link to="/go/ccme">the commit parser</Link> is one left-to-right pass, O(n) time in O(1) space, with no
        backtracking to feed untrusted input.
      </>
    ),
    media: {
      asset: 'demo-math',
      name: 'The math',
      label:
        'Three properties as three equations: plan = f(history, graph, config) while dispat status prints the identical plan twice; release(release(S)) = release(S) while a failed run and its re-run converge to done failed=0; and O(n) time, O(1) space while a cursor sweeps a commit message once, left to right.',
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
        ; a rejected push is the lock. A second run stops with nothing planned, built, or published, and the lock is
        given back on the way out even when a run fails, so the retry claims it cleanly.
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
  slides: Array<{title: string; name: string}>;
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
          className={styles.speedButton}
          aria-label={`Playback speed ${speed}x, click to change`}
          title="Playback speed"
          onClick={onCycleSpeed}>
          {speed}×
        </button>
      </div>
      <span className={styles.divider} />
      <div className={styles.pills} role="group" aria-label="Key features">
        {slides.map(({title, name}, i) => (
          <button
            key={title}
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
      {
        title: WHY_SLIDE.title,
        name: WHY_SLIDE.name,
        media: WHY_SLIDE.media,
        body: WHY_SLIDE.body,
      },
      ...features.map((feature) => {
        const media = FEATURE_MEDIA.get(feature.title);
        return {
          title: feature.title,
          name: media?.name ?? shortName(feature.title),
          media,
          body: <Inlines tokens={feature.body} />,
        };
      }),
      ...EXTRA_SLIDES.map((extra) => ({
        title: extra.title,
        name: extra.name,
        media: extra.media,
        body: extra.body,
      })),
    ],
    [features],
  );
  const [active, setActive] = React.useState(0);
  const [loop, setLoop] = React.useState(false);
  const [paused, setPaused] = React.useState(false);
  const [speed, setSpeed] = React.useState(1);
  // The handlers below run outside render, so they read the ref.
  const pausedRef = React.useRef(false);
  const videos = React.useRef<Array<HTMLVideoElement | null>>([]);
  // useBaseUrl is a hook, so it runs once for the root rather than once per
  // slide inside the map below.
  const base = useBaseUrl('/');

  // Autoplay attributes would start every clip at once; instead the active
  // one is played from its first frame and the rest are paused, so the
  // rotation is the only thing in motion.
  React.useEffect(() => {
    for (const [i, video] of videos.current.entries()) {
      if (!video) continue;
      if (i === active) {
        video.currentTime = 0;
        video.playbackRate = speedRef.current;
        // Autoplay of muted video is allowed everywhere, but play() still
        // returns a promise a strict browser can reject.
        if (!pausedRef.current) void video.play().catch(() => undefined);
      } else {
        video.pause();
      }
    }
    // A background tab, or a battery saver, is allowed to pause the clip
    // mid-play, and nothing else would ever start it again. Picking the
    // active clip back up when the page returns keeps the deck from coming
    // back frozen.
    const resume = () => {
      const video = videos.current[active];
      if (document.visibilityState === 'visible' && video && video.paused && !pausedRef.current) {
        void video.play().catch(() => undefined);
      }
    };
    document.addEventListener('visibilitychange', resume);
    return () => document.removeEventListener('visibilitychange', resume);
  }, [active, slides]);

  // Pause is the player's own: it freezes the active clip and, through the
  // effect below, the rotation with it.
  React.useEffect(() => {
    pausedRef.current = paused;
    const video = videos.current[active];
    if (!video) return;
    if (paused) video.pause();
    else void video.play().catch(() => undefined);
  }, [paused, active]);

  // Speed applies to whatever is playing; speedRef keeps the play effect
  // above from re-reading a stale value when the slide changes.
  const speedRef = React.useRef(1);
  React.useEffect(() => {
    speedRef.current = speed;
    const video = videos.current[active];
    if (video) video.playbackRate = speed;
  }, [speed, active]);

  // The rotation is the clips' own: a clip plays once and hands over the
  // moment it ends, straight round robin, unconditionally. Repeat-one is the
  // one way to stay on a slide (the video's loop attribute, so `ended`
  // never fires while it is on); nothing else, hover and focus included,
  // may hold the rotation, because any implicit hold reads as a looping
  // bug.
  const slidesCount = React.useRef(slides.length);
  slidesCount.current = slides.length;
  const onClipEnded = React.useCallback(() => {
    setActive((current) => (current + 1) % slidesCount.current);
  }, []);

  return (
    <div className={styles.carousel}>
      <div className={styles.frame}>
        <div className={styles.stack}>
        {slides.map(({title, media, body}, i) => (
          <div
            key={title}
            className={i === active ? `${styles.slide} ${styles.activeSlide}` : styles.slide}
            aria-hidden={i !== active}>
            {media ? (
              <video
                ref={(el) => {
                  videos.current[i] = el;
                }}
                className={styles.video}
                loop={loop}
                muted
                playsInline
                preload="auto"
                width={1920}
                height={1080}
                aria-label={media.label}
                onEnded={onClipEnded}>
                <source src={`${base}${media.asset}.webm`} type="video/webm" />
                <source src={`${base}${media.asset}.mp4`} type="video/mp4" />
              </video>
            ) : (
              <div className={styles.video} />
            )}
            <Heading as="h3" className={styles.slideTitle}>
              {title}
            </Heading>
            <div className={styles.band}>
              <p className={styles.bandBody}>{body}</p>
            </div>
          </div>
        ))}
        </div>
        <DeckControls
          slides={slides}
          active={active}
          onSelect={setActive}
          loop={loop}
          onToggleLoop={() => setLoop((l) => !l)}
          playing={!paused}
          onTogglePlay={() => setPaused((p) => !p)}
          speed={speed}
          onCycleSpeed={() => setSpeed((s) => ({1: 1.5, 1.5: 2, 2: 0.5, 0.5: 1}[s] ?? 1))}
        />
      </div>
    </div>
  );
}
