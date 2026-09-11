'use strict'

import path from 'node:path'
import { spawn } from 'node:child_process'
import { binaryName } from '#root/lib/platform.js'
import { PACKAGE_ROOT } from '#root/lib/root.js'
import type { ChildProcess, SpawnOptions } from 'node:child_process'

const VALUE_FLAGS = new Set(['--root', '--config', '--env-file', '--concurrency', '--log-format', '--log-level', '--owner', '--repo', '--api-url', '--token-env', '--release'])

interface Invocation {
  command: string
  commandIndex: number
  booleans: Map<string, boolean>
}

interface LaunchOptions {
  packageDir?: string
  platform?: NodeJS.Platform
  spawn?: (file: string, args: readonly string[], options: SpawnOptions) => ChildProcess
}

function invocation(args: readonly string[]): Invocation {
  let literal = false
  let command = ''
  let commandIndex = -1
  const booleans = new Map()
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]
    if (literal) { if (!command) { command = arg; commandIndex = i }; break }
    if (arg === '--') { literal = true; continue }
    if (VALUE_FLAGS.has(arg)) { i++; continue }
    if (arg === '-h') { booleans.set('--help', true); continue }
    if (arg.startsWith('-h=')) { booleans.set('--help', /^(?:1|t|true)$/i.test(arg.slice(3))); continue }
    if (arg.startsWith('--help') || arg.startsWith('--check') || arg.startsWith('--version')) {
      const [name, raw] = arg.split('=', 2)
      if (name === '--help' || name === '--check' || name === '--version') booleans.set(name, raw === undefined || /^(?:1|t|true)$/i.test(raw))
      continue
    }
    if (arg.startsWith('-')) continue
    if (!command) { command = arg; commandIndex = i }
    break
  }
  if (command === 'self-update') {
    for (let i = commandIndex + 1; i < args.length; i++) {
      const arg = args[i]
      if (arg === '--') break
      if (VALUE_FLAGS.has(arg)) { i++; continue }
      if (arg === '-h') booleans.set('--help', true)
      else if (arg.startsWith('-h=')) booleans.set('--help', /^(?:1|t|true)$/i.test(arg.slice(3)))
      else if (arg.startsWith('--help') || arg.startsWith('--check') || arg.startsWith('--version')) {
        const [name, raw] = arg.split('=', 2)
        if (name === '--help' || name === '--check' || name === '--version') booleans.set(name, raw === undefined || /^(?:1|t|true)$/i.test(raw))
      }
    }
  }
  return { command, commandIndex, booleans }
}

function commandOf(args: readonly string[]): string { return invocation(args).command }
function booleanFlag(args: readonly string[], name: string): boolean { return invocation(args).booleans.get(name) || false }

function launch(args: string[] = process.argv.slice(2), options: LaunchOptions = {}): ChildProcess | number {
  const parsed = invocation(args)
  if (parsed.command === 'self-update' && !parsed.booleans.get('--help') && !parsed.booleans.get('--check') && !parsed.booleans.get('--version')) {
    process.stderr.write('dispat: self-update is managed by npm. Run `npm update @dispat/bin` locally or `npm install -g @dispat/bin@latest` globally. To force or roll back, install an explicit version such as `npm install -g @dispat/bin@1.10.0`.\n')
    return 2
  }
  const packageDir = options.packageDir || PACKAGE_ROOT
  const binary = path.join(packageDir, binaryName(options.platform))
  let child
  try {
    child = (options.spawn || spawn)(binary, args, {
      cwd: process.cwd(), env: { ...process.env, DISPAT_UPDATE_CHECK: '0' }, stdio: 'inherit', windowsHide: false
    })
  } catch (error) { return missing(binary, asError(error)) }
  const handlers = new Map<NodeJS.Signals, () => void>()
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP'] as NodeJS.Signals[]) {
    const handler = () => { if (!child.killed) child.kill(signal) }
    handlers.set(signal, handler)
    process.once(signal, handler)
  }
  const cleanup = () => { for (const [signal, handler] of handlers) process.removeListener(signal, handler) }
  child.on('error', (error: Error) => { cleanup(); process.exitCode = missing(binary, error) })
  child.on('exit', (code: number | null, signal: NodeJS.Signals | null) => {
    cleanup()
    if (signal) {
      try { process.kill(process.pid, signal) } catch (_) { process.exitCode = 1 }
    } else process.exitCode = code == null ? 1 : code
  })
  return child
}

function missing(binary: string, error: Error): number {
  process.stderr.write(`dispat: could not launch ${binary}: ${error.message}\n`)
  process.stderr.write('dispat: repair a local install with `node node_modules/@dispat/bin/build/bin/postinstall.js`; for a global install run the same file under the directory printed by `npm root -g`.\n')
  return 1
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value))
}

export { launch, invocation, commandOf, booleanFlag, VALUE_FLAGS }
