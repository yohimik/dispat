import type {ExperimentCell, ExperimentLLVM} from './types';

const COMPLETED_STATUS = 'completed';
const NOT_MEASURED_TEXT = 'not measured';
const DURATION_DECIMAL_PLACES = 3;

export interface LLVMRow {
  id: string;
  tool: string;
  profile: string;
  mode: string;
  status: string;
  elapsed: string;
  capacity: string;
  targets: string;
  source: string;
  topology: string;
}

interface LLVMCellsOptions {
  cells: ExperimentCell[];
}

interface LLVMResultOptions {
  result: ExperimentLLVM;
}

export function llvmStatus(options: LLVMResultOptions): string {
  const {result} = options;
  return result.reason ? `${result.status}: ${result.reason}` : result.status;
}

export function llvmElapsed(options: LLVMResultOptions): string {
  const {result} = options;
  if (result.status !== COMPLETED_STATUS || result.seconds === undefined) {
    return NOT_MEASURED_TEXT;
  }
  return `${result.seconds.toFixed(DURATION_DECIMAL_PLACES)}s`;
}

export function llvmRows(options: LLVMCellsOptions): LLVMRow[] {
  const {cells} = options;
  return cells.flatMap((cell) => {
    if (cell.llvm === undefined) {
      return [];
    }
    const result = cell.llvm;
    return [{
      id: cell.id, tool: cell.tool, profile: result.profile, mode: result.mode,
      status: llvmStatus({result}), elapsed: llvmElapsed({result}),
      capacity: `${result.executorCount} × ${result.jobsPerExecutor}`,
      targets: result.buildTargets.join(', '), source: result.sourceRevision, topology: result.topology,
    }];
  });
}
