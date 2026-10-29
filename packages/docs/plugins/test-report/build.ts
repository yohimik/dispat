import type {ExperimentBuild} from './types';

const COMPLETED_STATUS = 'completed';
const NOT_MEASURED_TEXT = 'not measured';
const DURATION_DECIMAL_PLACES = 3;
const UNKNOWN_BINARY_TEXT = 'not recorded';

export interface BuildResultOptions { result: ExperimentBuild }

export function buildStatus(options: BuildResultOptions): string {
  const {result} = options;
  return result.reason ? `${result.status}: ${result.reason}` : result.status;
}

export function buildElapsed(options: BuildResultOptions): string {
  const {result} = options;
  if (result.status !== COMPLETED_STATUS || result.seconds === undefined) return NOT_MEASURED_TEXT;
  return `${result.seconds.toFixed(DURATION_DECIMAL_PLACES)}s`;
}

export function buildBinary(options: BuildResultOptions): string {
  const {result} = options;
  return result.binaryId ?? UNKNOWN_BINARY_TEXT;
}
