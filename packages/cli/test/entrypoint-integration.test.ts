import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { cp, mkdtemp, mkdir, writeFile, rm, symlink, realpath } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile, spawn } from 'node:child_process'
import { promisify } from 'node:util'
import { createHash } from 'node:crypto'
import { PACKAGE_ROOT } from '#root/lib/root.js'
import { ASSET_NAMES, binaryName, platformKey } from '#root/lib/platform.js'

const execute = promisify(execFile)
async function fixture(t: TestContext, nativeSource?: string) {
  const root = await mkdtemp(join(tmpdir(), 'dispat entrypoint '))
  t.after(() => rm(root, { recursive: true, force: true }))
  await cp(join(PACKAGE_ROOT, 'build'), join(root, 'build'), { recursive: true })
  await cp(join(PACKAGE_ROOT, 'package.json'), join(root, 'package.json'))
  await symlink(join(PACKAGE_ROOT, 'node_modules'), join(root, 'node_modules'), 'junction')
  if (nativeSource) {
    const body = Buffer.from(`#!${process.execPath}\n${nativeSource}\n`)
    await writeFile(join(root, binaryName()), body, { mode: 0o755 })
    const key = platformKey()
    await writeFile(join(root, 'release.json'), JSON.stringify({ version: '1.10.0', tag: 'services/dispat/v1.10.0', assets: {
      [key]: { name: ASSET_NAMES[key], size: body.length, sha256: createHash('sha256').update(body).digest('hex') }
    } }))
  }
  return root
}

const unixOnly = { skip: process.platform === 'win32' }

test('launcher preserves literal arguments, working directory, environment and stream contents', unixOnly, async t => {
  const root = await fixture(t, `process.stdin.setEncoding('utf8'); let input=''; process.stdin.on('data', chunk => input+=chunk); process.stdin.on('end', () => {
    process.stdout.write(JSON.stringify({ args:process.argv.slice(2), cwd:process.cwd(), input, update:process.env.DISPAT_UPDATE_CHECK, custom:process.env.DISPAT_TEST_VALUE }));
    process.stderr.write('native stderr\\n'); process.exitCode=17;
  });`)
  const cwd = join(root, 'working directory')
  await mkdir(cwd)
  const args = ['run', 'self-update', 'spaces and quotes "', '$(not-a-shell-command)', '--', '--check']
  const child = spawn(process.execPath, [join(root, 'build/bin/dispat.js'), ...args], {
    cwd, env: { ...process.env, DISPAT_UPDATE_CHECK: '1', DISPAT_TEST_VALUE: 'preserved' }, stdio: ['pipe', 'pipe', 'pipe']
  })
  let stdout = '', stderr = ''
  child.stdout.setEncoding('utf8').on('data', chunk => { stdout += chunk })
  child.stderr.setEncoding('utf8').on('data', chunk => { stderr += chunk })
  child.stdin.end('interactive input\n')
  const code = await new Promise<number | null>((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  assert.equal(code, 17)
  assert.deepEqual(JSON.parse(stdout), { args, cwd: await realpath(cwd), input: 'interactive input\n', update: '0', custom: 'preserved' })
  assert.equal(stderr, 'native stderr\n')
})

test('launcher forwards SIGINT unchanged and exits when the child is interrupted', unixOnly, async t => {
  const root = await fixture(t, `setInterval(() => {}, 1000); process.on('SIGINT', () => { process.stdout.write('SIGINT\\n'); process.exit(130) }); process.stdout.write('ready\\n');`)
  const child = spawn(process.execPath, [join(root, 'build/bin/dispat.js')], { stdio: ['ignore', 'pipe', 'pipe'] })
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL') })
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
  t.after(() => clearTimeout(timer))
  let stdout = ''
  child.stdout.setEncoding('utf8').on('data', chunk => {
    stdout += chunk
    if (stdout === 'ready\n') child.kill('SIGINT')
  })
  const code = await new Promise<number | null>((resolve, reject) => { child.once('error', reject); child.once('close', resolve) })
  assert.equal(code, 130)
  assert.equal(stdout, 'ready\nSIGINT\n')
})

test('launcher returns the original child termination signal', unixOnly, async t => {
  const root = await fixture(t, "process.kill(process.pid, 'SIGTERM')")
  await assert.rejects(execute(process.execPath, [join(root, 'build/bin/dispat.js')]), (error: unknown) => {
    assert.equal((error as NodeJS.ErrnoException & { signal: string }).signal, 'SIGTERM')
    return true
  })
})

test('missing binary reports repair instructions without trying a download', async t => {
  const root = await fixture(t)
  await assert.rejects(execute(process.execPath, [join(root, 'build/bin/dispat.js'), '--version']), (error: unknown) => {
    const failure = error as { code: number; stdout: string; stderr: string }
    assert.equal(failure.code, 1)
    assert.equal(failure.stdout, '')
    assert.match(failure.stderr, /install script may have been blocked/)
    assert.match(failure.stderr, new RegExp(join(root, 'build/bin/postinstall.js').replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    assert.doesNotMatch(failure.stderr, /npm root -g/)
    return true
  })
})

test('npm-managed global update advice retains npm 12 script approval', async t => {
  const root = await fixture(t)
  await assert.rejects(execute(process.execPath, [join(root, 'build/bin/dispat.js'), 'self-update']), (error: unknown) => {
    const failure = error as { code:number; stderr:string }
    assert.equal(failure.code, 2)
    assert.match(failure.stderr, /@latest --allow-scripts=@dispat\/bin/)
    assert.match(failure.stderr, /@1\.10\.0 --allow-scripts=@dispat\/bin/)
    return true
  })
})

test('postinstall verifies an existing binary and is silent on repeat installation', unixOnly, async t => {
  const root = await fixture(t, "process.stdout.write('dispat 1.10.0\\n')")
  const result = await execute(process.execPath, [join(root, 'build/bin/postinstall.js')])
  assert.equal(result.stdout, '')
  assert.equal(result.stderr, '')
})

test('postinstall failure is actionable and debug output remains on stderr', async t => {
  const root = await fixture(t)
  for (const debug of ['', '1']) {
    await assert.rejects(execute(process.execPath, [join(root, 'build/bin/postinstall.js')], { env: { ...process.env, DISPAT_NPM_DEBUG: debug } }), (error: unknown) => {
      const failure = error as { code: number; stdout: string; stderr: string }
      assert.equal(failure.code, 1)
      assert.equal(failure.stdout, '')
      assert.match(failure.stderr, /installation failed/)
      assert.match(failure.stderr, /postinstall\.js/)
      return true
    })
  }
})
