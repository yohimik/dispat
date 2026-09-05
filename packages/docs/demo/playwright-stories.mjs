import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('Set PLAYWRIGHT_RUNTIME to the directory containing node_modules/playwright');
const {chromium} = createRequire(path.join(runtime, 'package.json'))('playwright');
const base = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'output/playwright/stories');
await fs.mkdir(output, {recursive: true});
const browser = await chromium.launch();
const results = [];
try {
  await Promise.all([0, 1, 2].map(async (group) => {
    const directory = path.join(output, `group-${group}`);
    await fs.mkdir(directory, {recursive: true});
    const theme = group === 1 ? 'light' : 'dark';
    const context = await browser.newContext({viewport: {width: 1440, height: 1100}, colorScheme: theme, reducedMotion: 'reduce', recordVideo: {dir: directory, size: {width: 1440, height: 1100}}});
    await context.addInitScript((value) => localStorage.setItem('theme', value), theme);
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    try {
      await page.goto(base, {waitUntil: 'networkidle'});
      const deck = page.locator('[data-demo-id="landing-demos"]');
      await deck.scrollIntoViewIfNeeded();
      await deck.locator('[data-demo-scene-root]').waitFor();
      await deck.getByRole('button', {name: 'Loop the current slide'}).click();
      const ids = await deck.locator('[data-demo-feature]').evaluateAll((nodes) => nodes.map((node) => node.dataset.demoFeature));
      assert.equal(ids.length, 18);
      for (const id of ids.filter((_, index) => index % 3 === group)) {
        await deck.locator(`[data-demo-feature="${id}"]`).click();
        await deck.locator(`[data-slide-id="${id}"]`).waitFor();
        await deck.locator('[data-demo-scene-root]').waitFor();
        await deck.locator('[data-demo-canvas]').scrollIntoViewIfNeeded();
        const {duration, fps} = await deck.locator('[data-demo-duration]').evaluate((node) => ({duration: Number(node.dataset.demoDuration), fps: Number(node.dataset.demoFps)}));
        assert.ok(duration > 0 && fps === 20);
        assert.equal(await deck.getByRole('button', {name: /Playback speed/}).textContent(), '1×');
        await deck.getByRole('button', {name: 'Play the slide'}).click();
        const started = Date.now();
        const seconds = duration / fps;
        let capture = 0;
        while (Date.now() - started < (seconds - 0.25) * 1000) {
          await page.waitForTimeout(250);
          assert.equal(await deck.locator('[data-slide-id]').getAttribute('data-slide-id'), id);
          const overflows = await deck.locator('[data-demo-terminal]').evaluateAll((nodes) => nodes.flatMap((node) => [...node.children].filter((row) => row.scrollWidth > row.clientWidth + 1).map((row) => row.textContent)));
          assert.deepEqual(overflows, [], `terminal overflow in ${id}`);
          const clippedRows = await deck.locator('[data-demo-terminal]').evaluateAll((nodes) => nodes.flatMap((node) => {
            const terminal = node.getBoundingClientRect();
            return [...node.children].filter((row) => {
              const bounds = row.getBoundingClientRect();
              return bounds.top < terminal.top || bounds.bottom > terminal.bottom;
            }).map((row) => row.textContent);
          }));
          assert.deepEqual(clippedRows, [], `partially clipped terminal row in ${id}`);
          const elapsed = (Date.now() - started) / 1000;
          if (elapsed >= seconds * [0.1, 0.45, 0.8][capture]) {
            await deck.locator('[data-demo-canvas]').screenshot({path: path.join(directory, `${id}-${capture}.png`)});
            capture++;
          }
        }
        await deck.getByRole('button', {name: 'Pause the slide'}).click();
        results.push({id, seconds, theme, captures: capture});
        console.log(`recorded ${id}: ${seconds}s at 1x; terminal width passed throughout`);
      }
      assert.deepEqual(errors, []);
    } finally { await context.close(); }
  }));
  await fs.writeFile(path.join(output, 'results.json'), JSON.stringify(results, null, 2) + '\n');
  console.log('all 18 stories recorded at 1x with terminal overflow checks');
} finally { await browser.close(); }
