import test, { type TestContext } from 'node:test'
import type { BinaryExecutor, DownloadResponse } from '#root/lib/types.js'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'
import { Readable } from 'node:stream'
import { install, validateMetadata, validBinary } from '#root/lib/install.js'

async function fixture(t: TestContext, body = Buffer.from('native executable')) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'dispat npm ')); t.after(() => fs.rm(dir, { recursive: true, force: true }))
  const release = { version: '1.10.0', tag: 'services/dispat/v1.10.0', assets: { 'linux-x64': { name: 'dispat-linux-amd64', size: body.length, sha256: crypto.createHash('sha256').update(body).digest('hex') } } }
  await fs.writeFile(path.join(dir, 'release.json'), JSON.stringify(release))
  return { dir, release, body }
}
function response(body: Buffer, length: string | null = String(body.length)): DownloadResponse {
  return { ok: true, status: 200, headers: { get: name => name === 'content-length' ? length : null }, body: Readable.from(body) }
}
const goodExec: BinaryExecutor = async (_file, _args, options) => { assert.equal(options.env?.DISPAT_UPDATE_CHECK, '0'); return { stdout: 'dispat 1.10.0\n' } }

test('validates complete immutable metadata', () => {
  const release = { version:'1.0.0',tag:'services/dispat/v1.0.0',assets:{ 'linux-x64':{name:'dispat-linux-amd64',size:1,sha256:'a'.repeat(64)} } }
  assert.equal(validateMetadata(release,'linux-x64').size, 1)
  for (const bad of [null,{}, {...release,tag:'latest'}, {...release,version:'latest'}, {...release,assets:{}}, {...release,assets:{'linux-x64':{name:'x',size:0,sha256:'bad'}}}]) assert.throws(() => validateMetadata(bad,'linux-x64'), /metadata/)
})

test('streams, verifies, chmods and atomically installs', async t => {
  const f = await fixture(t); let cleaned = false
  const result = await install({ packageDir:f.dir, platform:'linux', arch:'x64', release:f.release, exec:goodExec, download:async url => { assert.match(url, /services%2Fdispat%2Fv1\.10\.0/); const r=response(f.body); r.dispatCleanup=()=>{cleaned=true}; return r } })
  assert.equal(result.installed, true); assert.deepEqual(await fs.readFile(result.target), f.body); assert.equal(cleaned, true)
  assert.equal((await fs.stat(result.target)).mode & 0o777, 0o755)
  assert.deepEqual(await fs.readdir(f.dir), ['dispat-native','release.json'])
})

test('accepts a valid existing binary and replaces an invalid same-version binary', async t => {
  const f = await fixture(t); const target=path.join(f.dir,'dispat-native'); await fs.writeFile(target,f.body)
  assert.equal((await install({packageDir:f.dir,platform:'linux',arch:'x64',release:f.release,exec:goodExec})).installed,false)
  await fs.writeFile(target, Buffer.alloc(f.body.length, 1)); let fetched=false
  await install({packageDir:f.dir,platform:'linux',arch:'x64',release:f.release,exec:goodExec,download:async()=>{fetched=true;return response(f.body)}})
  assert.equal(fetched,true)
})

test('preserves an existing binary and cleans temporary files on failures', async t => {
  const f=await fixture(t); const target=path.join(f.dir,'dispat-native'); await fs.writeFile(target,'old')
  const cases: [Buffer, string | null, RegExp][] = [[Buffer.from('short'),null,/truncated/],[Buffer.alloc(f.body.length,2),String(f.body.length),/checksum/],[Buffer.concat([f.body,Buffer.from('x')]),null,/exceeded/],[f.body,'xyz',/download size/]]
  for (const [body,length,pattern] of cases) {
    await assert.rejects(install({packageDir:f.dir,platform:'linux',arch:'x64',release:f.release,exec:goodExec,download:async()=>response(body,length)}),pattern)
    assert.equal(await fs.readFile(target,'utf8'),'old'); assert.deepEqual((await fs.readdir(f.dir)).filter(x=>x.includes('.tmp')),[])
  }
})

test('rejects a downloaded executable reporting the wrong version', async t => {
  const f=await fixture(t)
  await assert.rejects(install({packageDir:f.dir,platform:'linux',arch:'x64',release:f.release,exec:async()=>({stdout:'dispat 1.9.0'}),download:async()=>response(f.body)}),/did not report/)
  assert.equal(await validBinary('/missing','1.0.0',async()=>{throw new Error('missing')}),false)
})

test('reads packaged metadata and reports installation events at their severity', async t => {
  const f = await fixture(t)
  const events: string[] = []
  const options = { packageDir: f.dir, platform: 'linux', arch: 'x64', exec: goodExec,
    download: async () => response(f.body), log: (level: string, message: string) => { events.push(`${level}: ${message}`) } }
  assert.equal((await install(options)).installed, true)
  assert.deepEqual(events, ['debug: selected release asset', 'trace: starting binary download', 'info: installed dispat'])
  events.length = 0
  assert.equal((await install(options)).installed, false)
  assert.deepEqual(events, ['debug: selected release asset', 'debug: existing binary verified'])
})

test('Windows temporary executable retains an exe suffix before validation', async t => {
  const f = await fixture(t)
  const release = { ...f.release, assets: { 'win32-arm64': { ...f.release.assets['linux-x64'], name: 'dispat-windows-arm64.exe' } } }
  const result = await install({ packageDir: f.dir, release, platform: 'win32', arch: 'arm64',
    download: async () => response(f.body), exec: async (file, args, options) => {
      assert.match(file, /\.tmp\.exe$/)
      return goodExec(file, args, options)
    } })
  assert.equal(path.basename(result.target), 'dispat-native.exe')
  assert.deepEqual(await fs.readFile(result.target), f.body)
})

test('missing response body and network failures leave no temporary files', async t => {
  const f = await fixture(t)
  const options = { packageDir: f.dir, platform: 'linux', arch: 'x64', release: f.release, exec: goodExec }
  await assert.rejects(install({ ...options, download: async () => ({ ...response(f.body), body: null }) }), /no body/)
  await assert.rejects(install({ ...options, download: async () => { throw new Error('connection refused') } }), /connection refused/)
  assert.deepEqual(await fs.readdir(f.dir), ['release.json'])
})

test('a directory at the binary path is preserved when replacement fails', async t => {
  const f = await fixture(t)
  const target = path.join(f.dir, 'dispat-native')
  await fs.mkdir(target)
  await fs.writeFile(path.join(target, 'keep'), 'existing user data')
  await assert.rejects(install({ packageDir: f.dir, platform: 'linux', arch: 'x64', release: f.release,
    exec: goodExec, download: async () => response(f.body) }), /EISDIR|EPERM|EACCES/)
  assert.equal(await fs.readFile(path.join(target, 'keep'), 'utf8'), 'existing user data')
  assert.deepEqual(await fs.readdir(f.dir), ['dispat-native', 'release.json'])
})

test('never executes an existing symbolic link before replacing it', { skip: process.platform === 'win32' }, async t => {
  const f = await fixture(t)
  const linkedFile = path.join(f.dir, 'linked-file')
  await fs.writeFile(linkedFile, f.body)
  const target = path.join(f.dir, 'dispat-native')
  await fs.symlink(linkedFile, target)
  await install({ packageDir: f.dir, platform: 'linux', arch: 'x64', release: f.release,
    download: async () => response(f.body), exec: async (file, args, options) => {
      assert.notEqual(file, target)
      return goodExec(file, args, options)
    } })
  assert.equal((await fs.lstat(target)).isSymbolicLink(), false)
  assert.deepEqual(await fs.readFile(linkedFile), f.body)
})
