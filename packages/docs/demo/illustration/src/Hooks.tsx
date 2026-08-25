import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';

// The hooks claim, twenty seconds at Root.tsx's twenty frames per second:
// the release flow is the same everywhere, version, build, login, publish,
// announce, but each package brings its own hook set to it, so the stage
// strip is drawn INSIDE each package's row, three packages across two
// spaces, with only the hooks that package configured appearing above its
// stages. core and utils share the libs space, which is what makes the
// login's "once per space" visible: core's leg runs it, utils's leg reuses
// it, and api's services space runs its own. core's beforePublish hook is a
// script that prints its environment, which is how the DISPAT_* variables
// end up in the terminal.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

type Step = {
  name: string;
  /** This package's own hook on the stage, `hook: script`, when it has one. */
  hook?: string;
  at: number;
  dur: number;
  note?: string;
};

type Row = {
  id: string;
  space: string;
  spaceColor: string;
  y: number;
  steps: Step[];
};

// One strip per package: the same five stages, each package's own hooks.
const ROWS: Row[] = [
  {
    id: 'core',
    space: 'libs',
    spaceColor: colors.cyan,
    y: 392,
    steps: [
      {name: 'version', at: 96, dur: 30},
      {name: 'build', hook: 'beforeBuild: lint', at: 132, dur: 60},
      {name: 'login', at: 198, dur: 40, note: 'once per space'},
      {name: 'publish', hook: 'beforePublish: print-env', at: 258, dur: 44},
      {name: 'announce', at: 308, dur: 26},
    ],
  },
  {
    id: 'utils',
    space: 'libs',
    spaceColor: colors.cyan,
    y: 562,
    steps: [
      {name: 'version', at: 100, dur: 30},
      {name: 'build', at: 136, dur: 66},
      {name: 'login', at: 238, dur: 6, note: 'shared: libs already in'},
      {name: 'publish', hook: 'beforePublish: verify-sbom', at: 262, dur: 46},
      {name: 'announce', at: 314, dur: 24},
    ],
  },
  {
    id: 'api',
    space: 'services',
    spaceColor: colors.blue,
    y: 732,
    steps: [
      {name: 'version', hook: 'beforeVersion: check-migrations', at: 104, dur: 30},
      {name: 'build', at: 140, dur: 64},
      {name: 'login', at: 210, dur: 38, note: 'once per space'},
      {name: 'publish', at: 256, dur: 48},
      {name: 'announce', hook: 'announce: notify-slack', at: 310, dur: 26},
    ],
  },
];

const dim = (t: string) => ({text: t, color: colors.dim});

const rows: TermRow[] = [
  cmdRow(8, 'cat dispat.yaml'),
  outRow(26, [{text: 'packages.core.flow:     ', color: colors.cyan}, {text: '{ beforeBuild: lint, beforePublish: print-env }'}]),
  outRow(30, [{text: 'packages.utils.flow:    ', color: colors.cyan}, {text: '{ beforePublish: verify-sbom }'}]),
  outRow(34, [{text: 'packages.api.flow:      ', color: colors.cyan}, {text: '{ beforeVersion: check-migrations, announce: notify-slack }'}]),
  cmdRow(58, 'dispat'),
  outRow(106, [...INF, msg('hook'), ...kv('package', 'api'), ...kv('stage', 'beforeVersion'), ...kv('script', 'check-migrations')]),
  outRow(134, [...INF, msg('hook'), ...kv('package', 'core'), ...kv('stage', 'beforeBuild'), ...kv('script', 'lint')]),
  outRow(202, [...INF, msg('login'), ...kv('space', 'libs'), ...kv('stage', 'login')]),
  outRow(214, [...INF, msg('login'), ...kv('space', 'services'), ...kv('stage', 'login')]),
  outRow(246, [dim('print-env  '), {text: 'DISPAT_STAGE=beforePublish DISPAT_PACKAGE=core DISPAT_SPACE=libs', color: colors.cyan}]),
  outRow(254, [dim('print-env  '), {text: 'DISPAT_OLD_VERSION=1.4.2 DISPAT_NEW_VERSION=1.5.0 DISPAT_TAG=core@1.5.0', color: colors.cyan}]),
  outRow(306, [...INF, msg('published'), ...kv('package', 'core'), ...kv('tag', 'core@1.5.0')]),
  outRow(314, [...INF, msg('published'), ...kv('package', 'utils'), ...kv('tag', 'utils@2.0.4')]),
  outRow(320, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@0.8.3')]),
  outRow(342, [...INF, msg('announce'), ...kv('package', 'api'), ...kv('script', 'notify-slack')]),
];

export const HOOKS_DURATION = 410;

const BLOCK_X = [470, 736, 1002, 1268, 1534];
const BLOCK_W = 246;

export const Hooks: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [HOOKS_DURATION - 10, HOOKS_DURATION - 2], [1, 0], {
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
          <div style={{position: 'absolute', left: 140, top: row.y + 10, width: 310}}>
            <span style={{fontSize: 30, fontWeight: 700, color: colors.fg}}>{row.id}</span>
            <span
              style={{
                marginLeft: 14,
                fontSize: 17,
                color: row.spaceColor,
                border: `1.5px solid ${row.spaceColor}66`,
                borderRadius: 999,
                padding: '1px 12px',
              }}>
              {row.space}
            </span>
          </div>
          {row.steps.map((step, i) => {
            const lit = f >= step.at;
            const active = lit && f < step.at + step.dur;
            const c = active ? colors.cyan : lit ? colors.green : colors.faint;
            return (
              <div key={step.name} style={{position: 'absolute', left: BLOCK_X[i], top: row.y - 26, width: BLOCK_W}}>
                <div
                  style={{
                    height: 22,
                    fontSize: 15,
                    color: colors.dim,
                    textAlign: 'center',
                    whiteSpace: 'nowrap',
                    opacity: step.hook && f >= step.at - 12 ? 1 : 0,
                  }}>
                  {step.hook ? `${step.hook} ✓` : ''}
                </div>
                <div
                  style={{
                    borderRadius: 12,
                    border: `2px solid ${c}`,
                    background: colors.panel,
                    boxShadow: active ? `0 0 24px ${c}33` : 'none',
                    padding: '13px 0 11px',
                    textAlign: 'center',
                    fontSize: 22,
                    fontWeight: 700,
                    color: lit ? colors.fg : colors.dim,
                  }}>
                  {step.name}
                  <div style={{marginTop: 2, fontSize: 14, fontWeight: 400, color: colors.dim, minHeight: 18}}>
                    {step.note && f >= step.at - 12 ? step.note : ''}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      ))}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
