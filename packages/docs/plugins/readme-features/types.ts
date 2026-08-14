// The shape the landing page receives from services/dispat/README.md.
//
// Everything here crosses the plugin boundary through setGlobalData, so it has
// to be JSON: inline markdown arrives as a token tree rather than as rendered
// elements, and @site/src/components/Inline turns it back into React on the
// page. Keeping it to tokens is also what keeps the payload small — global
// data is loaded on every route, so the raw README must never be in it.

/** One run of inline markdown. */
export type Inline =
  | {t: 'text'; v: string}
  | {t: 'code'; v: string}
  | {t: 'strong'; v: Inline[]}
  | {t: 'em'; v: Inline[]}
  /**
   * `internal` marks a link that resolves inside this site, which the renderer
   * has to know: an internal one goes through <Link to> so Docusaurus routes
   * it and the build's link checker validates it.
   */
  | {t: 'link'; href: string; internal: boolean; v: Inline[]};

/** One `## Key features` bullet. */
export interface Feature {
  title: string;
  body: Inline[];
}

/** Everything the landing page reads out of the CLI README. */
export interface ReadmeData {
  /** The terminal tour: the first fenced block under `## In the terminal`. */
  transcript: string;
  /** The paragraph that follows it, which explains what the tour left out. */
  transcriptNote: Inline[];
  features: Feature[];
}
