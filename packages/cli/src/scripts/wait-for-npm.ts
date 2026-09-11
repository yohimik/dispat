import { setTimeout as sleep } from 'node:timers/promises'

interface WaitOptions {
  fetch?: typeof fetch
  now?: () => number
  sleep?: (milliseconds: number) => Promise<unknown>
  log?: (message: string) => void
  timeoutMs?: number
  intervalMs?: number
  requestTimeoutMs?: number
}

interface NpmVersion {
  name?: string
  version?: string
  dist?: { tarball?: string; integrity?: string }
}

interface NpmMetadata {
  name?: string
  versions?: Record<string, NpmVersion>
}

// This belongs in the independent post-release check, after release records.
// It must never turn a successful npm upload into a failed publish stage.
export async function waitForNpm(version: string | undefined, options: WaitOptions = {}): Promise<void> {
  if (!version || !/^\d+\.\d+\.\d+(?:-[\w.-]+)?(?:\+[\w.-]+)?$/.test(version)) {
    throw new Error('An exact npm version is required')
  }
  const request = options.fetch ?? fetch
  const now = options.now ?? Date.now
  const pause = options.sleep ?? sleep
  const log = options.log ?? console.log
  const timeout = options.timeoutMs ?? 300_000
  const interval = options.intervalMs ?? 10_000
  const requestTimeout = options.requestTimeoutMs ?? 15_000
  for (const value of [timeout, interval, requestTimeout]) {
    if (!Number.isSafeInteger(value) || value <= 0) throw new Error('Wait durations must be positive integers')
  }
  const deadline = now() + timeout
  let reason = 'version missing from npm metadata'
  while (now() < deadline) {
    let response: Response | undefined
    let metadata: NpmMetadata | undefined
    try {
      response = await request('https://registry.npmjs.org/@dispat%2fbin', {
        headers: { accept: 'application/vnd.npm.install-v1+json', 'cache-control': 'no-cache' },
        cache: 'no-store',
        signal: AbortSignal.timeout(Math.max(1, Math.min(requestTimeout, deadline - now())))
      })
      if (response.ok) metadata = await response.json() as NpmMetadata
      else await response.body?.cancel()
    } catch (error) {
      if (!(error instanceof TypeError) && !(error instanceof DOMException && ['TimeoutError', 'AbortError'].includes(error.name))) throw error
      reason = error.message
    }
    if (response && !response.ok) {
      if (response.status !== 404 && response.status !== 429 && response.status < 500) {
        throw new Error(`npm metadata request failed: HTTP ${response.status}`)
      }
      reason = `HTTP ${response.status}`
    }
    if (metadata) {
      const release: NpmVersion | undefined = metadata.versions?.[version]
      if (metadata.name === '@dispat/bin' && release?.name === '@dispat/bin' && release.version === version && release.dist?.tarball && release.dist.integrity) {
        log(`@dispat/bin@${version} is available in npm metadata`)
        return
      }
      reason = 'exact version or tarball metadata missing'
    }
    const remaining = deadline - now()
    if (remaining <= 0) break
    log(`Waiting for @dispat/bin@${version}: ${reason}`)
    await pause(Math.min(interval, remaining))
  }
  throw new Error(`Timed out waiting for @dispat/bin@${version} after ${timeout}ms: ${reason}`)
}
