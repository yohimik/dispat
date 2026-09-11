#!/usr/bin/env node
'use strict'

import fs from 'node:fs/promises'
import path from 'node:path'
import makeFetchHappen from 'make-fetch-happen'
import { PACKAGE_ROOT, isMain } from '#root/lib/root.js'
import { ASSET_NAMES } from '#root/lib/platform.js'
import { isVersion, validateMetadata } from '#root/lib/install.js'
import type { PlatformKey } from '#root/lib/platform.js'

const platforms = Object.entries(ASSET_NAMES) as [PlatformKey, string][]
const API_TIMEOUT_MS = 30_000
const MAX_API_RESPONSE_SIZE = 2 * 1024 * 1024

interface ReleaseAsset { name: string, size: number, digest: string }
export interface GitHubRelease { tag_name?: string, assets?: ReleaseAsset[] }
interface FetchResponse {
  ok: boolean
  status: number
  body?: AsyncIterable<Uint8Array> & { destroy?(): void }
  json?(): Promise<GitHubRelease>
}
interface PackageReleaseOptions {
  version?: string
  api?: string
  dir?: string
  fetch?: (url: string, options: Record<string, unknown>) => Promise<FetchResponse>
}

async function main(options: PackageReleaseOptions = {}) {
  const version = options.version || process.env.DISPAT_WORKSPACE_DISPAT_VERSION
  if (!isVersion(version)) throw new Error('DISPAT_WORKSPACE_DISPAT_VERSION must be an exact semantic version')
  const tag = `services/dispat/v${version}`
  const api = `${options.api || 'https://api.github.com/repos/yohimik/dispat/releases/tags'}/${encodeURIComponent(tag)}`
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), API_TIMEOUT_MS)
  let response: FetchResponse | undefined
  try {
    response = await (options.fetch || makeFetchHappen as unknown as PackageReleaseOptions['fetch'])!(api, {
      cache: 'no-store', retry: { retries: 2 }, timeout: API_TIMEOUT_MS, signal: controller.signal,
      headers: { accept: 'application/vnd.github+json', 'user-agent': '@dispat/cli release packager' }
    })
    if (!response.ok) throw new Error(`GitHub release ${tag} is unavailable: ${response.status}`)
    const release = await readRelease(response)
    if (release.tag_name !== tag) throw new Error(`GitHub returned tag ${release.tag_name || '<missing>'}; expected ${tag}`)
    const names = (release.assets || []).map((asset: ReleaseAsset) => asset.name)
    if (new Set(names).size !== names.length) throw new Error(`GitHub release ${tag} contains duplicate asset names`)
    const byName = new Map<string, ReleaseAsset>((release.assets || []).map((asset: ReleaseAsset) => [asset.name, asset]))
    const assets: Record<string, { name: string, size: number, sha256: string }> = {}
    for (const [key, name] of platforms) {
      const asset = byName.get(name)
      const digest = asset && /^sha256:([a-f0-9]{64})$/.exec(asset.digest || '')
      if (!asset || !digest) throw new Error(`GitHub release ${tag} has no complete metadata for ${name}`)
      assets[key] = { name, size: asset.size, sha256: digest[1] }
      try { validateMetadata({ version, tag, assets }, key) }
      catch { throw new Error(`GitHub release ${tag} has no complete metadata for ${name}`) }
    }
    const dir = options.dir || PACKAGE_ROOT
    await fs.writeFile(path.join(dir, 'release.json'), `${JSON.stringify({ version, tag, assets }, null, 2)}\n`)
  } finally {
    clearTimeout(timer)
    response?.body?.destroy?.()
    controller.abort()
  }
}

async function readRelease(response: FetchResponse): Promise<GitHubRelease> {
  if (!response.body) {
    if (!response.json) throw new Error('GitHub release response has no body')
    return response.json()
  }
  const chunks: Buffer[] = []
  let size = 0
  for await (const chunk of response.body) {
    size += chunk.byteLength
    if (size > MAX_API_RESPONSE_SIZE) throw new Error(`GitHub release response exceeded ${MAX_API_RESPONSE_SIZE} bytes`)
    chunks.push(Buffer.from(chunk))
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8')) as GitHubRelease
}

if (isMain(import.meta.url)) main().catch(error => { console.error(`dispat npm packager: ${error.message}`); process.exitCode = 1 })
export { main, platforms, readRelease }
