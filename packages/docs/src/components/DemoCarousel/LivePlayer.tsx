import {Player, type PlayerRef} from '@remotion/player';
import React from 'react';

type Props = Omit<React.ComponentProps<typeof Player>, 'component' | 'lazyComponent'> & {
  component: React.ComponentType;
  sourceHeight: number;
  cropTop: number;
};

/** Kept in its own browser-only chunk so the landing route never eagerly loads Remotion Player. */
const LivePlayer = React.forwardRef<PlayerRef, Props>(({component: Component, sourceHeight, cropTop, ...props}, ref) => {
  // Export compositions retain their original canvas. The live player removes
  // the title strip, since the accessible page heading sits outside the scene.
  const Scene = React.useMemo(() => function FramedScene() {
    return (
      <div data-demo-scene-root style={{position: 'absolute', width: 1920, height: sourceHeight, top: -cropTop, lineHeight: 1.2}}>
        <Component />
      </div>
    );
  }, [Component, sourceHeight, cropTop]);
  return <Player {...props} component={Scene} ref={ref} />;
});
LivePlayer.displayName = 'LivePlayer';
export default LivePlayer;
