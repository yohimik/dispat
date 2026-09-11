import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import http from 'node:http'
import type { Socket } from 'node:net'
import { main, platforms, readRelease } from '#root/scripts/package-release.js'
import type { GitHubRelease } from '#root/scripts/package-release.js'
import { publish } from '#root/scripts/publish.js'

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

test('publisher performs exactly one npm publish operation', async () => {
  const calls: string[][] = []
  await publish({ tarball:'/tmp/dispat-cli.tgz', run:async args => { calls.push(args) } })
  assert.deepEqual(calls, [[
    'publish', '/tmp/dispat-cli.tgz', '--access', 'public', '--provenance', '--tag', 'latest'
  ]])
})

test('publisher passes prerelease channels to npm without registry operations', async () => {
  const calls: string[][] = []
  await publish({ tarball:'/tmp/dispat-cli.tgz', channel:'rc', run:async args => { calls.push(args) } })
  assert.equal(calls.length, 1)
  assert.equal(calls[0][0], 'publish')
  assert.deepEqual(calls[0].slice(-2), ['--tag', 'rc'])
})

test('publisher uses release outputs and propagates npm failures', async () => {
  const previousTarball = process.env.DISPAT_OUTPUT_TARBALL
  const previousChannel = process.env.DISPAT_CHANNEL
  process.env.DISPAT_OUTPUT_TARBALL = '/tmp/dispat-cli.tgz'
  process.env.DISPAT_CHANNEL = 'beta'
  try {
    await assert.rejects(publish({ run:async args => {
      assert.deepEqual(args.slice(-2), ['--tag', 'beta'])
      throw new Error('npm rejected publication')
    } }), /npm rejected publication/)
  } finally {
    if (previousTarball === undefined) delete process.env.DISPAT_OUTPUT_TARBALL
    else process.env.DISPAT_OUTPUT_TARBALL = previousTarball
    if (previousChannel === undefined) delete process.env.DISPAT_CHANNEL
    else process.env.DISPAT_CHANNEL = previousChannel
  }
  await assert.rejects(publish({ run:async () => undefined }), /DISPAT_OUTPUT_TARBALL/)
})
