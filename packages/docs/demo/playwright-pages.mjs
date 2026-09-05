#!/usr/bin/env node
import fs from 'node:fs/promises';
import {createRequire} from 'node:module';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('PLAYWRIGHT_RUNTIME must name a directory containing the playwright package');
const require = createRequire(path.join(runtime, 'package.json'));
const {chromium} = require('playwright');

const base = process.env.DISPAT_DOCS_URL ?? 'http://127.0.0.1:3000';
const output = process.env.DISPAT_PAGES_OUTPUT ?? 'output/playwright/pages-mobile';
const widths = [320, 390, 768];
const pages = ['/', '/getting-started', '/cli/run', '/go/ccme', '/1.7/getting-started'];

await fs.mkdir(output, {recursive: true});
const browser = await chromium.launch({headless: true});
try {
  for (const width of widths) {
    for (const theme of ['light', 'dark']) {
      const page = await browser.newPage({viewport: {width, height: 900}, colorScheme: theme});
      for (const route of pages) {
        await page.goto(new URL(route, base).href, {waitUntil: 'networkidle'});
        await page.evaluate((value) => document.documentElement.dataset.theme = value, theme);
        const result = await page.evaluate(() => {
          const viewport = document.documentElement.clientWidth;
          const visible = (el) => {
            const r = el.getBoundingClientRect();
            return r.width > 0 && r.height > 0;
          };
          const outside = [...document.querySelectorAll('main p, main h1, main h2, main h3')]
            .filter(visible).filter((el) => {
              const r = el.getBoundingClientRect();
              return r.left < -0.5 || r.right > viewport + 0.5;
            }).map((el) => ({text: el.textContent?.trim().slice(0, 80), rect: el.getBoundingClientRect().toJSON()}));
          const blocks = [...document.querySelectorAll('.theme-code-block')].filter(visible).map((el) => {
            const r = el.getBoundingClientRect();
            const pre = el.querySelector('pre');
            return {left: r.left, right: r.right, width: r.width, preClient: pre?.clientWidth ?? 0, preScroll: pre?.scrollWidth ?? 0};
          });
          return {viewport, documentWidth: document.documentElement.scrollWidth, outside, blocks};
        });
        if (result.documentWidth > result.viewport || result.outside.length || result.blocks.some((b) => b.left < -0.5 || b.right > result.viewport + 0.5)) {
          throw new Error(`${route} ${width}px ${theme}: ${JSON.stringify(result)}`);
        }
        if (route === '/') {
          const block = page.getByText('Configure and preview', {exact: true}).locator('xpath=ancestor::*[contains(@class,"theme-code-block")]');
          await block.scrollIntoViewIfNeeded();
          const paragraph = page.getByText(/Start with a single package or a monorepo/);
          for (const locator of [block, paragraph]) {
            const box = await locator.boundingBox();
            if (!box || box.x < -0.5 || box.x + box.width > width + 0.5) throw new Error(`landing target overflow at ${width}px ${theme}`);
          }
          await page.screenshot({path: path.join(output, `landing-${width}-${theme}.png`), fullPage: true});
        }
      }
      await page.close();
    }
  }
  console.log(`mobile prose/code checks passed; screenshots: ${output}`);
} finally {
  await browser.close();
}
