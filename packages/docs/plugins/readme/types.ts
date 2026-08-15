// The shape the landing page receives from the two READMEs it is built from.
//
// Everything here crosses the plugin boundary through setGlobalData, so it has
// to be JSON: inline markdown arrives as a token tree rather than as rendered
// elements, and @site/src/components/Inline turns it back into React on the
// page. Keeping it to tokens is also what keeps the payload small — global
// data is loaded on every route, so neither README must ever be in it whole.

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

/** A paragraph, a list, and the paragraph that closes it. */
export interface Argument {
  intro: Inline[];
  /** Whether the README numbered the list, which is part of what it says. */
  ordered: boolean;
  items: Inline[][];
  outro?: Inline[];
}

/** One `## Key features` bullet of the CLI README, as a card. */
export interface Feature {
  title: string;
  body: Inline[];
}

/** What the landing page takes from the repository README. */
export interface RepositoryReadme {
  /** The opening paragraphs, before the install commands: what dispat is. */
  lead: Inline[][];
  /** `## Why one more monorepo tool?`: the two situations, and the answer. */
  problems: Argument;
  /** `## Inspiration`: what dispat descends from. */
  inspiration: Argument;
}

/** What the landing page takes from the CLI README. */
export interface CliReadme {
  /** The terminal tour: the first fenced block under `## In the terminal`. */
  transcript: string;
  /** The paragraph that follows it, which explains what the tour left out. */
  transcriptNote: Inline[];
  features: Feature[];
}

export interface ReadmeData {
  repository: RepositoryReadme;
  cli: CliReadme;
}
