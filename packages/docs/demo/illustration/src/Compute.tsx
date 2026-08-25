import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {colors} from './theme';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {Stage} from './Stage';

// The compute claim, eighteen seconds at Root.tsx's twenty frames per
// second: nobody transcribes the dependency graph. The config declares the
// spaces' folders, `dispat compute` reads every package's manifests and
// prints one suggestion per detected edge with the manifest line as the
// evidence, each edge drawing itself into the graph as its line prints. Then
// `--interactive` answers two suggestions by hand and `--write` applies the
// rest.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const add = (t: string): {text: string; color?: string; weight?: number}[] => [
  {text: '+ add     ', color: colors.green, weight: 700},
  {text: t},
];

const rows: TermRow[] = [
  cmdRow(8, 'cat dispat.yaml'),
  outRow(30, [{text: 'spaces:', color: colors.cyan}]),
  outRow(34, [{text: '  apps:', color: colors.cyan}]),
  outRow(38, [{text: '    path: ', color: colors.cyan}, {text: 'packages'}]),
  cmdRow(56, 'dispat compute'),
  outRow(84, [...add('core -> api    '), {text: 'packages/api/Dockerfile: FROM acme/core:1.4.2', color: colors.dim}]),
  outRow(102, [...add('utils -> api   '), {text: 'packages/api/go.mod: require github.com/acme/utils', color: colors.dim}]),
  outRow(120, [...add('api -> web     '), {text: 'packages/web/package.json: "@acme/api"', color: colors.dim}]),
  outRow(138, [...add('utils -> sdk   '), {text: 'packages/sdk/Cargo.toml: acme-utils', color: colors.dim}]),
  outRow(160, [
    {text: '+ initial ', color: colors.green, weight: 700},
    {text: 'core 1.4.2     '},
    {text: 'packages/core/package.json declares 1.4.2; no release tag yet', color: colors.dim},
  ]),
  cmdRow(196, 'dispat compute --interactive'),
  outRow(230, [{text: '+ add     core -> api        apply? ', color: colors.dim}, {text: 'y', color: colors.green, weight: 700}]),
  outRow(248, [{text: '+ initial core 1.4.2         apply? ', color: colors.dim}, {text: 'y', color: colors.green, weight: 700}]),
  cmdRow(272, 'dispat compute --write'),
  outRow(300, [...INF, msg('written'), ...kv('edges', '4'), ...kv('initials', '1'), ...kv('file', 'dispat.yaml')]),
];

// Each edge draws itself the moment its `+ add` line prints.
const EDGE_AT = [84, 102, 120, 138];

export const COMPUTE_DURATION = 364;

export const Compute: React.FC = () => {
  const f = useCurrentFrame();
  // The cards stand idle the whole time: compute reads, it does not release.
  const views: Record<string, NodeView> = Object.fromEntries(
    ['core', 'api', 'web', 'utils', 'sdk', 'docs', 'mobile'].map((id, i) => [
      id,
      {state: 'idle', opacity: bar(f, 6 + i * 4, 20 + i * 4)} as NodeView,
    ]),
  );
  const graphOpacity =
    bar(f, 0, 10) *
    interpolate(f, [COMPUTE_DURATION - 10, COMPUTE_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <Stage
      views={views}
      edgeDraw={(i) => bar(f, EDGE_AT[i], EDGE_AT[i] + 14)}
      graphOpacity={graphOpacity}
      terminal={<SceneTerminal rows={rows} f={f} />}
    />
  );
};
