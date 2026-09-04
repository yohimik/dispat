import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, fadeIO, INF, msg, kv} from './components';

// The claim that the model is mathematics rather than machinery, twenty-five
// seconds at Root.tsx's twenty frames per second, three properties as three
// equations. Determinism: the plan is a pure function of history, graph, and
// configuration, with no clocks and no state files, so the same status
// prints twice. Recovery consults durable release evidence, while an
// ambiguous publish must be safe for the publisher to repeat. The final
// beat draws the commit parser's bounded left-to-right scan.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const CHANGED = [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('bump', 'minor'), ...kv('version', '"1.4.2 -> 1.5.0"')];
const PLAN = [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '7'), ...kv('releasing', '3')];

const rows: TermRow[] = [
  cmdRow(20, 'dispat status'),
  outRow(48, CHANGED),
  outRow(52, PLAN),
  cmdRow(74, 'dispat status'),
  outRow(102, CHANGED),
  outRow(106, PLAN),
  outRow(122, [{text: '# same inputs, same plan: the planner is a pure function', color: colors.dim}]),
  cmdRow(176, 'dispat'),
  outRow(206, [...INF, msg('done'), ...kv('failed', '1'), ...kv('published', '2'), ...kv('skipped', '1'), ...kv('unchanged', '3')]),
  cmdRow(228, 'dispat'),
  outRow(260, [...INF, msg('done'), ...kv('failed', '0'), ...kv('published', '2'), ...kv('skipped', '0'), ...kv('unchanged', '5')]),
  outRow(292, [{text: '# confirmed tags skip completed work; ambiguous publish => safe repeat', color: colors.dim}]),
  outRow(360, [{text: '# ccme: untrusted commit messages in CI, parsed in one pass', color: colors.dim}]),
];

const COMMIT = 'feat(core)^^%beta!: add streaming api';

type Beat = {
  from: number;
  to: number;
  chip: string;
  equation: React.ReactNode;
  note: string;
};

export const MATH_DURATION = 500;

export const Math_: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [MATH_DURATION - 10, MATH_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  const sweep = bar(f, 350, 440);
  const shown = Math.floor(COMMIT.length * sweep);

  const beats: Beat[] = [
    {
      from: 14,
      to: 168,
      chip: 'deterministic',
      equation: (
        <>
          <span style={{color: colors.green, fontWeight: 700}}>plan</span>
          <span style={{color: colors.dim}}> = </span>
          <span style={{color: colors.fg}}>f(</span>
          <span style={{color: colors.cyan}}>history</span>
          <span style={{color: colors.dim}}>, </span>
          <span style={{color: colors.cyan}}>graph</span>
          <span style={{color: colors.dim}}>, </span>
          <span style={{color: colors.cyan}}>config</span>
          <span style={{color: colors.fg}}>)</span>
        </>
      ),
      note: 'no clocks, no state files, no memory of the previous run',
    },
    {
      from: 168,
      to: 328,
      chip: 'recorded progress',
      equation: (
        <>
          <span style={{color: colors.green}}>confirmed tag</span>
          <span style={{color: colors.dim}}> → skip · ambiguous publish → </span>
          <span style={{color: colors.cyan}}>safe repeat</span>
        </>
      ),
      note: 'recovery follows durable evidence; the publisher owns safe repetition across an uncertain network result',
    },
    {
      from: 328,
      to: 486,
      chip: 'linear',
      equation: (
        <>
          <span style={{color: colors.fg}}>O(</span>
          <span style={{color: colors.cyan}}>n</span>
          <span style={{color: colors.fg}}>)</span>
          <span style={{color: colors.dim}}> bounded left-to-right scan</span>
        </>
      ),
      note: 'the commit parser advances through untrusted input without backtracking',
    },
  ];

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {beats.map((beat) => {
        const beatOpacity = fadeIO(f, beat.from, beat.from + 8, beat.to - 8, beat.to);
        if (beatOpacity <= 0) return null;
        return (
          <div key={beat.chip} style={{opacity: beatOpacity, position: 'absolute', left: 0, right: 0, top: 0, bottom: 0}}>
            <div
              style={{
                position: 'absolute',
                left: 960,
                top: 390,
                transform: 'translateX(-50%)',
                fontSize: 21,
                letterSpacing: 3,
                textTransform: 'uppercase',
                color: colors.green,
                border: `1.5px solid ${colors.green}66`,
                borderRadius: 999,
                padding: '5px 22px',
              }}>
              {beat.chip}
            </div>
            {/* The parser beat sweeps a real commit once, left to right. */}
            {beat.chip === 'linear' && (
              <div style={{position: 'absolute', left: 0, right: 0, top: 480, textAlign: 'center', fontSize: 40, whiteSpace: 'pre'}}>
                <span style={{color: colors.fg}}>{COMMIT.slice(0, shown)}</span>
                {sweep > 0 && sweep < 1 && <span style={{color: colors.green}}>█</span>}
                <span style={{color: colors.faint}}>{COMMIT.slice(shown)}</span>
              </div>
            )}
            <div
              style={{
                position: 'absolute',
                left: 0,
                right: 0,
                top: beat.chip === 'linear' ? 580 : 500,
                textAlign: 'center',
                fontSize: 62,
                fontWeight: 700,
              }}>
              {beat.equation}
            </div>
            <div
              style={{
                position: 'absolute',
                left: 260,
                right: 260,
                top: beat.chip === 'linear' ? 690 : 610,
                textAlign: 'center',
                fontSize: 26,
                lineHeight: '40px',
                color: colors.dim,
              }}>
              {beat.note}
            </div>
          </div>
        );
      })}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
