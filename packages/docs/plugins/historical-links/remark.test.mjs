import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {rewriteHistoricalUrl} from './remark.ts';

const refs = {'1.3': '3333333333333333333333333333333333333333', '1.8': '8888888888888888888888888888888888888888', '1.9': '9999999999999999999999999999999999999999'};

function site() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'historical-links-'));
  fs.mkdirSync(path.join(root, 'versioned_docs/version-1.8/internals'), {recursive: true});
  fs.writeFileSync(path.join(root, 'versioned_docs/version-1.8/comparison.md'), '# comparison');
  fs.mkdirSync(path.join(root, 'versioned_docs/version-1.7'), {recursive: true});
  fs.writeFileSync(path.join(root, 'versioned_docs/version-1.7/comparison.md'), '# comparison');
  return root;
}

test('pins blob and tree links while preserving suffixes', () => {
  const root = site();
  assert.equal(
    rewriteHistoricalUrl('https://github.com/yohimik/dispat/blob/main/pkg/scanner/README.md#api', '1.8', refs, root),
    `https://github.com/yohimik/dispat/blob/${refs['1.8']}/pkg/scanner/README.md#api`,
  );
  assert.equal(
    rewriteHistoricalUrl('https://github.com/yohimik/dispat/tree/main/tools?tab=readme', '1.8', refs, root),
    `https://github.com/yohimik/dispat/tree/${refs['1.8']}/tools?tab=readme`,
  );
});

test('maps moved and retroactively introduced sources', () => {
  const root = site();
  assert.match(
    rewriteHistoricalUrl('https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md', '1.3', refs, root),
    new RegExp(`/blob/${refs['1.3']}/pkg/ccme/SPEC\\.md$`),
  );
  assert.match(
    rewriteHistoricalUrl('https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/SPEC.md', '1.9', refs, root),
    new RegExp(`/blob/${refs['1.9']}/specs/ccme-spec/SPEC\\.md$`),
  );
  assert.match(
    rewriteHistoricalUrl('https://github.com/yohimik/dispat/blob/main/Dockerfile.tinygo', '1.3', refs, root),
    /\/blob\/d78a3e59005a0b49a178ad2ff9b688c20453743a\/Dockerfile\.tinygo$/,
  );
});

test('versions internal routes and leaves shared assets alone', () => {
  const root = site();
  assert.equal(rewriteHistoricalUrl('/comparison#the-experiments', '1.8', refs, root, {}, {}, '1.8'), '/comparison#the-experiments');
  assert.equal(rewriteHistoricalUrl('/comparison#the-experiments', '1.7', refs, root, {}, {}, '1.8'), '/1.7/comparison#the-experiments');
  assert.equal(rewriteHistoricalUrl('/demo-blast.gif', '1.8', refs, root), '/demo-blast.gif');
  assert.equal(rewriteHistoricalUrl('/1.7/comparison/', '1.8', refs, root), '/1.7/comparison/');
  assert.equal(rewriteHistoricalUrl('/', '1.8', refs, root), '/');
  assert.equal(rewriteHistoricalUrl('//cdn.example.test/file', '1.8', refs, root), '//cdn.example.test/file');
});

test('pins pkg.go.dev to the module version resolved at the historical ref', () => {
  const root = site();
  const resolve = (module, major, ref) => {
    assert.equal(ref, refs['1.8']);
    return module === 'ccme' && major === '/v2' ? 'v2.0.0' : 'v1.2.0';
  };
  assert.equal(
    rewriteHistoricalUrl('https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner#Scan', '1.8', refs, root, {}, {}, '1.8', resolve),
    'https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner@v1.2.0#Scan',
  );
  assert.equal(
    rewriteHistoricalUrl('https://pkg.go.dev/github.com/yohimik/dispat/pkg/ccme/v2?tab=doc', '1.8', refs, root, {}, {}, '1.8', resolve),
    'https://pkg.go.dev/github.com/yohimik/dispat/pkg/ccme/v2@v2.0.0?tab=doc',
  );
});

test('prefers the release plan module version before its tag exists', () => {
  const root = site();
  let fallbacks = 0;
  const resolve = () => { fallbacks += 1; return 'v1.2.0'; };
  assert.equal(
    rewriteHistoricalUrl('https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner#Scan', '1.9', refs, root, {}, {}, '1.9', resolve, {scanner: 'v1.3.0'}),
    'https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner@v1.3.0#Scan',
  );
  assert.equal(fallbacks, 0);
});

test('rejects a recorded version whose major disagrees with the module path', () => {
  const root = site();
  assert.throws(
    () => rewriteHistoricalUrl('https://pkg.go.dev/github.com/yohimik/dispat/pkg/ccme/v2', '1.8', refs, root, {}, {}, '1.8', undefined, {ccme: 'v3.0.0'}),
    /does not match module path major v2/,
  );
});

test('does not fill an incomplete recorded plan from older tags', () => {
  assert.throws(
    () => rewriteHistoricalUrl('https://pkg.go.dev/github.com/yohimik/dispat/pkg/scanner', '1.8', refs, site(), {}, {}, '1.8', () => 'v1.2.0', {}),
    /missing scanner/,
  );
});

test('rejects links that would escape to a current-only route', () => {
  const root = site();
  assert.throws(
    () => rewriteHistoricalUrl('/missing/', '1.8', refs, root),
    /links to current-only route/,
  );
});
