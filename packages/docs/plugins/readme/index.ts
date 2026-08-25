import fs from 'node:fs/promises';
import path from 'node:path';

import logger from '@docusaurus/logger';
import type {LoadContext, Plugin} from '@docusaurus/types';

import {README_PLUGIN} from './name';
import {CLI_README, parseCliReadme, parseRepositoryReadme, ROOT_README} from './parse';
import type {ReadmeData} from './types';

/**
 * The repository root, relative to the site folder. Both READMEs are inputs to
 * this build as much as any page is, and the site is built in a container from
 * a context that has to reach outside the package to find them — see the COPY
 * lines in packages/docs/Dockerfile and the re-includes in
 * Dockerfile.dockerignore.
 */
const REPO = path.join('..', '..');

/**
 * Builds the landing page out of the two READMEs, so the page and the files a
 * reader lands on from GitHub cannot drift apart.
 */
export default function readme(context: LoadContext): Plugin {
  const resolved = (source: {path: string}) => path.resolve(context.siteDir, REPO, source.path);
  const sources = [ROOT_README, CLI_README];

  return {
    name: README_PLUGIN,

    // Editing a README is editing the landing page, so `docusaurus start`
    // should reload for it like it does for a doc.
    getPathsToWatch: () => sources.map(resolved),

    async loadContent(): Promise<ReadmeData> {
      const [root, cli] = await Promise.all(
        sources.map(async (source) => {
          try {
            return await fs.readFile(resolved(source), 'utf8');
          } catch (cause) {
            logger.error`The landing page is built from path=${source.path}, which could not be read.`;
            throw cause;
          }
        }),
      );
      const data = {repository: parseRepositoryReadme(root), cli: parseCliReadme(cli)};
      logger.info`Landing page: ${String(data.repository.lead.length)} opening paragraphs and ${String(
        data.repository.inspiration.items.length,
      )} inspirations from path=${ROOT_README.path}, ${String(data.cli.features.length)} features from path=${
        CLI_README.path
      }.`;
      return data;
    },

    contentLoaded({content, actions}) {
      // `content` is typed as unknown because Docusaurus's plugin type is
      // invariant in it; loadContent above is what pins the shape.
      actions.setGlobalData(content as ReadmeData);
    },
  };
}
