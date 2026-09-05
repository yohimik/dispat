import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {colors, font} from './theme';
import {SceneTerminal, TermRow, cmdRow, outRow, fadeIO, INF, msg, kv} from './components';
import {Edit} from './Polyglot';

// The glue commands, twenty-eight seconds at Root.tsx's twenty frames per
// second, in three acts. First `dispat if`: a stage branches on a condition
// without depending on its shell, ENV=prod holds, and the --then script is
// the one that runs. Then `dispat replacer`: literal text swapped in the
// files no manifest parser covers, the docs' own examples, a Gradle
// coordinate and a README install line, with everything else untouched.
// Last the local-link bracket: `autowriter --link-local` writes the go.mod
// `replace` pointing a workspace dependency at its folder, the tests run
// against the working tree, `--unlink-local` takes the redirect back out,
// and `scanner --verify-unlinked` proves the bracket closed.
//
// No title: the landing page crops the clip's empty top strip away and
// shows the feature text under the clip.

const bar = (frame: number, a: number, b: number) =>
  interpolate(frame, [a, b], [0, 1], {extrapolateLeft: 'clamp', extrapolateRight: 'clamp'});

const rows: TermRow[] = [
  cmdRow(10, "dispat if 'ENV=prod' --then 'echo deploying to prod' --else 'echo deploying to stage'"),
  outRow(148, [{text: 'deploying to prod', color: colors.green, weight: 700}]),
  cmdRow(180, "dispat replacer --replace 'com.acme:core:1.4.2=>com.acme:core:1.5.0' --replace '@acme/core@1.4.2=>@acme/core@1.5.0' build.gradle README.md"),
  outRow(402, [{text: 'build.gradle  applied  com.acme:core:1.4.2 -> com.acme:core:1.5.0', color: colors.green}]),
  outRow(430, [{text: 'README.md     applied  @acme/core@1.4.2 -> @acme/core@1.5.0', color: colors.green}]),
  cmdRow(466, 'dispat autowriter --link-local --since all --sync-lock=false'),
  outRow(568, [{text: 'packages/api/go.mod  applied  link  github.com/acme/core  ../core', color: colors.green}]),
  cmdRow(586, 'dispat tests'),
  outRow(620, [...INF, msg('run finished'), ...kv('failed', '0'), ...kv('ran', '2'), ...kv('script', 'tests')]),
  cmdRow(646, 'dispat autowriter --unlink-local --since all --sync-lock=false'),
  outRow(750, [{text: 'packages/api/go.mod  applied  link  github.com/acme/core  (removed)', color: colors.green}]),
  cmdRow(768, 'dispat scanner --verify-unlinked'),
  outRow(834, [{text: '3 manifest(s), 1 dependency declaration(s)', color: colors.dim}]),
];

/** The two files the replacer touches, drawn like the polyglot editor. */
const FILES: Array<{
  file: string;
  start: number;
  lines: Array<Array<{t?: string; c?: string; edit?: [string, string]}>>;
}> = [
  {
    file: 'build.gradle',
    start: 386,
    lines: [
      [{t: 'dependencies {', c: colors.dim}],
      [{t: '  implementation ', c: colors.cyan}, {t: '"com.acme:core:', c: colors.fg}, {edit: ['1.4.2', '1.5.0']}, {t: '"', c: colors.fg}],
      [{t: '}', c: colors.dim}],
    ],
  },
  {
    file: 'README.md',
    start: 418,
    lines: [
      [{t: '## Install', c: colors.dim}],
      [{t: 'npm install ', c: colors.cyan}, {t: '@acme/core@', c: colors.fg}, {edit: ['1.4.2', '1.5.0']}],
    ],
  },
];

export const GLUE_DURATION = 900;

export const Glue: React.FC = () => {
  const f = useCurrentFrame();
  const opacity =
    bar(f, 0, 10) *
    interpolate(f, [GLUE_DURATION - 10, GLUE_DURATION - 2], [1, 0], {
      extrapolateLeft: 'clamp',
      extrapolateRight: 'clamp',
    });
  // Act one holds the stage until the replacer command starts typing.
  const actOne = fadeIO(f, 16, 26, 166, 180);
  const chosen = f >= 140;

  return (
    <AbsoluteFill style={{background: colors.bg, fontFamily: font}}>
      {/* The fade dims the content onto the canvas, never the canvas itself. */}
      <div style={{position: 'absolute', inset: 0, opacity}}>
      <div style={{position: 'absolute', left: 48, top: 40, fontSize: 28, fontWeight: 700, color: colors.dim}}>dispat</div>
      {/* Act one: the branch. */}
      {actOne > 0 && (
        <div style={{opacity: actOne}}>
          <div
            style={{
              position: 'absolute',
              left: 960,
              top: 360,
              transform: 'translateX(-50%)',
              fontSize: 24,
              color: colors.cyan,
              background: colors.panel,
              border: `1.5px solid ${colors.panelEdge}`,
              borderRadius: 999,
              padding: '8px 26px',
            }}>
            ENV=prod
          </div>
          <svg width="1920" height="1080" style={{position: 'absolute', inset: 0}} viewBox="0 0 1920 1080">
            <path d="M 940 428 C 850 490, 780 510, 730 545" stroke={chosen ? colors.green : colors.faint} strokeWidth={chosen ? 4 : 3} fill="none" />
            <path d="M 980 428 C 1070 490, 1140 510, 1190 545" stroke={colors.faint} strokeWidth={3} fill="none" />
          </svg>
          {[
            {text: "--then 'deploy prod'", x: 640, on: chosen},
            {text: "--else 'deploy stage'", x: 1280, on: false},
          ].map((branch) => (
            <div
              key={branch.text}
              style={{
                position: 'absolute',
                left: branch.x,
                top: 560,
                transform: 'translateX(-50%)',
                fontSize: 26,
                fontWeight: branch.on ? 700 : 400,
                color: branch.on ? colors.green : colors.dim,
                background: colors.panel,
                border: `2px solid ${branch.on ? colors.green : colors.panelEdge}`,
                borderRadius: 14,
                padding: '14px 30px',
                opacity: !chosen || branch.on ? 1 : 0.55,
              }}>
              {branch.text}
            </div>
          ))}
        </div>
      )}
      {/* Act two: literal text, swapped in place. */}
      {FILES.map((panel) => {
        const b = f - panel.start;
        const panelOpacity = fadeIO(f, panel.start, panel.start + 6, 450, 460);
        if (panelOpacity <= 0) return null;
        return (
          <div
            key={panel.file}
            style={{
              position: 'absolute',
              left: panel.file === 'build.gradle' ? 340 : 990,
              width: 590,
              top: 430,
              borderRadius: 16,
              background: colors.panel,
              border: `1.5px solid ${colors.panelEdge}`,
              overflow: 'hidden',
              opacity: panelOpacity,
            }}>
            <div style={{padding: '14px 26px', borderBottom: `1.5px solid ${colors.panelEdge}`, fontSize: 24, fontWeight: 700, color: colors.fg}}>
              {panel.file}
            </div>
            <div style={{padding: '20px 28px 24px', fontSize: 24, lineHeight: '40px', whiteSpace: 'pre'}}>
              {panel.lines.map((line, li) => (
                <div key={li} style={{height: 40}}>
                  {line.map((tok, ti) =>
                    tok.edit ? (
                      <Edit key={ti} before={tok.edit[0]} after={tok.edit[1]} b={b} order={0} />
                    ) : (
                      <span key={ti} style={{color: tok.c ?? colors.fg}}>
                        {tok.t}
                      </span>
                    ),
                  )}
                </div>
              ))}
            </div>
          </div>
        );
      })}
      {/* Act three: the local-link bracket, opened and closed. */}
      {(() => {
        const panelOpacity = fadeIO(f, 460, 468, GLUE_DURATION - 18, GLUE_DURATION - 10);
        if (panelOpacity <= 0) return null;
        // The replace directive exists exactly between the link and the
        // unlink, which is the bracket's whole point.
        const link = fadeIO(f, 562, 568, 744, 750);
        return (
          <div
            style={{
              position: 'absolute',
              left: 960,
              top: 430,
              transform: 'translateX(-50%)',
              width: 860,
              borderRadius: 16,
              background: colors.panel,
              border: `1.5px solid ${colors.panelEdge}`,
              overflow: 'hidden',
              opacity: panelOpacity,
            }}>
            <div style={{padding: '14px 26px', borderBottom: `1.5px solid ${colors.panelEdge}`, fontSize: 24, color: colors.fg}}>
              <span style={{fontWeight: 700}}>go.mod</span>
              <span style={{color: colors.dim}}>  packages/api</span>
            </div>
            <div style={{padding: '20px 28px 24px', fontSize: 24, lineHeight: '40px', whiteSpace: 'pre'}}>
              <div style={{height: 40}}>
                <span style={{color: colors.cyan}}>module</span>
                <span style={{color: colors.fg}}> github.com/acme/api</span>
              </div>
              <div style={{height: 40}}>
                <span style={{color: colors.cyan}}>require</span>
                <span style={{color: colors.fg}}> github.com/acme/core v1.5.0</span>
              </div>
              <div style={{height: 40, opacity: link}}>
                <span style={{color: colors.cyan}}>replace</span>
                <span style={{color: colors.green, fontWeight: 700}}> github.com/acme/core =&gt; ../core</span>
              </div>
            </div>
          </div>
        );
      })()}
      <SceneTerminal rows={rows} f={f} />
      </div>
    </AbsoluteFill>
  );
};
