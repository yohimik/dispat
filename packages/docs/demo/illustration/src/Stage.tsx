import React from 'react';
import {AbsoluteFill, interpolate, interpolateColors, useCurrentFrame} from 'remotion';
import {loadFont} from '@remotion/google-fonts/JetBrainsMono';
import {colors} from './theme';
import {edges, edgePath, pkgs} from './graph';
import {NodeView, PkgNode, Wordmark} from './components';

loadFont();

/**
 * An edge highlight: from `start` the whole edge turns the plan's green and
 * stays that way, so the lit edges accumulate into the release's path
 * through the graph. `off` drops it again, which is how the planning glow
 * makes way for the run to light the same edges as the publishes land. No
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
  pulses?: Pulse[];
  edgeDraw?: (i: number) => number;
  graphOpacity?: number;
  /** The scene's terminal, a block under the graph. */
  terminal?: React.ReactNode;
  children?: React.ReactNode;
}> = ({views, pulses = [], edgeDraw, graphOpacity = 1, terminal, children}) => {
  const frame = useCurrentFrame();
  return (
    <AbsoluteFill style={{background: colors.bg}}>
      <div style={{opacity: graphOpacity}}>
        <svg
          width="1920"
          height="1080"
          style={{position: 'absolute', inset: 0}}
          viewBox="0 0 1920 1080"
        >
          {edges.map((e, i) => {
            const draw = edgeDraw ? edgeDraw(i) : 1;
            // Ramp to green when the highlight reaches this edge and hold,
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
            return (
              <path
                key={i}
                d={edgePath(e)}
                fill="none"
                stroke={interpolateColors(hot, [0, 1], [colors.faint, colors.green])}
                strokeWidth={3 + hot * 2}
                pathLength={1}
                strokeDasharray={1}
                strokeDashoffset={1 - draw}
              />
            );
          })}
        </svg>
        {pkgs.map((p) => (
          <PkgNode key={p.id} pkg={p} view={views[p.id] ?? {state: 'idle'}} />
        ))}
        {terminal}
        <Wordmark />
      </div>
      {children}
    </AbsoluteFill>
  );
};
