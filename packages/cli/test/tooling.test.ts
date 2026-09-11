import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { main as pack, parsePackOutput, verifyArtifact } from '#root/scripts/pack.js'
import { verify } from '#root/scripts/verify-package.js'
import { main as postinstall } from '#root/lib/postinstall.js'
import { runPostinstall } from '#root/bin/postinstall.js'
import { isMain } from '#root/lib/root.js'
import { pathToFileURL } from 'node:url'
import * as publicApi from '#root/lib/index.js'
import * as releaseApi from '#root/scripts/index.js'

async function temporary(t: TestContext): Promise<string> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'dispat tooling '))
  t.after(() => fs.rm(dir, { recursive: true, force: true }))
  return dir
}

test('pack output accepts npm 11 arrays and npm 12 keyed records', () => {
  const record = { filename: 'dispat.tgz', integrity: 'sha512-a', name: '@dispat/bin', version: '1.10.0' }
  assert.deepEqual(parsePackOutput(JSON.stringify([record])), record)
  assert.deepEqual(parsePackOutput(JSON.stringify({ '@dispat/bin': record })), record)
  assert.throws(() => parsePackOutput('{}'), /0 artifact records/)
  assert.throws(() => parsePackOutput('null'), /0 artifact records/)
  assert.throws(() => parsePackOutput(JSON.stringify([{}, {}])), /2 artifact records/)
  assert.throws(() => parsePackOutput('[{}]'), /incomplete/)
})

test('pack creates one reusable tarball and records its integrity', async t => {
  const root = await temporary(t)
  await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({ name:'fixture-package', version:'1.0.0', files:['README.md'] }))
  await fs.writeFile(path.join(root, 'README.md'), 'fixture')
  const outputFile = path.join(root, 'outputs')
  const oldOutput = process.env.DISPAT_OUTPUT
  process.env.DISPAT_OUTPUT = outputFile
  try { await pack({ cwd:root, output:'packed files' }) } finally {
    if (oldOutput === undefined) delete process.env.DISPAT_OUTPUT
    else process.env.DISPAT_OUTPUT = oldOutput
  }
  const artifact = JSON.parse(await fs.readFile(path.join(root,'packed files/artifact.json'),'utf8'))
  assert.equal(artifact.name, 'fixture-package')
  assert.equal(artifact.version, '1.0.0')
  assert.match(artifact.integrity, /^sha512-/)
  assert.equal((await fs.stat(artifact.tarball)).isFile(), true)
  assert.match(await fs.readFile(outputFile,'utf8'), /TARBALL=/)
  assert.deepEqual(await verifyArtifact(root, 'packed files', {}), artifact)
  await assert.rejects(verifyArtifact(root, 'packed files', { DISPAT_OUTPUT_TARBALL:'another.tgz' }), /TARBALL does not match/)
  await assert.rejects(verifyArtifact(root, 'packed files', { DISPAT_OUTPUT_PACKED_VERSION:'2.0.0' }), /PACKED_VERSION does not match/)
  await fs.appendFile(artifact.tarball, 'changed after validation')
  await assert.rejects(verifyArtifact(root, 'packed files', {}), /tarball integrity mismatch/)
})

test('artifact verification rejects incomplete and conflicting identities before publish', async t => {
  const root = await temporary(t)
  await fs.mkdir(path.join(root, 'dist'))
  await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({ name:'fixture-package', version:'1.0.0' }))
  await fs.writeFile(path.join(root, 'dist/artifact.json'), JSON.stringify({ tarball:'missing.tgz' }))
  await assert.rejects(verifyArtifact(root, 'dist', {}), /artifact record is missing integrity/)
  await fs.writeFile(path.join(root, 'dist/artifact.json'), JSON.stringify({
    tarball:'missing.tgz', integrity:'sha512-invalid', name:'another-package', version:'1.0.0'
  }))
  await assert.rejects(verifyArtifact(root, 'dist', {}), /packed artifact is another-package@1.0.0/)
})

test('pack entry point uses its working directory and default output folder', async t => {
  const root = await temporary(t)
  await fs.writeFile(path.join(root, 'package.json'), JSON.stringify({ name:'fixture-defaults', version:'1.0.0', files:['README.md'] }))
  await fs.writeFile(path.join(root, 'README.md'), 'fixture')
  const result = spawnSync(process.execPath, [path.resolve('build/scripts/pack.js')], {
    cwd: root,
    encoding: 'utf8',
    env: { ...process.env, DISPAT_OUTPUT: '' }
  })
  assert.equal(result.status, 0, result.stderr)
  assert.equal((await fs.stat(path.join(root, 'dist/artifact.json'))).isFile(), true)
})

test('package verifier checks generated metadata and allowlisted files', async t => {
  const root = await temporary(t)
  const assets = Object.fromEntries([
    ['darwin-x64','dispat-darwin-amd64'],['darwin-arm64','dispat-darwin-arm64'],['linux-x64','dispat-linux-amd64'],
    ['linux-arm64','dispat-linux-arm64'],['win32-x64','dispat-windows-amd64.exe'],['win32-arm64','dispat-windows-arm64.exe']
  ].map(([key,name]) => [key, { name, size: 1, sha256: 'a'.repeat(64) }]))
  await fs.mkdir(path.join(root, 'build/bin'), { recursive: true })
  await Promise.all(['build/bin/dispat.js','build/bin/postinstall.js','README.md','LICENSE'].map(file => fs.writeFile(path.join(root,file),'')))
  await fs.writeFile(path.join(root,'package.json'), JSON.stringify({ name:'@dispat/bin', version:'1.10.2' }))
  await fs.writeFile(path.join(root,'release.json'), JSON.stringify({ version:'1.10.0', tag:'services/dispat/v1.10.0', assets }))
  verify(root)
  await fs.rm(path.join(root, 'LICENSE'))
  assert.throws(() => verify(root), /missing LICENSE/)
  await fs.writeFile(path.join(root, 'LICENSE'), '')
  await fs.writeFile(path.join(root,'package.json'), JSON.stringify({ version:'1.11.0' }))
  assert.throws(() => verify(root), /major\/minor/)
})

test('postinstall is silent for a valid existing binary and diagnoses failures', async () => {
  let output = ''
  const stderr = { write(value: string | Uint8Array) { output += String(value); return true } }
  assert.equal(await postinstall({ installer: async () => ({ target:'x', installed:false }), stderr }), 0)
  assert.equal(output, '')
  assert.equal(await postinstall({ installer: async () => { throw new Error('offline') }, stderr, env:{ DISPAT_NPM_DEBUG:'1' } }), 1)
  assert.match(output, /installation failed: offline/)
  assert.match(output, /build\/bin\/postinstall/)
  output = ''
  assert.equal(await postinstall({ installer: async options => {
    options?.log?.('debug','details',{})
    options?.log?.('warn','warning',{})
    return { target:'x', installed:true }
  }, stderr, env:{ DISPAT_NPM_DEBUG:'1' } }), 0)
  assert.match(output, /\[debug\]: details/)
  assert.match(output, /installed verified/)
  output = ''
  assert.equal(await postinstall({ installer: async options => {
    options?.log?.('debug', 'hidden detail', {})
    options?.log?.('warn', 'visible warning', {})
    return { target:'x', installed:false }
  }, stderr, env:{} }), 0)
  assert.doesNotMatch(output, /hidden detail/)
  assert.match(output, /visible warning/)
  assert.equal(await postinstall({ installer: async () => { throw 'plain failure' }, stderr }), 1)
  const originalDebug = process.env.DISPAT_NPM_DEBUG
  process.env.DISPAT_NPM_DEBUG = '1'
  try {
    assert.equal(await postinstall({ installer: async options => {
      options?.log?.('debug','default streams',{})
      return { target:'x', installed:false }
    } }), 0)
  } finally {
    if (originalDebug === undefined) delete process.env.DISPAT_NPM_DEBUG
    else process.env.DISPAT_NPM_DEBUG = originalDebug
  }
})

test('barrels expose named side-effect-free APIs and bin diagnoses missing binary', () => {
  assert.equal(typeof publicApi.install, 'function')
  assert.equal(typeof publicApi.launch, 'function')
  assert.equal(typeof releaseApi.publish, 'function')
  const result = spawnSync(process.execPath, [path.resolve('build/bin/dispat.js'), 'status'], { encoding:'utf8' })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /could not launch/)
  const blocked = spawnSync(process.execPath, [path.resolve('build/bin/dispat.js'), 'self-update'], { encoding:'utf8' })
  assert.equal(blocked.status, 2)
})

test('postinstall entry point preserves its installer exit status', async () => {
  const previous = process.exitCode
  try {
    await runPostinstall(async () => 7)
    assert.equal(process.exitCode, 7)
  } finally {
    process.exitCode = previous
  }
})

test('entry point detection handles absent, canonical and missing paths', () => {
  const previous = process.argv[1]
  try {
    process.argv[1] = ''
    assert.equal(isMain(import.meta.url), false)
    process.argv[1] = previous
    assert.equal(isMain(pathToFileURL(previous).href), true)
    const missing = path.join(os.tmpdir(), 'dispat-entrypoint-does-not-exist')
    process.argv[1] = missing
    assert.equal(isMain(pathToFileURL(missing).href), true)
    assert.equal(isMain(pathToFileURL(`${missing}-other`).href), false)
  } finally {
    process.argv[1] = previous
  }
})

test('release tool entry points fail closed when required inputs are absent', () => {
  const env = { ...process.env }
  delete env.DISPAT_WORKSPACE_DISPAT_VERSION
  delete env.DISPAT_OUTPUT_TARBALL
  delete env.DISPAT_OUTPUT_INTEGRITY
  delete env.DISPAT_NEW_VERSION
  const metadata = spawnSync(process.execPath, [path.resolve('build/scripts/package-release.js')], { encoding:'utf8', env })
  assert.equal(metadata.status, 1)
  assert.match(metadata.stderr, /exact semantic version/)
  const publish = spawnSync(process.execPath, [path.resolve('build/scripts/publish.js')], { encoding:'utf8', env })
  assert.equal(publish.status, 1)
  assert.match(publish.stderr, /DISPAT_OUTPUT_TARBALL/)
  const verification = spawnSync(process.execPath, [path.resolve('build/scripts/verify-package.js')], { encoding:'utf8', env })
  assert.notEqual(verification.status, null)
})
