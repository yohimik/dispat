import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import {validateExecutionMeasurement, resolveMeasurementReference} from './measurement.ts';

const fixture = JSON.parse(fs.readFileSync(new URL('./testdata/measurement.json', import.meta.url), 'utf8'));
const validate = (value) => validateExecutionMeasurement({value, at: 'test.executionMeasurement'});

test('Go-produced nominal formulas and all raw extra fields survive the TypeScript reader', () => {
  const parsed = validate(structuredClone(fixture));
  assert.deepEqual(parsed, fixture);
  assert.deepEqual(parsed.analysis.slice(0, 4).map((calculation) => calculation.value), [6, 4, 4, 2]);
  assert.equal(parsed.producerExtra.kept, true);
  assert.equal(parsed.observedPeakOverlap.value, null);
  assert.equal(parsed.clockCompatibility.uncertainty.relativeRateErrorBound, null);
});

test('measurement reader rejects fabricated counts, formulas, coverage, clocks, capacity and containment', () => {
  const mutations = [
    (m) => { m.graph.counts.edges++; },
    (m) => { m.graph.vertices.push(m.graph.vertices[0]); },
    (m) => { m.graph.edges.push({from: 'a', to: 'missing', kind: 'build'}); },
    (m) => { m.graph.edges.push({from: 'a', to: 'b', kind: 'build'}, {from: 'b', to: 'a', kind: 'build'}); m.graph.counts.edges = 2; },
    (m) => { m.analysis[0].value = 999; },
    (m) => { m.analysis[1].criticalPath = ['b']; },
    (m) => { m.analysis[0].model = 'fixed-duration-lower-bound'; },
    (m) => { m.taskIntervals[0].durationSeconds = null; m.taskIntervals[0].reason = 'missing'; },
    (m) => { m.resources.taskSlotLimit.value = 1.5; },
    (m) => { m.trial.status = 'failed'; },
    (m) => { m.timeWindows[0].containsTasks = ['a']; },
    (m) => { m.clockCompatibility.uncertainty.relativeRateErrorBound = 0; },
    (m) => { m.graph.evidence[0].evidenceId = 'missing'; },
  ];
  for (const mutate of mutations) {
    const measurement = structuredClone(fixture); mutate(measurement);
    assert.throws(() => validate(measurement), /executionMeasurement/);
  }
});

test('JSON pointer escaping resolves exact own fields and rejects malformed or inherited paths', () => {
  const measurement = structuredClone(fixture);
  measurement['a/b'] = {'~field': 12};
  assert.equal(resolveMeasurementReference({root: measurement, reference: {source: 'measurement', pointer: '/a~1b/~0field'}, at: 'pointer'}), 12);
  for (const pointer of ['/a~2b', '/toString', '/taskIntervals/01', '/absent']) {
    assert.throws(() => resolveMeasurementReference({root: measurement, reference: {source: 'measurement', pointer}, at: 'pointer'}));
  }
});

const missingDuration = 'a: required operation has no complete successful duration';
const invalidSchedule = 'failed or repeated attempts do not establish a failure-free modeled schedule';
const missingSchedule = 'successful completion of the modeled schedule is not established';
const missingContainment = 'no named enclosing interval with complete task-containment evidence';
const nonMonotonic = 'nominal duration arithmetic requires local monotonic elapsed measurements';
const violatedPrecedence = 'retained modeled precedence is violated';
const missingPrecedence = 'modeled precedence has not been verified';
const capacityViolation = 'comparable retained intervals exceed the declared task-slot ceiling';
const precedenceViolation = 'comparable retained intervals violate modeled precedence, including virtual-relay paths';
const zeroDenominator = 'the selected bound is zero; division is undefined';
const unavailableCalculation = ({measurement, index, status, reasons}) => {
  Object.assign(measurement.analysis[index], {value: null, status, reasons});
  delete measurement.analysis[index].criticalPath;
};
const scheduleCoordinates = ({measurement, coordinates, domain = 'coordinator:monotonic'}) => {
  for (const [index, [startSeconds, endSeconds]] of coordinates.entries()) Object.assign(measurement.taskIntervals[index], {startSeconds, endSeconds, durationSeconds: endSeconds - startSeconds, clockDomain: domain});
};
const knownViolation = ({measurement, reasons}) => {
  for (const index of [2, 3]) unavailableCalculation({measurement, index, status: 'inapplicable', reasons});
  const upperViolations = reasons.filter((reason) => [invalidSchedule, nonMonotonic, violatedPrecedence, capacityViolation, precedenceViolation].includes(reason));
  if (upperViolations.length) unavailableCalculation({measurement, index: 4, status: 'inapplicable', reasons: [...upperViolations, ...fixture.analysis[4].reasons]});
};
const publishTimeline = ({measurement, span, overlap}) => {
  Object.assign(measurement.timeWindows[1], {seconds: span, clockDomain: 'coordinator:monotonic', evidence: fixture.graph.evidence});
  Object.assign(measurement.observedPeakOverlap, {value: overlap, evidence: fixture.graph.evidence});
};

test('zero-attempt trials distinguish missing completion from known failed or timed-out schedules', async (t) => {
  const missingDurations = [missingDuration, 'b: required operation has no complete successful duration'];
  for (const [trialStatus, scheduleStatus, scheduleReasons, upperReasons] of [
    ['completed', 'unavailable', [...missingDurations, missingSchedule], [...fixture.analysis[4].reasons, missingSchedule]],
    ['blocked', 'unavailable', [...missingDurations, missingSchedule], [...fixture.analysis[4].reasons, missingSchedule]],
    ['unsupported', 'unavailable', [...missingDurations, missingSchedule], [...fixture.analysis[4].reasons, missingSchedule]],
    ['failed', 'inapplicable', [invalidSchedule, ...missingDurations], [invalidSchedule, ...fixture.analysis[4].reasons]],
    ['timeout', 'inapplicable', [invalidSchedule, ...missingDurations], [invalidSchedule, ...fixture.analysis[4].reasons]],
  ]) {
    await t.test(trialStatus, () => {
      const measurement = structuredClone(fixture);
      Object.assign(measurement.trial, {status: trialStatus, tool: 'changesets', mode: 'distributed', profile: 'full'});
      measurement.taskIntervals = [];
      for (const vertex of measurement.graph.vertices) vertex.durationInputs = [];
      measurement.graph.counts.timedVertices = 0;
      Object.assign(measurement.timeWindows[0], {seconds: null, reason: 'no workload was executed'});
      delete measurement.analysis[3].inputs.T_enclosing;
      for (const index of [0, 1]) unavailableCalculation({measurement, index, status: 'unavailable', reasons: missingDurations});
      unavailableCalculation({measurement, index: 2, status: scheduleStatus, reasons: scheduleReasons});
      unavailableCalculation({measurement, index: 3, status: scheduleStatus, reasons: [...scheduleReasons, missingContainment]});
      unavailableCalculation({measurement, index: 4, status: scheduleStatus, reasons: upperReasons});
      assert.deepEqual(validate(measurement), measurement);
      assert.deepEqual(measurement.taskIntervals, []);
      assert.ok(measurement.analysis.every((calculation) => calculation.value === null));
      const misclassified = structuredClone(measurement);
      misclassified.analysis[2].status = scheduleStatus === 'unavailable' ? 'inapplicable' : 'unavailable';
      assert.throws(() => validate(misclassified), /executionMeasurement/);
      const inventedWork = structuredClone(measurement);
      Object.assign(inventedWork.analysis[0], {value: 0, status: 'computed', reasons: []});
      assert.throws(() => validate(inventedWork), /executionMeasurement/);
      const danglingInput = structuredClone(measurement);
      danglingInput.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/0/durationSeconds'});
      assert.throws(() => validate(danglingInput), /executionMeasurement/);
    });
  }
});

test('numeric durations do not establish successful completion of a noncompleted trial', async (t) => {
  for (const [trialStatus, scheduleStatus, reason] of [
    ['blocked', 'unavailable', missingSchedule],
    ['unsupported', 'unavailable', missingSchedule],
    ['failed', 'inapplicable', invalidSchedule],
    ['timeout', 'inapplicable', invalidSchedule],
  ]) {
    await t.test(trialStatus, () => {
      const measurement = structuredClone(fixture);
      measurement.trial.status = trialStatus;
      for (const index of [2, 3]) unavailableCalculation({measurement, index, status: scheduleStatus, reasons: [reason]});
      const upperReasons = scheduleStatus === 'inapplicable' ? [reason, ...fixture.analysis[4].reasons] : [...fixture.analysis[4].reasons, reason];
      unavailableCalculation({measurement, index: 4, status: scheduleStatus, reasons: upperReasons});
      assert.deepEqual(validate(measurement).analysis.slice(0, 2).map((calculation) => calculation.value), [6, 4]);
      const fabricatedSchedule = structuredClone(measurement);
      fabricatedSchedule.analysis[2] = structuredClone(fixture.analysis[2]);
      fabricatedSchedule.analysis[3] = structuredClone(fixture.analysis[3]);
      assert.throws(() => validate(fabricatedSchedule), /executionMeasurement/);
    });
  }
});

test('attempt outcomes distinguish known failure from open or undocumented statuses', async (t) => {
  for (const intervalStatus of ['failed', 'timeout', 'running', 'unknown', 'cancelled', 'canceled', 'aborted', 'custom-status']) {
    await t.test(intervalStatus, () => {
      const measurement = structuredClone(fixture);
      measurement.taskIntervals[0].status = intervalStatus;
      measurement.graph.counts.timedVertices = 1;
      for (const index of [0, 1]) unavailableCalculation({measurement, index, status: 'unavailable', reasons: [missingDuration]});
      const isKnownFailure = ['failed', 'timeout'].includes(intervalStatus);
      const status = isKnownFailure ? 'inapplicable' : 'unavailable';
      const reasons = isKnownFailure ? [invalidSchedule, missingDuration] : [missingDuration, missingSchedule];
      for (const index of [2, 3]) unavailableCalculation({measurement, index, status, reasons});
      const upperReasons = isKnownFailure ? [invalidSchedule, ...fixture.analysis[4].reasons] : [...fixture.analysis[4].reasons, missingSchedule];
      unavailableCalculation({measurement, index: 4, status, reasons: upperReasons});
      assert.deepEqual(validate(measurement), measurement);
      const misclassified = structuredClone(measurement);
      misclassified.analysis[2].status = isKnownFailure ? 'unavailable' : 'inapplicable';
      assert.throws(() => validate(misclassified), /executionMeasurement/);
      if (isKnownFailure) {
        measurement.trial.status = 'unsupported';
        assert.equal(validate(measurement).analysis[2].status, 'inapplicable');
      }
    });
  }
  const retry = structuredClone(fixture);
  retry.taskIntervals.push({...retry.taskIntervals[0], attemptId: 'attempt-2'});
  knownViolation({measurement: retry, reasons: [invalidSchedule]});
  assert.deepEqual(validate(retry).analysis.slice(0, 2).map((calculation) => calculation.value), [6, 4]);
  retry.trial.status = 'blocked';
  assert.equal(validate(retry).analysis[2].status, 'inapplicable');
  const openPhase = structuredClone(fixture);
  openPhase.taskIntervals.push({...openPhase.taskIntervals[0], operation: 'verification', status: 'running'});
  for (const index of [2, 3]) unavailableCalculation({measurement: openPhase, index, status: 'unavailable', reasons: [missingSchedule]});
  unavailableCalculation({measurement: openPhase, index: 4, status: 'unavailable', reasons: [...fixture.analysis[4].reasons, missingSchedule]});
  assert.deepEqual(validate(openPhase).analysis.slice(0, 2).map((calculation) => calculation.value), [6, 4]);
});

test('known capacity and precedence violations dominate unestablished completion', () => {
  for (const violation of [capacityViolation, violatedPrecedence]) {
    const measurement = structuredClone(fixture);
    measurement.trial.status = 'unsupported';
    if (violation === capacityViolation) {
      scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
      measurement.resources.taskSlotLimit.value = 1;
    } else {
      measurement.premises.precedenceValid = false;
      measurement.premises.precedenceReason = 'retained precedence is contradicted';
    }
    for (const index of [2, 3]) unavailableCalculation({measurement, index, status: 'inapplicable', reasons: [violation, missingSchedule]});
    unavailableCalculation({measurement, index: 4, status: 'inapplicable', reasons: [violation, ...fixture.analysis[4].reasons, missingSchedule]});
    assert.equal(validate(measurement).analysis[2].status, 'inapplicable');
    measurement.analysis[2].status = 'unavailable';
    assert.throws(() => validate(measurement), /executionMeasurement/);
  }
});

test('known violations dominate missing inputs while retaining both exact reasons', () => {
  const measurement = structuredClone(fixture);
  measurement.trial.status = 'failed';
  Object.assign(measurement.taskIntervals[0], {durationSeconds: null, reason: 'not observed'});
  measurement.graph.counts.timedVertices = 1;
  for (const index of [0, 1]) unavailableCalculation({measurement, index, status: 'unavailable', reasons: [missingDuration]});
  knownViolation({measurement, reasons: [invalidSchedule, missingDuration]});
  assert.deepEqual(validate(measurement), measurement);
  for (const reasons of [[invalidSchedule], [missingDuration]]) {
    const broken = structuredClone(measurement);
    broken.analysis[2].reasons = reasons;
    assert.throws(() => validate(broken), /executionMeasurement/);
  }
  const forged = structuredClone(measurement);
  forged.analysis[2].status = 'unavailable';
  assert.throws(() => validate(forged), /executionMeasurement/);
  const orphan = structuredClone(measurement);
  orphan.taskIntervals.push({...fixture.taskIntervals[0], taskId: 'orphan'});
  assert.throws(() => validate(orphan), /executionMeasurement/);
  const badReference = structuredClone(measurement);
  badReference.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/1/durationSeconds'});
  badReference.graph.vertices[0].durationRule = 'sum-disjoint';
  assert.throws(() => validate(badReference), /executionMeasurement/);
});

test('unknown precedence is unavailable, false precedence is inapplicable and requires a reason', () => {
  const unknown = structuredClone(fixture);
  delete unknown.premises.precedenceValid;
  for (const index of [2, 3]) unavailableCalculation({measurement: unknown, index, status: 'unavailable', reasons: [missingPrecedence]});
  assert.equal(validate(unknown).analysis[2].status, 'unavailable');
  const violated = structuredClone(fixture);
  violated.premises.precedenceValid = false;
  violated.premises.precedenceReason = 'a started before its predecessor finished';
  violated.premises.evidence = [];
  knownViolation({measurement: violated, reasons: [violatedPrecedence]});
  assert.equal(validate(violated).analysis[2].status, 'inapplicable');
  delete violated.premises.precedenceReason;
  assert.throws(() => validate(violated), /executionMeasurement/);
});

test('wall durations and wall numerators are inapplicable to nominal monotonic formulas', () => {
  const measurement = structuredClone(fixture);
  measurement.taskIntervals[0].clockKind = 'wall';
  for (const index of [0, 1, 2, 3]) unavailableCalculation({measurement, index, status: 'inapplicable', reasons: [nonMonotonic]});
  unavailableCalculation({measurement, index: 4, status: 'inapplicable', reasons: [nonMonotonic, ...fixture.analysis[4].reasons]});
  assert.equal(validate(measurement).analysis[0].value, null);
  const wallNumerator = structuredClone(fixture);
  wallNumerator.timeWindows[0].clockKind = 'wall';
  unavailableCalculation({measurement: wallNumerator, index: 3, status: 'inapplicable', reasons: [nonMonotonic]});
  assert.equal(validate(wallNumerator).analysis[3].status, 'inapplicable');
});

test('selected modeled phases exclude retained verification, nested work and idle gaps', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
  measurement.taskIntervals.push({...measurement.taskIntervals[0], operation: 'verification', startSeconds: 0, endSeconds: 100, durationSeconds: 100});
  publishTimeline({measurement, span: 4, overlap: 2});
  assert.equal(validate(measurement).timeWindows[1].seconds, 4);
  const inflated = structuredClone(measurement);
  inflated.timeWindows[1].seconds = 100;
  assert.throws(() => validate(inflated), /executionMeasurement/);
  const phases = structuredClone(fixture);
  scheduleCoordinates({measurement: phases, coordinates: [[0, 2], [2, 4]]});
  phases.taskIntervals.push({...phases.taskIntervals[0], operation: 'second-phase', startSeconds: 4, endSeconds: 6});
  phases.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/2/durationSeconds'});
  phases.graph.vertices[0].durationRule = 'sum-disjoint';
  publishTimeline({measurement: phases, span: 6, overlap: 1});
  assert.equal(validate(phases).observedPeakOverlap.value, 1);
  phases.observedPeakOverlap.value = 2;
  assert.throws(() => validate(phases), /executionMeasurement/);
});

test('comparable subsets contradict capacity even with no global timeline displayed', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
  measurement.resources.taskSlotLimit.value = 1;
  assert.throws(() => validate(measurement), /executionMeasurement/);
  knownViolation({measurement, reasons: [capacityViolation]});
  assert.equal(validate(measurement).analysis[2].status, 'inapplicable');
  measurement.taskIntervals[1].clockDomain = 'host-b:monotonic';
  measurement.analysis[2].value = 6;
  measurement.analysis[2].status = 'computed';
  measurement.analysis[2].reasons = [];
  measurement.analysis[3].value = 8 / 6;
  measurement.analysis[3].status = 'computed';
  measurement.analysis[3].reasons = [];
  measurement.analysis[4] = structuredClone(fixture.analysis[4]);
  assert.equal(validate(measurement).analysis[2].value, 6);
});

test('precedence through virtual relays is checked independently of clock origin', () => {
  for (const offset of [0, 1e12]) {
    const measurement = structuredClone(fixture);
    scheduleCoordinates({measurement, coordinates: [[offset, offset + 4], [offset + 3, offset + 5]]});
    measurement.graph.vertices.push({id: 'relay', operation: 'relay', isVirtual: true, durationInputs: []});
    measurement.graph.edges = [{from: 'a', to: 'relay', kind: 'build'}, {from: 'relay', to: 'b', kind: 'build'}];
    Object.assign(measurement.graph.counts, {vertices: 3, edges: 2, virtualVertices: 1});
    measurement.analysis[0].inputs.relay = {source: 'measurement', pointer: '/graph/vertices/2/durationInputs'};
    measurement.analysis[1].value = 6;
    measurement.analysis[1].criticalPath = ['a', 'relay', 'b'];
    knownViolation({measurement, reasons: [precedenceViolation]});
    assert.equal(validate(measurement).analysis[2].status, 'inapplicable');
    measurement.analysis[2].value = 6;
    measurement.analysis[2].status = 'computed';
    measurement.analysis[2].reasons = [];
    assert.throws(() => validate(measurement), /executionMeasurement/);
  }
});

test('empty or zero-duration modeled work has no positive overlap and a zero denominator is inapplicable', () => {
  const zero = structuredClone(fixture);
  scheduleCoordinates({measurement: zero, coordinates: [[7, 7], [7, 7]]});
  for (const index of [0, 1, 2]) zero.analysis[index].value = 0;
  knownViolation({measurement: zero, reasons: [zeroDenominator]});
  Object.assign(zero.analysis[2], {value: 0, status: 'computed', reasons: []});
  publishTimeline({measurement: zero, span: 0, overlap: 0});
  assert.equal(validate(zero).analysis[3].status, 'inapplicable');
  const empty = structuredClone(zero);
  empty.graph.vertices = [];
  empty.graph.counts = {vertices: 0, edges: 0, virtualVertices: 0, timedVertices: 0};
  empty.taskIntervals = [];
  empty.analysis[0].inputs = {};
  delete empty.analysis[1].criticalPath;
  empty.timeWindows[1].seconds = null;
  assert.equal(validate(empty).observedPeakOverlap.value, 0);
  empty.timeWindows[1].seconds = 0;
  assert.throws(() => validate(empty), /executionMeasurement/);
  const incomplete = structuredClone(fixture);
  Object.assign(incomplete.taskIntervals[0], {durationSeconds: null, reason: 'missing'});
  incomplete.graph.counts.timedVertices = 1;
  for (const index of [0, 1]) unavailableCalculation({measurement: incomplete, index, status: 'unavailable', reasons: [missingDuration]});
  for (const index of [2, 3]) unavailableCalculation({measurement: incomplete, index, status: 'unavailable', reasons: [missingDuration, missingSchedule]});
  unavailableCalculation({measurement: incomplete, index: 4, status: 'unavailable', reasons: [...fixture.analysis[4].reasons, missingSchedule]});
  Object.assign(incomplete.observedPeakOverlap, {value: 0, evidence: fixture.graph.evidence});
  assert.throws(() => validate(incomplete), /executionMeasurement/);
});

test('task span domain, attempt node identity and formula reasons are independently validated', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
  publishTimeline({measurement, span: 4, overlap: 2});
  measurement.timeWindows[1].clockDomain = 'host-b:monotonic';
  assert.throws(() => validate(measurement), /executionMeasurement/);
  const mismatch = structuredClone(fixture);
  mismatch.taskIntervals.push({...mismatch.taskIntervals[0], operation: 'verification', nodeId: 'host-b'});
  assert.throws(() => validate(mismatch), /executionMeasurement/);
  const differentClock = structuredClone(fixture);
  differentClock.taskIntervals.push({...differentClock.taskIntervals[0], operation: 'verification', clockDomain: 'host-b:monotonic'});
  assert.deepEqual(validate(differentClock), differentClock);
  const explanation = structuredClone(fixture);
  explanation.analysis[0].reasons = ['invented qualification'];
  assert.throws(() => validate(explanation), /executionMeasurement/);
});

test('all attaining critical paths are valid and malformed scalar/type records are rejected', () => {
  const tied = structuredClone(fixture);
  tied.taskIntervals[1].durationSeconds = 4;
  tied.analysis[0].value = 8;
  tied.analysis[1].criticalPath = ['b'];
  assert.equal(validate(tied).analysis[1].value, 4);
  for (const mutate of [
    (m) => { m.analysis[0].value = -1e-12; },
    (m) => { m.taskIntervals[0].startSeconds = 3; m.taskIntervals[0].endSeconds = 2; },
    (m) => { m.taskIntervals[0].startSeconds = '0'; m.taskIntervals[0].endSeconds = '4'; },
    (m) => { m.taskIntervals[0].startSeconds = null; m.taskIntervals[0].endSeconds = 4; },
    (m) => { m.taskIntervals[0].startSeconds = Infinity; m.taskIntervals[0].endSeconds = Infinity; },
    (m) => { m.clockCompatibility.assumptions = 'assumed'; },
    (m) => { m.clockCompatibility.assumptions = [42]; },
    (m) => { m.clockCompatibility.uncertainty.reason = ''; },
    (m) => { m.graph.edges = [{from: 'a', to: 'b', kind: 'build'}, {kind: 'build', to: 'b', from: 'a', extra: true}]; },
  ]) {
    const measurement = structuredClone(fixture); mutate(measurement);
    assert.throws(() => validate(measurement), /executionMeasurement/);
  }
});

test('overlapping phases make aggregation inapplicable without inventing extra task occupancy', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4]]});
  measurement.taskIntervals.push({...measurement.taskIntervals[0], operation: 'nested-phase', startSeconds: 2, endSeconds: 4, durationSeconds: 2});
  measurement.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/2/durationSeconds'});
  measurement.graph.vertices[0].durationRule = 'sum-disjoint';
  measurement.graph.counts.timedVertices = 1;
  measurement.resources.taskSlotLimit.value = 1;
  const violation = 'a: sum-disjoint duration inputs contain overlapping selected phases';
  for (const index of [0, 1]) unavailableCalculation({measurement, index, status: 'inapplicable', reasons: [violation, 'a: duration aggregation lacks comparable, disjoint phase intervals']});
  for (const index of [2, 3]) unavailableCalculation({measurement, index, status: 'inapplicable', reasons: [violation, 'a: duration aggregation lacks comparable, disjoint phase intervals', missingSchedule]});
  unavailableCalculation({measurement, index: 4, status: 'inapplicable', reasons: [violation, ...fixture.analysis[4].reasons, missingSchedule]});
  assert.equal(validate(measurement).analysis[2].status, 'inapplicable');
  measurement.taskIntervals.push({...measurement.taskIntervals[0], operation: 'missing-phase', durationSeconds: null, reason: 'not observed'});
  delete measurement.taskIntervals[3].startSeconds;
  delete measurement.taskIntervals[3].endSeconds;
  measurement.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/3/durationSeconds'});
  for (const index of [0, 1]) measurement.analysis[index].reasons = [violation, missingDuration];
  for (const index of [2, 3]) measurement.analysis[index].reasons = [violation, missingDuration, missingSchedule];
  assert.equal(validate(measurement).analysis[0].status, 'inapplicable');
});

test('comparable intervals disprove an enclosing duration without rejecting unmapped nominal ratios', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
  measurement.timeWindows[0].seconds = 3;
  unavailableCalculation({measurement, index: 3, status: 'inapplicable', reasons: ['comparable modeled task intervals cannot fit inside the declared enclosing window']});
  assert.deepEqual(validate(measurement).analysis.slice(0, 3).map((entry) => entry.value), [6, 4, 4]);
  const forged = structuredClone(measurement);
  Object.assign(forged.analysis[3], {value: 0.75, status: 'computed', reasons: []});
  assert.throws(() => validate(forged), /executionMeasurement/);
  const unmapped = structuredClone(fixture);
  unmapped.timeWindows[0].seconds = 3;
  unmapped.analysis[3].value = 0.75;
  assert.equal(validate(unmapped).analysis[3].value, 0.75);
  const noCoordinates = structuredClone(measurement);
  for (const interval of noCoordinates.taskIntervals) { delete interval.startSeconds; delete interval.endSeconds; }
  assert.equal(validate(noCoordinates).analysis[3].status, 'inapplicable');
});

test('zero-duration selected phase inside a positive phase is not an overlap violation', () => {
  const measurement = structuredClone(fixture);
  scheduleCoordinates({measurement, coordinates: [[0, 4], [0, 2]]});
  measurement.taskIntervals.push({...measurement.taskIntervals[0], operation: 'zero-phase', startSeconds: 1, endSeconds: 1, durationSeconds: 0});
  measurement.graph.vertices[0].durationInputs.push({source: 'measurement', pointer: '/taskIntervals/2/durationSeconds'});
  measurement.graph.vertices[0].durationRule = 'sum-disjoint';
  publishTimeline({measurement, span: 4, overlap: 2});
  assert.equal(validate(measurement).analysis[0].value, 6);
});
