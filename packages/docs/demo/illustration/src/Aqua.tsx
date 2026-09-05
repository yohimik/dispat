import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig} from 'remotion';
import fixture from '../../fixtures/aqua/expected.json';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, typingFrames} from './components';

const FIRST = 32;
const FIRST_DONE = FIRST + typingFrames(fixture.commands[0]);
const SECOND = FIRST_DONE + 44;
const SECOND_DONE = SECOND + typingFrames(fixture.commands[1]);
const APPLIED = SECOND_DONE + 12;
export const AQUA_DURATION = APPLIED + 100;
const show = (frame: number, at: number) => interpolate(frame, [at, at + 12], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const rows: TermRow[] = [
  cmdRow(FIRST, fixture.commands[0]),
  outRow(FIRST_DONE + 8, [{text: `selected: ${fixture.selectedPackages.join(', ')}`, color: colors.dim}]),
  cmdRow(SECOND, fixture.commands[1]),
  outRow(APPLIED, [{text: `applied=${fixture.outcomes.applied} skipped-dynamic=${fixture.outcomes.skippedDynamic} missing=${fixture.outcomes.missing}`, color: colors.green}]),
];

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
          const changed = before !== after && frame >= APPLIED;
          return (
            <div key={name} style={{flex: 1, minWidth: 0, minHeight: compact ? 180 : undefined, opacity: show(frame, 32 + index * 26), border: `2px solid ${changed ? colors.green : colors.panelEdge}`, borderRadius: 18, background: colors.panel, padding: compact ? '24px 28px' : 30}}>
              <div style={{fontSize: compact ? 28 : 30, lineHeight: 1.2, fontWeight: 700, overflowWrap: 'anywhere'}}>{name}</div>
              <div style={{marginTop: compact ? 14 : 22, fontSize: compact ? 25 : 27, lineHeight: 1.25, color: changed ? colors.green : colors.dim}}>
                {before}{changed ? ` → ${after}` : ''}
              </div>
              <div style={{marginTop: compact ? 10 : 16, lineHeight: 1.3, color: colors.dim}}>{changed ? 'applied' : index === 1 && frame >= APPLIED ? 'dynamic version skipped' : 'selected'}</div>
            </div>
          );
        })}
      </div>
      <SceneTerminal rows={rows} f={frame} lines={4} />
    </AbsoluteFill>
  );
};
