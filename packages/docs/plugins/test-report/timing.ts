import type {ExperimentCell} from './types';

// The Timing and Plans tables: a timing cell's figures, and its plan set
// beside dispat's. The plan strings are the ones tools/testreport's planOf
// writes into a job summary, so the page and the summary say the same thing
// about one record.

const REFERENCE_TOOL = 'dispat';

export interface TimingCellsOptions {
  cells: ExperimentCell[];
}

export interface PlanTextOptions {
  tool: string;
  plan?: ExperimentCell['plan'];
}

export interface TimingRow {
  id: string;
  scenario: string;
  tool: string;
  relation: string;
  planning: string;
  makespan: string;
  work: string;
  criticalPath: string;
  parallelism: string;
  bound: string;
  ratio: string;
  criticalChain: string;
}

export interface PlanRow {
  id: string;
  scenario: string;
  tool: string;
  text: string;
}

interface CountedOptions {
  count: number;
  noun: string;
}

function counted(options: CountedOptions): string {
  const {count, noun} = options;
  return count === 1 ? `1 ${noun}` : `${count} ${noun}s`;
}

interface SecondsOptions {
  seconds: number;
}

/** A duration to the millisecond the records are rounded to: `1.942s`. */
export function secondsText(options: SecondsOptions): string {
  const {seconds} = options;
  return `${seconds.toFixed(3)}s`;
}

/**
 * One cell's plan beside dispat's: `not measured`, `the reference, 4
 * packages`, `no dispat plan to compare`, `agrees with dispat, 6 packages`, or
 * what differs, in up to three clauses: `also docs 1.0.1; not chain2 1.0.1;
 * core 1.2.0 for dispat's 1.1.0`.
 */
export function planText(options: PlanTextOptions): string {
  const {tool, plan} = options;
  if (!plan) {
    return 'not measured';
  }
  if (tool === REFERENCE_TOOL) {
    return `the reference, ${counted({count: plan.packages.length, noun: 'package'})}`;
  }
  if (plan.agrees === undefined) {
    return 'no dispat plan to compare';
  }
  if (plan.agrees) {
    return `agrees with dispat, ${counted({count: plan.packages.length, noun: 'package'})}`;
  }
  const also = plan.differences.filter((d) => d.reference === '').map((d) => `${d.package} ${d.version}`);
  const not = plan.differences.filter((d) => d.reference !== '' && d.version === '').map((d) => `${d.package} ${d.reference}`);
  const changed = plan.differences
    .filter((d) => d.reference !== '' && d.version !== '')
    .map((d) => `${d.package} ${d.version} for dispat's ${d.reference}`);
  return [
    ...(also.length > 0 ? [`also ${also.join(', ')}`] : []),
    ...(not.length > 0 ? [`not ${not.join(', ')}`] : []),
    ...(changed.length > 0 ? [changed.join(', ')] : []),
  ].join('; ');
}

/** The timing cells' figures, a row per cell in the report's order. */
export function timingRows(options: TimingCellsOptions): TimingRow[] {
  const {cells} = options;
  return cells.flatMap((cell) => {
    const timing = cell.timing;
    if (!timing) {
      return [];
    }
    return [{
      id: cell.id,
      scenario: cell.scenario,
      tool: cell.tool,
      relation: timing.relation,
      planning: secondsText({seconds: timing.planningSeconds}),
      makespan: secondsText({seconds: timing.makespanSeconds}),
      work: secondsText({seconds: timing.work}),
      criticalPath: secondsText({seconds: timing.criticalPath}),
      parallelism: String(timing.parallelism),
      bound: secondsText({seconds: timing.bound}),
      ratio: timing.ratio.toFixed(2),
      criticalChain: timing.criticalChain.join(' → '),
    }];
  });
}

/** The timing cells' plans, a row per cell that recorded one. */
export function planRows(options: TimingCellsOptions): PlanRow[] {
  const {cells} = options;
  return cells
    .filter((cell) => cell.plan !== undefined)
    .map((cell) => ({id: cell.id, scenario: cell.scenario, tool: cell.tool, text: planText(cell)}));
}
