import fs from 'node:fs/promises';
import path from 'node:path';

import logger from '@docusaurus/logger';
import type {LoadContext, Plugin} from '@docusaurus/types';

import {TEST_REPORT_PLUGIN} from './name';
import type {ReportData} from './types';
import {validateArchivedReport, validateReport} from './validate';

/**
 * Where the release run puts the report before building the site. It is
 * gitignored: the numbers describe a test run, not a revision, so they are
 * measured into place by the docs package's own beforeBuild hook (see
 * packages/docs/dispat.yaml) rather than committed or carried by CI.
 */
const REPORT = path.join('data', 'report.json');
const ARCHIVES = path.join('static', 'test-reports');
const VERSIONS = 'versions.json';

/**
 * Set for the release build, and only there. A site built locally or by the CI
 * build gate has no report and says so on the page; a site about to be
 * published must not, so the one build whose output people read is the one
 * that refuses to start without it.
 */
const REQUIRE = 'DISPAT_DOCS_REQUIRE_REPORT';
const ARCHIVE_VERSION = 'DISPAT_DOCS_REPORT_VERSION';

/**
 * Feeds the coverage, test-results, benchmarks and experiments pages from the
 * report a release run measured, so no statement about the test suite, no
 * performance figure and no experiment's verdict is written by hand.
 */
export default function testReport(context: LoadContext): Plugin {
  const reportPath = path.resolve(context.siteDir, REPORT);
  const archivesPath = path.resolve(context.siteDir, ARCHIVES);
  const required = process.env[REQUIRE] === '1';
  return {
    name: TEST_REPORT_PLUGIN,

    getPathsToWatch: () => [reportPath, archivesPath],

    async loadContent(): Promise<ReportData> {
      let versions: string[] = [];
      try {
        const parsed = JSON.parse(await fs.readFile(path.resolve(context.siteDir, VERSIONS), 'utf8')) as unknown;
        if (Array.isArray(parsed) && parsed.every((version) => typeof version === 'string')) versions = parsed;
      } catch {
        // A new checkout can have no released doc snapshots yet.
      }
      const archiveVersion = process.env[ARCHIVE_VERSION] ?? '';
      if (archiveVersion && !required) throw new Error(`${ARCHIVE_VERSION} is only valid for a required release build`);
      if (archiveVersion && !versions.includes(archiveVersion)) {
        throw new Error(`${ARCHIVE_VERSION}=${archiveVersion} is not present in ${VERSIONS}`);
      }
      // A stable release explicitly names the snapshot its report belongs to.
      // Without that stamp, the report belongs only to /next/: even the newest
      // frozen docs must read their own archive rather than borrow local data.
      const currentVersions = ['current', ...(archiveVersion ? [archiveVersion] : [])];
      let archiveFiles: string[] = [];
      try {
        archiveFiles = (await fs.readdir(archivesPath)).filter((file) => file.endsWith('.json')).sort();
      } catch (cause) {
        if ((cause as NodeJS.ErrnoException).code !== 'ENOENT') throw cause;
      }
      const archivedVersions: string[] = [];
      for (const file of archiveFiles) {
        const version = file.slice(0, -'.json'.length);
        const archive = JSON.parse(await fs.readFile(path.join(archivesPath, file), 'utf8')) as unknown;
        validateArchivedReport(archive, version);
        archivedVersions.push(version);
      }
      let source: string;
      try {
        source = await fs.readFile(reportPath, 'utf8');
      } catch (cause) {
        if (required) {
          logger.error`A release build needs path=${REPORT}, which could not be read. It is built by ${'go run github.com/yohimik/dispat/tools/testreport build'} from a full test run and downloaded by the release workflow.`;
          throw cause;
        }
        logger.warn`No path=${REPORT} in this build: the coverage, test-results, benchmarks and experiments pages will render without numbers. This is expected outside a release.`;
        return {report: null, currentVersions, archivedVersions};
      }
      const report = validateReport(JSON.parse(source));
      if (required && process.env.GITHUB_SHA && report.commit !== process.env.GITHUB_SHA) {
        throw new Error(`Report commit ${report.commit} does not match GITHUB_SHA ${process.env.GITHUB_SHA}`);
      }
      if (archiveVersion) {
        await fs.mkdir(archivesPath, {recursive: true});
        const destination = path.join(archivesPath, `${archiveVersion}.json`);
        const temporary = `${destination}.${process.pid}.tmp`;
        try {
          await fs.writeFile(temporary, `${JSON.stringify({docsVersion: archiveVersion, report}, null, 2)}\n`);
          await fs.rename(temporary, destination);
        } finally {
          await fs.rm(temporary, {force: true});
        }
        if (!archivedVersions.includes(archiveVersion)) archivedVersions.push(archiveVersion);
      }
      logger.info`Test report: ${report.coverage.total.percent.toFixed(1)}% of ${String(
        report.coverage.total.statements,
      )} statements, ${String(report.suite.totals.tests)} tests, ${String(
        report.experiments.cells.length,
      )} experiment cells, measured at commit ${report.commit.slice(0, 12)}.`;
      return {report, currentVersions, archivedVersions};
    },

    contentLoaded({content, actions}) {
      // `content` is typed as unknown because Docusaurus's plugin type is
      // invariant in it; loadContent above is what pins the shape.
      actions.setGlobalData(content as ReportData);
    },
  };
}
