import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, ERR, msg, kv} from './components';

// The release-lock claim, twenty seconds at Root.tsx's twenty frames per
// second: one release at a time, enforced by git itself. ci-runner-7 claims
// the repository by pushing the dispat-release-lock tag and releases; the
// session below is the laptop that tries meanwhile and is rejected with
// nothing planned, built, or published. The runner gives the lock back on
// the way out, and the laptop's second try claims it cleanly.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

// The race, in frames: A claims, A releases, B is rejected, A unlocks, B
// claims.
const A_CLAIM = 40;
const B_REJECT = 120;
const A_DONE = 250;
const B_CLAIM = 300;

const rows: TermRow[] = [
  cmdRow(96, 'dispat release'),
  outRow(B_REJECT, [
    ...ERR,
    msg('unable to create the release lock tag'),
    ...kv('holder', '"host ci-runner-7 pid 4242"'),
    ...kv('error', '"! [rejected] dispat-release-lock (already exists)"'),
  ]),
  outRow(B_REJECT + 10, [{text: 'nothing was planned, built, published, or tagged', color: colors.dim}]),
  cmdRow(276, 'dispat release'),
  outRow(B_CLAIM + 8, [...INF, msg('release lock claimed'), ...kv('remote', 'origin'), ...kv('tag', 'dispat-release-lock')]),
  outRow(B_CLAIM + 26, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '1')]),
];

export const LOCK_DURATION = 400;

const Runner: React.FC<{
  name: string;
  x: number;
  state: {text: string; color: string};
}> = ({name, x, state}) => (
  <div
    style={{
      position: 'absolute',
      left: x - 210,
      top: 560,
      width: 420,
      borderRadius: 16,
      background: colors.panel,
      border: `2px solid ${state.color === colors.faint ? colors.panelEdge : state.color}`,
      padding: '20px 26px',
      boxSizing: 'border-box',
    }}>
    <div style={{fontSize: 26, fontWeight: 700, color: colors.fg}}>{name}</div>
    <div style={{marginTop: 10, fontSize: 20, fontWeight: 700, color: state.color, minHeight: 28}}>
      {state.color === colors.faint ? '' : state.text}
    </div>
  </div>
);

export const Lock: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [LOCK_DURATION - 10, LOCK_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  const aState =
    f < A_CLAIM
      ? {text: '', color: colors.faint}
      : f < A_DONE
        ? {text: 'releasing…', color: colors.cyan}
        : {text: '✓ released, owned lock returned', color: colors.green};
  const bState =
    f < 96
      ? {text: '', color: colors.faint}
      : f < B_REJECT
        ? {text: 'dispat release', color: colors.cyan}
        : f < 276
          ? {text: '✗ rejected: lock exists', color: colors.red}
          : f < B_CLAIM
            ? {text: 'dispat release', color: colors.cyan}
            : {text: '✓ lock claimed', color: colors.green},
  // Who holds the tag on origin right now.
  holder = f >= A_CLAIM && f < A_DONE ? 'ci-runner-7' : f >= B_CLAIM ? 'laptop' : null;

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {/* The remote, and the one tag that is the lock. */}
      <div
        style={{
          position: 'absolute',
          left: 960,
          top: 350,
          transform: 'translateX(-50%)',
          width: 560,
          borderRadius: 16,
          background: colors.panel,
          border: `1.5px solid ${colors.panelEdge}`,
          padding: '20px 30px 24px',
          textAlign: 'center',
        }}>
        <div style={{fontSize: 23, color: colors.dim}}>origin</div>
        <div
          style={{
            marginTop: 14,
            display: 'inline-block',
            fontSize: 23,
            fontWeight: 700,
            color: holder ? colors.yellow : colors.faint,
            border: `2px ${holder ? 'solid' : 'dashed'} ${holder ? colors.yellow : colors.faint}`,
            borderRadius: 999,
            padding: '7px 26px',
          }}>
          dispat-release-lock · unique attempt
        </div>
        <div style={{marginTop: 10, fontSize: 19, color: colors.dim, minHeight: 26}}>
          {holder ? `held by ${holder}; release guarded by object-id lease` : 'free'}
        </div>
      </div>
      <Runner name="ci-runner-7" x={560} state={aState} />
      <Runner name="laptop" x={1360} state={bState} />
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
