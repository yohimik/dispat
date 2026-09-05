import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';
import fixture from '../../fixtures/run/expected.json';

// The dispat-run example, twenty-three seconds at Root.tsx's twenty frames per
// second: scripts for exactly what changed, nothing released or tagged.
// The default window is the same unreleased commit window used by status and
// release. Here `--since HEAD~1` replaces it with exactly the last commit;
// `--consumers` then expands downstream after filtering. A fix lands in utils,
// so the command selects
// utils and adds its direct and transitive consumers: api, sdk, and web.
// utils runs first, api and sdk can then run side by side, and web follows
// api. Only core, docs, and mobile remain unselected.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

function utilsView(f: number): NodeView {
  if (f < 106) return {state: 'idle'};
  if (f < 214) return {state: 'changed'};
  if (f < 252) return {state: 'running', progress: bar(f, 214, 250)};
  return {state: 'changed', note: 'tests ok'};
}

function consumerView(f: number, from: number, to: number, note: string): NodeView {
  if (f < 190) return {state: 'idle'};
  if (f < from) return {state: 'waiting', note};
  if (f < to) return {state: 'running', progress: bar(f, from, to - 2), note};
  return {state: 'running', progress: 1, note: 'tests ok'};
}

/** Not selected: no window covers them and the expansion never reaches them. */
function outsiderView(f: number): NodeView {
  return f < 190 ? {state: 'idle'} : {state: 'unchanged'};
}

// The graph order as edge state: an edge lights when its provider's script
// finishes and its consumers may start; api finishing then releases web.
const pulses: Pulse[] = [
  {edge: 1, start: 250}, // utils -> api
  {edge: 3, start: 250}, // utils -> sdk
  {edge: 2, start: 326}, // api -> web
];

const rows: TermRow[] = [
  cmdRow(8, `git commit --quiet -m "${fixture.commit}"`),
  cmdRow(112, fixture.command),
  outRow(190, [...INF, msg('● changed'), ...kv('package', 'utils'), ...kv('bump', 'patch'), ...kv('reason', 'direct')]),
  outRow(204, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '1')]),
  outRow(214, [...INF, msg('run script started'), ...kv('package', 'utils'), ...kv('stage', 'run:tests')]),
  outRow(256, [...INF, msg('run script started'), ...kv('package', 'api'), ...kv('stage', 'run:tests')]),
  outRow(260, [...INF, msg('run script started'), ...kv('package', 'sdk'), ...kv('stage', 'run:tests')]),
  outRow(330, [...INF, msg('run script started'), ...kv('package', 'web'), ...kv('stage', 'run:tests')]),
  outRow(408, [...INF, msg('run finished'), ...kv('failed', String(fixture.outcomes.failed)), ...kv('ran', String(fixture.outcomes.ok)), ...kv('script', 'tests'), ...kv('skipped', '0')]),
];

export const RUN_DURATION = 460;

export const Run: React.FC = () => {
  const f = useCurrentFrame();
  const views: Record<string, NodeView> = {
    utils: utilsView(f),
    api: consumerView(f, 256, 320, 'consumer of utils'),
    sdk: consumerView(f, 256, 328, 'consumer of utils'),
    web: consumerView(f, 330, 388, 'transitive consumer via api'),
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
