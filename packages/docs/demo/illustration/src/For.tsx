import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {SceneTerminal, TermRow, cmdRow, outRow} from './components';
import {colors, font} from './theme';
import fixture from '../../fixtures/for/expected.json';

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
  const opacity = interpolate(frame, [0, 10, FOR_DURATION - 10, FOR_DURATION - 2], [0, 1, 1, 0], {
    extrapolateLeft: 'clamp', extrapolateRight: 'clamp',
  });
  return <AbsoluteFill style={{background: colors.bg, color: colors.fg, fontFamily: font, opacity}}>
    <div style={{position: 'absolute', left: 0, right: 0, top: 332, textAlign: 'center', fontSize: 42, fontWeight: 700}}>One command per changed package</div>
    <div style={{position: 'absolute', left: 0, right: 0, top: 396, textAlign: 'center', fontSize: 25, color: colors.dim}}>A renderer change reaches its game and native-app consumers.</div>
    <div style={{position: 'absolute', left: 300, right: 300, top: 478, display: 'grid', gridTemplateColumns: '1fr auto 1fr auto 1fr', alignItems: 'center', gap: 24}}>
      {fixture.order.map((name, index) => {
        const started = frame >= 150 + index * 34;
        return <React.Fragment key={name}>
          {index > 0 && <div style={{fontSize: 40, color: started ? colors.green : colors.faint}}>→</div>}
          <div style={{minWidth: 0, minHeight: 230, display: 'grid', alignContent: 'center', padding: '28px 18px', borderRadius: 16, textAlign: 'center', fontSize: 29, fontWeight: 700, background: colors.panel, border: `2px solid ${started ? colors.green : colors.panelEdge}`, color: started ? colors.green : colors.dim}}>
            {name}<div style={{marginTop: 10, fontSize: 20, fontWeight: 400, color: colors.dim}}>{['renderer', 'gameplay', 'desktop launcher'][index]}</div><div style={{marginTop: 24, fontSize: 18, fontWeight: 400, color: colors.dim}}>{started ? 'command finished' : index === 0 ? 'changed' : `consumes ${fixture.order[index - 1]}`}</div>
          </div>
        </React.Fragment>;
      })}
    </div>
    <SceneTerminal rows={rows} f={frame} />
  </AbsoluteFill>;
};
