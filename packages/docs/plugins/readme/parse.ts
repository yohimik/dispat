import {firstFence, firstList, firstPara, paraAfterList, parseBlocks, section} from './blocks';
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

/** Parses one block of inline markdown, naming the source if it fails. */
function inline(text: string, source: Source, where: string): Inline[] {
  return parseInline(text, `${source.path}: ${where}`, source.dir);
}

function inlines(texts: string[], source: Source, where: string): Inline[][] {
  return texts.map((text, i) => inline(text, source, `${where}, item ${i + 1}`));
}

/**
 * A paragraph, the list under it, and the paragraph that closes the list.
 *
 * Both sections read from the repository README are shaped this way: state the
 * problem, enumerate it, then answer it.
 */
function argument(blocks: ReturnType<typeof parseBlocks>, source: Source, heading: string, closed: boolean): Argument {
  const where = `"## ${heading}"`;
  const body = section(blocks, heading, source.path);
  const list = firstList(body, `${source.path}: ${where}`);
  return {
    intro: inline(firstPara(body, `${source.path}: ${where}`), source, where),
    ordered: list.ordered,
    items: inlines(list.items, source, where),
    outro: closed ? inline(paraAfterList(body, `${source.path}: ${where}`), source, `${where} closing`) : undefined,
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
    // The problems section ends on the paragraph that answers them; the
    // inspiration list is followed by the next heading, so it has no closing
    // remark to look for.
    problems: argument(blocks, ROOT_README, 'Why one more monorepo tool?', true),
    inspiration: argument(blocks, ROOT_README, 'Inspiration', false),
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
function feature(bullet: string): Feature {
  const titled = /^\*\*(.+?)\*\*\s*/.exec(bullet);
  if (!titled) {
    throw new Error(`${CLI_README.path}: a key feature does not open with its own **title**: ${bullet.slice(0, 60)}…`);
  }
  const title = titled[1];
  if (/[`*[\]]/.test(title)) {
    throw new Error(`${CLI_README.path}: the key feature title "${title}" must be plain text`);
  }
  return {
    // The README ends each title with a full stop because it reads as the
    // opening of a sentence there. On a card it is a heading.
    title: title.replace(/\.$/, ''),
    body: inline(bullet.slice(titled[0].length), CLI_README, `key feature "${title}"`),
  };
}

/** Reads the CLI README. */
export function parseCliReadme(src: string): CliReadme {
  const blocks = parseBlocks(src);

  // The first fenced block under "In the terminal" is the walkthrough; the "a
  // few more moves" block below it is a reference list and belongs on the CLI
  // page rather than in the hero.
  const terminal = firstFence(section(blocks, 'In the terminal', CLI_README.path), `${CLI_README.path}: "## In the terminal"`);
  const features = firstList(section(blocks, 'Key features', CLI_README.path), `${CLI_README.path}: "## Key features"`).items.map(feature);

  return {
    transcript: terminal.code,
    transcriptNote: inline(terminal.note, CLI_README, 'the terminal tour note'),
    features,
  };
}
