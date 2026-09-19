// Shared metadata for the landing page, search results and social cards.

/** The project's promise, in the form the READMEs open with. */
export const TAGLINE = 'Autistic stability for ADHD projects. Mathematical planning. Saga recovery.';

/**
 * The tagline's first sentence: the landing page's own title, and what a
 * browser tab, a search result and a shared card show above everything else.
 * A title is a headline rather than a paragraph, so the two short sentences
 * that finish the tagline are the hero's note under the buttons instead.
 */
export const PAGE_TITLE = 'Autistic stability for ADHD projects';

/** The note the hero closes with: the rest of the tagline. */
export const TAGLINE_NOTE = TAGLINE.slice(`${PAGE_TITLE}. `.length);

/**
 * The project stated as a name rather than as a page: `alternateName` in both
 * structured-data blocks, and the site-wide og:/twitter: title a page that
 * states none of its own falls back to.
 */
export const TITLE = 'dispat: autistic stability for ADHD projects';

/** One sentence, short enough to survive a search result intact. */
export const DESCRIPTION =
  'Autistic stability for ADHD projects. Mathematical version planning and saga recovery ' +
  'to release packages in any language, across one repository or many.';
