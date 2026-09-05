#!/usr/bin/env node
import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('PLAYWRIGHT_RUNTIME must name a directory containing the playwright package');
const require = createRequire(path.join(runtime, 'package.json'));
const {chromium} = require('playwright');
const url = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'playwright-resources-output');
await fs.mkdir(output, {recursive: true});

const browser = await chromium.launch({headless: true});
try {
  const context = await browser.newContext({viewport: {width: 1280, height: 900}, reducedMotion: 'no-preference'});
  await context.addInitScript(() => {
    globalThis.__demoAudioContexts = 0;
    for (const name of ['AudioContext', 'webkitAudioContext']) {
      const Original = globalThis[name];
      if (typeof Original !== 'function') continue;
      globalThis[name] = class CountedAudioContext extends Original {
        constructor(...args) {
          globalThis.__demoAudioContexts += 1;
          super(...args);
        }
      };
    }
  });
  const page = await context.newPage();
  const fontRequests = [];
  const resourceWarnings = [];
  page.on('request', (request) => {
    if (/fonts\.(?:googleapis|gstatic)\.com/i.test(request.url())) fontRequests.push(request.url());
  });
  page.on('console', (message) => {
    if (['warning', 'error'].includes(message.type()) && /font|audio|autoplay/i.test(message.text())) {
      resourceWarnings.push(`${message.type()}: ${message.text()}`);
    }
  });
  page.on('pageerror', (error) => resourceWarnings.push(`pageerror: ${error.message}`));
  await page.goto(url, {waitUntil: 'networkidle'});
  await page.evaluate(() => document.fonts.ready);
  const carousel = page.locator('[data-demo-id="landing-demos"]');
  // Programmatic scrolling is deliberately not a user gesture. Intersection
  // visibility must be sufficient to start the muted player.
  await carousel.evaluate((node) => node.scrollIntoView({block: 'center'}));
  await carousel.locator('[data-demo-scene-root]').waitFor({state: 'attached', timeout: 15000});
  const before = await carousel.locator('[data-demo-scene-root]').innerText();
  await page.waitForTimeout(1800);
  const after = await carousel.locator('[data-demo-scene-root]').innerText();
  if (before === after) throw new Error(`muted autoplay did not advance scene text without a gesture: ${JSON.stringify({before, after})}`);

  const buttons = carousel.locator('[data-demo-feature]');
  const count = await buttons.count();
  if (count !== 19) throw new Error(`expected 19 scenes, found ${count}`);
  const first = await buttons.first().getAttribute('data-demo-feature');
  for (let index = 0; index < count; index += 1) {
    const button = buttons.nth(index);
    const id = await button.getAttribute('data-demo-feature');
    await button.click();
    await carousel.locator(`[data-slide-id="${id}"]`).waitFor({state: 'visible'});
    await carousel.locator('[data-demo-scene-root]').waitFor({state: 'attached', timeout: 15000});
    if (await carousel.locator('[data-demo-scene-root]').count() !== 1) throw new Error(`${id}: more than one player scene is mounted`);
  }
  const fontCountBeforeRevisit = fontRequests.length;
  await carousel.locator(`[data-demo-feature="${first}"]`).click();
  await carousel.locator(`[data-slide-id="${first}"] [data-demo-scene-root]`).waitFor({state: 'attached'});
  await page.waitForTimeout(300);
  if (fontRequests.length !== fontCountBeforeRevisit) {
    throw new Error(`revisiting a scene fetched another font: ${JSON.stringify(fontRequests.slice(fontCountBeforeRevisit))}`);
  }
  if (fontRequests.length > 2) throw new Error(`expected at most two Google font requests, saw ${fontRequests.length}: ${JSON.stringify(fontRequests)}`);

  const resources = await page.evaluate(() => ({
    audioContexts: globalThis.__demoAudioContexts,
    audioElements: document.querySelectorAll('audio').length,
    sceneRoots: document.querySelectorAll('[data-demo-scene-root]').length,
  }));
  if (resources.audioContexts !== 0 || resources.audioElements !== 0 || resources.sceneRoots !== 1) {
    throw new Error(`unexpected player resources: ${JSON.stringify(resources)}`);
  }
  if (resourceWarnings.length) throw new Error(`resource/autoplay console diagnostics: ${JSON.stringify(resourceWarnings)}`);
  await carousel.screenshot({path: path.join(output, 'resources-final.png')});
  await fs.writeFile(path.join(output, 'resources.json'), JSON.stringify({fontRequests, resources}, null, 2) + '\n');
  console.log(`resource regression passed: 19 scenes, ${fontRequests.length} font requests, zero AudioContexts/audio elements`);
  await context.close();
} finally {
  await browser.close();
}
