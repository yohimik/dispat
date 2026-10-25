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

/**
 * One step of a protocol and the code it exited with. `kind` and `phase` are
 * absent from records written before the harness typed its steps.
 */
export interface ExperimentStep {
  step: string;
  exit: number;
  /** `release`: the tool's own command; `manual`: done by hand; `query`: a read-only question. */
  kind?: ExperimentStepKind;
  /** `initial`: the run the fault was injected into; `catch-up`: what finishing it took. */
  phase?: ExperimentStepPhase;
}

export type ExperimentStepKind = 'release' | 'manual' | 'query';

export type ExperimentStepPhase = 'initial' | 'catch-up';

/**
 * What finishing a release took once its fault was removed, as the harness's
 * lib/recovery.jq counts it. A run is an uninterrupted pass of the tool's own
 * commands, ended by a manual step or by a command that failed; a query is
 * never counted. `converged` means every package ended consistent or at its
 * baseline, the clone clean and level with its origin, and the tool's own next
 * plan empty.
 */
export interface ExperimentRecovery {
  runs: number;
  releaseCommands: number;
  manualCommands: number;
  converged: boolean;
}

/**
 * A timing cell's figures in the vocabulary of the dispat theorem (CCME
 * SPEC.md §13.11), as the harness's lib/timing.py computes them from the
 * fixture's own records of every build and upload. Seconds throughout.
 */
export interface ExperimentTiming {
  /** What a consumer's build waits for, as the tool's configuration declares it (§19.2a). */
  relation: ExperimentRelation;
  /** The strongest relation every consumer build in the records honoured; absent where no consumer built. */
  observedRelation?: ExperimentRelation;
  consumerBuilds: ExperimentConsumerBuilds;
  /** Every edge of the declared precedence the records contradict. */
  violations: ExperimentEdge[];
  planningSeconds: number;
  /** The builds the plan command ran inside its own duration. */
  planBuilds: ExperimentPlanBuilds;
  makespanSeconds: number;
  /** The part of the makespan no task was running in. */
  idleSeconds: number;
  /** Wk, the summed duration of the release's builds and uploads. */
  work: number;
  /** Lk, the duration of the longest precedence chain. */
  criticalPath: number;
  /** s as the release was seen to use it: the most tasks running at one instant. */
  parallelism: number;
  /** max(Lk, Wk / s). */
  bound: number;
  /** The makespan over the bound. */
  ratio: number;
  /** The tasks on the longest chain, the first first. */
  criticalChain: string[];
  tasks: ExperimentTaskKinds;
  taskTimes: ExperimentTaskTime[];
}

export type ExperimentRelation = 'publish' | 'build' | 'none';

/** Provider-to-consumer pairs that both built, and how many consumer builds followed the provider's build and its publication. */
export interface ExperimentConsumerBuilds {
  edges: number;
  afterBuild: number;
  afterPublish: number;
}

/** One precedence edge, `core build` before `ui build`. */
export interface ExperimentEdge {
  before: string;
  after: string;
}

export interface ExperimentPlanBuilds {
  count: number;
  seconds: number;
}

/** The builds and uploads Wk is made of, and the `npm publish` processes beside them, which are not part of it. */
export interface ExperimentTaskKinds {
  build: ExperimentTaskStats;
  publish: ExperimentTaskStats;
  npmPublish: ExperimentTaskStats;
}

/** One kind's tasks: how many, their summed, median and longest durations, and how many failed. */
export interface ExperimentTaskStats {
  count: number;
  seconds: number;
  median: number;
  max: number;
  failed: number;
}

/** One task: its package, its kind, when it started after the release did, and how long it ran. */
export interface ExperimentTaskTime {
  package: string;
  task: 'build' | 'publish';
  start: number;
  seconds: number;
}

/** One package of a plan at the version planned for it. */
export interface ExperimentPlannedPackage {
  name: string;
  version: string;
}

/** One package two plans disagree about; a version is empty where that plan releases none. */
export interface ExperimentPlanDifference {
  package: string;
  version: string;
  reference: string;
}

/**
 * What a tool's own plan command said it would release, set beside dispat's
 * plan of the same scenario. `reference` and `agrees` are absent for dispat's
 * own plan and where no dispat cell recorded one.
 */
export interface ExperimentPlan {
  packages: ExperimentPlannedPackage[];
  /** What the tool read of the pending change: the commit subject, or the pending changesets. */
  told: string;
  reference?: string;
  agrees?: boolean;
  differences: ExperimentPlanDifference[];
}

/** One expectation about the state a run left behind, and whether it held. */
export interface ExperimentCheck {
  check: string;
  ok: boolean;
}

/**
 * One package's answer at the end of a run. `registry` is the version served,
 * `absent`, or `error` when the registry itself answered with one; `state` is
 * the harness's vocabulary (consistent, orphan, unpushed, dangling,
 * unrecorded).
 */
export interface ExperimentPackage {
  name: string;
  registry: string;
  state: string;
}

/** The state a run ended in, from the last observation it took. */
export interface ExperimentState {
  label: string;
  packages: ExperimentPackage[];
}

/**
 * One cell: one experiment, one scenario where it has one, one tool. `passed`
 * is the harness's own verdict, which gates a run only for the tool the
 * expectations are about; for a compared tool the cell is a record, and
 * `false` there describes that tool.
 */
export interface ExperimentCell {
  id: string;
  experiment: string;
  /** Empty for an experiment that has only one scenario. */
  scenario: string;
  tool: string;
  dispat: string;
  platform: string;
  steps: ExperimentStep[];
  checks: ExperimentCheck[];
  passed: boolean;
  final: ExperimentState;
  /** Absent for a protocol that measures no catch-up, and in every older record. */
  recovery?: ExperimentRecovery;
  /** Present in the timing cells alone. */
  timing?: ExperimentTiming;
  plan?: ExperimentPlan;
}

/**
 * The release experiments' campaign: every cell run against one published
 * image. `version` is empty when the cells disagree, which is the only honest
 * answer to which release a merged folder is about.
 */
export interface Experiments {
  version: string;
  cells: ExperimentCell[];
}

export interface Report {
  /** RFC 3339, UTC. */
  generatedAt: string;
  commit: string;
  coverage: Coverage;
  suite: Suite;
  benchmarks: Benchmarks;
  experiments: Experiments;
}

/** A release report preserved under its documentation minor version. */
export interface ArchivedReport {
  docsVersion: string;
  report?: Report;
  evidence?: HistoricalEvidence;
}

/** Exact aggregate facts recovered when the full report was not retained. */
export interface HistoricalEvidence {
  releaseVersion: string;
  generatedAt: string;
  commit: string;
  runUrl: string;
  coverageArtifactUrl: string;
  coverage: {unitPercent: number; integrationPercent: number; totalPercent: number; statements: number};
  suite: {tests: number; fuzz: number};
  benchmarks: number;
  experiments?: {cells: number; artifactUrl: string};
  experimentResults?: Experiments;
}

/** What the plugin puts in global data: the report and the doc versions it describes. */
export interface ReportData {
  report: Report | null;
  currentVersions: string[];
  archivedVersions: string[];
}
