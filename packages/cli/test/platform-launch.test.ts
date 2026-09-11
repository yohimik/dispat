import test from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import type { ChildProcess, SpawnOptions } from 'node:child_process'
import { platformKey, binaryName } from '#root/lib/platform.js'
import { commandOf, booleanFlag, launch } from '#root/lib/launch.js'

function fakeChild(): ChildProcess {
  return Object.assign(new EventEmitter(), { killed: false, kill() { this.killed = true; return true } }) as unknown as ChildProcess
}

test('maps every supported platform and architecture', () => {
  for (const [os, arch, key] of [['linux','x64','linux-x64'],['linux','arm64','linux-arm64'],['darwin','x64','darwin-x64'],['darwin','arm64','darwin-arm64'],['win32','x64','win32-x64'],['win32','arm64','win32-arm64']]) assert.equal(platformKey(os, arch), key)
  assert.equal(binaryName('win32'), 'dispat-native.exe'); assert.equal(binaryName('linux'), 'dispat-native')
  assert.throws(() => platformKey('freebsd', 'x64'), /unsupported platform/)
  assert.throws(() => platformKey('linux', 'ia32'), /unsupported platform/)
})

test('finds the native command without mistaking values or arguments', () => {
  assert.equal(commandOf(['--root', 'self-update', 'status']), 'status')
  assert.equal(commandOf(['--root=self-update', 'status']), 'status')
  assert.equal(commandOf(['run', 'self-update']), 'run')
  assert.equal(commandOf(['status', '--', 'self-update']), 'status')
  assert.equal(commandOf(['--', 'self-update']), 'self-update')
  assert.equal(commandOf(['--help']), '')
})

test('boolean flags use last explicit value', () => {
  assert.equal(booleanFlag(['--check', '--check=false'], '--check'), false)
  assert.equal(booleanFlag(['--check=false', '--check'], '--check'), true)
  assert.equal(booleanFlag(['--check=0'], '--check'), false)
  assert.equal(booleanFlag(['self-update','--check=TRUE'], '--check'), true)
  assert.equal(booleanFlag(['self-update','--token-env','--check'], '--check'), false)
  assert.equal(booleanFlag(['--root','--check','self-update'], '--check'), false)
  assert.equal(booleanFlag(['self-update','-h'], '--help'), true)
  assert.equal(booleanFlag(['self-update','--help','-h=false'], '--help'), false)
  assert.equal(booleanFlag(['self-update','-h=TRUE'], '--help'), true)
  assert.equal(booleanFlag(['self-update','--help','--','--help=false'], '--help'), true)
  assert.equal(booleanFlag(['--version','self-update'], '--version'), true)
  assert.equal(booleanFlag(['self-update','--version'], '--version'), true)
  assert.equal(booleanFlag(['-h'], '--help'), true)
  assert.equal(booleanFlag(['-h=false'], '--help'), false)
})

test('launcher preserves argv, environment and exit status', async () => {
  const child = fakeChild()
  let call: { file: string, args: readonly string[], options: SpawnOptions } | undefined
  const result = launch(['status', '--json'], { packageDir: '/a path', platform: 'linux', spawn(file, args, options) { call = { file, args, options }; return child } })
  assert.equal(result, child); assert.ok(call); assert.equal(call.file, '/a path/dispat-native'); assert.deepEqual(call.args, ['status', '--json'])
  assert.equal(call.options.stdio, 'inherit'); assert.equal(call.options.cwd, process.cwd()); assert.equal(call.options.env?.DISPAT_UPDATE_CHECK, '0')
  child.emit('exit', 17, null); assert.equal(process.exitCode, 17); process.exitCode = 0
})

test('launcher permits read-only self-update forms and rejects mutations', () => {
  for (const args of [['self-update','--check'],['self-update','--rollback','--check=true'],['self-update','--help'],['self-update','--version'],['--version','self-update']]) {
    const child = fakeChild()
    assert.equal(launch(args, { spawn: () => child }).constructor, EventEmitter)
    child.emit('exit', 0, null)
  }
  for (const args of [['self-update'],['self-update','--force'],['self-update','--check=false'],['self-update','--help=false']]) assert.equal(launch(args), 2)
  process.exitCode = 0
})

test('launcher reports synchronous and asynchronous spawn failures', () => {
  assert.equal(launch(['status'], { spawn() { throw new Error('no file') } }), 1)
  const child = fakeChild()
  launch(['status'], { spawn: () => child }); child.emit('error', new Error('denied'))
  assert.equal(process.exitCode, 1); process.exitCode = 0
  assert.equal(launch(['status'], { spawn() { throw 'plain spawn failure' } }), 1)
})
