import assert from 'node:assert/strict';
import test from 'node:test';

import {planRows, planText, secondsText, timingRows} from './timing.ts';
import {validateReport} from './validate.ts';

// The same table as tools/testreport's TestPlanOf: the page and the job
// summary must say the same thing about one record.
const two = [{name: 'cli', version: '1.0.1'}, {name: 'core', version: '1.1.0'}];
const cases = [
  ['no plan', {tool: 'lerna'}, 'not measured'],
  ['the reference', {tool: 'dispat', plan: {packages: two, differences: []}}, 'the reference, 2 packages'],
  ['no reference', {tool: 'nx', plan: {packages: two, differences: []}}, 'no dispat plan to compare'],
  ['agreement', {tool: 'nx', plan: {packages: two, agrees: true, differences: []}}, 'agrees with dispat, 2 packages'],
  ['one more package', {tool: 'lerna', plan: {packages: [], agrees: false, differences: [
    {package: 'docs', version: '1.0.1', reference: ''}, {package: 'theme', version: '1.0.1', reference: ''},
  ]}}, 'also docs 1.0.1, theme 1.0.1'],
  ['every kind', {tool: 'changesets', plan: {packages: [], agrees: false, differences: [
    {package: 'chain2', version: '', reference: '1.0.1'}, {package: 'core', version: '1.2.0', reference: '1.1.0'},
    {package: 'docs', version: '1.0.1', reference: ''}, {package: 'chain3', version: '', reference: '1.0.1'},
  ]}}, "also docs 1.0.1; not chain2 1.0.1, chain3 1.0.1; core 1.2.0 for dispat's 1.1.0"],
];

for (const [name, cell, want] of cases) {
  test(`plan text: ${name}`, () => {
    assert.equal(planText(cell), want);
  });
}

const timing = {
  relation: 'build', planningSeconds: 0.009, makespanSeconds: 1.942, work: 3.442, criticalPath: 1.399,
  parallelism: 4, bound: 1.399, ratio: 1.388, criticalChain: ['core build', 'api build', 'api publish'],
  tasks: {version: 0, build: 4, publish: 4}, publishTimedAt: {npm: 4, registry: 0},
};

function report(extra) {
  return {
    generatedAt: '2026-10-25T00:00:00Z', commit: 'abc',
    coverage: {
      total: {statements: 1, covered: 1, percent: 100}, unit: {statements: 1, covered: 1, percent: 100},
      integration: {statements: 1, covered: 1, percent: 100}, modules: [],
    },
    suite: {
      totals: {packages: 0, tests: 0, fuzz: 0, benchmarks: 0, subtests: 0, passed: 0, failed: 0, skipped: 0, elapsed: 0},
      groups: [],
    },
    benchmarks: {groups: []},
    experiments: {version: '1.11.0-rc.7', cells: [{
      id: 'timing-six-dispat', experiment: 'timing', scenario: 'six', tool: 'dispat', dispat: '1.11.0-rc.7',
      platform: 'linux_arm64', checks: [], passed: true, final: {label: 'after-release', packages: []},
      steps: [{step: 'plan', exit: 0}], ...extra,
    }]},
  };
}

test('a timing cell validates and its figures render to the millisecond', () => {
  const plan = {packages: two, reference: 'timing-six-dispat', agrees: false,
    differences: [{package: 'docs', version: '1.0.1', reference: ''}]};
  const cells = validateReport(report({timing, plan})).experiments.cells;
  assert.deepEqual(cells[0].timing, timing);
  assert.deepEqual(cells[0].plan, plan);
  assert.deepEqual(timingRows({cells}), [{
    id: 'timing-six-dispat', scenario: 'six', tool: 'dispat', relation: 'build', planning: '0.009s',
    makespan: '1.942s', work: '3.442s', criticalPath: '1.399s', parallelism: '4', bound: '1.399s', ratio: '1.39',
    criticalChain: 'core build → api build → api publish',
  }]);
  assert.deepEqual(planRows({cells}), [{id: 'timing-six-dispat', scenario: 'six', tool: 'dispat',
    text: 'the reference, 2 packages'}]);
  assert.equal(secondsText({seconds: 30.5674}), '30.567s');
});

test('a cell without figures validates without them and has no rows', () => {
  const cells = validateReport(report({})).experiments.cells;
  assert.equal(cells[0].timing, undefined);
  assert.equal(cells[0].plan, undefined);
  assert.deepEqual(timingRows({cells}), []);
  assert.deepEqual(planRows({cells}), []);
});

test('a plan without a comparison reads differences as none', () => {
  const plan = validateReport(report({plan: {packages: two, differences: null}})).experiments.cells[0].plan;
  assert.deepEqual(plan, {packages: two, differences: []});
});

test('impossible figures stop the build naming the field', () => {
  const at = 'report.experiments.cells[0].timing';
  for (const [extra, field] of [
    [{relation: 'version'}, `${at}.relation`],
    [{makespanSeconds: -1}, `${at}.makespanSeconds`],
    [{parallelism: 0}, `${at}.parallelism`],
    [{parallelism: 1.5}, `${at}.parallelism`],
    [{criticalPath: 9}, `${at}.criticalPath`],
    [{tasks: {version: 0, build: -1, publish: 4}}, `${at}.tasks.build`],
    [{criticalChain: [1]}, `${at}.criticalChain[0]`],
  ]) {
    assert.throws(() => validateReport(report({timing: {...timing, ...extra}})),
      (error) => error.message.startsWith(`${field}:`), JSON.stringify(extra));
  }
  assert.throws(() => validateReport(report({plan: {packages: [{name: 'core'}]}})),
    (error) => error.message.startsWith('report.experiments.cells[0].plan.packages[0].version:'));
});
