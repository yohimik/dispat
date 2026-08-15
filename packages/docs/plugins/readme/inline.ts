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
const SITE = 'https://yohimik.github.io/dispat';

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

/**
 * Parses one run of inline markdown into tokens.
 *
 * `where` names the README section, so a failure says which paragraph to look
 * at rather than only what was wrong with it, and `baseDir` is the folder the
 * README sits in, which is what a relative link is relative to.
 */
export function parseInline(src: string, where: string, baseDir: string): Inline[] {
  const out: Inline[] = [];
  let text = '';
  let i = 0;

  const flush = () => {
    if (text) {
      out.push({t: 'text', v: text});
      text = '';
    }
  };
  // Consumes up to `close`, returning what was between. The delimiters here
  // never nest inside themselves, so a plain search is the whole rule.
  const until = (close: string, from: number, what: string): [string, number] => {
    const end = src.indexOf(close, from);
    if (end < 0) {
      fail(where, src, from, `unclosed ${what}`);
    }
    return [src.slice(from, end), end + close.length];
  };

  while (i < src.length) {
    const ch = src[i];
    if (ch === '`') {
      const [code, next] = until('`', i + 1, '`code`');
      flush();
      out.push({t: 'code', v: code});
      i = next;
    } else if (src.startsWith('**', i)) {
      const [inner, next] = until('**', i + 2, '**strong**');
      flush();
      out.push({t: 'strong', v: parseInline(inner, where, baseDir)});
      i = next;
    } else if (ch === '*') {
      const [inner, next] = until('*', i + 1, '*emphasis*');
      flush();
      out.push({t: 'em', v: parseInline(inner, where, baseDir)});
      i = next;
    } else if (ch === '[') {
      const [label, afterLabel] = until(']', i + 1, '[link label]');
      if (src[afterLabel] !== '(') {
        fail(where, src, afterLabel, 'a bracketed span that is not a link');
      }
      const [href, next] = until(')', afterLabel + 1, '(link target)');
      flush();
      if (href.startsWith('#')) {
        fail(where, src, afterLabel, `${href} is an anchor into the README, which this site does not publish`);
      }
      out.push({t: 'link', ...rewrite(href, baseDir), v: parseInline(label, where, baseDir)});
      i = next;
    } else {
      text += ch;
      i += 1;
    }
  }
  flush();
  return out;
}
