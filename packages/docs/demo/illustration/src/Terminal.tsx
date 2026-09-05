import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {alpha, colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, WRN, msg, kv} from './components';
import {useDemoLayout} from './layout';

// The steps-as-commands claim, twenty-two seconds at Root.tsx's twenty
// frames per second: the release flow is the same everywhere, but each
// package orders its own step set inside it, so the strip is drawn INSIDE
// each package's row. core takes the release's default, publish first and
// the records written at the end of the run. api nests the steps in its own
// flow, changelog then commit before its publish, so the commit contains
// the changelog. utils publishes its GitHub release from its own announce
// script, right after the artifact upload. Then the second act: run alone
// after the release, `dispat changelog` finds the entry already written and
// says so (W226), the same check that lets a package's own flow and the
// release stage coexist without doing the work twice.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

type Step = {name: string; at: number; dur: number; step?: boolean};

type Row = {
  id: string;
  eco: string;
  ecoColor: string;
  y: number;
  note: string;
  steps: Step[];
};

// One strip per package: the same run, each package's own step set. `step`
// marks the chips that are step commands rather than stages.
const ROWS: Row[] = [
  {
    id: 'core',
    eco: 'npm',
    ecoColor: colors.red,
    y: 392,
    note: "the release's own order: the records written at the end of the run",
    steps: [
      {name: 'build', at: 40, dur: 62},
      {name: 'publish', at: 108, dur: 36},
      {name: 'changelog', at: 210, dur: 18, step: true},
      {name: 'commit', at: 232, dur: 20, step: true},
      {name: 'github', at: 256, dur: 18, step: true},
    ],
  },
  {
    id: 'api',
    eco: 'docker',
    ecoColor: colors.blue,
    y: 562,
    note: 'flow.beforePublish: [changelog, commit]. The commit contains the changelog.',
    steps: [
      {name: 'build', at: 40, dur: 64},
      {name: 'changelog', at: 110, dur: 18, step: true},
      {name: 'commit', at: 132, dur: 20, step: true},
      {name: 'publish', at: 158, dur: 38},
    ],
  },
  {
    id: 'utils',
    eco: 'go',
    ecoColor: colors.cyan,
    y: 732,
    note: 'announce: dispat github. The GitHub release follows the artifact upload.',
    steps: [
      {name: 'build', at: 40, dur: 60},
      {name: 'publish', at: 106, dur: 36},
      {name: 'github', at: 148, dur: 22, step: true},
    ],
  },
];

const rows: TermRow[] = [
  cmdRow(14, 'dispat'),
  outRow(44, [...INF, msg('build started'), ...kv('package', 'core'), ...kv('stage', 'build'), ...kv('version', '1.5.0')]),
  outRow(114, [...INF, msg('changelog written'), ...kv('package', 'api'), ...kv('by', 'flow.beforePublish')]),
  outRow(136, [...INF, msg('release commit'), ...kv('package', 'api'), ...kv('by', 'flow.beforePublish')]),
  outRow(146, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0')]),
  outRow(152, [...INF, msg('published'), ...kv('package', 'utils'), ...kv('tag', 'utils@2.0.4')]),
  outRow(174, [...INF, msg('github release'), ...kv('package', 'utils'), ...kv('by', 'announce')]),
  outRow(200, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3')]),
  outRow(214, [...INF, msg('changelog written'), ...kv('package', 'core'), ...kv('by', 'release')]),
  outRow(236, [...INF, msg('release commit'), ...kv('tagged', '"core, api, utils"')]),
  outRow(260, [...INF, msg('github release'), ...kv('package', 'core'), ...kv('by', 'release')]),
  outRow(282, [...INF, msg('done'), ...kv('published', '3'), ...kv('failed', '0')]),
  cmdRow(312, 'dispat changelog -p core'),
  outRow(356, [...WRN, msg('W226'), ...kv('package', 'core'), ...kv('reason', '"CHANGELOG.md already has the entry for core@1.5.0"')]),
  outRow(378, [{text: '# every step is also a command; run alone, it finds the work done', color: colors.dim}]),
];

export const TERMINAL_DURATION = 442;

const BLOCK_W = 246;

export const Terminal: React.FC = () => {
  const f = useCurrentFrame();
  const mobile = useDemoLayout();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [TERMINAL_DURATION - 10, TERMINAL_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {ROWS.map((row) => (
        <div key={row.id}>
          {/* The package this strip belongs to. */}
          <div style={{position: 'absolute', left: mobile ? 30 : 140, top: mobile ? 150 + ROWS.indexOf(row) * 270 : row.y + 4, width: mobile ? 650 : 310}}>
            <span style={{fontSize: 30, fontWeight: 700, color: colors.fg}}>{row.id}</span>
            <span
              style={{
                marginLeft: 14,
                fontSize: mobile ? 26 : 17,
                color: row.ecoColor,
                border: `1.5px solid ${alpha(row.ecoColor, 0.4)}`,
                borderRadius: 999,
                padding: '1px 12px',
              }}>
              {row.eco}
            </span>
          </div>
          {row.steps.map((step, i) => {
            const lit = f >= step.at;
            const active = lit && f < step.at + step.dur;
            const c = active ? colors.cyan : lit ? colors.green : colors.faint;
            return (
              <div key={step.name} style={{position: 'absolute', left: mobile ? 30 + (i % 3) * 220 : 470 + i * 266, top: mobile ? 215 + ROWS.indexOf(row) * 270 + Math.floor(i / 3) * 76 : row.y - 4, width: mobile ? 200 : BLOCK_W}}>
                <div
                  style={{
                    borderRadius: 12,
                    border: `2px ${step.step ? 'dashed' : 'solid'} ${c}`,
                    background: colors.panel,
                    boxShadow: active ? `0 0 24px ${alpha(c, 0.2)}` : 'none',
                    padding: '15px 0 13px',
                    textAlign: 'center',
                    fontSize: mobile ? 26 : 22,
                    whiteSpace: 'nowrap',
                    fontWeight: 700,
                    color: lit ? colors.fg : colors.dim,
                  }}>
                  {step.name}
                  {/* A dashed border marks a step command among the stages. */}
                  <div style={{display: mobile ? 'none' : 'block', marginTop: 2, fontSize: 14, fontWeight: 400, color: colors.dim, minHeight: 18}}>
                    {step.step ? 'step command' : 'stage'}
                  </div>
                </div>
              </div>
            );
          })}
          <div style={{position: 'absolute', left: mobile ? 30 : 470, right: mobile ? 30 : 120, top: mobile ? 355 + ROWS.indexOf(row) * 270 : row.y + 88, fontSize: mobile ? 22 : 17, lineHeight: mobile ? '29px' : undefined, color: colors.dim}}>
            {row.note}
          </div>
        </div>
      ))}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
