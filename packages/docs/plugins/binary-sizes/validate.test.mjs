import assert from 'node:assert/strict';
import test from 'node:test';
import {validateBinarySizes} from './validate.ts';

const assets = [
  ['dispat-linux-amd64', 'linux', 'amd64', 'go'],
  ['dispat-linux-arm64', 'linux', 'arm64', 'go'],
  ['dispat-darwin-amd64', 'darwin', 'amd64', 'go'],
  ['dispat-darwin-arm64', 'darwin', 'arm64', 'go'],
  ['dispat-windows-amd64.exe', 'windows', 'amd64', 'go'],
  ['dispat-windows-arm64.exe', 'windows', 'arm64', 'go'],
  ['dispat-tiny-linux-amd64', 'linux', 'amd64', 'tinygo'],
  ['dispat-tiny-linux-arm64', 'linux', 'arm64', 'tinygo'],
];

function manifest() {
  return {schemaVersion: 1, version: '1.9.0', sourceCommit: 'a'.repeat(40),
    toolchains: {go: 'go version go1.27.0 linux/amd64', tinygo: 'tinygo version 0.44.0'},
    binaries: assets.map(([name, os, arch, compiler], i) => ({name, os, arch, compiler, bytes: i + 1, sha256: 'b'.repeat(64)}))};
}

test('accepts the complete eight-asset release manifest', () => {
  assert.equal(validateBinarySizes(manifest(), '1.9.0').binaries.length, 8);
});

test('rejects stale, incomplete, duplicate, and unrecorded manifests', () => {
  assert.throws(() => validateBinarySizes(manifest(), '1.9.1'), /expected 1.9.1/);
  const missing = manifest(); missing.binaries.pop();
  assert.throws(() => validateBinarySizes(missing), /exactly 8/);
  const duplicate = manifest(); duplicate.binaries[7] = duplicate.binaries[6];
  assert.throws(() => validateBinarySizes(duplicate), /unexpected or duplicate/);
  const zero = manifest(); zero.sourceCommit = '0'.repeat(40);
  assert.throws(() => validateBinarySizes(zero), /nonzero/);
});
