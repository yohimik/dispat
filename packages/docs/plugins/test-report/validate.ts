import type {
  Benchmark,
  BenchGroup,
  Benchmarks,
  Counts,
  Coverage,
  CoverageModule,
  CoveragePackage,
  ExperimentCell,
  ExperimentCheck,
  ExperimentPackage,
  Experiments,
  ExperimentState,
  ExperimentStep,
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

function experimentStep(value: unknown, at: string): ExperimentStep {
  const o = object(value, at);
  return {step: string(o.step, `${at}.step`), exit: number(o.exit, `${at}.exit`)};
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
  };
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
