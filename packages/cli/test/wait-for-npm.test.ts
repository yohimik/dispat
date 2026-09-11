import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { spawnSync } from 'node:child_process'
import { waitForNpm } from '#root/scripts/wait-for-npm.js'

const version = '1.10.0'
const release = { name: '@dispat/bin', version, dist: { tarball: 'https://registry.npmjs.org/@dispat/bin/-/bin-1.10.0.tgz', integrity: 'sha512-fixture' } }
const ready = { name: '@dispat/bin', versions: { [version]: release } }

test('npm readiness retries missing metadata and transient errors until the exact version is complete', async () => {
  const responses: (Response | Error)[] = [
    new Response('not found', { status: 404 }),
    new Response(null, { status: 429 }),
    new Response('unavailable', { status: 503 }),
    new TypeError('connection reset'),
    new DOMException('request timed out', 'TimeoutError'),
    new DOMException('aborted', 'AbortError'),
    Response.json({ name: '@dispat/bin', versions: { '1.9.0': release } }),
    Response.json({ name: '@dispat/bin', versions: { [version]: { name: '@dispat/bin', version } } }),
    Response.json({ ...ready, name: 'wrong-package' }),
    Response.json({ name: '@dispat/bin', versions: { [version]: { ...release, name: 'wrong-package' } } }),
    Response.json({ name: '@dispat/bin', versions: { [version]: { ...release, version: '1.9.0' } } }),
    Response.json({ name: '@dispat/bin', versions: { [version]: { ...release, dist: { tarball: release.dist.tarball } } } }),
    Response.json(ready)
  ]
  let clock = 0
  const messages: string[] = []
  await waitForNpm(version, {
    now: () => clock,
    sleep: async ms => { assert.equal(ms, 10_000); clock += ms },
    log: message => messages.push(message),
    fetch: async (url, options) => {
      assert.equal(url, 'https://registry.npmjs.org/@dispat%2fbin')
      assert.equal(options?.cache, 'no-store')
      assert.equal(new Headers(options?.headers).get('accept'), 'application/vnd.npm.install-v1+json')
      assert.ok(options?.signal)
      const next = responses.shift()!
      if (next instanceof Error) throw next
      return next
    }
  })
  assert.equal(responses.length, 0)
  assert.equal(clock, 120_000)
  assert.match(messages.at(-1)!, /1\.10\.0 is available/)
})

test('npm readiness stops at its deadline instead of waiting indefinitely', async () => {
  let clock = 0
  let requests = 0
  await assert.rejects(waitForNpm(version, {
    timeoutMs: 25, intervalMs: 10, now: () => clock,
    sleep: async ms => { clock += ms }, log: () => {},
    fetch: async () => { requests++; return Response.json({}) }
  }), /Timed out.*after 25ms/)
  assert.equal(clock, 25)
  assert.equal(requests, 3)
  await assert.rejects(waitForNpm(version, {
    timeoutMs: 10, now: () => clock, log: () => {},
    fetch: async () => { clock += 10; return Response.json(null) }
  }), /Timed out/)
})

test('npm readiness rejects permanent failures and malformed input without retrying', async () => {
  for (const value of [undefined, '', 'latest', '^1.10.1', '1.10']) {
    await assert.rejects(waitForNpm(value), /exact npm version/)
  }
  for (const value of [0, -1, 1.5, Infinity]) {
    await assert.rejects(waitForNpm(version, { timeoutMs: value }), /positive integers/)
  }
  for (const status of [400, 401, 403]) {
    await assert.rejects(waitForNpm(version, {
      fetch: async () => new Response('refused', { status }),
      sleep: async () => assert.fail('must not retry')
    }), new RegExp(`HTTP ${status}`))
  }
  for (const error of [new Error('unexpected'), new DOMException('invalid', 'InvalidStateError')]) {
    await assert.rejects(waitForNpm(version, { fetch: async () => { throw error } }), error)
  }
  await assert.rejects(waitForNpm(version, { fetch: async () => new Response('{') }), SyntaxError)
})

test('npm readiness bounds stalled response headers and bodies with real HTTP requests', async t => {
  let requests = 0
  const server = createServer((_req, res) => {
    requests++
    if (requests > 1) {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.write('{"name":')
    }
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  const address = server.address()
  assert.ok(address && typeof address !== 'string')
  const started = Date.now()
  await assert.rejects(waitForNpm(version, {
    timeoutMs: 250, requestTimeoutMs: 40, intervalMs: 1, log: () => {},
    fetch: (_url, options) => fetch(`http://127.0.0.1:${address.port}`, options)
  }), /Timed out waiting/)
  assert.ok(requests >= 2)
  assert.ok(Date.now() - started < 5000)
})

test('post-release workflow can run the TypeScript source without compiling or installing dependencies', () => {
  const script = `
    import { waitForNpm } from './src/scripts/wait-for-npm.ts'
    globalThis.fetch = async () => Response.json(${JSON.stringify(ready)})
    await waitForNpm('${version}')
  `
  const result = spawnSync(process.execPath, ['--experimental-strip-types', '--input-type=module', '-e', script], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.stdout, /1\.10\.0 is available/)
})
