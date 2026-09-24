import type {ExperimentRecovery} from './types';

// The Catch-up column: what finishing a release took once its fault was gone.
// The strings are the ones tools/testreport's catchUpOf writes into a job
// summary, so the page and the summary say the same thing about one record.

export interface CatchUpOptions {
  recovery?: ExperimentRecovery;
}

interface CountedOptions {
  count: number;
  noun: string;
}

/** A count and its noun, plural unless the count is one. */
function counted(options: CountedOptions): string {
  const {count, noun} = options;
  return count === 1 ? `1 ${noun}` : `${count} ${noun}s`;
}

/**
 * One cell's catch-up as a phrase: `not measured` for a protocol that measured
 * none, `none needed` for a catch-up that ran nothing and converged, and
 * otherwise the runs and the commands (release and manual together), then how
 * many were manual, then `did not converge` when the release did not end
 * settled: `2 runs, 3 commands, 1 manual`.
 */
export function catchUpText(options: CatchUpOptions): string {
  const {recovery} = options;
  if (!recovery) {
    return 'not measured';
  }
  const commands = recovery.releaseCommands + recovery.manualCommands;
  if (commands === 0 && recovery.converged) {
    return 'none needed';
  }
  return [
    counted({count: recovery.runs, noun: 'run'}),
    counted({count: commands, noun: 'command'}),
    ...(recovery.manualCommands > 0 ? [`${recovery.manualCommands} manual`] : []),
    ...(recovery.converged ? [] : ['did not converge']),
  ].join(', ');
}

/**
 * Whether a catch-up finished in one run of one of the tool's own commands,
 * with no manual step: the whole promise of a re-run, kept.
 */
export function isSingleCommandCatchUp(options: CatchUpOptions): boolean {
  const {recovery} = options;
  if (!recovery) {
    return false;
  }
  return recovery.runs === 1 && recovery.releaseCommands === 1 && recovery.manualCommands === 0 && recovery.converged;
}
