import type {ExperimentCell, ExperimentLLVM} from './types';
import {buildBinary, buildElapsed, buildStatus} from './build.ts';

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
  binary: string;
  record: ExperimentLLVM;
}

interface LLVMCellsOptions {
  cells: ExperimentCell[];
}

interface LLVMResultOptions {
  result: ExperimentLLVM;
}

export function llvmStatus(options: LLVMResultOptions): string {
  return buildStatus(options);
}

export function llvmElapsed(options: LLVMResultOptions): string {
  return buildElapsed(options);
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
      binary: buildBinary({result}), record: result,
    }];
  });
}
