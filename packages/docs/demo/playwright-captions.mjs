import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('Set PLAYWRIGHT_RUNTIME to the directory containing node_modules/playwright');
const {chromium} = createRequire(path.join(runtime, 'package.json'))('playwright');
const base = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'output/playwright/captions');
await fs.mkdir(output, {recursive: true});
const browser = await chromium.launch();
const results = [];

async function geometry(deck) {
  return deck.evaluate((node) => {
    const outer = node.getBoundingClientRect();
    const caption = node.querySelector('#landing-demo-description').getBoundingClientRect();
    const details = node.querySelector('details');
    const summary = details.querySelector('summary').getBoundingClientRect();
    return {height: outer.height, captionTop: caption.top - outer.top, captionLeft: caption.left - outer.left,
      summaryTop: summary.top - outer.top, summaryLeft: summary.left - outer.left, transcriptHeight: details.getBoundingClientRect().height};
  });
}

function stable(actual, expected, label) {
  for (const key of Object.keys(expected)) {
    assert.ok(Math.abs(actual[key] - expected[key]) <= 1, `${label}: ${key} moved from ${expected[key]} to ${actual[key]}`);
  }
}

try {
  await Promise.all([1440, 390, 320].map(async (width) => {
    for (const theme of ['light', 'dark']) {
      const directory = path.join(output, `${width}-${theme}`);
      await fs.mkdir(directory, {recursive: true});
      const context = await browser.newContext({viewport: {width, height: 1000}, colorScheme: theme,
        reducedMotion: 'reduce', recordVideo: {dir: directory}});
      await context.addInitScript((value) => localStorage.setItem('theme', value), theme);
      const page = await context.newPage();
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      try {
        await page.goto(base, {waitUntil: 'networkidle'});
        await page.evaluate(() => document.fonts.ready);
        const deck = page.locator('[data-demo-id="landing-demos"]');
        await deck.locator('[data-demo-scene-root]').waitFor();
        const ids = await deck.locator('[data-demo-feature]').evaluateAll((nodes) => nodes.map((node) => node.dataset.demoFeature));
        assert.equal(ids.length, 19);
        for (const open of [false, true]) {
          const details = deck.locator('details');
          if (await details.evaluate((node) => node.open) !== open) await details.locator('summary').click();
          const baseline = await geometry(deck);
          for (const id of ids) {
            await deck.locator(`[data-demo-feature="${id}"]`).click();
            await deck.locator(`[data-slide-id="${id}"]`).waitFor();
            assert.equal(await details.evaluate((node) => node.open), open, 'scene change toggled transcript');
            stable(await geometry(deck), baseline, `${width}px ${theme} ${id} open=${open}`);
            assert.equal(await deck.locator('#landing-demo-description:visible').count(), 1);
            assert.equal(await details.locator('pre:visible').count(), open ? 1 : 0);
            const hidden = await deck.locator('[aria-hidden="true"][inert]').evaluateAll((nodes) => nodes.every((node) => getComputedStyle(node).visibility === 'hidden'));
            assert.ok(hidden, 'inactive copy became visible');
          }
          await deck.locator('[data-demo-description-band]').screenshot({path: path.join(directory, `transcript-${open ? 'open' : 'closed'}.png`)});
        }
        const sectionOrder = await page.locator('main h2').evaluateAll((nodes) => nodes.map((node) => node.id));
        assert.deepEqual(sectionOrder, ['demos', 'workflows', 'projects', 'install', 'documentation', 'libraries', 'inspiration', 'community']);
        assert.deepEqual(errors, []);
        results.push({width, theme, scenes: ids.length, errors});
      } finally {
        await context.close();
      }
    }
  }));
  await fs.writeFile(path.join(output, 'results.json'), JSON.stringify(results, null, 2));
  console.log('caption geometry passed: 19 scenes, open/closed transcripts, desktop/390/320, both themes; landing order matches');
} finally {
  await browser.close();
}
