import type {FC} from 'react';
import {llvmRows} from '@site/plugins/test-report/llvm';
import {useExperimentCells} from './index';
import {LLVMTable} from './LLVMTable';

export interface BaseExperimentsLLVMTableProps {}

export interface ExperimentsLLVMTableProps extends BaseExperimentsLLVMTableProps {
  children?: never;
}

const NO_ROWS_COUNT = 0;

export const ExperimentsLLVMTable: FC<ExperimentsLLVMTableProps> = (props) => {
  const {} = props;
  const rows = llvmRows({cells: useExperimentCells()});
  if (rows.length === NO_ROWS_COUNT) {
    return <p>No LLVM build campaign has been recorded for this version.</p>;
  }
  return <LLVMTable rows={rows} />;
};
