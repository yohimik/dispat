import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {alpha, colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, fadeIO} from './components';
import {useDemoLayout} from './layout';

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
  cmdRow(204, 'npm view @acme/api@1.5.0 version'),
  outRow(270, [{text: '1.5.0  # destination confirms the publish landed', color: colors.cyan}]),
  outRow(298, [{text: '# record/reconcile that result before any retry', color: colors.dim}]),
];

export const PROGRESS_DURATION = 380;

const Card: React.FC<{
  active: number;
  left: number;
  label: string;
  title: string;
  body: string;
  color: string;
  mobile?: boolean;
  index?: number;
}> = ({active, left, label, title, body, color, mobile = false, index = 0}) => (
  <div
    style={{
      position: 'absolute',
      left: mobile ? 30 : left,
      right: mobile ? 30 : undefined,
      top: mobile ? 150 + index * 230 : 390,
      width: mobile ? 'auto' : 470,
      height: mobile ? 220 : 300,
      boxSizing: 'border-box',
      padding: mobile ? '16px 26px' : '28px 30px',
      borderRadius: 18,
      border: `1.5px solid ${alpha(color, 0.55)}`,
      background: colors.panel,
      opacity: active,
      transform: `translateY(${(1 - active) * 18}px)`,
    }}
  >
    <div style={{fontSize: mobile ? 26 : 18, letterSpacing: 2.4, textTransform: 'uppercase', color}}>{label}</div>
    <div style={{marginTop: mobile ? 8 : 17, fontSize: 31, fontWeight: 700, color: colors.fg}}>{title}</div>
    <div style={{marginTop: mobile ? 6 : 15, fontSize: mobile ? 26 : 22, lineHeight: mobile ? '32px' : '33px', color: colors.dim}}>{body}</div>
  </div>
);

export const Progress: React.FC = () => {
  const f = useCurrentFrame();
  const mobile = useDemoLayout();
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
            left: mobile ? 360 : 960,
            top: mobile ? 100 : 326,
            width: mobile ? 600 : undefined,
            whiteSpace: 'nowrap',
            textAlign: 'center',
            boxSizing: 'border-box',
            transform: 'translateX(-50%)',
            fontSize: mobile ? 26 : 21,
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
          mobile={mobile} index={0}
        />
        <Card
          active={bar(f, 118, 140)}
          left={725}
          label="uncertain"
          title="The response was lost"
          body="Publishing may have succeeded before its tag could be written."
          color={colors.yellow}
          mobile={mobile} index={1}
        />
        <Card
          active={bar(f, 204, 226)}
          left={1260}
          label="idempotent retry"
          title="Check before retrying"
          body="Confirm destination state, then use a configured publisher that is safe to repeat."
          color={colors.cyan}
          mobile={mobile} index={2}
        />
        <div
          style={{
            position: 'absolute',
            left: mobile ? 36 : 240,
            right: mobile ? 36 : 240,
            top: mobile ? 840 : 720,
            textAlign: 'center',
            fontSize: mobile ? 26 : 24,
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
