import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import fixture from '../../fixtures/aqua/expected.json';
import {colors, font} from './theme';

export const AQUA_DURATION = 320;
const show = (frame: number, at: number) => interpolate(frame, [at, at + 12], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

export const Aqua: React.FC = () => {
  const frame = useCurrentFrame();
  return (
    <AbsoluteFill style={{background: colors.bg, color: colors.fg, fontFamily: font, padding: 64}}>
      <div style={{fontSize: 26, color: colors.dim}}>aqua.yaml + imported .aqua/tools.inc</div>
      <div style={{display: 'flex', gap: 36, marginTop: 80}}>
        {fixture.selectedPackages.map((name, index) => {
          const before = fixture.versionsBefore[name as keyof typeof fixture.versionsBefore];
          const after = fixture.versionsAfter[name as keyof typeof fixture.versionsAfter];
          const changed = before !== after && frame > 178;
          return (
            <div key={name} style={{flex: 1, opacity: show(frame, 32 + index * 26), border: `2px solid ${changed ? colors.green : colors.panelEdge}`, borderRadius: 18, background: colors.panel, padding: 30}}>
              <div style={{fontSize: 30, fontWeight: 700}}>{name}</div>
              <div style={{marginTop: 22, fontSize: 27, color: changed ? colors.green : colors.dim}}>
                {before}{changed ? ` → ${after}` : ''}
              </div>
              <div style={{marginTop: 16, color: colors.dim}}>{changed ? 'applied' : index === 1 && frame > 178 ? 'dynamic version skipped' : 'selected'}</div>
            </div>
          );
        })}
      </div>
      <div style={{position: 'absolute', left: 64, right: 64, bottom: 66, borderRadius: 14, background: '#0b100d', padding: 28, fontSize: 22, lineHeight: 1.65}}>
        <div style={{opacity: show(frame, 92), color: colors.cyan}}>$ {fixture.commands[0]}</div>
        <div style={{opacity: show(frame, 126), color: colors.dim}}>selected: {fixture.selectedPackages.join(', ')}</div>
        <div style={{opacity: show(frame, 166), color: colors.cyan}}>$ {fixture.commands[1]}</div>
        <div style={{opacity: show(frame, 216), color: colors.green}}>applied={fixture.outcomes.applied} skipped-dynamic={fixture.outcomes.skippedDynamic} missing={fixture.outcomes.missing}</div>
      </div>
    </AbsoluteFill>
  );
};
