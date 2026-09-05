// The shared visual system: the documentation site's background, the log
// palette of the CLI's pretty mode, and one mono typeface throughout.
export const colors = {
  bg: 'var(--demo-bg, #101713)',
  panel: 'var(--demo-panel, #161d18)',
  panelEdge: 'var(--demo-panel-edge, #34463a)',
  terminal: 'var(--demo-terminal, #0b100d)',
  fg: 'var(--demo-fg, #dce3dd)',
  dim: 'var(--demo-dim, #9aaa9f)',
  faint: 'var(--demo-faint, #65766b)',
  green: 'var(--demo-green, #4cd98a)',
  yellow: 'var(--demo-yellow, #f5c542)',
  red: 'var(--demo-red, #ff6b6b)',
  cyan: 'var(--demo-cyan, #5fc9b8)',
  blue: 'var(--demo-blue, #6ea8fe)',
};

/** A theme color at an opacity that also works when the color is a CSS variable. */
export const alpha = (color: string, opacity: number) =>
  `color-mix(in srgb, ${color} ${Math.max(0, Math.min(1, opacity)) * 100}%, transparent)`;

/** Interpolate two theme colors without parsing their CSS-variable values in JS. */
export const mix = (from: string, to: string, progress: number) => {
  const toPercent = Math.max(0, Math.min(1, progress)) * 100;
  return `color-mix(in srgb, ${from} ${100 - toPercent}%, ${to} ${toPercent}%)`;
};

export const font = "'JetBrains Mono', 'SFMono-Regular', Menlo, monospace";

/** Node visual states over the story. */
export type NodeState =
  | 'idle'
  | 'changed'
  | 'building'
  | 'waiting'
  | 'publishing'
  | 'published'
  | 'failed'
  | 'skipped'
  | 'unchanged'
  | 'catchup'
  | 'running';

export const stateColor: Record<NodeState, string> = {
  idle: colors.faint,
  changed: colors.green,
  building: colors.cyan,
  waiting: colors.dim,
  publishing: colors.cyan,
  published: colors.green,
  failed: colors.red,
  skipped: colors.dim,
  unchanged: colors.dim,
  catchup: colors.yellow,
  running: colors.cyan,
};
