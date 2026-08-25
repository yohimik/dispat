import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv, CapSeg} from './components';

// The release-control claim, one decision per beat: a commit is typed into
// the scene's terminal and the package card's next release answers it. An
// ordinary feat bumps it, %beta starts a prerelease train, another commit
// rides the train, a breaking change arriving mid-train moves the whole
// train to the next major (the next prerelease is computed over the whole
// train), %beta>stable graduates it there, Release-As: none holds the
// package, and Release-As: auto resumes it. The point the motion makes is
// that every one of these is a commit: reviewed, versioned, and in the
// history, and the terminal answers each one the moment the card does.
//
// Twenty frames per second, like every composition in Root.tsx. No title:
// the landing page overlays the feature's own text over the top strip, so
// the canvas keeps it clear.

type Beat = {
  /** The commit, typed into the terminal; footers ride as a second -m. */
  cmd: string;
  /** The card's next-release line: what it was, what it becomes. */
  from: string;
  to: string;
  /** Badge on the card while this beat holds. */
  state: {text: string; color: string};
  held?: boolean;
  caption: CapSeg[];
};

const BEATS: Beat[] = [
  {
    cmd: 'git commit -m "feat(core): add streaming api"',
    from: '1.4.2',
    to: '1.5.0',
    state: {text: 'minor', color: colors.green},
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('version', '"1.4.2 -> 1.5.0"')],
  },
  {
    cmd: 'git commit -m "feat(core)%beta: try it out"',
    from: '1.4.2',
    to: '1.5.0-beta.0',
    state: {text: 'beta train', color: colors.yellow},
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('channel', 'beta'), ...kv('version', '"1.5.0-beta.0"')],
  },
  {
    cmd: 'git commit -m "fix(core)%beta: tighten retries"',
    from: '1.5.0-beta.0',
    to: '1.5.0-beta.1',
    state: {text: 'beta train', color: colors.yellow},
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('channel', 'beta'), ...kv('version', '"1.5.0-beta.1"')],
  },
  {
    // Mid-train, a breaking change: the next prerelease is computed over the
    // whole train, so the train itself moves to the next major.
    cmd: 'git commit -m "feat(core)%beta!: drop the v1 wire format"',
    from: '1.5.0-beta.1',
    to: '2.0.0-beta.0',
    state: {text: '! train moved', color: colors.red},
    caption: [...INF, msg('● changed'), ...kv('bump', 'major'), ...kv('channel', 'beta'), ...kv('package', 'core'), ...kv('version', '"2.0.0-beta.0"')],
  },
  {
    cmd: 'git commit -m "release(core)%beta>stable:"',
    from: '2.0.0-beta.0',
    to: '2.0.0',
    state: {text: 'graduated', color: colors.green},
    caption: [...INF, msg('graduated'), ...kv('package', 'core'), ...kv('channel', '"beta -> stable"'), ...kv('version', '2.0.0')],
  },
  {
    cmd: 'git commit -m "chore(core): pause releases" -m "Release-As: none"',
    from: '2.0.0',
    to: 'held',
    state: {text: '⊘ held', color: colors.dim},
    held: true,
    caption: [...INF, msg('⊘ held'), ...kv('package', 'core'), ...kv('reason', '"Release-As: none"')],
  },
  {
    cmd: 'git commit -m "chore(core): resume releases" -m "Release-As: auto"',
    from: 'held',
    to: '2.0.0',
    state: {text: 'resumed', color: colors.green},
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('version', '"1.4.2 -> 2.0.0"')],
  },
];

const INTRO = 16;
const BEAT = 68;
export const CONTROL_DURATION = INTRO + BEATS.length * BEAT + 20;

// The scene's terminal: each beat is one commit typed fast (they are long
// lines) and the pretty log's answer, printed the moment the card answers.
const rows: TermRow[] = BEATS.flatMap((beat, i) => {
  const start = INTRO + i * BEAT;
  return [cmdRow(start + 2, beat.cmd, 0.55), outRow(start + 52, beat.caption)];
});

export const Control: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    interpolate(f, [0, 10], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'}) *
    interpolate(f, [CONTROL_DURATION - 10, CONTROL_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  const cardIn = interpolate(f, [6, 18], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

  const active = Math.min(BEATS.length - 1, Math.max(0, Math.floor((f - INTRO) / BEAT)));
  const beat = BEATS[active];
  const b = f - (INTRO + active * BEAT);
  // The answer arrives after the commit is typed: terminal first, then the
  // card, then the log line confirming what the card already shows.
  const answer = interpolate(b, [40, 48], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  const held = beat.held ? answer : 0;

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      <SceneTerminal rows={rows} f={f} />
      {/* The one package the decisions are about. */}
      <div
        style={{
          position: 'absolute',
          left: 960,
          top: 430,
          transform: 'translateX(-50%)',
          width: 700,
          borderRadius: 18,
          background: colors.panel,
          border: `2px solid ${beat.held && answer > 0.5 ? colors.faint : colors.panelEdge}`,
          padding: '30px 40px 34px',
          opacity: cardIn * (1 - held * 0.45),
        }}>
        <div style={{display: 'flex', alignItems: 'center', gap: 16}}>
          <span style={{fontSize: 34, fontWeight: 700, color: colors.fg}}>core</span>
          <span
            style={{
              fontSize: 19,
              color: colors.red,
              border: `1.5px solid ${colors.red}`,
              borderRadius: 999,
              padding: '1px 12px',
              opacity: 0.9,
            }}>
            npm
          </span>
          <span style={{marginLeft: 'auto', fontSize: 24, fontWeight: 700, color: beat.state.color, opacity: answer}}>
            {beat.state.text}
          </span>
        </div>
        <div style={{marginTop: 26, fontSize: 26, color: colors.dim}}>next release</div>
        <div style={{marginTop: 8, fontSize: 44}}>
          <span style={{color: colors.dim}}>{beat.from}</span>
          <span style={{color: colors.dim}}> ⟶ </span>
          <span
            style={{
              color: beat.held ? colors.dim : colors.green,
              fontWeight: 700,
              opacity: answer,
              display: 'inline-block',
              transform: `translateY(${(1 - answer) * 10}px)`,
            }}>
            {beat.to}
          </span>
        </div>
      </div>
      </div>
    </AbsoluteFill>
  );
};
