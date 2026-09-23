import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import http from 'node:http'
import type { Socket } from 'node:net'
import { calculateRateLimitWait, main, platforms, readRelease } from '#root/scripts/package-release.js'
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

interface FakeResponse { ok: boolean, status: number, headers?: Headers, json: () => Promise<GitHubRelease> }
interface RecordedRequest { url: string, headers: Record<string, string> }
interface ScriptedFetch {
  requests: RecordedRequest[]
  fetch: (url: string, options: Record<string, unknown>) => Promise<FakeResponse>
}
interface ScriptedFetchOptions { answers: FakeResponse[] }
interface RefusalOptions { status: number, headers?: Record<string, string>, body?: unknown }
interface GitHubTokenScope { token?: string, run: () => Promise<void> }
interface RateLimitCase { name: string, status: number, headers?: Record<string, string>, expected?: number }
interface MessageCase { name: string, answer: FakeResponse, expected: string }

// Answers each request with the next scripted response, repeating the last one, and records what was sent.
function scriptFetch(options: ScriptedFetchOptions): ScriptedFetch {
  const { answers } = options
  const requests: RecordedRequest[] = []
  const fetch = async (url: string, requestOptions: Record<string, unknown>): Promise<FakeResponse> => {
    requests.push({ url, headers:requestOptions.headers as Record<string, string> })
    return answers[Math.min(requests.length, answers.length) - 1]
  }
  return { requests, fetch }
}

function refuse(options: RefusalOptions): FakeResponse {
  const { status, headers = {}, body } = options
  return { ok:false, status, headers:new Headers(headers), json:async () => body as GitHubRelease }
}

// The packager reads GITHUB_TOKEN itself, so each test states the token it runs with and restores the caller's.
async function withGitHubToken(scope: GitHubTokenScope): Promise<void> {
  const { token, run } = scope
  const previous = process.env.GITHUB_TOKEN
  if (token === undefined) delete process.env.GITHUB_TOKEN
  else process.env.GITHUB_TOKEN = token
  try {
    await run()
  } finally {
    if (previous === undefined) delete process.env.GITHUB_TOKEN
    else process.env.GITHUB_TOKEN = previous
  }
}

test('release metadata sends the GitHub token to api.github.com and nowhere else', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata token '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  await withGitHubToken({ token:'ghs_release_token', run:async () => {
    const github = scriptFetch({ answers:[response(metadata())] })
    await main({ version:'1.10.1', dir, fetch:github.fetch })
    assert.match(github.requests[0].url, /^https:\/\/api\.github\.com\/repos\/yohimik\/dispat\//)
    assert.equal(github.requests[0].headers.authorization, 'Bearer ghs_release_token')
    const others = [
      'https://mirror.example.test/releases',
      'http://api.github.com/repos/yohimik/dispat/releases/tags',
      'https://api.github.com.example.test/releases',
      'https://api.github.com:8443/releases'
    ]
    for (const api of others) {
      const mirror = scriptFetch({ answers:[response(metadata())] })
      await main({ version:'1.10.1', dir, api, fetch:mirror.fetch })
      assert.equal(mirror.requests[0].headers.authorization, undefined, api)
    }
  } })
})

test('release metadata without a token sends no authorization header', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata anonymous '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  for (const token of [undefined, '']) {
    await withGitHubToken({ token, run:async () => {
      const github = scriptFetch({ answers:[response(metadata())] })
      await main({ version:'1.10.1', dir, fetch:github.fetch })
      assert.equal(github.requests[0].headers.authorization, undefined)
      assert.equal(github.requests[0].headers.accept, 'application/vnd.github+json')
    } })
  }
})

test('release metadata waits out a rate-limited 403 and then succeeds', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata rate limit '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const resetSeconds = 1_900_000_000
  const limited = refuse({ status:403, body:{ message:'API rate limit exceeded for 203.0.113.7.' }, headers:{
    'content-type':'application/json; charset=utf-8', 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':String(resetSeconds)
  } })
  const github = scriptFetch({ answers:[limited, response(metadata())] })
  const waits: number[] = []
  const warnings: string[] = []
  await main({
    version:'1.10.1', dir, fetch:github.fetch, now:() => resetSeconds * 1000 - 30_000,
    sleep:async milliseconds => { waits.push(milliseconds) }, warn:message => { warnings.push(message) }
  })
  assert.equal(github.requests.length, 2)
  assert.deepEqual(waits, [30_000])
  assert.equal(warnings.length, 1)
  assert.match(warnings[0], /unavailable: 403 API rate limit exceeded for 203\.0\.113\.7\.; retrying in 30s \(attempt 2 of 3\)$/)
  assert.equal(JSON.parse(await fs.readFile(path.join(dir, 'release.json'), 'utf8')).version, '1.10.1')
})

test('release metadata fails a plain 403 at once with GitHub\'s message', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata forbidden '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const github = scriptFetch({ answers:[refuse({ status:403, headers:{ 'content-type':'application/json' }, body:{
    message:'Resource not accessible by integration', documentation_url:'https://docs.github.com/rest'
  } })] })
  const waits: number[] = []
  await assert.rejects(main({ version:'1.10.1', dir, fetch:github.fetch, sleep:async milliseconds => { waits.push(milliseconds) } }),
    { message:'GitHub release services/dispat/v1.10.1 is unavailable: 403 Resource not accessible by integration' })
  assert.equal(github.requests.length, 1)
  assert.deepEqual(waits, [])
})

test('release metadata stops after three rate-limited attempts', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata exhausted '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const github = scriptFetch({ answers:[refuse({ status:429, headers:{ 'content-type':'application/json', 'retry-after':'7' }, body:{
    message:'You have exceeded a secondary rate limit.'
  } })] })
  const waits: number[] = []
  const warnings: string[] = []
  await assert.rejects(main({
    version:'1.10.1', dir, fetch:github.fetch,
    sleep:async milliseconds => { waits.push(milliseconds) }, warn:message => { warnings.push(message) }
  }), { message:'GitHub release services/dispat/v1.10.1 is unavailable: 429 You have exceeded a secondary rate limit.' })
  assert.equal(github.requests.length, 3)
  assert.deepEqual(waits, [7_000, 7_000])
  assert.deepEqual(warnings.map(warning => /\(attempt (\d) of 3\)$/.exec(warning)?.[1]), ['2', '3'])
})

test('rate-limit waits follow retry-after, then the reset time, and never exceed a minute', () => {
  const now = 1_900_000_000_000
  const nowSeconds = now / 1000
  const cases: RateLimitCase[] = [
    { name:'a missing release is no rate limit', status:404, headers:{ 'x-ratelimit-remaining':'0' } },
    { name:'a refusal without headers is no rate limit', status:403 },
    { name:'a permission refusal is no rate limit', status:403, headers:{ 'x-ratelimit-remaining':'4999' } },
    { name:'retry-after names the wait', status:429, headers:{ 'retry-after':'5' }, expected:5_000 },
    { name:'retry-after wins over the reset time', status:403,
      headers:{ 'retry-after':'2', 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':String(nowSeconds + 50) }, expected:2_000 },
    { name:'a long retry-after is capped', status:403, headers:{ 'retry-after':'3600' }, expected:60_000 },
    { name:'an unreadable retry-after waits a minute', status:403, headers:{ 'retry-after':'Wed, 21 Oct 2026 07:28:00 GMT' }, expected:60_000 },
    { name:'an exhausted quota waits until its reset', status:403,
      headers:{ 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':String(nowSeconds + 42) }, expected:42_000 },
    { name:'a reset already past retries at once', status:403,
      headers:{ 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':String(nowSeconds - 5) }, expected:0 },
    { name:'a distant reset is capped', status:429,
      headers:{ 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':String(nowSeconds + 3600) }, expected:60_000 },
    { name:'an exhausted quota without a reset waits a minute', status:403, headers:{ 'x-ratelimit-remaining':'0' }, expected:60_000 },
    { name:'an unreadable reset waits a minute', status:403,
      headers:{ 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':'soon' }, expected:60_000 }
  ]
  for (const { name, status, headers, expected } of cases) {
    assert.equal(calculateRateLimitWait({ status, headers:headers && new Headers(headers), now }), expected, name)
  }
})

test('release metadata errors carry a bounded single-line GitHub message', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata messages '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const json = { 'content-type':'application/json' }
  const unreadable: FakeResponse = {
    ok:false, status:403, headers:new Headers(json), json:async () => { throw new SyntaxError('Unexpected token < in JSON') }
  }
  const cases: MessageCase[] = [
    { name:'a missing release', answer:refuse({ status:404, headers:json, body:{ message:'Not Found' } }), expected:'404 Not Found' },
    { name:'an error page', answer:refuse({ status:502, headers:{ 'content-type':'text/html' }, body:{ message:'ignored' } }), expected:'502' },
    { name:'a message that is not text', answer:refuse({ status:403, headers:json, body:{ message:42 } }), expected:'403' },
    { name:'a document that is not an object', answer:refuse({ status:403, headers:json, body:null }), expected:'403' },
    { name:'an unreadable document', answer:unreadable, expected:'403' },
    { name:'control characters and line breaks',
      answer:refuse({ status:422, headers:json, body:{ message:'line one\r\n\u001b[31mline two\u0000' } }), expected:'422 line one [31mline two' },
    { name:'a long message', answer:refuse({ status:403, headers:json, body:{ message:'x'.repeat(500) } }), expected:`403 ${'x'.repeat(200)}...` }
  ]
  for (const { name, answer, expected } of cases) {
    const github = scriptFetch({ answers:[answer] })
    await assert.rejects(main({ version:'1.10.1', dir, fetch:github.fetch }),
      { message:`GitHub release services/dispat/v1.10.1 is unavailable: ${expected}` }, name)
  }
})

test('release metadata over HTTP keeps the token from other hosts and waits out a rate limit', async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'npm metadata http rate limit '))
  t.after(() => fs.rm(dir, { recursive:true, force:true }))
  const authorizations: (string | undefined)[] = []
  const server = http.createServer((request, reply) => {
    authorizations.push(request.headers.authorization)
    reply.setHeader('content-type', 'application/json; charset=utf-8')
    if (authorizations.length === 1) {
      reply.writeHead(403, { 'x-ratelimit-remaining':'0', 'x-ratelimit-reset':'0' })
      reply.end(JSON.stringify({ message:'API rate limit exceeded for 127.0.0.1.' }))
      return
    }
    if (authorizations.length === 3) {
      reply.writeHead(403)
      reply.end(JSON.stringify({ message:'Must have admin rights to Repository.' }))
      return
    }
    reply.end(JSON.stringify(metadata()))
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => server.close())
  const address = server.address()
  assert.ok(address && typeof address === 'object')
  const api = `http://127.0.0.1:${address.port}`
  const waits: number[] = []
  const warnings: string[] = []
  const sleep = async (milliseconds: number) => { waits.push(milliseconds) }
  const warn = (message: string) => { warnings.push(message) }
  await withGitHubToken({ token:'ghs_release_token', run:async () => {
    await main({ version:'1.10.1', dir, api, sleep, warn })
    await assert.rejects(main({ version:'1.10.1', dir, api, sleep, warn }),
      { message:'GitHub release services/dispat/v1.10.1 is unavailable: 403 Must have admin rights to Repository.' })
  } })
  assert.deepEqual(authorizations, [undefined, undefined, undefined])
  assert.deepEqual(waits, [0])
  assert.equal(warnings.length, 1)
  assert.match(warnings[0], /unavailable: 403 API rate limit exceeded for 127\.0\.0\.1\.; retrying in 0s \(attempt 2 of 3\)$/)
  assert.equal(JSON.parse(await fs.readFile(path.join(dir, 'release.json'), 'utf8')).version, '1.10.1')
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
