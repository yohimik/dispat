import type {ExperimentCell, ExperimentLinuxKernel} from './types';
import {buildBinary, buildElapsed, buildStatus} from './build.ts';

export interface KernelRow {
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
  configuration: string;
  modules: string;
  record: ExperimentLinuxKernel;
}

interface KernelCellsOptions { cells: ExperimentCell[] }
const KERNEL_ARCHITECTURES = ['x86_64', 'arm64'] as const;
const UNKNOWN_COUNT_TEXT = 'unknown';

export function kernelRows(options: KernelCellsOptions): KernelRow[] {
  const {cells} = options;
  return cells.flatMap((cell) => {
    if (cell.linuxKernel === undefined) return [];
    const result = cell.linuxKernel;
    return [{
      id: cell.id, tool: cell.tool, profile: result.profile, mode: result.mode,
      status: buildStatus({result}), elapsed: buildElapsed({result}),
      capacity: `${result.executorCount} × ${result.jobsPerExecutor}`,
      targets: result.buildTargets.join(', '), source: result.sourceRevision, topology: result.topology,
      binary: buildBinary({result}),
      configuration: result.configuration,
      modules: KERNEL_ARCHITECTURES.map((architecture) => `${architecture}: ${result.moduleCounts[architecture] ?? UNKNOWN_COUNT_TEXT}`).join('; '),
      record: result,
    }];
  });
}
