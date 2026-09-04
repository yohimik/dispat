import {Player, type PlayerRef} from '@remotion/player';
import React from 'react';

type Props = React.ComponentProps<typeof Player>;

/** Kept in its own browser-only chunk so the landing route never eagerly loads Remotion Player. */
const LivePlayer = React.forwardRef<PlayerRef, Props>((props, ref) => <Player {...props} ref={ref} />);
LivePlayer.displayName = 'LivePlayer';
export default LivePlayer;
