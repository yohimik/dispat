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
  /**
   * Benchmark functions that ran in a test pass, which is nought for every
   * suite: benchmarks are measured in a pass of their own, not tallied here.
   */
  benchmarks: number;
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
  fuzzTargets: FuzzTarget[];
}

/**
 * One fuzz function and what the run put through it. A fuzz target under a
 * plain `go test` runs its corpus — the f.Add seeds plus whatever testdata
 * holds — so `seeds` is what was exercised rather than a promise about a
 * fuzzing session nobody ran.
 */
export interface FuzzTarget {
  name: string;
  package: string;
  seeds: number;
}

export interface Suite {
  totals: Counts;
  groups: Group[];
}

/**
 * One benchmark's result. Zero means the benchmark did not report the figure:
 * a run without -benchmem leaves the two allocation numbers at nought, and
 * only a benchmark calling SetBytes reports a throughput.
 */
export interface Benchmark {
  name: string;
  package: string;
  /** The GOMAXPROCS the name carried as its -N suffix. */
  procs: number;
  /** The iterations the timing was averaged over. */
  runs: number;
  nsPerOp: number;
  bytesPerOp: number;
  allocsPerOp: number;
  mbPerSec: number;
}

/**
 * One `testreport bench` invocation, with the machine it ran on. The machine
 * is part of the measurement: a nanosecond figure without the CPU that
 * produced it is a number nobody can compare anything to.
 */
export interface BenchGroup {
  id: string;
  path: string;
  goos: string;
  goarch: string;
  cpu: string;
  results: Benchmark[];
}

export interface Benchmarks {
  groups: BenchGroup[];
}

export interface Report {
  /** RFC 3339, UTC. */
  generatedAt: string;
  commit: string;
  coverage: Coverage;
  suite: Suite;
  benchmarks: Benchmarks;
}

/** What the plugin puts in global data: the report, or nothing measured. */
export interface ReportData {
  report: Report | null;
}
