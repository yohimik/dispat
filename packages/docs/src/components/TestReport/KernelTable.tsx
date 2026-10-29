import type {FC} from 'react';
import type {KernelRow} from '@site/plugins/test-report/kernel';
import styles from './styles.module.css';

export interface BaseKernelTableProps { rows: KernelRow[] }
export interface KernelTableProps extends BaseKernelTableProps { children?: never }

const RAW_INDENT_SPACES = 2;

export const KernelTable: FC<KernelTableProps> = (props) => {
  const {rows} = props;
  return <>
    <div className={styles.scroll}><table>
      <thead><tr>
        <th>Tool</th><th>Mode</th><th>Profile / configuration</th><th>Result</th>
        <th>Elapsed</th><th>Executors × Make jobs</th><th>Build targets</th><th>Built modules</th><th>Topology</th><th>Kernel source</th><th>Campaign Dispat binary</th>
      </tr></thead>
      <tbody>{rows.map((row) => <tr key={row.id}>
        <td><code>{row.tool}</code></td><td>{row.mode}</td><td>{row.profile} / {row.configuration}</td><td>{row.status}</td>
        <td className={styles.number}>{row.elapsed}</td><td>{row.capacity}</td><td>{row.targets}</td><td>{row.modules}</td>
        <td>{row.topology}</td><td><code>{row.source}</code></td><td><code>{row.binary}</code></td>
      </tr>)}</tbody>
    </table></div>
    {rows.map((row) => <details key={row.id}>
      <summary>{row.tool} / {row.mode}: configurations, omissions and returned artifact digests</summary>
      <pre>{JSON.stringify(row.record, null, RAW_INDENT_SPACES)}</pre>
    </details>)}
  </>;
};
