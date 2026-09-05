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
await fs.mkdir(output, {recursive: true});

const browser = await chromium.launch({headless: true});
try {
  for (const width of [320, 390]) for (const theme of ['light', 'dark']) for (const route of ['/', '/getting-started', '/1.7/getting-started']) {
    const context = await browser.newContext({viewport: {width, height: 760}, colorScheme: theme});
    await context.addInitScript((value) => localStorage.setItem('theme', value), theme);
    const page = await context.newPage();
    await page.goto(new URL(route, url).href, {waitUntil: 'networkidle'});
    await page.evaluate(() => scrollTo(0, Math.min(1200, document.body.scrollHeight - innerHeight)));
    const toggle = page.locator('.navbar__toggle');
    await toggle.focus(); await toggle.press('Enter');
    const sidebar = page.locator('.navbar-sidebar');
    await sidebar.waitFor({state: 'visible'});
    await page.waitForTimeout(300);
    const geometry = await page.evaluate(() => {
      const sidebar = document.querySelector('.navbar-sidebar');
      const items = document.querySelector('.navbar-sidebar__items');
      const backdrop = document.querySelector('.navbar-sidebar__backdrop');
      const rect = (node) => node?.getBoundingClientRect().toJSON();
      return {viewport: innerHeight, sidebar: rect(sidebar), items: rect(items), backdrop: rect(backdrop), itemScrollHeight: items?.scrollHeight};
    });
    if (geometry.sidebar.height < geometry.viewport - 1 || geometry.items.height <= 100 || geometry.backdrop.height < geometry.viewport - 1) {
      throw new Error(`${route} ${width}px ${theme}: collapsed mobile menu ${JSON.stringify(geometry)}`);
    }
    const visibleLinks = sidebar.locator('a:visible');
    if (await visibleLinks.count() < 2) throw new Error(`${route} ${width}px ${theme}: menu links are absent`);
    const firstLink = visibleLinks.first();
    await firstLink.focus();
    if (!await firstLink.evaluate((node) => node === document.activeElement)) throw new Error('menu link cannot receive keyboard focus');
    const close = sidebar.locator('.navbar-sidebar__close');
    const closeIsTopmost = await close.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)?.closest('button') === node;
    });
    if (!closeIsTopmost || await page.locator('.DocSearch-Modal:visible').count()) {
      throw new Error(`${route} ${width}px ${theme}: an overlay blocks the menu close control`);
    }
    await close.focus(); await close.press('Enter');
    await page.waitForFunction(() => !document.querySelector('.navbar')?.classList.contains('navbar-sidebar--show'));
    await toggle.focus(); await toggle.press('Enter');
    const menuLinks = sidebar.locator('a:visible');
    const before = page.url();
    const targetIndex = await menuLinks.evaluateAll((nodes, current) => nodes.findIndex((node) => {
      const href = node.href;
      return href && href !== current && !href.startsWith(`${current}#`);
    }), before);
    if (targetIndex < 0) throw new Error(`${route} ${width}px ${theme}: no keyboard navigation target`);
    const navigable = menuLinks.nth(targetIndex); await navigable.focus(); await navigable.press('Enter');
    await page.waitForLoadState('domcontentloaded');
    if (page.url() === before) throw new Error(`${route} ${width}px ${theme}: keyboard link did not navigate`);
    await page.waitForFunction(() => !document.querySelector('.navbar')?.classList.contains('navbar-sidebar--show'));
    await page.goBack({waitUntil: 'networkidle'}); await toggle.press('Enter');
    await page.screenshot({path: path.join(output, `${route.replaceAll('/', '_') || 'landing'}-${width}-${theme}.png`), fullPage: false});
    await context.close();
  }
  console.log(`mobile menu passed at 320/390px, both themes, landing/current/1.7 docs; screenshots: ${output}`);
} finally {
  await browser.close();
}
