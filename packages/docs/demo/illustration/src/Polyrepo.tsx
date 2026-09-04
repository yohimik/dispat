import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, INF, msg, kv} from './components';

// The control-repository pattern, twenty-two seconds at Root.tsx's twenty
// frames per second: many repositories, one release. A small repository
// holds the dispat configuration and a git submodule per linked repository,
// which gives dispat the single checkout its graph needs. Moving a pointer
// is an ordinary commit, so the sync lands, sdk's card answers, and the
// fleet releases in dependency order while the linked repositories stay
// untouched.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

// One card per linked repository inside the control repository's frame.
type Repo = {
  id: string;
  remote: string;
  x: number;
  base: string;
  next: string;
};

const REPOS: Repo[] = [
  {id: 'sdk', remote: 'github.com/acme/sdk', x: 520, base: '2.3.0', next: '2.4.0'},
  {id: 'api', remote: 'github.com/acme/api', x: 960, base: '1.1.2', next: '1.1.3'},
  {id: 'web', remote: 'github.com/acme/web', x: 1400, base: '5.0.1', next: '5.0.1'},
];

const CARD_W = 380;
const CARD_Y = 500;

function view(id: string, f: number): {label: string; color: string; bumped: boolean; progress?: number; tag?: string} {
  const changed = {sdk: 150, api: 162}[id as 'sdk' | 'api'];
  if (id === 'web') {
    return f < 170 ? {label: '', color: colors.faint, bumped: false} : {label: 'unchanged', color: colors.dim, bumped: false};
  }
  if (changed === undefined || f < changed) return {label: '', color: colors.faint, bumped: false};
  const run = id === 'sdk' ? {b: 208, p: 268, done: 300, tag: 'sdk@2.4.0'} : {b: 306, p: 356, done: 386, tag: 'api@1.1.3'};
  if (f < 200 || f < run.b) return {label: '● changed', color: colors.green, bumped: f > changed + 10};
  if (f < run.p) return {label: 'build', color: colors.cyan, bumped: true, progress: bar(f, run.b, run.p - 2)};
  if (f < run.done) return {label: 'publish', color: colors.cyan, bumped: true, progress: bar(f, run.p, run.done - 2)};
  return {label: '✓ published', color: colors.green, bumped: true, tag: run.tag};
}

const rows: TermRow[] = [
  cmdRow(8, 'git -C libs/sdk/src fetch origin && git -C libs/sdk/src checkout origin/main', 0.6),
  outRow(52, [{text: 'libs/sdk/src: ', color: colors.dim}, {text: '9f3c2a1', color: colors.yellow}, {text: ' -> ', color: colors.dim}, {text: 'b82d47e', color: colors.yellow}]),
  cmdRow(72, 'git commit -am "feat(sdk)^: new tokenizer"', 0.7),
  cmdRow(118, 'dispat status'),
  outRow(152, [...INF, msg('● changed'), ...kv('package', 'sdk'), ...kv('reason', 'direct'), ...kv('version', '"2.3.0 -> 2.4.0"')]),
  outRow(164, [...INF, msg('● changed'), ...kv('dueToProviders', '["sdk"]'), ...kv('package', 'api'), ...kv('version', '"1.1.2 -> 1.1.3"')]),
  outRow(170, [{text: '  unchanged ', color: colors.dim}, ...kv('package', 'web'), ...kv('version', '5.0.1')]),
  outRow(178, [...INF, msg('release plan ready'), ...kv('held', '0'), ...kv('packages', '3'), ...kv('releasing', '2')]),
  cmdRow(196, 'dispat'),
  outRow(302, [...INF, msg('published'), ...kv('package', 'sdk'), ...kv('tag', 'sdk@2.4.0'), ...kv('version', '2.4.0')]),
  outRow(388, [...INF, msg('published'), ...kv('package', 'api'), ...kv('tag', 'api@1.1.3'), ...kv('version', '1.1.3')]),
  outRow(396, [...INF, msg('done'), ...kv('published', '2'), ...kv('unchanged', '1')]),
];

export const POLYREPO_DURATION = 452;

export const Polyrepo: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [POLYREPO_DURATION - 10, POLYREPO_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  // The sdk pointer flips as the sync's line prints.
  const synced = f >= 54;
  const edgeHot = (at: number) => bar(f, at, at + 8);

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {/* The control repository: no product code, one graph. */}
      <div
        style={{
          position: 'absolute',
          left: 250,
          right: 250,
          top: 360,
          height: 420,
          border: `2px dashed ${colors.faint}`,
          borderRadius: 22,
        }}>
        <span style={{position: 'absolute', left: 30, top: -16, background: colors.bg, padding: '0 14px', fontSize: 21, color: colors.dim}}>
          platform: the control repository (dispat.yaml lives here)
        </span>
      </div>
      <svg width="1920" height="1080" style={{position: 'absolute', inset: 0}} viewBox="0 0 1920 1080">
        <path
          d={`M ${520 + CARD_W / 2} ${CARD_Y} L ${960 - CARD_W / 2} ${CARD_Y}`}
          stroke={f >= 300 ? colors.green : colors.faint}
          strokeWidth={3 + edgeHot(300) * 2}
          fill="none"
        />
        <path
          d={`M ${960 + CARD_W / 2} ${CARD_Y} L ${1400 - CARD_W / 2} ${CARD_Y}`}
          stroke={colors.faint}
          strokeWidth={3}
          fill="none"
        />
      </svg>
      {REPOS.map((repo) => {
        const v = view(repo.id, f);
        const active = v.label !== '';
        return (
          <div
            key={repo.id}
            style={{
              position: 'absolute',
              left: repo.x - CARD_W / 2,
              top: CARD_Y - 105,
              width: CARD_W,
              height: 210,
              borderRadius: 16,
              background: colors.panel,
              border: `2px solid ${active ? v.color : colors.panelEdge}`,
              boxShadow: active && v.color !== colors.dim ? `0 0 30px ${v.color}33` : 'none',
              padding: '18px 22px',
              boxSizing: 'border-box',
            }}>
            <div style={{display: 'flex', alignItems: 'center', gap: 12}}>
              <span style={{fontSize: 28, fontWeight: 700, color: colors.fg}}>{repo.id}</span>
              <span style={{marginLeft: 'auto', fontSize: 19, fontWeight: 700, color: v.color, whiteSpace: 'nowrap'}}>{v.label}</span>
            </div>
            <div style={{marginTop: 8, fontSize: 16, color: colors.dim}}>
              src ⟶ {repo.remote}
              {'\n'}
            </div>
            <div style={{marginTop: 4, fontSize: 16, color: colors.dim}}>
              submodule @{' '}
              <span style={{color: colors.yellow}}>{repo.id === 'sdk' && synced ? 'b82d47e' : '9f3c2a1'}</span>
            </div>
            <div style={{marginTop: 8, fontSize: 20}}>
              <span style={{color: v.bumped ? colors.dim : colors.fg}}>{repo.base}</span>
              {v.bumped && (
                <>
                  <span style={{color: colors.dim}}>{' -> '}</span>
                  <span style={{color: colors.green, fontWeight: 700}}>{repo.next}</span>
                </>
              )}
            </div>
            {v.progress !== undefined && (
              <div style={{position: 'absolute', left: 22, right: 22, bottom: 16, height: 5, borderRadius: 3, background: colors.panelEdge}}>
                <div style={{width: `${v.progress * 100}%`, height: '100%', borderRadius: 3, background: v.color}} />
              </div>
            )}
            {v.tag && (
              <div
                style={{
                  position: 'absolute',
                  left: 22,
                  bottom: -21,
                  fontSize: 17,
                  color: colors.green,
                  background: colors.bg,
                  border: `1.5px solid ${colors.green}66`,
                  borderRadius: 8,
                  padding: '2px 12px',
                }}>
                {v.tag}
              </div>
            )}
          </div>
        );
      })}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
