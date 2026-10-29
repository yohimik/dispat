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
printf '%s %s\\n' "$2" "$EXPERIMENTS_DISPAT_VERSION" >> "$MOCK_ROOT/delegated-commands"
case "$2" in
  experiments) exit "\${MOCK_STANDARD_EXIT:-0}" ;;
  llvm-experiments) exit "\${MOCK_LLVM_EXIT:-0}" ;;
  summary) exit "\${MOCK_SUMMARY_EXIT:-0}" ;;
  *) exit 2 ;;
esac
`;

async function prepareFixture(t) {
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
  return {dir, output, env};
}

const expectedDelegations = [
  'experiments 1.11.0-rc.7',
  'llvm-experiments 1.11.0-rc.7',
  'summary 1.11.0-rc.7',
].join('\n') + '\n';

test('release and LLVM experiments select the same published image independently of the CLI version', async (t) => {
  const {dir, output, env} = await prepareFixture(t);
  const result = spawnSync('sh', ['-c', script], {cwd: dir, env, encoding: 'utf8', timeout: 5000});
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /yohimik\/dispat-alpine:1\.11\.0-rc\.7/);
  assert.equal(await fs.readFile(path.join(dir, 'delegated-commands'), 'utf8'), expectedDelegations);
  assert.match(await fs.readFile(output, 'utf8'), /^REPORT_EXPERIMENTS_VERSION=1\.11\.0-rc\.7$/m);

  await fs.rm(path.join(dir, 'delegated-commands'));
  const missing = spawnSync('sh', ['-c', script], {
    cwd: dir,
    env: {...env, DISPAT_WORKSPACE_DISPAT_ALPINE_VERSION: ''},
    encoding: 'utf8',
    timeout: 5000,
  });
  assert.ifError(missing.error);
  assert.notEqual(missing.status, 0);
  assert.match(missing.stderr, /DISPAT_WORKSPACE_DISPAT_ALPINE_VERSION is required/);
  await assert.rejects(fs.access(path.join(dir, 'delegated-commands')));
});

test('release experiment failures preserve the first campaign error and still summarize both campaigns', async (t) => {
  const scenarios = [
    {name: 'standard fails', standard: '17', llvm: '0', summary: '0', expected: 17},
    {name: 'LLVM fails', standard: '0', llvm: '23', summary: '0', expected: 23},
    {name: 'both campaigns fail', standard: '17', llvm: '23', summary: '0', expected: 17},
    {name: 'summary fails after successful campaigns', standard: '0', llvm: '0', summary: '29', expected: 0},
  ];
  for (const scenario of scenarios) {
    await t.test(scenario.name, async (t) => {
      const {dir, output, env} = await prepareFixture(t);
      const result = spawnSync('sh', ['-c', script], {
        cwd: dir,
        env: {...env, MOCK_STANDARD_EXIT: scenario.standard, MOCK_LLVM_EXIT: scenario.llvm, MOCK_SUMMARY_EXIT: scenario.summary},
        encoding: 'utf8',
        timeout: 5000,
      });
      assert.ifError(result.error);
      assert.equal(result.status, scenario.expected, result.stderr);
      assert.equal(await fs.readFile(path.join(dir, 'delegated-commands'), 'utf8'), expectedDelegations);
      const outputs = await fs.readFile(output, 'utf8');
      assert.match(outputs, /^REPORT_EXPERIMENTS=cell $/m);
      assert.match(outputs, /^REPORT_EXPERIMENTS_VERSION=1\.11\.0-rc\.7$/m);
    });
  }
});
