import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { publish } from '#root/scripts/publish.js'

const execute = promisify(execFile)
interface RegistryVersion { name: string; version: string; dist: { integrity: string } }
interface RegistryDocument {
  name: string
  versions: Record<string, RegistryVersion>
  'dist-tags': Record<string, string>
}

async function registry(t: TestContext) {
  const directory = await mkdtemp(join(tmpdir(), 'dispat registry '))
  let document: RegistryDocument = { name: '@dispat/cli', versions: {}, 'dist-tags': {} }
  let loseReply = false
  let publications = 0
  const server = createServer(async (request, response) => {
    const chunks: Buffer[] = []
    for await (const chunk of request) chunks.push(Buffer.from(chunk))
    const body = Buffer.concat(chunks).toString()
    response.setHeader('content-type', 'application/json')
    const url = decodeURIComponent(request.url ?? '')
    if (request.method === 'PUT' && url.startsWith('/-/package/')) {
      document['dist-tags'][url.split('/').at(-1)!] = JSON.parse(body) as string
      response.end('{}')
    } else if (request.method === 'PUT') {
      publications++
      const incoming = JSON.parse(body) as RegistryDocument
      Object.assign(document.versions, incoming.versions)
      Object.assign(document['dist-tags'], incoming['dist-tags'])
      if (loseReply) { loseReply = false; request.socket.destroy(); return }
      response.statusCode = 201
      response.end('{"ok":true}')
    } else if (!Object.keys(document.versions).length) {
      response.statusCode = 404
      response.end('{"error":"Not found"}')
    } else response.end(JSON.stringify(document))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  const config = join(directory, 'npmrc')
  await writeFile(config, `registry=${address}\n//127.0.0.1:${(server.address() as AddressInfo).port}/:_authToken=local-fixture-only\nprovenance=false\nfetch-retries=0\n`)
  t.after(async () => {
    server.closeAllConnections()
    await new Promise<void>(resolve => server.close(() => resolve()))
    await rm(directory, { recursive: true, force: true })
  })
  const run = async (args: string[]) => execute(process.platform === 'win32' ? 'npm.cmd' : 'npm', [
    ...args.filter(value => value !== '--provenance'), '--provenance=false', '--userconfig', config
  ], { cwd: directory, timeout: 15000, env: { ...process.env, NODE_AUTH_TOKEN: '', NPM_TOKEN: '', npm_config_cache: join(directory, 'cache') } })
  async function pack(version: string) {
    await writeFile(join(directory, 'package.json'), JSON.stringify({ name: '@dispat/cli', version, description: 'Disposable registry integration fixture', files: ['README.md'] }))
    await writeFile(join(directory, 'README.md'), `Fixture ${version}`)
    const { stdout } = await run(['pack', '--ignore-scripts', '--json'])
    type PackedArtifact = { filename: string; integrity: string; name: string; version: string }
    const parsed = JSON.parse(stdout) as PackedArtifact[] | Record<string, PackedArtifact>
    const artifact = Array.isArray(parsed) ? parsed : Object.values(parsed)
    assert.equal(artifact.length, 1)
    return { tarball: join(directory, artifact[0].filename), integrity: artifact[0].integrity,
      version, packedName: artifact[0].name, packedVersion: artifact[0].version, run }
  }
  return { pack, document, get publications() { return publications }, loseNextReply() { loseReply = true } }
}

test('real npm publication verifies bytes and retries an existing release without another upload', async t => {
  const fixture = await registry(t)
  const artifact = await fixture.pack('1.10.1')
  assert.equal((await publish(artifact)).published, true)
  assert.equal(fixture.document.versions['1.10.1'].dist.integrity, artifact.integrity)
  assert.equal(fixture.document['dist-tags'].latest, '1.10.1')
  assert.equal((await publish(artifact)).published, false)
  assert.equal(fixture.publications, 1)
  await assert.rejects(publish({ ...artifact, integrity: 'sha512-conflict' }), /integrity mismatch/)
})

test('real npm recovers a lost publication response and keeps prerelease channel separate', async t => {
  const fixture = await registry(t)
  fixture.loseNextReply()
  const artifact = await fixture.pack('1.11.0-beta.0')
  assert.equal((await publish({ ...artifact, channel: 'beta' })).published, false)
  assert.equal(fixture.document['dist-tags'].beta, '1.11.0-beta.0')
  assert.equal(fixture.document['dist-tags'].latest, undefined)
  assert.equal(fixture.publications, 1)
})
