import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {SceneTerminal, TermRow, cmdRow, outRow} from './components';
import {colors, font} from './theme';
import fixture from '../../fixtures/for/expected.json';
import {useDemoLayout} from './layout';

// `for --changed` uses the release window by default. `--since` replaces that
// window, and `--consumers` expands it downstream. The loop is sequential in
// dependency order and runs the literal command once per selected item.
const rows: TermRow[] = [
  cmdRow(12, fixture.command),
  ...fixture.output.map((text, index) => outRow(150 + index * 34, [{text, color: colors.green}])),
];

export const FOR_DURATION = 300;

export const For: React.FC = () => {
  const frame = useCurrentFrame();
  const mobile = useDemoLayout();
  const opacity = interpolate(frame, [0, 10, FOR_DURATION - 10, FOR_DURATION - 2], [0, 1, 1, 0], {
    extrapolateLeft: 'clamp', extrapolateRight: 'clamp',
  });
  return <AbsoluteFill style={{background: colors.bg, color: colors.fg, fontFamily: font, opacity}}>
    <div style={{position: 'absolute', left: 24, right: 24, top: mobile ? 100 : 332, textAlign: 'center', fontSize: mobile ? 36 : 42, fontWeight: 700}}>One command per changed package</div>
    <div style={{position: 'absolute', left: 30, right: 30, top: mobile ? 210 : 396, textAlign: 'center', fontSize: 26, color: colors.dim}}>A renderer change reaches its game and native-app consumers.</div>
    <div style={{position: 'absolute', left: mobile ? 40 : 300, right: mobile ? 40 : 300, top: mobile ? 315 : 478, display: 'grid', gridTemplateColumns: mobile ? '1fr' : '1fr auto 1fr auto 1fr', alignItems: 'center', gap: mobile ? 14 : 24}}>
      {fixture.order.map((name, index) => {
        const started = frame >= 150 + index * 34;
        return <React.Fragment key={name}>
          {index > 0 && <div style={{fontSize: 40, lineHeight: mobile ? '28px' : undefined, textAlign: 'center', transform: mobile ? 'rotate(90deg)' : undefined, color: started ? colors.green : colors.faint}}>→</div>}
          <div style={{minWidth: 0, minHeight: mobile ? 145 : 230, display: 'grid', alignContent: 'center', padding: mobile ? '18px' : '28px 18px', borderRadius: 16, textAlign: 'center', fontSize: 29, fontWeight: 700, background: colors.panel, border: `2px solid ${started ? colors.green : colors.panelEdge}`, color: started ? colors.green : colors.dim}}>
            {name}<div style={{marginTop: 10, fontSize: mobile ? 26 : 20, fontWeight: 400, color: colors.dim}}>{['renderer', 'gameplay', 'desktop launcher'][index]}</div><div style={{marginTop: mobile ? 10 : 24, fontSize: mobile ? 26 : 18, fontWeight: 400, color: colors.dim}}>{started ? 'command finished' : index === 0 ? 'changed' : `consumes ${fixture.order[index - 1]}`}</div>
          </div>
        </React.Fragment>;
      })}
    </div>
    <SceneTerminal rows={rows} f={frame} />
  </AbsoluteFill>;
};
