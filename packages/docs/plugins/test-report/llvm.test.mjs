import assert from 'node:assert/strict';
import test from 'node:test';
import {llvmRows, llvmElapsed, llvmStatus} from './llvm.ts';
import {validateReport} from './validate.ts';

const result = {
  status: 'completed', mode: 'hybrid', profile: 'full', llvmVersion: '20.1.8', sourceRevision: 'a'.repeat(40),
  topology: 'two executor processes, isolated checkouts, one host', executorCount: 2, jobsPerExecutor: 2, timeoutSeconds: 2700,
  buildTargets: ['all'], backends: ['X86', 'AArch64'], cache: 'cold', seconds: 123.456,
  nodes: ['node-1', 'node-2'], artifacts: ['x86', 'arm'].flatMap((name) =>
    ['llvm-cxxfilt', 'llvm-size', 'llc', 'opt', 'llvm-config'].map((tool) => ({package: name, path: tool, sha256: 'b'.repeat(64), bytes: 123}))),
};

function report(llvm, passed = llvm.status === 'completed') {
  return {
    generatedAt: '2026-10-29T00:00:00Z', commit: 'abc',
    coverage: {
      total: {statements: 1, covered: 1, percent: 100}, unit: {statements: 1, covered: 1, percent: 100},
      integration: {statements: 1, covered: 1, percent: 100}, modules: [],
    },
    suite: {
      totals: {packages: 0, tests: 0, fuzz: 0, benchmarks: 0, subtests: 0, passed: 0, failed: 0, skipped: 0, elapsed: 0}, groups: [],
    },
    benchmarks: {groups: []},
    experiments: {version: '1.11.0-rc.10', cells: [{
      id: 'llvm-full-hybrid-dispat', experiment: 'llvm', scenario: 'full-hybrid', tool: 'dispat', dispat: '1.11.0-rc.10',
      platform: 'linux_arm64', checks: [], passed, final: {label: llvm.status, packages: []}, steps: [], llvm,
    }]},
  };
}

test('LLVM rows preserve full workload, source, executor budget, and measured duration', () => {
  const cells = validateReport(report(result)).experiments.cells;
  assert.deepEqual(cells[0].llvm, result);
  assert.equal(llvmRows({cells})[0].elapsed, '123.456s');
  assert.equal(llvmRows({cells})[0].capacity, '2 × 2');
  assert.equal(llvmRows({cells})[0].targets, 'all');
  assert.equal(llvmRows({cells})[0].source, result.sourceRevision);
});

test('unavailable distributed competitors have an explanation and no timing', () => {
  for (const status of ['failed', 'blocked', 'unsupported']) {
    const unavailable = {...result, status, reason: 'not available', seconds: undefined, nodes: [], artifacts: []};
    const cells = validateReport(report(unavailable)).experiments.cells;
    assert.equal(llvmRows({cells})[0].elapsed, 'not measured');
    assert.equal(llvmStatus({result: unavailable}), `${status}: not available`);
    assert.equal(llvmElapsed({result: unavailable}), 'not measured');
  }
  assert.deepEqual(llvmRows({cells: [{id: 'legacy'}]}), []);
});

test('incomplete, fabricated, or contradictory LLVM results fail report validation', () => {
  for (const invalid of [
    {...result, seconds: undefined}, {...result, seconds: 0}, {...result, seconds: Infinity},
    {...result, nodes: ['node-1', 'node-1']}, {...result, artifacts: []}, {...result, sourceRevision: 'main'},
    {...result, status: 'blocked', reason: 'missing credentials'}, {...result, status: 'unsupported', seconds: undefined},
    {...result, jobsPerExecutor: 1.5}, {...result, mode: 'local'}, {...result, profile: 'quick'},
    {...result, timeoutSeconds: 0}, {...result, timeoutSeconds: -1},
    {...result, artifacts: [{...result.artifacts[0], sha256: 'bad'}]},
  ]) {
    assert.throws(() => validateReport(report(invalid)), /llvm/);
  }
  assert.throws(() => validateReport(report(result, false)), /passed/);
});
