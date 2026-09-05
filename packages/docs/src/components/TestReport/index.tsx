import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import {usePluginData} from '@docusaurus/useGlobalData';
import {useDocsVersion} from '@docusaurus/plugin-content-docs/client';
import {TEST_REPORT_PLUGIN} from '@site/plugins/test-report/name';
import type {
  BenchGroup,
  Counts,
  ExperimentCell,
  Group,
  Report,
  ReportData,
  ArchivedReport,
} from '@site/plugins/test-report/types';
import {validateArchivedReport} from '@site/plugins/test-report/validate';
import {resolveArchivedState, type ArchivedState} from '@site/plugins/test-report/state';
import Admonition from '@theme/Admonition';
import React, {useEffect, useState} from 'react';

import styles from './styles.module.css';

// The coverage, test-results, benchmarks and experiments pages, insofar as
// they are numbers.
//
// Current pages read the report measured by the release build. Frozen versions
// read their own archive, which can contain a full report or recovered evidence.
// Missing historical detail is identified rather than filled from a newer run.

const archivedReports = new Map<string, Promise<ArchivedReport | null>>();

function archivedReport(version: string, url: string): Promise<ArchivedReport | null> {
  const existing = archivedReports.get(version);
  if (existing) return existing;
  const request = fetch(url)
    .then(async (response) => response.ok ? validateArchivedReport(await response.json(), version) : null)
    .catch(() => null);
  archivedReports.set(version, request);
  return request;
}

function useReportState(): ArchivedState {
  const data = usePluginData(TEST_REPORT_PLUGIN) as ReportData;
  const version = useDocsVersion().version;
  const archiveUrl = useBaseUrl(`/test-reports/${encodeURIComponent(version)}.json`);
  const isCurrent = data.currentVersions.includes(version);
  const isArchived = data.archivedVersions.includes(version);
  const [archive, setArchive] = useState<{version: string; value: ArchivedReport | null} | null>(null);
  useEffect(() => {
    let mounted = true;
    if (!isCurrent && isArchived) {
      void archivedReport(version, archiveUrl).then((value) => { if (mounted) setArchive({version, value}); });
    }
    return () => { mounted = false; };
  }, [archiveUrl, isArchived, isCurrent, version]);
  if (isCurrent) return {report: data.report, evidence: null, status: data.report ? 'available' : 'unavailable'};
  return resolveArchivedState(version, isArchived, archive);
}

/** The report for the doc version being read, or null when none was archived. */
export function useReport(): Report | null {
  return useReportState().report;
}

/** `12345` -> `12,345`, without asking the platform's locale. */
function count(n: number): string {
  return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

function pct(n: number): string {
  return `${n.toFixed(1)}%`;
}

/** Seconds as the length they are: a unit run in seconds, a suite in minutes. */
function duration(seconds: number): string {
  if (seconds < 60) {
    return `${seconds.toFixed(1)}s`;
  }
  const whole = Math.round(seconds);
  return `${Math.floor(whole / 60)}m ${whole % 60}s`;
}

/**
 * The date only, from the report's RFC 3339 stamp.
 *
 * Deliberately not toLocaleDateString: the server renders this page once and
 * the browser hydrates it again, and a formatter that consults a locale can
 * disagree between the two.
 */
function day(timestamp: string): string {
  return timestamp.slice(0, 10);
}

/** The invocation as a reader knows it: the module, and which pass it was. */
function label(group: Group): string {
  return group.race ? `${group.path} (-race)` : group.path;
}

/** How a run ended, in a clause. Skipped tests are not failures, nor passes. */
function outcome(counts: Counts): string {
  if (counts.failed > 0) {
    return `${count(counts.failed)} failed.`;
  }
  if (counts.skipped > 0) {
    return `All of them pass, other than ${count(counts.skipped)} skipped.`;
  }
  return 'All of them pass.';
}

/**
 * Where the numbers on this page came from, or why there are none.
 *
 * The stamp is the answer to the question the hand-written tables could never
 * answer: which run measured this. A report from another commit is visible
 * rather than merely stale.
 */
export function ReportStamp(): React.ReactElement {
  const {report, evidence, status} = useReportState();
  const version = useDocsVersion().version;
  const {currentVersions} = usePluginData(TEST_REPORT_PLUGIN) as ReportData;
  if (!report) {
    if (status === 'loading') {
      return (
        <Admonition type="note" title={`Loading the ${version} release report`}>
          <p>The test, coverage, benchmark, and experiment results are loading from this release&apos;s archive.</p>
        </Admonition>
      );
    }
    if (evidence) {
      return (
        <Admonition type="note" title={`Recovered release evidence for ${version}`}>
          <p>
            The {evidence.releaseVersion} release recorded {count(evidence.suite.tests)} tests, {count(evidence.suite.fuzz)} fuzz
            targets, and {count(evidence.benchmarks)} benchmarks. Total coverage was {pct(evidence.coverage.totalPercent)} of{' '}
            {count(evidence.coverage.statements)} statements (unit {pct(evidence.coverage.unitPercent)}; integration{' '}
            {pct(evidence.coverage.integrationPercent)}).
          </p>
          {evidence.experiments ? (
            <p>
              It also recorded {count(evidence.experiments.cells)} experiment cells. Their verdicts, steps, checks, and
              final states are preserved in the tables below from the exact{' '}
              <Link to={evidence.experiments.artifactUrl}>experiment artifact</Link>.
            </p>
          ) : null}
          <p>
            Measured on {day(evidence.generatedAt)} at commit {evidence.commit}. See the{' '}
            <Link to={evidence.runUrl}>release run</Link> and{' '}
            <Link to={evidence.coverageArtifactUrl}>coverage artifact</Link>. Per-package suite results and individual benchmark
            measurements were not retained, so the detailed tables below are left empty.
          </p>
        </Admonition>
      );
    }
    if (!currentVersions.includes(version)) {
      return (
        <Admonition type="note" title={`No archived report for ${version}`}>
          <p>
            This documentation version has no preserved test report. Its figures are unavailable rather than being
            replaced with results from a newer release.
          </p>
        </Admonition>
      );
    }
    return (
      <Admonition type="note" title="Measured at release time">
        <p>
          This build of the site carries no test report, so the figures are left out below. They are measured by the
          run that gates a release and published with the site it releases; the{' '}
          <Link to="https://github.com/yohimik/dispat/actions/workflows/tests.yml">coverage badge</Link> on the
          repository README is always the latest number.
        </p>
      </Admonition>
    );
  }
  return (
    <p>
      <em>
        Measured on {day(report.generatedAt)}
        {report.commit ? ` at commit ${report.commit.slice(0, 12)}` : ''}, by the release that published this site.
      </em>
    </p>
  );
}

/** The three coverage layers in a sentence. */
export function CoverageSummary(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  const {total, unit, integration} = report.coverage;
  return (
    <p>
      The whole test suite covers <strong>{pct(total.percent)}</strong> of the workspace&apos;s{' '}
      {count(total.statements)} statements. The unit layer alone reaches {pct(unit.percent)}, and the integration
      layer&apos;s instrumented binary {pct(integration.percent)}; the two overlap, so the total is every profile
      merged rather than either of them added up.
    </p>
  );
}

/** Every module, and the packages inside it. */
export function CoverageTable(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  return (
    <table>
      <thead>
        <tr>
          <th>Module / package</th>
          <th className={styles.number}>Statements</th>
          <th className={styles.number}>Covered</th>
          <th className={styles.number}>Coverage</th>
        </tr>
      </thead>
      <tbody>
        {report.coverage.modules.map((module) => {
          // A module with one package that *is* the module has nothing to
          // break out: the row above would say it again.
          const own = module.packages.length === 1 && module.packages[0].path === module.path;
          return (
            <React.Fragment key={module.path}>
              <tr>
                <td>
                  <strong>
                    <code>{module.path}</code>
                  </strong>
                </td>
                <td className={styles.number}>{count(module.statements)}</td>
                <td className={styles.number}>{count(module.covered)}</td>
                <td className={styles.number}>
                  <strong>{pct(module.percent)}</strong>
                </td>
              </tr>
              {!own &&
                module.packages.map((pkg) => (
                  <tr key={pkg.path}>
                    <td className={styles.package}>
                      {/* The module's own package has no path under the module
                          to name it by, and an empty cell would read as a
                          missing row. */}
                      {pkg.path === module.path ? <em>the module itself</em> : <code>{pkg.path.slice(module.path.length + 1)}</code>}
                    </td>
                    <td className={styles.number}>{count(pkg.statements)}</td>
                    <td className={styles.number}>{count(pkg.covered)}</td>
                    <td className={styles.number}>{pct(pkg.percent)}</td>
                  </tr>
                ))}
            </React.Fragment>
          );
        })}
      </tbody>
    </table>
  );
}

/** What the run did, in a sentence. */
export function SuiteSummary(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  const {totals, groups} = report.suite;
  const race = groups.filter((group) => group.race);
  const raceClean = race.length > 0 && race.every((group) => group.failed === 0);
  return (
    <>
      <p>
        The suite runs <strong>{count(totals.tests)} test functions</strong> and{' '}
        <strong>{count(totals.fuzz)} fuzz targets</strong>, {count(totals.subtests)} subtests between them, across{' '}
        {count(totals.packages)} packages, in {duration(totals.elapsed)}.{' '}
        {outcome(totals)}
      </p>
      {raceClean && (
        <p>
          <strong>Race-clean</strong>: the integration suite passes again under <code>go test -race</code>, as its own
          pass in the table below.
        </p>
      )}
    </>
  );
}

/** One row per `go test` invocation. */
export function SuiteTable(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  return (
    <table>
      <thead>
        <tr>
          <th>Module</th>
          <th className={styles.number}>Tests</th>
          <th className={styles.number}>Fuzz</th>
          <th className={styles.number}>Subtests</th>
          <th className={styles.number}>Time</th>
          <th>Result</th>
        </tr>
      </thead>
      <tbody>
        {report.suite.groups.map((group) => (
          <tr key={group.id}>
            <td>
              <code>{label(group)}</code>
            </td>
            <td className={styles.number}>{count(group.tests)}</td>
            <td className={styles.number}>{count(group.fuzz)}</td>
            <td className={styles.number}>{count(group.subtests)}</td>
            <td className={styles.number}>{duration(group.elapsed)}</td>
            <td>
              {group.failed === 0
                ? `passed${group.skipped > 0 ? `, ${count(group.skipped)} skipped` : ''}`
                : `${count(group.failed)} failed`}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * A duration in the unit it deserves. A benchmark spans nine orders of
 * magnitude — a fold is nanoseconds, a file rewrite is milliseconds — and one
 * unit across all of them is a column of zeroes or a column of exponents.
 */
function perOp(ns: number): string {
  if (ns < 1_000) {
    return `${ns.toFixed(ns < 100 ? 2 : 1)} ns`;
  }
  if (ns < 1_000_000) {
    return `${(ns / 1_000).toFixed(1)} µs`;
  }
  return `${(ns / 1_000_000).toFixed(1)} ms`;
}

/** Bytes as a size, for the same reason perOp exists. */
function bytes(n: number): string {
  if (n < 1_024) {
    return `${count(n)} B`;
  }
  if (n < 1_024 * 1_024) {
    return `${(n / 1_024).toFixed(1)} KiB`;
  }
  return `${(n / (1_024 * 1_024)).toFixed(1)} MiB`;
}

/** The machine a group was measured on, as one clause. */
function machine(group: BenchGroup): string {
  const cpu = group.cpu ? `, ${group.cpu}` : '';
  return `${group.goos}/${group.goarch}${cpu}`;
}

/**
 * What the run measured, in a sentence: how many benchmarks across how many
 * modules, and on what.
 */
export function BenchmarkSummary(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  const groups = report.benchmarks.groups;
  const total = groups.reduce((n, group) => n + group.results.length, 0);
  if (total === 0) {
    return null;
  }
  const machines = Array.from(new Set(groups.map(machine)));
  return (
    <p>
      This release measured <strong>{count(total)} benchmarks</strong> across{' '}
      <strong>{count(groups.length)} modules</strong>, on {machines.join(' and ')}.
    </p>
  );
}

/**
 * One table per module: every benchmark it declares, with the iteration count
 * the timing was averaged over.
 *
 * The iteration count is a column rather than a footnote because it is what
 * says how much to trust the row beside it: a benchmark the tool ran ten times
 * and one it ran ten million times are not the same kind of number.
 */
export function BenchmarkTable(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  const groups = report.benchmarks.groups.filter((group) => group.results.length > 0);
  if (groups.length === 0) {
    return null;
  }
  return (
    <>
      {groups.map((group) => (
        <div key={group.id}>
          <h3>
            <code>{group.path}</code>
          </h3>
          <p>
            <em>{machine(group)}</em>
          </p>
          <table>
            <thead>
              <tr>
                <th>Benchmark</th>
                <th className={styles.number}>Time/op</th>
                <th className={styles.number}>Bytes/op</th>
                <th className={styles.number}>Allocs/op</th>
                <th className={styles.number}>Iterations</th>
              </tr>
            </thead>
            <tbody>
              {group.results.map((result) => (
                <tr key={`${result.package}/${result.name}`}>
                  <td>
                    <code>{result.name}</code>
                  </td>
                  <td className={styles.number}>{perOp(result.nsPerOp)}</td>
                  <td className={styles.number}>{bytes(result.bytesPerOp)}</td>
                  <td className={styles.number}>{count(result.allocsPerOp)}</td>
                  <td className={styles.number}>{count(result.runs)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </>
  );
}

/**
 * Every fuzz target the suite ran, with the corpus entries it was run against.
 *
 * A plain `go test` runs a fuzz target's corpus rather than fuzzing, so this
 * is what the suite exercised and not a claim about a fuzzing session. The
 * seed count is the honest half of the number.
 */
export function FuzzTable(): React.ReactElement | null {
  const report = useReport();
  if (!report) {
    return null;
  }
  const targets = report.suite.groups
    .filter((group) => !group.race)
    .flatMap((group) => group.fuzzTargets);
  if (targets.length === 0) {
    return null;
  }
  return (
    <table>
      <thead>
        <tr>
          <th>Target</th>
          <th>Package</th>
          <th className={styles.number}>Corpus entries</th>
        </tr>
      </thead>
      <tbody>
        {targets.map((target) => (
          <tr key={`${target.package}/${target.name}`}>
            <td>
              <code>{target.name}</code>
            </td>
            <td>
              <code>{target.package}</code>
            </td>
            <td className={styles.number}>{count(target.seeds)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/**
 * How many cells a campaign ran, against which image, and how many hold.
 *
 * The image tag is the point of the sentence. Every cell copies its binary out
 * of a published `yohimik/dispat-alpine:<version>`, so the page is about bytes
 * somebody can pull rather than about a build of a checkout, and a reader who
 * wants to disbelieve it has the tag to run it against.
 */
export function ExperimentsSummary(): React.ReactElement | null {
  const {report, evidence} = useReportState();
  const experiments = report?.experiments ?? evidence?.experimentResults;
  if (!experiments) return null;
  const {version, cells} = experiments;
  if (cells.length === 0) {
    return null;
  }
  const own = cells.filter((cell) => cell.tool === 'dispat');
  const holding = own.filter((cell) => cell.passed).length;
  return (
    <p>
      This release ran <strong>{count(cells.length)} cells</strong> against{' '}
      {version ? <code>yohimik/dispat-alpine:{version}</code> : <em>more than one published image</em>}: two faults,
      four release tools, the same fixture each time. Of dispat&apos;s{' '}
      <strong>{count(own.length)} cells</strong>, <strong>{count(holding)}</strong> hold every expectation. The other
      tools&apos; cells are records rather than expectations, and their counts describe those tools.
    </p>
  );
}

/** The cells of one experiment and scenario, as a heading a reader can name. */
function experimentTitle(cell: ExperimentCell): string {
  return cell.scenario ? `${cell.experiment} (${cell.scenario})` : cell.experiment;
}

/** How a cell ended, in a clause: what held, or everything that did not. */
function cellOutcome(cell: ExperimentCell): string {
  if (cell.passed) {
    return 'every expectation holds';
  }
  const failed = cell.checks.filter((check) => !check.ok);
  if (failed.length === 0) {
    return 'no expectations were recorded';
  }
  return failed.map((check) => check.check).join('; ');
}

/**
 * One table per experiment and scenario, a row per tool.
 *
 * Grouped by the fault rather than by the tool, because what the page is about
 * is what four tools do with one fault, and a table sorted by tool puts the
 * four answers to one question in four different places.
 */
export function ExperimentsTable(): React.ReactElement | null {
  const {report, evidence} = useReportState();
  const cells = (report?.experiments ?? evidence?.experimentResults)?.cells ?? [];
  if (cells.length === 0) {
    return null;
  }
  // Insertion order over the report's already-sorted cells, so the groups
  // appear in the order the ids do and two builds of one report render
  // identically.
  const groups: {title: string; cells: ExperimentCell[]}[] = [];
  for (const cell of cells) {
    const title = experimentTitle(cell);
    const group = groups.find((candidate) => candidate.title === title);
    if (group) {
      group.cells.push(cell);
    } else {
      groups.push({title, cells: [cell]});
    }
  }
  return (
    <>
      {groups.map((group) => (
        <div key={group.title}>
          <h3>{group.title}</h3>
          <table>
            <thead>
              <tr>
                <th>Tool</th>
                <th>Steps</th>
                <th className={styles.number}>Expectations</th>
                <th>Outcome</th>
                <th>Final state</th>
              </tr>
            </thead>
            <tbody>
              {group.cells.map((cell) => (
                <tr key={cell.id}>
                  <td>
                    <code>{cell.tool}</code>
                  </td>
                  <td>
                    <code>{cell.steps.map((step) => `${step.step}=${step.exit}`).join(' ')}</code>
                  </td>
                  <td className={styles.number}>
                    {count(cell.checks.filter((check) => check.ok).length)}/{count(cell.checks.length)}
                  </td>
                  <td>{cellOutcome(cell)}</td>
                  <td>
                    <code>
                      {cell.final.packages.map((pkg) => `${pkg.name}=${pkg.registry}/${pkg.state}`).join(' ')}
                    </code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </>
  );
}
