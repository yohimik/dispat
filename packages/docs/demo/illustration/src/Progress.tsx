import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {alpha, colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, fadeIO} from './components';

// Publishing crosses a network boundary, so this scene carefully separates
// confirmed progress from an ambiguous response. A durable tag lets a rerun
// skip recorded work. If publishing may have succeeded before the tag was
// written, the destination must be checked or the publisher must be safe to
// repeat. This is the useful, qualified meaning of idempotence here.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const rows: TermRow[] = [
  cmdRow(18, 'dispat'),
  outRow(55, [{text: '# operator note: publish response lost before its tag was recorded', color: colors.yellow}]),
  cmdRow(204, 'dispat status'),
  outRow(242, [{text: '# operator checks the destination before deciding whether to retry', color: colors.cyan}]),
  outRow(274, [{text: '# confirmed tags remove recorded work from the next plan', color: colors.dim}]),
  cmdRow(302, 'dispat'),
];

export const PROGRESS_DURATION = 400;

const Card: React.FC<{
  active: number;
  left: number;
  label: string;
  title: string;
  body: string;
  color: string;
}> = ({active, left, label, title, body, color}) => (
  <div
    style={{
      position: 'absolute',
      left,
      top: 390,
      width: 470,
      height: 300,
      boxSizing: 'border-box',
      padding: '28px 30px',
      borderRadius: 18,
      border: `1.5px solid ${alpha(color, 0.55)}`,
      background: colors.panel,
      opacity: active,
      transform: `translateY(${(1 - active) * 18}px)`,
    }}
  >
    <div style={{fontSize: 18, letterSpacing: 2.4, textTransform: 'uppercase', color}}>{label}</div>
    <div style={{marginTop: 17, fontSize: 31, fontWeight: 700, color: colors.fg}}>{title}</div>
    <div style={{marginTop: 15, fontSize: 22, lineHeight: '33px', color: colors.dim}}>{body}</div>
  </div>
);

export const Progress: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [PROGRESS_DURATION - 10, PROGRESS_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      <div style={{position: 'absolute', inset: 0, opacity}}>
        <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
        <div
          style={{
            position: 'absolute',
            left: 960,
            top: 326,
            transform: 'translateX(-50%)',
            fontSize: 21,
            letterSpacing: 3,
            textTransform: 'uppercase',
            color: colors.green,
            border: `1.5px solid ${alpha(colors.green, 0.4)}`,
            borderRadius: 999,
            padding: '5px 22px',
          }}
        >
          recorded progress
        </div>
        <Card
          active={bar(f, 32, 50)}
          left={190}
          label="confirmed"
          title="Tag records completion"
          body="A rerun finds durable evidence and skips work already recorded."
          color={colors.green}
        />
        <Card
          active={bar(f, 118, 140)}
          left={725}
          label="uncertain"
          title="The response was lost"
          body="Publishing may have succeeded before its tag could be written."
          color={colors.yellow}
        />
        <Card
          active={bar(f, 204, 226)}
          left={1260}
          label="idempotent retry"
          title="Check before retrying"
          body="Confirm destination state, then use a configured publisher that is safe to repeat."
          color={colors.cyan}
        />
        <div
          style={{
            position: 'absolute',
            left: 240,
            right: 240,
            top: 720,
            textAlign: 'center',
            fontSize: 24,
            lineHeight: '36px',
            color: colors.dim,
            opacity: fadeIO(f, 220, 238, 372, 392),
          }}
        >
          Dispat does not infer an ambiguous destination result. Check it before rerunning with an idempotent publisher.
        </div>
        <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
