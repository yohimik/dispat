import type {FC} from 'react';
import type {ExecutionMeasurement as Measurement} from '@site/plugins/test-report/measurement';
import styles from './styles.module.css';

export interface BaseExecutionMeasurementProps { measurement: Measurement }
export interface ExecutionMeasurementProps extends BaseExecutionMeasurementProps { children?: never }

const DISPLAY_SIGNIFICANT_DIGITS = 6;
const RAW_INDENT_SPACES = 2;
const UNKNOWN_TEXT = 'unknown';

interface FormatValueOptions { value: number | null; reason?: string }
function formatValue(options: FormatValueOptions): string {
  const {value, reason} = options;
  if (value === null) return reason ? `${UNKNOWN_TEXT}: ${reason}` : UNKNOWN_TEXT;
  return Number(value.toPrecision(DISPLAY_SIGNIFICANT_DIGITS)).toString();
}

export const ExecutionMeasurement: FC<ExecutionMeasurementProps> = (props) => {
  const {measurement} = props;
  const {trial, graph, resources, taskIntervals, timeWindows, analysis, clockCompatibility: clock} = measurement;
  return <details>
    <summary>Recorded inputs and formula calculations — {trial.tool}, {trial.mode}, {trial.profile}, repetition {trial.repetition}</summary>
    <p>Trial <code>{trial.id}</code>: {trial.status}. Workload source <code>{trial.sourceRevision}</code>.</p>
    <h4>Captured inputs</h4>
    <p>Graph <code>{graph.id}</code> ({graph.scope}). Provenance: {graph.provenance}.</p>
    <div className={styles.scroll}><table>
      <thead><tr><th>Vertices</th><th>Typed edges</th><th>Virtual relays</th><th>Completely timed vertices</th><th>Task-slot ceiling</th><th>Available interchangeable slots</th></tr></thead>
      <tbody><tr><td>{graph.counts.vertices}</td><td>{graph.counts.edges}</td><td>{graph.counts.virtualVertices}</td><td>{graph.counts.timedVertices}</td><td>{formatValue(resources.taskSlotLimit)}</td><td>{formatValue(resources.usableOnePoolSlots)}</td></tr></tbody>
    </table></div>
    <p>Omitted work: {graph.omittedWork.join('; ') || 'none declared'}.</p>
    <details><summary>Node identities, capacities and nested build parameters</summary><pre>{JSON.stringify(resources, null, RAW_INDENT_SPACES)}</pre></details>
    <h4>Measured intervals and outcomes</h4>
    <div className={styles.scroll}><table>
      <thead><tr><th>Task / attempt / executor</th><th>Operation boundary</th><th>Local elapsed seconds</th><th>Clock domain / kind</th><th>Outcome</th></tr></thead>
      <tbody>{taskIntervals.map((interval) => <tr key={`${interval.taskId}/${interval.attemptId}/${interval.operation}`}><td>{interval.taskId} / {interval.attemptId} / {interval.nodeId}</td><td>{interval.boundary}</td><td>{formatValue({value: interval.durationSeconds, reason: interval.reason})}</td><td>{interval.clockDomain} / {interval.clockKind}</td><td>{interval.status}</td></tr>)}</tbody>
    </table></div>
    <div className={styles.scroll}><table>
      <thead><tr><th>Named window</th><th>Boundary / aggregation</th><th>Seconds</th><th>Clock domain / kind</th></tr></thead>
      <tbody>{timeWindows.map((window) => <tr key={window.id}><td>{window.id}</td><td>{window.boundary}{window.aggregation ? `; ${window.aggregation}` : ''}</td><td>{formatValue({value: window.seconds, reason: window.reason})}</td><td>{window.clockDomain} / {window.clockKind}</td></tr>)}</tbody>
    </table></div>
    <p>Observed peak overlap: {formatValue(measurement.observedPeakOverlap)}. This observation is separate from the configured task-slot ceiling.</p>
    <h4>Formula calculations</h4>
    <p>Clock method: <code>{clock.method}</code>. {clock.assumptions.join(' ')} {clock.uncertainty.reason}</p>
    <div className={styles.scroll}><table>
      <thead><tr><th>Formula</th><th>Selected inputs</th><th>Calculated value / unit</th><th>Model / applicability</th></tr></thead>
      <tbody>{analysis.map((calculation) => <tr key={calculation.id}>
        <td><code>{calculation.formula}</code></td>
        <td><details><summary>Input references ({Object.keys(calculation.inputs).length})</summary>{Object.entries(calculation.inputs).map(([symbol, reference]) => <div key={symbol}><code>{symbol} = {reference.evidenceId ?? ''}{reference.pointer}</code></div>)}</details></td>
        <td>{formatValue({value: calculation.value})} {calculation.unit}</td>
        <td>{calculation.model} / {calculation.status}. {calculation.reasons.join('; ')}{calculation.criticalPath?.length ? <div>Attaining path: {calculation.criticalPath.join(' → ')}</div> : null}</td>
      </tr>)}</tbody>
    </table></div>
    <p>These per-trial ratios are not cross-tool speedups. Raw values are retained at full precision; displayed values use six significant digits.</p>
    <details><summary>Complete raw measurement and evidence references</summary><pre>{JSON.stringify(measurement, null, RAW_INDENT_SPACES)}</pre></details>
  </details>;
};
