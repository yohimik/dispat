import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {alpha, colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, fadeIO, INF, msg, kv, CapSeg} from './components';
import {useDemoLayout} from './layout';

// The polyglot claim, shown rather than listed: one manifest after another
// opens in the same editor panel and the version write happens in place. The
// Each writer changes the requested value while preserving the manifest's
// supported structure and formatting. Some formats, including go.mod, may
// also run their standard formatter. The plist beat makes the complementary
// point: the build number beside the marketing version is read but unchanged.
//
// Twenty frames per second, like every composition in Root.tsx. No title:
// the landing page overlays the feature's own text over the top strip, so
// the panel sits low and the canvas keeps the strip clear.

/** One run of manifest text, or the value the write replaces. */
type Tok = {t: string; c?: string; w?: number} | {edit: [before: string, after: string]};

type Beat = {
  file: string;
  example: string;
  badge: string;
  badgeColor: string;
  lines: Tok[][];
  caption: CapSeg[];
};

const d = (t: string): Tok => ({t, c: colors.dim});
const k = (t: string): Tok => ({t, c: colors.cyan});
const p = (t: string): Tok => ({t});

const BEATS: Beat[] = [
  {
    file: 'package.json',
    example: 'web workspace',
    badge: 'npm',
    badgeColor: colors.red,
    caption: [...INF, msg('version written'), ...kv('file', 'package.json'), ...kv('applied', '2')],
    lines: [
      [d('{')],
      [d('  '), k('"name"'), d(': '), p('"@acme/core"'), d(',')],
      [d('  '), k('"version"'), d(': '), {edit: ['"1.4.2"', '"1.5.0"']}, d(',')],
      [d('  '), k('"dependencies"'), d(': {')],
      [d('    '), k('"@acme/utils"'), d(': '), {edit: ['"^2.0.3"', '"^2.0.4"']}],
      [d('  }')],
      [d('}')],
    ],
  },
  {
    file: 'go.mod',
    example: 'native service',
    badge: 'go',
    badgeColor: colors.cyan,
    caption: [...INF, msg('version written'), ...kv('file', 'go.mod'), ...kv('applied', '1')],
    lines: [
      [k('module'), p(' github.com/acme/api')],
      [],
      [k('go'), p(' 1.24')],
      [],
      [k('require'), p(' github.com/acme/core '), {edit: ['v1.4.2', 'v1.5.0']}],
    ],
  },
  {
    file: 'Cargo.toml',
    example: 'game server',
    badge: 'cargo',
    badgeColor: colors.yellow,
    caption: [...INF, msg('version written'), ...kv('file', 'Cargo.toml'), ...kv('applied', '1')],
    lines: [
      [d('[package]')],
      [k('name'), d(' = '), p('"acme-core"')],
      [k('version'), d(' = '), {edit: ['"1.4.2"', '"1.5.0"']}],
      [k('edition'), d(' = '), p('"2024"')],
    ],
  },
  {
    file: 'pom.xml',
    example: 'Android backend',
    badge: 'maven',
    badgeColor: colors.blue,
    caption: [...INF, msg('version written'), ...kv('file', 'pom.xml'), ...kv('applied', '1')],
    lines: [
      [d('<project>')],
      [d('  <'), k('groupId'), d('>'), p('com.acme'), d('</'), k('groupId'), d('>')],
      [d('  <'), k('artifactId'), d('>'), p('core'), d('</'), k('artifactId'), d('>')],
      [d('  <'), k('version'), d('>'), {edit: ['1.4.2', '1.5.0']}, d('</'), k('version'), d('>')],
      [d('</project>')],
    ],
  },
  {
    file: 'pubspec.yaml',
    example: 'cross-platform game',
    badge: 'pub',
    badgeColor: colors.cyan,
    caption: [...INF, msg('version written'), ...kv('file', 'pubspec.yaml'), ...kv('applied', '1')],
    lines: [
      [k('name'), d(': '), p('acme_core')],
      [k('version'), d(': '), {edit: ['1.4.2', '1.5.0']}],
      [],
      [k('environment'), d(':')],
      [d('  '), k('sdk'), d(': '), p('^3.5.0')],
    ],
  },
  {
    file: 'Info.plist',
    example: 'native iOS app',
    badge: 'ios',
    badgeColor: colors.fg,
    caption: [...INF, msg('version written'), ...kv('file', 'Info.plist'), ...kv('kept', 'CFBundleVersion=87')],
    lines: [
      [d('<'), k('key'), d('>'), p('CFBundleShortVersionString'), d('</'), k('key'), d('>')],
      [d('<'), k('string'), d('>'), {edit: ['1.4.2', '1.5.0']}, d('</'), k('string'), d('>')],
      [d('<'), k('key'), d('>'), p('CFBundleVersion'), d('</'), k('key'), d('>')],
      [d('<'), k('string'), d('>'), p('87'), d('</'), k('string'), d('>'), d('   ⟵ the build number never moves')],
    ],
  },
  {
    file: 'Dockerfile',
    example: 'web deployment',
    badge: 'docker',
    badgeColor: colors.blue,
    caption: [...INF, msg('provider pinned'), ...kv('file', 'Dockerfile'), ...kv('applied', '1')],
    lines: [
      [k('FROM'), p(' acme/core:'), {edit: ['1.4.2', '1.5.0']}],
      [],
      [k('COPY'), p(' . /app')],
      [k('RUN'), p(' make build')],
    ],
  },
];

const INTRO = 14;
const BEAT = 58;
export const POLYGLOT_DURATION = INTRO + BEATS.length * BEAT + 20;

// The scene's terminal: the release command once, then one `version written`
// line per manifest, printed the moment the value in the panel has swapped.
const rows: TermRow[] = [
  cmdRow(2, 'dispat'),
  ...BEATS.map((beat, i) => outRow(INTRO + i * BEAT + 38, beat.caption)),
];

/** The changing value: held, then swapped in place while the file sits still. */
export const Edit: React.FC<{before: string; after: string; b: number; order: number}> = ({before, after, b, order}) => {
  // Staggered when a beat carries two writes: the version first, the
  // dependency range one breath later.
  const swap = 26 + order * 9;
  const gone = b >= swap;
  const glow = fadeIO(b, 12 + order * 9, 16 + order * 9, swap - 2, swap);
  const pop = interpolate(b, [swap, swap + 5], [1.25, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  const inOp = interpolate(b, [swap, swap + 4], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});
  return gone ? (
    <span style={{color: colors.green, fontWeight: 700, opacity: inOp, display: 'inline-block', transform: `scale(${pop})`}}>
      {after}
    </span>
  ) : (
    <span
      style={{
        color: colors.fg,
        background: alpha(colors.yellow, 0.22 * glow),
        borderRadius: 6,
        boxShadow: glow > 0 ? `0 0 0 2px ${alpha(colors.yellow, 0.5 * glow)}` : undefined,
      }}>
      {before}
    </span>
  );
};

export const Polyglot: React.FC = () => {
  const f = useCurrentFrame();
  const mobile = useDemoLayout();
  const opacity =
    interpolate(f, [0, 10], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'}) *
    interpolate(f, [POLYGLOT_DURATION - 10, POLYGLOT_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      <SceneTerminal rows={rows} f={f} />
      {BEATS.map((beat, i) => {
        const start = INTRO + i * BEAT;
        const b = f - start;
        const opacity = fadeIO(f, start, start + 6, start + BEAT - 6, start + BEAT);
        if (opacity <= 0) return null;
        let edits = 0;
        return (
          <div key={beat.file} style={{opacity}}>
            <div
              style={{
                position: 'absolute',
                left: mobile ? 28 : 340,
                right: mobile ? 28 : 340,
                top: mobile ? 245 : 420,
                borderRadius: 16,
                background: colors.panel,
                border: `1.5px solid ${colors.panelEdge}`,
                overflow: 'hidden',
              }}>
              <div
                style={{
                  display: 'flex',
                  flexWrap: mobile ? 'wrap' : 'nowrap',
                  alignItems: 'center',
                  gap: 16,
                  padding: '16px 28px',
                  borderBottom: `1.5px solid ${colors.panelEdge}`,
                  fontSize: 26,
                }}>
                <span style={{color: colors.fg, fontWeight: 700}}>{beat.file}</span>
                <span
                  style={{
                    fontSize: mobile ? 26 : 19,
                    color: beat.badgeColor,
                    border: `1.5px solid ${beat.badgeColor}`,
                    borderRadius: 999,
                    padding: '1px 12px',
                    opacity: 0.9,
                  }}>
                  {beat.badge}
                </span>
                <span style={{marginLeft: 'auto', fontSize: mobile ? 26 : 19, color: colors.dim}}>{beat.example}</span>
              </div>
              <div style={{padding: '24px 34px 28px', fontSize: 26, lineHeight: '42px', whiteSpace: mobile ? 'pre-wrap' : 'pre', overflowWrap: 'anywhere'}}>
                {beat.lines.map((line, li) => (
                  <div key={li} style={{minHeight: 42, height: mobile ? undefined : 42}}>
                    {line.map((tok, ti) =>
                      'edit' in tok ? (
                        <Edit key={ti} before={tok.edit[0]} after={tok.edit[1]} b={b} order={edits++} />
                      ) : (
                        <span key={ti} style={{color: tok.c ?? colors.fg, fontWeight: tok.w ?? 400}}>
                          {tok.t}
                        </span>
                      ),
                    )}
                  </div>
                ))}
              </div>
            </div>
          </div>
        );
      })}
      </div>
    </AbsoluteFill>
  );
};
