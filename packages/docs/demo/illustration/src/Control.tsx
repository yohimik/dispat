import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, typingFrames, INF, msg, kv, CapSeg} from './components';

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
  release?: boolean;
  syntax: string;
  caption: CapSeg[];
};

const BEATS: Beat[] = [
  {
    cmd: 'git commit -m "feat(core): add streaming api"',
    from: '1.4.2',
    to: '1.5.0',
    state: {text: 'minor', color: colors.green},
    syntax: 'feat(core)  → scope core · minor bump',
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('version', '"1.4.2 -> 1.5.0"')],
  },
  {
    cmd: 'git commit -m "feat(core)%beta: try it out"',
    from: '1.5.0',
    to: '1.6.0-beta.0',
    state: {text: 'beta train', color: colors.yellow},
    syntax: '%beta  → enter the beta channel',
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('channel', 'beta'), ...kv('version', '"1.5.0 -> 1.6.0-beta.0"')],
  },
  {
    cmd: 'git commit -m "fix(core)%beta: tighten retries"',
    from: '1.6.0-beta.0',
    to: '1.6.0-beta.1',
    state: {text: 'beta train', color: colors.yellow},
    syntax: '%beta  → continue the same train',
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('channel', 'beta'), ...kv('version', '"1.6.0-beta.0 -> 1.6.0-beta.1"')],
  },
  {
    // Mid-train, a breaking change: the next prerelease is computed over the
    // whole train, so the train itself moves to the next major.
    cmd: 'git commit -m "feat(core)%beta!: drop the v1 wire format"',
    from: '1.6.0-beta.1',
    to: '2.0.0-beta.0',
    state: {text: '! train moved', color: colors.red},
    syntax: '!  → breaking change · recompute the train',
    caption: [...INF, msg('● changed'), ...kv('bump', 'major'), ...kv('channel', 'beta'), ...kv('package', 'core'), ...kv('version', '"2.0.0-beta.0"')],
  },
  {
    cmd: 'git commit -m "release(core)%beta>stable: graduate"',
    from: '2.0.0-beta.0',
    to: '2.0.0',
    state: {text: 'graduated', color: colors.green},
    syntax: '%beta>stable  → graduate beta to stable',
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('channel', 'stable'), ...kv('version', '"2.0.0-beta.0 -> 2.0.0"')],
  },
  {
    cmd: 'git commit -m "feat(core): queue dashboard" -m "Release-As: none"',
    from: '2.0.0',
    to: 'held',
    state: {text: '‖ held', color: colors.dim},
    held: true,
    release: false,
    syntax: 'Release-As: none  → hold this package',
    caption: [...INF, msg('‖ held (Release-As: none)'), ...kv('package', 'core'), ...kv('version', '"2.0.0 -> 2.1.0"')],
  },
  {
    cmd: 'git commit --allow-empty -m "release(core): resume" -m "Release-As: auto"',
    from: 'held',
    to: '2.1.0',
    state: {text: 'resumed', color: colors.green},
    syntax: 'Release-As: auto  → resume computed releases',
    caption: [...INF, msg('● changed'), ...kv('package', 'core'), ...kv('version', '"2.0.0 -> 2.1.0"')],
  },
];

const INTRO = 16;
type Schedule = {start: number; status: number; answer: number; run?: number; build?: number; published?: number; end: number};
const schedules: Schedule[] = [];
let cursor = INTRO;
for (const beat of BEATS) {
  const start = cursor + 2;
  const status = start + typingFrames(beat.cmd) + 10;
  const answer = status + typingFrames('dispat status') + 8;
  if (beat.release === false) {
    schedules.push({start, status, answer, end: answer + 36});
  } else {
    const run = answer + 18;
    const build = run + typingFrames('dispat') + 8;
    const published = build + 32;
    schedules.push({start, status, answer, run, build, published, end: published + 24});
  }
  cursor = schedules.at(-1)!.end;
}
export const CONTROL_DURATION = cursor + 20;

// Every directive is typed at the shared terminal pace; the answer gets a
// short reading beat after the complete commit.
const rows: TermRow[] = BEATS.flatMap((beat, i) => {
  const at = schedules[i];
  const result: TermRow[] = [cmdRow(at.start, beat.cmd), cmdRow(at.status, 'dispat status'), outRow(at.answer, beat.caption)];
  if (at.run !== undefined && at.build !== undefined && at.published !== undefined) {
    result.push(
      cmdRow(at.run, 'dispat'),
      outRow(at.build, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', beat.to)]),
      outRow(at.published, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', `core@${beat.to}`)]),
    );
  }
  return result;
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

  const nextSchedule = schedules.findIndex((schedule) => f < schedule.end);
  const active = nextSchedule === -1 ? BEATS.length - 1 : nextSchedule;
  const beat = BEATS[active];
  const schedule = schedules[active];
  // The answer arrives after the commit is typed: terminal first, then the
  // card, then the log line confirming what the card already shows.
  const answer = interpolate(f, [schedule.answer - 4, schedule.answer + 2], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
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
        <div style={{marginTop: 18, fontSize: 21, color: beat.state.color, opacity: answer}}>{beat.syntax}</div>
      </div>
      </div>
    </AbsoluteFill>
  );
};
