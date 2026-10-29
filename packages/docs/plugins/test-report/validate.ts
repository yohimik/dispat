import {validateExecutionMeasurement} from './measurement.ts';
import type {
  Benchmark,
  BenchGroup,
  Benchmarks,
  Counts,
  Coverage,
  CoverageModule,
  CoveragePackage,
  ExperimentCell,
  ExperimentLLVM,
  ExperimentCheck,
  ExperimentPackage,
  ExperimentPlan,
  ExperimentPlanDifference,
  ExperimentPlannedPackage,
  ExperimentRecovery,
  ExperimentRelation,
  Experiments,
  ExperimentState,
  ExperimentStep,
  ExperimentStepKind,
  ExperimentStepPhase,
  ExperimentTaskStats,
  ExperimentTaskTime,
  ExperimentTiming,
  FuzzTarget,
  Group,
  Report,
  Stats,
  Suite,
  ArchivedReport,
  HistoricalEvidence,
} from './types';

// A shape check over the report before any page reads it.
//
// The file is produced by another program in another language, so "it is the
// right shape" is an assumption rather than a fact. Without this, a renamed field renders as `undefined%` on a published
// page — a wrong number that looks like a number. With it, the build stops and
// says which field.

class ReportError extends Error {
  constructor(at: string, want: string, got: unknown) {
    super(`${at}: expected ${want}, got ${JSON.stringify(got)}`);
    this.name = 'ReportError';
  }
}

function object(value: unknown, at: string): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new ReportError(at, 'an object', value);
  }
  return value as Record<string, unknown>;
}

function array(value: unknown, at: string): unknown[] {
  if (!Array.isArray(value)) {
    throw new ReportError(at, 'an array', value);
  }
  return value;
}

// A Go nil slice marshals to null rather than to []. Reading it as an empty
// list is the shape the report means, and refusing it would fail a build over
// a package that has nothing to report.
function optionalArray(value: unknown, at: string): unknown[] {
  if (value === null || value === undefined) {
    return [];
  }
  return array(value, at);
}

function number(value: unknown, at: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new ReportError(at, 'a number', value);
  }
  return value;
}

function string(value: unknown, at: string): string {
  if (typeof value !== 'string') {
    throw new ReportError(at, 'a string', value);
  }
  return value;
}

function boolean(value: unknown, at: string): boolean {
  if (typeof value !== 'boolean') {
    throw new ReportError(at, 'a boolean', value);
  }
  return value;
}

function stats(value: unknown, at: string): Stats {
  const o = object(value, at);
  return {
    statements: number(o.statements, `${at}.statements`),
    covered: number(o.covered, `${at}.covered`),
    percent: number(o.percent, `${at}.percent`),
  };
}

function coveragePackage(value: unknown, at: string): CoveragePackage {
  return {...stats(value, at), path: string(object(value, at).path, `${at}.path`)};
}

function coverageModule(value: unknown, at: string): CoverageModule {
  const o = object(value, at);
  return {
    ...stats(value, at),
    path: string(o.path, `${at}.path`),
    packages: array(o.packages, `${at}.packages`).map((p, i) => coveragePackage(p, `${at}.packages[${i}]`)),
  };
}

function coverage(value: unknown, at: string): Coverage {
  const o = object(value, at);
  return {
    total: stats(o.total, `${at}.total`),
    unit: stats(o.unit, `${at}.unit`),
    integration: stats(o.integration, `${at}.integration`),
    modules: array(o.modules, `${at}.modules`).map((m, i) => coverageModule(m, `${at}.modules[${i}]`)),
  };
}

function counts(value: unknown, at: string): Counts {
  const o = object(value, at);
  return {
    packages: number(o.packages, `${at}.packages`),
    tests: number(o.tests, `${at}.tests`),
    fuzz: number(o.fuzz, `${at}.fuzz`),
    benchmarks: number(o.benchmarks, `${at}.benchmarks`),
    subtests: number(o.subtests, `${at}.subtests`),
    passed: number(o.passed, `${at}.passed`),
    failed: number(o.failed, `${at}.failed`),
    skipped: number(o.skipped, `${at}.skipped`),
    elapsed: number(o.elapsed, `${at}.elapsed`),
  };
}

function fuzzTarget(value: unknown, at: string): FuzzTarget {
  const o = object(value, at);
  return {
    name: string(o.name, `${at}.name`),
    package: string(o.package, `${at}.package`),
    seeds: number(o.seeds, `${at}.seeds`),
  };
}

function group(value: unknown, at: string): Group {
  const o = object(value, at);
  return {
    ...counts(value, at),
    id: string(o.id, `${at}.id`),
    path: string(o.path, `${at}.path`),
    race: boolean(o.race, `${at}.race`),
    // A suite with no fuzz targets serialises the field as null rather than as
    // an empty array, which is Go's nil slice and not a missing field.
    fuzzTargets: optionalArray(o.fuzzTargets, `${at}.fuzzTargets`).map((f, i) =>
      fuzzTarget(f, `${at}.fuzzTargets[${i}]`),
    ),
  };
}

function benchmark(value: unknown, at: string): Benchmark {
  const o = object(value, at);
  return {
    name: string(o.name, `${at}.name`),
    package: string(o.package, `${at}.package`),
    procs: number(o.procs, `${at}.procs`),
    runs: number(o.runs, `${at}.runs`),
    nsPerOp: number(o.nsPerOp, `${at}.nsPerOp`),
    bytesPerOp: number(o.bytesPerOp, `${at}.bytesPerOp`),
    allocsPerOp: number(o.allocsPerOp, `${at}.allocsPerOp`),
    mbPerSec: number(o.mbPerSec, `${at}.mbPerSec`),
  };
}

function benchGroup(value: unknown, at: string): BenchGroup {
  const o = object(value, at);
  return {
    id: string(o.id, `${at}.id`),
    path: string(o.path, `${at}.path`),
    goos: string(o.goos, `${at}.goos`),
    goarch: string(o.goarch, `${at}.goarch`),
    cpu: string(o.cpu, `${at}.cpu`),
    results: optionalArray(o.results, `${at}.results`).map((r, i) => benchmark(r, `${at}.results[${i}]`)),
  };
}

function benchmarks(value: unknown, at: string): Benchmarks {
  const o = object(value, at);
  return {
    groups: optionalArray(o.groups, `${at}.groups`).map((g, i) => benchGroup(g, `${at}.groups[${i}]`)),
  };
}

function suite(value: unknown, at: string): Suite {
  const o = object(value, at);
  return {
    totals: counts(o.totals, `${at}.totals`),
    groups: array(o.groups, `${at}.groups`).map((g, i) => group(g, `${at}.groups[${i}]`)),
  };
}

interface OptionalWordOptions<T extends string> {
  value: unknown;
  at: string;
  words: readonly T[];
}

/**
 * An optional field holding one of a fixed set of words: absent in a record
 * written before the field existed, and refused when it holds anything else,
 * so a renamed value stops the build instead of rendering as an unknown one.
 */
function optionalWord<T extends string>(options: OptionalWordOptions<T>): T | undefined {
  const {value, at, words} = options;
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== 'string' || !(words as readonly string[]).includes(value)) {
    throw new ReportError(at, `one of ${words.join(', ')}`, value);
  }
  return value as T;
}

const STEP_KINDS: readonly ExperimentStepKind[] = ['release', 'manual', 'query'];
const STEP_PHASES: readonly ExperimentStepPhase[] = ['initial', 'catch-up'];

function experimentStep(value: unknown, at: string): ExperimentStep {
  const o = object(value, at);
  const kind = optionalWord({value: o.kind, at: `${at}.kind`, words: STEP_KINDS});
  const phase = optionalWord({value: o.phase, at: `${at}.phase`, words: STEP_PHASES});
  return {
    step: string(o.step, `${at}.step`),
    exit: number(o.exit, `${at}.exit`),
    ...(kind === undefined ? {} : {kind}),
    ...(phase === undefined ? {} : {phase}),
  };
}

interface ExperimentRecoveryOptions {
  value: unknown;
  at: string;
}

/**
 * A cell's catch-up summary. A count below zero, or more runs than release
 * commands when every run starts at one, is refused like any other field of
 * the wrong shape: no catch-up could have been counted that way.
 */
function experimentRecovery(options: ExperimentRecoveryOptions): ExperimentRecovery {
  const {value, at} = options;
  const o = object(value, at);
  const recovery = {
    runs: number(o.runs, `${at}.runs`),
    releaseCommands: number(o.releaseCommands, `${at}.releaseCommands`),
    manualCommands: number(o.manualCommands, `${at}.manualCommands`),
    converged: boolean(o.converged, `${at}.converged`),
  };
  for (const [field, count] of Object.entries(recovery)) {
    if (typeof count === 'number' && (count < 0 || !Number.isInteger(count))) {
      throw new ReportError(`${at}.${field}`, 'a count', count);
    }
  }
  if (recovery.runs > recovery.releaseCommands) {
    throw new ReportError(`${at}.runs`, `at most ${recovery.releaseCommands} runs`, recovery.runs);
  }
  return recovery;
}

interface ExperimentFieldOptions {
  value: unknown;
  at: string;
}

const RELATIONS: readonly ExperimentRelation[] = ['publish', 'build', 'none'];

/** A duration in seconds, never below zero. */
function duration(options: ExperimentFieldOptions): number {
  const {value, at} = options;
  const seconds = number(value, at);
  if (seconds < 0) {
    throw new ReportError(at, 'a duration', seconds);
  }
  return seconds;
}

/** A count of tasks or slots: a whole number, never below zero. */
function tally(options: ExperimentFieldOptions): number {
  const {value, at} = options;
  const n = number(value, at);
  if (n < 0 || !Number.isInteger(n)) {
    throw new ReportError(at, 'a count', n);
  }
  return n;
}

// The slack between figures lib/timing.py rounded to the millisecond on
// their own, and how far a release that honoured its precedence can still
// come in under its bound: two of the harness's 50 ms clock slacks. Both are
// tools/testreport's timing.go's too.
const ROUNDING = 0.002;
const CLOCK_SLACK = 0.1;
const TASKS: readonly ExperimentTaskTime['task'][] = ['build', 'publish'];

function taskStats(options: ExperimentFieldOptions): ExperimentTaskStats {
  const {value, at} = options;
  const o = object(value, at);
  return {
    count: tally({value: o.count, at: `${at}.count`}),
    seconds: duration({value: o.seconds, at: `${at}.seconds`}),
    median: duration({value: o.median, at: `${at}.median`}),
    max: duration({value: o.max, at: `${at}.max`}),
    failed: tally({value: o.failed, at: `${at}.failed`}),
  };
}

function taskTime(options: ExperimentFieldOptions): ExperimentTaskTime {
  const {value, at} = options;
  const o = object(value, at);
  const task = optionalWord({value: o.task, at: `${at}.task`, words: TASKS});
  if (task === undefined) {
    throw new ReportError(`${at}.task`, `one of ${TASKS.join(', ')}`, o.task);
  }
  return {
    package: string(o.package, `${at}.package`),
    task,
    start: number(o.start, `${at}.start`),
    seconds: duration({value: o.seconds, at: `${at}.seconds`}),
  };
}

function relationWord(options: ExperimentFieldOptions): ExperimentRelation {
  const {value, at} = options;
  const relation = optionalWord({value, at, words: RELATIONS});
  if (relation === undefined) {
    throw new ReportError(at, `one of ${RELATIONS.join(', ')}`, value);
  }
  return relation;
}

/**
 * Figures over records that did not cover the release: a shipped package
 * without a build or an accepted upload, a package outside the graph, or a
 * record crossing the release's edge. The harness refuses such a cell, and so
 * does the page.
 */
function refuseUncovered(options: ExperimentFieldOptions): void {
  const {value, at} = options;
  const o = object(value, at);
  for (const field of ['untimed', 'unknown']) {
    const names = optionalArray(o[field], `${at}.${field}`);
    if (names.length > 0) {
      throw new ReportError(`${at}.${field}`, 'no package', names);
    }
  }
  if (o.straddling !== undefined && tally({value: o.straddling, at: `${at}.straddling`}) > 0) {
    throw new ReportError(`${at}.straddling`, 'no record', o.straddling);
  }
}

/**
 * A timing cell's figures, held to what a measurement could have produced:
 * the same checks tools/testreport's validateTiming makes, so a report that
 * one accepts the other does too.
 */
function experimentTiming(options: ExperimentFieldOptions): ExperimentTiming {
  const {value, at} = options;
  const o = object(value, at);
  refuseUncovered({value, at});
  const builds = object(o.consumerBuilds, `${at}.consumerBuilds`);
  const planBuilds = object(o.planBuilds, `${at}.planBuilds`);
  const tasks = object(o.tasks, `${at}.tasks`);
  const timing: ExperimentTiming = {
    relation: relationWord({value: o.relation, at: `${at}.relation`}),
    ...(o.observedRelation === undefined || o.observedRelation === null
      ? {}
      : {observedRelation: relationWord({value: o.observedRelation, at: `${at}.observedRelation`})}),
    consumerBuilds: {
      edges: tally({value: builds.edges, at: `${at}.consumerBuilds.edges`}),
      afterBuild: tally({value: builds.afterBuild, at: `${at}.consumerBuilds.afterBuild`}),
      afterPublish: tally({value: builds.afterPublish, at: `${at}.consumerBuilds.afterPublish`}),
    },
    violations: optionalArray(o.violations, `${at}.violations`).map((edge, i) => {
      const e = object(edge, `${at}.violations[${i}]`);
      return {before: string(e.before, `${at}.violations[${i}].before`), after: string(e.after, `${at}.violations[${i}].after`)};
    }),
    planningSeconds: duration({value: o.planningSeconds, at: `${at}.planningSeconds`}),
    planBuilds: {
      count: tally({value: planBuilds.count, at: `${at}.planBuilds.count`}),
      seconds: duration({value: planBuilds.seconds, at: `${at}.planBuilds.seconds`}),
    },
    makespanSeconds: duration({value: o.makespanSeconds, at: `${at}.makespanSeconds`}),
    idleSeconds: duration({value: o.idleSeconds, at: `${at}.idleSeconds`}),
    work: duration({value: o.work, at: `${at}.work`}),
    criticalPath: duration({value: o.criticalPath, at: `${at}.criticalPath`}),
    parallelism: tally({value: o.parallelism, at: `${at}.parallelism`}),
    bound: duration({value: o.bound, at: `${at}.bound`}),
    ratio: duration({value: o.ratio, at: `${at}.ratio`}),
    criticalChain: optionalArray(o.criticalChain, `${at}.criticalChain`).map((task, i) =>
      string(task, `${at}.criticalChain[${i}]`),
    ),
    tasks: {
      build: taskStats({value: tasks.build, at: `${at}.tasks.build`}),
      publish: taskStats({value: tasks.publish, at: `${at}.tasks.publish`}),
      npmPublish: taskStats({value: tasks.npmPublish, at: `${at}.tasks.npmPublish`}),
    },
    taskTimes: optionalArray(o.taskTimes, `${at}.taskTimes`).map((task, i) =>
      taskTime({value: task, at: `${at}.taskTimes[${i}]`}),
    ),
  };
  const {build, publish} = timing.tasks;
  const checks: [boolean, string, string, unknown][] = [
    [build.count < 1 || publish.count < 1, 'tasks', 'at least one build and one publication', timing.tasks],
    [timing.parallelism < 1, 'parallelism', 'at least one slot', timing.parallelism],
    [Math.abs(build.seconds + publish.seconds - timing.work) > ROUNDING, 'work', 'its builds and publications summed', timing.work],
    [timing.taskTimes.length !== build.count + publish.count, 'taskTimes', `${build.count + publish.count} tasks`, timing.taskTimes.length],
    [timing.criticalPath > timing.work + ROUNDING, 'criticalPath', `at most the work, ${timing.work}`, timing.criticalPath],
    [timing.idleSeconds > timing.makespanSeconds + ROUNDING, 'idleSeconds', 'at most the makespan', timing.idleSeconds],
    [Math.abs(timing.bound - Math.max(timing.criticalPath, timing.work / Math.max(timing.parallelism, 1))) > ROUNDING,
      'bound', 'max(Lk, Wk / s)', timing.bound],
    [Math.abs(timing.ratio * timing.bound - timing.makespanSeconds) > ROUNDING * (1 + timing.ratio + timing.bound),
      'ratio', 'the makespan over the bound', timing.ratio],
    [timing.violations.length === 0 && timing.makespanSeconds < timing.bound - CLOCK_SLACK,
      'makespanSeconds', 'no shorter than the bound of a precedence it honoured', timing.makespanSeconds],
    [timing.consumerBuilds.afterBuild > timing.consumerBuilds.edges || timing.consumerBuilds.afterPublish > timing.consumerBuilds.edges,
      'consumerBuilds', 'no more consumer builds than edges', timing.consumerBuilds],
  ];
  for (const [refused, field, expected, got] of checks) {
    if (refused) {
      throw new ReportError(`${at}.${field}`, expected, got);
    }
  }
  return timing;
}

function experimentPlannedPackage(options: ExperimentFieldOptions): ExperimentPlannedPackage {
  const {value, at} = options;
  const o = object(value, at);
  return {name: string(o.name, `${at}.name`), version: string(o.version, `${at}.version`)};
}

function experimentPlanDifference(options: ExperimentFieldOptions): ExperimentPlanDifference {
  const {value, at} = options;
  const o = object(value, at);
  return {
    package: string(o.package, `${at}.package`),
    version: string(o.version, `${at}.version`),
    reference: string(o.reference, `${at}.reference`),
  };
}

/** A tool's plan and, where there was one to set it beside, the comparison with dispat's. */
function experimentPlan(options: ExperimentFieldOptions): ExperimentPlan {
  const {value, at} = options;
  const o = object(value, at);
  return {
    packages: optionalArray(o.packages, `${at}.packages`).map((p, i) =>
      experimentPlannedPackage({value: p, at: `${at}.packages[${i}]`}),
    ),
    told: string(o.told, `${at}.told`),
    ...(o.reference === undefined ? {} : {reference: string(o.reference, `${at}.reference`)}),
    ...(o.agrees === undefined ? {} : {agrees: boolean(o.agrees, `${at}.agrees`)}),
    differences: optionalArray(o.differences, `${at}.differences`).map((d, i) =>
      experimentPlanDifference({value: d, at: `${at}.differences[${i}]`}),
    ),
  };
}

function experimentCheck(value: unknown, at: string): ExperimentCheck {
  const o = object(value, at);
  return {check: string(o.check, `${at}.check`), ok: boolean(o.ok, `${at}.ok`)};
}

function experimentPackage(value: unknown, at: string): ExperimentPackage {
  const o = object(value, at);
  return {
    name: string(o.name, `${at}.name`),
    registry: string(o.registry, `${at}.registry`),
    state: string(o.state, `${at}.state`),
  };
}

function experimentState(value: unknown, at: string): ExperimentState {
  const o = object(value, at);
  return {
    label: string(o.label, `${at}.label`),
    // A cell that recorded no observations serialises its packages as null,
    // which is Go's nil slice and not a missing field.
    packages: optionalArray(o.packages, `${at}.packages`).map((p, i) =>
      experimentPackage(p, `${at}.packages[${i}]`),
    ),
  };
}

function experimentCell(value: unknown, at: string): ExperimentCell {
  const o = object(value, at);
  const llvm = o.llvm === undefined ? undefined : experimentLLVM({value: o.llvm, at: `${at}.llvm`});
  if (llvm !== undefined && o.passed !== (llvm.status === 'completed')) {
    throw new ReportError(`${at}.passed`, 'consistent with LLVM completion status', o.passed);
  }
  return {
    id: string(o.id, `${at}.id`),
    experiment: string(o.experiment, `${at}.experiment`),
    scenario: string(o.scenario, `${at}.scenario`),
    tool: string(o.tool, `${at}.tool`),
    dispat: string(o.dispat, `${at}.dispat`),
    platform: string(o.platform, `${at}.platform`),
    steps: optionalArray(o.steps, `${at}.steps`).map((s, i) => experimentStep(s, `${at}.steps[${i}]`)),
    checks: optionalArray(o.checks, `${at}.checks`).map((c, i) => experimentCheck(c, `${at}.checks[${i}]`)),
    passed: boolean(o.passed, `${at}.passed`),
    final: experimentState(o.final, `${at}.final`),
    // Absent from every record written before the harness measured a
    // catch-up, which is why the frozen archives still validate.
    ...(o.recovery === undefined ? {} : {recovery: experimentRecovery({value: o.recovery, at: `${at}.recovery`})}),
    // Present in the timing cells alone, so every other record validates as it did.
    ...(o.timing === undefined ? {} : {timing: experimentTiming({value: o.timing, at: `${at}.timing`})}),
    ...(o.plan === undefined ? {} : {plan: experimentPlan({value: o.plan, at: `${at}.plan`})}),
    ...(llvm === undefined ? {} : {llvm}),
    ...(o.executionMeasurement === undefined ? {} : {executionMeasurement: validateExecutionMeasurement({value: o.executionMeasurement, at: `${at}.executionMeasurement`})}),
  };
}

function experimentLLVM(options: ExperimentFieldOptions): ExperimentLLVM {
  const {value, at} = options;
  const o = object(value, at);
  const status = string(o.status, `${at}.status`);
  const mode = string(o.mode, `${at}.mode`);
  const profile = string(o.profile, `${at}.profile`);
  if (!['completed', 'failed', 'blocked', 'unsupported'].includes(status)) {
    throw new ReportError(`${at}.status`, 'completed, failed, blocked, or unsupported', status);
  }
  if (!['compilation', 'interpretation', 'hybrid', 'distributed', 'manual-gcp-split'].includes(mode)) {
    throw new ReportError(`${at}.mode`, 'a distribution mode', mode);
  }
  if (profile !== 'full' && profile !== 'smoke') {
    throw new ReportError(`${at}.profile`, 'full or smoke', profile);
  }
  const stringList = (input: unknown, field: string) => array(input, `${at}.${field}`).map((item, index) =>
    string(item, `${at}.${field}[${index}]`));
  const result: ExperimentLLVM = {
    status: status as ExperimentLLVM['status'], mode: mode as ExperimentLLVM['mode'], profile,
    llvmVersion: string(o.llvmVersion, `${at}.llvmVersion`), sourceRevision: string(o.sourceRevision, `${at}.sourceRevision`),
    topology: string(o.topology, `${at}.topology`), executorCount: number(o.executorCount, `${at}.executorCount`),
    jobsPerExecutor: number(o.jobsPerExecutor, `${at}.jobsPerExecutor`),
    ...(o.timeoutSeconds === undefined ? {} : {timeoutSeconds: number(o.timeoutSeconds, `${at}.timeoutSeconds`)}),
    buildTargets: stringList(o.buildTargets, 'buildTargets'), backends: stringList(o.backends, 'backends'),
    cache: string(o.cache, `${at}.cache`), nodes: stringList(o.nodes, 'nodes'),
    ...(o.reason === undefined ? {} : {reason: string(o.reason, `${at}.reason`)}),
    ...(o.seconds === undefined ? {} : {seconds: number(o.seconds, `${at}.seconds`)}),
    ...(o.platform === undefined ? {} : {platform: string(o.platform, `${at}.platform`)}),
    ...(o.binaryId === undefined ? {} : {binaryId: string(o.binaryId, `${at}.binaryId`)}),
    ...(o.binarySha256 === undefined ? {} : {binarySha256: string(o.binarySha256, `${at}.binarySha256`)}),
    ...(o.toolVersions === undefined ? {} : {toolVersions: Object.fromEntries(
      Object.entries(object(o.toolVersions, `${at}.toolVersions`)).map(([name, version]) => [name, string(version, `${at}.toolVersions.${name}`)]),
    )}),
    artifacts: array(o.artifacts, `${at}.artifacts`).map((item, index) => {
      const artifact = object(item, `${at}.artifacts[${index}]`);
      const parsed = {
        package: string(artifact.package, `${at}.artifacts[${index}].package`),
        path: string(artifact.path, `${at}.artifacts[${index}].path`),
        sha256: string(artifact.sha256, `${at}.artifacts[${index}].sha256`),
        bytes: number(artifact.bytes, `${at}.artifacts[${index}].bytes`),
      };
      if (!parsed.package || !parsed.path || !/^[a-f0-9]{64}$/.test(parsed.sha256) || parsed.bytes <= 0) {
        throw new ReportError(`${at}.artifacts[${index}]`, 'an identified executable with positive size and SHA-256', item);
      }
      return parsed;
    }),
  };
  if (!/^[a-f0-9]{40}$/.test(result.sourceRevision) || !result.llvmVersion || !result.topology || !result.cache ||
      result.executorCount !== 2 || !Number.isInteger(result.jobsPerExecutor) || result.jobsPerExecutor < 1 ||
      result.buildTargets.length === 0 || result.backends.length !== 2) {
    throw new ReportError(at, 'a pinned LLVM workload with two executors and positive Ninja parallelism', value);
  }
  if (result.binarySha256 !== undefined && !/^[a-f0-9]{64}$/.test(result.binarySha256)) {
    throw new ReportError(`${at}.binarySha256`, 'a Dispat binary SHA-256 digest', result.binarySha256);
  }
  if (result.timeoutSeconds !== undefined && result.timeoutSeconds <= 0) {
    throw new ReportError(`${at}.timeoutSeconds`, 'a positive timeout in seconds', result.timeoutSeconds);
  }
  if (status === 'completed') {
    const expectedTools = profile === 'full' ? ['llvm-cxxfilt', 'llvm-size', 'llc', 'opt', 'llvm-config'] : ['llvm-cxxfilt', 'llvm-size'];
    const packages = [...new Set(result.artifacts.map((artifact) => artifact.package))];
    if (result.seconds === undefined || result.seconds <= 0 || result.nodes.length !== 2 ||
        result.nodes.some((node) => !node) || new Set(result.nodes).size !== 2 || result.artifacts.length !== expectedTools.length * 2 ||
        packages.length !== 2 || packages.some((name) => expectedTools.some((tool) =>
          result.artifacts.filter((artifact) => artifact.package === name && artifact.path === tool).length !== 1))) {
      throw new ReportError(at, 'a positive duration, two distinct executors, and verified LLVM artifacts', value);
    }
  } else if (!result.reason || result.seconds !== undefined) {
    throw new ReportError(at, 'an unmeasured status with a reason and no duration', value);
  }
  if (profile === 'full' && (result.buildTargets.length !== 1 || result.buildTargets[0] !== 'all')) {
    throw new ReportError(`${at}.buildTargets`, 'all for the full LLVM profile', result.buildTargets);
  }
  return result;
}

function experiments(value: unknown, at: string): Experiments {
  const o = object(value, at);
  return {
    version: string(o.version, `${at}.version`),
    cells: optionalArray(o.cells, `${at}.cells`).map((c, i) => experimentCell(c, `${at}.cells[${i}]`)),
  };
}

/** Validates a parsed report.json, throwing at the first field that is wrong. */
export function validateReport(value: unknown): Report {
  const o = object(value, 'report');
  return {
    generatedAt: string(o.generatedAt, 'report.generatedAt'),
    commit: string(o.commit, 'report.commit'),
    coverage: coverage(o.coverage, 'report.coverage'),
    suite: suite(o.suite, 'report.suite'),
    benchmarks: benchmarks(o.benchmarks, 'report.benchmarks'),
    experiments: experiments(o.experiments, 'report.experiments'),
  };
}

/** Validates an archive and refuses a report filed under another docs version. */
export function validateArchivedReport(value: unknown, expectedVersion: string): ArchivedReport {
  const o = object(value, 'archive');
  const docsVersion = string(o.docsVersion, 'archive.docsVersion');
  if (docsVersion !== expectedVersion) {
    throw new ReportError('archive.docsVersion', JSON.stringify(expectedVersion), docsVersion);
  }
  const report = o.report === undefined ? undefined : validateReport(o.report);
  const evidence = o.evidence === undefined ? undefined : historicalEvidence(o.evidence);
  if (!report && !evidence) throw new ReportError('archive', 'a report or recovered evidence', value);
  return {docsVersion, report, evidence};
}

function historicalEvidence(value: unknown): HistoricalEvidence {
  const o = object(value, 'archive.evidence');
  const c = object(o.coverage, 'archive.evidence.coverage');
  const s = object(o.suite, 'archive.evidence.suite');
  const e = o.experiments === undefined ? undefined : object(o.experiments, 'archive.evidence.experiments');
  const result: HistoricalEvidence = {
    releaseVersion: string(o.releaseVersion, 'archive.evidence.releaseVersion'),
    generatedAt: string(o.generatedAt, 'archive.evidence.generatedAt'),
    commit: string(o.commit, 'archive.evidence.commit'),
    runUrl: string(o.runUrl, 'archive.evidence.runUrl'),
    coverageArtifactUrl: string(o.coverageArtifactUrl, 'archive.evidence.coverageArtifactUrl'),
    coverage: {
      unitPercent: number(c.unitPercent, 'archive.evidence.coverage.unitPercent'),
      integrationPercent: number(c.integrationPercent, 'archive.evidence.coverage.integrationPercent'),
      totalPercent: number(c.totalPercent, 'archive.evidence.coverage.totalPercent'),
      statements: number(c.statements, 'archive.evidence.coverage.statements'),
    },
    suite: {
      tests: number(s.tests, 'archive.evidence.suite.tests'),
      fuzz: number(s.fuzz, 'archive.evidence.suite.fuzz'),
    },
    benchmarks: number(o.benchmarks, 'archive.evidence.benchmarks'),
    experiments: e ? {
      cells: number(e.cells, 'archive.evidence.experiments.cells'),
      artifactUrl: string(e.artifactUrl, 'archive.evidence.experiments.artifactUrl'),
    } : undefined,
    experimentResults: o.experimentResults === undefined
      ? undefined
      : experiments(o.experimentResults, 'archive.evidence.experimentResults'),
  };
  if (result.experimentResults && result.experimentResults.version !== result.releaseVersion) {
    throw new ReportError(
      'archive.evidence.experimentResults.version',
      JSON.stringify(result.releaseVersion),
      result.experimentResults.version,
    );
  }
  if (result.experimentResults && result.experiments?.cells !== result.experimentResults.cells.length) {
    throw new ReportError(
      'archive.evidence.experiments.cells',
      String(result.experimentResults.cells.length),
      result.experiments?.cells,
    );
  }
  return result;
}
