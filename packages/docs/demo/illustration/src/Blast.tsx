import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {colors} from './theme';
import {NodeView, SceneTerminal, SceneTitle, TermRow, cmdRow, outRow, typeIO, INF, msg, kv} from './components';
import {Pulse, Stage} from './Stage';

// The short cut, eighteen seconds at Root.tsx's twenty frames per second:
// the same commit planned twice. Without carets only core releases; amended
// to feat(core)^^ the whole consumer closure joins the plan. utils, a
// provider, stays unchanged either way, and sdk, docs, and mobile never
// enter the plan at all: seven packages, and at most three releasing.
//
// The `clip` variant is the landing page's render: no title and the commit
// line sits lower, because the page overlays the feature's own text over the
// top strip.

function view(id: string, f: number): NodeView {
  const changedCore = f > 70;
  const propagated = {api: f > 215, web: f > 245};
  switch (id) {
    case 'core':
      return changedCore ? {state: 'changed', bumped: f > 82} : {state: 'idle'};
    case 'api':
      if (propagated.api) return {state: 'changed', bumped: f > 228};
      break;
    case 'web':
      if (propagated.web) return {state: 'changed', bumped: f > 258};
      break;
    default:
      break;
  }
  // Everyone the plan leaves alone: utils untouched by the caretless fix,
  // and the bystanders the closure never reaches.
  return f > 95 ? {state: 'unchanged'} : {state: 'idle'};
}

const IDS = ['core', 'utils', 'api', 'web', 'sdk', 'docs', 'mobile'] as const;

// Propagation as edge state: the caret turns the consumer edges green one
// hop at a time, and they stay lit as the plan's path. The utils edges
// never light up.
const pulses: Pulse[] = [
  {edge: 0, start: 185}, // core -> api
  {edge: 2, start: 215}, // api -> web
];

// The scene's terminal is the whole story: the caretless commit, a dry run
// answering with one package releasing, the amend that adds the carets, and
// the second dry run printing the propagation as the edges light.
const rows: TermRow[] = [
  cmdRow(28, 'git commit -m "feat(core): add streaming api"', 0.7),
  cmdRow(84, 'dispat status'),
  outRow(104, [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('bump', 'minor'), ...kv('version', '"1.4.2 -> 1.5.0"')]),
  outRow(108, [{text: '  unchanged ', color: colors.dim}, ...kv('package', 'utils'), ...kv('version', '2.0.3')]),
  outRow(114, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '1')]),
  cmdRow(148, 'git commit --amend -m "feat(core)^^: add streaming api"', 0.7),
  cmdRow(196, 'dispat status'),
  outRow(224, [...INF, msg('● changed'), ...kv('package', 'api'), ...kv('reason', '"propagated from core"'), ...kv('version', '"0.8.2 -> 0.8.3"')]),
  outRow(252, [...INF, msg('● changed'), ...kv('package', 'web'), ...kv('reason', '"propagated from api"'), ...kv('version', '"2.1.0 -> 2.1.1"')]),
  outRow(262, [{text: '  unchanged ', color: colors.dim}, ...kv('package', 'utils'), ...kv('version', '2.0.3')]),
  outRow(272, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '3')]),
];

export const BLAST_DURATION = 360;

export const Blast: React.FC<{clip?: boolean}> = ({clip = false}) => {
  const f = useCurrentFrame();
  const views = Object.fromEntries(IDS.map((id) => [id, view(id, f)]));
  const graphIn = interpolate(f, [0, 20], [0, 1], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });
  const graphOut = interpolate(f, [350, 360], [1, 0], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });
  return (
    <Stage views={views} pulses={pulses} graphOpacity={graphIn * graphOut} terminal={<SceneTerminal rows={rows} f={f} />}>
      {!clip && <SceneTitle text="The commit decides the blast radius." progress={typeIO(f, 8, 36, 344)} />}
    </Stage>
  );
};
