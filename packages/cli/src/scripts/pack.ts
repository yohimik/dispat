#!/usr/bin/env node
'use strict'

import fs from 'node:fs/promises'
import path from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { isMain } from '#root/lib/root.js'
const exec = promisify(execFile)

interface PackOptions { cwd?: string, output?: string }
interface PackRecord { filename: string, integrity: string, name: string, version: string }

function parsePackOutput(stdout: string): PackRecord {
  const parsed: unknown = JSON.parse(stdout)
  const records = Array.isArray(parsed) ? parsed : parsed && typeof parsed === 'object' ? Object.values(parsed) : []
  if (records.length !== 1) throw new Error(`npm pack returned ${records.length} artifact records; expected one`)
  const record = records[0] as Partial<PackRecord>
  if (!record || typeof record.filename !== 'string' || typeof record.integrity !== 'string' || typeof record.name !== 'string' || typeof record.version !== 'string') {
    throw new Error('npm pack returned an incomplete artifact record')
  }
  return record as PackRecord
}

async function main(options: PackOptions = {}) {
  const cwd = options.cwd || process.cwd()
  const out = path.resolve(cwd, options.output || 'dist')
  await fs.mkdir(out, { recursive: true })
  const { stdout } = await exec('npm', ['pack', '--json', '--pack-destination', out], { cwd, windowsHide: true })
  const record = parsePackOutput(stdout)
  const tarball = path.join(out, record.filename)
  await fs.writeFile(path.join(out, 'artifact.json'), `${JSON.stringify({ tarball, integrity: record.integrity, name: record.name, version: record.version }, null, 2)}\n`)
  if (process.env.DISPAT_OUTPUT) await fs.appendFile(process.env.DISPAT_OUTPUT, `TARBALL=${tarball}\nINTEGRITY=${record.integrity}\nPACKED_NAME=${record.name}\nPACKED_VERSION=${record.version}\n`)
  process.stdout.write(`${tarball}\n`)
}

if (isMain(import.meta.url)) main().catch(error => { console.error(error instanceof Error ? error.message : String(error)); process.exitCode = 1 })
export { main, parsePackOutput }
