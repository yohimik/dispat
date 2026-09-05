import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('Set PLAYWRIGHT_RUNTIME to the directory containing node_modules/playwright');
const {chromium} = createRequire(path.join(runtime, 'package.json'))('playwright');
const base = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'output/playwright/mobile-scenes');
await fs.mkdir(output, {recursive: true});

const expectedSlides = 19;
const selectedScene = process.argv[4];
const checkpoints = [0.1, 0.45, 0.8];
const scenarios = [
  {name: '390-light', viewport: {width: 390, height: 844}, colorScheme: 'light'},
  {name: '390-dark', viewport: {width: 390, height: 844}, colorScheme: 'dark'},
  {name: '320-light', viewport: {width: 320, height: 720}, colorScheme: 'light'},
  {name: '320-dark', viewport: {width: 320, height: 720}, colorScheme: 'dark'},
];

async function inspectGeometry(deck, id, checkpoint) {
  return deck.locator('[data-demo-scene-root]').evaluate((root, marker) => {
    if (!(root instanceof HTMLElement)) return [`${marker}: composition root is missing`];
    const scene = root.closest('[data-demo-layout]');
    if (!(scene instanceof HTMLElement)) return [`${marker}: scene viewport is missing`];
    const tolerance = 2;
    const failures = [];
    const within = (inner, outer) => inner.left >= outer.left - tolerance && inner.top >= outer.top - tolerance
      && inner.right <= outer.right + tolerance && inner.bottom <= outer.bottom + tolerance;
    const visible = (element) => {
      if (!(element instanceof HTMLElement || element instanceof SVGElement)) return false;
      const style = getComputedStyle(element);
      return style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity) > 0.02;
    };
    const sceneBounds = scene.getBoundingClientRect();
    if (scene.getAttribute('data-demo-layout') !== 'portrait') failures.push(`${marker}: landscape player remained mounted`);
    if (root.offsetWidth !== 720 || root.offsetHeight !== 1280) {
      failures.push(`${marker}: composition is ${root.offsetWidth}x${root.offsetHeight}, expected 720x1280`);
    }
    if (!within(root.getBoundingClientRect(), sceneBounds)) failures.push(`${marker}: composition is cropped by its viewport`);

    const terminal = root.querySelector('[data-demo-terminal]');
    if (terminal instanceof HTMLElement && visible(terminal)) {
      const bounds = terminal.getBoundingClientRect();
      if (!within(bounds, root.getBoundingClientRect())) failures.push(`${marker}: terminal escapes the composition`);
      const scale = bounds.width / terminal.offsetWidth;
      const naturalWidth = terminal.offsetWidth;
      const naturalHeight = terminal.offsetHeight;
      const fontSize = Number.parseFloat(getComputedStyle(terminal).fontSize);
      if (Math.abs(naturalWidth - 680) > tolerance || Math.abs(naturalHeight - 300) > tolerance || fontSize < 26) {
        failures.push(`${marker}: terminal geometry ${naturalWidth}x${naturalHeight}, font ${fontSize}px`);
      }
      for (const row of terminal.children) {
        const rowBounds = row.getBoundingClientRect();
        if (!within(rowBounds, bounds)) failures.push(`${marker}: clipped terminal row ${row.textContent?.trim()}`);
        if (row.scrollWidth > row.clientWidth + 1 || row.scrollHeight * scale > bounds.height + tolerance) {
          failures.push(`${marker}: overflowing terminal row ${row.textContent?.trim()}`);
        }
      }
    }

    const cards = [...root.querySelectorAll('div')].filter((element) => {
      const style = getComputedStyle(element);
      return element instanceof HTMLElement && element.offsetWidth === 320 && element.offsetHeight >= 172
        && style.position === 'absolute' && style.borderStyle === 'solid';
    });
    for (const card of cards) {
      const bounds = card.getBoundingClientRect();
      if (!within(bounds, root.getBoundingClientRect())) failures.push(`${marker}: card escapes composition: ${card.textContent?.trim()}`);
      const smallestText = Math.min(...[...card.querySelectorAll('span, div')]
        .filter((element) => visible(element) && [...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim()))
        .map((element) => Number.parseFloat(getComputedStyle(element).fontSize)));
      if (Number.isFinite(smallestText) && smallestText < 17) failures.push(`${marker}: card text below 17px: ${smallestText}px`);
      for (const child of card.children) {
        if (visible(child) && !within(child.getBoundingClientRect(), bounds)) {
          failures.push(`${marker}: card content escapes: ${child.textContent?.trim()}`);
        }
      }
    }
    for (let left = 0; left < cards.length; left++) {
      for (let right = left + 1; right < cards.length; right++) {
        const a = cards[left].getBoundingClientRect();
        const b = cards[right].getBoundingClientRect();
        if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > tolerance
            && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > tolerance) {
          failures.push(`${marker}: package cards overlap`);
        }
      }
    }

    for (const stage of root.querySelectorAll('[data-demo-stage]')) {
      const caption = stage.firstElementChild;
      if (caption && (caption.scrollHeight > caption.clientHeight + 1 || caption.scrollWidth > caption.clientWidth + 1)) {
        failures.push(`${marker}: hook caption overlaps its stage: ${caption.textContent}`);
      }
      const terminal = root.querySelector('[data-demo-terminal]');
      if (terminal && stage.getBoundingClientRect().bottom > terminal.getBoundingClientRect().top + tolerance) {
        failures.push(`${marker}: hook stage overlaps terminal`);
      }
    }

    const edgeLabels = [...root.querySelectorAll('[data-demo-edge-label]')].filter((label) => {
      if (!visible(label)) return false;
      for (let element = label.parentElement; element && element !== root; element = element.parentElement) {
        if (Number(getComputedStyle(element).opacity) <= 0.02) return false;
      }
      return true;
    });
    for (let left = 0; left < edgeLabels.length; left++) {
      for (let right = left + 1; right < edgeLabels.length; right++) {
        const a = edgeLabels[left].getBoundingClientRect();
        const b = edgeLabels[right].getBoundingClientRect();
        if (Math.min(a.right, b.right) - Math.max(a.left, b.left) > tolerance
            && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > tolerance) {
          failures.push(`${marker}: edge labels overlap: ${edgeLabels[left].textContent?.trim()} / ${edgeLabels[right].textContent?.trim()}`);
        }
      }
    }

    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (!node.textContent?.trim() || !(node.parentElement instanceof HTMLElement) || !visible(node.parentElement)) continue;
      if (node.parentElement.closest('[data-demo-terminal]')) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      const bounds = range.getBoundingClientRect();
      if (bounds.width > 0 && bounds.height > 0 && !within(bounds, root.getBoundingClientRect())) {
        failures.push(`${marker}: visible text escapes composition: ${node.textContent.trim()}`);
      }
      const terminalBounds = root.querySelector('[data-demo-terminal]')?.getBoundingClientRect();
      if (terminalBounds && bounds.width > 0 && bounds.height > 0
          && bounds.bottom > terminalBounds.top + tolerance && bounds.top < terminalBounds.bottom
          && bounds.right > terminalBounds.left && bounds.left < terminalBounds.right) {
        failures.push(`${marker}: scene text overlaps terminal: ${node.textContent.trim()}`);
      }
    }
    return [...new Set(failures)];
  }, `${id}@${checkpoint}`);
}

async function runScenario(browser, scenario) {
  const directory = path.join(output, scenario.name);
  await fs.mkdir(directory, {recursive: true});
  const context = await browser.newContext({
    viewport: scenario.viewport,
    colorScheme: scenario.colorScheme,
    reducedMotion: 'reduce',
    recordVideo: {dir: directory, size: scenario.viewport},
  });
  await context.addInitScript((theme) => localStorage.setItem('theme', theme), scenario.colorScheme);
  const page = await context.newPage();
  const browserErrors = [];
  page.on('pageerror', (error) => browserErrors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') browserErrors.push(message.text()); });
  const failures = [];
  const captures = [];
  try {
    await page.goto(base, {waitUntil: 'networkidle'});
    await page.evaluate(() => document.fonts.ready);
    const deck = page.locator('[data-demo-id="landing-demos"]');
    await deck.scrollIntoViewIfNeeded();
    await deck.locator('[data-demo-scene-root]').waitFor({timeout: 15000});
    const ids = await deck.locator('[data-demo-feature]').evaluateAll((nodes) => nodes.map((node) => node.dataset.demoFeature));
    assert.equal(ids.length, expectedSlides);
    await deck.getByRole('button', {name: 'Loop the current slide'}).click();
    const speed = deck.getByRole('button', {name: /Playback speed/});
    while ((await speed.textContent())?.trim() !== '2×') await speed.click();

    if (selectedScene) assert.ok(ids.includes(selectedScene), `unknown scene: ${selectedScene}`);
    for (const id of selectedScene ? [selectedScene] : ids) {
      await deck.locator(`[data-demo-feature="${id}"]`).click();
      await deck.locator(`[data-slide-id="${id}"]`).waitFor();
      await deck.locator('[data-demo-scene-root]').waitFor();
      await deck.locator('[data-demo-layout="portrait"]').waitFor();
      const {duration, fps} = await deck.locator('[data-demo-duration]').evaluate((node) => ({
        duration: Number(node.dataset.demoDuration),
        fps: Number(node.dataset.demoFps),
      }));
      failures.push(...await inspectGeometry(deck, id, 'still'));
      await deck.getByRole('button', {name: 'Play the slide'}).click();
      const started = Date.now();
      for (const checkpoint of checkpoints) {
        const targetMs = duration / fps / 2 * checkpoint * 1000;
        await page.waitForTimeout(Math.max(0, targetMs - (Date.now() - started)));
        failures.push(...await inspectGeometry(deck, id, checkpoint));
        const capture = `${id}-${String(checkpoint).replace('.', '')}.png`;
        await deck.locator('[data-demo-canvas]').screenshot({path: path.join(directory, capture)});
        captures.push(capture);
      }
      await deck.getByRole('button', {name: 'Pause the slide'}).click();
      if (await deck.locator('[data-slide-id]').getAttribute('data-slide-id') !== id) failures.push(`${id}: advanced despite loop`);
    }

    const overflow = await page.evaluate(() => ({client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth}));
    if (overflow.scroll > overflow.client + 1) failures.push(`page width ${overflow.scroll} exceeds ${overflow.client}`);
    const canvasOverflow = await deck.locator('[data-demo-canvas]').evaluate((node) => ({client: node.clientWidth, scroll: node.scrollWidth}));
    if (canvasOverflow.scroll > canvasOverflow.client + 1) failures.push(`canvas width ${canvasOverflow.scroll} exceeds ${canvasOverflow.client}`);
    failures.push(...browserErrors.map((error) => `browser: ${error}`));
    return {...scenario, captures, failures: [...new Set(failures)]};
  } finally {
    await context.close();
  }
}

const browser = await chromium.launch({headless: true});
try {
  const results = await Promise.all(scenarios.map((scenario) => runScenario(browser, scenario)));
  await fs.writeFile(path.join(output, 'results.json'), `${JSON.stringify(results, null, 2)}\n`);
  const failures = results.flatMap((result) => result.failures.map((failure) => `${result.name}: ${failure}`));
  assert.deepEqual(failures, []);
  console.log(`mobile scene geometry passed: ${selectedScene ?? `${expectedSlides} scenes`} at three beats, 320/390px, light/dark`);
} finally {
  await browser.close();
}
