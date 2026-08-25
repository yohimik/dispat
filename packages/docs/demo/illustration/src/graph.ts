// The demo monorepo: seven packages across six ecosystems, the shape the CLI
// README's terminal tour describes with room around it. The release story
// runs along the top row: api is a container image, so it can only build
// once core is published, and web consumes api. utils is a provider with a
// fix of its own in the master cut, sdk consumes utils, and docs and mobile
// stand alone. sdk, docs, and mobile exist to be left out: a release plan is
// the packages the commits reach, and most of the graph is simply not in it.
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
export const edges: Array<{from: string; to: string}> = [
  {from: 'core', to: 'api'},
  {from: 'utils', to: 'api'},
  {from: 'api', to: 'web'},
  {from: 'utils', to: 'sdk'},
];

/** Anchor points: provider's right edge center to consumer's left edge center. */
export function edgeAnchors(e: {from: string; to: string}) {
  const a = byId[e.from];
  const b = byId[e.to];
  return {
    x1: a.x + NODE_W / 2,
    y1: a.y,
    x2: b.x - NODE_W / 2,
    y2: b.y,
  };
}

export function edgePath(e: {from: string; to: string}) {
  const {x1, y1, x2, y2} = edgeAnchors(e);
  const dx = (x2 - x1) * 0.5;
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
}
