import test, { type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'

const execute = promisify(execFile)

// The fixture sits outside the repository, where corepack would otherwise
// resolve the newest pnpm rather than the one the release build runs. The
// repository root's packageManager is that pin, read from the source tree the
// compiled test runs beside.
const repositoryManifest = new URL('../../../package.json', import.meta.url)

async function packageManager(): Promise<string> {
  const { packageManager:pin } = JSON.parse(await readFile(repositoryManifest, 'utf8')) as { packageManager?:unknown }
  assert.equal(typeof pin, 'string', 'the repository root names no packageManager')
  return pin as string
}

async function workspace(t: TestContext): Promise<string> {
  const root = await mkdtemp(path.join(tmpdir(), 'dispat pnpm release '))
  t.after(() => rm(root, { recursive:true, force:true }))
  await mkdir(path.join(root, 'packages/cli'), { recursive:true })
  await mkdir(path.join(root, 'packages/provider'), { recursive:true })
  await writeFile(path.join(root, 'pnpm-workspace.yaml'), "packages:\n  - 'packages/*'\n")
  await writeFile(path.join(root, 'package.json'), JSON.stringify({ private:true, packageManager:await packageManager() }))
  await writeFile(path.join(root, 'packages/provider/package.json'), JSON.stringify({ name:'fixture-provider', version:'1.0.0' }))
  await writeFile(path.join(root, 'packages/cli/package.json'), JSON.stringify({
    name:'fixture-cli', version:'1.0.0', scripts:{
      compile:"node -e \"require('fs').writeFileSync('compiled','yes')\"",
      build:'pnpm compile',
      'compile:test':"node -e \"require('fs').writeFileSync('tests-compiled','yes')\""
    }
  }))
  await execute('pnpm', ['install', '--ignore-scripts'], { cwd:root })
  await writeFile(path.join(root, 'packages/cli/package.json'), JSON.stringify({
    name:'fixture-cli', version:'1.0.1',
    devDependencies:{ 'fixture-provider':'workspace:*' },
    scripts:{
      postinstall:"node -e \"require('fs').writeFileSync('unexpected-postinstall','yes');process.exit(42)\"",
      compile:"node -e \"require('fs').writeFileSync('compiled','yes')\"",
      build:'pnpm compile',
      'compile:test':"node -e \"require('fs').writeFileSync('tests-compiled','yes')\""
    }
  }))
  return root
}

test('release build does not implicitly reinstall a stale pnpm workspace', async t => {
  const env = { ...process.env, pnpm_config_verify_deps_before_run:'install' }
  const unguarded = await workspace(t)
  await assert.rejects(execute('pnpm', ['--filter', 'fixture-cli', 'run', 'build'], { cwd:unguarded, env }))
  assert.equal(await readFile(path.join(unguarded, 'packages/cli/unexpected-postinstall'), 'utf8'), 'yes')

  const root = await workspace(t)
  const guardedEnv = { ...process.env, pnpm_config_verify_deps_before_run:'false' }
  const cli = path.join(root, 'packages/cli')
  await execute('pnpm', ['build'], { cwd:cli, env:guardedEnv })
  await execute('pnpm', ['compile:test'], { cwd:cli, env:guardedEnv })
  assert.equal(await readFile(path.join(cli, 'compiled'), 'utf8'), 'yes')
  assert.equal(await readFile(path.join(cli, 'tests-compiled'), 'utf8'), 'yes')
  await assert.rejects(readFile(path.join(root, 'packages/cli/unexpected-postinstall')))
  const lockfile = await readFile(path.join(root, 'pnpm-lock.yaml'), 'utf8')
  assert.doesNotMatch(lockfile, /fixture-provider/)
})
