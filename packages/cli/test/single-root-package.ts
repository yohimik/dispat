import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { promisify } from 'node:util'

const execute = promisify(execFile)

async function run(file: string, args: string[], cwd: string) {
  return execute(file, args, {
    cwd,
    windowsHide: true,
    timeout: 120_000,
    env: { ...process.env, DISPAT_UPDATE_CHECK: '0' }
  })
}

async function commit(cwd: string, message: string, files: string[]): Promise<void> {
  await run('git', ['add', ...files], cwd)
  await run('git', [
    '-c', 'user.name=Artifact Test',
    '-c', 'user.email=test@example.invalid',
    '-c', 'commit.gpgsign=false',
    'commit', '-qm', message
  ], cwd)
}

function configuration(source: string): string {
  // Disposable repositories have no remote. Replace publication with a local
  // packing probe and disable only the remote lock; keep real release records.
  return `unsafeDisableLock: true
scripts:
  version: cd .. && npm version "$DISPAT_NEW_VERSION" --no-git-tag-version --ignore-scripts --allow-same-version
  tests: cd .. && npm test
  build: cd .. && npm run build
  publish: cd .. && npm run publish-local
packages:
  app:
    path: ${source}
    tagFormat: v{version}
    autoVersion:
      enabled: true
      manifests: none
    flow:
      version: version
      beforeBuild: tests
      build: build
      publish: publish
commit:
  enabled: true
  name: Artifact Test
  email: test@example.invalid
  include: [package.json, package-lock.json]
`
}

async function fixture(root: string, source: 'src' | 'lib', tarball: string): Promise<void> {
  const cwd = path.join(root, `single root ${source}`)
  await mkdir(path.join(cwd, source), { recursive: true })
  const manifest = {
    name: `single-root-${source}`,
    version: '0.0.0',
    private: false,
    files: [`${source}/`],
    allowScripts: { [`file:${tarball}`]: true },
    scripts: {
      test: 'node probe.cjs test',
      build: 'node probe.cjs build',
      'publish-local': 'node probe.cjs publish && npm pack --dry-run --json > pack.json'
    }
  }
  await writeFile(path.join(cwd, 'package.json'), `${JSON.stringify(manifest, null, 2)}\n`)
  await run('npm', ['install', '--save-dev', '--ignore-scripts=false', tarball], cwd)
  const installedManifest = JSON.parse(await readFile(path.join(cwd, 'package.json'), 'utf8')) as {
    devDependencies: Record<string, string>
  }
  const installedDispat = installedManifest.devDependencies['@dispat/bin']
  assert.ok(installedDispat, 'dispat must be installed as a root development dependency')
  await writeFile(path.join(cwd, 'dispat.yaml'), configuration(source))
  await writeFile(path.join(cwd, 'probe.cjs'), `const assert = require('node:assert/strict')
assert.equal(require('./package.json').version, process.env.DISPAT_NEW_VERSION)
require('node:fs').appendFileSync('events.log', [process.argv[2], process.env.DISPAT_PACKAGE, process.env.DISPAT_NEW_VERSION, process.cwd()].join(' ')+'\\n')
`)
  await writeFile(path.join(cwd, source, 'index.js'), 'export const answer = 42\n')
  await run('git', ['init', '-q', '-b', 'main'], cwd)
  await commit(cwd, 'chore: fixture', ['package.json', 'package-lock.json', 'dispat.yaml', 'probe.cjs', `${source}/index.js`])

  const firstReleaseChoices = [
    ['fix(app): choose the first patch', '0.0.1'],
    ['feat(app): choose the first minor', '0.1.0'],
    ['feat(app)!: choose the first stable major', '1.0.0'],
    ['feat(app)%beta: choose the first minor prerelease', '0.1.0-beta.0'],
    ['feat(app)%beta!: choose the first major prerelease', '1.0.0-beta.0'],
    ['feat!: derive an unscoped breaking source change', '1.0.0']
  ] as const
  for (const [message, expected] of firstReleaseChoices) {
    await writeFile(path.join(cwd, source, 'index.js'), `export const answer = ${expected.length}\n`)
    await commit(cwd, message, [`${source}/index.js`])
    const plan = await run('npm', ['exec', '--', 'dispat', 'status', '--require-release'], cwd)
    assert.match(plan.stdout + plan.stderr, new RegExp(`0\\.0\\.0 -> ${expected.replaceAll('.', '\\.')}"`))
    await run('git', ['reset', '--hard', 'HEAD~1'], cwd)
  }

  await writeFile(path.join(cwd, 'probe.cjs'), `${await readFile(path.join(cwd, 'probe.cjs'), 'utf8')}// root-only change\n`)
  await commit(cwd, 'fix: root-only change', ['probe.cjs'])
  await assert.rejects(
    run('npm', ['exec', '--', 'dispat', 'status', '--require-release'], cwd),
    error => (error as { code?: number }).code === 3
  )

  await writeFile(path.join(cwd, source, 'index.js'), 'export const answer = 43\n')
  await commit(cwd, 'fix: source change', [`${source}/index.js`])
  const beforeReadOnly = await Promise.all([
    readFile(path.join(cwd, 'package.json'), 'utf8'),
    readFile(path.join(cwd, 'package-lock.json'), 'utf8'),
    readFile(path.join(cwd, 'dispat.yaml'), 'utf8')
  ])
  const status = await run('npm', ['exec', '--', 'dispat', 'status', '--require-release'], cwd)
  assert.match(status.stdout + status.stderr, /0\.0\.0 -> 0\.0\.1/)
  await run('npm', ['exec', '--', 'dispat', 'preview'], cwd)
  await run('npm', ['exec', '--', 'dispat', 'compute'], cwd)
  assert.deepEqual(await Promise.all([
    readFile(path.join(cwd, 'package.json'), 'utf8'),
    readFile(path.join(cwd, 'package-lock.json'), 'utf8'),
    readFile(path.join(cwd, 'dispat.yaml'), 'utf8')
  ]), beforeReadOnly)
  await run('npm', ['exec', '--', 'dispat', 'release'], cwd)

  const releasedManifest = JSON.parse(await readFile(path.join(cwd, 'package.json'), 'utf8')) as {
    version: string, scripts: Record<string, string>, devDependencies?: Record<string, string>
  }
  const lock = JSON.parse(await readFile(path.join(cwd, 'package-lock.json'), 'utf8')) as {
    version: string, packages: Record<string, { version?: string, devDependencies?: Record<string, string> }>
  }
  assert.equal(releasedManifest.version, '0.0.1')
  assert.equal(lock.version, '0.0.1')
  assert.equal(lock.packages[''].version, '0.0.1')
  assert.equal(releasedManifest.devDependencies?.['@dispat/bin'], installedDispat)
  assert.equal(lock.packages[''].devDependencies?.['@dispat/bin'], installedDispat)
  assert.equal((await run('git', ['tag', '--list', 'v0.0.1'], cwd)).stdout.trim(), 'v0.0.1')
  const taggedManifest = JSON.parse((await run('git', ['show', 'v0.0.1:package.json'], cwd)).stdout) as { version: string }
  const taggedLock = JSON.parse((await run('git', ['show', 'v0.0.1:package-lock.json'], cwd)).stdout) as { version: string }
  assert.equal(taggedManifest.version, '0.0.1')
  assert.equal(taggedLock.version, '0.0.1')
  const events = await readFile(path.join(cwd, 'events.log'), 'utf8')
  assert.deepEqual(events.trim().split('\n').map(line => line.split(' ')[0]), ['test', 'build', 'publish'])
  assert.match(events, new RegExp(`test app 0\\.0\\.1 .*single root ${source}`))
  assert.match(events, /build app 0\.0\.1/)
  assert.match(events, /publish app 0\.0\.1/)
  type PackRecord = { name: string, version: string, files: Array<{ path: string }> }
  const pack = JSON.parse(await readFile(path.join(cwd, 'pack.json'), 'utf8')) as
    PackRecord[] | Record<string, PackRecord>
  const packed = Array.isArray(pack) ? pack[0] : Object.values(pack)[0]
  assert.equal(Array.isArray(pack) ? pack.length : Object.keys(pack).length, 1)
  assert.equal(packed.name, `single-root-${source}`)
  assert.equal(packed.version, '0.0.1')
  assert.deepEqual(packed.files.map(file => file.path).sort(), ['package.json', `${source}/index.js`].sort())

  await run('npm', ['version', '0.0.1', '--no-git-tag-version', '--ignore-scripts', '--allow-same-version'], cwd)
  assert.equal((JSON.parse(await readFile(path.join(cwd, 'package.json'), 'utf8')) as { version: string }).version, '0.0.1')

  await assert.rejects(
    run('npm', ['exec', '--', 'dispat', 'status', '--require-release'], cwd),
    error => (error as { code?: number }).code === 3
  )

  releasedManifest.scripts.test = 'node -e "process.exit(7)"'
  await writeFile(path.join(cwd, 'package.json'), `${JSON.stringify(releasedManifest, null, 2)}\n`)
  await commit(cwd, 'fix(app): exercise build failure', ['package.json'])
  await assert.rejects(run('npm', ['exec', '--', 'dispat', 'release'], cwd))
  const afterFailure = JSON.parse(await readFile(path.join(cwd, 'package.json'), 'utf8')) as { version: string }
  assert.equal(afterFailure.version, '0.0.2', 'the explicit version stage runs before build gates')
  assert.equal((await run('git', ['tag', '--list', 'v0.0.2'], cwd)).stdout.trim(), '')
  const eventsAfterFailure = await readFile(path.join(cwd, 'events.log'), 'utf8')
  assert.equal(eventsAfterFailure, events, 'the failing test gate must stop build and publish')
  await assert.rejects(run('npm', ['exec', '--', 'dispat', 'release'], cwd), error => {
    const failure = error as { stdout?: string, stderr?: string }
    assert.match(`${failure.stdout ?? ''}${failure.stderr ?? ''}`, /pre-existing local changes/)
    return true
  })
  assert.equal(await readFile(path.join(cwd, 'events.log'), 'utf8'), eventsAfterFailure,
    'a retry with dirty root release inputs must not reach publish')
}

export async function smokeSingleRootPackages(root: string, tarball: string): Promise<void> {
  await fixture(root, 'src', tarball)
  await fixture(root, 'lib', tarball)
}
