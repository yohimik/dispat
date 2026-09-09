import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {download} from './download.mjs';
import {expected} from './validate.ts';

function release() {
  return {tag_name: 'services/dispat/v1.9.0', draft: false, assets: [
    ...[...expected.keys()].map((name, i) => ({name, size: i + 1, digest: `sha256:${'b'.repeat(64)}`, state: 'uploaded'})),
    {name: 'unrelated.json', size: 1},
  ]};
}

test('builds docs data from exact release metadata and preserves it on invalid responses', async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'dispat-size-download-'));
  t.after(() => fs.rm(directory, {recursive: true, force: true}));
  let body = JSON.stringify(release());
  let status = 200;
  const requests = [];
  const server = http.createServer((request, response) => {
    requests.push(request.url);
    response.writeHead(status);
    response.end(body);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const base = `http://127.0.0.1:${server.address().port}`;
  const destination = path.join(directory, 'manifest.json');
  await download('1.9.0', destination, base);
  const saved = await fs.readFile(destination, 'utf8');
  const manifest = JSON.parse(saved);
  assert.equal(manifest.schemaVersion, 2);
  assert.equal(manifest.version, '1.9.0');
  assert.equal(manifest.binaries.length, 8);
  assert.deepEqual(manifest.binaries.find((b) => b.name === 'dispat-linux-amd64'), {
    name: 'dispat-linux-amd64', os: 'linux', arch: 'amd64', compiler: 'go', bytes: 1, sha256: 'b'.repeat(64),
  });
  assert.deepEqual(requests, ['/releases/tags/services%2Fdispat%2Fv1.9.0']);

  for (const [mutate, error] of [
    [(r) => {r.tag_name = 'services/dispat/v1.8.0';}, /exact published/],
    [(r) => {r.draft = true;}, /exact published/],
    [(r) => {r.assets.shift();}, /exactly 8/],
    [(r) => {r.assets[0] = r.assets[1];}, /duplicate/],
    [(r) => {r.assets[0].size = 0;}, /positive integer/],
    [(r) => {r.assets[0].digest = null;}, /SHA-256/],
    [(r) => {r.assets[0].state = 'new';}, /uploaded/],
  ]) {
    const broken = release(); mutate(broken); body = JSON.stringify(broken);
    await assert.rejects(download('1.9.0', destination, base), error);
    assert.equal(await fs.readFile(destination, 'utf8'), saved);
  }
  body = 'x'.repeat(256 * 1024 + 1);
  await assert.rejects(download('1.9.0', destination, base), /exceeds 262144 bytes/);
  status = 404;
  await assert.rejects(download('1.9.0', destination, base), /HTTP 404/);
  await assert.rejects(download('../latest', destination, base), /invalid dispat version/);
  assert.equal(await fs.readFile(destination, 'utf8'), saved);
  assert.deepEqual(await fs.readdir(directory), ['manifest.json']);
});
