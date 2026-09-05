import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import historicalLinks, {parsePlannedModuleVersions, updateReleaseModules, updateReleaseRef} from './index.ts';

const planned = {
  ccme: 'v2.0.0', config: 'v1.0.0', manifest: 'v1.2.0', models: 'v1.8.0', scanner: 'v1.3.0', writer: 'v1.2.0',
};

test('updates one stable minor and preserves older refs', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'historical-ref-'));
  const folder = path.join(root, 'plugins/historical-links');
  await fs.mkdir(folder, {recursive: true});
  await fs.writeFile(path.join(root, 'versions.json'), '["1.8", "1.7"]\n');
  await fs.writeFile(path.join(folder, 'refs.json'), '{"1.7":"7777777777777777777777777777777777777777"}\n');
  await updateReleaseRef(root, '1.8', '8888888888888888888888888888888888888888');
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(folder, 'refs.json'), 'utf8')), {
    '1.7': '7777777777777777777777777777777777777777',
    '1.8': '8888888888888888888888888888888888888888',
  });
  assert.deepEqual((await fs.readdir(folder)).sort(), ['refs.json']);
});

test('rejects prerelease labels and abbreviated commits', async () => {
  await assert.rejects(() => updateReleaseRef('/tmp', '1.9-rc.1', '8'.repeat(40)), /stable minor/);
  await assert.rejects(() => updateReleaseRef('/tmp', '1.9', '8888888'), /full Git commit/);
});

test('rejects a minor that is not a documentation version', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'historical-ref-'));
  const folder = path.join(root, 'plugins/historical-links');
  await fs.mkdir(folder, {recursive: true});
  await fs.writeFile(path.join(root, 'versions.json'), '["1.8"]\n');
  await fs.writeFile(path.join(folder, 'refs.json'), '{}\n');
  await assert.rejects(() => updateReleaseRef(root, '1.9', '9'.repeat(40)), /not present in versions\.json/);
  assert.equal(await fs.readFile(path.join(folder, 'refs.json'), 'utf8'), '{}\n');
});

test('records a future planned module version and preserves older maps', async () => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'historical-modules-'));
  const folder = path.join(root, 'plugins/historical-links');
  await fs.mkdir(folder, {recursive: true});
  await fs.writeFile(path.join(folder, 'modules.json'), '{"1.8":{"scanner":"v1.2.0"}}\n');
  await updateReleaseModules(root, '1.9', planned);
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(folder, 'modules.json'), 'utf8')), {
    '1.8': {scanner: 'v1.2.0'},
    '1.9': planned,
  });
  assert.deepEqual((await fs.readdir(folder)).sort(), ['modules.json']);
});

test('requires one stable version for every published Go module', () => {
  assert.deepEqual(parsePlannedModuleVersions(JSON.stringify(planned)), planned);
  const {writer: _writer, ...missing} = planned;
  assert.throws(() => parsePlannedModuleVersions(JSON.stringify(missing)), /writer must be a stable version/);
  assert.throws(() => parsePlannedModuleVersions(JSON.stringify({...planned, scanner: 'v1.3.0-rc.1'})), /scanner must be a stable version/);
});

test('stable release refuses to archive without the planned module map', async () => {
  const oldVersion = process.env.DISPAT_DOCS_REPORT_VERSION;
  const oldRequired = process.env.DISPAT_DOCS_REQUIRE_REPORT;
  const oldModules = process.env.DISPAT_DOCS_MODULE_VERSIONS;
  process.env.DISPAT_DOCS_REPORT_VERSION = '1.9';
  process.env.DISPAT_DOCS_REQUIRE_REPORT = '1';
  delete process.env.DISPAT_DOCS_MODULE_VERSIONS;
  try {
    const plugin = historicalLinks({siteDir: '/tmp'});
    await assert.rejects(() => plugin.loadContent(), /DISPAT_DOCS_MODULE_VERSIONS is required/);
  } finally {
    if (oldVersion === undefined) delete process.env.DISPAT_DOCS_REPORT_VERSION; else process.env.DISPAT_DOCS_REPORT_VERSION = oldVersion;
    if (oldRequired === undefined) delete process.env.DISPAT_DOCS_REQUIRE_REPORT; else process.env.DISPAT_DOCS_REQUIRE_REPORT = oldRequired;
    if (oldModules === undefined) delete process.env.DISPAT_DOCS_MODULE_VERSIONS; else process.env.DISPAT_DOCS_MODULE_VERSIONS = oldModules;
  }
});
