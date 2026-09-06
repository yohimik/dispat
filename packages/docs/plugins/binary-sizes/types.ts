export type Compiler = 'go' | 'tinygo';

export interface BinarySize {
  name: string;
  os: string;
  arch: string;
  compiler: Compiler;
  bytes: number;
  sha256: string;
}

export interface BinarySizesManifest {
  schemaVersion: 1;
  version: string;
  sourceCommit: string;
  toolchains: {go: string; tinygo: string};
  binaries: BinarySize[];
}

export interface BinarySizesData {
  manifest: BinarySizesManifest | null;
  currentVersions: string[];
  archives: Record<string, BinarySizesManifest>;
}
