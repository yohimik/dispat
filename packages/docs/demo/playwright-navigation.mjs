import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('Set PLAYWRIGHT_RUNTIME to the directory containing node_modules/playwright');
const {chromium} = createRequire(path.join(runtime, 'package.json'))('playwright');
const base = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'output/playwright/navigation');
await fs.mkdir(output, {recursive: true});
const browser = await chromium.launch();
const context = await browser.newContext({viewport: {width: 1440, height: 1000}, reducedMotion: 'reduce', recordVideo: {dir: output}});
const page = await context.newPage();
const errors = [];
page.on('pageerror', (error) => errors.push(error.message));
try {
  await page.goto(base, {waitUntil: 'networkidle'});
  const deck = page.locator('[data-demo-id="landing-demos"]');
  await deck.scrollIntoViewIfNeeded();
  await deck.locator('[data-demo-scene-root]').waitFor();
  const original = await deck.locator('[data-slide-id]').getAttribute('data-slide-id');
  const originalScene = await deck.locator('[data-demo-scene-root]').elementHandle();
  const pending = [];
  let holdRequests = true;
  await page.route('**/*.js', async (route) => {
    if (!holdRequests) return route.continue();
    await new Promise((resolve) => pending.push(async () => { await route.continue(); resolve(); }));
  });
  const waitForRequest = async (count) => {
    for (let attempt = 0; attempt < 100 && pending.length < count; attempt++) await page.waitForTimeout(50);
    assert.ok(pending.length >= count, 'selected scene did not request its lazy chunk');
  };
  await deck.locator('[data-demo-feature="math"]').click();
  await waitForRequest(1);
  await page.waitForTimeout(300);
  assert.equal(await deck.locator('[data-slide-id]').getAttribute('data-slide-id'), original);
  assert.equal(await originalScene.evaluate((node) => node.isConnected), true, 'old player was unmounted while loading');
  assert.equal(await deck.getByText('Loading interactive demo…', {exact: true}).count(), 0);
  const firstRequests = pending.splice(0);
  await deck.locator('[data-demo-feature="for"]').click();
  await waitForRequest(1);
  holdRequests = false;
  await Promise.all(pending.splice(0).map((release) => release()));
  await deck.locator('[data-slide-id="for"]').waitFor();
  await Promise.all(firstRequests.map((release) => release()));
  await page.unroute('**/*.js');
  await page.waitForTimeout(500);
  assert.equal(await deck.locator('[data-slide-id]').getAttribute('data-slide-id'), 'for', 'late request replaced the latest selection');
  assert.equal(await deck.locator('[data-demo-scene-root]').count(), 1);
  await page.route('**/*.js', (route) => route.abort('failed'));
  await deck.locator('[data-demo-feature="release-control"]').click();
  await deck.getByRole('status').waitFor();
  assert.equal(await deck.locator('[data-slide-id]').getAttribute('data-slide-id'), 'for', 'failed selection discarded current scene');
  await page.unroute('**/*.js');
  await deck.getByRole('button', {name: 'Try again'}).click();
  await deck.locator('[data-slide-id="release-control"]').waitFor();
  assert.equal(await deck.getByRole('status').count(), 0);
  assert.equal(await deck.getByRole('button', {name: 'Play the slide'}).count(), 1, 'loading changed manual pause');
  await deck.screenshot({path: path.join(output, 'loaded-after-retry.png')});

  for (const theme of ['dark', 'light']) {
    for (const route of ['getting-started/', '1.7/getting-started/']) {
      await page.goto(new URL(route, base).href, {waitUntil: 'networkidle'});
      await page.evaluate((value) => { localStorage.setItem('theme', value); document.documentElement.dataset.theme = value; }, theme);
      const menu = page.locator('.theme-doc-sidebar-menu').first();
      const examples = menu.getByRole('button', {name: "Expand sidebar category 'Examples'", exact: true});
      if (await examples.count()) await examples.click();
      const names = ['Examples', 'Ecosystem by ecosystem', 'Game development', 'dispat in CI'];
      let baseline;
      for (const name of names) {
        const caret = menu.getByRole('button', {name: new RegExp(`^(Expand|Collapse) sidebar category '${name}'$`)});
        if (!(await caret.count())) continue; // Older versions may not have newer categories.
        await caret.scrollIntoViewIfNeeded();
        const row = caret.locator('..');
        assert.equal(await row.locator('.menu__link--sublist-caret').count(), 0, 'pseudo caret survived alongside native button');
        const geometry = await caret.evaluate((node) => ({width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height}));
        baseline ??= geometry;
        assert.deepEqual(geometry, baseline, `inconsistent caret geometry: ${theme} ${route} ${name}`);
        const before = await caret.getAttribute('aria-expanded');
        await caret.focus();
        await caret.press('Space');
        assert.notEqual(await caret.getAttribute('aria-expanded'), before, 'Space did not toggle caret');
        await caret.press('Enter');
        assert.equal(await caret.getAttribute('aria-expanded'), before, 'Enter did not restore caret');
        const label = row.locator('.menu__link');
        if (await label.getAttribute('role') === 'button') {
          await label.focus();
          await label.press('Space');
          assert.notEqual(await caret.getAttribute('aria-expanded'), before, 'Space did not toggle unlinked label');
          await label.press('Enter');
          assert.equal(await caret.getAttribute('aria-expanded'), before, 'Enter did not restore unlinked label');
        }
        await caret.hover();
        const styles = await row.evaluate((node) => ({row: getComputedStyle(node).backgroundColor, caret: getComputedStyle(node.querySelector('.menu__caret')).backgroundColor, pseudo: getComputedStyle(node.querySelector('.menu__caret'), '::before').backgroundColor}));
        assert.notEqual(styles.row, 'rgba(0, 0, 0, 0)', 'row lost hover/focus background');
        assert.equal(styles.pseudo, 'rgba(0, 0, 0, 0)', 'filtered arrow has its own colored background');
      }
      await menu.screenshot({path: path.join(output, `sidebar-${theme}-${route.startsWith('1.7') ? '1.7' : '1.8'}.png`)});
    }
  }
  assert.deepEqual(errors, []);
  console.log('navigation passed: retained lazy scene, latest selection, failure/retry, shared category hover and keyboard behavior in both themes and versions');
} finally {
  await context.close();
  await browser.close();
}
