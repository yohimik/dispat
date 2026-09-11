#!/usr/bin/env node
'use strict'

import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { isMain } from '#root/lib/root.js'

const exec = promisify(execFile)
const NPM_TIMEOUT_MS = 120_000
const NPM_MAX_BUFFER = 2 * 1024 * 1024

type Runner = (args: string[]) => Promise<unknown>
interface PublishOptions {
  tarball?: string
  channel?: string
  run?: Runner
}

async function runNpm(args: string[]): Promise<void> {
  const result = await exec('npm', args, {
    windowsHide: true,
    timeout: NPM_TIMEOUT_MS,
    killSignal: 'SIGKILL',
    maxBuffer: NPM_MAX_BUFFER
  })
  process.stdout.write(result.stdout)
  process.stderr.write(result.stderr)
}

async function publish(options: PublishOptions = {}): Promise<void> {
  const tarball = options.tarball || process.env.DISPAT_OUTPUT_TARBALL
  if (!tarball) throw new Error('DISPAT_OUTPUT_TARBALL is required')
  const channel = options.channel || process.env.DISPAT_CHANNEL || 'stable'
  const tag = channel === 'stable' ? 'latest' : channel
  await (options.run || runNpm)(['publish', tarball, '--access', 'public', '--provenance', '--tag', tag])
}

if (isMain(import.meta.url)) publish()
  .then(() => console.log('published @dispat/cli'))
  .catch(error => { console.error(error instanceof Error ? error.message : String(error)); process.exitCode = 1 })

export { publish }
