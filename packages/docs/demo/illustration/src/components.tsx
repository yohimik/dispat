import React from 'react';
import {interpolate} from 'remotion';
import {alpha, colors, font, NodeState, stateColor} from './theme';
import {NODE_W, NODE_H, Pkg} from './graph';

// The status line inside a card quotes the CLI's own plan glyphs.
const stateLabel: Record<NodeState, string> = {
  idle: '',
  changed: '● changed',
  building: 'build',
  waiting: 'waiting',
  publishing: 'publish',
  published: '✓ published',
  failed: '✗ build failed',
  skipped: '⊘ skipped',
  unchanged: 'unchanged',
  catchup: '↻ catch-up',
  running: 'run',
};

const ecoBadge: Record<Pkg['eco'], {label: string; color: string}> = {
  npm: {label: 'npm', color: colors.red},
  go: {label: 'go', color: colors.cyan},
  docker: {label: 'docker', color: colors.blue},
  cargo: {label: 'cargo', color: colors.yellow},
  ios: {label: 'ios', color: colors.fg},
};

export type NodeView = {
  state: NodeState;
  /** 0..1 build/publish progress bar; undefined hides the bar */
  progress?: number;
  /** show the bumped version */
  bumped?: boolean;
  /** a note under the status, e.g. "waits for core@1.5.0" */
  note?: string;
  /** the release tag chip, once published */
  tag?: string;
  /** 0..1 highlight of the manifest rewrite (api's FROM line) */
  rewrite?: number;
  opacity?: number;
};

export const PkgNode: React.FC<{pkg: Pkg; view: NodeView}> = ({pkg, view}) => {
  const c = stateColor[view.state];
  const active = view.state !== 'idle';
  const badge = ecoBadge[pkg.eco];
  const rewrite = view.rewrite ?? 0;
  const manifestLine = pkg.manifest;
  return (
    <div
      style={{
        position: 'absolute',
        left: pkg.x - NODE_W / 2,
        top: pkg.y - NODE_H / 2,
        width: NODE_W,
        height: NODE_H,
        borderRadius: 14,
        background: colors.panel,
        border: `2px solid ${active ? c : colors.panelEdge}`,
        boxShadow: active ? `0 0 34px ${alpha(c, 0.2)}` : 'none',
        fontFamily: font,
        color: colors.fg,
        padding: '12px 20px',
        boxSizing: 'border-box',
        opacity: view.opacity ?? 1,
      }}
    >
      <div style={{display: 'flex', alignItems: 'center', gap: 10, minWidth: 0}}>
        <span style={{fontSize: 27, lineHeight: '32px', fontWeight: 700, minWidth: 0, overflowWrap: 'anywhere'}}>{pkg.id}</span>
        <span
          style={{
            fontSize: 16,
            color: badge.color,
            border: `1.5px solid ${alpha(badge.color, 0.4)}`,
            borderRadius: 999,
            padding: '1px 10px',
            lineHeight: '24px',
            flexShrink: 0,
          }}
        >
          {badge.label}
        </span>
        <span style={{marginLeft: 'auto', fontSize: 18, lineHeight: '24px', color: c, fontWeight: 700, whiteSpace: 'nowrap', flexShrink: 0}}>
          {stateLabel[view.state]}
        </span>
      </div>
      <div
        style={{
          fontSize: 17,
          color: rewrite > 0.4 && rewrite < 1 ? colors.cyan : colors.dim,
          marginTop: 7,
          lineHeight: '22px',
          overflowWrap: 'anywhere',
        wordBreak: 'break-all',
        }}
      >
        {manifestLine}
      </div>
      <div style={{fontSize: 20, lineHeight: '24px', marginTop: 5}}>
        <span style={{color: view.bumped ? colors.dim : colors.fg}}>{pkg.base}</span>
        {view.bumped ? (
          <>
            <span style={{color: colors.dim}}>{' -> '}</span>
            <span style={{color: colors.green, fontWeight: 700}}>{pkg.next}</span>
          </>
        ) : null}
      </div>
      {view.note ? (
        <div style={{fontSize: view.note.length > 48 ? 12 : 14, lineHeight: view.note.length > 48 ? '15px' : '18px', color: colors.dim, marginTop: 2, textAlign: 'right', overflowWrap: 'anywhere'}}>{view.note}</div>
      ) : null}
      {view.progress !== undefined ? (
        <div
          style={{
            position: 'absolute',
            left: 20,
            right: 20,
            bottom: 11,
            height: 5,
            borderRadius: 3,
            background: colors.panelEdge,
          }}
        >
          <div
            style={{
              width: `${Math.min(1, view.progress) * 100}%`,
              height: '100%',
              borderRadius: 3,
              background: c,
            }}
          />
        </div>
      ) : null}
      {view.tag ? (
        <div
          style={{
            position: 'absolute',
            left: 20,
            bottom: -21,
            fontSize: 17,
            color: colors.green,
            background: colors.bg,
            border: `1.5px solid ${alpha(colors.green, 0.4)}`,
            borderRadius: 8,
            padding: '2px 12px',
          }}
        >
          {view.tag}
        </div>
      ) : null}
    </div>
  );
};

/**
 * A commit line, typed rather than faded: `progress` is the typed fraction
 * across the prompt and the message together, with a block cursor while the
 * typing runs. Zero progress is no pill at all, which is also how it leaves.
 */
export const CommitPill: React.FC<{
  parts: Array<{text: string; color?: string; weight?: number}>;
  x: number;
  y: number;
  progress: number;
  scale?: number;
}> = ({parts, x, y, progress, scale = 1}) => {
  if (progress <= 0) return null;
  const prefix = '$ git commit -m ';
  const total = prefix.length + parts.reduce((n, p) => n + p.text.length, 0);
  let budget = Math.ceil(total * Math.min(1, progress));
  const take = (text: string) => {
    const shown = text.slice(0, Math.max(0, budget));
    budget -= text.length;
    return shown;
  };
  return (
    <div
      style={{
        position: 'absolute',
        left: x,
        top: y,
        transform: `translateX(-50%) scale(${scale})`,
        fontFamily: font,
        fontSize: 24,
        color: colors.fg,
        background: colors.panel,
        border: `2px solid ${colors.panelEdge}`,
        borderRadius: 999,
        padding: '10px 24px',
        whiteSpace: 'pre',
      }}
    >
      <span style={{color: colors.dim}}>{take(prefix)}</span>
      {parts.map((p, i) => (
        <span key={i} style={{color: p.color ?? colors.fg, fontWeight: p.weight ?? 400}}>
          {take(p.text)}
        </span>
      ))}
      {progress < 1 ? <span style={{color: colors.green}}>█</span> : null}
    </div>
  );
};

export type CapSeg = {text: string; color?: string; weight?: number};

/** One row of a scene's terminal: a typed command or a printed line. */
export type TermRow =
  | {kind: 'cmd'; start: number; typeEnd: number; text: string}
  | {kind: 'out'; start: number; segs: CapSeg[]};

/** One shared terminal cadence: fourteen visible characters each second. */
export const TYPING_CHARS_PER_SECOND = 14;
export const typingFrames = (text: string, fps = 20) => Math.ceil(text.length * fps / TYPING_CHARS_PER_SECOND);
export const typingProgress = (frame: number, start: number, text: string, fps = 20) =>
  interpolate(frame, [start, start + typingFrames(text, fps)], [0, 1], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });

export const cmdRow = (start: number, text: string): TermRow => ({
  kind: 'cmd',
  start,
  typeEnd: start + typingFrames(text),
  text,
});

/** A printed line: on screen whole the moment the graph shows its state. */
export const outRow = (start: number, segs: CapSeg[]): TermRow => ({kind: 'out', start, segs});

/**
 * The scene's terminal, a block under the diagram: commands type, output
 * prints at the moment the diagram shows the state it reports, the last few
 * lines stay on screen like any terminal's tail, and the prompt comes back
 * and blinks whenever the session is between commands.
 */
export const SceneTerminal: React.FC<{rows: TermRow[]; f: number; lines?: number}> = ({rows, f, lines = 4}) => {
  const available = rows.filter((row) => f >= row.start);
  const shownCount = available.length;
  // Reserve whole physical lines at the fixed monospace canvas width. Drop
  // complete older rows when a command wraps, rather than clipping half a row.
  const visible: TermRow[] = [];
  let remaining = lines;
  for (let index = available.length - 1; index >= 0; index--) {
    const row = available[index];
    const text = row.kind === 'cmd' ? `$ ${row.text}█` : row.segs.map((segment) => segment.text).join('');
    const physicalLines = text.split('\n').reduce((total, line) => total + Math.max(1, Math.ceil(line.length / 128)), 0);
    if (physicalLines > remaining && visible.length) break;
    visible.unshift(row);
    remaining -= physicalLines;
  }
  const last = visible[visible.length - 1];
  const typing = last !== undefined && last.kind === 'cmd' && f < last.typeEnd;
  // The prompt only comes back once the running command's output is
  // complete: before the first command, or when whatever comes next is a
  // command, or when the session is over.
  const next = rows[shownCount];
  const prompt = !typing && (next === undefined || next.kind === 'cmd');
  const blink = Math.floor(f / 10) % 2 === 0;
  const roomForPrompt = remaining > 0;
  return (
    <div
      data-demo-terminal
      style={{
        position: 'absolute',
        left: 150,
        right: 150,
        bottom: 26,
        height: lines * 32 + 34,
        boxSizing: 'border-box',
        borderRadius: 12,
        background: colors.panel,
        border: `1.5px solid ${colors.panelEdge}`,
        padding: '15px 26px',
        fontFamily: font,
        fontSize: 20,
        lineHeight: '32px',
        whiteSpace: 'pre-wrap',
        overflowWrap: 'anywhere',
        wordBreak: 'break-all',
        overflow: 'hidden',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'flex-end',
      }}
    >
      {visible.map((row) => {
        if (row.kind === 'cmd') {
          const done = f >= row.typeEnd;
          const frac = done ? 1 : Math.max(0, (f - row.start) / (row.typeEnd - row.start));
          const shown = row.text.slice(0, Math.ceil(row.text.length * frac));
          return (
            <div key={`c${row.start}`} style={{minHeight: 32, lineHeight: '32px', flexShrink: 0}}>
              <span style={{color: colors.green, fontWeight: 700}}>$ </span>
              <span style={{color: colors.fg}}>{shown}</span>
              {!done && <span style={{color: colors.green}}>█</span>}
            </div>
          );
        }
        return (
          <div key={`o${row.start}`} style={{minHeight: 32, lineHeight: '32px', flexShrink: 0}}>
            {row.segs.map((s, j) => (
              <span key={j} style={{color: s.color ?? colors.fg, fontWeight: s.weight ?? 400}}>
                {s.text}
              </span>
            ))}
          </div>
        );
      })}
      {prompt && roomForPrompt && (
        <div style={{minHeight: 32, lineHeight: '32px', flexShrink: 0}}>
          <span style={{color: colors.green, fontWeight: 700}}>$ </span>
          <span style={{color: colors.fg, opacity: blink ? 1 : 0}}>█</span>
        </div>
      )}
    </div>
  );
};

export const SceneTitle: React.FC<{text: string; progress: number}> = ({text, progress}) => {
  if (progress <= 0) return null;
  const shown = text.slice(0, Math.ceil(text.length * Math.min(1, progress)));
  return (
    <div
      style={{
        position: 'absolute',
        left: 0,
        right: 0,
        top: 74,
        textAlign: 'center',
        fontFamily: font,
        fontSize: 36,
        fontWeight: 700,
        color: colors.fg,
      }}
    >
      {shown}
      {progress < 1 ? <span style={{color: colors.green}}>█</span> : null}
    </div>
  );
};

export const Wordmark: React.FC = () => (
  <div
    style={{
      position: 'absolute',
      left: 48,
      top: 40,
      fontFamily: font,
      fontSize: 28,
      fontWeight: 700,
      color: colors.dim,
    }}
  >
    dispat
  </div>
);

/** Fade helper: in at [a, b], out at [c, d]. For panels, never for text. */
export function fadeIO(frame: number, a: number, b: number, c: number, d: number) {
  return interpolate(frame, [a, b, c, d], [0, 1, 1, 0], {
    extrapolateLeft: 'clamp',
    extrapolateRight: 'clamp',
  });
}

/**
 * Terminal input: typed over [a, b], held, and cleared at once at `cut`,
 * because terminal text never fades.
 */
export function typeIO(frame: number, a: number, b: number, cut: number) {
  if (frame >= cut) return 0;
  return interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
}

// Caption builders in the pretty log's palette.
export const T = (t: string): CapSeg => ({text: t, color: colors.dim});
export const INF: CapSeg[] = [T('12:04:05 '), {text: 'INF ', color: colors.green, weight: 700}];
export const ERR: CapSeg[] = [T('12:04:05 '), {text: 'ERR ', color: colors.red, weight: 700}];
export const WRN: CapSeg[] = [T('12:04:05 '), {text: 'WRN ', color: colors.yellow, weight: 700}];
export const DBG: CapSeg[] = [T('12:04:05 '), {text: 'DBG ', color: colors.dim, weight: 700}];
export const msg = (t: string): CapSeg => ({text: t, weight: 700});
export const kv = (k: string, v: string): CapSeg[] => [
  {text: ` ${k}=`, color: colors.cyan},
  {text: v, color: colors.fg},
];
