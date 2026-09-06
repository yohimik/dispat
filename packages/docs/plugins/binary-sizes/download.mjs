#!/usr/bin/env node
import fs from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import {pathToFileURL} from 'node:url';

const MAX_BYTES = 64 * 1024;
const TIMEOUT_MS = 60_000;

export async function download(version, destination, base = 'https://github.com/yohimik/dispat/releases/download') {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error(`invalid Dispat version: ${version}`);
  const tag = encodeURIComponent(`services/dispat/v${version}`);
  const url = `${base}/${tag}/dispat-binary-sizes.json`;
  const temporary = `${destination}.${process.pid}.${randomUUID()}.tmp`;
  try {
    const response = await fetch(url, {redirect: 'follow', signal: AbortSignal.timeout(TIMEOUT_MS)});
    if (!response.ok) throw new Error(`binary-size manifest download failed: HTTP ${response.status}`);
    const declared = Number(response.headers.get('content-length'));
    if (Number.isFinite(declared) && declared > MAX_BYTES) throw new Error(`binary-size manifest exceeds ${MAX_BYTES} bytes`);
    if (!response.body) throw new Error('binary-size manifest response has no body');
    const chunks = [];
    let bytes = 0;
    for await (const chunk of response.body) {
      bytes += chunk.byteLength;
      if (bytes > MAX_BYTES) throw new Error(`binary-size manifest exceeds ${MAX_BYTES} bytes`);
      chunks.push(chunk);
    }
    await fs.writeFile(temporary, Buffer.concat(chunks));
    await fs.rename(temporary, destination);
  } finally {
    await fs.rm(temporary, {force: true});
  }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [, , version, destination] = process.argv;
  if (!version || !destination) {
    console.error('usage: download.mjs VERSION DESTINATION');
    process.exitCode = 2;
  } else {
    download(version, destination).catch((cause) => {
      console.error(cause instanceof Error ? cause.message : String(cause));
      process.exitCode = 1;
    });
  }
}
