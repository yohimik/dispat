import fs from 'node:fs/promises';
import path from 'node:path';
import logger from '@docusaurus/logger';
import type {LoadContext, Plugin} from '@docusaurus/types';
import {BINARY_SIZES_PLUGIN} from './name';
import type {BinarySizesData} from './types';
import {validateBinarySizes} from './validate';

const INPUT = path.join('data', 'binary-sizes.json');
const ARCHIVES = path.join('static', 'binary-sizes');

interface ReadArchiveNamesOptions {
  archives: string;
}

/** The archive folder's entries; none when the folder is absent. */
async function readArchiveNames(options: ReadArchiveNamesOptions): Promise<string[]> {
  const {archives} = options;
  try {
    return await fs.readdir(archives);
  } catch (cause) {
    if ((cause as NodeJS.ErrnoException).code !== 'ENOENT') throw cause;
    return [];
  }
}

interface ReadCurrentManifestOptions {
  input: string;
  requiredVersion: string;
}

/**
 * This build's measured manifest, or null when it has none and no release
 * version requires one.
 */
async function readCurrentManifest(options: ReadCurrentManifestOptions): Promise<ReturnType<typeof validateBinarySizes> | null> {
  const {input, requiredVersion} = options;
  try {
    return validateBinarySizes(JSON.parse(await fs.readFile(input, 'utf8')), requiredVersion || undefined);
  } catch (cause) {
    if ((cause as NodeJS.ErrnoException).code !== 'ENOENT' || requiredVersion) throw cause;
    logger.warn`No path=${INPUT} in this build: current binary sizes are unavailable.`;
    return null;
  }
}

export default function binarySizes(context: LoadContext): Plugin {
  const requiredVersion = process.env.DISPAT_DOCS_BINARY_SIZES_VERSION ?? '';
  const archiveVersion = process.env.DISPAT_DOCS_BINARY_SIZES_ARCHIVE ?? '';
  const input = path.resolve(context.siteDir, INPUT);
  const archives = path.resolve(context.siteDir, ARCHIVES);
  return {
    name: BINARY_SIZES_PLUGIN,
    getPathsToWatch: () => [input, archives],
    async loadContent(): Promise<BinarySizesData> {
      const archiveNames = await readArchiveNames({archives});
      const archived: Record<string, ReturnType<typeof validateBinarySizes>> = {};
      for (const name of archiveNames.filter((entry) => entry.endsWith('.json')).sort()) {
        const docsVersion = name.slice(0, -5);
        const manifest = validateBinarySizes(JSON.parse(await fs.readFile(path.join(archives, name), 'utf8')));
        if (!manifest.version.startsWith(`${docsVersion}.`)) {
          throw new Error(`${name}: release ${manifest.version} does not belong to docs ${docsVersion}`);
        }
        archived[docsVersion] = manifest;
      }
      const manifest = await readCurrentManifest({input, requiredVersion});
      if (archiveVersion) {
        if (!requiredVersion || !manifest) throw new Error('a binary-size archive requires an exact release manifest');
        if (!manifest.version.startsWith(`${archiveVersion}.`)) {
          throw new Error(`binary sizes ${manifest.version} do not belong to docs ${archiveVersion}`);
        }
        await fs.mkdir(archives, {recursive: true});
        await fs.writeFile(path.join(archives, `${archiveVersion}.json`), `${JSON.stringify(manifest, null, 2)}\n`);
        archived[archiveVersion] = manifest;
      }
      return {manifest, currentVersions: ['current', ...(archiveVersion ? [archiveVersion] : [])], archives: archived};
    },
    contentLoaded({content, actions}) { actions.setGlobalData(content as BinarySizesData); },
  };
}
