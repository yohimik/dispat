import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig} from 'remotion';
import fixture from '../../fixtures/aqua/expected.json';
import {colors, font} from './theme';

export const AQUA_DURATION = 320;
const show = (frame: number, at: number) => interpolate(frame, [at, at + 12], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

export const Aqua: React.FC = () => {
  const frame = useCurrentFrame();
  const {height} = useVideoConfig();
  const compact = height <= 800;
  const inset = compact ? 48 : 64;
  return (
    <AbsoluteFill style={{background: colors.bg, color: colors.fg, fontFamily: font, padding: inset}}>
      <div style={{fontSize: compact ? 24 : 26, lineHeight: 1.3, color: colors.dim}}>
        aqua.yaml + imported .aqua/tools.inc
      </div>
      <div style={{display: 'flex', gap: 32, marginTop: compact ? 32 : 80}}>
        {fixture.selectedPackages.map((name, index) => {
          const before = fixture.versionsBefore[name as keyof typeof fixture.versionsBefore];
          const after = fixture.versionsAfter[name as keyof typeof fixture.versionsAfter];
          const changed = before !== after && frame > 178;
          return (
            <div key={name} style={{flex: 1, minWidth: 0, minHeight: compact ? 180 : undefined, opacity: show(frame, 32 + index * 26), border: `2px solid ${changed ? colors.green : colors.panelEdge}`, borderRadius: 18, background: colors.panel, padding: compact ? '24px 28px' : 30}}>
              <div style={{fontSize: compact ? 28 : 30, lineHeight: 1.2, fontWeight: 700, overflowWrap: 'anywhere'}}>{name}</div>
              <div style={{marginTop: compact ? 14 : 22, fontSize: compact ? 25 : 27, lineHeight: 1.25, color: changed ? colors.green : colors.dim}}>
                {before}{changed ? ` → ${after}` : ''}
              </div>
              <div style={{marginTop: compact ? 10 : 16, lineHeight: 1.3, color: colors.dim}}>{changed ? 'applied' : index === 1 && frame > 178 ? 'dynamic version skipped' : 'selected'}</div>
            </div>
          );
        })}
      </div>
      <div style={{position: 'absolute', display: compact ? 'flex' : 'block', flexDirection: compact ? 'column' : undefined, justifyContent: compact ? 'space-evenly' : undefined, left: inset, right: inset, bottom: compact ? 42 : 66, height: compact ? 350 : undefined, boxSizing: 'border-box', borderRadius: 14, background: colors.terminal, padding: compact ? '22px 26px' : 28, fontSize: compact ? 21 : 22, lineHeight: compact ? 1.5 : 1.65, overflowWrap: 'anywhere'}}>
        <div style={{opacity: show(frame, 92), color: colors.cyan}}>$ {fixture.commands[0]}</div>
        <div style={{opacity: show(frame, 126), color: colors.dim}}>selected: {fixture.selectedPackages.join(', ')}</div>
        <div style={{opacity: show(frame, 166), color: colors.cyan}}>$ {fixture.commands[1]}</div>
        <div style={{opacity: show(frame, 216), color: colors.green}}>applied={fixture.outcomes.applied} skipped-dynamic={fixture.outcomes.skippedDynamic} missing={fixture.outcomes.missing}</div>
      </div>
    </AbsoluteFill>
  );
};
