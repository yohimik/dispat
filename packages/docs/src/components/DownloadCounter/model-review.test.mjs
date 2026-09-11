import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import test from 'node:test';
import {formatCounter, REFRESH_MS, startPolling, validateSnapshot} from './model.ts';

const empty = {
  schemaVersion: 1, total: 0, github: 0, dockerHub: 0, collectedAt: '2026-09-11T00:00:00Z',
  repositories: ['alpine', 'debian', 'ubuntu', 'dind'].map(name => ({repository: `yohimik/dispat-${name}`, pulls: 0})),
};

test('groups eight-place and larger counters with stationary thousands separators', () => {
  assert.equal(formatCounter(undefined), '00.000.000');
  assert.equal(formatCounter(20769), '00.020.769');
  assert.equal(formatCounter(1234567), '01.234.567');
  assert.equal(formatCounter(123456789), '123.456.789');
  assert.equal(formatCounter(Number.MAX_SAFE_INTEGER), '9.007.199.254.740.991');
});

test('accepts genuine zero and rejects malformed or incomplete provider totals', () => {
  assert.equal(validateSnapshot(empty).total, 0);
  for (const value of [null, undefined, 0, '0', [], {},
    {...empty, schemaVersion: 2}, {...empty, github: '0'}, {...empty, dockerHub: Number.NaN},
    {...empty, total: Number.MAX_SAFE_INTEGER + 1}, {...empty, collectedAt: null},
    {...empty, repositories: null}, {...empty, repositories: [null, ...empty.repositories.slice(1)]},
    {...empty, repositories: [{...empty.repositories[0], pulls: -1}, ...empty.repositories.slice(1)]},
    {...empty, repositories: empty.repositories.map(item => ({...item, pulls: Number.MAX_SAFE_INTEGER}))},
    {...empty, repositories: [{...empty.repositories[0], pulls: 1}, ...empty.repositories.slice(1)]},
  ]) assert.throws(() => validateSnapshot(value), `accepted ${JSON.stringify(value)}`);
});

test('collection timestamps preserve real UTC calendar dates from Go and JavaScript', () => {
  for (const collectedAt of ['2026-09-11T00:00:00Z', '2026-09-11T00:00:00.000Z', '2026-09-11T00:00:00.123Z']) {
    assert.equal(validateSnapshot({...empty, collectedAt}).collectedAt, collectedAt);
  }
  for (const collectedAt of ['2026-02-30T00:00:00Z', '2026-09-11Z', 'September 11 2026 Z']) {
    assert.throws(() => validateSnapshot({...empty, collectedAt}), collectedAt);
  }
});

test('a hidden initial page starts on visibility and a stopped poller ignores late timers and errors', async () => {
  let visible = false;
  let listener;
  let reject;
  let requests = 0;
  let failures = 0;
  let scheduled;
  const stop = startPolling({
    visible: () => visible,
    listen: fn => { listener = fn; return () => {}; },
    load: () => { requests++; return new Promise((_, fail) => { reject = fail; }); },
    value: () => assert.fail('unexpected value'),
    failure: () => { failures++; },
    timer: (fn, delay) => { assert.equal(delay, REFRESH_MS); scheduled = fn; return 1; },
    clearTimer: () => {},
  });
  assert.equal(requests, 0);
  visible = true;
  listener();
  reject(new Error('offline'));
  await setImmediate();
  assert.equal(failures, 1);
  assert.equal(REFRESH_MS, 15 * 60 * 1000);
  stop();
  scheduled();
  listener();
  assert.equal(requests, 1);
});

test('an explicit polling interval is honored and late failures cannot update an unmounted counter', async () => {
  let resolve;
  let reject;
  let scheduled;
  let failure = 0;
  const stop = startPolling({
    visible: () => true, listen: () => () => {},
    load: () => new Promise((ok, fail) => { resolve = ok; reject = fail; }),
    value: () => {}, failure: () => { failure++; },
    timer: (fn, delay) => { assert.equal(delay, 5); scheduled = fn; return 1; }, clearTimer: () => {}, interval: 5,
  });
  resolve(empty);
  await setImmediate();
  scheduled();
  stop();
  reject(new Error('late network failure'));
  await setImmediate();
  assert.equal(failure, 0);
});
