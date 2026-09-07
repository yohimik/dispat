#!/usr/bin/env node
import fs from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {expected, validateBinarySizes} from './validate.ts';

const MAX_BYTES = 256 * 1024;
const TIMEOUT_MS = 60_000;

export async function download(version, destination, base = 'https://api.github.com/repos/yohimik/dispat') {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error(`invalid Dispat version: ${version}`);
  const tag = encodeURIComponent(`services/dispat/v${version}`);
  const url = `${base}/releases/tags/${tag}`;
  const temporary = `${destination}.${process.pid}.${randomUUID()}.tmp`;
  try {
    const headers = {Accept: 'application/vnd.github+json'};
    if (process.env.GITHUB_TOKEN && new URL(base).origin === 'https://api.github.com') {
      headers.Authorization = `Bearer ${process.env.GITHUB_TOKEN}`;
    }
    const response = await fetch(url, {headers, redirect: 'error', signal: AbortSignal.timeout(TIMEOUT_MS)});
    if (!response.ok) throw new Error(`release metadata download failed: HTTP ${response.status}`);
    const declared = Number(response.headers.get('content-length'));
    if (Number.isFinite(declared) && declared > MAX_BYTES) throw new Error(`release metadata exceeds ${MAX_BYTES} bytes`);
    if (!response.body) throw new Error('release metadata response has no body');
    const chunks = [];
    let bytes = 0;
    for await (const chunk of response.body) {
      bytes += chunk.byteLength;
      if (bytes > MAX_BYTES) throw new Error(`release metadata exceeds ${MAX_BYTES} bytes`);
      chunks.push(chunk);
    }
    const release = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    if (release.tag_name !== `services/dispat/v${version}` || release.draft !== false) {
      throw new Error('release metadata: expected the exact published release tag');
    }
    if (!Array.isArray(release.assets)) throw new Error('release metadata: missing assets');
    const binaries = release.assets.filter((asset) => expected.has(asset?.name)).map((asset) => {
      if (asset.state !== 'uploaded' || !/^sha256:[0-9a-f]{64}$/.test(asset.digest ?? '')) {
        throw new Error(`${asset.name}: expected uploaded asset with SHA-256 digest`);
      }
      const [os, arch, compiler] = expected.get(asset.name);
      return {name: asset.name, os, arch, compiler, bytes: asset.size, sha256: asset.digest.slice(7)};
    }).sort((a, b) => a.name.localeCompare(b.name, 'en'));
    const manifest = validateBinarySizes({schemaVersion: 2, version, binaries}, version);
    await fs.writeFile(temporary, `${JSON.stringify(manifest, null, 2)}\n`);
    await fs.rename(temporary, destination);
  } finally {
    await fs.rm(temporary, {force: true});
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
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
