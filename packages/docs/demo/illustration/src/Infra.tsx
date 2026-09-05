import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import fixture from '../../fixtures/infra/expected.json';
import {colors, font} from './theme';
import {useDemoLayout} from './layout';
import {NodeView, SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {type Edge, type Pkg} from './graph';
import {Pulse, Stage} from './Stage';

// A versioned infrastructure change is planned before application builds and
// applied before either application deploy starts. Terraform still uses state:
// this repository's tf-plan script reconstructs it, saves tfplan, and tf-apply applies exactly
// that saved plan. Dispat supplies release ordering around those scripts.

const bar = (frame: number, from: number, to: number) =>
  interpolate(frame, [from, to], [0, 1], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });

const packages: Pkg[] = [
  {id: 'infra', stackedHeader: true, eco: 'terraform', manifest: 'main.tf', base: '1.2.0', next: '1.3.0', x: 490, y: 560},
  {id: 'backend', stackedHeader: true, eco: 'docker', manifest: 'Dockerfile', base: '0.8.2', next: '0.8.3', x: 1320, y: 420},
  {id: 'frontend', stackedHeader: true, eco: 'npm', manifest: 'package.json', base: '2.1.0', next: '2.1.1', x: 1320, y: 710},
];

const edges: Edge[] = [
  {from: 'infra', to: 'backend', label: 'deploy waits apply'},
  {from: 'infra', to: 'frontend', label: 'deploy waits apply'},
];

function infraView(frame: number): NodeView {
  if (frame < 116) return {state: 'idle'};
  if (frame < 165) return {state: 'changed', bumped: true};
  if (frame < 230) return {state: 'building', bumped: true, progress: bar(frame, 165, 228), note: 'rebuild state + save tfplan'};
  if (frame < 300) return {state: 'publishing', bumped: true, progress: bar(frame, 230, 298), note: 'apply saved tfplan'};
  return {state: 'published', bumped: true, tag: 'infra/v1.3.0'};
}

function backendView(frame: number): NodeView {
  if (frame < 120) return {state: 'idle'};
  if (frame < 235) return {state: frame < 145 ? 'changed' : 'waiting', bumped: true, note: frame < 145 ? undefined : 'waits for infra plan'};
  if (frame < 285) return {state: 'building', bumped: true, progress: bar(frame, 235, 283), note: 'overlaps infra apply'};
  if (frame < 305) return {state: 'waiting', bumped: true, note: 'built · waits to deploy'};
  if (frame < 365) return {state: 'publishing', bumped: true, progress: bar(frame, 305, 363)};
  return {state: 'published', bumped: true, tag: 'backend@0.8.3'};
}

function frontendView(frame: number): NodeView {
  if (frame < 124) return {state: 'idle'};
  if (frame < 240) return {state: frame < 145 ? 'changed' : 'waiting', bumped: true, note: frame < 145 ? undefined : 'waits for infra plan'};
  if (frame < 290) return {state: 'building', bumped: true, progress: bar(frame, 240, 288), note: 'overlaps infra apply'};
  if (frame < 310) return {state: 'waiting', bumped: true, note: 'built · waits to deploy'};
  if (frame < 415) return {state: 'publishing', bumped: true, progress: bar(frame, 310, 413)};
  return {state: 'published', bumped: true, tag: 'frontend@2.1.1'};
}

const pulses: Pulse[] = [
  {edge: 0, start: 230},
  {edge: 1, start: 230},
];

const rows: TermRow[] = [
  cmdRow(6, `git commit -m "${fixture.commit}"`),
  cmdRow(90, 'dispat status'),
  ...fixture.plan.map((item, index) => outRow(116 + index * 4, [
    ...INF, msg('● changed'), ...kv('package', item.package),
    ...(item.dueToProviders.length ? kv('dueToProviders', JSON.stringify(item.dueToProviders)) : []),
    ...kv('version', JSON.stringify(item.version)),
  ])),
  outRow(128, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', String(fixture.plan.length)), ...kv('releasing', String(fixture.published.length))]),
  cmdRow(145, 'dispat'),
  outRow(165, [...INF, msg('build started'), ...kv('package', 'infra'), ...kv('stage', 'build')]),
  outRow(230, [...INF, msg('publish started'), ...kv('package', 'infra'), ...kv('stage', 'publish')]),
  outRow(235, [...INF, msg('build started'), ...kv('package', 'backend'), ...kv('stage', 'build')]),
  outRow(240, [...INF, msg('build started'), ...kv('package', 'frontend'), ...kv('stage', 'build')]),
  outRow(300, [...INF, msg('published'), ...kv('package', 'infra'), ...kv('tag', 'infra/v1.3.0'), ...kv('version', '1.3.0')]),
  outRow(305, [...INF, msg('publish started'), ...kv('package', 'backend'), ...kv('stage', 'publish')]),
  outRow(310, [...INF, msg('publish started'), ...kv('package', 'frontend'), ...kv('stage', 'publish')]),
  outRow(365, [...INF, msg('published'), ...kv('package', 'backend'), ...kv('tag', 'backend@0.8.3'), ...kv('version', '0.8.3')]),
  outRow(415, [...INF, msg('published'), ...kv('package', 'frontend'), ...kv('tag', 'frontend@2.1.1'), ...kv('version', '2.1.1')]),
  outRow(425, [...INF, msg('done'), ...kv('cancelled', '0'), ...kv('failed', '0'), ...kv('published', '3'), ...kv('skipped', '0'), ...kv('unchanged', '0')]),
];

export const INFRA_DURATION = 480;

export const Infra: React.FC = () => {
  const frame = useCurrentFrame();
  const mobile = useDemoLayout();
  const opacity =
    bar(frame, 0, 8) *
    interpolate(frame, [INFRA_DURATION - 10, INFRA_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  return (
    <div style={{opacity}}>
      <Stage
        views={{infra: infraView(frame), backend: backendView(frame), frontend: frontendView(frame)}}
        graphPkgs={packages}
        graphEdges={edges}
        pulses={pulses}
        edgeLabels
        edgeDraw={(index) => bar(frame, 35 + index * 12, 65 + index * 12)}
        terminal={<SceneTerminal rows={rows} f={frame} />}
      >
        {!mobile && <div style={{position: 'absolute', left: 200, right: 200, top: 830, fontFamily: font, fontSize: 30, lineHeight: '32px', color: colors.dim, textAlign: 'center'}}>
          Temporary state; no persistent state bucket
        </div>}
      </Stage>
    </div>
  );
};
