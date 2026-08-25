import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';

// The dispat-run claim, seventeen seconds at Root.tsx's twenty frames per
// second: scripts for exactly what changed, nothing released or tagged. A
// fix lands in utils, `dispat run tests --since HEAD~1 --consumers` selects
// utils and adds its consumers, api and sdk, and the tests run in graph
// order: utils first, then api and sdk side by side. Everyone else, web
// included, is simply not selected.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

function utilsView(f: number): NodeView {
  if (f < 44) return {state: 'idle'};
  if (f < 106) return {state: 'changed'};
  if (f < 168) return {state: 'running', progress: bar(f, 106, 166)};
  return {state: 'changed', note: 'tests ok'};
}

function consumerView(f: number, from: number, to: number, note: string): NodeView {
  if (f < 100) return {state: 'idle'};
  if (f < from) return {state: 'waiting', note};
  if (f < to) return {state: 'running', progress: bar(f, from, to - 2), note};
  return {state: 'running', progress: 1, note: 'tests ok'};
}

/** Not selected: no window covers them and the expansion never reaches them. */
function outsiderView(f: number): NodeView {
  return f < 100 ? {state: 'idle'} : {state: 'unchanged'};
}

// The graph order as edge state: an edge lights when its provider's script
// finishes and its consumers may start. The api -> web edge never lights:
// web is not a consumer of anything the commit changed.
const pulses: Pulse[] = [
  {edge: 1, start: 166}, // utils -> api
  {edge: 3, start: 166}, // utils -> sdk
];

const rows: TermRow[] = [
  cmdRow(8, 'git commit -m "fix(utils): close file handle leak"', 0.7),
  cmdRow(56, 'dispat run tests --since HEAD~1 --consumers', 0.8),
  outRow(102, [...INF, msg('run'), ...kv('script', 'tests'), ...kv('selected', '"utils +2 consumers"'), ...kv('packages', '3')]),
  outRow(172, [...INF, msg('script ok'), ...kv('package', 'utils'), ...kv('script', 'tests')]),
  outRow(246, [...INF, msg('script ok'), ...kv('package', 'api'), ...kv('script', 'tests')]),
  outRow(254, [...INF, msg('script ok'), ...kv('package', 'sdk'), ...kv('script', 'tests')]),
  outRow(266, [...INF, msg('done'), ...kv('ok', '3'), ...kv('failed', '0'), ...kv('unselected', '4'), ...kv('released', '0')]),
];

export const RUN_DURATION = 344;

export const Run: React.FC = () => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    utils: utilsView(f),
    api: consumerView(f, 172, 244, 'consumer of utils'),
    sdk: consumerView(f, 172, 252, 'consumer of utils'),
    web: outsiderView(f),
    core: outsiderView(f),
    docs: outsiderView(f),
    mobile: outsiderView(f),
  };
  const graphOpacity =
    bar(f, 0, 10) *
    interpolate(f, [RUN_DURATION - 10, RUN_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <Stage views={views} pulses={pulses} graphOpacity={graphOpacity} terminal={<SceneTerminal rows={rows} f={f} />} />
  );
};
