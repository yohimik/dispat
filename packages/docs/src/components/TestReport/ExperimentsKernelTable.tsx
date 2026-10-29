import type {FC} from 'react';
import {kernelRows} from '@site/plugins/test-report/kernel';
import {useExperimentCells} from './index';
import {KernelTable} from './KernelTable';

export interface BaseExperimentsKernelTableProps {}
export interface ExperimentsKernelTableProps extends BaseExperimentsKernelTableProps { children?: never }

const NO_ROWS_COUNT = 0;

export const ExperimentsKernelTable: FC<ExperimentsKernelTableProps> = (props) => {
  const {} = props;
  const rows = kernelRows({cells: useExperimentCells()});
  if (rows.length === NO_ROWS_COUNT) return <p>No Linux kernel build campaign has been recorded for this version.</p>;
  return <KernelTable rows={rows} />;
};
