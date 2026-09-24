import assert from 'node:assert/strict';
import test from 'node:test';

import {catchUpText, isSingleCommandCatchUp} from './catchup.ts';
import {validateReport} from './validate.ts';

// The same table as tools/testreport's TestCatchUpOf: the page and the job
// summary must say the same thing about one record.
const cases = [
  ['no catch-up measured', undefined, 'not measured'],
  ['nothing to do', {runs: 0, releaseCommands: 0, manualCommands: 0, converged: true}, 'none needed'],
  ['nothing done and not settled', {runs: 0, releaseCommands: 0, manualCommands: 0, converged: false},
    '0 runs, 0 commands, did not converge'],
  ['one command', {runs: 1, releaseCommands: 1, manualCommands: 0, converged: true}, '1 run, 1 command'],
  ['a push by hand', {runs: 1, releaseCommands: 1, manualCommands: 1, converged: true}, '1 run, 2 commands, 1 manual'],
  ['a failed recovery and a cleanup', {runs: 2, releaseCommands: 2, manualCommands: 1, converged: true},
    '2 runs, 3 commands, 1 manual'],
  ['two commands in one run', {runs: 1, releaseCommands: 2, manualCommands: 0, converged: true}, '1 run, 2 commands'],
  ['unsettled', {runs: 1, releaseCommands: 1, manualCommands: 2, converged: false},
    '1 run, 3 commands, 2 manual, did not converge'],
];

for (const [name, recovery, want] of cases) {
  test(`catch-up text: ${name}`, () => {
    assert.equal(catchUpText({recovery}), want);
  });
}

test('a single-command catch-up is one run of one command, with no manual step, that converged', () => {
  assert.equal(isSingleCommandCatchUp({recovery: {runs: 1, releaseCommands: 1, manualCommands: 0, converged: true}}), true);
  assert.equal(isSingleCommandCatchUp({}), false);
  for (const recovery of [
    {runs: 1, releaseCommands: 1, manualCommands: 1, converged: true},
    {runs: 2, releaseCommands: 2, manualCommands: 0, converged: true},
    {runs: 1, releaseCommands: 2, manualCommands: 0, converged: true},
    {runs: 1, releaseCommands: 1, manualCommands: 0, converged: false},
    {runs: 0, releaseCommands: 0, manualCommands: 0, converged: true},
  ]) {
    assert.equal(isSingleCommandCatchUp({recovery}), false, JSON.stringify(recovery));
  }
});

function report(cell) {
  return {
    generatedAt: '2026-09-24T00:00:00Z', commit: 'abc',
    coverage: {
      total: {statements: 1, covered: 1, percent: 100}, unit: {statements: 1, covered: 1, percent: 100},
      integration: {statements: 1, covered: 1, percent: 100}, modules: [],
    },
    suite: {
      totals: {packages: 0, tests: 0, fuzz: 0, benchmarks: 0, subtests: 0, passed: 0, failed: 0, skipped: 0, elapsed: 0},
      groups: [],
    },
    benchmarks: {groups: []},
    experiments: {version: '1.11.0-rc.6', cells: [cell]},
  };
}

function cell(extra) {
  return {
    id: 'propagation-build-dispat', experiment: 'propagation', scenario: 'build', tool: 'dispat', dispat: '1.11.0-rc.6',
    platform: 'linux_amd64', checks: [], passed: true, final: {label: 'after-recovery', packages: []},
    steps: [{step: 'release1', exit: 1}],
    ...extra,
  };
}

test('typed steps and a catch-up validate and survive as written', () => {
  const recovery = {runs: 1, releaseCommands: 1, manualCommands: 0, converged: true};
  const steps = [
    {step: 'release1', exit: 1, kind: 'release', phase: 'initial'},
    {step: 'retry-plan', exit: 0, kind: 'query', phase: 'catch-up'},
  ];
  const validated = validateReport(report(cell({steps, recovery}))).experiments.cells[0];
  assert.deepEqual(validated.steps, steps);
  assert.deepEqual(validated.recovery, recovery);
});

test('a record from before the catch-up validates without one', () => {
  const validated = validateReport(report(cell({}))).experiments.cells[0];
  assert.equal(validated.recovery, undefined);
  assert.deepEqual(validated.steps, [{step: 'release1', exit: 1}]);
  assert.equal(catchUpText(validated), 'not measured');
});

test('a malformed step or catch-up stops the build naming the field', () => {
  const at = 'report.experiments.cells[0]';
  for (const [extra, field] of [
    [{steps: [{step: 'release1', exit: 1, kind: 'publish'}]}, `${at}.steps[0].kind`],
    [{steps: [{step: 'release1', exit: 1, phase: 'recovery'}]}, `${at}.steps[0].phase`],
    [{recovery: 'one run'}, `${at}.recovery`],
    [{recovery: {runs: '1', releaseCommands: 1, manualCommands: 0, converged: true}}, `${at}.recovery.runs`],
    [{recovery: {runs: 1, releaseCommands: 1, manualCommands: -1, converged: true}}, `${at}.recovery.manualCommands`],
    [{recovery: {runs: 1, releaseCommands: 1.5, manualCommands: 0, converged: true}}, `${at}.recovery.releaseCommands`],
    [{recovery: {runs: 2, releaseCommands: 1, manualCommands: 0, converged: true}}, `${at}.recovery.runs`],
    [{recovery: {runs: 1, releaseCommands: 1, manualCommands: 0}}, `${at}.recovery.converged`],
  ]) {
    assert.throws(() => validateReport(report(cell(extra))), (error) => error.message.startsWith(`${field}:`),
      JSON.stringify(extra));
  }
});
