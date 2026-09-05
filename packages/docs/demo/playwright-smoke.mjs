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
const expectedSlides = 18;
const controlTolerance = 2;

function fail(message, details) {
  throw new Error(details === undefined ? message : `${message}: ${JSON.stringify(details)}`);
}

async function openPage({name, viewport, theme, reducedMotion = 'no-preference'}) {
  const directory = path.join(output, name);
  await fs.mkdir(directory, {recursive: true});
  const context = await browser.newContext({
    viewport,
    colorScheme: theme,
    reducedMotion,
    recordVideo: {dir: directory, size: viewport},
  });
  await context.addInitScript((selectedTheme) => {
    localStorage.setItem('theme', selectedTheme);
  }, theme);
  const page = await context.newPage();
  const errors = [];
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(url, {waitUntil: 'networkidle'});
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate((selectedTheme) => document.documentElement.setAttribute('data-theme', selectedTheme), theme);
  if (await page.locator('html').getAttribute('data-theme') !== theme) fail(`${name} did not select ${theme} theme`);
  const carousel = page.locator('[data-demo-id="landing-demos"]');
  await carousel.scrollIntoViewIfNeeded();
  await page.waitForTimeout(800);
  return {name, context, page, carousel, errors, directory};
}

async function pause(carousel) {
  const pauseButton = carousel.getByRole('button', {name: 'Pause the slide'});
  if (await pauseButton.count()) await pauseButton.click();
  if (await carousel.getByRole('button', {name: 'Play the slide'}).count() !== 1) {
    fail('the carousel did not enter its manually paused state');
  }
}

async function relativeControlY(carousel) {
  const [root, controls] = await Promise.all([
    carousel.boundingBox(),
    carousel.getByRole('toolbar', {name: 'Slide controls'}).boundingBox(),
  ]);
  if (!root || !controls) fail('carousel or controls have no layout box');
  return controls.y - root.y;
}

async function selectSlide(carousel, id) {
  await carousel.locator(`[data-demo-feature="${id}"]`).click();
  await carousel.locator(`[data-slide-id="${id}"]`).waitFor({state: 'visible'});
  await carousel.locator('[data-demo-scene-root]').waitFor({state: 'attached', timeout: 10000});
  if (await carousel.locator('[data-slide-id]').count() !== 1) fail(`slide ${id} mounted beside another slide`);
}

async function setDetails(carousel, open) {
  const details = carousel.locator('details');
  if (await details.count() !== 1) fail('accessible description and transcript is missing');
  if ((await details.evaluate((node) => node.open)) !== open) {
    const summary = details.locator('summary');
    await summary.focus();
    await summary.press('Enter');
    await details.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  }
  if ((await details.evaluate((node) => node.open)) !== open) fail(`details did not become ${open ? 'open' : 'closed'}`);
}

async function assertDetails(carousel, open, action) {
  const actual = await carousel.locator('details').evaluate((node) => node.open);
  if (actual !== open) fail(`${action} lost the shared ${open ? 'expanded' : 'collapsed'} transcript preference`);
}

async function inspectEverySlide({name, page, carousel, directory}) {
  await pause(carousel);
  const transportLabels = await carousel.getByRole('toolbar').locator('button').evaluateAll(
    (nodes) => nodes.slice(0, 3).map((node) => node.getAttribute('aria-label')),
  );
  if (transportLabels[0] !== 'Loop the current slide' || transportLabels[1] !== 'Play the slide'
      || !transportLabels[2]?.startsWith('Playback speed ')) {
    fail(`${name} playback controls must follow repeat, play/pause, speed`, transportLabels);
  }
  const buttons = carousel.locator('[data-demo-feature]');
  const count = await buttons.count();
  if (count !== expectedSlides) fail(`${name} has ${count} slides, expected ${expectedSlides}`);
  const ids = await buttons.evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-demo-feature')));
  if (new Set(ids).size !== expectedSlides || ids.some((id) => !id)) fail(`${name} has missing or duplicate stable slide IDs`, ids);

  await selectSlide(carousel, ids[0]);
  const baselineY = await relativeControlY(carousel);
  const baselineTitle = await carousel.locator('[class*="slideTitle"]').boundingBox();
  if (!baselineTitle) fail(`${name} title has no layout box`);
  for (const id of ids) {
    await selectSlide(carousel, id);
    const y = await relativeControlY(carousel);
    if (Math.abs(y - baselineY) > controlTolerance) {
      fail(`${name} controls moved on slide ${id}`, {baselineY, y});
    }
    const title = await carousel.locator('[class*="slideTitle"]').boundingBox();
    if (!title || Math.abs(title.height - baselineTitle.height) > controlTolerance) {
      fail(`${name} title height changed on slide ${id}`, {baseline: baselineTitle.height, actual: title?.height});
    }
    const [controls, band] = await Promise.all([
      carousel.getByRole('toolbar', {name: 'Slide controls'}).boundingBox(),
      carousel.locator('div[class*="band_"]').boundingBox(),
    ]);
    if (!controls || !band || controls.y + controls.height > band.y + controlTolerance) {
      fail(`${name} controls are not before the text band on slide ${id}`, {controls, band});
    }
    await carousel.screenshot({
      path: path.join(directory, `slide-${id}.png`),
      style: '.navbar { visibility: hidden !important; }',
    });
  }
  return {ids, baselineY};
}

async function assertTranscriptAndTransport({page, carousel, ids, baselineY}) {
  const first = ids[0];
  const second = ids[1];
  const third = ids[2];

  await setDetails(carousel, false);
  const closedY = await relativeControlY(carousel);
  if (Math.abs(closedY - baselineY) > controlTolerance) fail('controls moved when transcript closed', {baselineY, closedY});
  await selectSlide(carousel, first);
  await assertDetails(carousel, false, 'manual navigation');

  await setDetails(carousel, true);
  const openY = await relativeControlY(carousel);
  if (Math.abs(openY - baselineY) > controlTolerance) fail('controls moved when transcript opened', {baselineY, openY});
  await selectSlide(carousel, second);
  await assertDetails(carousel, true, 'manual navigation');

  // A manual pause is transport state, not slide state.
  await pause(carousel);
  await selectSlide(carousel, third);
  if (await carousel.getByRole('button', {name: 'Play the slide'}).count() !== 1) fail('manual pause was lost on selection');

  // Cycle 1x -> 1.5x -> 2x, then let two clips end. Both explicit details
  // preferences must survive the same automatic selection path.
  await selectSlide(carousel, 'blast-radius');
  await carousel.scrollIntoViewIfNeeded();
  const speed = carousel.getByRole('button', {name: /Playback speed .* click to change/});
  for (let attempt = 0; attempt < 4 && (await speed.textContent())?.trim() !== '2×'; attempt += 1) {
    await speed.click();
  }
  if ((await speed.textContent())?.trim() !== '2×') fail('could not select 2x playback speed');
  await setDetails(carousel, false);
  const beforeClosedAdvance = await carousel.locator('[data-slide-id]').getAttribute('data-slide-id');
  await carousel.getByRole('button', {name: 'Play the slide'}).click();
  await page.waitForFunction(
    (id) => document.querySelector('[data-demo-id="landing-demos"] [data-slide-id]')?.getAttribute('data-slide-id') !== id,
    beforeClosedAdvance,
    {timeout: 45000},
  );
  await assertDetails(carousel, false, 'automatic advance');

  await setDetails(carousel, true);
  const beforeOpenAdvance = await carousel.locator('[data-slide-id]').getAttribute('data-slide-id');
  await page.waitForFunction(
    (id) => document.querySelector('[data-demo-id="landing-demos"] [data-slide-id]')?.getAttribute('data-slide-id') !== id,
    beforeOpenAdvance,
    {timeout: 45000},
  );
  await assertDetails(carousel, true, 'automatic advance');
  await pause(carousel);
}

async function recordLaterBeats({page, carousel, directory}) {
  const speed = carousel.getByRole('button', {name: /Playback speed .* click to change/});
  for (let attempt = 0; attempt < 4 && (await speed.textContent())?.trim() !== '2×'; attempt += 1) {
    await speed.click();
  }
  for (const id of ['math', 'progress']) {
    await selectSlide(carousel, id);
    await carousel.getByRole('button', {name: 'Play the slide'}).click();
    const duration = Number(await carousel.locator('[data-demo-duration]').getAttribute('data-demo-duration'));
    await page.waitForTimeout(duration / 20 / 2 * 0.75 * 1000);
    await pause(carousel);
    if (await carousel.locator('[data-slide-id]').getAttribute('data-slide-id') !== id) fail(`${id} advanced before its later beat`);
    await carousel.screenshot({path: path.join(directory, `slide-${id}-later.png`), style: '.navbar { visibility: hidden !important; }'});
  }
}

async function assertMobileLayout(page, carousel) {
  const widths = await page.evaluate(() => {
    const dimensions = (selector) => {
      const node = document.querySelector(selector);
      if (!(node instanceof HTMLElement)) throw new Error(`missing ${selector}`);
      return {client: node.clientWidth, scroll: node.scrollWidth};
    };
    return {
      page: {client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth},
      carousel: dimensions('[data-demo-id="landing-demos"]'),
      canvas: dimensions('[data-demo-canvas]'),
      controls: dimensions('[role="toolbar"][aria-label="Slide controls"]'),
      title: dimensions('[class*="slideTitle"]'),
      band: dimensions('[class*="band"]'),
      transcript: dimensions('[class*="transcript"]'),
    };
  });
  if (widths.page.scroll !== widths.page.client) fail('page overflows mobile viewport', widths);
  for (const [name, width] of Object.entries(widths)) {
    if (width.scroll !== width.client) fail(`${name} overflows mobile viewport`, widths);
  }
  const viewport = carousel.locator('[data-demo-layout="portrait"]');
  if (await viewport.count() !== 1) fail('mobile scene did not use portrait layout');
  const box = await viewport.boundingBox();
  if (!box || Math.abs(box.width / box.height - 720 / 1280) > 0.002) fail('portrait aspect ratio changed', box);

}

async function dragSelection(page, locator) {
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  if (!box) fail('selection target has no layout box');
  await page.evaluate(() => window.getSelection()?.removeAllRanges());
  await page.mouse.move(box.x + 8, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width - 8, box.y + box.height / 2, {steps: 12});
  await page.mouse.up();
  return page.evaluate(() => window.getSelection()?.toString() ?? '');
}

async function assertSelectionPolicy(page, carousel) {
  if (await dragSelection(page, carousel.locator('[class*="slideTitle"]'))) {
    fail('dragging the slide title selected player text');
  }
  if (await dragSelection(page, carousel.locator('[class*="sceneViewport"]'))) {
    fail('dragging the animated scene selected player text');
  }

  await setDetails(carousel, true);
  const command = carousel.locator('details code');
  const selectedCommand = {
    text: await dragSelection(page, command),
    userSelect: await command.evaluate((node) => getComputedStyle(node).userSelect),
  };
  if (!selectedCommand.text.trim() || selectedCommand.userSelect === 'none') {
    fail('transcript command is not selectable', selectedCommand);
  }

  if (await page.locator('header[class*="hero"] img').count()) {
    fail('the removed hero logo row is still present');
  }
  const brand = page.locator('.navbar__brand').first();
  if (await brand.evaluate((node) => getComputedStyle(node).userSelect) !== 'none') {
    fail('navbar brand selection policy is missing');
  }
}

async function assertStaticHeadingAnchors(page) {
  const headings = await page.locator('main h2, main h3').evaluateAll((nodes) => nodes
    .filter((node) => !node.closest('[data-demo-id="landing-demos"]'))
    .map((node) => ({
      id: node.id,
      level: node.tagName.toLowerCase(),
      text: node.textContent?.trim(),
      hrefs: [...node.querySelectorAll('a[href^="#"]')].map((link) => link.getAttribute('href')),
    })));
  if (!headings.length) fail('landing page has no static section headings');
  if (headings.some(({id}) => !id)) fail('static landing heading is missing an ID', headings);
  const ids = headings.map(({id}) => id);
  if (new Set(ids).size !== ids.length) fail('static landing heading IDs are not unique', ids);
  for (const heading of headings) {
    if (!heading.hrefs.includes(`#${heading.id}`)) {
      fail(`static ${heading.level} #${heading.id} has no matching anchor href`, heading);
    }
  }
  if (!ids.includes('install')) fail('the existing install anchor is missing', ids);

  const nested = headings.find(({level}) => level === 'h3');
  const targets = [...new Set(['install', 'demos', nested?.id].filter(Boolean))];
  for (const id of targets) {
    await page.locator(`main a[href="#${id}"]`).first().click();
    if (decodeURIComponent(new URL(page.url()).hash) !== `#${id}`) {
      fail(`clicking #${id} did not update the URL`, page.url());
    }
    await page.reload({waitUntil: 'networkidle'});
    await page.evaluate(() => document.fonts.ready);
    const target = page.locator(`main [id="${id}"]`);
    const visible = await target.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return rect.bottom > 0 && rect.top < window.innerHeight;
    });
    if (!visible) fail(`reloaded #${id} target is outside the viewport`);
  }
}

const scenarios = [
  {name: 'desktop-light', viewport: {width: 1440, height: 1000}, theme: 'light'},
  {name: 'desktop-dark', viewport: {width: 1440, height: 1000}, theme: 'dark'},
  {name: 'mobile-light', viewport: {width: 390, height: 844}, theme: 'light'},
  {name: 'mobile-dark-reduced', viewport: {width: 390, height: 844}, theme: 'dark', reducedMotion: 'reduce'},
  {name: 'mobile-320-light', viewport: {width: 320, height: 720}, theme: 'light'},
];

try {
  for (const scenario of scenarios) {
    const state = await openPage(scenario);
    try {
      if (scenario.reducedMotion === 'reduce' &&
          await state.carousel.getByRole('button', {name: 'Play the slide'}).count() !== 1) {
        fail('reduced motion did not start on a paused meaningful still');
      }
      const inspected = await inspectEverySlide(state);
      if (scenario.name.startsWith('desktop-')) await recordLaterBeats(state);
      if (scenario.name === 'desktop-light') {
        await assertStaticHeadingAnchors(state.page);
        await assertTranscriptAndTransport({...state, ...inspected});
        await assertSelectionPolicy(state.page, state.carousel);
      }
      if (scenario.name.startsWith('mobile-')) await assertMobileLayout(state.page, state.carousel);
      if (state.errors.length) fail(`${scenario.name} browser errors`, state.errors);
    } finally {
      await state.context.close();
    }
  }
  console.log(`demo smoke passed: ${expectedSlides} slides in desktop/mobile light/dark; recordings and screenshots in ${output}`);
} finally {
  await browser.close();
}
