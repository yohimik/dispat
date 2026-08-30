import type {
  Benchmark,
  BenchGroup,
  Benchmarks,
  Counts,
  Coverage,
  CoverageModule,
  CoveragePackage,
  FuzzTarget,
  Group,
  Report,
  Stats,
  Suite,
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

/** Validates a parsed report.json, throwing at the first field that is wrong. */
export function validateReport(value: unknown): Report {
  const o = object(value, 'report');
  return {
    generatedAt: string(o.generatedAt, 'report.generatedAt'),
    commit: string(o.commit, 'report.commit'),
    coverage: coverage(o.coverage, 'report.coverage'),
    suite: suite(o.suite, 'report.suite'),
    benchmarks: benchmarks(o.benchmarks, 'report.benchmarks'),
  };
}
