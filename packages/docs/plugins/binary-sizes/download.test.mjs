import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {download} from './download.mjs';

test('downloads atomically and rejects a response over the bound', async (t) => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'dispat-size-download-'));
  t.after(() => fs.rm(directory, {recursive: true, force: true}));
  let body = '{"schemaVersion":1}';
  const server = http.createServer((_request, response) => response.end(body));
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise((resolve) => server.close(resolve)));
  const address = server.address();
  const base = `http://127.0.0.1:${address.port}`;
  const destination = path.join(directory, 'manifest.json');
  await download('1.9.0', destination, base);
  assert.equal(await fs.readFile(destination, 'utf8'), body);

  body = 'x'.repeat(64 * 1024 + 1);
  await assert.rejects(download('1.9.0', destination, base), /exceeds 65536 bytes/);
  assert.equal(await fs.readFile(destination, 'utf8'), '{"schemaVersion":1}');
  assert.deepEqual((await fs.readdir(directory)).sort(), ['manifest.json']);
});
