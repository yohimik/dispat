import assert from 'node:assert/strict';
import test from 'node:test';
import {kernelRows} from './kernel.ts';
import {validateReport} from './validate.ts';

const architectures = ['x86_64', 'arm64'];
const fullArtifacts = ['vmlinux.stripped', 'boot-image', '.config', 'Module.symvers', 'modules.order', 'modules-manifest.json', 'sample-module.ko.stripped'];

function result(profile = 'full') {
  const names = profile === 'full' ? fullArtifacts : fullArtifacts.slice(0, 3);
  return {
    status: 'completed', mode: 'manual-gcp-split', profile, kernelVersion: '6.18.59', sourceRevision: 'a'.repeat(40),
    topology: 'two separately identified GCE VMs', executorCount: 2, jobsPerExecutor: 2, timeoutSeconds: 14400,
    buildTargets: profile === 'full' ? ['vmlinux', 'modules', 'architecture-boot-image'] : ['vmlinux', 'architecture-boot-image'],
    backends: architectures, cache: 'fresh output trees; OS page cache unmeasured', seconds: 123.456,
    nodes: ['gce:1001', 'gce:1002'], configuration: profile === 'full' ? 'allmodconfig' : 'defconfig',
    omissions: profile === 'full' ? [] : ['modules'],
    configDigests: {x86_64: 'b'.repeat(64), arm64: 'c'.repeat(64)},
    moduleCounts: profile === 'full' ? {x86_64: 42, arm64: 43} : {x86_64: 0, arm64: 0},
    artifacts: architectures.flatMap((architecture) => names.map((path) => ({
      package: `kernel-${architecture}`, path, sha256: (architecture === 'x86_64' ? 'b' : 'c').repeat(64), bytes: 123,
    }))),
  };
}

function report(linuxKernel, passed = linuxKernel.status === 'completed') {
  return {
    generatedAt: '2026-10-29T00:00:00Z', commit: 'synthetic-test-fixture',
    coverage: {
      total: {statements: 0, covered: 0, percent: 0}, unit: {statements: 0, covered: 0, percent: 0},
      integration: {statements: 0, covered: 0, percent: 0}, modules: [],
    },
    suite: {totals: {packages: 0, tests: 0, fuzz: 0, benchmarks: 0, subtests: 0, passed: 0, failed: 0, skipped: 0, elapsed: 0}, groups: []},
    benchmarks: {groups: []},
    experiments: {version: 'candidate', cells: [{
      id: 'kernel-test-fixture', experiment: 'linux-kernel', scenario: `${linuxKernel.profile}-manual-gcp-split`, tool: 'nx',
      dispat: 'candidate', platform: 'linux_amd64', checks: [], passed, final: {label: linuxKernel.status, packages: []}, steps: [], linuxKernel,
    }]},
  };
}

test('kernel full and smoke preserve their own configuration, module counts and targets', () => {
  for (const profile of ['full', 'smoke']) {
    const kernel = result(profile);
    const cells = validateReport(report(kernel)).experiments.cells;
    assert.deepEqual(cells[0].linuxKernel, kernel);
    assert.equal(cells[0].llvm, undefined);
    const row = kernelRows({cells})[0];
    assert.equal(row.elapsed, '123.456s');
    assert.equal(row.capacity, '2 × 2');
    assert.equal(row.configuration, kernel.configuration);
    assert.equal(row.modules, profile === 'full' ? 'x86_64: 42; arm64: 43' : 'x86_64: 0; arm64: 0');
    assert.deepEqual(row.record.configDigests, kernel.configDigests);
  }
  assert.deepEqual(kernelRows({cells: [{id: 'legacy'}]}), []);
});

test('kernel failures keep absent observations unavailable rather than inventing completion times or module counts', () => {
  for (const status of ['failed', 'blocked', 'unsupported']) {
    const kernel = {...result(), status, reason: 'no completed build', seconds: undefined, nodes: [], artifacts: [], configDigests: {}, moduleCounts: {}};
    const row = kernelRows({cells: validateReport(report(kernel)).experiments.cells})[0];
    assert.equal(row.elapsed, 'not measured');
    assert.equal(row.modules, 'x86_64: unknown; arm64: unknown');
    assert.equal(row.status, `${status}: no completed build`);
  }
  const partial = {...result(), status: 'failed', reason: 'second backend failed', seconds: undefined, nodes: [], artifacts: [],
    configDigests: {x86_64: 'b'.repeat(64)}, moduleCounts: {x86_64: 42}};
  const partialRow = kernelRows({cells: validateReport(report(partial)).experiments.cells})[0];
  assert.equal(partialRow.modules, 'x86_64: 42; arm64: unknown');
  assert.equal(partialRow.elapsed, 'not measured');
});

test('kernel completion rejects partial builds, profile drift and inconsistent configuration evidence', () => {
  const kernel = result();
  for (const invalid of [
    {...kernel, seconds: undefined}, {...kernel, seconds: 0}, {...kernel, nodes: ['same', 'same']},
    {...kernel, artifacts: kernel.artifacts.slice(1)}, {...kernel, artifacts: [...kernel.artifacts.slice(1), kernel.artifacts[1]]},
    {...kernel, configDigests: {}}, {...kernel, configDigests: {...kernel.configDigests, x86_64: 'd'.repeat(64)}},
    {...kernel, configDigests: {...kernel.configDigests, mips: 'e'.repeat(64)}}, {...kernel, moduleCounts: {}},
    {...kernel, moduleCounts: {x86_64: 0, arm64: 43}}, {...kernel, moduleCounts: {x86_64: 1.5, arm64: 43}},
    {...kernel, buildTargets: ['vmlinux']}, {...kernel, backends: ['x86_64', 'x86_64']}, {...kernel, configuration: 'defconfig'},
    {...result('smoke'), moduleCounts: {x86_64: 1, arm64: 0}}, {...kernel, status: 'failed', reason: 'failure'},
    {...kernel, llvmVersion: null}, {...kernel, LLVMVersion: '20.1.8'}, {...kernel, omissions: ['']},
    {...result('smoke'), omissions: []},
  ]) assert.throws(() => validateReport(report(invalid)), /linuxKernel/);
  assert.throws(() => validateReport(report(kernel, false)), /passed/);
  const wrongNamespace = report(kernel);
  wrongNamespace.experiments.cells[0].experiment = 'llvm';
  assert.throws(() => validateReport(wrongNamespace), /payload/);
  const missingPayload = report(kernel);
  delete missingPayload.experiments.cells[0].linuxKernel;
  assert.throws(() => validateReport(missingPayload), /payload/);
});
