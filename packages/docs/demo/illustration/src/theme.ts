// The shared visual system: the documentation site's background, the log
// palette of the CLI's pretty mode, and one mono typeface throughout.
export const colors = {
  bg: '#101713',
  panel: '#161d18',
  panelEdge: '#232b26',
  fg: '#dce3dd',
  dim: '#5f6a62',
  faint: '#39443d',
  green: '#4cd98a',
  yellow: '#f5c542',
  red: '#ff6b6b',
  cyan: '#5fc9b8',
  blue: '#6ea8fe',
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
