import type {Counts, Coverage, CoverageModule, CoveragePackage, Group, Report, Stats, Suite} from './types';

// A shape check over the report before any page reads it.
//
// The file is produced by another program in another language and arrives over
// a CI artifact, so "it is the right shape" is an assumption rather than a
// fact. Without this, a renamed field renders as `undefined%` on a published
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
    subtests: number(o.subtests, `${at}.subtests`),
    passed: number(o.passed, `${at}.passed`),
    failed: number(o.failed, `${at}.failed`),
    skipped: number(o.skipped, `${at}.skipped`),
    elapsed: number(o.elapsed, `${at}.elapsed`),
  };
}

function group(value: unknown, at: string): Group {
  const o = object(value, at);
  return {
    ...counts(value, at),
    id: string(o.id, `${at}.id`),
    path: string(o.path, `${at}.path`),
    race: boolean(o.race, `${at}.race`),
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
  };
}
