#!/usr/bin/env node
import { mkdtemp, mkdir, readFile, writeFile, stat, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { PACKAGE_ROOT, isMain } from '#root/lib/root.js'
import { verifyArtifact } from '#root/scripts/pack.js'

const execute = promisify(execFile)
async function run(file: string, args: string[], cwd: string, extraEnv: NodeJS.ProcessEnv = {}) {
  return execute(file, args, { cwd, windowsHide: true, timeout: 120_000,
    env: { ...process.env, npm_config_ignore_scripts: 'false', npm_config_allow_scripts: '', DISPAT_UPDATE_CHECK: '0', ...extraEnv } })
}

function expectVersion(stdout: string, version: string, operation: string): void {
  if (!stdout.split(/\r?\n/).some(line => line.split(/\s+/)[0] === 'dispat' && line.split(/\s+/)[1] === version)) {
    throw new Error(`${operation} returned the wrong native version: ${stdout}`)
  }
}

export async function smoke(): Promise<void> {
  const artifact = await verifyArtifact(PACKAGE_ROOT)
  const tarball = process.env.DISPAT_OUTPUT_TARBALL ?? artifact.tarball
  const release = JSON.parse(await readFile(path.join(PACKAGE_ROOT, 'release.json'), 'utf8')) as { version: string }
  const work = await mkdtemp(path.join(tmpdir(), 'dispat npm artifact '))
  try {
    const prefix = path.join(work, 'global prefix')
    await run('npm', ['install', '-g', '--prefix', prefix, '--ignore-scripts=false', tarball], work, { npm_config_allow_scripts: `file:${tarball}` })
    const globalBin = path.join(prefix, process.platform === 'win32' ? 'dispat.cmd' : 'bin/dispat')
    expectVersion((await run(globalBin, ['--version'], work)).stdout, release.version, 'global install')

    const consumer = path.join(work, 'local consumer')
    await mkdir(consumer)
    await writeFile(path.join(consumer, 'package.json'), JSON.stringify({ name: 'dispat-artifact-consumer', private: true, allowScripts: { [`file:${tarball}`]: true } }))
    await run('npm', ['install', '--ignore-scripts=false', tarball], consumer)
    const help = await run('npm', ['exec', '--', 'dispat', '--help'], consumer)
    if (!(help.stdout + help.stderr).includes('dispat')) throw new Error('npm exec help smoke failed')
    await run('git', ['init', '-q'], consumer)
    await mkdir(path.join(consumer, 'app'))
    await writeFile(path.join(consumer, 'app/package.json'), '{"name":"fixture-app","version":"1.0.0"}\n')
    await writeFile(path.join(consumer, 'dispat.yaml'), 'packages:\n  app:\n    path: app\n')
    await run('git', ['add', 'dispat.yaml', 'app/package.json'], consumer)
    await run('git', ['-c', 'user.name=Artifact Test', '-c', 'user.email=test@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-qm', 'chore: fixture'], consumer)
    await run('npm', ['exec', '--', 'dispat', 'status'], consumer)

    const npxConsumer = path.join(work, 'npm exec consumer')
    await mkdir(npxConsumer)
    await writeFile(path.join(npxConsumer, 'package.json'), '{"name":"dispat-npx-consumer","private":true}\n')
    expectVersion((await run('npm', ['exec', '--yes', '--package', tarball, '--', 'dispat', '--version'], npxConsumer, { npm_config_allow_scripts: `file:${tarball}` })).stdout, release.version, 'npm exec package install')

    const pnpmConsumer = path.join(work, 'pnpm consumer')
    await mkdir(pnpmConsumer)
    await writeFile(path.join(pnpmConsumer, 'package.json'), '{"name":"dispat-pnpm-consumer","private":true}\n')
    await run('pnpm', ['add', '--ignore-scripts', tarball], pnpmConsumer)
    await assertMissing(path.join(pnpmConsumer, 'node_modules/@dispat/bin', process.platform === 'win32' ? 'dispat-native.exe' : 'dispat-native'))
    await run(process.execPath, [path.join(pnpmConsumer, 'node_modules/@dispat/bin/build/bin/postinstall.js')], pnpmConsumer)
    expectVersion((await run('pnpm', ['exec', 'dispat', '--version'], pnpmConsumer)).stdout, release.version, 'pnpm repair')
    process.stdout.write(`npm artifact passed: ${process.platform}/${process.arch}, native ${release.version}\n`)
  } finally { await rm(work, { recursive: true, force: true }) }
}

async function assertMissing(file: string): Promise<void> {
  try {
    await stat(file)
    throw new Error('script-disabled pnpm install unexpectedly installed the native binary')
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
  }
}

if (isMain(import.meta.url)) smoke().catch(error => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`)
  process.exitCode = 1
})
