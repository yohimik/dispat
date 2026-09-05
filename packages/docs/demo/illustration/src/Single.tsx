import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {alpha, colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';
import {useDemoLayout} from './layout';

// The single-package claim, nineteen seconds at Root.tsx's twenty frames per
// second: no monorepo required. One standalone `packages` entry pointing at
// a folder is the whole setup, and the session is the documentation's own:
// a scoped commit, the status line it produces, and a release that leaves a
// changelog, a tag, and a GitHub release behind. The records appear under
// the card as the run writes them.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const rows: TermRow[] = [
  cmdRow(8, 'git commit -m "feat(app): first version"'),
  cmdRow(88, 'dispat status'),
  outRow(115, [...INF, msg('● changed'), ...kv('bump', 'minor'), ...kv('ownCommits', '1'), ...kv('package', 'app'), ...kv('reason', 'direct'), ...kv('version', '"0.0.0 -> 0.1.0"')]),
  outRow(127, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '1'), ...kv('releasing', '1')]),
  cmdRow(150, 'dispat'),
  outRow(170, [...INF, msg('build started'), ...kv('package', 'app'), ...kv('stage', 'build'), ...kv('version', '0.1.0')]),
  outRow(212, [...INF, msg('published'), ...kv('package', 'app'), ...kv('tag', 'app@0.1.0'), ...kv('version', '0.1.0')]),
  outRow(236, [...INF, msg('changelog written'), ...kv('package', 'app'), ...kv('file', 'CHANGELOG.md')]),
  outRow(258, [...INF, msg('github release created'), ...kv('tag', 'app@0.1.0')]),
  outRow(276, [...INF, msg('done'), ...kv('published', '1'), ...kv('unchanged', '0')]),
];

/** What the card is doing, keyed to the session above. */
function state(f: number): {label: string; color: string; progress?: number} {
  if (f < 70) return {label: '', color: colors.faint};
  if (f < 115) return {label: '● changed', color: colors.green};
  if (f < 170) return {label: '● changed', color: colors.green};
  if (f < 208) return {label: 'build', color: colors.cyan, progress: bar(f, 170, 206)};
  return {label: '✓ published', color: colors.green};
}

const RECORDS: Array<{at: number; text: string}> = [
  {at: 214, text: 'app@0.1.0'},
  {at: 240, text: 'CHANGELOG.md'},
  {at: 262, text: 'GitHub release'},
];

export const SINGLE_DURATION = 380;

export const Single: React.FC = () => {
  const f = useCurrentFrame();
  const mobile = useDemoLayout();
  const s = state(f);
  const changed = f >= 70;
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [SINGLE_DURATION - 10, SINGLE_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {/* The whole configuration: one standalone entry. */}
      <div
        style={{
          position: 'absolute',
          left: mobile ? 30 : 960,
          right: mobile ? 30 : undefined,
          top: mobile ? 160 : 330,
          transform: mobile ? undefined : 'translateX(-50%)',
          fontSize: mobile ? 27 : 24,
          lineHeight: '38px',
          whiteSpace: 'pre',
          color: colors.dim,
          background: colors.panel,
          border: `1.5px solid ${colors.panelEdge}`,
          borderRadius: 12,
          padding: '16px 30px',
        }}>
        <span style={{color: colors.cyan}}>packages</span>
        {': { '}
        <span style={{color: colors.cyan}}>app</span>
        {': { '}
        <span style={{color: colors.cyan}}>path</span>
        {': '}
        <span style={{color: colors.fg}}>&quot;src&quot;</span>
        {' } }'}
      </div>
      {/* The one package. */}
      <div
        style={{
          position: 'absolute',
          left: mobile ? 30 : 960,
          right: mobile ? 30 : undefined,
          top: mobile ? 390 : 480,
          transform: mobile ? undefined : 'translateX(-50%)',
          width: mobile ? 'auto' : 640,
          borderRadius: 18,
          background: colors.panel,
          border: `2px solid ${changed ? s.color : colors.panelEdge}`,
          boxShadow: changed ? `0 0 34px ${alpha(s.color, 0.2)}` : 'none',
          padding: '26px 36px 30px',
        }}>
        <div style={{display: 'flex', alignItems: 'center', gap: 16}}>
          <span style={{fontSize: 34, fontWeight: 700, color: colors.fg}}>app</span>
          <span style={{fontSize: 19, color: colors.red, border: `1.5px solid ${alpha(colors.red, 0.4)}`, borderRadius: 999, padding: '1px 12px'}}>
            npm
          </span>
          <span style={{marginLeft: 'auto', fontSize: mobile ? 26 : 23, fontWeight: 700, color: s.color}}>{s.label}</span>
        </div>
        <div style={{marginTop: 12, fontSize: 21, color: colors.dim}}>src/</div>
        <div style={{marginTop: 10, fontSize: 30}}>
          <span style={{color: changed ? colors.dim : colors.fg}}>0.0.0</span>
          {f > 115 && (
            <>
              <span style={{color: colors.dim}}>{' -> '}</span>
              <span style={{color: colors.green, fontWeight: 700}}>0.1.0</span>
            </>
          )}
        </div>
        {s.progress !== undefined && (
          <div style={{marginTop: 16, height: 6, borderRadius: 3, background: colors.panelEdge}}>
            <div style={{width: `${s.progress * 100}%`, height: '100%', borderRadius: 3, background: s.color}} />
          </div>
        )}
      </div>
      {/* The records the release leaves behind. */}
      <div style={{position: 'absolute', left: mobile ? 30 : 0, right: mobile ? 30 : 0, top: mobile ? 710 : 726, display: 'flex', flexWrap: 'wrap', justifyContent: 'center', gap: 22}}>
        {RECORDS.map((r) => (
          <span
            key={r.text}
            style={{
              fontSize: mobile ? 26 : 21,
              color: colors.green,
              background: colors.bg,
              border: `1.5px solid ${alpha(colors.green, 0.4)}`,
              borderRadius: 10,
              padding: '5px 18px',
              opacity: bar(f, r.at, r.at + 6),
              display: 'inline-block',
              transform: `translateY(${(1 - bar(f, r.at, r.at + 6)) * 10}px)`,
            }}>
            {r.text}
          </span>
        ))}
      </div>
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
