#!/usr/bin/env node
import {createRequire} from 'node:module';
import fs from 'node:fs/promises';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('PLAYWRIGHT_RUNTIME must name a directory containing the playwright package');
const require = createRequire(path.join(runtime, 'package.json'));
const {chromium} = require('playwright');
const url = process.argv[2] ?? 'http://127.0.0.1:3000/';
const output = path.resolve(process.argv[3] ?? 'playwright-menu-output');
const widths = [320, 390, 768, 996];
const routes = ['/', '/getting-started', '/1.7/getting-started'];
await fs.mkdir(output, {recursive: true});

async function activeMenuLinks(page) {
  return page.locator('.navbar-sidebar__item a').evaluateAll((links) => links.flatMap((link, index) => {
    const rect = link.getBoundingClientRect();
    const clippedWidth = Math.min(rect.right, innerWidth) - Math.max(rect.left, 0);
    const clippedHeight = Math.min(rect.bottom, innerHeight) - Math.max(rect.top, 0);
    if (clippedWidth <= 1 || clippedHeight <= 1) return [];
    const x = Math.max(rect.left, 0) + clippedWidth / 2;
    const y = Math.max(rect.top, 0) + clippedHeight / 2;
    if (document.elementFromPoint(x, y)?.closest('a') !== link) return [];
    return [{index, text: link.textContent.trim(), href: link.href, rect: rect.toJSON()}];
  }));
}

async function assertActiveMenu(page, label) {
  const links = await activeMenuLinks(page);
  if (links.length < 2) throw new Error(`${label}: fewer than two on-screen, hittable menu links: ${JSON.stringify(links)}`);
  const link = page.locator('.navbar-sidebar__item a').nth(links[0].index);
  await link.focus();
  if (!await link.evaluate((node) => node === document.activeElement)) throw new Error(`${label}: active menu link cannot receive keyboard focus`);
  return links;
}

const browser = await chromium.launch({headless: true});
try {
  for (const width of widths) for (const theme of ['light', 'dark']) for (const route of routes) {
    const slug = `${route.replaceAll('/', '_') || 'landing'}-${width}-${theme}`;
    const context = await browser.newContext({
      viewport: {width, height: 760}, colorScheme: theme,
      recordVideo: {dir: output, size: {width, height: 760}},
    });
    await context.addInitScript((value) => localStorage.setItem('theme', value), theme);
    const page = await context.newPage();
    const video = page.video();
    await page.goto(new URL(route, url).href, {waitUntil: 'networkidle'});
    await page.evaluate(() => scrollTo(0, Math.min(1200, document.body.scrollHeight - innerHeight)));
    const toggle = page.locator('.navbar__toggle');
    await toggle.focus(); await toggle.press('Enter');
    const sidebar = page.locator('.navbar-sidebar');
    await sidebar.waitFor({state: 'visible'}); await page.waitForTimeout(300);
    const geometry = await page.evaluate(() => {
      const rect = (node) => node?.getBoundingClientRect().toJSON();
      const sidebar = document.querySelector('.navbar-sidebar');
      const items = document.querySelector('.navbar-sidebar__items');
      const backdrop = document.querySelector('.navbar-sidebar__backdrop');
      return {viewport: innerHeight, sidebar: rect(sidebar), items: rect(items), backdrop: rect(backdrop), itemScrollHeight: items?.scrollHeight};
    });
    if (geometry.sidebar.height < geometry.viewport - 1 || geometry.items.height <= 100 || geometry.backdrop.height < geometry.viewport - 1) throw new Error(`${route} ${width}px ${theme}: collapsed mobile menu ${JSON.stringify(geometry)}`);

    let links = await assertActiveMenu(page, `${route} ${width}px ${theme} initial panel`);
    const back = sidebar.locator('.navbar-sidebar__back');
    if (await sidebar.locator('.navbar-sidebar__items').evaluate((node) => node.classList.contains('navbar-sidebar__items--show-secondary'))) {
      if (!links.some(({text}) => text === 'Getting started')) throw new Error(`${route} ${width}px ${theme}: secondary docs menu is not on screen`);
      await back.click(); await page.waitForTimeout(300);
      links = await assertActiveMenu(page, `${route} ${width}px ${theme} primary panel`);
    }
    if (!links.some(({text}) => text === 'Docs')) throw new Error(`${route} ${width}px ${theme}: primary menu is not on screen`);

    const close = sidebar.locator('.navbar-sidebar__close');
    const closeIsTopmost = await close.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)?.closest('button') === node;
    });
    if (!closeIsTopmost || await page.locator('.DocSearch-Modal:visible').count()) throw new Error(`${route} ${width}px ${theme}: an overlay blocks the menu close control`);
    await close.focus(); await close.press('Enter');
    await page.waitForFunction(() => !document.querySelector('.navbar')?.classList.contains('navbar-sidebar--show'));
    await toggle.focus(); await toggle.press('Enter'); await page.waitForTimeout(300);
    links = await assertActiveMenu(page, `${route} ${width}px ${theme} reopened panel`);
    const before = page.url();
    const target = links.find(({href}) => href && href !== before && !href.startsWith(`${before}#`));
    if (!target) throw new Error(`${route} ${width}px ${theme}: no on-screen keyboard navigation target`);
    const navigable = page.locator('.navbar-sidebar__item a').nth(target.index);
    await navigable.focus(); await navigable.press('Enter'); await page.waitForLoadState('domcontentloaded');
    if (page.url() === before) throw new Error(`${route} ${width}px ${theme}: keyboard link did not navigate`);
    await page.waitForFunction(() => !document.querySelector('.navbar')?.classList.contains('navbar-sidebar--show'));
    await page.goBack({waitUntil: 'networkidle'}); await toggle.press('Enter'); await page.waitForTimeout(300);
    await assertActiveMenu(page, `${route} ${width}px ${theme} route-back panel`);
    await page.screenshot({path: path.join(output, `${slug}.png`), fullPage: false});
    await context.close();
    await fs.rename(await video.path(), path.join(output, `${slug}.webm`));
  }
  console.log(`mobile menu passed at ${widths.join('/')}px, both themes, landing/current/1.7 docs; screenshots and videos: ${output}`);
} finally { await browser.close(); }
