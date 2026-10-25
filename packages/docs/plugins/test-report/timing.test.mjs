import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';

import {finalStateEntries, planRows, planText, secondsText, taskLists, taskRows, timingRows} from './timing.ts';
import {validateReport} from './validate.ts';

// The same list tools/testreport's TestTheTextsTheSiteWritesToo reads: the
// page and the job summary must say the same thing about one record.
const texts = JSON.parse(readFileSync(new URL('./testdata/texts.json', import.meta.url), 'utf8'));

for (const {name, cell, want} of texts.plans) {
  test(`plan text: ${name}`, () => {
    assert.equal(planText(cell), want);
  });
}

for (const {name, packages, want} of texts.states) {
  test(`final state: ${name}`, () => {
    assert.equal(finalStateEntries({packages}).join(' '), want);
  });
}

const taskTimes = ['build', 'publish'].flatMap((task) =>
  ['core', 'api', 'cli', 'ui'].map((p) => ({package: p, task, start: 0.1, seconds: 0.5})),
);

const timing = {
  relation: 'build', observedRelation: 'build', consumerBuilds: {edges: 3, afterBuild: 3, afterPublish: 0},
  violations: [], planningSeconds: 0.005, planBuilds: {count: 0, seconds: 0}, makespanSeconds: 1.661,
  idleSeconds: 0.448, work: 2.663, criticalPath: 1.204, parallelism: 4, bound: 1.204, ratio: 1.379,
  criticalChain: ['core build', 'ui build', 'ui publish'],
  tasks: {
    build: {count: 4, seconds: 2.009, median: 0.502, max: 0.503, failed: 0},
    publish: {count: 4, seconds: 0.654, median: 0.199, max: 0.199, failed: 0},
    npmPublish: {count: 4, seconds: 1.401, median: 0.403, max: 0.406, failed: 0},
  },
  taskTimes,
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

test('a timing cell validates and its figures and tasks render to the millisecond', () => {
  const plan = {packages: [{name: 'cli', version: '1.0.1'}, {name: 'core', version: '1.1.0'}], told: 'feat(core)^: streaming reader',
    reference: 'timing-six-dispat', agrees: false, differences: [{package: 'docs', version: '1.0.1', reference: ''}]};
  const cells = validateReport(report({timing: {...timing, untimed: null, unknown: null, straddling: 0}, plan})).experiments.cells;
  assert.deepEqual(cells[0].timing, timing);
  assert.deepEqual(cells[0].plan, plan);
  assert.deepEqual(timingRows({cells}), [{
    id: 'timing-six-dispat', scenario: 'six', tool: 'dispat', relation: 'build', planning: '0.005s',
    makespan: '1.661s', idle: '0.448s', work: '2.663s', criticalPath: '1.204s', parallelism: '4', bound: '1.204s',
    ratio: '1.38', notHonoured: 'none', criticalChain: 'core build → ui build → ui publish',
  }]);
  assert.deepEqual(taskRows({cells}), [{
    id: 'timing-six-dispat', scenario: 'six', tool: 'dispat', build: '4, median 0.502s, max 0.503s, total 2.009s',
    publish: '4, median 0.199s, max 0.199s, total 0.654s', npmPublish: '4, median 0.403s, max 0.406s, total 1.401s',
    planBuilds: 'none',
  }]);
  assert.deepEqual(taskLists({cells})[0].tasks[0], {package: 'core', task: 'build', start: '+0.100s', seconds: '0.500s'});
  assert.deepEqual(planRows({cells}), [{id: 'timing-six-dispat', scenario: 'six', tool: 'dispat',
    told: 'feat(core)^: streaming reader', text: 'the reference: cli at 1.0.1; core at 1.1.0'}]);
  assert.equal(secondsText({seconds: 30.5674}), '30.567s');
});

test('a relation the records contradict and an edge not honoured are rendered', () => {
  const broken = {...timing, observedRelation: 'none', makespanSeconds: 1.0, ratio: 0.831, idleSeconds: 0.1,
    violations: [{before: 'core build', after: 'ui build'}],
    tasks: {...timing.tasks, publish: {...timing.tasks.publish, failed: 1}}, planBuilds: {count: 6, seconds: 3.017}};
  const cells = validateReport(report({timing: broken})).experiments.cells;
  const [row] = timingRows({cells});
  assert.equal(row.relation, 'build (records: none)');
  assert.equal(row.notHonoured, 'core build > ui build');
  const [tasks] = taskRows({cells});
  assert.equal(tasks.publish, '4, median 0.199s, max 0.199s, total 0.654s, 1 failed');
  assert.equal(tasks.planBuilds, '6, total 3.017s');
});

test('a cell without figures validates without them and has no rows', () => {
  const cells = validateReport(report({})).experiments.cells;
  assert.equal(cells[0].timing, undefined);
  assert.equal(cells[0].plan, undefined);
  assert.deepEqual(timingRows({cells}), []);
  assert.deepEqual(taskRows({cells}), []);
  assert.deepEqual(planRows({cells}), []);
});

test('a plan without a comparison reads differences as none', () => {
  const plan = validateReport(report({plan: {packages: [], told: '"core": minor', differences: null}})).experiments.cells[0].plan;
  assert.deepEqual(plan, {packages: [], told: '"core": minor', differences: []});
});

test('impossible figures, and figures over records that did not cover the release, stop the build naming the field', () => {
  const at = 'report.experiments.cells[0].timing';
  for (const [extra, field] of [
    [{relation: 'version'}, `${at}.relation`],
    [{observedRelation: 'sometimes'}, `${at}.observedRelation`],
    [{ratio: null}, `${at}.ratio`],
    [{makespanSeconds: -1}, `${at}.makespanSeconds`],
    [{parallelism: 0}, `${at}.parallelism`],
    [{parallelism: 1.5}, `${at}.parallelism`],
    [{criticalPath: 9}, `${at}.criticalPath`],
    [{bound: 1, ratio: 1.661}, `${at}.bound`],
    [{ratio: 2}, `${at}.ratio`],
    [{work: 4}, `${at}.work`],
    [{taskTimes: taskTimes.slice(1)}, `${at}.taskTimes`],
    [{taskTimes: [{...taskTimes[0], task: 'version'}, ...taskTimes.slice(1)]}, `${at}.taskTimes[0].task`],
    [{idleSeconds: 2}, `${at}.idleSeconds`],
    [{makespanSeconds: 1.0, ratio: 0.831}, `${at}.makespanSeconds`],
    [{tasks: {...timing.tasks, build: {...timing.tasks.build, count: 0}}}, `${at}.tasks`],
    [{tasks: {...timing.tasks, build: {...timing.tasks.build, count: -1}}}, `${at}.tasks.build.count`],
    [{consumerBuilds: {edges: 3, afterBuild: 4, afterPublish: 0}}, `${at}.consumerBuilds`],
    [{untimed: ['cli']}, `${at}.untimed`],
    [{unknown: ['fixture']}, `${at}.unknown`],
    [{straddling: 1}, `${at}.straddling`],
    [{criticalChain: [1]}, `${at}.criticalChain[0]`],
  ]) {
    assert.throws(() => validateReport(report({timing: {...timing, ...extra}})),
      (error) => error.message.startsWith(`${field}:`), JSON.stringify(extra));
  }
  assert.throws(() => validateReport(report({plan: {packages: [{name: 'core'}], told: ''}})),
    (error) => error.message.startsWith('report.experiments.cells[0].plan.packages[0].version:'));
  assert.throws(() => validateReport(report({plan: {packages: []}})),
    (error) => error.message.startsWith('report.experiments.cells[0].plan.told:'));
});
