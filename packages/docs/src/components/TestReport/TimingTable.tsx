import type {FC} from 'react';
import type {TimingRow} from '@site/plugins/test-report/timing';

import styles from './styles.module.css';

export interface BaseTimingTableProps {
  rows: TimingRow[];
}

export interface TimingTableProps extends BaseTimingTableProps {
  children?: never;
}

/** The timing cells' figures, a row per scenario and tool. */
export const TimingTable: FC<TimingTableProps> = (props) => {
  const {rows} = props;

  return (
    <div className={styles.scroll}>
      <table>
        <thead>
          <tr>
            <th>Scenario</th>
            <th>Tool</th>
            <th>Relation</th>
            <th className={styles.number}>Planning</th>
            <th className={styles.number}>Makespan</th>
            <th className={styles.number}>Idle</th>
            <th className={styles.number}>Wk</th>
            <th className={styles.number}>Lk</th>
            <th className={styles.number}>s</th>
            <th className={styles.number}>Bound</th>
            <th className={styles.number}>Ratio</th>
            <th>Edges not honoured</th>
            <th>Longest chain</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.id}>
              <td>{row.scenario}</td>
              <td>
                <code className={styles.whole}>{row.tool}</code>
              </td>
              <td>
                <code>{row.relation}</code>
              </td>
              <td className={styles.number}>{row.planning}</td>
              <td className={styles.number}>{row.makespan}</td>
              <td className={styles.number}>{row.idle}</td>
              <td className={styles.number}>{row.work}</td>
              <td className={styles.number}>{row.criticalPath}</td>
              <td className={styles.number}>{row.parallelism}</td>
              <td className={styles.number}>{row.bound}</td>
              <td className={styles.number}>
                <strong>{row.ratio}</strong>
              </td>
              <td>{row.notHonoured}</td>
              <td>{row.criticalChain}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};
