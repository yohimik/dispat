import test from 'node:test'
import assert from 'node:assert/strict'
import { createServer as createHTTPServer } from 'node:http'
import { createServer as createHTTPSServer } from 'node:https'
import { connect, type AddressInfo, type Socket } from 'node:net'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { download } from '#root/lib/transport.js'
import { PACKAGE_ROOT } from '#root/lib/root.js'

// This key is a public test fixture, never a credential for a deployed service.
test('npm proxy and custom CA settings support a real HTTPS tunnel', async t => {
  const certificate = join(PACKAGE_ROOT, 'test/fixtures/localhost-cert.pem')
  const [key, cert] = await Promise.all([
    readFile(join(PACKAGE_ROOT, 'test/fixtures/localhost-key.pem')), readFile(certificate)
  ])
  const sockets = new Set<Socket>()
  const track = (socket: Socket) => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)) }
  const origin = createHTTPSServer({ key, cert }, (_request, response) => response.end('verified HTTPS fixture'))
  origin.on('connection', track)
  await new Promise<void>(resolve => origin.listen(0, '127.0.0.1', resolve))
  const originPort = (origin.address() as AddressInfo).port
  let tunnels = 0
  const proxy = createHTTPServer()
  proxy.on('connection', track)
  proxy.on('connect', (request, socket, head) => {
    tunnels++
    assert.equal(request.url, `dispat.fixture.invalid:${originPort}`)
    const upstream = connect(originPort, '127.0.0.1', () => {
      socket.write('HTTP/1.1 200 Connection Established\r\n\r\n')
      if (head.length) upstream.write(head)
      socket.pipe(upstream).pipe(socket)
    })
    track(upstream)
    socket.on('error', () => upstream.destroy())
    upstream.on('error', () => socket.destroy())
    socket.on('close', () => upstream.destroy())
  })
  await new Promise<void>(resolve => proxy.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const socket of sockets) socket.destroy()
    await Promise.all([
      new Promise<void>(resolve => proxy.close(() => resolve())),
      new Promise<void>(resolve => origin.close(() => resolve()))
    ])
  })
  const response = await download(`https://dispat.fixture.invalid:${originPort}/binary`, { timeout: 3000, env: {
    npm_config_https_proxy: `http://127.0.0.1:${(proxy.address() as AddressInfo).port}`,
    npm_config_cafile: certificate,
    npm_config_noproxy: ''
  } })
  try {
    const chunks: Buffer[] = []
    assert.ok(response.body)
    for await (const chunk of response.body) chunks.push(Buffer.from(chunk))
    assert.equal(Buffer.concat(chunks).toString(), 'verified HTTPS fixture')
    assert.equal(tunnels, 1)
  } finally { response.dispatCleanup?.() }
})
