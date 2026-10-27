import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

const config = await fs.readFile(new URL('../dispat.yaml', import.meta.url), 'utf8');
const start = config.indexOf('  experiments: |\n');
assert.notEqual(start, -1, 'the docs release experiments hook exists');
const end = config.indexOf('\n  # The release announcement', start);
assert.notEqual(end, -1, 'the docs release experiments hook has a following section');
const script = config.slice(start + '  experiments: |\n'.length, end).replace(/^    /gm, '');

const git = `#!/bin/sh
set -eu
[ "$1" = rev-parse ] && [ "$2" = --show-toplevel ] || exit 2
printf '%s\n' "$MOCK_ROOT"
`;
const dispat = `#!/bin/sh
set -eu
[ "$1" = exec ] || exit 2
case "$2" in
  experiments) printf '%s\n' "$EXPERIMENTS_DISPAT_VERSION" > "$MOCK_ROOT/delegated-image-version" ;;
  summary) ;;
  *) exit 2 ;;
esac
`;

test('release experiments select the published image version independently of the CLI version', async (t) => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'docs-release-experiments-'));
  t.after(() => fs.rm(dir, {recursive: true, force: true}));
  await fs.mkdir(path.join(dir, 'bin'));
  await fs.mkdir(path.join(dir, 'coverage/experiments/cell'), {recursive: true});
  await fs.writeFile(path.join(dir, 'coverage/experiments/cell/verdict.json'), '{}\n');
  await fs.writeFile(path.join(dir, 'bin/git'), git, {mode: 0o755});
  await fs.writeFile(path.join(dir, 'bin/dispat'), dispat, {mode: 0o755});
  const output = path.join(dir, 'outputs');
  const env = {
    PATH: `${dir}/bin:${process.env.PATH}`,
    MOCK_ROOT: dir,
    DISPAT_OUTPUT: output,
    DISPAT_WORKSPACE_DISPAT_VERSION: '1.11.0-rc.9',
    DISPAT_WORKSPACE_DISPAT_ALPINE_VERSION: '1.11.0-rc.7',
  };
  const result = spawnSync('sh', ['-c', script], {cwd: dir, env, encoding: 'utf8', timeout: 5000});
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /yohimik\/dispat-alpine:1\.11\.0-rc\.7/);
  assert.equal(await fs.readFile(path.join(dir, 'delegated-image-version'), 'utf8'), '1.11.0-rc.7\n');
  assert.match(await fs.readFile(output, 'utf8'), /^REPORT_EXPERIMENTS_VERSION=1\.11\.0-rc\.7$/m);

  await fs.rm(path.join(dir, 'delegated-image-version'));
  const missing = spawnSync('sh', ['-c', script], {
    cwd: dir,
    env: {...env, DISPAT_WORKSPACE_DISPAT_ALPINE_VERSION: ''},
    encoding: 'utf8',
    timeout: 5000,
  });
  assert.ifError(missing.error);
  assert.notEqual(missing.status, 0);
  assert.match(missing.stderr, /DISPAT_WORKSPACE_DISPAT_ALPINE_VERSION is required/);
  await assert.rejects(fs.access(path.join(dir, 'delegated-image-version')));
});
