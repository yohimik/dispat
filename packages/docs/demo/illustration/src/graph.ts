// The default demo monorepo used by the original graph scenes. Focused scenes
// may supply another set of nodes and edges to Stage without changing what an
// unrelated story means.
export type Pkg = {
  id: string;
  eco: 'npm' | 'go' | 'docker' | 'cargo' | 'ios';
  manifest: string;
  base: string;
  next: string;
  x: number;
  y: number;
};

export const NODE_W = 350;
export const NODE_H = 160;

// The top ~335px stays clear on purpose: the master's scene titles type
// there, and the landing page crops most of that strip off each clip,
// leaving one title line's worth over the graph. The bottom ~190px belongs
// to the scene's terminal, and the row spacing leaves clear air between the
// title, the graph, and the terminal.
export const pkgs: Pkg[] = [
  {id: 'core', eco: 'npm', manifest: 'package.json', base: '1.4.2', next: '1.5.0', x: 330, y: 415},
  {id: 'api', eco: 'docker', manifest: 'Dockerfile', base: '0.8.2', next: '0.8.3', x: 890, y: 415},
  {id: 'web', eco: 'npm', manifest: 'package.json', base: '2.1.0', next: '2.1.1', x: 1450, y: 415},
  {id: 'utils', eco: 'go', manifest: 'go.mod', base: '2.0.3', next: '2.0.4', x: 330, y: 600},
  {id: 'sdk', eco: 'cargo', manifest: 'Cargo.toml', base: '0.3.1', next: '0.3.2', x: 890, y: 600},
  {id: 'docs', eco: 'npm', manifest: 'package.json', base: '1.1.0', next: '1.1.1', x: 660, y: 785},
  {id: 'mobile', eco: 'ios', manifest: 'Info.plist', base: '3.1.0', next: '3.2.0', x: 1220, y: 785},
];

export const byId = Object.fromEntries(pkgs.map((p) => [p.id, p]));

/** provider -> consumer */
export type Edge = {from: string; to: string; label?: string};

export const edges: Edge[] = [
  {from: 'core', to: 'api'},
  {from: 'utils', to: 'api'},
  {from: 'api', to: 'web'},
  {from: 'utils', to: 'sdk'},
];

/** The policy-focused graph used by Order and the recovery story. */
export const releaseGraph = {
  pkgs: pkgs.map((pkg): Pkg => {
    if (pkg.id === 'core') return {...pkg, eco: 'go', manifest: 'go.mod'};
    if (pkg.id === 'api') return {...pkg, eco: 'go', manifest: 'go.mod'};
    if (pkg.id === 'utils' || pkg.id === 'sdk') return {...pkg, eco: 'npm', manifest: 'package.json'};
    return pkg;
  }),
  edges: [
    {from: 'core', to: 'api', label: 'wait for publish'},
    {from: 'api', to: 'web'},
    {from: 'utils', to: 'sdk', label: 'local workspace'},
  ] satisfies Edge[],
};

/** Anchor points: provider's right edge center to consumer's left edge center. */
export function edgeAnchors(e: Edge, nodes: Record<string, Pkg> = byId) {
  const a = nodes[e.from];
  const b = nodes[e.to];
  return {
    x1: a.x + NODE_W / 2,
    y1: a.y,
    x2: b.x - NODE_W / 2,
    y2: b.y,
  };
}

export function edgePath(e: Edge, nodes: Record<string, Pkg> = byId) {
  const {x1, y1, x2, y2} = edgeAnchors(e, nodes);
  const dx = (x2 - x1) * 0.5;
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}
