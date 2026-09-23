#!/usr/bin/env node
'use strict'

import fs from 'node:fs/promises'
import path from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import makeFetchHappen from 'make-fetch-happen'
import { PACKAGE_ROOT, isMain } from '#root/lib/root.js'
import { ASSET_NAMES } from '#root/lib/platform.js'
import { isVersion, validateMetadata } from '#root/lib/install.js'
import type { PlatformKey } from '#root/lib/platform.js'

const platforms = Object.entries(ASSET_NAMES) as [PlatformKey, string][]
const RELEASES_API = 'https://api.github.com/repos/yohimik/dispat/releases/tags'
const GITHUB_API_HOST = 'api.github.com'
const API_TIMEOUT_MS = 30_000
const MAX_API_RESPONSE_SIZE = 2 * 1024 * 1024
const MAX_REQUEST_ATTEMPTS = 3
const MAX_RATE_LIMIT_WAIT_MS = 60_000
const MAX_ERROR_MESSAGE_LENGTH = 200

interface ReleaseAsset { name: string, size: number, digest: string }
export interface GitHubRelease { tag_name?: string, assets?: ReleaseAsset[] }
interface GitHubErrorDocument { message?: unknown }
interface ResponseHeaders { get(name: string): string | null }
interface FetchResponse {
  ok: boolean
  status: number
  headers?: ResponseHeaders
  body?: AsyncIterable<Uint8Array> & { destroy?(): void }
  json?(): Promise<GitHubRelease>
}
interface PackageReleaseOptions {
  version?: string
  api?: string
  dir?: string
  fetch?: (url: string, options: Record<string, unknown>) => Promise<FetchResponse>
  sleep?: (milliseconds: number) => Promise<unknown>
  now?: () => number
  warn?: (message: string) => void
}
interface RequestHeadersOptions { url: string, token?: string }
interface ReleaseAttemptOptions extends Required<Pick<PackageReleaseOptions, 'fetch' | 'now'>> {
  url: string
  tag: string
  headers: Record<string, string>
}
interface ReleaseRequestOptions extends ReleaseAttemptOptions, Required<Pick<PackageReleaseOptions, 'sleep' | 'warn'>> {
  attempt?: number
}
interface ReleaseResponseOptions { response: FetchResponse, tag: string, now: () => number }
interface RateLimitWaitOptions { status: number, headers?: ResponseHeaders, now: number }
interface ReleaseFound { release: GitHubRelease }
interface ReleaseRefused { reason: string, waitMs?: number }

async function main(options: PackageReleaseOptions = {}): Promise<void> {
  const {
    version = process.env.DISPAT_WORKSPACE_DISPAT_VERSION,
    api = RELEASES_API,
    dir = PACKAGE_ROOT,
    fetch = makeFetchHappen as unknown as NonNullable<PackageReleaseOptions['fetch']>,
    sleep = delay,
    now = Date.now,
    warn = console.warn
  } = options
  if (!isVersion(version)) throw new Error('DISPAT_WORKSPACE_DISPAT_VERSION must be an exact semantic version')
  const tag = `services/dispat/v${version}`
  const url = `${api}/${encodeURIComponent(tag)}`
  const headers = createRequestHeaders({ url, token: process.env.GITHUB_TOKEN })
  const release = await requestRelease({ url, tag, headers, fetch, sleep, now, warn })
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
  await fs.writeFile(path.join(dir, 'release.json'), `${JSON.stringify({ version, tag, assets }, null, 2)}\n`)
}

// The release job exports GITHUB_TOKEN, and an anonymous lookup shares the runner address's small hourly quota with
// every other anonymous caller there. The token goes only to GitHub's own API over HTTPS: an overridden api (a
// mirror, or a test server) never receives it, and make-fetch-happen drops the header when a redirect leaves the host.
function createRequestHeaders(options: RequestHeadersOptions): Record<string, string> {
  const { url, token } = options
  const headers = { accept: 'application/vnd.github+json', 'user-agent': '@dispat/bin release packager' }
  if (!token || !isGitHubApi(url)) return headers
  return { ...headers, authorization: `Bearer ${token}` }
}

function isGitHubApi(url: string): boolean {
  const target = new URL(url)
  return target.protocol === 'https:' && target.host === GITHUB_API_HOST
}

// make-fetch-happen retries timeouts and server errors itself, but it returns a 403 at once and retries a 429 on its
// own schedule, without reading when GitHub will take the next request. A rate-limited answer is therefore retried
// here, after the wait the response names, and the last refusal fails with GitHub's own explanation.
async function requestRelease(options: ReleaseRequestOptions): Promise<GitHubRelease> {
  const { attempt = 1, sleep, warn, ...attemptOptions } = options
  const lookup = await attemptRelease(attemptOptions)
  if ('release' in lookup) return lookup.release
  if (lookup.waitMs === undefined || attempt >= MAX_REQUEST_ATTEMPTS) throw new Error(lookup.reason)
  const seconds = Math.ceil(lookup.waitMs / 1000)
  warn(`dispat npm packager: ${lookup.reason}; retrying in ${seconds}s (attempt ${attempt + 1} of ${MAX_REQUEST_ATTEMPTS})`)
  await sleep(lookup.waitMs)
  return requestRelease({ ...options, attempt: attempt + 1 })
}

// One attempt owns its deadline, its abort signal and its response body, and releases all three before the caller
// waits for the next attempt.
async function attemptRelease(options: ReleaseAttemptOptions): Promise<ReleaseFound | ReleaseRefused> {
  const { url, tag, headers, fetch, now } = options
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), API_TIMEOUT_MS)
  try {
    const response = await fetch(url, {
      cache: 'no-store', retry: { retries: 2 }, timeout: API_TIMEOUT_MS, signal: controller.signal, headers
    })
    try {
      return await inspectReleaseResponse({ response, tag, now })
    } finally {
      response.body?.destroy?.()
    }
  } finally {
    clearTimeout(timer)
    controller.abort()
  }
}

async function inspectReleaseResponse(options: ReleaseResponseOptions): Promise<ReleaseFound | ReleaseRefused> {
  const { response, tag, now } = options
  if (response.ok) return { release: await readRelease(response) }
  const message = await readGitHubMessage(response)
  return {
    reason: `GitHub release ${tag} is unavailable: ${response.status}${message ? ` ${message}` : ''}`,
    waitMs: calculateRateLimitWait({ status: response.status, headers: response.headers, now: now() })
  }
}

// GitHub refuses with a 403 or 429 when a rate limit is spent, and says when to come back: retry-after in seconds,
// or x-ratelimit-reset in epoch seconds once x-ratelimit-remaining is 0. Without either it asks for a minute, which
// is also the longest wait. Undefined means the refusal is no rate limit, so another attempt would get the same answer.
function calculateRateLimitWait(options: RateLimitWaitOptions): number | undefined {
  const { status, headers, now } = options
  if ((status !== 403 && status !== 429) || !headers) return undefined
  const retryAfter = headers.get('retry-after')
  const isExhausted = headers.get('x-ratelimit-remaining') === '0'
  if (retryAfter === null && !isExhausted) return undefined
  if (retryAfter !== null && /^\d+$/.test(retryAfter)) return clampRateLimitWait(Number(retryAfter) * 1000)
  const reset = headers.get('x-ratelimit-reset') ?? ''
  if (isExhausted && /^\d+$/.test(reset)) return clampRateLimitWait(Number(reset) * 1000 - now)
  return MAX_RATE_LIMIT_WAIT_MS
}

function clampRateLimitWait(milliseconds: number): number {
  return Math.min(Math.max(milliseconds, 0), MAX_RATE_LIMIT_WAIT_MS)
}

// GitHub explains a refusal in a small JSON document ({"message": ...}), read through the same bounded reader as a
// release. The message only adds context: a response that is not JSON, cannot be read or carries no message still
// fails with its status.
async function readGitHubMessage(response: FetchResponse): Promise<string> {
  if (!response.headers?.get('content-type')?.includes('json')) return ''
  try {
    return formatGitHubMessage(await readRelease(response))
  } catch {
    return ''
  }
}

// The message reaches a CI log, so control characters and line breaks collapse to spaces and the length is bounded.
function formatGitHubMessage(document: unknown): string {
  if (typeof document !== 'object' || document === null) return ''
  const { message } = document as GitHubErrorDocument
  if (typeof message !== 'string') return ''
  const text = message.replace(/[\s\u0000-\u001f\u007f-\u009f]+/g, ' ').trim()
  if (text.length <= MAX_ERROR_MESSAGE_LENGTH) return text
  return `${text.slice(0, MAX_ERROR_MESSAGE_LENGTH)}...`
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
export { main, platforms, readRelease, calculateRateLimitWait }
