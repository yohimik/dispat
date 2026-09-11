import test from 'node:test'
import assert from 'node:assert/strict'
import { Readable } from 'node:stream'
import { download, MAX_REDIRECTS } from '#root/lib/transport.js'
import type { DownloadResponse } from '#root/lib/types.js'

function response(status = 200, location: string | null = null): DownloadResponse {
  return { ok: status === 200, status, headers: { get: name => name === 'location' ? location : null }, body: Readable.from('') }
}

test('returns a successful response and retains deadline until cleanup', async () => {
  const result = response()
  assert.equal(await download('https://example.test/x', { fetch: async () => result }), result)
  assert.equal(typeof result.dispatCleanup, 'function')
  result.dispatCleanup?.()
  assert.equal(result.body?.destroyed, true)
})

test('follows bounded HTTPS redirects and closes each abandoned body', async () => {
  const redirected = response(302, '/next')
  let calls = 0
  let finalURL = ''
  const result = await download('https://a.test/x', { fetch: async url => {
    calls++
    finalURL = String(url)
    return calls === 1 ? redirected : response()
  } })
  result.dispatCleanup?.()
  assert.equal(calls, 2)
  assert.equal(finalURL, 'https://a.test/next')
  assert.equal(redirected.body?.destroyed, true)
})

test('rejects insecure URLs, downgrades, malformed redirects and statuses', async () => {
  await assert.rejects(download('http://a.test/x'), /HTTPS/)
  await assert.rejects(download('https://a.test/x', { allowHTTP: true, fetch: async () => response(302, 'http://a.test/y') }), /downgrade/)
  await assert.rejects(download('https://a.test/x', { fetch: async () => response(302) }), /no location/)
  await assert.rejects(download('http://a.test/x', { allowHTTP: true, fetch: async () => response(302, 'file:///tmp/binary') }), /HTTPS/)
  const failed = response(503)
  await assert.rejects(download('https://a.test/x', { fetch: async () => failed }), /503/)
  assert.equal(failed.body?.destroyed, true)
  let calls = 0
  await assert.rejects(download('https://a.test/x', { fetch: async () => { calls++; return response(302, '/x') } }), /redirects/)
  assert.equal(calls, MAX_REDIRECTS + 1)
})

test('aborts a request at the deadline', async () => {
  await assert.rejects(download('https://a.test/x', { timeout: 5, fetch: (_url, options) => new Promise((_resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(new Error('aborted')))
  }) }), /aborted/)
})
