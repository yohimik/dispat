#!/usr/bin/env node
'use strict'

import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { isMain } from '#root/lib/root.js'
import { isVersion } from '#root/lib/install.js'
const exec = promisify(execFile)

interface CommandResult { stdout: string, stderr?: string }
type Runner = (args: string[]) => Promise<CommandResult>
interface PublishOptions {
  tarball?: string
  integrity?: string
  name?: string
  version?: string
  channel?: string
  packedName?: string
  packedVersion?: string
  run?: Runner
}

async function sh(args: string[]): Promise<CommandResult> { return exec('npm', args, { windowsHide: true }) }

function npmScalar(stdout: string, field: string): string {
  if (!stdout.trim()) return ''
  const value: unknown = JSON.parse(stdout)
  const scalars = Array.isArray(value) ? value : [value]
  if (scalars.length !== 1 || typeof scalars[0] !== 'string') throw new Error(`npm returned ambiguous ${field} metadata`)
  return scalars[0]
}

async function registryIntegrity(spec: string, run: Runner = sh): Promise<string> {
  try { return npmScalar((await run(['view', spec, 'dist.integrity', '--json'])).stdout, 'integrity') }
  catch (error: unknown) {
    const failure = error as { stderr?: string }
    if (failure.stderr && /E404|404 Not Found/.test(failure.stderr)) return ''
    throw error
  }
}

function compareVersions(a: string, b: string): number {
  if (!isVersion(a) || !isVersion(b)) throw new Error(`cannot compare invalid versions ${a} and ${b}`)
  const normalizedA = a.split('+', 1)[0], normalizedB = b.split('+', 1)[0]
  const pa = normalizedA.split(/[.-]/).slice(0, 3).map(Number), pb = normalizedB.split(/[.-]/).slice(0, 3).map(Number)
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] - pb[i]
  return normalizedA.includes('-') === normalizedB.includes('-') ? 0 : (normalizedA.includes('-') ? -1 : 1)
}

async function reconcileTag(spec: string, name: string, version: string, channel: string, run: Runner): Promise<void> {
  const tag = channel === 'stable' ? 'latest' : channel
  if (tag === 'latest') {
    let latest = ''
    try { latest = npmScalar((await run(['view', `${name}@latest`, 'version', '--json'])).stdout, 'version') }
    catch (error: unknown) {
      const failure = error as { stderr?: string }
      if (!failure.stderr || !/E404|404 Not Found/.test(failure.stderr)) throw error
    }
    if (latest && compareVersions(version, latest) < 0) return
  }
  await run(['dist-tag', 'add', spec, tag])
}

async function publish(options: PublishOptions = {}) {
  const tarball = options.tarball || process.env.DISPAT_OUTPUT_TARBALL
  const integrity = options.integrity || process.env.DISPAT_OUTPUT_INTEGRITY
  const name = options.name || '@dispat/cli'
  const version = options.version || process.env.DISPAT_NEW_VERSION
  const channel = options.channel || process.env.DISPAT_CHANNEL || 'stable'
  if (!tarball || !integrity || !version) throw new Error('tarball, integrity and DISPAT_NEW_VERSION are required')
  if (!isVersion(version)) throw new Error(`invalid npm version ${version}`)
  if (!/^[a-z0-9][a-z0-9._-]*$/.test(channel)) throw new Error(`invalid npm channel ${channel}`)
  const spec = `${name}@${version}`
  const run = options.run || sh
  const bytes = await fs.readFile(tarball)
  const localIntegrity = `sha512-${crypto.createHash('sha512').update(bytes).digest('base64')}`
  if (localIntegrity !== integrity) throw new Error(`tarball integrity mismatch: expected ${integrity}, got ${localIntegrity}`)
  const packedName = options.packedName || process.env.DISPAT_OUTPUT_PACKED_NAME
  const packedVersion = options.packedVersion || process.env.DISPAT_OUTPUT_PACKED_VERSION
  if (packedName !== name || packedVersion !== version) {
    throw new Error(`packed artifact is ${packedName || '<unknown>'}@${packedVersion || '<unknown>'}; expected ${spec}`)
  }
  const existing = await registryIntegrity(spec, run)
  if (existing) {
    if (existing === integrity) { await reconcileTag(spec, name, version, channel, run); return { published: false, integrity } }
    throw new Error(`${spec} already exists with conflicting integrity ${existing}`)
  }
  if (channel === 'stable') {
    let latest = ''
    try { latest = npmScalar((await run(['view', `${name}@latest`, 'version', '--json'])).stdout, 'version') }
    catch (error: unknown) {
      const failure = error as { stderr?: string }
      if (!failure.stderr || !/E404|404 Not Found/.test(failure.stderr)) throw error
    }
    if (latest && compareVersions(version, latest) < 0) throw new Error(`refusing to publish ${spec} with latest because ${name}@latest is newer (${latest})`)
  }
  const tag = channel === 'stable' ? 'latest' : channel
  try { await run(['publish', tarball, '--access', 'public', '--provenance', '--tag', tag]) }
  catch (error) {
    const after = await registryIntegrity(spec, run)
    if (after === integrity) { await reconcileTag(spec, name, version, channel, run); return { published: false, integrity } }
    if (after) throw new Error(`${spec} appeared with conflicting integrity ${after} after publish failed`, { cause: error })
    throw error
  }
  const after = await registryIntegrity(spec, run)
  if (after !== integrity) throw new Error(`${spec} publication could not be reconciled: expected ${integrity}, got ${after || '<missing>'}`)
  return { published: true, integrity }
}

if (isMain(import.meta.url)) publish().then(result => console.error(result.published ? 'published @dispat/cli' : 'accepted identical existing @dispat/cli publication')).catch(error => { console.error(error.message); process.exitCode = 1 })
export { publish, registryIntegrity, compareVersions, reconcileTag, npmScalar }
