import {firstList, firstPara, parseBlocks, section} from './blocks';
import {parseInline} from './inline';
import type {Argument, CliReadme, Feature, Inline, RepositoryReadme} from './types';

// Reading the landing page out of the two READMEs it used to restate.
//
// The hero, the "why one more monorepo tool?" argument, the feature cards and
// the inspiration list were all a second copy, typed into src/pages/index.tsx
// under a comment saying it "carries the same claims as the repository README"
// — which is a promise a comment cannot keep. The READMEs are the copies a
// reader arrives at from GitHub, `go install` or a search result, so they are
// the ones that have to be right, and this makes them the only ones.
//
// Every extraction is strict: a heading that moved, a bullet that stopped
// opening with its own title, a link that stopped resolving. The site build is
// already the link checker; this makes it the README checker too.

/** Where a README sits, so its relative links can be resolved. */
export const ROOT_README = {path: 'README.md', dir: ''};
export const CLI_README = {path: 'services/dispat/README.md', dir: 'services/dispat'};

type Source = typeof ROOT_README;

// Explicit content slots: copy edits do not rename anchors, selectors, or
// recordings. Adding/reordering a README feature requires choosing its ID.
const FEATURE_IDS = ['release-graph', 'blast-radius', 'self-healing', 'release-control', 'polyglot', 'step-commands'] as const;

/** Parses one block of inline markdown, naming the source if it fails. */
function inline(text: string, source: Source, where: string): Inline[] {
  return parseInline(text, `${source.path}: ${where}`, source.dir);
}

function inlines(texts: string[], source: Source, where: string): Inline[][] {
  return texts.map((text, i) => inline(text, source, `${where}, item ${i + 1}`));
}

/**
 * A paragraph and the list under it: the shape of the inspiration section.
 */
function argument(blocks: ReturnType<typeof parseBlocks>, source: Source, heading: string): Argument {
  const where = `"## ${heading}"`;
  const body = section(blocks, heading, source.path);
  const list = firstList(body, `${source.path}: ${where}`);
  return {
    intro: inline(firstPara(body, `${source.path}: ${where}`), source, where),
    ordered: list.ordered,
    items: inlines(list.items, source, where),
  };
}

/** Reads the repository README. */
export function parseRepositoryReadme(src: string): RepositoryReadme {
  const blocks = parseBlocks(src);

  // Everything before the install commands, which is where the prose stops and
  // the reference begins. Badge-only paragraphs are markup rather than text.
  const lead: string[] = [];
  for (const block of blocks) {
    if (block.kind === 'fence') {
      break;
    }
    if (block.kind === 'para' && !/^\[!\[/.test(block.text)) {
      lead.push(block.text);
    }
  }
  if (lead.length === 0) {
    throw new Error(`${ROOT_README.path}: no opening paragraphs before the first code block`);
  }

  // The projects section is a bare list: no intro paragraph to demand, no
  // closing remark to look for — just the repositories themselves.
  const usersWhere = `${ROOT_README.path}: "## Projects using dispat"`;
  const users = firstList(section(blocks, 'Projects using dispat', ROOT_README.path), usersWhere);

  return {
    lead: inlines(lead, ROOT_README, 'the opening'),
    inspiration: argument(blocks, ROOT_README, 'Inspiration'),
    users: inlines(users.items, ROOT_README, '"## Projects using dispat"'),
  };
}

/**
 * One `## Key features` bullet as a card: the leading bold run is the title,
 * the rest is the body.
 *
 * The title is required to be plain text. A card title is rendered as a
 * heading, where a code span or a link would either be dropped or come out as
 * literal markdown, and neither is something to discover on the published page.
 */
function feature(bullet: string, index: number): Feature {
  const titled = /^\*\*(.+?)\*\*\s*/.exec(bullet);
  if (!titled) {
    throw new Error(`${CLI_README.path}: a key feature does not open with its own **title**: ${bullet.slice(0, 60)}…`);
  }
  const title = titled[1];
  if (/[`*[\]]/.test(title)) {
    throw new Error(`${CLI_README.path}: the key feature title "${title}" must be plain text`);
  }
  const normalizedTitle = title.replace(/\.$/, '');
  const id = FEATURE_IDS[index];
  if (!id) throw new Error(`${CLI_README.path}: key feature ${index + 1} has no stable feature ID`);
  return {
    id,
    // The README ends each title with a full stop because it reads as the
    // opening of a sentence there. On a card it is a heading.
    title: normalizedTitle,
    body: inline(bullet.slice(titled[0].length), CLI_README, `key feature "${title}"`),
  };
}

/** Reads the CLI README. */
export function parseCliReadme(src: string): CliReadme {
  const blocks = parseBlocks(src);

  // The terminal tour under "## In the terminal" stays with the README: the
  // hero used to quote it, but the demo carousel now plays the same lines as
  // the scenes' log captions, so the only extraction left is the feature
  // cards.
  const features = firstList(section(blocks, 'Key features', CLI_README.path), `${CLI_README.path}: "## Key features"`).items.map(feature);

  return {features};
}
