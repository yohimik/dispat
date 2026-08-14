import fs from 'node:fs/promises';
import path from 'node:path';

import logger from '@docusaurus/logger';
import type {LoadContext, Plugin} from '@docusaurus/types';

import {TEST_REPORT_PLUGIN} from './name';
import type {ReportData} from './types';
import {validateReport} from './validate';

/**
 * Where the release workflow puts the report before building the site. It is
 * gitignored: the numbers describe a test run, not a revision, so they travel
 * as an artifact from the job that measured them (see
 * .github/workflows/release.yml).
 */
const REPORT = path.join('data', 'report.json');

/**
 * Set for the release build, and only there. A site built locally or by the CI
 * build gate has no report and says so on the page; a site about to be
 * published must not, so the one build whose output people read is the one
 * that refuses to start without it.
 */
const REQUIRE = 'DISPAT_DOCS_REQUIRE_REPORT';

/**
 * Feeds the coverage and test-results pages from the report a release run
 * measured, so no statement about the test suite is written by hand.
 */
export default function testReport(context: LoadContext): Plugin {
  const reportPath = path.resolve(context.siteDir, REPORT);
  const required = process.env[REQUIRE] === '1';
  return {
    name: TEST_REPORT_PLUGIN,

    getPathsToWatch: () => [reportPath],

    async loadContent(): Promise<ReportData> {
      let source: string;
      try {
        source = await fs.readFile(reportPath, 'utf8');
      } catch (cause) {
        if (required) {
          logger.error`A release build needs path=${REPORT}, which could not be read. It is built by ${'go run github.com/yohimik/dispat/tools/testreport build'} from a full test run and downloaded by the release workflow.`;
          throw cause;
        }
        logger.warn`No path=${REPORT} in this build: the coverage and test-results pages will render without numbers. This is expected outside a release.`;
        return {report: null};
      }
      const report = validateReport(JSON.parse(source));
      logger.info`Test report: ${report.coverage.total.percent.toFixed(1)}% of ${String(
        report.coverage.total.statements,
      )} statements, ${String(report.suite.totals.tests)} tests, measured at commit ${report.commit.slice(0, 12)}.`;
      return {report};
    },

    contentLoaded({content, actions}) {
      // `content` is typed as unknown because Docusaurus's plugin type is
      // invariant in it; loadContent above is what pins the shape.
      actions.setGlobalData(content as ReportData);
    },
  };
}
