import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {colors} from './theme';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow} from './components';
import {Stage} from './Stage';
import {useDemoLayout} from './layout';
import fixture from '../../fixtures/compute/expected.json';
import {Edge, Pkg} from './graph';

// The compute example, fifteen seconds at Root.tsx's twenty frames per
// second: the config declares the space folder, `dispat compute` reads the
// package manifests, and its preview names the npm workspace edge and both
// initial versions with evidence. Preview is read-only. The separate
// `--write` invocation applies the three reviewed changes. A disposable
// fixture exercises these exact lines with the released CLI.
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
  outRow(4, [{text: '# preview manifest evidence; write only after review', color: colors.dim}]),
  cmdRow(12, fixture.command),
  outRow(48, [...add('web -> core (dependencies)  '), {text: 'package.json: "@acme/core": "workspace:*"', color: colors.dim}]),
  outRow(76, [
    {text: '+ initial ', color: colors.green, weight: 700},
    {text: 'core 1.4.2     '},
    {text: 'packages/core/package.json declares 1.4.2; no release tag yet', color: colors.dim},
  ]),
  outRow(96, [
    {text: '+ initial ', color: colors.green, weight: 700},
    {text: 'web 2.1.0      '},
    {text: 'packages/web/package.json declares 2.1.0; no release tag yet', color: colors.dim},
  ]),
  outRow(122, [{text: '# preview left dispat.yaml unchanged', color: colors.dim}]),
  cmdRow(150, fixture.writeCommand),
  outRow(202, [{text: fixture.writeContains, color: colors.green}]),
];

// Each edge draws itself the moment its `+ add` line prints.
const EDGE_AT = [48];
const fixturePkgs: Pkg[] = [
  {id: 'core', eco: 'npm', manifest: 'package.json', base: '1.4.2', next: '1.4.2', x: 600, y: 530},
  {id: 'web', eco: 'npm', manifest: 'package.json', base: '2.1.0', next: '2.1.0', x: 1320, y: 530},
];
const fixtureEdges: Edge[] = [{from: 'core', to: 'web'}];

// Leave a long reading hold after the two package manifests and
// the write summary; compute is an explanation, not a command-speed montage.
export const COMPUTE_DURATION = 300;

export const Compute: React.FC = () => {
  const f = useCurrentFrame();
  const mobile = useDemoLayout();
  // The cards stand idle the whole time: compute reads, it does not release.
  const views: Record<string, NodeView> = Object.fromEntries(
    fixturePkgs.map(({id}, i) => [
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
      graphPkgs={fixturePkgs}
      graphEdges={fixtureEdges}
      edgeDraw={(i) => bar(f, EDGE_AT[i], EDGE_AT[i] + 14)}
      graphOpacity={graphOpacity}
      terminal={<SceneTerminal rows={rows} f={f} />}
    >
      <div style={{position: 'absolute', left: mobile ? 24 : 0, right: mobile ? 24 : 0, top: mobile ? 425 : 300, textAlign: 'center', fontSize: 30, fontWeight: 700, color: colors.cyan}}>
        Detect manifest drift, preview evidence, then write
      </div>
    </Stage>
  );
};
