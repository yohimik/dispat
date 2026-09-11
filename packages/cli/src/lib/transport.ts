import { readFile } from 'node:fs/promises'
import makeFetchHappen from 'make-fetch-happen'
import type { DownloadResponse, Fetch, TransportOptions } from '#root/lib/types.js'

const fetch = makeFetchHappen as unknown as Fetch
export const MAX_REDIRECTS = 5
export const MAX_RETRIES = 2
export const TIMEOUT = 30_000

export async function download(url: string, options: TransportOptions = {}): Promise<DownloadResponse> {
  const request = options.fetch ?? fetch
  let current = new URL(url)
  if (current.protocol !== 'https:' && !(options.allowHTTP && current.protocol === 'http:')) throw new Error('download URL must use HTTPS')
  const timeout = options.timeout ?? TIMEOUT
  if (!Number.isSafeInteger(timeout) || timeout <= 0) throw new Error('download timeout must be a positive integer')
  const env = options.env ?? process.env
  const ca = env.npm_config_cafile ? await readFile(env.npm_config_cafile) : undefined
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeout)
  let active: DownloadResponse | undefined
  const cleanup = () => {
    clearTimeout(timer)
    active?.body?.destroy()
    controller.abort()
  }
  try {
    for (let redirects = 0; ; redirects++) {
      const response = await request(current, {
        cache: 'no-store', redirect: 'manual',
        retry: { retries: MAX_RETRIES, factor: 2, minTimeout: 250, maxTimeout: 2000 },
        signal: controller.signal,
        headers: { 'accept-encoding': 'identity' },
        proxy: env.npm_config_https_proxy || env.npm_config_proxy,
        noProxy: env.npm_config_noproxy,
        ca
      })
      active = response
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        response.body?.destroy()
        active = undefined
        if (redirects >= MAX_REDIRECTS) throw new Error(`download exceeded ${MAX_REDIRECTS} redirects`)
        const location = response.headers.get('location')
        if (!location) throw new Error('download redirect had no location')
        const next = new URL(location, current)
        if (current.protocol === 'https:' && next.protocol !== 'https:') throw new Error('download refused an HTTPS downgrade')
        if (next.protocol !== 'https:' && !(options.allowHTTP && next.protocol === 'http:')) throw new Error('download URL must use HTTPS')
        current = next
        continue
      }
      if (!response.ok) throw new Error(`download failed: ${response.status} ${response.statusText ?? ''}`)
      response.dispatCleanup = cleanup
      return response
    }
  } catch (error) {
    cleanup()
    throw error
  }
}
