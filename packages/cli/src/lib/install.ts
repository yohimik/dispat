import { createHash, randomBytes } from 'node:crypto'
import { createReadStream, createWriteStream } from 'node:fs'
import { readFile, lstat, chmod, rename, rm } from 'node:fs/promises'
import path from 'node:path'
import { pipeline } from 'node:stream/promises'
import { Transform } from 'node:stream'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { download } from '#root/lib/transport.js'
import { ASSET_NAMES, isPlatformKey, platformKey, binaryName } from '#root/lib/platform.js'
import { PACKAGE_ROOT } from '#root/lib/root.js'
import type { BinaryExecutor, DownloadResponse, InstallOptions, ReleaseAsset, ReleaseMetadata } from '#root/lib/types.js'

const execute: BinaryExecutor = promisify(execFile)
export const BASE_URL = 'https://github.com/yohimik/dispat/releases/download'
export const MAX_BINARY_SIZE = 128 * 1024 * 1024

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function isVersion(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.exec(value)
  return match !== null && (!match[4] || match[4].split('.').every(part => !/^\d+$/.test(part) || part === '0' || !part.startsWith('0')))
}

export function validateMetadata(release: unknown, key: string): ReleaseAsset {
  const asset = record(release) && record(release.assets) ? release.assets[key] : undefined
  if (!record(release) || !isVersion(release.version) || release.tag !== `services/dispat/v${release.version}` ||
      !record(asset) || !isPlatformKey(key) || asset.name !== ASSET_NAMES[key] ||
      typeof asset.size !== 'number' || !Number.isSafeInteger(asset.size) || asset.size <= 0 || asset.size > MAX_BINARY_SIZE ||
      typeof asset.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(asset.sha256)) {
    throw new Error(`release metadata is missing or invalid for ${key}`)
  }
  return { name: asset.name as string, size: asset.size, sha256: asset.sha256 }
}

export async function validBinary(file: string, version: string, exec: BinaryExecutor = execute): Promise<boolean> {
  try {
    const { stdout } = await exec(file, ['--version'], {
      encoding: 'utf8', timeout: 10_000, killSignal: 'SIGKILL', maxBuffer: 64 * 1024,
      windowsHide: true, env: { ...process.env, DISPAT_UPDATE_CHECK: '0' }
    })
    return new RegExp(`(?:^|\\n)dispat ${version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?:\\s|$)`).test(stdout.trim())
  } catch { return false }
}

async function matchesFile(file: string, asset: ReleaseAsset): Promise<boolean> {
  try {
    const stat = await lstat(file)
    if (!stat.isFile() || stat.size !== asset.size) return false
    const hash = createHash('sha256')
    let count = 0
    for await (const chunk of createReadStream(file)) {
      count += chunk.length
      if (count > asset.size) return false
      hash.update(chunk)
    }
    return count === asset.size && hash.digest('hex') === asset.sha256
  } catch { return false }
}

export async function install(options: InstallOptions = {}): Promise<{ target: string; installed: boolean }> {
  const packageDir = options.packageDir ?? PACKAGE_ROOT
  const metadata: unknown = options.release ?? JSON.parse(await readFile(path.join(packageDir, 'release.json'), 'utf8'))
  const key = platformKey(options.platform, options.arch)
  const asset = validateMetadata(metadata, key)
  const release = metadata as ReleaseMetadata
  const target = path.join(packageDir, binaryName(options.platform))
  options.log?.('debug', 'selected release asset', { version: release.version, platform: key })
  if (await matchesFile(target, asset) && await validBinary(target, release.version, options.exec)) {
    options.log?.('debug', 'existing binary verified', { target })
    return { target, installed: false }
  }
  const suffix = (options.platform ?? process.platform) === 'win32' ? '.tmp.exe' : '.tmp'
  const temp = path.join(packageDir, `.${binaryName(options.platform)}.${process.pid}.${randomBytes(6).toString('hex')}${suffix}`)
  const url = `${options.baseURL ?? BASE_URL}/${encodeURIComponent(release.tag)}/${asset.name}`
  const hash = createHash('sha256')
  let count = 0
  const verifier = new Transform({
    transform(chunk: Buffer, _encoding, callback) {
      count += chunk.length
      if (count > asset.size) return callback(new Error(`download exceeded expected size ${asset.size}`))
      hash.update(chunk)
      callback(null, chunk)
    }
  })
  let response: DownloadResponse | undefined
  try {
    options.log?.('trace', 'starting binary download', { asset: asset.name, size: asset.size })
    response = await (options.download ?? download)(url, options.transportOptions)
    const contentLength = response.headers.get('content-length')
    if (contentLength !== null && (!/^\d+$/.test(contentLength) || Number(contentLength) !== asset.size)) {
      throw new Error(`download size ${contentLength} does not match expected ${asset.size}`)
    }
    if (!response.body) throw new Error('download response has no body')
    await pipeline(response.body, verifier, createWriteStream(temp, { flags: 'wx', mode: 0o700 }))
    if (count !== asset.size) throw new Error(`download was truncated: received ${count} of ${asset.size} bytes`)
    if (hash.digest('hex') !== asset.sha256) throw new Error('download checksum did not match release metadata')
    if ((options.platform ?? process.platform) !== 'win32') await chmod(temp, 0o755)
    if (!await validBinary(temp, release.version, options.exec)) throw new Error(`downloaded executable did not report dispat ${release.version}`)
    await rename(temp, target)
    options.log?.('info', 'installed dispat', { version: release.version })
    return { target, installed: true }
  } finally {
    response?.body?.destroy()
    response?.dispatCleanup?.()
    await rm(temp, { force: true }).catch(() => options.log?.('warn', 'could not remove temporary download', { path: temp }))
  }
}
