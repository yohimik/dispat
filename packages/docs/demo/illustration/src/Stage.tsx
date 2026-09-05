import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, mix} from './theme';
import {edges, edgeAnchors, edgePath, pkgs, type Edge, type Pkg} from './graph';
import {MOBILE_NODE_H, NodeView, PkgNode, Wordmark} from './components';
import {MOBILE_HEIGHT, MOBILE_WIDTH, useDemoLayout} from './layout';

const MOBILE_GRAPH_TOP = 120;
const MOBILE_GRAPH_BOTTOM = 810;

/** Stable topological rows for portrait. Cycles fall back to source order. */
const portraitPkgs = (graphPkgs: Pkg[], graphEdges: Edge[]): Pkg[] => {
  const known = new Set(graphPkgs.map(({id}) => id));
  const rank = new Map(graphPkgs.map(({id}) => [id, 0]));
  const indegree = new Map(graphPkgs.map(({id}) => [id, 0]));
  const outgoing = new Map(graphPkgs.map(({id}) => [id, [] as string[]]));
  for (const edge of graphEdges) {
    if (!known.has(edge.from) || !known.has(edge.to)) continue;
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
    outgoing.get(edge.from)?.push(edge.to);
  }
  const queue = graphPkgs.filter(({id}) => indegree.get(id) === 0).map(({id}) => id);
  let visited = 0;
  for (let cursor = 0; cursor < queue.length; cursor++) {
    const id = queue[cursor];
    visited++;
    for (const next of outgoing.get(id) ?? []) {
      rank.set(next, Math.max(rank.get(next) ?? 0, (rank.get(id) ?? 0) + 1));
      indegree.set(next, (indegree.get(next) ?? 1) - 1);
      if (indegree.get(next) === 0) queue.push(next);
    }
  }
  if (visited !== graphPkgs.length) graphPkgs.forEach(({id}) => rank.set(id, 0));

  // Isolated packages sit after the dependency chain so its edges never
  // appear to run through an unrelated package card.
  const connected = new Set(graphEdges.flatMap(({from, to}) => [from, to]));
  const isolatedRank = Math.max(0, ...rank.values()) + 1;
  graphPkgs.filter(({id}) => !connected.has(id)).forEach(({id}) => rank.set(id, isolatedRank));
  const levels = [...new Set(graphPkgs.map(({id}) => rank.get(id) ?? 0))].sort((a, b) => a - b);
  const rows: Pkg[][] = [];
  for (const level of levels) {
    const layer = graphPkgs.filter(({id}) => rank.get(id) === level);
    for (let index = 0; index < layer.length; index += 2) rows.push(layer.slice(index, index + 2));
  }
  const gap = rows.length > 1 ? (MOBILE_GRAPH_BOTTOM - MOBILE_GRAPH_TOP) / (rows.length - 1) : 0;
  return rows.flatMap((row, rowIndex) =>
    row.map((pkg, column) => ({
      ...pkg,
      x: row.length === 1 ? MOBILE_WIDTH / 2 : column === 0 ? 180 : 540,
      y: MOBILE_GRAPH_TOP + rowIndex * gap,
    })),
  );
};

/**
 * An edge highlight: from `start` the whole edge turns the plan's green and
 * stays that way, so the lit edges accumulate into the release's path
 * through the graph. `off` drops it again, which is how the planning glow
 * makes way for the run to light the same edges as their prerequisites land. No
 * token travels the curve; the edge itself carries the state.
 */
export type Pulse = {edge: number; start: number; off?: number};

/**
 * The shared stage: background, wordmark, the edge layer, and the package
 * cards. Scenes supply per-frame node views and propagation windows;
 * everything else is composition-specific overlay.
 */
export const Stage: React.FC<{
  views: Record<string, NodeView>;
  graphPkgs?: Pkg[];
  graphEdges?: Edge[];
  pulses?: Pulse[];
  edgeDraw?: (i: number) => number;
  /** Show the configured build policy on the edges that differ. */
  edgeLabels?: boolean;
  graphOpacity?: number;
  /** The scene's terminal, a block under the graph. */
  terminal?: React.ReactNode;
  children?: React.ReactNode;
}> = ({views, graphPkgs = pkgs, graphEdges = edges, pulses = [], edgeDraw, edgeLabels = false, graphOpacity = 1, terminal, children}) => {
  const frame = useCurrentFrame();
  const mobile = useDemoLayout();
  const displayedPkgs = mobile ? portraitPkgs(graphPkgs, graphEdges) : graphPkgs;
  const nodeLookup = Object.fromEntries(displayedPkgs.map((pkg) => [pkg.id, pkg]));
  const canvasWidth = mobile ? MOBILE_WIDTH : 1920;
  const canvasHeight = mobile ? MOBILE_HEIGHT : 1080;
  return (
    <AbsoluteFill style={{background: colors.bg}}>
      <div style={{opacity: graphOpacity}}>
        <svg
          width={canvasWidth}
          height={canvasHeight}
          style={{position: 'absolute', inset: 0}}
          viewBox={`0 0 ${canvasWidth} ${canvasHeight}`}
        >
          {graphEdges.map((e, i) => {
            const draw = edgeDraw ? edgeDraw(i) : 1;
            // Ramp to green when the configured prerequisite is ready and hold,
            // dropping only at an explicit `off`: the lit edges accumulate
            // into the release's path.
            let hot = 0;
            for (const p of pulses) {
              if (p.edge !== i) continue;
              const on = interpolate(frame, [p.start, p.start + 8], [0, 1], {
                extrapolateLeft: 'clamp',
                extrapolateRight: 'clamp',
              });
              const kept =
                p.off === undefined
                  ? 1
                  : interpolate(frame, [p.off, p.off + 6], [1, 0], {
                      extrapolateLeft: 'clamp',
                      extrapolateRight: 'clamp',
                    });
              hot = Math.max(hot, on * kept);
            }
            const anchors = mobile
              ? {
                  x1: nodeLookup[e.from].x,
                  y1: nodeLookup[e.from].y + MOBILE_NODE_H / 2,
                  x2: nodeLookup[e.to].x,
                  y2: nodeLookup[e.to].y - MOBILE_NODE_H / 2,
                }
              : edgeAnchors(e, nodeLookup);
            const path = mobile
              ? `M ${anchors.x1} ${anchors.y1} C ${anchors.x1} ${(anchors.y1 + anchors.y2) / 2}, ${anchors.x2} ${(anchors.y1 + anchors.y2) / 2}, ${anchors.x2} ${anchors.y2}`
              : edgePath(e, nodeLookup);
            return (
              <g key={i}>
                <path
                  d={path}
                  fill="none"
                  stroke={mix(colors.faint, colors.green, hot)}
                  strokeWidth={3 + hot * 2}
                  pathLength={1}
                  strokeDasharray={1}
                  strokeDashoffset={1 - draw}
                />
                {edgeLabels && e.label && draw > 0.8 && (
                  <text
                    x={(anchors.x1 + anchors.x2) / 2}
                    y={(anchors.y1 + anchors.y2) / 2 + (mobile ? 7 : -12)}
                    textAnchor="middle"
                    fill={colors.dim}
                    fontFamily="'JetBrains Mono', monospace"
                    fontSize={mobile ? 20 : 17}
                  >
                    {e.label}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
        {displayedPkgs.map((p) => (
          <PkgNode key={p.id} pkg={p} view={views[p.id] ?? {state: 'idle'}} />
        ))}
        {terminal}
        <Wordmark />
      </div>
      {children}
    </AbsoluteFill>
  );
};
