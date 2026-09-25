import type {Inline} from './types';

// A markdown inline parser covering exactly the four constructs the CLI
// README's key features use: `code`, **strong**, *emphasis* and [links](url).
//
// Deliberately not a markdown library. The input is one file in this
// repository, written by the same people who read this parser, and the failure
// mode that matters is silence: a construct nobody handled rendering as
// literal asterisks on the landing page, noticed by a visitor rather than by
// CI. So anything unbalanced throws, and the site build fails the way it
// already does for a broken link.

/** The site's own base URL as the README spells it. */
const SITE = 'https://dispat.dev';

/** The repository, for the paths a README writes relative to itself. */
const GITHUB = 'https://github.com/yohimik/dispat';

/**
 * Rewrites a link written for a README reader into one that works on this
 * site.
 *
 * Two kinds need it. A **documentation** link is absolute in the README —
 * relative markdown to a docs page would mean nothing on GitHub — and here
 * those are the very pages being built: going out to the live site would take
 * a reader off the version they are reading and hide the link from the build's
 * link checker. Rewritten, a renamed page fails CI instead.
 *
 * A **repository** link is relative in the README, resolved against the folder
 * that README sits in, and means nothing at all on a site served from another
 * origin. It becomes a GitHub URL. `tree` or `blob` is decided by whether the
 * last segment carries an extension, because the site build cannot stat a path
 * that is not in its container's context.
 */
export function rewrite(href: string, baseDir: string): {href: string; internal: boolean} {
  if (href === SITE || href === `${SITE}/`) {
    return {href: '/', internal: true};
  }
  if (href.startsWith(`${SITE}/`)) {
    return {href: href.slice(SITE.length), internal: true};
  }
  if (/^https?:\/\//.test(href)) {
    return {href, internal: false};
  }
  const target = resolve(baseDir, href);
  const kind = /\.[a-z0-9]+$/i.test(target.slice(target.lastIndexOf('/') + 1)) ? 'blob' : 'tree';
  return {href: `${GITHUB}/${kind}/main/${target}`, internal: false};
}

/** Resolves `../../tests/integration` against `services/dispat`. */
function resolve(baseDir: string, href: string): string {
  const out: string[] = baseDir ? baseDir.split('/') : [];
  for (const part of href.split('/')) {
    if (part === '' || part === '.') {
      continue;
    }
    if (part === '..') {
      if (out.length === 0) {
        throw new Error(`the link ${href} climbs above the repository root`);
      }
      out.pop();
      continue;
    }
    out.push(part);
  }
  return out.join('/');
}

/** Where a parse gave up, named well enough to fix without a debugger. */
function fail(where: string, src: string, at: number, what: string): never {
  const around = src.slice(Math.max(0, at - 40), at + 40).replace(/\n/g, ' ');
  throw new Error(`${where}: ${what} at offset ${at}: …${around}…`);
}

/** The characters that open a construct; everything else is text. */
const OPENERS = '`*[';

interface InlineReaderOptions {
  src: string;
  where: string;
  baseDir: string;
}

/** One pass over a run of inline markdown, token by token. */
class InlineReader {
  private readonly src: string;
  private readonly where: string;
  private readonly baseDir: string;
  private position = 0;

  constructor(options: InlineReaderOptions) {
    const {src, where, baseDir} = options;
    this.src = src;
    this.where = where;
    this.baseDir = baseDir;
  }

  read(): Inline[] {
    const out: Inline[] = [];
    while (this.position < this.src.length) {
      out.push(this.readToken());
    }
    return out;
  }

  private readToken(): Inline {
    const at = this.position;
    const ch = this.src[at];
    if (ch === '`') {
      return {t: 'code', v: this.until('`', at + 1, '`code`')};
    }
    if (this.src.startsWith('**', at)) {
      return {t: 'strong', v: this.parseNested(this.until('**', at + 2, '**strong**'))};
    }
    if (ch === '*') {
      return {t: 'em', v: this.parseNested(this.until('*', at + 1, '*emphasis*'))};
    }
    if (ch === '[') {
      return this.readLink();
    }
    return this.readText();
  }

  private readLink(): Inline {
    const label = this.until(']', this.position + 1, '[link label]');
    const afterLabel = this.position;
    if (this.src[afterLabel] !== '(') {
      fail(this.where, this.src, afterLabel, 'a bracketed span that is not a link');
    }
    const href = this.until(')', afterLabel + 1, '(link target)');
    if (href.startsWith('#')) {
      fail(this.where, this.src, afterLabel, `${href} is an anchor into the README, which this site does not publish`);
    }
    return {t: 'link', ...rewrite(href, this.baseDir), v: this.parseNested(label)};
  }

  /** Plain text runs up to the next character that opens a construct. */
  private readText(): Inline {
    const start = this.position;
    while (this.position < this.src.length && !OPENERS.includes(this.src[this.position])) {
      this.position += 1;
    }
    return {t: 'text', v: this.src.slice(start, this.position)};
  }

  // Consumes up to `close`, returning what was between. The delimiters here
  // never nest inside themselves, so a plain search is the whole rule.
  private until(close: string, from: number, what: string): string {
    const end = this.src.indexOf(close, from);
    if (end < 0) {
      fail(this.where, this.src, from, `unclosed ${what}`);
    }
    this.position = end + close.length;
    return this.src.slice(from, end);
  }

  private parseNested(inner: string): Inline[] {
    return parseInline(inner, this.where, this.baseDir);
  }
}

/**
 * Parses one run of inline markdown into tokens.
 *
 * `where` names the README section, so a failure says which paragraph to look
 * at rather than only what was wrong with it, and `baseDir` is the folder the
 * README sits in, which is what a relative link is relative to.
 */
export function parseInline(src: string, where: string, baseDir: string): Inline[] {
  return new InlineReader({src, where, baseDir}).read();
}
