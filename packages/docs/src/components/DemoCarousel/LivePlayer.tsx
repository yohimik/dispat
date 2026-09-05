import {Player, type PlayerRef} from '@remotion/player';
import React from 'react';
import {DemoLayoutProvider, MOBILE_WIDTH, MOBILE_HEIGHT} from '../../../demo/illustration/src/layout';

type Props = Omit<React.ComponentProps<typeof Player>, 'component' | 'lazyComponent' | 'compositionWidth' | 'compositionHeight'> & {
  component: React.ComponentType;
  mobile: boolean;
  sourceHeight: number;
  cropTop: number;
};

/** Kept in its own browser-only chunk so the landing route never eagerly loads Remotion Player. */
const LivePlayer = React.forwardRef<PlayerRef, Props>(({component: Component, sourceHeight, cropTop, mobile, ...props}, ref) => {
  // Desktop crops the export title strip; portrait reflows the same scene.
  // The accessible page heading sits outside both canvases.
  const Scene = React.useMemo(() => function FramedScene() {
    return (
      <div data-demo-scene-root style={{position: 'absolute', width: mobile ? MOBILE_WIDTH : 1920, height: mobile ? MOBILE_HEIGHT : sourceHeight, top: mobile ? 0 : -cropTop, lineHeight: 1.2}}>
        <DemoLayoutProvider mobile={mobile}><Component /></DemoLayoutProvider>
      </div>
    );
  }, [Component, sourceHeight, cropTop, mobile]);
  // These compositions contain no audio. Do not allocate silent audio tags or
  // create a Web Audio context just to advance visual frames.
  return <Player {...props} component={Scene} ref={ref} compositionWidth={mobile ? MOBILE_WIDTH : 1920} compositionHeight={mobile ? MOBILE_HEIGHT : 800} initiallyMuted numberOfSharedAudioTags={0} showVolumeControls={false} />;
});
LivePlayer.displayName = 'LivePlayer';
export default LivePlayer;
