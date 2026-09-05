import {createRequire} from 'node:module';
import assert from 'node:assert/strict';
import path from 'node:path';

const runtime = process.env.PLAYWRIGHT_RUNTIME;
if (!runtime) throw new Error('Set PLAYWRIGHT_RUNTIME to the directory containing node_modules/playwright');
const {chromium} = createRequire(path.join(runtime, 'package.json'))('playwright');
const base = process.argv[2] ?? 'http://127.0.0.1:3000/';
const browser = await chromium.launch();
const page = await browser.newPage({viewport: {width: 1440, height: 1000}});

async function anchoredStart() {
  await page.goto(new URL('getting-started/?anchor-test=1#installation', base).href, {
    waitUntil: 'networkidle',
  });
}

async function expectNavigation(locator, pathname, search = '') {
  await Promise.all([
    page.waitForURL((url) => url.pathname === pathname && url.hash === '' && url.search === search, {timeout: 5000}),
    locator.click(),
  ]);
  assert.equal(new URL(page.url()).hash, '', `${pathname} inherited the previous page anchor`);
}

try {
  await anchoredStart();
  await expectNavigation(page.locator('.theme-doc-sidebar-menu').getByRole('link', {name: 'Concepts', exact: true}), '/concepts/');

  await anchoredStart();
  await expectNavigation(page.locator('.theme-doc-sidebar-menu').getByRole('link', {name: 'Examples', exact: true}), '/examples/');

  await anchoredStart();
  await expectNavigation(page.locator('nav').getByRole('link', {name: 'Docs', exact: true}), '/getting-started/');

  await anchoredStart();
  await expectNavigation(page.locator('nav').getByRole('link', {name: 'API', exact: true}), '/api/');

  await anchoredStart();
  await expectNavigation(page.locator('a.navbar__brand'), '/');

  // The current and historical version entries target another rendered page.
  // Preserve the query string used by Docusaurus, but clear the old heading.
  await anchoredStart();
  await page.locator('.navbar__item.dropdown').filter({hasText: /^1\.8/}).hover();
  await expectNavigation(page.locator('.dropdown__menu a').filter({hasText: /^1\.8$/}), '/getting-started/', '?anchor-test=1');

  await anchoredStart();
  await page.locator('.navbar__item.dropdown').filter({hasText: /^1\.8/}).hover();
  await expectNavigation(page.locator('.dropdown__menu a').filter({hasText: /^1\.7$/}), '/1.7/getting-started/', '?anchor-test=1');

  // Authored cross-page targets and same-page anchors remain intentional.
  await page.goto(new URL('getting-started/', base).href, {waitUntil: 'networkidle'});
  await page.locator('#install a.hash-link').click();
  assert.equal(new URL(page.url()).hash, '#install');
  await page.goBack();
  assert.equal(new URL(page.url()).hash, '');
  await page.goForward();
  assert.equal(new URL(page.url()).hash, '#install', 'forward history lost the explicit anchor');

  await page.goto(new URL('getting-started/', base).href, {waitUntil: 'networkidle'});
  await page.getByRole('link', {name: 'a composite action', exact: true}).click();
  assert.equal(new URL(page.url()).pathname, '/reference/ci/');
  assert.equal(new URL(page.url()).hash, '#the-github-action', 'authored cross-page target was cleared');

  console.log('anchor navigation passed: page changes clear stale hashes; explicit anchors and history are preserved');
} finally {
  await browser.close();
}
