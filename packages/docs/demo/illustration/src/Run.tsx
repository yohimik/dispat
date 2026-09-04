import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';
import fixture from '../../fixtures/run/expected.json';

// The dispat-run claim, seventeen seconds at Root.tsx's twenty frames per
// second: scripts for exactly what changed, nothing released or tagged. A
// fix lands in utils, `dispat run tests --since HEAD~1 --consumers` selects
// utils and adds its direct and transitive consumers: api, sdk, and web.
// utils runs first, api and sdk can then run side by side, and web follows
// api. Only core, docs, and mobile remain unselected.
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
// finishes and its consumers may start; api finishing then releases web.
const pulses: Pulse[] = [
  {edge: 1, start: 166}, // utils -> api
  {edge: 3, start: 166}, // utils -> sdk
  {edge: 2, start: 250}, // api -> web
];

const rows: TermRow[] = [
  cmdRow(8, `git commit -m "${fixture.commit}"`, 0.7),
  cmdRow(56, fixture.command, 0.8),
  outRow(102, [...INF, msg('run'), ...kv('script', 'tests'), ...kv('selected', '"utils +3 consumers"'), ...kv('packages', String(fixture.selected.length))]),
  outRow(172, [...INF, msg('script ok'), ...kv('package', 'utils'), ...kv('script', 'tests')]),
  outRow(246, [...INF, msg('script ok'), ...kv('package', 'api'), ...kv('script', 'tests')]),
  outRow(254, [...INF, msg('script ok'), ...kv('package', 'sdk'), ...kv('script', 'tests')]),
  outRow(320, [...INF, msg('script ok'), ...kv('package', 'web'), ...kv('script', 'tests')]),
  outRow(332, [...INF, msg('done'), ...kv('ok', String(fixture.outcomes.ok)), ...kv('failed', String(fixture.outcomes.failed)), ...kv('unselected', String(fixture.outcomes.unselected)), ...kv('released', String(fixture.outcomes.released))]),
];

export const RUN_DURATION = 390;

export const Run: React.FC = () => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    utils: utilsView(f),
    api: consumerView(f, 172, 244, 'consumer of utils'),
    sdk: consumerView(f, 172, 252, 'consumer of utils'),
    web: consumerView(f, 252, 318, 'transitive consumer via api'),
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
