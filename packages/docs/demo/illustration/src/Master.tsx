import React from 'react';
import {interpolate, Sequence, useCurrentFrame} from 'remotion';
import {NodeView, SceneTerminal, SceneTitle, TermRow, cmdRow, outRow, typeIO, INF, ERR, msg, kv} from './components';
import {Pulse, Stage} from './Stage';

// The story, forty-five seconds at Root.tsx's twenty frames per second:
//   S1    0-140   the graph comes from the manifests
//   S2  140-330   two commits decide the blast radius
//   S3  330-560   ordered, parallel build and publish; api waits for core
//   S4  560-720   api fails, web is skipped, core and utils still shipped
//   S5  720-900   the re-run finishes exactly what the first run owed
//
// Seven packages, four in the story: sdk, docs, and mobile sit in the graph
// the whole time and never join the plan, because most of a monorepo is not
// in any given release.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

function coreView(f: number): NodeView {
  if (f < 180) return {state: 'idle'};
  if (f < 350) return {state: 'changed', bumped: f > 188};
  if (f < 420) return {state: 'building', bumped: true, progress: bar(f, 350, 418)};
  if (f < 470) return {state: 'publishing', bumped: true, progress: bar(f, 420, 468)};
  if (f < 735) return {state: 'published', bumped: true, tag: 'core@1.5.0'};
  return {state: 'unchanged', bumped: true, tag: 'core@1.5.0'};
}

function utilsView(f: number): NodeView {
  if (f < 262) return {state: 'idle'};
  if (f < 350) return {state: 'changed', bumped: f > 270};
  if (f < 420) return {state: 'building', bumped: true, progress: bar(f, 350, 418)};
  if (f < 470) return {state: 'publishing', bumped: true, progress: bar(f, 420, 468)};
  if (f < 735) return {state: 'published', bumped: true, tag: 'utils@2.0.4'};
  return {state: 'unchanged', bumped: true, tag: 'utils@2.0.4'};
}

function apiView(f: number): NodeView {
  if (f < 215) return {state: 'idle'};
  if (f < 350) return {state: 'changed', bumped: f > 230};
  if (f < 470) return {state: 'waiting', bumped: true, note: 'waits for core@1.5.0'};
  if (f < 510) return {state: 'building', bumped: true, rewrite: bar(f, 470, 508), note: 'Dockerfile rewritten'};
  if (f < 580) return {state: 'building', bumped: true, rewrite: 1, progress: bar(f, 510, 590)};
  if (f < 735) return {state: 'failed', bumped: true, rewrite: 1, progress: 0.85, note: 'tests failed'};
  if (f < 755) return {state: 'catchup', bumped: true, rewrite: 1};
  if (f < 795) return {state: 'building', bumped: true, rewrite: 1, progress: bar(f, 755, 793)};
  if (f < 815) return {state: 'publishing', bumped: true, rewrite: 1, progress: bar(f, 795, 813)};
  return {state: 'published', bumped: true, rewrite: 1, tag: 'api@0.8.3'};
}

function webView(f: number): NodeView {
  if (f < 245) return {state: 'idle'};
  if (f < 350) return {state: 'changed', bumped: f > 258};
  if (f < 600) return {state: 'waiting', bumped: true, note: 'waits for api'};
  if (f < 735) return {state: 'skipped', bumped: true, note: 'provider failed'};
  if (f < 815) return {state: 'waiting', bumped: true, note: 'waits for api@0.8.3'};
  if (f < 845) return {state: 'building', bumped: true, progress: bar(f, 815, 843)};
  if (f < 865) return {state: 'publishing', bumped: true, progress: bar(f, 845, 863)};
  return {state: 'published', bumped: true, tag: 'web@2.1.1'};
}

/**
 * sdk, docs, and mobile: in the graph, never in the plan. They turn
 * `unchanged` when the plan is ready and stay that way through both runs,
 * which is the whole point of having them on the stage.
 */
function bystanderView(f: number): NodeView {
  return f < 295 ? {state: 'idle'} : {state: 'unchanged'};
}

// The edges tell the story twice. In S2 the propagation lights the path the
// bump will take, and the glow drops just before the run starts. From S3 on
// the run relights each edge at the moment its provider actually publishes,
// so the path advances with the release: core and utils light api's edges
// when they publish, api's failure leaves the web edge dark through S4, and
// the re-run's api publish is what finally lights it. utils -> sdk never
// lights: sdk is not in the plan.
const pulses: Pulse[] = [
  {edge: 0, start: 185, off: 324}, // planning: core -> api
  {edge: 2, start: 215, off: 324}, // planning: api -> web
  {edge: 0, start: 466}, // core published; api may build
  {edge: 1, start: 466}, // utils published
  {edge: 2, start: 813}, // api published on the re-run; web may build
];

const titles: Array<{text: string; in: [number, number]; out: number}> = [
  {text: 'A package is a folder. The graph comes from its manifests.', in: [15, 52], out: 140},
  {text: 'The commit decides the blast radius.', in: [150, 178], out: 330},
  {text: 'Builds and publishes in dependency order, in parallel.', in: [340, 375], out: 560},
  {text: 'An error in the middle stays contained.', in: [565, 592], out: 720},
  {text: 'Running it again finishes what the run still owed.', in: [725, 758], out: 885},
];

// The scene's terminal, in step with the graph: the plan lines print as the
// cards take their marks, each run opens with the command that starts it,
// and every log line lands at the moment the diagram shows the state it
// reports.
const rows: TermRow[] = [
  cmdRow(144, 'git commit -m "feat(core)^^: add streaming api"', 0.7),
  outRow(192, [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('bump', 'minor'), ...kv('version', '"1.4.2 -> 1.5.0"')]),
  cmdRow(228, 'git commit -m "fix(utils): close file handle leak"', 0.7),
  outRow(272, [...INF, msg('● changed'), ...kv('dueToProviders', '["core"]'), ...kv('package', 'api'), ...kv('reason', '"propagated from core"'), ...kv('version', '"0.8.2 -> 0.8.3"')]),
  outRow(280, [...INF, msg('● changed'), ...kv('package', 'utils'), ...kv('bump', 'patch'), ...kv('version', '"2.0.3 -> 2.0.4"')]),
  outRow(300, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '4')]),
  cmdRow(334, 'dispat'),
  outRow(362, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', '1.5.0')]),
  outRow(470, [...INF, msg('published'), ...kv('package', 'utils'), ...kv('tag', 'utils@2.0.4'), ...kv('version', '2.0.4')]),
  outRow(478, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0'), ...kv('version', '1.5.0')]),
  outRow(590, [...ERR, msg('build script failed'), ...kv('error', '"exit status 1"'), ...kv('package', 'api'), ...kv('stage', 'build')]),
  outRow(664, [...INF, msg('done'), ...kv('cancelled', '0'), ...kv('failed', '1'), ...kv('published', '2'), ...kv('skipped', '1'), ...kv('unchanged', '3')]),
  cmdRow(724, 'dispat'),
  outRow(748, [...INF, msg('↻ catch-up'), ...kv('package', 'api'), ...kv('reason', '"catch-up from core"'), ...kv('version', '"0.8.2 -> 0.8.3"')]),
  outRow(820, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3'), ...kv('version', '0.8.3')]),
  outRow(868, [...INF, msg('published'), ...kv('package', 'web'), ...kv('tag', 'web@2.1.1'), ...kv('version', '2.1.1')]),
  outRow(874, [...INF, msg('done'), ...kv('cancelled', '0'), ...kv('failed', '0'), ...kv('published', '2'), ...kv('skipped', '0'), ...kv('unchanged', '5')]),
];

// The fade-in order of the cards in S1, providers before consumers.
const REVEAL = ['core', 'utils', 'api', 'web', 'sdk', 'docs', 'mobile'] as const;

export const Master: React.FC<{titles?: boolean}> = ({titles: withTitles = true}) => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    core: coreView(f),
    utils: utilsView(f),
    api: apiView(f),
    web: webView(f),
    sdk: bystanderView(f),
    docs: bystanderView(f),
    mobile: bystanderView(f),
  };
  // S1: cards fade in with a manifest glow, edges draw afterwards.
  for (const [i, id] of REVEAL.entries()) {
    const start = 10 + i * 9;
    views[id].opacity = interpolate(f, [start, start + 16], [0, 1], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  }
  const graphOpacity = interpolate(f, [888, 900], [1, 0], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });

  return (
    <Stage
      views={views}
      pulses={pulses}
      edgeDraw={(i) => bar(f, 60 + i * 14, 110 + i * 14)}
      graphOpacity={graphOpacity}
      terminal={<SceneTerminal rows={rows} f={f} />}
    >
      {withTitles &&
        titles.map((t, i) => (
          <SceneTitle key={i} text={t.text} progress={typeIO(f, t.in[0], t.in[1], t.out)} />
        ))}

    </Stage>
  );
};

// One cut of the story above, also its own composition: Heal (S4 and S5,
// one take) illustrates the self-healing claim on the landing page's
// carousel. The other features are illustrated by their own compositions;
// in particular Order is not a cut of this timeline, because this story
// fails api on purpose and the graph-not-a-list slide wants the run that
// completes. The frame window is the one in the storyboard comment at the
// top of this file, so the cut can never drift from the master it is cut
// from.
export type SceneCut = {
  /** Composition id, and the file name of the render under out/. */
  id: string;
  /** Base name of the committed webm and mp4 pair in imgs/. */
  asset: string;
  from: number;
  to: number;
};

export const SCENES: SceneCut[] = [
  {id: 'Heal', asset: 'demo-heal', from: 560, to: 900},
];
export const HEAL_DURATION = SCENES[0].to - SCENES[0].from;

// A negative Sequence offset starts the master timeline mid-story, so a scene
// is a window onto the same continuous animation rather than a re-staging:
// whatever state the earlier scenes left on the graph is exactly what the cut
// opens on. No titles: the landing page crops the clip's top strip away and
// shows the feature text under the clip.
export const Scene: React.FC<{from: number}> = ({from}) => {
  const f = useCurrentFrame();
  // The cut opens mid-story, so it fades itself in; the master's own ending
  // provides the fade out.
  const opacity = interpolate(f, [0, 8], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  return (
    <div style={{opacity}}>
      <Sequence from={-from}>
        <Master titles={false} />
      </Sequence>
    </div>
  );
};

/** Browser-player entry for the self-healing cut of the master timeline. */
export const Heal: React.FC = () => <Scene from={560} />;
