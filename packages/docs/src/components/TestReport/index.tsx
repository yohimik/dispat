import Link from '@docusaurus/Link';
import {usePluginData} from '@docusaurus/useGlobalData';
import {TEST_REPORT_PLUGIN} from '@site/plugins/test-report/name';
import type {BenchGroup, Counts, Group, Report, ReportData} from '@site/plugins/test-report/types';
import Admonition from '@theme/Admonition';
import React from 'react';

import styles from './styles.module.css';

// The coverage, test-results and benchmarks pages, insofar as they are
// numbers.
//
// Everything here comes from one report measured by the release that published
// this site (tools/testreport). Nothing on either page is a figure someone
// typed, which is the point: the three copies these replace had already drifted
// apart from each other and from the badge.
//
// When a build carries no report — every local build, and the CI gate that
// builds the site on an ordinary commit — <ReportStamp/> says so and the data
// components render nothing. The pages' prose stands on its own either way.

/** The report this build was given, or null when it was built without one. */
export function useReport(): Report | null {
  return (usePluginData(TEST_REPORT_PLUGIN) as ReportData).report;
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
  const report = useReport();
  if (!report) {
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
