import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFile } from 'node:child_process'
import { chmod, cp, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { promisify } from 'node:util'
import { pathToFileURL } from 'node:url'
import { PACKAGE_ROOT } from '#root/lib/root.js'
import { ASSET_NAMES, binaryName, platformKey } from '#root/lib/platform.js'
import { parsePackOutput } from '#root/scripts/pack.js'

const execute = promisify(execFile)

test('packed global install recovers from npm 12 blocking its postinstall', { skip: process.platform === 'win32' }, async t => {
  const work = await mkdtemp(path.join(tmpdir(), "dispat global ' $HOME $(nope) `nope` "))
  t.after(() => rm(work, { recursive:true, force:true }))
  const source = path.join(work, 'package source')
  await mkdir(source)
  await cp(path.join(PACKAGE_ROOT, 'build'), path.join(source, 'build'), { recursive:true })
  const manifest = JSON.parse(await readFile(path.join(PACKAGE_ROOT, 'package.json'), 'utf8')) as { version:string; scripts:Record<string,string>; dependencies?:Record<string,string> }
  manifest.version = '1.10.99'
  manifest.dependencies = {}
  await writeFile(path.join(source, 'package.json'), `${JSON.stringify(manifest)}\n`)
  await writeFile(path.join(source, 'README.md'), 'fixture\n')
  const native = path.join(work, 'fixture native')
  const body = Buffer.from("#!/bin/sh\nprintf 'dispat 1.10.99\\n'\n")
  await writeFile(native, body)
  await chmod(native, 0o755)
  const key = platformKey()
  await writeFile(path.join(source, 'release.json'), JSON.stringify({ version:'1.10.99', tag:'services/dispat/v1.10.99', assets:{
    [key]: { name:ASSET_NAMES[key], size:body.length, sha256:createHash('sha256').update(body).digest('hex') }
  } }))
  const emptyUserConfig = path.join(work, 'empty-user.npmrc')
  await writeFile(emptyUserConfig, '')
  const npmEnv = { ...process.env, npm_config_userconfig:emptyUserConfig, npm_config_cache:path.join(work, 'npm cache'),
    npm_config_allow_scripts:'', npm_config_ignore_scripts:'false', npm_config_dangerously_allow_all_scripts:'false',
    npm_config_strict_allow_scripts:'false' }
  const packed = parsePackOutput((await execute('npm', ['pack', '--json', '--ignore-scripts'], { cwd:source, env:npmEnv, timeout:30_000 })).stdout)
  const tarball = path.join(source, packed.filename)
  const prefix = path.join(work, 'global prefix')
  const npmMajor = Number((await execute('npm', ['--version'], { env:npmEnv, timeout:30_000 })).stdout.trim().split('.')[0])
  const installArgs = ['install', '--global', '--prefix', prefix]
  if (npmMajor < 12) installArgs.push('--ignore-scripts')
  installArgs.push(tarball)
  await execute('npm', installArgs, { cwd:work, env:npmEnv, timeout:30_000 })
  const installed = path.join(prefix, 'lib/node_modules/@dispat/bin')
  const command = path.join(prefix, 'bin/dispat')
  await assert.rejects(stat(path.join(installed, binaryName())), { code:'ENOENT' })
  let repair = ''
  await assert.rejects(execute(command, ['--version'], { cwd:work, timeout:30_000 }), error => {
    const stderr = (error as { stderr:string }).stderr
    assert.match(stderr, /install script may have been blocked/)
    repair = stderr.trim().split('\n').at(-1)!.trim()
    assert.match(repair, /@dispat\/bin\/build\/bin\/postinstall\.js'$/)
    return true
  })
  const mockFetch = path.join(work, 'mock-fetch.mjs')
  const loader = path.join(work, 'loader.mjs')
  await writeFile(mockFetch, `import { createReadStream } from 'node:fs'; export default async()=>({ok:true,status:200,statusText:'OK',headers:{get:n=>n.toLowerCase()==='content-length'?process.env.DISPAT_FIXTURE_SIZE:null},body:createReadStream(process.env.DISPAT_FIXTURE_BINARY)})\n`)
  await writeFile(loader, `import { pathToFileURL } from 'node:url'; export async function resolve(s,c,n){return s==='make-fetch-happen'?{url:pathToFileURL(process.env.DISPAT_FIXTURE_FETCH).href,shortCircuit:true}:n(s,c)}\n`)
  const env = { ...process.env, NODE_OPTIONS:`--experimental-loader=${pathToFileURL(loader).href}`, DISPAT_FIXTURE_FETCH:mockFetch,
    DISPAT_FIXTURE_BINARY:native, DISPAT_FIXTURE_SIZE:String(body.length) }
  await execute('/bin/sh', ['-c', repair], { cwd:work, env, timeout:30_000 })
  assert.match((await execute(command, ['--version'], { cwd:work, timeout:30_000 })).stdout, /^dispat 1\.10\.99/m)
})
