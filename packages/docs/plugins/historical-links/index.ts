import {execFile} from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import {promisify} from 'node:util';

import type {LoadContext, Plugin} from '@docusaurus/types';

const exec = promisify(execFile);
const RELEASE_VERSION = 'DISPAT_DOCS_REPORT_VERSION';
const REQUIRE_REPORT = 'DISPAT_DOCS_REQUIRE_REPORT';
const MODULE_VERSIONS = 'DISPAT_DOCS_MODULE_VERSIONS';
const modules = ['ccme', 'config', 'manifest', 'models', 'scanner', 'writer'] as const;

export type PlannedModuleVersions = Record<(typeof modules)[number], string>;

export function parsePlannedModuleVersions(value: string): PlannedModuleVersions {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error(`${MODULE_VERSIONS} must be a JSON object`);
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(`${MODULE_VERSIONS} must be a JSON object`);
  }
  const record = parsed as Record<string, unknown>;
  for (const module of modules) {
    const version = record[module];
    if (typeof version !== 'string' || !/^v\d+\.\d+\.\d+$/.test(version)) {
      throw new Error(`${MODULE_VERSIONS}.${module} must be a stable version such as v1.2.0`);
    }
  }
  const unknown = Object.keys(record).filter((name) => !modules.includes(name as (typeof modules)[number]));
  if (unknown.length > 0) throw new Error(`${MODULE_VERSIONS} contains unknown modules: ${unknown.join(', ')}`);
  return Object.fromEntries(modules.map((module) => [module, record[module]])) as PlannedModuleVersions;
}

export async function updateReleaseRef(siteDir: string, version: string, commit: string): Promise<void> {
  if (!/^\d+\.\d+$/.test(version)) throw new Error(`${RELEASE_VERSION} must be a stable minor such as 1.8`);
  if (!/^[0-9a-f]{40}$/.test(commit)) throw new Error(`Historical documentation ref must be a full Git commit`);
  const versions = JSON.parse(await fs.readFile(path.join(siteDir, 'versions.json'), 'utf8')) as unknown;
  if (!Array.isArray(versions) || !versions.every((entry) => typeof entry === 'string') || !versions.includes(version)) {
    throw new Error(`${RELEASE_VERSION}=${version} is not present in versions.json`);
  }
  const refsPath = path.join(siteDir, 'plugins', 'historical-links', 'refs.json');
  const refs = JSON.parse(await fs.readFile(refsPath, 'utf8')) as Record<string, string>;
  refs[version] = commit;
  const ordered = Object.fromEntries(Object.entries(refs).sort(([a], [b]) =>
    a.localeCompare(b, undefined, {numeric: true}),
  ));
  const temporary = `${refsPath}.${process.pid}.tmp`;
  try {
    await fs.writeFile(temporary, `${JSON.stringify(ordered, null, 2)}\n`);
    await fs.rename(temporary, refsPath);
  } finally {
    await fs.rm(temporary, {force: true});
  }
}

export async function updateReleaseModules(siteDir: string, version: string, planned: PlannedModuleVersions): Promise<void> {
  const modulesPath = path.join(siteDir, 'plugins', 'historical-links', 'modules.json');
  let recorded: Record<string, PlannedModuleVersions> = {};
  try {
    recorded = JSON.parse(await fs.readFile(modulesPath, 'utf8')) as Record<string, PlannedModuleVersions>;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
  }
  recorded[version] = planned;
  const ordered = Object.fromEntries(Object.entries(recorded).sort(([a], [b]) =>
    a.localeCompare(b, undefined, {numeric: true}),
  ));
  const temporary = `${modulesPath}.${process.pid}.tmp`;
  try {
    await fs.writeFile(temporary, `${JSON.stringify(ordered, null, 2)}\n`);
    await fs.rename(temporary, modulesPath);
  } finally {
    await fs.rm(temporary, {force: true});
  }
}

/** Records the release checkout before historical Markdown is compiled. */
export default function historicalLinks(context: LoadContext): Plugin {
  return {
    name: 'historical-links',
    async loadContent() {
      const version = process.env[RELEASE_VERSION];
      if (!version) return null;
      if (process.env[REQUIRE_REPORT] !== '1') {
        throw new Error(`${RELEASE_VERSION} is only valid when ${REQUIRE_REPORT}=1`);
      }
      const plannedValue = process.env[MODULE_VERSIONS];
      if (!plannedValue) throw new Error(`${MODULE_VERSIONS} is required for a stable documentation release`);
      const planned = parsePlannedModuleVersions(plannedValue);
      const {stdout} = await exec('git', ['rev-parse', 'HEAD'], {cwd: context.siteDir});
      await updateReleaseRef(context.siteDir, version, stdout.trim());
      await updateReleaseModules(context.siteDir, version, planned);
      return null;
    },
  };
}
