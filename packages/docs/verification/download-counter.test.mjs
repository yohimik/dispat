import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import test from 'node:test';

const root = new URL('../../../', import.meta.url);
const read = (path) => fs.readFile(new URL(path, root), 'utf8');

test('scheduled refresh reuses WIF, is bounded, and uploads only the static snapshot', async () => {
  const workflow = await read('.github/workflows/download-counter.yml');
  const schedule = workflow.match(/cron: '([\d,]+) \* \* \* \*'/);
  assert.ok(schedule, 'refresh runs every hour of every day');
  const minutes = schedule[1].split(',').map(Number);
  assert.equal(minutes.length, 4, 'four refreshes per hour');
  for (let i = 0; i < minutes.length; i++) {
    assert.ok(minutes[i] >= 0 && minutes[i] < 60);
    const next = i + 1 < minutes.length ? minutes[i + 1] : minutes[0] + 60;
    assert.equal(next - minutes[i], 15, '15-minute gaps, including across hours');
  }
  assert.match(workflow, /push:\n    branches: \[main\]\n    paths:\n      - '\.github\/workflows\/download-counter\.yml'\n      - 'packages\/docs\/download-counter\/\*\*'/);
  assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /timeout-minutes: 5/);
  assert.match(workflow, /permissions:\n  contents: read\n  id-token: write/);
  assert.match(workflow, /secrets\.GCP_WIF_PROVIDER/);
  assert.match(workflow, /secrets\.GCP_RELEASER_SA/);
  assert.match(workflow, /vars\.DOCS_BUCKET/);
  assert.match(workflow, /downloads\.json "gs:\/\/\$\{DOCS_BUCKET\}\/downloads\.json"/);
});

test('site deployment cannot delete or package the independently refreshed snapshot', async () => {
  const [config, deploy] = await Promise.all([read('packages/docs/docusaurus.config.ts'), read('packages/docs/dispat.yaml')]);
  assert.doesNotMatch(config, /downloads\.json/);
  assert.match(deploy, /--exclude="\^downloads\\\\\.json\$"/);
  assert.match(deploy, /--delete-unmatched-destination-objects/);
});

test('the textual live status is visually hidden while remaining available to assistive tools', async () => {
  const [component, styles] = await Promise.all([
    read('packages/docs/src/components/DownloadCounter/index.tsx'),
    read('packages/docs/src/components/DownloadCounter/styles.module.css'),
  ]);
  assert.match(component, /className=\{styles\.screenReaderOnly\} role="status" aria-live="polite" aria-atomic="true"/);
  assert.match(styles, /\.screenReaderOnly \{[^}]*position: absolute;[^}]*clip: rect\(0, 0, 0, 0\);/);
});

test('landing calls to action can wrap inside the mobile content padding', async () => {
  const styles = await read('packages/docs/src/pages/index.module.css');
  assert.match(styles, /\.buttons > :global\(\.button\) \{[^}]*min-width: 0;[^}]*max-width: 100%;[^}]*white-space: normal;/);
});
