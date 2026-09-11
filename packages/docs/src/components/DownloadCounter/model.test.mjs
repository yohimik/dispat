import assert from 'node:assert/strict';
import test from 'node:test';
import {displayDigits, startPolling, validateSnapshot} from './model.ts';

const flush = () => new Promise((resolve) => setImmediate(resolve));

const good = {
  schemaVersion: 1, total: 50, github: 10, dockerHub: 40, collectedAt: '2026-09-11T00:02:03Z',
  repositories: ['alpine', 'debian', 'ubuntu', 'dind'].map((name) => ({repository: `yohimik/dispat-${name}`, pulls: 10})),
};

test('validates the Go snapshot contract and exact sums', () => {
  assert.deepEqual(validateSnapshot(good), good);
  for (const broken of [
    {...good, total: 49}, {...good, total: -1}, {...good, total: 1.5}, {...good, collectedAt: 'yesterday'},
    {...good, collectedAt: '2026-09-11T00:02:03+00:00'}, {...good, repositories: good.repositories.slice(1)},
    {...good, repositories: good.repositories.map((item) => ({...item, repository: 'yohimik/dispat-alpine'}))},
  ]) assert.throws(() => validateSnapshot(broken));
});

test('renders at least eight places and expands without truncating', () => {
  assert.equal(displayDigits(undefined).join(''), '00000000');
  assert.equal(displayDigits(42).join(''), '00000042');
  assert.equal(displayDigits(123456789).join(''), '123456789');
});

test('polling preserves last good data, pauses hidden work, prevents overlap, and cleans up', async () => {
  let visible = true; let listener; let pending; let timers = []; let values = []; let failures = 0; let aborted = 0;
  const load = (signal) => new Promise((resolve, reject) => {
    pending = {resolve, reject}; signal.addEventListener('abort', () => { aborted++; reject(new DOMException('aborted', 'AbortError')); });
  });
  const stop = startPolling({load, value: (value) => values.push(value), failure: () => failures++, visible: () => visible,
    listen: (next) => { listener = next; return () => { listener = undefined; }; }, timer: (fn) => { timers.push(fn); return fn; }, clearTimer: (id) => { timers = timers.filter((fn) => fn !== id); }});
  const first = pending; first.resolve(good); await flush();
  assert.equal(values.length, 1); assert.equal(timers.length, 1);
  timers.shift()(); const second = pending; timers.forEach((fn) => fn()); assert.equal(pending, second);
  second.reject(new Error('offline')); await flush(); assert.equal(failures, 1); assert.equal(values.length, 1);
  timers.shift()(); visible = false; listener(); await flush(); assert.equal(aborted, 1); assert.equal(timers.length, 0);
  visible = true; listener(); assert.ok(pending); stop(); assert.equal(aborted, 2); assert.equal(listener, undefined); assert.equal(timers.length, 0);
});

test('late completion from an aborted request cannot clear or replace the newer request', async () => {
  let visible = true; let listener; const requests = []; let values = [];
  const stop = startPolling({load: () => new Promise((resolve) => requests.push(resolve)), value: (v) => values.push(v), failure: () => {}, visible: () => visible,
    listen: (fn) => { listener = fn; return () => {}; }, timer: () => 1, clearTimer: () => {}});
  visible = false; listener(); visible = true; listener();
  requests[0](good); await flush(); assert.equal(values.length, 0);
  requests[1]({...good, total: 51, github: 11}); await flush(); assert.equal(values[0].total, 51); stop();
});
