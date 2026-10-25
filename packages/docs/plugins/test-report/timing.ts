import type {
  ExperimentCell,
  ExperimentPackage,
  ExperimentPlannedPackage,
  ExperimentTaskStats,
  ExperimentTiming,
} from './types';

// The Timing, Tasks and Plans tables: a timing cell's figures, its tasks by
// kind and one by one, and its plan set beside dispat's; and the final state
// every experiments table prints. The plan and state strings are the ones
// tools/testreport's planOf and stateOf write into a job summary, held to
// testdata/texts.json from both sides, so the page and the summary say the
// same thing about one record.
//
// Packages that share a value are read as one entry where listing each would
// bury the table: the wide timing cells release forty packages at one
// version.

const REFERENCE_TOOL = 'dispat';

/** The most packages a rendered group names; a larger one is counted. */
const NAMED_GROUP = 4;

/** The most packages a final state lists one by one. */
const WHOLE_STATE = 8;

interface Group {
  value: string;
  names: string[];
}

interface GroupedOptions {
  entries: {name: string; value: string}[];
}

/** The entries grouped by value, the groups in the order the first of each came. */
function grouped(options: GroupedOptions): Group[] {
  const {entries} = options;
  const groups: Group[] = [];
  for (const {name, value} of entries) {
    const group = groups.find((candidate) => candidate.value === value);
    if (group) {
      group.names.push(name);
    } else {
      groups.push({value, names: [name]});
    }
  }
  return groups;
}

export interface FinalStateEntriesOptions {
  packages: ExperimentPackage[];
}

/**
 * A final state as the tables print it, `core=1.1.0/consistent` per package.
 * A state of more than eight packages prints a group of more than four that
 * share a version and a state as one entry, `40 packages=1.0.1/consistent`.
 */
export function finalStateEntries(options: FinalStateEntriesOptions): string[] {
  const {packages} = options;
  const groups = grouped({entries: packages.map((pkg) => ({name: pkg.name, value: `${pkg.registry}/${pkg.state}`}))});
  return groups.flatMap((group) =>
    packages.length > WHOLE_STATE && group.names.length > NAMED_GROUP
      ? [`${group.names.length} packages=${group.value}`]
      : group.names.map((name) => `${name}=${group.value}`),
  );
}

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
  idle: string;
  work: string;
  criticalPath: string;
  parallelism: string;
  bound: string;
  ratio: string;
  notHonoured: string;
  criticalChain: string;
}

export interface TaskRow {
  id: string;
  scenario: string;
  tool: string;
  build: string;
  publish: string;
  npmPublish: string;
  planBuilds: string;
}

export interface TaskListRow {
  package: string;
  task: string;
  start: string;
  seconds: string;
}

export interface TaskList {
  id: string;
  scenario: string;
  tool: string;
  tasks: TaskListRow[];
}

export interface PlanRow {
  id: string;
  scenario: string;
  tool: string;
  told: string;
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

interface PlannedTextOptions {
  packages: ExperimentPlannedPackage[];
}

/** A plan's packages by version: `api, cli, ui at 1.0.1; core at 1.1.0`, a group of more than four counted. */
function plannedText(options: PlannedTextOptions): string {
  const {packages} = options;
  return grouped({entries: packages.map((p) => ({name: p.name, value: p.version}))})
    .map((group) => {
      const who = group.names.length > NAMED_GROUP ? counted({count: group.names.length, noun: 'package'}) : group.names.join(', ');
      return `${who} at ${group.value}`;
    })
    .join('; ');
}

/**
 * One cell's plan beside dispat's: `not measured`, `the reference: api, cli,
 * ui at 1.0.1; core at 1.1.0`, `no dispat plan to compare`, `agrees with
 * dispat, 6 packages`, or what differs, in up to three clauses: `also docs
 * 1.0.1; not chain2 1.0.1; core 1.2.0 for dispat's 1.1.0`.
 */
export function planText(options: PlanTextOptions): string {
  const {tool, plan} = options;
  if (!plan) {
    return 'not measured';
  }
  if (tool === REFERENCE_TOOL) {
    return `the reference: ${plannedText({packages: plan.packages})}`;
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

interface TimingOptions {
  timing: ExperimentTiming;
}

/** The declared relation, with the one the records show where they differ: `build (records: publish)`. */
export function relationText(options: TimingOptions): string {
  const {timing} = options;
  const {relation, observedRelation} = timing;
  return observedRelation === undefined || observedRelation === relation ? relation : `${relation} (records: ${observedRelation})`;
}

/** The edges a release did not honour: `none`, or `core build > ui build, …`. */
export function violationsText(options: TimingOptions): string {
  const {timing} = options;
  return timing.violations.length === 0 ? 'none' : timing.violations.map((e) => `${e.before} > ${e.after}`).join(', ');
}

interface StatsTextOptions {
  stats: ExperimentTaskStats;
}

/** One kind's tasks: `41, median 0.502s, max 0.509s, total 20.585s`, and `, 1 failed` when any was. */
export function statsText(options: StatsTextOptions): string {
  const {stats} = options;
  if (stats.count === 0) {
    return 'none';
  }
  const text = `${stats.count}, median ${secondsText({seconds: stats.median})}, max ${secondsText({seconds: stats.max})}, total ${secondsText({seconds: stats.seconds})}`;
  return stats.failed > 0 ? `${text}, ${stats.failed} failed` : text;
}

function timed(cells: ExperimentCell[]): (ExperimentCell & {timing: ExperimentTiming})[] {
  return cells.filter((cell): cell is ExperimentCell & {timing: ExperimentTiming} => cell.timing !== undefined);
}

/** The timing cells' figures, a row per cell in the report's order. */
export function timingRows(options: TimingCellsOptions): TimingRow[] {
  const {cells} = options;
  return timed(cells).map((cell) => {
    const {timing} = cell;
    return {
      id: cell.id,
      scenario: cell.scenario,
      tool: cell.tool,
      relation: relationText({timing}),
      planning: secondsText({seconds: timing.planningSeconds}),
      makespan: secondsText({seconds: timing.makespanSeconds}),
      idle: secondsText({seconds: timing.idleSeconds}),
      work: secondsText({seconds: timing.work}),
      criticalPath: secondsText({seconds: timing.criticalPath}),
      parallelism: String(timing.parallelism),
      bound: secondsText({seconds: timing.bound}),
      ratio: timing.ratio.toFixed(2),
      notHonoured: violationsText({timing}),
      criticalChain: timing.criticalChain.join(' → '),
    };
  });
}

/** The timing cells' tasks by kind, a row per cell. */
export function taskRows(options: TimingCellsOptions): TaskRow[] {
  const {cells} = options;
  return timed(cells).map((cell) => {
    const {tasks, planBuilds} = cell.timing;
    return {
      id: cell.id,
      scenario: cell.scenario,
      tool: cell.tool,
      build: statsText({stats: tasks.build}),
      publish: statsText({stats: tasks.publish}),
      npmPublish: statsText({stats: tasks.npmPublish}),
      planBuilds: planBuilds.count === 0 ? 'none' : `${planBuilds.count}, total ${secondsText({seconds: planBuilds.seconds})}`,
    };
  });
}

/** Every task of every timing cell, in the order the tasks started. */
export function taskLists(options: TimingCellsOptions): TaskList[] {
  const {cells} = options;
  return timed(cells).map((cell) => ({
    id: cell.id,
    scenario: cell.scenario,
    tool: cell.tool,
    tasks: cell.timing.taskTimes.map((task) => ({
      package: task.package,
      task: task.task,
      start: `${task.start < 0 ? '' : '+'}${secondsText({seconds: task.start})}`,
      seconds: secondsText({seconds: task.seconds}),
    })),
  }));
}

/** The timing cells' plans, a row per cell that recorded one. */
export function planRows(options: TimingCellsOptions): PlanRow[] {
  const {cells} = options;
  return cells.flatMap((cell) =>
    cell.plan === undefined
      ? []
      : [{id: cell.id, scenario: cell.scenario, tool: cell.tool, told: cell.plan.told, text: planText(cell)}],
  );
}
