import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('PLAYWRIGHT_RUNTIME must name a directory containing the playwright package');
const require = createRequire(path.join(runtime, 'package.json'));
const {chromium} = require('playwright');
const url = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'playwright-output');
await fs.mkdir(output, {recursive: true});

const browser = await chromium.launch({headless: true});
const context = await browser.newContext({
  viewport: {width: 1440, height: 1000},
  recordVideo: {dir: output, size: {width: 1440, height: 1000}},
});
const page = await context.newPage();
const errors = [];
page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
page.on('pageerror', (error) => errors.push(error.message));

await page.goto(url, {waitUntil: 'networkidle'});
const carousel = page.locator('[data-demo-id="landing-demos"]');
await carousel.scrollIntoViewIfNeeded();
await page.waitForTimeout(1200);
if (await carousel.locator('[data-slide-id]').count() !== 1) throw new Error('more than one scene is mounted');
await carousel.screenshot({path: path.join(output, 'carousel.png')});

await carousel.getByRole('button', {name: 'Pause the slide'}).click();
await carousel.getByRole('button', {name: 'Playback speed 1x, click to change'}).click();
await carousel.getByRole('button', {name: 'Loop the current slide'}).click();
await carousel.locator('[data-demo-feature="release-graph"]').click();
if (await carousel.locator('[data-slide-id="release-graph"]').count() !== 1) throw new Error('feature selection did not switch scenes');
if ((await carousel.getByRole('button', {name: 'Play the slide'}).count()) !== 1) throw new Error('manual pause was lost on selection');
if ((await carousel.locator('details').count()) !== 1) throw new Error('accessible transcript is missing');
await page.waitForTimeout(1000);

await page.emulateMedia({reducedMotion: 'reduce'});
await page.reload({waitUntil: 'networkidle'});
const reduced = page.locator('[data-demo-id="landing-demos"]');
await reduced.scrollIntoViewIfNeeded();
if ((await reduced.getByRole('button', {name: 'Play the slide'}).count()) !== 1) {
  throw new Error('reduced motion did not start on a paused still');
}

if (errors.length) throw new Error(`browser errors:\n${errors.join('\n')}`);
await context.close();

const mobile = await browser.newContext({
  viewport: {width: 390, height: 844},
  recordVideo: {dir: output, size: {width: 390, height: 844}},
});
const mobilePage = await mobile.newPage();
await mobilePage.goto(url, {waitUntil: 'networkidle'});
const mobileCarousel = mobilePage.locator('[data-demo-id="landing-demos"]');
await mobileCarousel.scrollIntoViewIfNeeded();
await mobileCarousel.screenshot({path: path.join(output, 'carousel-mobile.png')});
const widths = await mobilePage.evaluate(() => {
  const dimensions = (selector) => {
    const node = document.querySelector(selector);
    if (!(node instanceof HTMLElement)) throw new Error(`missing ${selector}`);
    return {client: node.clientWidth, scroll: node.scrollWidth};
  };
  return {
    page: {client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth},
    carousel: dimensions('[data-demo-id="landing-demos"]'),
    canvas: dimensions('[data-demo-canvas]'),
    title: dimensions('[class*="slideTitle"]'),
    band: dimensions('[class*="band"]'),
  };
});
if (widths.page.scroll !== widths.page.client) throw new Error(`page overflows mobile viewport: ${JSON.stringify(widths)}`);
for (const [name, width] of Object.entries({carousel: widths.carousel, title: widths.title, band: widths.band})) {
  if (width.scroll !== width.client) throw new Error(`${name} overflows mobile viewport: ${JSON.stringify(widths)}`);
}
if (widths.canvas.scroll <= widths.canvas.client) throw new Error(`canvas is not independently scrollable: ${JSON.stringify(widths)}`);

const mobileCanvas = mobileCarousel.locator('[data-demo-canvas]');
await mobileCanvas.focus();
const beforeArrow = await mobileCanvas.evaluate((node) => node.scrollLeft);
await mobilePage.keyboard.press('ArrowRight');
await mobilePage.waitForTimeout(150);
const afterArrow = await mobileCanvas.evaluate((node) => node.scrollLeft);
if (afterArrow <= beforeArrow) throw new Error(`ArrowRight did not scroll the focused canvas: ${beforeArrow} -> ${afterArrow}`);
await mobile.close();
await browser.close();
console.log(`demo smoke passed; recording and screenshot in ${output}`);
