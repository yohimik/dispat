import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {loadFont} from '@remotion/google-fonts/JetBrainsMono';
import {colors, mix} from './theme';
import {edges, edgeAnchors, edgePath, pkgs, type Edge, type Pkg} from './graph';
import {NodeView, PkgNode, Wordmark} from './components';

loadFont();

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
  const nodeLookup = Object.fromEntries(graphPkgs.map((pkg) => [pkg.id, pkg]));
  return (
    <AbsoluteFill style={{background: colors.bg}}>
      <div style={{opacity: graphOpacity}}>
        <svg
          width="1920"
          height="1080"
          style={{position: 'absolute', inset: 0}}
          viewBox="0 0 1920 1080"
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
            const anchors = edgeAnchors(e, nodeLookup);
            return (
              <g key={i}>
                <path
                  d={edgePath(e, nodeLookup)}
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
                    y={(anchors.y1 + anchors.y2) / 2 - 12}
                    textAnchor="middle"
                    fill={colors.dim}
                    fontFamily="'JetBrains Mono', monospace"
                    fontSize={17}
                  >
                    {e.label}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
        {graphPkgs.map((p) => (
          <PkgNode key={p.id} pkg={p} view={views[p.id] ?? {state: 'idle'}} />
        ))}
        {terminal}
        <Wordmark />
      </div>
      {children}
    </AbsoluteFill>
  );
};
