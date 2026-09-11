import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'
import http from 'node:http'
import type { Socket } from 'node:net'
import { main, platforms, readRelease } from '#root/scripts/package-release.js'
import type { GitHubRelease } from '#root/scripts/package-release.js'
import { publish, registryIntegrity, compareVersions, reconcileTag, npmScalar } from '#root/scripts/publish.js'
import type { TestContext } from 'node:test'

const metadata = (version='1.10.1') => ({ tag_name:`services/dispat/v${version}`, assets:platforms.map(([,name],i)=>({name,size:i+1,digest:`sha256:${String(i).repeat(64)}`})) })
const response = (body: GitHubRelease, status=200) => ({ ok:status===200,status,json:async()=>body })

test('generates exact release metadata without latest resolution', async t => {
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'npm metadata ')); t.after(()=>fs.rm(dir,{recursive:true,force:true})); let url; let requestOptions: Record<string, unknown> = {}
  await main({version:'1.10.1',dir,fetch:async (value, options)=>{url=String(value);requestOptions=options;return response(metadata())}})
  assert.match(url!,/services%2Fdispat%2Fv1\.10\.1$/); const got=JSON.parse(await fs.readFile(path.join(dir,'release.json'), 'utf8'))
  assert.equal(got.version,'1.10.1'); assert.equal(Object.keys(got.assets).length,6)
  assert.equal(requestOptions.timeout, 30_000)
})
test('release metadata generation fails closed', async t => {
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'npm metadata ')); t.after(()=>fs.rm(dir,{recursive:true,force:true}))
  const previousVersion = process.env.DISPAT_WORKSPACE_DISPAT_VERSION
  delete process.env.DISPAT_WORKSPACE_DISPAT_VERSION
  try { await assert.rejects(main({dir}),/exact semantic/) } finally {
    if (previousVersion !== undefined) process.env.DISPAT_WORKSPACE_DISPAT_VERSION = previousVersion
  }
  await assert.rejects(main({version:'latest',dir}),/exact semantic/)
  await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response({},404)}),/unavailable/)
  await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response({...metadata(),tag_name:'other'})}),/expected/)
  await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response({...metadata(),tag_name:undefined})}),/<missing>/)
  await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response({...metadata(),assets:[...metadata().assets,metadata().assets[0]]})}),/duplicate/)
  const missing=metadata(); missing.assets.pop(); await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(missing)}),/complete metadata/)
  const bad=metadata(); bad.assets[0].digest='sha256:bad'; await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(bad)}),/complete metadata/)
  const absentDigest=metadata(); absentDigest.assets[0].digest=''; await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(absentDigest)}),/complete metadata/)
  const zero=metadata(); zero.assets[0].size=0; await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(zero)}),/complete metadata/)
  const fractional=metadata(); fractional.assets[0].size=1.5; await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(fractional)}),/complete metadata/)
  const oversized=metadata(); oversized.assets[0].size=129 * 1024 * 1024; await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response(oversized)}),/complete metadata/)
  await assert.rejects(main({version:'1.10.1',dir,fetch:async()=>response({tag_name:'services/dispat/v1.10.1'})}),/complete metadata/)
})

test('release metadata can use the workspace pin and canonical API without writing latest', async t => {
  const old = process.env.DISPAT_WORKSPACE_DISPAT_VERSION
  process.env.DISPAT_WORKSPACE_DISPAT_VERSION = '1.10.1'
  let url = ''
  try {
    await main({ fetch:async value => { url=String(value); return response(metadata()) } })
    assert.match(url, /^https:\/\/api\.github\.com\/.+services%2Fdispat%2Fv1\.10\.1$/)
  } finally {
    if (old === undefined) delete process.env.DISPAT_WORKSPACE_DISPAT_VERSION
    else process.env.DISPAT_WORKSPACE_DISPAT_VERSION = old
    await fs.rm(path.resolve('release.json'), { force:true })
  }
})

test('release metadata uses the maintained transport against an exact custom endpoint', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata transport '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const server = http.createServer((_request, reply) => {
    reply.setHeader('content-type', 'application/json')
    reply.end(JSON.stringify(metadata()))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => server.close())
  const address = server.address()
  assert.ok(address && typeof address === 'object')
  await main({ version:'1.10.1', dir, api:`http://127.0.0.1:${address.port}` })
  assert.equal(JSON.parse(await fs.readFile(path.join(dir, 'release.json'), 'utf8')).version, '1.10.1')
})

test('release metadata closes a stalled HTTP error response', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata error '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  let closed!: () => void
  const responseClosed = new Promise<void>(resolve => { closed = resolve })
  const server = http.createServer((_request, reply) => {
    reply.on('close', closed)
    reply.writeHead(503)
    reply.write('unavailable')
  })
  const sockets = new Set<Socket>()
  server.on('connection', socket => {
    sockets.add(socket)
    socket.once('close', () => sockets.delete(socket))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const socket of sockets) socket.destroy()
    await new Promise<void>(resolve => server.close(() => resolve()))
  })
  const address = server.address()
  assert.ok(address && typeof address === 'object')
  await assert.rejects(main({ version:'1.10.1', dir, api:`http://127.0.0.1:${address.port}` }), /unavailable: 503/)
  let deadline!: NodeJS.Timeout
  try {
    await Promise.race([responseClosed, new Promise((_, reject) => {
      deadline = setTimeout(() => reject(new Error('response remained open')), 500)
    })])
  } finally { clearTimeout(deadline) }
})

test('release metadata response parsing rejects missing and oversized bodies', async () => {
  await assert.rejects(readRelease({ ok:true, status:200 }), /no body/)
  async function *oversized(): AsyncIterable<Uint8Array> { yield Buffer.alloc(2 * 1024 * 1024 + 1) }
  await assert.rejects(readRelease({ ok:true, status:200, body:oversized() }), /exceeded/)
})

async function artifact(t: TestContext) { const dir=await fs.mkdtemp(path.join(os.tmpdir(),'npm publish '));t.after(()=>fs.rm(dir,{recursive:true,force:true}));const file=path.join(dir,'a.tgz');const bytes=Buffer.from('tarball');await fs.writeFile(file,bytes);return {file,integrity:`sha512-${crypto.createHash('sha512').update(bytes).digest('base64')}`} }
const out = (value?: string) => ({stdout:value?`"${value}"\n`:''})
test('version ordering protects registry tags from rollback', () => {
  const ordered: [string, string][] = [
    ['1.11.0', '1.10.9'],
    ['1.10.1', '1.10.1-rc.1'],
    ['1.10.1-rc.10', '1.10.1-rc.2'],
    ['1.10.1-beta', '1.10.1-10'],
    ['1.10.1-beta', '1.10.1-alpha'],
    ['1.10.1-alpha', '1.10.1-BETA'],
    ['1.10.1-rc.1', '1.10.1-rc'],
    ['1.10.1-rc-two', '1.10.1-rc-one'],
    ['100000000000000000000.0.0', '99999999999999999999.0.0']
  ]
  for (const [later, earlier] of ordered) {
    assert.ok(compareVersions(later, earlier) > 0, `${later} should follow ${earlier}`)
    assert.ok(compareVersions(earlier, later) < 0, `${earlier} should precede ${later}`)
  }
  for (const [left, right] of [
    ['1.10.1-rc.1', '1.10.1-rc.1'],
    ['1.0.0', '1.0.0'],
    ['1.2.3+build-2', '1.2.3+build-1']
  ]) assert.equal(compareVersions(left, right), 0)
})
test('registry lookup distinguishes missing versions and errors',async()=>{assert.equal(await registryIntegrity('x',async()=>out('sha512-x')),'sha512-x');assert.equal(await registryIntegrity('x',async()=>{const e=Object.assign(new Error(),{stderr:'npm E404'});throw e}), '');await assert.rejects(registryIntegrity('x',async()=>{throw new Error('network')}),/network/)})
test('publishes once then verifies registry integrity',async t=>{const a=await artifact(t);const calls:string[][]=[];let views=0;const result=await publish({tarball:a.file,integrity:a.integrity,version:'1.10.1',packedName:'@dispat/cli',packedVersion:'1.10.1',run:async args=>{calls.push(args);if(args.includes('version'))return out();if(args[0]==='view')return out(views++?a.integrity:'');return out('')}})
  assert.equal(result.published,true);assert.ok(calls.some(x=>x[0]==='publish'))})
test('accepts an identical publication and repairs channel tags',async t=>{const a=await artifact(t);const calls:string[][]=[];const result=await publish({tarball:a.file,integrity:a.integrity,version:'1.10.1-rc.1',channel:'rc',packedName:'@dispat/cli',packedVersion:'1.10.1-rc.1',run:async args=>{calls.push(args);return out(args[0]==='view'?a.integrity:'')}});assert.equal(result.published,false);assert.deepEqual(calls.at(-1),['dist-tag','add','@dispat/cli@1.10.1-rc.1','rc'])})
test('rejects local, packed and registry identity conflicts',async t=>{const a=await artifact(t);const base={tarball:a.file,integrity:a.integrity,version:'1.10.1',packedName:'@dispat/cli',packedVersion:'1.10.1',run:async()=>out('sha512-other')};await assert.rejects(publish({...base,integrity:'sha512-bad'}),/tarball integrity/);await assert.rejects(publish({...base,packedVersion:'1.10.2'}),/packed artifact/);await assert.rejects(publish(base),/conflicting integrity/)})
test('reconciles an ambiguous failed publish and preserves an older latest',async t=>{const a=await artifact(t);let views=0;const ok=await publish({tarball:a.file,integrity:a.integrity,version:'1.10.1',packedName:'@dispat/cli',packedVersion:'1.10.1',run:async args=>{if(args[0]==='publish')throw new Error('lost');if(args.includes('version'))return out();if(args[0]==='view')return out(views++?a.integrity:'');return out('')}});assert.equal(ok.published,false)
  const calls:string[][]=[];await reconcileTag('@dispat/cli@1.9.0','@dispat/cli','1.9.0','stable',async args=>{calls.push(args);return out('1.10.0')});assert.equal(calls.length,1)
})

test('scalar parsing and publication inputs fail closed', async t => {
  assert.equal(npmScalar('', 'version'), '')
  assert.equal(npmScalar('["1.0.0"]', 'version'), '1.0.0')
  assert.throws(() => npmScalar('["a","b"]', 'version'), /ambiguous/)
  assert.throws(() => compareVersions('latest','1.0.0'), /invalid versions/)
  const a = await artifact(t)
  await assert.rejects(publish({ tarball:a.file, integrity:a.integrity, version:'latest', packedName:'@dispat/cli', packedVersion:'latest' }), /invalid npm version/)
  await assert.rejects(publish({ tarball:a.file, integrity:a.integrity, version:'1.0.0', channel:'Bad Tag!', packedName:'@dispat/cli', packedVersion:'1.0.0' }), /invalid npm channel/)
  await assert.rejects(publish({ tarball:a.file, integrity:a.integrity, version:'1.0.0-rc.1', packedName:'@dispat/cli', packedVersion:'1.0.0-rc.1' }), /requires a prerelease npm channel/)
  await assert.rejects(publish({ tarball:a.file, integrity:a.integrity, version:'1.0.0-rc.1', channel:'latest', packedName:'@dispat/cli', packedVersion:'1.0.0-rc.1' }), /requires a prerelease npm channel/)
  await assert.rejects(publish({ tarball:a.file, integrity:a.integrity, version:'1.0.0+build-id', packedName:'@dispat/cli', packedVersion:'1.0.0+build-id', run:async()=>{ throw new Error('registry reached') } }), /registry reached/)
})

test('refuses a fresh stable version older than latest and propagates lookup errors', async t => {
  const a = await artifact(t)
  const base = { tarball:a.file, integrity:a.integrity, version:'1.9.0', packedName:'@dispat/cli', packedVersion:'1.9.0' }
  await assert.rejects(publish({ ...base, run: async args => args.includes('dist.integrity') ? out() : out('1.10.0') }), /latest is newer/)
  await assert.rejects(publish({ ...base, channel:'latest', run: async args => args.includes('dist.integrity') ? out() : out('1.10.0') }), /latest is newer/)
  await assert.rejects(reconcileTag('@dispat/cli@1.0.0','@dispat/cli','1.0.0','stable',async()=>{ throw new Error('offline') }), /offline/)
})

test('publisher accepts dispat release environment outputs', async t => {
  const a = await artifact(t)
  const previous = { ...process.env }
  Object.assign(process.env, {
    DISPAT_OUTPUT_TARBALL:a.file, DISPAT_OUTPUT_INTEGRITY:a.integrity,
    DISPAT_OUTPUT_PACKED_NAME:'@dispat/cli', DISPAT_OUTPUT_PACKED_VERSION:'1.10.2',
    DISPAT_NEW_VERSION:'1.10.2', DISPAT_CHANNEL:'beta'
  })
  try {
    const result = await publish({ run:async args => out(args[0] === 'view' ? a.integrity : '') })
    assert.equal(result.published, false)
  } finally {
    for (const key of ['DISPAT_OUTPUT_TARBALL','DISPAT_OUTPUT_INTEGRITY','DISPAT_OUTPUT_PACKED_NAME','DISPAT_OUTPUT_PACKED_VERSION','DISPAT_NEW_VERSION','DISPAT_CHANNEL']) {
      if (previous[key] === undefined) delete process.env[key]
      else process.env[key] = previous[key]
    }
  }
})

test('publisher rejects unresolved and conflicting ambiguous failures', async t => {
  const a = await artifact(t)
  const base = { tarball:a.file, integrity:a.integrity, version:'1.10.3', packedName:'@dispat/cli', packedVersion:'1.10.3', channel:'beta' }
  let views = 0
  await assert.rejects(publish({ ...base, run:async args => {
    if (args[0] === 'publish') throw new Error('lost')
    return out(views++ ? 'sha512-conflict' : '')
  } }), /appeared with conflicting integrity/)
  await assert.rejects(publish({ ...base, run:async args => {
    if (args[0] === 'publish') throw new Error('offline')
    return out()
  } }), /offline/)
})
