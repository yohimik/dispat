import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

const workflow = await fs.readFile(new URL('../../../.github/workflows/download-counter.yml', import.meta.url), 'utf8');
const publish = workflow.slice(workflow.indexOf('      - name: Publish the snapshot'));
const script = publish.split('        run: |\n')[1].replace(/^          /gm, '');

// Execute the actual workflow shell against a fake origin and CDN. The edge
// begins with a cached 404 and only invalidation makes the upload visible.
const gcloud = `#!/bin/sh
set -eu
case "$*" in
  'storage cp '*)
    echo upload >> calls
    [ "$FAIL_STAGE" != upload ] || exit 1
    cp downloads.json origin.json
    ;;
  'compute url-maps invalidate-cdn-cache test-map --path=/downloads.json --quiet')
    echo invalidate >> calls
    [ "$FAIL_STAGE" != invalidate ] || exit 1
    cp origin.json edge.json
    ;;
  *) exit 2 ;;
esac
`;
const curl = `#!/bin/sh
set -eu
echo fetch >> calls
[ "$FAIL_STAGE" != fetch ] || exit 22
[ -f edge.json ] || exit 22
if [ "$FAIL_STAGE" = stale ]; then
  echo '{}' > served-downloads.json
else
  cp edge.json served-downloads.json
fi
`;

for (const [failure, expectedCalls] of [
  ['', ['upload', 'invalidate', 'fetch']],
  ['upload', ['upload']],
  ['invalidate', ['upload', 'invalidate']],
  ['fetch', ['upload', 'invalidate', 'fetch']],
  ['stale', ['upload', 'invalidate', 'fetch']],
]) {
  test(failure ? `publication rejects ${failure} failure` : 'publication evicts a cached 404 before verifying the public snapshot', async (t) => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'counter-publish-'));
    t.after(() => fs.rm(dir, {recursive: true, force: true}));
    await fs.mkdir(path.join(dir, 'bin'));
    await fs.writeFile(path.join(dir, 'bin/gcloud'), gcloud, {mode: 0o755});
    await fs.writeFile(path.join(dir, 'bin/curl'), curl, {mode: 0o755});
    await fs.writeFile(path.join(dir, 'downloads.json'), '{"total":20784}\n');
    const result = spawnSync('sh', ['-c', script], {
      cwd: dir,
      encoding: 'utf8',
      timeout: 5000,
      env: {PATH: `${dir}/bin:${process.env.PATH}`, DOCS_BUCKET: 'test-bucket', DOCS_URL_MAP: 'test-map', FAIL_STAGE: failure},
    });
    assert.ifError(result.error);
    if (failure) assert.notEqual(result.status, 0, result.stderr);
    else assert.equal(result.status, 0, result.stderr);
    assert.deepEqual((await fs.readFile(path.join(dir, 'calls'), 'utf8')).trim().split('\n'), expectedCalls);
  });
}

test('public verification checks the canonical URL with bounded HTTP retries', () => {
  assert.match(script, /--fail --silent --show-error --retry 3 --retry-all-errors/);
  assert.match(script, /--connect-timeout 10 --max-time 20/);
  assert.match(script, /https:\/\/dispat\.dev\/downloads\.json --output served-downloads\.json/);
  assert.match(publish, /DOCS_URL_MAP: \$\{\{ vars\.DOCS_URL_MAP \}\}/);
});
