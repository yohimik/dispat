import type { ExecFileOptionsWithStringEncoding } from 'node:child_process'
import type { Readable } from 'node:stream'
import type { PlatformKey } from '#root/lib/platform.js'

export interface ReleaseAsset { name: string; size: number; sha256: string }
export interface ReleaseMetadata {
  version: string
  tag: string
  assets: Partial<Record<PlatformKey, ReleaseAsset>>
}
export interface DownloadResponse {
  ok: boolean
  status: number
  statusText?: string
  headers: { get(name: string): string | null }
  body: Readable | null
  dispatCleanup?: () => void
}
export interface FetchOptions {
  cache: 'no-store'
  redirect: 'manual'
  retry: { retries: number; factor: number; minTimeout: number; maxTimeout: number }
  signal: AbortSignal
  headers: Record<string, string>
  proxy?: string
  noProxy?: string
  ca?: Buffer
}
export type Fetch = (url: URL, options: FetchOptions) => Promise<DownloadResponse>
export interface TransportOptions {
  fetch?: Fetch
  timeout?: number
  /** Only local fixture servers may opt into HTTP. HTTPS downgrades are always rejected. */
  allowHTTP?: boolean
  env?: NodeJS.ProcessEnv
}
export type Downloader = (url: string, options?: TransportOptions) => Promise<DownloadResponse>
export type BinaryExecutor = (file: string, args: string[], options: ExecFileOptionsWithStringEncoding) => Promise<{ stdout: string }>
export type InstallerLog = (level: 'debug' | 'trace' | 'info' | 'warn', message: string, fields: Record<string, string | number>) => void
export interface InstallOptions {
  packageDir?: string
  release?: unknown
  platform?: string
  arch?: string
  exec?: BinaryExecutor
  download?: Downloader
  baseURL?: string
  transportOptions?: TransportOptions
  log?: InstallerLog
}
