import React, {createContext, useContext} from 'react';

export const MOBILE_WIDTH = 720;
export const MOBILE_HEIGHT = 1280;

const DemoLayoutContext = createContext(false);

/** Selects the portrait layout used by the live mobile player. */
export const DemoLayoutProvider: React.FC<React.PropsWithChildren<{mobile: boolean}>> = ({mobile, children}) => (
  <DemoLayoutContext.Provider value={mobile}>{children}</DemoLayoutContext.Provider>
);

/** False for Remotion exports and any player that does not opt into portrait. */
export const useDemoLayout = () => useContext(DemoLayoutContext);
