import fs from 'node:fs';
import path from 'node:path';
import {execFileSync} from 'node:child_process';

type Node = {type?: string; url?: string; children?: Node[]};
type File = {path?: string; message?: (reason: string, node?: Node) => Error};

export type HistoricalLinksOptions = {
  siteDir: string;
  refsFile?: string;
  versionsFile?: string;
  modulesFile?: string;
};

const repositorySource = /^(https:\/\/github\.com\/yohimik\/dispat\/(?:blob|tree)\/)main(\/[^?#]*)([?#].*)?$/;
const versionedSource = /(?:^|\/)versioned_docs\/version-(\d+\.\d+)(?:\/|$)/;
const staticAsset = /\.(?:avif|gif|ico|jpe?g|mp4|png|svg|webm|webp)(?:[?#].*)?$/i;
const pkgGoDev = /^(https:\/\/pkg\.go\.dev\/github\.com\/yohimik\/dispat\/pkg\/)(ccme|config|manifest|models|scanner|writer)(\/v\d+)?([?#].*)?$/;
const explicitVersionRoute = /^\/\d+\.\d+(?:\/|$)/;

// These documents were added to old snapshots after the corresponding release.
// Their links name the immutable source that the added prose actually describes.
const sourceOverrides: Record<string, {ref: string; target?: string}> = {
  '1.3:Dockerfile.tinygo': {ref: 'd78a3e59005a0b49a178ad2ff9b688c20453743a'},
  '1.0:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.1:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.2:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.3:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.4:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.5:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
  '1.6:tests/experiments/README.md': {ref: '9a5ad365d442ad494276f191600062f68b9d3891'},
};

function sourceTarget(version: string, target: string, ref: string): {ref: string; target: string} {
  const override = sourceOverrides[`${version}:${target}`];
  if (override) return {ref: override.ref, target: override.target ?? target};
  const [major, minor] = version.split('.').map(Number);
  if (target === 'specs/ccme-spec/SPEC.md' && (major < 1 || (major === 1 && minor < 8))) {
    return {ref, target: 'pkg/ccme/SPEC.md'};
  }
  return {ref, target};
}

export type ModuleVersionResolver = (module: string, major: string | undefined, ref: string) => string;

/** Finds the newest stable module tag reachable from the documentation commit. */
export function gitModuleVersionResolver(repository: string): ModuleVersionResolver {
  const cache = new Map<string, string>();
  return (module, major, ref) => {
    const key = `${ref}:${module}:${major ?? ''}`;
    const existing = cache.get(key);
    if (existing) return existing;
    const prefix = `pkg/${module}/v`;
    const tags = execFileSync('git', ['tag', '--merged', ref, '--list', `${prefix}*`, '--sort=-version:refname'], {
      cwd: repository,
      encoding: 'utf8',
    }).split(/\r?\n/);
    const wantedMajor = major?.slice(2);
    const tag = tags.find((candidate) => {
      const match = new RegExp(`^${prefix}(\\d+)\\.\\d+\\.\\d+$`).exec(candidate);
      return match && (!wantedMajor || match[1] === wantedMajor);
    });
    if (!tag) throw new Error(`No stable pkg/${module}${major ?? ''} tag is reachable from ${ref}`);
    const version = tag.slice(`pkg/${module}/`.length);
    cache.set(key, version);
    return version;
  };
}

function routeExists(siteDir: string, version: string, pathname: string): boolean {
  const relative = pathname.replace(/^\/+|\/+$/g, '');
  if (!relative) return true;
  const base = path.join(siteDir, 'versioned_docs', `version-${version}`, relative);
  return ['.md', '.mdx', '/README.md', '/README.mdx', '/index.md', '/index.mdx'].some((suffix) =>
    fs.existsSync(`${base}${suffix}`),
  );
}

function fail(file: File, node: Node, reason: string): never {
  if (file.message) throw file.message(reason, node);
  throw new Error(reason);
}

export function rewriteHistoricalUrl(
  url: string,
  version: string,
  refs: Record<string, string>,
  siteDir: string,
  file: File = {},
  node: Node = {},
  latestVersion?: string,
  resolveModuleVersion?: ModuleVersionResolver,
  recordedModules?: Record<string, string>,
): string {
  const source = repositorySource.exec(url);
  if (source) {
    const ref = refs[version];
    if (!ref) fail(file, node, `No immutable source ref is recorded for documentation ${version}`);
    const target = source[2].slice(1);
    const resolved = sourceTarget(version, target, ref);
    return `${source[1]}${resolved.ref}/${resolved.target}${source[3] ?? ''}`;
  }
  const godoc = pkgGoDev.exec(url);
  if (godoc) {
    const ref = refs[version];
    if (!ref) fail(file, node, `No immutable source ref is recorded for documentation ${version}`);
    if (recordedModules && !recordedModules[godoc[2]]) {
      fail(file, node, `Recorded module versions for ${version} are missing ${godoc[2]}`);
    }
    const moduleVersion = recordedModules?.[godoc[2]] ?? resolveModuleVersion?.(godoc[2], godoc[3], ref);
    if (!moduleVersion) fail(file, node, `No module version was recorded or reachable for ${url}`);
    const wantedMajor = godoc[3]?.slice(2) ?? '1';
    if (!moduleVersion.startsWith(`v${wantedMajor}.`)) {
      fail(file, node, `Recorded ${godoc[2]} version ${moduleVersion} does not match module path major v${wantedMajor}`);
    }
    return `${godoc[1]}${godoc[2]}${godoc[3] ?? ''}@${moduleVersion}${godoc[4] ?? ''}`;
  }
  if (!url.startsWith('/') || url.startsWith('//') || url === '/' || explicitVersionRoute.test(url) || staticAsset.test(url)) return url;
  const match = /^([^?#]*)([?#].*)?$/.exec(url);
  const pathname = match?.[1] ?? url;
  if (!routeExists(siteDir, version, pathname)) {
    fail(file, node, `Historical documentation ${version} links to current-only route ${url}`);
  }
  return `${version === latestVersion ? '' : `/${version}`}${pathname}${match?.[2] ?? ''}`;
}

export default function remarkHistoricalLinks(options: HistoricalLinksOptions) {
  const refsFile = options.refsFile ?? path.join(options.siteDir, 'plugins', 'historical-links', 'refs.json');
  const versionsFile = options.versionsFile ?? path.join(options.siteDir, 'versions.json');
  const modulesFile = options.modulesFile ?? path.join(options.siteDir, 'plugins', 'historical-links', 'modules.json');
  const resolveModuleVersion = gitModuleVersionResolver(options.siteDir);
  return (tree: Node, file: File) => {
    const match = file.path ? versionedSource.exec(file.path) : null;
    if (!match) return;
    const version = match[1];
    const refs = JSON.parse(fs.readFileSync(refsFile, 'utf8')) as Record<string, string>;
    const versions = JSON.parse(fs.readFileSync(versionsFile, 'utf8')) as string[];
    const recorded = fs.existsSync(modulesFile)
      ? JSON.parse(fs.readFileSync(modulesFile, 'utf8')) as Record<string, Record<string, string>>
      : {};
    const latestVersion = versions[0];
    const visit = (node: Node): void => {
      if ((node.type === 'link' || node.type === 'image') && node.url) {
        node.url = rewriteHistoricalUrl(node.url, version, refs, options.siteDir, file, node, latestVersion, resolveModuleVersion, recorded[version]);
      }
      node.children?.forEach(visit);
    };
    visit(tree);
  };
}
