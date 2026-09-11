import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { chmod, mkdtemp, writeFile, rm } from 'node:fs/promises'
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
  let publications = 0
  let metadataUnavailable = false
  let rejectPublication = false
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
      if (rejectPublication) {
        rejectPublication = false
        response.statusCode = 503
        response.end('{"error":"publication unavailable"}')
        return
      }
      publications++
      const incoming = JSON.parse(body) as RegistryDocument
      Object.assign(document.versions, incoming.versions)
      Object.assign(document['dist-tags'], incoming['dist-tags'])
      response.statusCode = 201
      response.end('{"ok":true}')
    } else if (metadataUnavailable || !Object.keys(document.versions).length) {
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
    ...args.filter(value => value !== '--provenance'), '--provenance=false', '--userconfig', config, '--registry', address
  ], { cwd: directory, timeout: 15000, env: { ...process.env, NODE_AUTH_TOKEN: '', NPM_TOKEN: '', npm_config_cache: join(directory, 'cache') } })
  async function pack(version: string) {
    await writeFile(join(directory, 'package.json'), JSON.stringify({
      name: '@dispat/cli', version, description: 'Disposable registry integration fixture', files: ['README.md'],
      publishConfig: { access:'public', provenance:false }
    }))
    await writeFile(join(directory, 'README.md'), `Fixture ${version}`)
    const { stdout } = await run(['pack', '--ignore-scripts', '--json'])
    type PackedArtifact = { filename: string; integrity: string; name: string; version: string }
    const parsed = JSON.parse(stdout) as PackedArtifact[] | Record<string, PackedArtifact>
    const artifact = Array.isArray(parsed) ? parsed : Object.values(parsed)
    assert.equal(artifact.length, 1)
    return { tarball: join(directory, artifact[0].filename), run }
  }
  return {
    pack, document, config, directory, address,
    get publications() { return publications },
    hideMetadata() { metadataUnavailable = true },
    rejectNext() { rejectPublication = true }
  }
}

test('real npm publish succeeds once without publisher registry reconciliation', async t => {
  const fixture = await registry(t)
  const artifact = await fixture.pack('1.10.1')
  const commands: string[][] = []
  await publish({ tarball:artifact.tarball, run:async args => { commands.push(args); return artifact.run(args) } })
  assert.equal(commands.length, 1)
  assert.equal(commands[0][0], 'publish')
  assert.equal(fixture.document['dist-tags'].latest, '1.10.1')
  assert.equal(fixture.publications, 1)
})

test('publish entry point follows npm success and failure without registry reconciliation', async t => {
  const fixture = await registry(t)
  const artifact = await fixture.pack('1.10.2')
  fixture.hideMetadata()
  const shim = await mkdtemp(join(tmpdir(), 'dispat npm shim '))
  t.after(() => rm(shim, { recursive:true, force:true }))
  const script = join(shim, 'npm-shim.cjs')
  await writeFile(script, [
    "const { spawnSync } = require('node:child_process')",
    "const args = process.argv.slice(2).filter(value => value !== '--provenance')",
    "const command = process.platform === 'win32' ? 'npm.cmd' : 'npm'",
    "const result = spawnSync(command, args, { stdio:'inherit', env:{ ...process.env, PATH:process.env.REAL_NPM_PATH } })",
    "if (result.error) throw result.error",
    "process.exit(result.status ?? 1)"
  ].join('\n'))
  if (process.platform === 'win32') {
    await writeFile(join(shim, 'npm.cmd'), `@"${process.execPath}" "${script}" %*\r\n`)
  } else {
    await writeFile(join(shim, 'npm'), `#!/bin/sh\nexec "${process.execPath}" "${script}" "$@"\n`)
    await chmod(join(shim, 'npm'), 0o755)
  }
  const command = [process.execPath, [join(process.cwd(), 'build/scripts/publish.js')]] as const
  const env = {
      ...process.env,
      DISPAT_CHANNEL: 'stable',
      NODE_AUTH_TOKEN: '',
      NPM_TOKEN: '',
      npm_config_cache: join(fixture.directory, 'cache'),
      npm_config_provenance: 'false',
      npm_config_registry: fixture.address,
      npm_config_userconfig: fixture.config,
      REAL_NPM_PATH: process.env.PATH,
      PATH: `${shim}${process.platform === 'win32' ? ';' : ':'}${process.env.PATH}`
  }
  const result = await execute(command[0], command[1], {
    cwd: fixture.directory,
    timeout: 15_000,
    env: { ...env, DISPAT_OUTPUT_TARBALL:artifact.tarball }
  })
  assert.match(result.stdout, /published @dispat\/cli/)
  assert.equal(fixture.document['dist-tags'].latest, '1.10.2')
  assert.equal(fixture.publications, 1)
  const rejected = await fixture.pack('1.10.3')
  fixture.rejectNext()
  await assert.rejects(execute(command[0], command[1], {
    cwd:fixture.directory, timeout:15_000,
    env:{ ...env, DISPAT_OUTPUT_TARBALL:rejected.tarball }
  }), error => {
    assert.equal((error as { code?: number }).code, 1)
    return true
  })
  assert.equal(fixture.publications, 1)
})
