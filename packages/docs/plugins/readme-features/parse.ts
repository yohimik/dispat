import {parseInline} from './inline';
import type {Feature, ReadmeData} from './types';

// Reading the landing page's hero and feature cards out of the CLI README.
//
// They used to be a second copy, typed into src/pages/index.tsx under a
// comment saying it "carries the same claims as the repository README" — which
// is a promise a comment cannot keep. The README is the copy a reader arrives
// at from GitHub, `go install` or a search result, so it is the one that has to
// be right, and this makes it the only one.
//
// Every extraction here is strict: a heading that moved, a bullet that stopped
// opening with its own title, a link that stopped resolving. The site build is
// already the link checker, and this makes it the README checker too.

/** Returns the body of a `## <heading>` section, up to the next `## `. */
function section(src: string, heading: string): string {
  const start = src.indexOf(`\n## ${heading}\n`);
  if (start < 0) {
    throw new Error(`services/dispat/README.md: no "## ${heading}" section`);
  }
  const from = start + `\n## ${heading}\n`.length;
  const end = src.indexOf('\n## ', from);
  return end < 0 ? src.slice(from) : src.slice(from, end);
}

/**
 * The first fenced code block of a section, and where it ended.
 *
 * The terminal tour is the first block under `## In the terminal`; the "a few
 * more moves" block below it is a reference list rather than a walkthrough and
 * belongs on the CLI page, not in the hero.
 */
function firstFence(body: string, heading: string): [code: string, after: number] {
  const fence = /^```[a-z]*\n([\s\S]*?)\n```$/m.exec(body);
  if (!fence) {
    throw new Error(`services/dispat/README.md: no fenced block under "## ${heading}"`);
  }
  return [fence[1], fence.index + fence[0].length];
}

/** The first paragraph at or after `from`, as one line. */
function paragraph(body: string, from: number, heading: string): string {
  const rest = body.slice(from).replace(/^\n+/, '');
  const end = rest.indexOf('\n\n');
  const text = (end < 0 ? rest : rest.slice(0, end)).trim();
  if (!text) {
    throw new Error(`services/dispat/README.md: nothing follows the fenced block under "## ${heading}"`);
  }
  return unwrap(text);
}

/**
 * Joins a hard-wrapped block into one line. The README wraps at 120 columns;
 * a line break there is typography, not content.
 */
function unwrap(text: string): string {
  return text.replace(/\s*\n\s*/g, ' ').trim();
}

/**
 * Splits a section into its top-level `- ` bullets, each unwrapped.
 *
 * Continuation lines are indented, which is what tells them apart from the
 * next bullet — so the split is on a hyphen at column zero and nothing else.
 */
function bullets(body: string): string[] {
  return body
    .split(/^- /m)
    .slice(1)
    .map(unwrap)
    .filter(Boolean);
}

/**
 * One bullet as a card: the leading bold run is the title, the rest is the
 * body.
 *
 * The title is required to be plain text. A card title is rendered as a
 * heading, where a code span or a link would either be dropped or come out as
 * literal markdown, and neither is something to discover on the published
 * page.
 */
function feature(bullet: string): Feature {
  const titled = /^\*\*(.+?)\*\*\s*/.exec(bullet);
  if (!titled) {
    throw new Error(`services/dispat/README.md: a key feature does not open with its own **title**: ${bullet.slice(0, 60)}…`);
  }
  const title = titled[1];
  if (/[`*[\]]/.test(title)) {
    throw new Error(`services/dispat/README.md: the key feature title "${title}" must be plain text`);
  }
  return {
    // The README ends each title with a full stop because it reads as the
    // opening of a sentence there. On a card it is a heading.
    title: title.replace(/\.$/, ''),
    body: parseInline(bullet.slice(titled[0].length), `key feature "${title}"`),
  };
}

/** Reads everything the landing page takes from the CLI README. */
export function parseReadme(src: string): ReadmeData {
  const terminal = section(src, 'In the terminal');
  const [transcript, after] = firstFence(terminal, 'In the terminal');
  const features = bullets(section(src, 'Key features')).map(feature);
  if (features.length === 0) {
    throw new Error('services/dispat/README.md: no bullets under "## Key features"');
  }
  return {
    transcript,
    transcriptNote: parseInline(paragraph(terminal, after, 'In the terminal'), 'the terminal tour note'),
    features,
  };
}
