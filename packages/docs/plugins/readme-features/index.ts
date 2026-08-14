import fs from 'node:fs/promises';
import path from 'node:path';

import logger from '@docusaurus/logger';
import type {LoadContext, Plugin} from '@docusaurus/types';

import {README_FEATURES_PLUGIN} from './name';
import {parseReadme} from './parse';
import type {ReadmeData} from './types';

/**
 * The CLI README, relative to the site folder. The site is built in a
 * container from a context that has to reach outside the package to find it —
 * see the COPY in packages/docs/Dockerfile and the re-includes in
 * Dockerfile.dockerignore.
 */
const README = path.join('..', '..', 'services', 'dispat', 'README.md');

/**
 * Feeds the landing page's terminal tour and feature cards from
 * services/dispat/README.md, so the two cannot drift.
 */
export default function readmeFeatures(context: LoadContext): Plugin {
  const readmePath = path.resolve(context.siteDir, README);
  return {
    name: README_FEATURES_PLUGIN,

    // Editing the README is editing the landing page, so `docusaurus start`
    // should reload for it like it does for a doc.
    getPathsToWatch: () => [readmePath],

    async loadContent(): Promise<ReadmeData> {
      let source: string;
      try {
        source = await fs.readFile(readmePath, 'utf8');
      } catch (cause) {
        logger.error`The landing page is built from path=${README}, which could not be read.`;
        throw cause;
      }
      const data = parseReadme(source);
      logger.info`Landing page: ${String(data.features.length)} features and the terminal tour from path=${README}.`;
      return data;
    },

    contentLoaded({content, actions}) {
      // `content` is typed as unknown because Docusaurus's plugin type is
      // invariant in it; loadContent above is what pins the shape.
      actions.setGlobalData(content as ReadmeData);
    },
  };
}
