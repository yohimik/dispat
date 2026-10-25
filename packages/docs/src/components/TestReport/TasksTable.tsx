import type {FC} from 'react';
import type {TaskList, TaskRow} from '@site/plugins/test-report/timing';

import styles from './styles.module.css';

export interface BaseTasksTableProps {
  rows: TaskRow[];
  lists: TaskList[];
}

export interface TasksTableProps extends BaseTasksTableProps {
  children?: never;
}

/**
 * The timing cells' tasks: by kind, a row per scenario and tool, then every
 * task of each cell, folded so the wide cells' eighty do not bury the page.
 */
export const TasksTable: FC<TasksTableProps> = (props) => {
  const {rows, lists} = props;

  return (
    <>
      <div className={styles.scroll}>
        <table>
          <thead>
            <tr>
              <th>Scenario</th>
              <th>Tool</th>
              <th>Builds</th>
              <th>Publications (uploads)</th>
              <th>
                <code>npm publish</code> processes
              </th>
              <th>Builds while planning</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.id}>
                <td>{row.scenario}</td>
                <td>
                  <code className={styles.whole}>{row.tool}</code>
                </td>
                <td>{row.build}</td>
                <td>{row.publish}</td>
                <td>{row.npmPublish}</td>
                <td>{row.planBuilds}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {lists.map((list) => (
        <details key={list.id}>
          <summary>
            Every task of {list.scenario} under <code>{list.tool}</code>, {list.tasks.length} in all
          </summary>
          <div className={styles.scroll}>
            <table>
              <thead>
                <tr>
                  <th>Package</th>
                  <th>Task</th>
                  <th className={styles.number}>Started</th>
                  <th className={styles.number}>Took</th>
                </tr>
              </thead>
              <tbody>
                {list.tasks.map((task, index) => (
                  <tr key={index}>
                    <td>
                      <code className={styles.whole}>{task.package}</code>
                    </td>
                    <td>{task.task}</td>
                    <td className={styles.number}>{task.start}</td>
                    <td className={styles.number}>{task.seconds}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      ))}
    </>
  );
};
