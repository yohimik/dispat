import type {BinarySize, BinarySizesManifest, Compiler} from './types';

export const expected = new Map<string, [string, string, Compiler]>([
  ['dispat-linux-amd64', ['linux', 'amd64', 'go']],
  ['dispat-linux-arm64', ['linux', 'arm64', 'go']],
  ['dispat-darwin-amd64', ['darwin', 'amd64', 'go']],
  ['dispat-darwin-arm64', ['darwin', 'arm64', 'go']],
  ['dispat-windows-amd64.exe', ['windows', 'amd64', 'go']],
  ['dispat-windows-arm64.exe', ['windows', 'arm64', 'go']],
  ['dispat-tiny-linux-amd64', ['linux', 'amd64', 'tinygo']],
  ['dispat-tiny-linux-arm64', ['linux', 'arm64', 'tinygo']],
]);

function record(value: unknown, at: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${at}: expected object`);
  return value as Record<string, unknown>;
}

function text(value: unknown, at: string): string {
  if (typeof value !== 'string' || value.length === 0) throw new Error(`${at}: expected non-empty string`);
  return value;
}

export function validateBinarySizes(value: unknown, expectedVersion?: string): BinarySizesManifest {
  const root = record(value, 'binary sizes');
  if (root.schemaVersion !== 1 && root.schemaVersion !== 2) throw new Error('binary sizes.schemaVersion: expected 1 or 2');
  const version = text(root.version, 'binary sizes.version');
  if (expectedVersion && version !== expectedVersion) {
    throw new Error(`binary sizes.version: expected ${expectedVersion}, got ${version}`);
  }
  if (!Array.isArray(root.binaries) || root.binaries.length !== expected.size) {
    throw new Error(`binary sizes.binaries: expected exactly ${expected.size} release assets`);
  }
  const seen = new Set<string>();
  const binaries: BinarySize[] = root.binaries.map((item, index) => {
    const binary = record(item, `binary sizes.binaries[${index}]`);
    const name = text(binary.name, `binary sizes.binaries[${index}].name`);
    const want = expected.get(name);
    if (!want || seen.has(name)) throw new Error(`binary sizes.binaries[${index}].name: unexpected or duplicate ${name}`);
    seen.add(name);
    const [os, arch, compiler] = want;
    if (binary.os !== os || binary.arch !== arch || binary.compiler !== compiler) {
      throw new Error(`${name}: expected ${os}/${arch} built with ${compiler}`);
    }
    if (!Number.isSafeInteger(binary.bytes) || (binary.bytes as number) <= 0) throw new Error(`${name}.bytes: expected positive integer`);
    const sha256 = text(binary.sha256, `${name}.sha256`);
    if (!/^[0-9a-f]{64}$/.test(sha256)) throw new Error(`${name}.sha256: expected 64 lowercase hex characters`);
    return {name, os, arch, compiler, bytes: binary.bytes as number, sha256};
  });
  if (root.schemaVersion === 2) return {schemaVersion: 2, version, binaries};
  const sourceCommit = text(root.sourceCommit, 'binary sizes.sourceCommit');
  if (!/^[0-9a-f]{40}$/.test(sourceCommit) || /^0{40}$/.test(sourceCommit)) {
    throw new Error('binary sizes.sourceCommit: expected a nonzero 40-character lowercase hex commit');
  }
  const toolchains = record(root.toolchains, 'binary sizes.toolchains');
  const parsedToolchains = {
    go: text(toolchains.go, 'binary sizes.toolchains.go'),
    tinygo: text(toolchains.tinygo, 'binary sizes.toolchains.tinygo'),
  };
  return {schemaVersion: 1, version, sourceCommit, toolchains: parsedToolchains, binaries};
}
