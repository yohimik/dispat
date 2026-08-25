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

/** Splits a markdown document into blocks, in order. */
export function parseBlocks(src: string): Block[] {
  const lines = src.split('\n');
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === '') {
      i += 1;
      continue;
    }

    const fence = /^```(\S*)\s*$/.exec(line);
    if (fence) {
      const code: string[] = [];
      i += 1;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        code.push(lines[i]);
        i += 1;
      }
      if (i === lines.length) {
        throw new Error(`unclosed code fence opened with ${JSON.stringify(line)}`);
      }
      blocks.push({kind: 'fence', lang: fence[1], code: code.join('\n')});
      i += 1;
      continue;
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      blocks.push({kind: 'heading', level: heading[1].length, text: heading[2].trim()});
      i += 1;
      continue;
    }

    if (BULLET.test(line)) {
      // One list runs until a blank line. Continuation lines are indented,
      // which is the only thing telling them from the next item.
      const items: string[] = [];
      let current: string[] = [];
      const ordered = /^\d/.test(line);
      while (i < lines.length && lines[i].trim() !== '') {
        const marker = BULLET.exec(lines[i]);
        if (marker && !/^\s/.test(lines[i])) {
          if (current.length > 0) {
            items.push(unwrap(current));
          }
          current = [lines[i].slice(marker[0].length)];
        } else {
          current.push(lines[i].trim());
        }
        i += 1;
      }
      items.push(unwrap(current));
      blocks.push({kind: 'list', ordered, items});
      continue;
    }

    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== '' && !BULLET.test(lines[i]) && !/^```/.test(lines[i]) && !/^#/.test(lines[i])) {
      para.push(lines[i].trim());
      i += 1;
    }
    blocks.push({kind: 'para', text: unwrap(para)});
  }

  return blocks;
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

