import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import http, { type IncomingMessage, type ServerResponse } from 'node:http'
import type { Socket, AddressInfo } from 'node:net'
import type { InstallOptions, ReleaseMetadata } from '#root/lib/types.js'
import { install, validateMetadata, isVersion, MAX_BINARY_SIZE } from '#root/lib/install.js'
import { download } from '#root/lib/transport.js'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'

async function fixture(t: TestContext, handler: (req: IncomingMessage, res: ServerResponse, body: Buffer) => void) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'dispat resources '))
  const body = Buffer.from('fixture native executable')
  const release: ReleaseMetadata = { version: '1.10.0', tag: 'services/dispat/v1.10.0', assets: {
    'linux-x64': { name: 'dispat-linux-amd64', size: body.length, sha256: crypto.createHash('sha256').update(body).digest('hex') }
  } }
  const sockets = new Set<Socket>()
  const server = http.createServer((req, res) => handler(req, res, body))
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)) })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', () => resolve()))
  t.after(async () => {
    for (const socket of sockets) socket.destroy()
    await new Promise(resolve => server.close(resolve))
    await fs.rm(directory, { recursive: true, force: true })
  })
  return { install, download, validateMetadata, isVersion, MAX_BINARY_SIZE, directory, body, release, sockets,
    options: { packageDir: directory, release, platform: 'linux', arch: 'x64',
      baseURL: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, transportOptions: { allowHTTP: true, timeout: 500 },
      exec: async () => ({ stdout: 'dispat 1.10.0\n' }) } satisfies InstallOptions }
}

async function socketsClosed(sockets: Set<Socket>) {
  const deadline = Date.now() + 1000
  while (sockets.size && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(sockets.size, 0, 'download must release its connection')
}

test('header rejection closes a stalled response and preserves the prior binary', async t => {
  const f = await fixture(t, (_req, res) => { res.writeHead(200, { 'Content-Length': '999' }); res.flushHeaders() })
  await fs.writeFile(path.join(f.directory, 'dispat-native'), 'previous')
  await assert.rejects(f.install(f.options), /download size/)
  await socketsClosed(f.sockets)
  assert.equal(await fs.readFile(path.join(f.directory, 'dispat-native'), 'utf8'), 'previous')
  assert.deepEqual(await fs.readdir(f.directory), ['dispat-native'])
})

test('body timeout aborts the pipeline and removes partial downloads', async t => {
  const f = await fixture(t, (_req, res, body) => { res.writeHead(200, { 'Content-Length': String(body.length) }); res.write(body.subarray(0, 2)) })
  await assert.rejects(f.install({ ...f.options, transportOptions: { allowHTTP: true, timeout: 50 } }), /abort/i)
  await socketsClosed(f.sockets)
  assert.deepEqual(await fs.readdir(f.directory), [])
})

test('HTTP error releases an unfinished response', async t => {
  const f = await fixture(t, (_req, res) => { res.writeHead(404); res.flushHeaders() })
  await assert.rejects(f.install(f.options), /404/)
  await socketsClosed(f.sockets)
})

test('concurrent and repeated installation leaves one verified executable', async t => {
  let requests = 0
  const f = await fixture(t, (_req, res, body) => { requests++; res.end(body) })
  const results = await Promise.all(Array.from({ length: 4 }, () => f.install(f.options)))
  assert.equal(results.length, 4)
  assert.deepEqual(await fs.readFile(results[0].target), f.body)
  assert.deepEqual(await fs.readdir(f.directory), ['dispat-native'])
  const previousRequests = requests
  assert.equal((await f.install(f.options)).installed, false)
  assert.equal(requests, previousRequests)
})

test('metadata refuses unsafe versions, asset sizes, and names before downloading', async t => {
  const f = await fixture(t, (_req, res) => res.end())
  for (const version of ['01.2.3', '1.2.3-01', '1.2.3-', '1.2.3/a', 123]) assert.equal(f.isVersion(version), false)
  for (const version of ['1.2.3', '1.2.3-beta.0', '1.2.3+build.01']) assert.equal(f.isVersion(version), true)
  const asset = f.release.assets['linux-x64']
  for (const changed of [{ size: f.MAX_BINARY_SIZE + 1 }, { size: -1 }, { size: 1.2 }, { size: '3' }, { sha256: 123 }, { name: '../native' }]) {
    assert.throws(() => f.validateMetadata({ ...f.release, assets: { 'linux-x64': { ...asset, ...changed } } }, 'linux-x64'), /metadata/)
  }
  for (const timeout of [0, -1, Infinity, 1.5]) await assert.rejects(f.download('https://example.test', { timeout }), /timeout/)
  await assert.rejects(f.download('file:///tmp/binary', { allowHTTP: true }), /HTTPS/)
})
