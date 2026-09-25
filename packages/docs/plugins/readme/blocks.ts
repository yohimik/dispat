// Splitting a README into the handful of block kinds this site reads out of
// one: paragraphs, fenced code, and lists.
//
// Deliberately not a markdown parser. Two files in this repository are the
// only input, and what matters is that a heading someone renamed or a list
// someone reflowed *fails* rather than quietly producing an empty landing
// page. Every selector below therefore throws when it finds nothing.

export type Block =
  | {kind: 'heading'; level: number; text: string}
  | {kind: 'para'; text: string}
  | {kind: 'fence'; lang: string; code: string}
  | {kind: 'list'; ordered: boolean; items: string[]};

/**
 * Joins a hard-wrapped block into one line. Both READMEs wrap at 120 columns;
 * a line break there is typography, not content.
 */
function unwrap(lines: string[]): string {
  return lines.join(' ').replace(/\s+/g, ' ').trim();
}

const BULLET = /^([-*]|\d+\.)\s+/;

/** A position in a document's lines, which the block readers advance. */
class LineCursor {
  private readonly lines: string[];
  private index = 0;

  constructor(lines: string[]) {
    this.lines = lines;
  }

  isDone(): boolean {
    return this.index >= this.lines.length;
  }

  line(): string {
    return this.lines[this.index];
  }

  advance(): void {
    this.index += 1;
  }
}

interface BlockReaderOptions {
  cursor: LineCursor;
}

/** Splits a markdown document into blocks, in order. */
export function parseBlocks(src: string): Block[] {
  const cursor = new LineCursor(src.split('\n'));
  const blocks: Block[] = [];
  while (!cursor.isDone()) {
    if (cursor.line().trim() === '') {
      cursor.advance();
      continue;
    }
    blocks.push(readBlock({cursor}));
  }
  return blocks;
}

/** Reads the block starting at the cursor's non-blank line. */
function readBlock(options: BlockReaderOptions): Block {
  const {cursor} = options;
  const line = cursor.line();

  const fence = /^```(\S*)\s*$/.exec(line);
  if (fence) {
    return readFence({cursor, lang: fence[1]});
  }

  const heading = /^(#{1,6})\s+(.*)$/.exec(line);
  if (heading) {
    cursor.advance();
    return {kind: 'heading', level: heading[1].length, text: heading[2].trim()};
  }

  if (BULLET.test(line)) {
    return readList({cursor});
  }

  return readParagraph({cursor});
}

interface ReadFenceOptions extends BlockReaderOptions {
  lang: string;
}

/** Reads a fenced code block, which has to be closed. */
function readFence(options: ReadFenceOptions): Block {
  const {cursor, lang} = options;
  const opening = cursor.line();
  const code: string[] = [];
  cursor.advance();
  while (!cursor.isDone() && !/^```\s*$/.test(cursor.line())) {
    code.push(cursor.line());
    cursor.advance();
  }
  if (cursor.isDone()) {
    throw new Error(`unclosed code fence opened with ${JSON.stringify(opening)}`);
  }
  cursor.advance();
  return {kind: 'fence', lang, code: code.join('\n')};
}

/** Reads a list, which runs until a blank line. */
function readList(options: BlockReaderOptions): Block {
  const {cursor} = options;
  const ordered = /^\d/.test(cursor.line());
  const lines: string[] = [];
  while (!cursor.isDone() && cursor.line().trim() !== '') {
    lines.push(cursor.line());
    cursor.advance();
  }
  return {kind: 'list', ordered, items: groupListItems({lines})};
}

interface GroupListItemsOptions {
  lines: string[];
}

/**
 * Groups a list's lines into its items. Continuation lines are indented,
 * which is the only thing telling them from the next item.
 */
function groupListItems(options: GroupListItemsOptions): string[] {
  const {lines} = options;
  const starts = lines.flatMap((line, index) => (BULLET.test(line) && !/^\s/.test(line) ? [index] : []));
  return starts.map((start, index) => {
    const end = starts[index + 1] ?? lines.length;
    const marker = BULLET.exec(lines[start]) as RegExpExecArray;
    const continuation = lines.slice(start + 1, end).map((line) => line.trim());
    return unwrap([lines[start].slice(marker[0].length), ...continuation]);
  });
}

/** Reads a paragraph, which runs until a blank line or another block. */
function readParagraph(options: BlockReaderOptions): Block {
  const {cursor} = options;
  const para: string[] = [];
  while (!cursor.isDone() && cursor.line().trim() !== '' && !BULLET.test(cursor.line()) && !/^```/.test(cursor.line()) && !/^#/.test(cursor.line())) {
    para.push(cursor.line().trim());
    cursor.advance();
  }
  return {kind: 'para', text: unwrap(para)};
}

/** The blocks under a `## <heading>`, up to the next heading of the same level. */
export function section(blocks: Block[], heading: string, where: string): Block[] {
  const start = blocks.findIndex((b) => b.kind === 'heading' && b.level === 2 && b.text === heading);
  if (start < 0) {
    throw new Error(`${where}: no "## ${heading}" section`);
  }
  const rest = blocks.slice(start + 1);
  const end = rest.findIndex((b) => b.kind === 'heading' && b.level <= 2);
  return end < 0 ? rest : rest.slice(0, end);
}

/** The first paragraph, or a failure naming what was being looked for. */
export function firstPara(blocks: Block[], where: string): string {
  const found = blocks.find((b) => b.kind === 'para');
  if (!found) {
    throw new Error(`${where}: no paragraph`);
  }
  return found.text;
}

/** The first list, with its items unwrapped and its numbering remembered. */
export function firstList(blocks: Block[], where: string): {ordered: boolean; items: string[]} {
  const found = blocks.find((b) => b.kind === 'list');
  if (!found || found.kind !== 'list') {
    throw new Error(`${where}: no list`);
  }
  return {ordered: found.ordered, items: found.items};
}

