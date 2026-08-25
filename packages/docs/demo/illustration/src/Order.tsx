import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {colors} from './theme';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';

// The graph-not-a-list claim on its own timeline, twenty-five seconds at
// Root.tsx's twenty frames per second: the one clip where the run completes,
// under the stage budgets `concurrency: [2, 1]`. Both providers build at
// once (build x2), utils then queues for the single publish slot behind
// core (publish x1), and api's isBuildWaitingPublish keeps its build parked
// until core's publish lands.
// The master storyline fails api on purpose (that failure is the
// self-healing slide's story), so this composition is not a cut of it. It
// opens on the plan, all four marked changed, then runs it: core and utils
// build and publish in parallel, api waits for core@1.5.0, rewrites its
// Dockerfile, builds and publishes, web follows, and the run ends with all
// four published and the path lit end to end. sdk, docs, and mobile sit it
// out, unchanged.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

// The plan beat: everything before RUN is the four cards standing marked
// `● changed`, so the run visibly starts from a plan rather than mid-air.
const RUN = 56;

function coreView(f: number): NodeView {
  if (f < RUN) return {state: 'changed', bumped: true};
  if (f < 126) return {state: 'building', bumped: true, progress: bar(f, RUN, 124)};
  if (f < 164) return {state: 'publishing', bumped: true, progress: bar(f, 126, 162)};
  return {state: 'published', bumped: true, tag: 'core@1.5.0'};
}

function utilsView(f: number): NodeView {
  if (f < RUN) return {state: 'changed', bumped: true};
  if (f < 140) return {state: 'building', bumped: true, progress: bar(f, RUN, 138)};
  // publish x1: the build is done, but core holds the only publish slot.
  if (f < 166) return {state: 'waiting', bumped: true, note: 'publish ×1: waits for core'};
  if (f < 204) return {state: 'publishing', bumped: true, progress: bar(f, 166, 202)};
  return {state: 'published', bumped: true, tag: 'utils@2.0.4'};
}

function apiView(f: number): NodeView {
  if (f < RUN + 4) return {state: 'changed', bumped: true};
  if (f < 166) return {state: 'waiting', bumped: true, note: 'isBuildWaitingPublish: core@1.5.0'};
  if (f < 196) return {state: 'building', bumped: true, rewrite: bar(f, 166, 194), note: 'Dockerfile rewritten'};
  if (f < 282) return {state: 'building', bumped: true, rewrite: 1, progress: bar(f, 196, 280)};
  if (f < 316) return {state: 'publishing', bumped: true, rewrite: 1, progress: bar(f, 282, 314)};
  return {state: 'published', bumped: true, rewrite: 1, tag: 'api@0.8.3'};
}

function webView(f: number): NodeView {
  if (f < RUN + 8) return {state: 'changed', bumped: true};
  if (f < 316) return {state: 'waiting', bumped: true, note: 'waits for api'};
  if (f < 372) return {state: 'building', bumped: true, progress: bar(f, 316, 370)};
  if (f < 404) return {state: 'publishing', bumped: true, progress: bar(f, 372, 402)};
  return {state: 'published', bumped: true, tag: 'web@2.1.1'};
}

// Each edge lights at the moment its provider's publish lands, so the green
// path advances exactly as the release does.
const pulses: Pulse[] = [
  {edge: 0, start: 162}, // core published; api may build
  {edge: 1, start: 202}, // utils published
  {edge: 2, start: 314}, // api published; web may build
];

export const ORDER_DURATION = 492;

// The scene's terminal, in step with the graph: the command that starts the
// run, then every log line at the moment the diagram shows the state it
// reports, down to the summary.
const rows: TermRow[] = [
  cmdRow(6, 'dispat'),
  outRow(28, [{text: '  unchanged ', color: colors.dim}, ...kv('package', 'sdk'), ...kv('version', '0.3.1')]),
  outRow(32, [...INF, msg('release plan ready'), ...kv('concurrency', '"[2, 1]"'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '4')]),
  outRow(62, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', '1.5.0')]),
  outRow(68, [...INF, msg('build started'), ...kv('package', 'utils'), ...kv('stage', 'build'), ...kv('version', '2.0.4')]),
  outRow(168, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0'), ...kv('version', '1.5.0')]),
  outRow(200, [...INF, msg('build started'), ...kv('package', 'api'), ...kv('stage', 'build'), ...kv('version', '0.8.3')]),
  outRow(208, [...INF, msg('published'), ...kv('package', 'utils'), ...kv('tag', 'utils@2.0.4'), ...kv('version', '2.0.4')]),
  outRow(320, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3'), ...kv('version', '0.8.3')]),
  outRow(326, [...INF, msg('build started'), ...kv('package', 'web'), ...kv('stage', 'build'), ...kv('version', '2.1.1')]),
  outRow(408, [...INF, msg('published'), ...kv('package', 'web'), ...kv('tag', 'web@2.1.1'), ...kv('version', '2.1.1')]),
  outRow(416, [...INF, msg('done'), ...kv('cancelled', '0'), ...kv('failed', '0'), ...kv('published', '4'), ...kv('skipped', '0'), ...kv('unchanged', '3')]),
];

export const Order: React.FC = () => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    core: coreView(f),
    utils: utilsView(f),
    api: apiView(f),
    web: webView(f),
    sdk: {state: 'unchanged'},
    docs: {state: 'unchanged'},
    mobile: {state: 'unchanged'},
  };
  const graphOpacity =
    bar(f, 0, 10) *
    interpolate(f, [ORDER_DURATION - 10, ORDER_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <Stage views={views} pulses={pulses} graphOpacity={graphOpacity} terminal={<SceneTerminal rows={rows} f={f} />} />
  );
};
