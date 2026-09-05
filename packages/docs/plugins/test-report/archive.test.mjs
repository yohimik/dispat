import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import test from 'node:test';

import {resolveArchivedState} from './state.ts';
import {validateArchivedReport} from './validate.ts';

test('a late archive response cannot cross into another version', () => {
  const old = {docsVersion: '1.7', evidence: recovered('1.7.2', 2847)};
  assert.deepEqual(resolveArchivedState('1.6', true, {version: '1.7', value: old}), {
    report: null, evidence: null, status: 'loading',
  });
  assert.equal(resolveArchivedState('1.6', false, null).status, 'unavailable');
});

test('malformed and mislabeled archives fail validation', () => {
  assert.throws(() => validateArchivedReport({docsVersion: '1.6'}, '1.6'), /a report or recovered evidence/);
  assert.throws(
    () => validateArchivedReport({docsVersion: '1.7', evidence: recovered('1.7.2', 2847)}, '1.6'),
    /archive.docsVersion/,
  );
  assert.throws(() => validateArchivedReport({
    docsVersion: '1.7',
    evidence: {...recovered('1.7.2', 2847), experiments: {cells: 1, artifactUrl: 'https://example.test/e'},
      experimentResults: {version: '1.7.2', cells: []}},
  }, '1.7'), /experiments.cells/);
});

test('recovered archives retain their exact aggregate counts', async () => {
  const expected = {'1.5': [2708, 25, 95.4], '1.6': [2752, 32, 95.4], '1.7': [2847, 33, 94.7]};
  for (const [version, counts] of Object.entries(expected)) {
    const value = JSON.parse(await fs.readFile(new URL(`../../static/test-reports/${version}.json`, import.meta.url)));
    const archive = validateArchivedReport(value, version);
    assert.deepEqual(
      [archive.evidence.suite.tests, archive.evidence.benchmarks, archive.evidence.coverage.totalPercent],
      counts,
    );
    assert.equal(archive.report, undefined);
    if (version === '1.7') {
      const results = archive.evidence.experimentResults;
      assert.equal(results.version, '1.7.2');
      assert.equal(results.cells.length, 12);
      assert.deepEqual(
        results.cells.filter(({tool}) => tool === 'dispat').map(({id, passed}) => [id, passed]),
        [
          ['midrelease-clean-dispat', true],
          ['midrelease-conflict-dispat', true],
          ['orphan-dispat', true],
        ],
      );
      assert.ok(results.cells.every(({steps, checks, final}) => steps.length && checks.length && final.label));
    }
  }
});

function recovered(releaseVersion, tests) {
  return {
    releaseVersion, generatedAt: '2026-09-03T23:10:03Z', commit: 'e1ac8c514e68',
    runUrl: 'https://example.test/run', coverageArtifactUrl: 'https://example.test/coverage',
    coverage: {unitPercent: 87.7, integrationPercent: 86.3, totalPercent: 94.7, statements: 19233},
    suite: {tests, fuzz: 35}, benchmarks: 33,
  };
}
