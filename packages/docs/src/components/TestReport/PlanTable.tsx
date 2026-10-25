import type {FC} from 'react';
import type {PlanRow} from '@site/plugins/test-report/timing';

import styles from './styles.module.css';

export interface BasePlanTableProps {
  rows: PlanRow[];
}

export interface PlanTableProps extends BasePlanTableProps {
  children?: never;
}

/** Each tool's plan of a scenario, set beside dispat's: agreement, or what differs. */
export const PlanTable: FC<PlanTableProps> = (props) => {
  const {rows} = props;

  return (
    <div className={styles.scroll}>
      <table>
        <thead>
          <tr>
            <th>Scenario</th>
            <th>Tool</th>
            <th>Plan beside dispat&apos;s</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.id}>
              <td>{row.scenario}</td>
              <td>
                <code className={styles.whole}>{row.tool}</code>
              </td>
              <td>{row.text}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};
