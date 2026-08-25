import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {NodeView, PkgNode, SceneTerminal, TermRow, cmdRow, outRow, fadeIO, INF, ERR, msg, kv} from './components';
import {Pkg} from './graph';

// The repository README's "why one more monorepo tool?" argument, drawn:
// every major tool can topologically sort a build, but the split is always
// the same, and two situations break build-all-then-publish-all in
// practice. Beat one: a Docker consumer can only be *built* once its
// provider is *published*, so the assumed model fails before it publishes
// anything. Beat two: an error in the middle of a publish leaves half the
// packages shipped, and a registry can answer only one ecosystem's half of
// "what does the run still owe". Beat three: dispat's answer, build and
// publish as legs of one graph, in order, with the records written as it
// goes.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const rows: TermRow[] = [
  outRow(24, [{text: '# the assumed model: build everything, then publish everything', color: colors.dim}]),
  outRow(100, [...ERR, msg('build failed'), ...kv('package', 'api'), ...kv('error', '"pull acme/core:1.5.0: not found"')]),
  outRow(126, [{text: '# the provider was never published: mixed graphs break the model', color: colors.dim}]),
  outRow(186, [...ERR, msg('publish failed'), ...kv('package', 'api'), ...kv('published', '"core, utils"'), ...kv('left', 'unknown')]),
  outRow(224, [{text: '# a registry answers one ecosystem; recovery becomes a script you write', color: colors.dim}]),
  cmdRow(316, 'dispat'),
  outRow(348, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', '1.5.0')]),
  outRow(382, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0'), ...kv('version', '1.5.0')]),
  outRow(438, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3'), ...kv('version', '0.8.3')]),
  outRow(450, [...INF, msg('done'), ...kv('published', '2'), ...kv('failed', '0'), ...kv('skipped', '0')]),
];

const CORE: Pkg = {id: 'core', eco: 'npm', manifest: 'package.json', base: '1.4.2', next: '1.5.0', x: 640, y: 660};
const API: Pkg = {id: 'api', eco: 'docker', manifest: 'Dockerfile', base: '0.8.2', next: '0.8.3', x: 1280, y: 660};

function beat1Views(f: number): [NodeView, NodeView] {
  return [
    f < 58 ? {state: 'building', progress: bar(f, 30, 56)} : {state: 'building', progress: 1, note: 'built, waiting for publish all'},
    f < 95 ? {state: 'building', progress: bar(f, 58, 93) * 0.4} : {state: 'failed', note: 'base image not in the registry'},
  ];
}

function beat3Views(f: number): [NodeView, NodeView] {
  const core: NodeView =
    f < 350
      ? {state: 'building', bumped: true, progress: bar(f, 318, 348)}
      : f < 380
        ? {state: 'publishing', bumped: true, progress: bar(f, 350, 378)}
        : {state: 'published', bumped: true, tag: 'core@1.5.0'};
  const api: NodeView =
    f < 380
      ? {state: 'waiting', bumped: true, note: 'isBuildWaitingPublish: core@1.5.0'}
      : f < 414
        ? {state: 'building', bumped: true, progress: bar(f, 380, 412)}
        : f < 436
          ? {state: 'publishing', bumped: true, progress: bar(f, 414, 434)}
          : {state: 'published', bumped: true, tag: 'api@0.8.3'};
  return [core, api];
}

const Chip: React.FC<{text: string; color?: string}> = ({text, color = colors.green}) => (
  <div
    style={{
      position: 'absolute',
      left: 960,
      top: 380,
      transform: 'translateX(-50%)',
      fontSize: 21,
      letterSpacing: 3,
      textTransform: 'uppercase',
      color,
      border: `1.5px solid ${color}66`,
      borderRadius: 999,
      padding: '5px 22px',
      whiteSpace: 'nowrap',
    }}>
    {text}
  </div>
);

const Phase: React.FC<{text: string; x: number; state: 'idle' | 'active' | 'dead'}> = ({text, x, state}) => (
  <div
    style={{
      position: 'absolute',
      left: x,
      top: 460,
      transform: 'translateX(-50%)',
      width: 380,
      textAlign: 'center',
      fontSize: 30,
      fontWeight: 700,
      color: state === 'dead' ? colors.dim : colors.fg,
      background: colors.panel,
      border: `2px solid ${state === 'active' ? colors.cyan : state === 'dead' ? colors.red : colors.panelEdge}`,
      borderRadius: 14,
      padding: '22px 0',
      opacity: state === 'dead' ? 0.6 : 1,
      textDecoration: state === 'dead' ? 'line-through' : 'none',
    }}>
    {text}
  </div>
);

export const WHY_DURATION = 484;

export const Why: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [WHY_DURATION - 10, WHY_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  const b1 = fadeIO(f, 14, 22, 158, 166);
  const b2 = fadeIO(f, 166, 174, 300, 308);
  const b3 = fadeIO(f, 308, 316, WHY_DURATION - 16, WHY_DURATION - 8);
  const [b1core, b1api] = beat1Views(f);
  const [b3core, b3api] = beat3Views(f);

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {b1 > 0 && (
        <div style={{opacity: b1, position: 'absolute', inset: 0}}>
          <Chip text="every other tool: build all, then publish all" color={colors.yellow} />
          <Phase text="build all" x={640} state={f < 100 ? 'active' : 'dead'} />
          <Phase text="publish all" x={1280} state={f < 100 ? 'idle' : 'dead'} />
          <PkgNode pkg={CORE} view={b1core} />
          <PkgNode pkg={API} view={b1api} />
        </div>
      )}
      {b2 > 0 && (
        <div style={{opacity: b2, position: 'absolute', inset: 0}}>
          <Chip text="an error in the middle of a run" color={colors.red} />
          {[
            {text: '✓ core published', color: colors.green, x: 460},
            {text: '✓ utils published', color: colors.green, x: 800},
            {text: '✗ api failed', color: colors.red, x: 1130},
            {text: '? web', color: colors.dim, x: 1400},
          ].map((c) => (
            <div
              key={c.text}
              style={{
                position: 'absolute',
                left: c.x,
                top: 500,
                transform: 'translateX(-50%)',
                fontSize: 27,
                fontWeight: 700,
                color: c.color,
                background: colors.panel,
                border: `2px solid ${c.color}66`,
                borderRadius: 14,
                padding: '16px 28px',
                whiteSpace: 'nowrap',
                opacity: bar(f, 176 + c.x / 90, 184 + c.x / 90),
              }}>
              {c.text}
            </div>
          ))}
          <div
            style={{
              position: 'absolute',
              left: 260,
              right: 260,
              top: 620,
              textAlign: 'center',
              fontSize: 26,
              lineHeight: '40px',
              color: colors.dim,
              opacity: bar(f, 216, 228),
            }}>
            what does the run still owe? a registry answers only whether a version is there, and only for one ecosystem
          </div>
        </div>
      )}
      {b3 > 0 && (
        <div style={{opacity: b3, position: 'absolute', inset: 0}}>
          <Chip text="dispat: build and publish are legs of one graph" />
          <svg width="1920" height="1080" style={{position: 'absolute', inset: 0}} viewBox="0 0 1920 1080">
            <path
              d="M 815 660 L 1105 660"
              stroke={f >= 378 ? colors.green : colors.faint}
              strokeWidth={f >= 378 ? 5 : 3}
              fill="none"
            />
          </svg>
          <PkgNode pkg={CORE} view={b3core} />
          <PkgNode pkg={API} view={b3api} />
        </div>
      )}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
