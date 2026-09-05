import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {colors} from './theme';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';
import {releaseGraph} from './graph';

// The graph-not-a-list claim on its own timeline, twenty-five seconds at
// Root.tsx's twenty frames per second: the one clip where the run completes,
// under the stage budgets `concurrency: [4, 4]`. The independent providers
// core and utils build and publish together. sdk may build from the local npm
// workspace as soon as utils' build finishes. api consumes the Go module core
// from its published location, so core explicitly sets isBuildWaitingPublish
// and api stays parked until core's publish lands.
// The master storyline fails api on purpose (that failure is the
// self-healing slide's story), so this composition is not a cut of it. It
// opens on the plan, all five marked changed, then runs it. api waits for
// core@1.5.0, while sdk's build overlaps utils' publish; web follows api. The
// run ends with all five published. docs and mobile sit it out, unchanged.
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
  if (f < 140) return {state: 'building', bumped: true, progress: bar(f, RUN, 138)};
  if (f < 206) return {state: 'publishing', bumped: true, progress: bar(f, 140, 204)};
  return {state: 'published', bumped: true, tag: 'core@1.5.0'};
}

function utilsView(f: number): NodeView {
  if (f < RUN) return {state: 'changed', bumped: true};
  if (f < 140) return {state: 'building', bumped: true, progress: bar(f, RUN, 138)};
  if (f < 206) return {state: 'publishing', bumped: true, progress: bar(f, 140, 204)};
  return {state: 'published', bumped: true, tag: 'utils@2.0.4'};
}

function apiView(f: number): NodeView {
  if (f < RUN + 4) return {state: 'changed', bumped: true};
  if (f < 206) return {state: 'waiting', bumped: true, note: 'configured: wait for core publish'};
  if (f < 286) return {state: 'building', bumped: true, progress: bar(f, 206, 284), note: 'downloads core@v1.5.0'};
  if (f < 332) return {state: 'publishing', bumped: true, progress: bar(f, 286, 330)};
  return {state: 'published', bumped: true, tag: 'api@0.8.3'};
}

function sdkView(f: number): NodeView {
  if (f < RUN + 4) return {state: 'changed', bumped: true};
  if (f < 140) return {state: 'waiting', bumped: true, note: 'waits for utils build'};
  if (f < 220) return {state: 'building', bumped: true, progress: bar(f, 140, 218), note: 'uses local npm workspace'};
  if (f < 266) return {state: 'publishing', bumped: true, progress: bar(f, 220, 264)};
  return {state: 'published', bumped: true, tag: 'sdk@0.3.2'};
}

function webView(f: number): NodeView {
  if (f < RUN + 8) return {state: 'changed', bumped: true};
  if (f < 286) return {state: 'waiting', bumped: true, note: 'waits for api build'};
  if (f < 326) return {state: 'building', bumped: true, progress: bar(f, 286, 324)};
  if (f < 332) return {state: 'waiting', bumped: true, note: 'publish waits for api'};
  if (f < 382) return {state: 'publishing', bumped: true, progress: bar(f, 332, 380)};
  return {state: 'published', bumped: true, tag: 'web@2.1.1'};
}

// Each edge lights at the moment its configured prerequisite lands: utils'
// local build for sdk, but a completed core publish for api.
const pulses: Pulse[] = [
  {edge: 0, start: 204}, // core published; api may build
  {edge: 2, start: 138}, // utils built; sdk may build from the workspace
  {edge: 1, start: 284}, // api built; web may build while api publishes
];

export const ORDER_DURATION = 492;

// The scene's terminal, in step with the graph: the command that starts the
// run, then every log line at the moment the diagram shows the state it
// reports, down to the summary.
const rows: TermRow[] = [
  cmdRow(6, 'dispat'),
  outRow(28, [{text: '  unchanged ', color: colors.dim}, ...kv('package', 'docs'), ...kv('version', '1.1.0')]),
  outRow(32, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '5')]),
  outRow(62, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', '1.5.0')]),
  outRow(68, [...INF, msg('build started'), ...kv('package', 'utils'), ...kv('stage', 'build'), ...kv('version', '2.0.4')]),
  outRow(144, [...INF, msg('build started'), ...kv('package', 'sdk'), ...kv('stage', 'build'), ...kv('version', '0.3.2')]),
  outRow(210, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0'), ...kv('version', '1.5.0')]),
  outRow(216, [...INF, msg('published'), ...kv('package', 'utils'), ...kv('tag', 'utils@2.0.4'), ...kv('version', '2.0.4')]),
  outRow(222, [...INF, msg('build started'), ...kv('package', 'api'), ...kv('stage', 'build'), ...kv('version', '0.8.3')]),
  outRow(270, [...INF, msg('published'), ...kv('package', 'sdk'), ...kv('tag', 'sdk@0.3.2'), ...kv('version', '0.3.2')]),
  outRow(292, [...INF, msg('build started'), ...kv('package', 'web'), ...kv('stage', 'build'), ...kv('version', '2.1.1')]),
  outRow(336, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3'), ...kv('version', '0.8.3')]),
  outRow(388, [...INF, msg('published'), ...kv('package', 'web'), ...kv('tag', 'web@2.1.1'), ...kv('version', '2.1.1')]),
  outRow(396, [...INF, msg('done'), ...kv('cancelled', '0'), ...kv('failed', '0'), ...kv('published', '5'), ...kv('skipped', '0'), ...kv('unchanged', '2')]),
];

export const Order: React.FC = () => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    core: coreView(f),
    utils: utilsView(f),
    api: apiView(f),
    web: webView(f),
    sdk: sdkView(f),
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
    <Stage views={views} graphPkgs={releaseGraph.pkgs} graphEdges={releaseGraph.edges} pulses={pulses} edgeLabels graphOpacity={graphOpacity} terminal={<SceneTerminal rows={rows} f={f} />} />
  );
};
