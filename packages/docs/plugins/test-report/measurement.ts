export interface MeasurementReference {
  source: 'measurement' | 'evidence';
  pointer: string;
  evidenceId?: string;
}

export interface MeasurementQuantity {
  value: number | null;
  evidence: MeasurementReference[];
  reason?: string;
}

export interface MeasurementVertex {
  id: string;
  operation: string;
  isVirtual: boolean;
  durationInputs: MeasurementReference[];
  durationRule?: string;
}

export interface MeasurementInterval {
  taskId: string;
  attemptId: string;
  nodeId: string;
  status: string;
  operation: string;
  boundary: string;
  durationSeconds: number | null;
  clockDomain: string;
  clockKind: string;
  startSeconds?: number | null;
  endSeconds?: number | null;
  evidence: MeasurementReference[];
  reason?: string;
}

export interface MeasurementWindow {
  id: string;
  boundary: string;
  seconds: number | null;
  clockDomain: string;
  clockKind: string;
  evidence: MeasurementReference[];
  containsTasks?: string[];
  containmentEvidence?: MeasurementReference[];
  includedSteps?: string[];
  aggregation?: string;
  reason?: string;
}

export interface MeasurementCalculation {
  id: string;
  formula: string;
  inputs: Record<string, MeasurementReference>;
  value: number | null;
  unit: string;
  status: 'computed' | 'unavailable' | 'inapplicable';
  reasons: string[];
  model: string;
  clockMethod: string;
  criticalPath?: string[];
}

export interface ExecutionMeasurement {
  schema: string;
  trial: {
    id: string; repetition: number; status: string; tool: string; mode: string; profile: string; workload: string;
    sourceRevision: string; binarySha256: string; protocolRevision: string; configurationSha256: string;
    toolchain: Record<string, string>;
    evidence: {id: string; path?: string; uri?: string; sha256: string}[];
  };
  graph: {
    id: string; scope: string; provenance: string; vertices: MeasurementVertex[];
    edges: {from: string; to: string; kind: string}[];
    counts: {vertices: number; edges: number; virtualVertices: number; timedVertices: number};
    omittedWork: string[]; evidence: MeasurementReference[];
  };
  resources: {
    nodes: Record<string, unknown>[]; pools: Record<string, unknown>[]; nested: Record<string, unknown>[];
    taskSlotLimit: MeasurementQuantity; usableOnePoolSlots: MeasurementQuantity;
  };
  taskIntervals: MeasurementInterval[];
  timeWindows: MeasurementWindow[];
  observedPeakOverlap: MeasurementQuantity;
  analysis: MeasurementCalculation[];
  clockCompatibility: {
    method: string; referenceDomain: string; domains: string[]; assumptions: string[];
    uncertainty: {relativeRateErrorBound: null; reason: string}; evidence: MeasurementReference[];
  };
  premises: {precedenceValid?: boolean; enclosingWindowId?: string; evidence?: MeasurementReference[]; [key: string]: unknown};
  [key: string]: unknown;
}

const MEASUREMENT_SCHEMA = 'ccme-execution-measurement/v1';
const NOMINAL_METHOD = 'nominal-seconds';
const COMPARISON_TOLERANCE = 1e-9;
const DIGEST_PATTERN = /^[a-f0-9]{64}$/;
const INTERVAL_POINTER_PATTERN = /^\/taskIntervals\/(0|[1-9][0-9]*)\/durationSeconds$/;
const SUCCESS_STATUSES = ['completed', 'success'];
const FAILED_SCHEDULE_STATUSES = ['failed', 'timeout'];
const COMPLETED_TRIAL_STATUS = 'completed';
const ELIGIBLE_SCHEDULE_STATUS = 'eligible';
const MISSING_SCHEDULE_STATUS = 'missing';
const VIOLATED_SCHEDULE_STATUS = 'violated';
const FAILURE_FREE_ATTEMPT_COUNT = 1;
const CLOCK_KINDS = ['monotonic', 'wall', 'bounded-mapping'];
const TRIAL_STATUSES = ['completed', 'failed', 'timeout', 'blocked', 'unsupported'];
const GRAPH_SCOPES = ['native-task-graph', 'analysis-projection', 'fixture-workload'];

interface AssertionOptions { condition: unknown; at: string; reason: string }
function requireMeasurement(options: AssertionOptions): asserts options is AssertionOptions & {condition: true} {
  const {condition, at, reason} = options;
  if (!condition) throw new Error(`${at}: executionMeasurement ${reason}`);
}

interface EqualOptions { left: number; right: number }
function isNear(options: EqualOptions): boolean {
  const {left, right} = options;
  return Number.isFinite(left) && Number.isFinite(right) && left >= 0 && right >= 0 && Math.abs(left - right) <= COMPARISON_TOLERANCE * Math.max(1, Math.abs(left), Math.abs(right));
}

interface ReferenceOptions { root: ExecutionMeasurement; reference: MeasurementReference; at: string }
export function resolveMeasurementReference(options: ReferenceOptions): unknown {
  const {root, reference, at} = options;
  requireMeasurement({condition: reference && typeof reference.pointer === 'string', at, reason: 'reference lacks a JSON pointer'});
  if (reference.source === 'evidence') {
    requireMeasurement({condition: root.trial.evidence.some((record) => record.id === reference.evidenceId) && (reference.pointer === '' || reference.pointer.startsWith('/')), at, reason: 'reference names missing evidence'});
    return undefined;
  }
  requireMeasurement({condition: reference.source === 'measurement' && !reference.evidenceId && (reference.pointer === '' || reference.pointer.startsWith('/')), at, reason: 'reference source is invalid'});
  if (reference.pointer === '') return root;
  return reference.pointer.slice(1).split('/').reduce<unknown>((container, encoded) => {
    requireMeasurement({condition: !/~(?:[^01]|$)/.test(encoded), at, reason: 'JSON pointer escape is invalid'});
    const key = encoded.replaceAll('~1', '/').replaceAll('~0', '~');
    requireMeasurement({condition: container !== null && typeof container === 'object' && Object.hasOwn(container, key) && (!Array.isArray(container) || /^(0|[1-9][0-9]*)$/.test(key)), at, reason: `JSON pointer does not resolve: ${reference.pointer}`});
    return (container as Record<string, unknown>)[key];
  }, root);
}

interface QuantityOptions { quantity: MeasurementQuantity; at: string; isInteger?: boolean; allowZero?: boolean }
function validateQuantity(options: QuantityOptions): void {
  const {quantity, at, isInteger = false, allowZero = false} = options;
  requireMeasurement({condition: quantity && Array.isArray(quantity.evidence) && (quantity.reason === undefined || typeof quantity.reason === 'string'), at, reason: 'quantity lacks evidence records'});
  if (quantity.value === null) {
    requireMeasurement({condition: typeof quantity.reason === 'string' && quantity.reason.length > 0, at, reason: 'null quantity lacks a reason'});
    return;
  }
  requireMeasurement({condition: typeof quantity.value === 'number' && Number.isFinite(quantity.value) && quantity.value >= 0 && quantity.evidence.length > 0 && (!isInteger || (Number.isInteger(quantity.value) && (allowZero || quantity.value > 0))), at, reason: 'quantity is not finite, evidenced, or in the required units'});
}

interface ValidateMeasurementOptions { value: unknown; at: string }
export function validateExecutionMeasurement(options: ValidateMeasurementOptions): ExecutionMeasurement {
  const {value, at} = options;
  requireMeasurement({condition: value !== null && typeof value === 'object' && !Array.isArray(value), at, reason: 'must be an object'});
  const measurement = value as ExecutionMeasurement;
  const {trial, graph, resources, taskIntervals, timeWindows, clockCompatibility: clock} = measurement;
  requireMeasurement({condition: measurement.schema === MEASUREMENT_SCHEMA && trial && graph && resources && clock && measurement.premises, at, reason: 'required schema sections are missing'});
  requireMeasurement({condition: ['id', 'tool', 'mode', 'profile', 'workload', 'sourceRevision', 'protocolRevision'].every((key) => typeof trial[key as keyof typeof trial] === 'string' && trial[key as keyof typeof trial] !== '') && Number.isInteger(trial.repetition) && trial.repetition > 0 && TRIAL_STATUSES.includes(trial.status) && DIGEST_PATTERN.test(trial.binarySha256) && DIGEST_PATTERN.test(trial.configurationSha256) && trial.toolchain && typeof trial.toolchain === 'object' && !Array.isArray(trial.toolchain) && Object.keys(trial.toolchain).length > 0 && Object.values(trial.toolchain).every((version) => typeof version === 'string'), at, reason: 'trial identities are invalid'});
  requireMeasurement({condition: [trial.evidence, graph.vertices, graph.edges, graph.omittedWork, graph.evidence, resources.nodes, resources.pools, resources.nested, taskIntervals, timeWindows, measurement.analysis].every(Array.isArray), at, reason: 'record arrays are missing'});
  const evidenceIDs = new Set(trial.evidence.map((record) => record.id));
  requireMeasurement({condition: evidenceIDs.size === trial.evidence.length && trial.evidence.length > 0 && trial.evidence.every((record) => typeof record.id === 'string' && record.id && ((typeof record.path === 'string' && record.path) || (typeof record.uri === 'string' && record.uri)) && DIGEST_PATTERN.test(record.sha256)), at, reason: 'evidence identities or digests are invalid'});
  requireMeasurement({condition: typeof graph.id === 'string' && graph.id && typeof graph.provenance === 'string' && graph.provenance && GRAPH_SCOPES.includes(graph.scope) && graph.evidence.length > 0 && graph.omittedWork.every((item) => typeof item === 'string'), at, reason: 'graph scope/provenance is missing'});
  const nodeIDs = new Set(resources.nodes.map((node) => node.id));
  requireMeasurement({condition: nodeIDs.size === resources.nodes.length && resources.nodes.every((node) => typeof node.id === 'string' && node.id && Object.hasOwn(node, 'hostId')), at, reason: 'node identities are invalid'});
  [resources.taskSlotLimit, resources.usableOnePoolSlots].forEach((quantity) => validateQuantity({quantity, at, isInteger: true}));
  validateQuantity({quantity: measurement.observedPeakOverlap, at, isInteger: true, allowZero: true});
  requireMeasurement({condition: measurement.premises.precedenceValid !== false || (typeof measurement.premises.precedenceReason === 'string' && measurement.premises.precedenceReason.trim().length > 0), at, reason: 'false precedence premise lacks its recorded reason'});
  const intervalIDs = new Set<string>();
  const attemptNodes = new Map<string, string>();
  taskIntervals.forEach((interval) => {
    const identity = [interval.taskId, interval.attemptId, interval.operation].join('\0');
    requireMeasurement({condition: [interval.taskId, interval.attemptId, interval.operation, interval.boundary, interval.status].every((field) => typeof field === 'string' && field.length > 0) && nodeIDs.has(interval.nodeId) && !intervalIDs.has(identity), at, reason: 'task interval identity is invalid'});
    intervalIDs.add(identity);
    const attemptIdentity = JSON.stringify([interval.taskId, interval.attemptId]);
    requireMeasurement({condition: !attemptNodes.has(attemptIdentity) || attemptNodes.get(attemptIdentity) === interval.nodeId, at, reason: 'one task attempt names different physical executors'});
    attemptNodes.set(attemptIdentity, interval.nodeId);
    validateQuantity({quantity: {value: interval.durationSeconds, evidence: interval.evidence, reason: interval.reason}, at});
    requireMeasurement({condition: interval.clockDomain && CLOCK_KINDS.includes(interval.clockKind) && (interval.startSeconds == null) === (interval.endSeconds == null), at, reason: 'task clock is invalid'});
    if (interval.startSeconds != null) requireMeasurement({condition: typeof interval.startSeconds === 'number' && typeof interval.endSeconds === 'number' && Number.isFinite(interval.startSeconds) && Number.isFinite(interval.endSeconds) && interval.endSeconds >= interval.startSeconds && interval.durationSeconds !== null && isNear({left: interval.endSeconds - interval.startSeconds, right: interval.durationSeconds}), at, reason: 'task endpoints contradict duration'});
  });
  const windowIDs = new Set<string>();
  timeWindows.forEach((window) => {
    requireMeasurement({condition: [window.id, window.boundary, window.clockDomain].every((field) => typeof field === 'string' && field.length > 0) && !windowIDs.has(window.id) && CLOCK_KINDS.includes(window.clockKind) && [window.containsTasks, window.includedSteps].every((entries) => entries === undefined || (Array.isArray(entries) && entries.every((entry) => typeof entry === 'string'))) && (window.aggregation === undefined || typeof window.aggregation === 'string'), at, reason: 'window identity/boundary/clock is invalid'});
    windowIDs.add(window.id);
    validateQuantity({quantity: {value: window.seconds, evidence: window.evidence, reason: window.reason}, at});
    if (window.id === 'step-sum') requireMeasurement({condition: window.includedSteps?.length && window.aggregation, at, reason: 'step sum lacks selected steps and aggregation convention'});
  });
  requireMeasurement({condition: clock.method === NOMINAL_METHOD && Array.isArray(clock.domains) && clock.domains.every((domain) => typeof domain === 'string' && domain.trim().length > 0) && new Set(clock.domains).size === clock.domains.length && clock.referenceDomain && clock.domains.includes(clock.referenceDomain) && Array.isArray(clock.assumptions) && clock.assumptions.length > 0 && clock.assumptions.every((assumption) => typeof assumption === 'string' && assumption.trim().length > 0) && clock.uncertainty?.relativeRateErrorBound === null && typeof clock.uncertainty.reason === 'string' && clock.uncertainty.reason.trim().length > 0 && Array.isArray(clock.evidence) && taskIntervals.every((interval) => clock.domains.includes(interval.clockDomain)) && timeWindows.every((window) => window.seconds === null || clock.domains.includes(window.clockDomain)), at, reason: 'nominal clock assumptions/unknown uncertainty are missing'});
  const references = [...graph.evidence, ...graph.vertices.flatMap((vertex) => vertex.durationInputs), ...taskIntervals.flatMap((interval) => interval.evidence), ...timeWindows.flatMap((window) => [...window.evidence, ...(window.containmentEvidence ?? [])]), ...resources.taskSlotLimit.evidence, ...resources.usableOnePoolSlots.evidence, ...measurement.observedPeakOverlap.evidence, ...(measurement.premises.evidence ?? []), ...clock.evidence, ...measurement.analysis.flatMap((calculation) => Object.values(calculation.inputs))];
  references.forEach((reference) => resolveMeasurementReference({root: measurement, reference, at}));
  const selectedGraph = selectGraph({measurement, at});
  validateCalculations({measurement, at, graph: selectedGraph});
  validateTimeline({measurement, at, graph: selectedGraph});
  return measurement;
}

interface CalculationValidationOptions { measurement: ExecutionMeasurement; at: string }
interface SelectedGraph {
  selected: Map<string, MeasurementInterval[]>;
  durations: Map<string, number>;
  order: string[];
  reasons: string[];
  violations: string[];
  work: number;
  critical: number;
  path: string[];
  schedulePremise: SchedulePremise;
}
type SchedulePremise = typeof ELIGIBLE_SCHEDULE_STATUS | typeof MISSING_SCHEDULE_STATUS | typeof VIOLATED_SCHEDULE_STATUS;
const MISSING_DURATION_REASON = 'required operation has no complete successful duration';
const PHASE_OVERLAP_REASON = 'sum-disjoint duration inputs contain overlapping selected phases';
const DISCONNECTED_DURATION_REASON = 'duration aggregation lacks comparable, disjoint phase intervals';
const INVALID_SCHEDULE_REASON = 'failed or repeated attempts do not establish a failure-free modeled schedule';
const MISSING_SCHEDULE_REASON = 'successful completion of the modeled schedule is not established';
const NON_MONOTONIC_REASON = 'nominal duration arithmetic requires local monotonic elapsed measurements';
const MISSING_PRECEDENCE_REASON = 'modeled precedence has not been verified';
const VIOLATED_PRECEDENCE_REASON = 'retained modeled precedence is violated';
const MISSING_CAPACITY_REASON = 'no evidenced unit-task concurrency upper bound';
const MISSING_CONTAINMENT_REASON = 'no named enclosing interval with complete task-containment evidence';
const CONTAINMENT_VIOLATION_REASON = 'comparable modeled task intervals cannot fit inside the declared enclosing window';
const ZERO_DENOMINATOR_REASON = 'the selected bound is zero; division is undefined';
const CAPACITY_VIOLATION_REASON = 'comparable retained intervals exceed the declared task-slot ceiling';
const PRECEDENCE_VIOLATION_REASON = 'comparable retained intervals violate modeled precedence, including virtual-relay paths';
const UPPER_REASONS = ['one-pool upper-bound evaluation is unsupported by this nominal reporting profile', 'fixed available interchangeable slots and all CCME §13.11 clause 2 premises are not established'];

interface SchedulePremiseOptions {measurement: ExecutionMeasurement; hasCompleteDurations: boolean}
function classifySchedulePremise(options: SchedulePremiseOptions): SchedulePremise {
  const {measurement, hasCompleteDurations} = options;
  const {trial, taskIntervals} = measurement;
  if (FAILED_SCHEDULE_STATUSES.includes(trial.status)) return VIOLATED_SCHEDULE_STATUS;
  const attempts = new Map<string, Set<string>>();
  for (const interval of taskIntervals) {
    if (FAILED_SCHEDULE_STATUSES.includes(interval.status)) return VIOLATED_SCHEDULE_STATUS;
    const identities = attempts.get(interval.taskId) ?? new Set<string>();
    identities.add(interval.attemptId);
    attempts.set(interval.taskId, identities);
    if (identities.size > FAILURE_FREE_ATTEMPT_COUNT) return VIOLATED_SCHEDULE_STATUS;
  }
  if (trial.status !== COMPLETED_TRIAL_STATUS || !hasCompleteDurations || taskIntervals.some((interval) => !SUCCESS_STATUSES.includes(interval.status))) return MISSING_SCHEDULE_STATUS;
  return ELIGIBLE_SCHEDULE_STATUS;
}

function selectGraph(options: CalculationValidationOptions): SelectedGraph {
  const {measurement, at} = options;
  const {graph, taskIntervals} = measurement;
  const vertices = new Map(graph.vertices.map((vertex) => [vertex.id, vertex]));
  requireMeasurement({condition: vertices.size === graph.vertices.length && graph.vertices.every((vertex) => typeof vertex.id === 'string' && vertex.id && typeof vertex.operation === 'string' && vertex.operation && typeof vertex.isVirtual === 'boolean' && (vertex.durationRule === undefined || typeof vertex.durationRule === 'string') && Array.isArray(vertex.durationInputs) && (!vertex.isVirtual || vertex.durationInputs.length === 0)), at, reason: 'vertex identity or virtual duration is invalid'});
  const typedEdges = new Set(graph.edges.map((edge) => JSON.stringify([edge.from, edge.to, edge.kind])));
  requireMeasurement({condition: typedEdges.size === graph.edges.length && graph.edges.every((edge) => vertices.has(edge.from) && vertices.has(edge.to) && typeof edge.kind === 'string' && edge.kind), at, reason: 'typed edge is duplicated or has an unknown endpoint'});
  requireMeasurement({condition: taskIntervals.every((interval) => vertices.has(interval.taskId)), at, reason: 'interval names a missing graph vertex'});
  const selected = new Map<string, MeasurementInterval[]>();
  const durations = new Map<string, number>();
  const reasons: string[] = [];
  for (const vertex of graph.vertices) {
    const intervals = vertex.durationInputs.map((reference) => {
      const match = INTERVAL_POINTER_PATTERN.exec(reference.pointer);
      requireMeasurement({condition: reference.source === 'measurement' && !reference.evidenceId && match, at, reason: 'duration must reference its retained interval'});
      const interval = taskIntervals[Number(match![1])];
      requireMeasurement({condition: interval && interval.taskId === vertex.id, at, reason: 'duration names another task'});
      return interval;
    });
    requireMeasurement({condition: new Set(vertex.durationInputs.map((reference) => reference.pointer)).size === intervals.length, at, reason: 'duration input is counted twice'});
    selected.set(vertex.id, intervals);
    if (vertex.isVirtual) { durations.set(vertex.id, 0); continue; }
    const reason = missingVertexDuration({vertex, intervals});
    if (reason) { reasons.push(`${vertex.id}: ${reason}`); continue; }
    durations.set(vertex.id, intervals.reduce((sum, interval) => sum + interval.durationSeconds!, 0));
  }
  const indegrees = new Map(graph.vertices.map((vertex) => [vertex.id, 0]));
  for (const edge of graph.edges) indegrees.set(edge.to, indegrees.get(edge.to)! + 1);
  const queue = graph.vertices.filter((vertex) => indegrees.get(vertex.id) === 0).map((vertex) => vertex.id);
  const order: string[] = [];
  while (queue.length) {
    const identity = queue.shift()!;
    order.push(identity);
    for (const edge of graph.edges.filter((edge) => edge.from === identity)) {
      indegrees.set(edge.to, indegrees.get(edge.to)! - 1);
      if (indegrees.get(edge.to) === 0) queue.push(edge.to);
    }
  }
  requireMeasurement({condition: order.length === vertices.size, at, reason: 'graph is cyclic'});
  const durationViolations = graph.vertices.filter((vertex) => vertex.durationRule === 'sum-disjoint').flatMap((vertex) => {
    const intervals = (selected.get(vertex.id) ?? []).filter((interval) => interval.durationSeconds !== null && SUCCESS_STATUSES.includes(interval.status) && typeof interval.startSeconds === 'number' && typeof interval.endSeconds === 'number');
    const isOverlapping = intervals.some((interval, index) => intervals.slice(0, index).some((previous) => previous.clockDomain === interval.clockDomain && previous.clockKind === interval.clockKind && previous.endSeconds! > previous.startSeconds! && interval.endSeconds! > interval.startSeconds! && previous.startSeconds! < interval.endSeconds! && interval.startSeconds! < previous.endSeconds!));
    return isOverlapping ? [`${vertex.id}: ${PHASE_OVERLAP_REASON}`] : [];
  });
  const schedulePremise = classifySchedulePremise({measurement, hasCompleteDurations: durations.size === vertices.size});
  const result: SelectedGraph = {selected, durations, order, reasons, violations: durationViolations, work: 0, critical: 0, path: [], schedulePremise};
  const counts = graph.counts;
  requireMeasurement({condition: counts && counts.vertices === vertices.size && counts.edges === graph.edges.length && counts.virtualVertices === graph.vertices.filter((vertex) => vertex.isVirtual).length && counts.timedVertices === graph.vertices.filter((vertex) => !vertex.isVirtual && durations.has(vertex.id)).length, at, reason: 'counts differ from retained graph/coverage'});
  if (reasons.length) return result;
  const finishes = new Map<string, number>();
  const paths = new Map<string, string[]>();
  for (const identity of order) {
    const duration = durations.get(identity)!;
    result.work += duration;
    const finish = (finishes.get(identity) ?? 0) + duration;
    finishes.set(identity, finish);
    const path = [...(paths.get(identity) ?? []), identity];
    paths.set(identity, path);
    if (finish > result.critical || result.path.length === 0) { result.critical = finish; result.path = path; }
    for (const edge of graph.edges.filter((edge) => edge.from === identity)) {
      if (finish > (finishes.get(edge.to) ?? 0) || !paths.get(edge.to)?.length) { finishes.set(edge.to, finish); paths.set(edge.to, path); }
    }
  }
  return result;
}

interface VertexDurationOptions {vertex: MeasurementVertex; intervals: MeasurementInterval[]}
function missingVertexDuration(options: VertexDurationOptions): string | undefined {
  const {vertex, intervals} = options;
  if (intervals.length === 0) return MISSING_DURATION_REASON;
  if (intervals.some((interval) => interval.durationSeconds === null || !SUCCESS_STATUSES.includes(interval.status))) return MISSING_DURATION_REASON;
  if (intervals.length > 1 && vertex.durationRule !== 'sum-disjoint') return DISCONNECTED_DURATION_REASON;
  if (intervals.length > 1 && intervals.some((interval, index) => interval.startSeconds == null || interval.endSeconds == null || intervals.slice(0, index).some((previous) => previous.clockDomain !== interval.clockDomain || previous.clockKind !== interval.clockKind || (previous.endSeconds! > previous.startSeconds! && interval.endSeconds! > interval.startSeconds! && previous.startSeconds! < interval.endSeconds! && interval.startSeconds! < previous.endSeconds!)))) return DISCONNECTED_DURATION_REASON;
  return undefined;
}

interface CalculationState {value: number | null; status: MeasurementCalculation['status']; reasons: string[]; path?: string[]}
interface DerivedValidationOptions extends CalculationValidationOptions {graph: SelectedGraph}
function validateCalculations(options: DerivedValidationOptions): void {
  const {measurement, at, graph} = options;
  const {resources, analysis} = measurement;
  const hasMonotonicDurations = [...graph.selected.values()].flat().every((interval) => interval.durationSeconds === null || interval.clockKind === 'monotonic');
  const work: CalculationState = graph.violations.length ? {value: null, status: 'inapplicable', reasons: [...graph.violations, ...graph.reasons]} : !hasMonotonicDurations ? {value: null, status: 'inapplicable', reasons: [NON_MONOTONIC_REASON, ...graph.reasons]} : graph.reasons.length ? {value: null, status: 'unavailable', reasons: graph.reasons} : {value: graph.work, status: 'computed', reasons: []};
  const critical: CalculationState = {...work, value: work.value === null ? null : graph.critical, path: work.value === null ? [] : graph.path};
  const violations = findScheduleViolations(options);
  const missing: string[] = [];
  if (work.status === 'inapplicable') violations.push(...work.reasons);
  else if (work.value === null) missing.push(...work.reasons);
  if (graph.schedulePremise === VIOLATED_SCHEDULE_STATUS) violations.push(INVALID_SCHEDULE_REASON);
  else if (graph.schedulePremise === MISSING_SCHEDULE_STATUS) missing.push(MISSING_SCHEDULE_REASON);
  if (measurement.premises.precedenceValid === false) violations.push(VIOLATED_PRECEDENCE_REASON);
  else if (measurement.premises.precedenceValid !== true || !measurement.premises.evidence?.length) missing.push(MISSING_PRECEDENCE_REASON);
  if (resources.taskSlotLimit.value === null) missing.push(MISSING_CAPACITY_REASON);
  const slot: CalculationState = violations.length ? {value: null, status: 'inapplicable', reasons: [...violations, ...missing]} : missing.length ? {value: null, status: 'unavailable', reasons: missing} : {value: Math.max(graph.critical, graph.work / resources.taskSlotLimit.value!), status: 'computed', reasons: []};
  const window = measurement.timeWindows.find((candidate) => candidate.id === measurement.premises.enclosingWindowId && candidate.seconds !== null && candidate.containmentEvidence?.length && measurement.graph.vertices.every((vertex) => vertex.isVirtual || candidate.containsTasks?.includes(vertex.id)));
  const ratio: CalculationState = {value: null, status: slot.value === null ? slot.status : 'unavailable', reasons: slot.value === null ? [...slot.reasons] : []};
  if (!window) ratio.reasons.push(MISSING_CONTAINMENT_REASON);
  if (slot.value === 0) { ratio.status = 'inapplicable'; ratio.reasons.push(ZERO_DENOMINATOR_REASON); }
  if (window && window.clockKind !== 'monotonic') { ratio.status = 'inapplicable'; ratio.reasons.push(NON_MONOTONIC_REASON); }
  if (window && contradictsContainment({graph, window})) { ratio.status = 'inapplicable'; ratio.reasons.push(CONTAINMENT_VIOLATION_REASON); }
  if (!ratio.reasons.length) { ratio.value = window!.seconds! / slot.value!; ratio.status = 'computed'; }
  const upperViolations = [...graph.violations, ...(graph.schedulePremise === VIOLATED_SCHEDULE_STATUS ? [INVALID_SCHEDULE_REASON] : []), ...(!hasMonotonicDurations ? [NON_MONOTONIC_REASON] : []), ...findScheduleViolations(options), ...(measurement.premises.precedenceValid === false ? [VIOLATED_PRECEDENCE_REASON] : [])];
  const upperMissing = [...UPPER_REASONS, ...(graph.schedulePremise === MISSING_SCHEDULE_STATUS ? [MISSING_SCHEDULE_REASON] : [])];
  const expected = [work, critical, slot, ratio, {value: null, status: upperViolations.length ? 'inapplicable' : 'unavailable', reasons: [...upperViolations, ...upperMissing]}];
  const identities = ['nominal-work', 'nominal-critical-path', 'nominal-slot-bound', 'nominal-enclosing-ratio', 'one-pool-upper-bound'];
  const formulas = ['W_nom = sum_i d_i', 'F_i = d_i + max_{j -> i} F_j; L_nom = max_i F_i', 'B_nom = max(L_nom, W_nom / s)', 'R_nom = T_enclosing / B_nom', 'U = W/s + (1 - 1/s)*L'];
  const internal = (pointer: string): MeasurementReference => ({source: 'measurement', pointer});
  const selectedInputs: Record<string, MeasurementReference>[] = [
    Object.fromEntries(measurement.graph.vertices.map((vertex, index) => [vertex.id, internal(`/graph/vertices/${index}/durationInputs`)])),
    {d: internal('/graph/vertices'), E: internal('/graph/edges')},
    {W_nom: internal('/analysis/0/value'), L_nom: internal('/analysis/1/value'), s: internal('/resources/taskSlotLimit/value')},
    {B_nom: internal('/analysis/2/value'), ...(window ? {T_enclosing: internal(`/timeWindows/${measurement.timeWindows.indexOf(window)}/seconds`)} : {})},
    {W: internal('/analysis/0/value'), L: internal('/analysis/1/value'), s: internal('/resources/usableOnePoolSlots/value')},
  ];
  requireMeasurement({condition: analysis.length === identities.length, at, reason: 'formula records are incomplete'});
  analysis.forEach((calculation, index) => {
    const derived = expected[index];
    requireMeasurement({condition: calculation.id === identities[index] && calculation.formula === formulas[index] && calculation.clockMethod === NOMINAL_METHOD && calculation.model === 'descriptive' && calculation.unit === (index === 3 ? 'dimensionless' : NOMINAL_METHOD) && Array.isArray(calculation.reasons) && JSON.stringify(calculation.reasons) === JSON.stringify(derived.reasons) && calculation.status === derived.status, at, reason: 'formula identity, units, applicability, reasons or clock method differ'});
    const inputs = selectedInputs[index];
    requireMeasurement({condition: Object.keys(calculation.inputs).length === Object.keys(inputs).length && Object.entries(inputs).every(([symbol, reference]) => calculation.inputs[symbol]?.source === reference.source && calculation.inputs[symbol]?.pointer === reference.pointer && !calculation.inputs[symbol]?.evidenceId), at, reason: 'formula references do not select the calculated inputs'});
    requireMeasurement({condition: derived.value === null ? calculation.value === null : typeof calculation.value === 'number' && isNear({left: calculation.value, right: derived.value}), at, reason: 'formula differs from recorded inputs'});
    const path = calculation.criticalPath ?? [];
    if (index !== 1 || critical.value === null || measurement.graph.vertices.length === 0) {
      requireMeasurement({condition: Array.isArray(path) && path.length === 0, at, reason: 'uncomputed or non-path calculation carries a critical path'});
      return;
    }
    requireMeasurement({condition: Array.isArray(path) && path.length > 0 && new Set(path).size === path.length && path.every((identity, pathIndex) => graph.durations.has(identity) && (pathIndex === 0 || measurement.graph.edges.some((edge) => edge.from === path[pathIndex - 1] && edge.to === identity))) && isNear({left: path.reduce((sum, identity) => sum + graph.durations.get(identity)!, 0), right: graph.critical}), at, reason: 'critical path does not attain the retained-graph recurrence'});
  });
}

interface ContainmentOptions {graph: SelectedGraph; window: MeasurementWindow}
function contradictsContainment(options: ContainmentOptions): boolean {
  const {graph, window} = options;
  if (window.seconds === null || window.clockKind !== 'monotonic') return false;
  const intervals = [...graph.selected.values()].flat().filter((interval) => interval.durationSeconds !== null && SUCCESS_STATUSES.includes(interval.status) && interval.clockKind === 'monotonic' && interval.clockDomain === window.clockDomain);
  if (intervals.some((interval) => interval.durationSeconds! > window.seconds!)) return true;
  const coordinates = intervals.filter(isComparable);
  return coordinates.length > 0 && Math.max(...coordinates.map((interval) => interval.endSeconds)) - Math.min(...coordinates.map((interval) => interval.startSeconds)) > window.seconds;
}

interface ComparableInterval extends MeasurementInterval {startSeconds: number; endSeconds: number}
function isComparable(interval: MeasurementInterval): interval is ComparableInterval {
  return interval.clockKind === 'monotonic' && typeof interval.startSeconds === 'number' && typeof interval.endSeconds === 'number';
}
interface PeakOptions {intervals: ComparableInterval[]}
function calculatePeak(options: PeakOptions): number {
  // Count each selected attempt only during its selected phases. Retained
  // verification records and gaps between disjoint phases consume no slots.
  const intervals = options.intervals.filter((interval) => interval.endSeconds > interval.startSeconds);
  const events = intervals.flatMap((interval) => [{seconds: interval.startSeconds, delta: 1, identity: JSON.stringify([interval.taskId, interval.attemptId])}, {seconds: interval.endSeconds, delta: -1, identity: JSON.stringify([interval.taskId, interval.attemptId])}]).sort((left, right) => left.seconds - right.seconds || left.delta - right.delta);
  const running = new Map<string, number>();
  const peaks = events.map((event) => {
    const count = (running.get(event.identity) ?? 0) + event.delta;
    if (count === 0) running.delete(event.identity); else running.set(event.identity, count);
    return running.size;
  });
  return Math.max(0, ...peaks);
}

function findScheduleViolations(options: DerivedValidationOptions): string[] {
  const {measurement, graph} = options;
  const violations: string[] = [];
  const selected = [...graph.selected.values()].flat().filter((interval) => interval.durationSeconds !== null && SUCCESS_STATUSES.includes(interval.status)).filter(isComparable);
  const domains = new Set(selected.map((interval) => interval.clockDomain));
  if (measurement.resources.taskSlotLimit.value !== null && [...domains].some((domain) => calculatePeak({intervals: selected.filter((interval) => interval.clockDomain === domain)}) > measurement.resources.taskSlotLimit.value!)) violations.push(CAPACITY_VIOLATION_REASON);
  const finishes = new Map<string, Map<string, number>>();
  const broken: string[] = [];
  for (const identity of graph.order) {
    const prior = finishes.get(identity) ?? new Map<string, number>();
    finishes.set(identity, prior);
    const intervals = (graph.selected.get(identity) ?? []).filter((interval) => interval.durationSeconds !== null && SUCCESS_STATUSES.includes(interval.status)).filter(isComparable);
    for (const interval of intervals) {
      const required = prior.get(interval.clockDomain);
      if (required !== undefined && interval.startSeconds < required) broken.push(identity);
    }
    for (const interval of intervals) prior.set(interval.clockDomain, Math.max(prior.get(interval.clockDomain) ?? -Infinity, interval.endSeconds));
    for (const edge of measurement.graph.edges.filter((edge) => edge.from === identity)) {
      const successor = finishes.get(edge.to) ?? new Map<string, number>();
      finishes.set(edge.to, successor);
      for (const [domain, finish] of prior) successor.set(domain, Math.max(successor.get(domain) ?? -Infinity, finish));
    }
  }
  if (broken.length) violations.push(PRECEDENCE_VIOLATION_REASON);
  return violations;
}

function validateTimeline(options: DerivedValidationOptions): void {
  const {measurement, graph, at} = options;
  const window = measurement.timeWindows.find((candidate) => candidate.id === 'task-span');
  const span = window?.seconds ?? null;
  const overlap = measurement.observedPeakOverlap.value;
  if (span === null && overlap === null) return;
  const intervals = [...graph.selected.values()].flat();
  requireMeasurement({condition: graph.reasons.length === 0 && intervals.every(isComparable), at, reason: 'global task span/overlap requires complete selected comparable monotonic intervals'});
  if (intervals.length === 0) {
    requireMeasurement({condition: span === null && overlap === 0, at, reason: 'empty modeled set has overlap zero and no measured task span'});
    return;
  }
  const domain = intervals[0].clockDomain;
  requireMeasurement({condition: intervals.every((interval) => interval.clockDomain === domain) && (span === null || (window!.clockDomain === domain && window!.clockKind === 'monotonic')), at, reason: 'global task span/overlap requires one correctly identified monotonic timeline'});
  const expectedSpan = Math.max(...intervals.map((interval) => interval.endSeconds!)) - Math.min(...intervals.map((interval) => interval.startSeconds!));
  const peak = calculatePeak({intervals: intervals.filter(isComparable)});
  requireMeasurement({condition: (span === null || isNear({left: span, right: expectedSpan})) && (overlap === null || overlap === peak), at, reason: 'global task span/overlap differs from selected operation coordinates'});
}
