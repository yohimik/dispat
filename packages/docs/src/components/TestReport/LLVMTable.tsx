import type {FC} from 'react';
import type {LLVMRow} from '@site/plugins/test-report/llvm';
import styles from './styles.module.css';

export interface BaseLLVMTableProps {
  rows: LLVMRow[];
}

export interface LLVMTableProps extends BaseLLVMTableProps {
  children?: never;
}

export const LLVMTable: FC<LLVMTableProps> = (props) => {
  const {rows} = props;
  return (
    <div className={styles.scroll}>
      <table>
        <thead>
          <tr>
            <th>Tool</th><th>Mode</th><th>Profile</th><th>Result</th>
            <th>Elapsed</th><th>Executors × Ninja jobs</th><th>Build targets</th><th>Topology</th><th>LLVM source</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.id}>
              <td><code>{row.tool}</code></td><td>{row.mode}</td><td>{row.profile}</td><td>{row.status}</td>
              <td className={styles.number}>{row.elapsed}</td><td>{row.capacity}</td><td>{row.targets}</td>
              <td>{row.topology}</td><td><code>{row.source}</code></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};
