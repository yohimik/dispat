// The report `go run -C tools ./testreport build` writes, as the site reads
// it. Mirrors tools/testreport/report.go, which is where the field
// documentation lives.

/** One statement-coverage measurement. */
export interface Stats {
  statements: number;
  covered: number;
  percent: number;
}

/** One Go package's coverage, by workspace-relative path. */
export interface CoveragePackage extends Stats {
  path: string;
}

/** One Go module's coverage, with the packages inside it. */
export interface CoverageModule extends Stats {
  path: string;
  packages: CoveragePackage[];
}

/**
 * The three layers. They overlap — the CLI's own tests and the black-box
 * suite's instrumented binary both cover services/dispat — so `total` is the
 * merge of every profile, neither the sum nor the average of the other two.
 */
export interface Coverage {
  total: Stats;
  unit: Stats;
  integration: Stats;
  modules: CoverageModule[];
}

/**
 * The tally of one `go test` invocation, or of the whole run. `tests` and
 * `fuzz` count top-level functions; `subtests` are counted apart, because a
 * suite reporting its subtests as tests inflates the number several-fold.
 */
export interface Counts {
  packages: number;
  tests: number;
  fuzz: number;
  subtests: number;
  passed: number;
  failed: number;
  skipped: number;
  /** Seconds. */
  elapsed: number;
}

/** One invocation: a package's `tests` script, or one of the suite's passes. */
export interface Group extends Counts {
  id: string;
  path: string;
  race: boolean;
}

export interface Suite {
  totals: Counts;
  groups: Group[];
}

export interface Report {
  /** RFC 3339, UTC. */
  generatedAt: string;
  commit: string;
  coverage: Coverage;
  suite: Suite;
}

/** What the plugin puts in global data: the report, or nothing measured. */
export interface ReportData {
  report: Report | null;
}
