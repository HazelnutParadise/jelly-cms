#!/usr/bin/env node
// Browser acceptance for ticket 12 (docs/tickets/12-zero-cdn-jelly-ui.md), slot Z7. These checks pin visible
// behavior for Jelly UI controls, confirmations, notifications, settings, and posts filtering against a real
// server. This is a plain Node script rather than @playwright/test; Playwright loads through createRequire.
//
// Start a server (once), then run the script:
//
//   go build -o /tmp/jelly-server ./cmd/server
//   createdb ...                       # a dedicated empty database, never production data
//   DB_HOST=127.0.0.1 DB_PORT=55432 DB_USER=... DB_PASSWORD=... DB_NAME=jelly_ui_acceptance \
//     /tmp/jelly-server                # run from the repository root: it serves ./web
//   curl -s localhost:8080/api/install -H 'Content-Type: application/json' -H 'X-Jelly-CSRF: 1' -d '{
//     "db_host":"127.0.0.1","db_port":"55432","db_user":"...","db_pass":"...","db_name":"jelly_ui_acceptance",
//     "db_timezone":"UTC","site_title":"Jelly","admin_user":"admin","admin_email":"a@example.test",
//     "admin_pass":"correct-horse-battery"}'
//
//   JELLY_BASE_URL=http://127.0.0.1:8080 JELLY_ADMIN_USER=admin JELLY_ADMIN_PASSWORD=correct-horse-battery \
//     node scripts/acceptance/jelly-ui.spec.mjs
//
// Run it only against a dedicated fresh acceptance database: the checks create temporary fixtures and the
// shop settings interactions save through /api/admin/shop/settings. Never point it at production data.
// The design canvas check starts a static server on design/project and needs design/project/fonts/
// (run scripts/vendor/fonts.sh). JELLY_ONLY=admin|public|design|consistency runs one group; consistency is the
// ticket 26 group (docs/tickets/26-jelly-ui-consistency.md, slot 26-B). JELLY_SCREENSHOT_DIR enables
// the bounded 24-image locale/width/theme visual matrix. Exit code 1 on any failure.

import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const { chromium } = createRequire(import.meta.url)('playwright');

const here = path.dirname(fileURLToPath(import.meta.url));
const designDir = path.resolve(here, '..', '..', 'design', 'project');
const base = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const adminUser = process.env.JELLY_ADMIN_USER || 'admin';
const adminPassword = process.env.JELLY_ADMIN_PASSWORD;
const only = process.env.JELLY_ONLY || '';
const screenshotDir = process.env.JELLY_SCREENSHOT_DIR || '';
const baseOrigin = new URL(base).origin;

// Requests that are known to miss and are not part of this ticket: the browser asks for /favicon.ico (the
// admin has no favicon yet) and design boards ask for ./support.js (the canvas runtime provides it).
const knownMisses = [/\/favicon\.ico(\s|$)/, /\/support\.js(\s|$)/];

const results = [];
async function check(name, fn) {
  try {
    await fn();
    results.push({ name, ok: true });
    console.log(`PASS  ${name}`);
  } catch (error) {
    results.push({ name, ok: false, error });
    console.log(`FAIL  ${name}\n      ${String(error && error.message || error).split('\n').join('\n      ')}`);
  }
}
function assert(condition, message) { if (!condition) throw new Error(message); }

// A page whose every request outside the allowed origins is aborted and remembered. A request that fails
// for any reason, or answers 4xx/5xx, is recorded too: an offline page must have none.
async function openPage(context, allowedOrigins) {
  const page = await context.newPage();
  page.problems = [];
  await page.route('**/*', (route) => {
    const url = route.request().url();
    if (/^(data|blob|about):/.test(url) || allowedOrigins.some((origin) => url.startsWith(origin + '/') || url === origin)) {
      return route.continue();
    }
    page.problems.push(`blocked external request: ${url}`);
    return route.abort();
  });
  page.on('requestfailed', (request) => {
    if (!page.problems.some((p) => p.endsWith(request.url()))) page.problems.push(`request failed: ${request.url()} ${request.failure()?.errorText || ''}`);
  });
  page.on('response', (response) => {
    const url = response.url();
    // The page's own status is checked by the caller (a 404 page is expected to answer 404).
    if (response.request().resourceType() === 'document') return;
    if (response.status() >= 400 && !knownMisses.some((re) => re.test(url))) page.problems.push(`HTTP ${response.status()}: ${url}`);
  });
  page.on('pageerror', (error) => page.problems.push(`page error: ${error.message}`));
  return page;
}

function problemsOf(page) {
  return page.problems.filter((p) => !knownMisses.some((re) => re.test(p)));
}

async function fontsLoaded(page) {
  return page.evaluate(async () => {
    await Promise.all([
      document.fonts.load('16px "Noto Sans TC"', '中文'),
      document.fonts.load('800 16px "Bricolage Grotesque"', 'Jelly'),
    ]);
    await document.fonts.ready;
    return document.fonts.check('16px "Noto Sans TC"') && document.fonts.check('16px "Bricolage Grotesque"');
  });
}

async function settleVisualElement(page, locator, label) {
  await locator.evaluate(async (element) => {
    const animations = element.getAnimations({ subtree: true }).filter((animation) =>
      animation.playState === 'running' && animation.effect?.getComputedTiming().iterations !== Infinity);
    await Promise.all(animations.map((animation) => animation.finished.catch(() => {})));
  });
  let previous = null;
  let stableFrames = 0;
  for (let frame = 0; frame < 12; frame++) {
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(resolve)));
    const bounds = await locator.boundingBox();
    if (!bounds) throw new Error(`${label} is not visible while waiting for stable layout`);
    const stable = previous && ['x', 'y', 'width', 'height'].every((key) => Math.abs(bounds[key] - previous[key]) <= 0.25);
    stableFrames = stable ? stableFrames + 1 : 0;
    if (stableFrames >= 2) return bounds;
    previous = bounds;
  }
  throw new Error(`${label} bounds did not stabilize after its finite entry animations: ${JSON.stringify(previous)}`);
}

async function login(context) {
  const page = await openPage(context, [baseOrigin]);
  await page.goto(`${base}/login`);
  await page.fill('#loginUsername', adminUser);
  await page.fill('#loginPassword', adminPassword);
  await Promise.all([page.waitForURL(/\/admin/, { timeout: 15000 }), page.click('#localLoginForm button[type=submit]')]);
  await page.close();
}

async function captureVisualMatrixPage(context, combination, kind, runId) {
  const { locale, width, theme } = combination;
  const route = kind === 'settings' ? '/admin/settings/payment' : kind === 'posts' ? '/admin/posts' : '/admin/trash';
  const file = `${runId}-${locale}-${width}-${theme}-${kind}.png`;
  const record = {
    locale, width, theme, page: kind, url: `${base}${route}?lang=${encodeURIComponent(locale)}`,
    screenshot: path.join(screenshotDir, file), console: { errors: [], warnings: [] },
    externalLoads: [], failedRequests: [], mutations: [], unexpectedErrors: [],
  };
  const page = await openPage(context, [baseOrigin]);
  page.on('console', (message) => {
    const entry = { message: message.text(), url: message.location().url || '' };
    if (message.type() === 'error') record.console.errors.push(entry);
    else if (message.type() === 'warning') record.console.warnings.push(entry);
  });
  page.on('request', (request) => {
    if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method())) {
      record.mutations.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  try {
    await page.setViewportSize({ width, height: 800 });
    // Screenshot references should capture the settled layout. Core interaction
    // cases still run with normal motion, while this matrix honors the site's
    // reduced-motion path so WAAPI and CSS/canvas-driven transitions cannot
    // leave transient geometry in the saved reference.
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await page.goto(record.url, { waitUntil: 'networkidle' });
    await page.evaluate((mode) => document.documentElement.setAttribute('data-theme', mode), theme);
    await page.waitForFunction((mode) => document.documentElement.getAttribute('data-jelly-mode') === mode, theme, { timeout: 5000 });
    record.fontsLoaded = await fontsLoaded(page);

    if (kind === 'settings') {
      await jellyReady(page, 'jelly-switch', 'jelly-segmented');
      const toggle = page.locator('#ecpayEnabled');
      const environment = page.locator('#ecpayTestMode');
      await toggle.waitFor({ state: 'visible', timeout: 10000 });
      await environment.waitFor({ state: 'visible', timeout: 10000 });
      const expectedEnvironment = locale === 'en'
        ? { label: 'Environment', options: ['Test', 'Live'] }
        : { label: '環境', options: ['測試', '正式'] };
      record.actualLabels = await page.evaluate(() => ({
        switches: ['ecpayEnabled', 'newebpayEnabled'].map((id) => {
          const el = document.getElementById(id);
          return { id, label: el?.getAttribute('label') || '', checked: el?.hasAttribute('checked') || false };
        }),
        environments: ['ecpayTestMode', 'newebpayTestMode'].map((id) => {
          const el = document.getElementById(id);
          return {
            id,
            label: el?.getAttribute('label') || '',
            selected: el?.getAttribute('value') || '',
            options: [...(el?.querySelectorAll('jelly-segment') || [])].map((option) => option.innerText.trim()),
          };
        }),
      }));
      assert(record.actualLabels.switches.every((item) => item.label) &&
        record.actualLabels.environments.length === 2 && record.actualLabels.environments.every((item) =>
          item.label === expectedEnvironment.label && JSON.stringify(item.options) === JSON.stringify(expectedEnvironment.options)),
      `payment environment labels for ${locale} do not match ${JSON.stringify(expectedEnvironment)}: ${JSON.stringify(record.actualLabels)}`);
      await toggle.focus();
      await settleVisualElement(page, toggle, 'payment switch');
    } else if (kind === 'posts') {
      await jellyReady(page, 'jelly-segmented', 'jelly-toaster');
      const filter = page.locator('#postStatus');
      await filter.waitFor({ state: 'visible', timeout: 10000 });
      const draft = filter.locator('button[role="radio"]', { hasText: /draft|草稿/i });
      await draft.click();
      assert(await filter.getAttribute('value') === 'draft', `posts filter is ${await filter.getAttribute('value')}, want draft`);
      await settleVisualElement(page, filter, 'posts status filter');
      record.actualLabels = await page.evaluate(() => ({
        filter: document.getElementById('postStatus')?.getAttribute('label') || '',
        options: [...(document.getElementById('postStatus')?.querySelectorAll('jelly-segment') || [])].map((el) => el.innerText.trim()),
      }));
      assert(record.actualLabels.filter && record.actualLabels.options.length === 4 && record.actualLabels.options.every(Boolean),
        `posts status filter labels are incomplete: ${JSON.stringify(record.actualLabels)}`);
      await page.evaluate((locale) => {
        const message = locale === 'en' ? 'Visual review preview' : '畫面檢查預覽';
        const detail = locale === 'en' ? 'Synthetic notification with a long unbroken identifier ' : '測試通知與長檔名 ';
        const longText = `LongUnbrokenIdentifier_${'0123456789abcdef'.repeat(12)}`;
        window.Jelly.toast(message, {
          kind: 'error', detail: `${detail}${longText}`,
          action: { label: locale === 'en' ? 'Retry' : '重試', onClick: () => {} },
        });
      }, locale);
      const toast = page.locator('jelly-toaster .toast').filter({ hasText: /Visual review preview|畫面檢查預覽/ });
      await toast.waitFor({ state: 'visible', timeout: 5000 });
      await settleVisualElement(page, toast, 'visual matrix toast');
      const buttons = await toast.locator('button').all();
      assert(buttons.length >= 2, `the real toast should show action and close buttons, found ${buttons.length}`);
      for (const [index, button] of buttons.entries()) {
        await settleVisualElement(page, button, `visual matrix toast button ${index + 1}`);
        assert(await button.isVisible(), 'the toast action and close buttons must remain visible');
        const bounds = await button.boundingBox();
        assert(bounds && bounds.x >= 0 && bounds.x + bounds.width <= width,
          `toast button extends outside the viewport: ${JSON.stringify(bounds)} at ${width}px`);
      }
      record.actualLabels.toast = {
        message: await toast.locator('b').innerText(),
        detail: await toast.locator('.toast-text span').innerText(),
        buttons: await Promise.all(buttons.map(async (button) => ({
          text: (await button.innerText()).trim(), ariaLabel: await button.getAttribute('aria-label'),
        }))),
      };
    } else {
      await jellyReady(page, 'jelly-dialog');
      record.actualLabels = await page.evaluate((locale) => {
        const confirmText = locale === 'en' ? 'Delete permanently' : '永久刪除';
        const cancelText = locale === 'en' ? 'Cancel' : '取消';
        const typeWord = 'DELETE';
        window.__visualConfirmResult = 'pending';
        window.Jelly.confirm({
          title: locale === 'en' ? 'Delete this item permanently?' : '永久刪除這個項目？',
          message: locale === 'en' ? `Type ${typeWord} to continue. This is a visual preview only.` : `輸入 ${typeWord} 繼續。這只是畫面預覽。`,
          confirmLabel: confirmText, cancelLabel: cancelText, tone: 'danger', requireText: typeWord,
        }).then((value) => { window.__visualConfirmResult = value; });
        return { confirm: confirmText, cancel: cancelText, requiredText: typeWord };
      }, locale);
      const dialog = page.locator('jelly-dialog[open][data-jelly-confirm]');
      await dialog.waitFor({ state: 'visible', timeout: 5000 });
      const input = dialog.locator('input[data-jelly-confirm-input]');
      await input.waitFor({ state: 'visible', timeout: 5000 });
      await input.fill('DELETE');
      assert(await dialog.locator('button[data-jelly-confirm]').isEnabled(), 'typing the required confirmation text should enable the real confirm button');
      await dialog.locator('button[data-jelly-cancel]').waitFor({ state: 'visible' });
      await settleVisualElement(page, dialog, 'typed confirmation dialog');
      const dialogButtons = await dialog.locator('button').all();
      record.actualLabels.dialog = {
        title: (await dialog.locator('h2').innerText()).trim(),
        message: (await dialog.locator('p').innerText()).trim(),
        input: await input.getAttribute('aria-label'),
        typedValue: await input.inputValue(),
        buttons: await Promise.all(dialogButtons.map(async (button) => ({
          text: (await button.innerText()).trim(), ariaLabel: await button.getAttribute('aria-label'),
        }))),
      };
    }

    record.pageIdentity = await page.evaluate(() => ({
      title: document.title,
      heading: document.querySelector('main h1, h1')?.innerText?.trim() || '',
      path: location.pathname,
      lang: document.documentElement.lang,
      theme: document.documentElement.getAttribute('data-jelly-mode'),
    }));
    assert(record.pageIdentity.lang === locale, `page lang is ${record.pageIdentity.lang}, want ${locale}`);
    assert(record.pageIdentity.theme === theme, `page theme is ${record.pageIdentity.theme}, want ${theme}`);
    record.computedFonts = await page.evaluate(() => ({
      heading: getComputedStyle(document.querySelector('main h1, h1') || document.body).fontFamily,
      control: getComputedStyle(document.querySelector('#ecpayEnabled, #postStatus, jelly-dialog[open]') || document.body).fontFamily,
      loaded: [...document.fonts].filter((font) => font.status === 'loaded').map((font) => font.family),
    }));
    record.focus = await page.evaluate(() => {
      const path = [];
      let el = document.activeElement;
      while (el) {
        path.push({ tag: el.tagName.toLowerCase(), id: el.id || '', role: el.getAttribute('role') || '', ariaLabel: el.getAttribute('aria-label') || '' });
        el = el.shadowRoot?.activeElement || null;
      }
      return path;
    });
    record.metrics = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentClientWidth: document.documentElement.clientWidth,
      documentScrollWidth: document.documentElement.scrollWidth,
      bodyClientWidth: document.body.clientWidth,
      bodyScrollWidth: document.body.scrollWidth,
      documentOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
      bodyOverflow: document.body.scrollWidth > document.body.clientWidth,
    }));
    if (record.metrics.documentOverflow || record.metrics.bodyOverflow) {
      record.unexpectedErrors.push(`horizontal overflow: ${JSON.stringify(record.metrics)}`);
    }
    assert(record.fontsLoaded, 'self-hosted Noto Sans TC and Bricolage Grotesque must load');
    await page.screenshot({ path: record.screenshot, fullPage: kind === 'settings' });
    record.screenshotCreated = true;
    if (kind === 'trash') {
      await page.locator('jelly-dialog[open] button[data-jelly-cancel]').click();
      await page.waitForFunction(() => window.__visualConfirmResult === false, null, { timeout: 5000 });
    }
  } catch (error) {
    record.unexpectedErrors.push(String(error?.message || error));
    try {
      await page.screenshot({ path: record.screenshot, fullPage: kind === 'settings' });
      record.screenshotCreated = true;
    } catch (screenshotError) {
      record.unexpectedErrors.push(`screenshot failed: ${String(screenshotError?.message || screenshotError)}`);
    }
  } finally {
    record.externalLoads = page.problems.filter((problem) => problem.startsWith('blocked external request:'));
    record.failedRequests = problemsOf(page);
    record.unexpectedErrors.push(...record.console.errors
      .filter((entry) => !knownMisses.some((re) => re.test(`${entry.url} ${entry.message}`)))
      .map((entry) => `console error: ${entry.message} (${entry.url})`));
    record.unexpectedErrors.push(...record.failedRequests);
    if (record.mutations.length) record.unexpectedErrors.push(`unexpected state-changing requests: ${record.mutations.join(', ')}`);
    record.ok = record.unexpectedErrors.length === 0 && record.externalLoads.length === 0 && record.mutations.length === 0;
    await page.close();
  }
  return record;
}

async function runVisualScreenshotMatrix(context) {
  if (!screenshotDir) return;
  assert(path.isAbsolute(screenshotDir), 'JELLY_SCREENSHOT_DIR must be an absolute path');
  fs.mkdirSync(screenshotDir, { recursive: true });
  const runId = new Date().toISOString().replace(/[:.]/g, '-');
  const combinations = [];
  for (const locale of ['zh-TW', 'en']) for (const width of [390, 1280]) for (const theme of ['light', 'dark']) {
    combinations.push({ locale, width, theme });
  }
  const records = [];
  for (const combination of combinations) {
    for (const kind of ['settings', 'posts', 'trash']) {
      records.push(await captureVisualMatrixPage(context, combination, kind, runId));
    }
  }
  const summary = {
    generatedAt: new Date().toISOString(),
    base,
    screenshotDirectory: screenshotDir,
    expectedScreenshots: 24,
    screenshotsCreated: records.filter((record) => record.screenshotCreated).length,
    records,
  };
  const summaryPath = path.join(screenshotDir, `${runId}-summary.json`);
  fs.writeFileSync(summaryPath, `${JSON.stringify(summary, null, 2)}\n`, { flag: 'wx' });
  console.log(`VISUAL_MATRIX ${JSON.stringify({ summaryPath, screenshotsCreated: summary.screenshotsCreated, records: records.map(({ locale, width, theme, page, ok, unexpectedErrors }) => ({ locale, width, theme, page, ok, unexpectedErrors })) })}`);
  assert(summary.screenshotsCreated === 24, `visual matrix created ${summary.screenshotsCreated}/24 screenshots; see ${summaryPath}`);
  assert(records.every((record) => record.ok), `visual matrix failures; see ${summaryPath}`);
}

// Waits until jelly.js has defined the elements, so property reads see the upgraded class.
async function jellyReady(page, ...tags) {
  const names = tags.length ? tags : ['jelly-toaster'];
  try {
    await page.waitForFunction((list) => list.every((name) => customElements.get(name)), names, { timeout: 10000 });
  } catch {
    throw new Error(`jelly.js did not define <${names.join('>, <')}> within 10 s on ${page.url()}: it is not loaded, or it failed (${page.problems.join('; ') || 'no request problem recorded'})`);
  }
}

// Opens a page and returns its HTTP status; the caller decides which status is right.
async function visit(page, url, options = {}) {
  const response = await page.goto(url, { waitUntil: 'networkidle', ...options });
  return response ? response.status() : 0;
}

const adminPaths = ['/admin', '/admin/posts', '/admin/posts/new', '/admin/pages', '/admin/pages/new', '/admin/media',
  '/admin/products', '/admin/orders', '/admin/themes', '/admin/plugins', '/admin/settings', '/admin/taxonomy',
  '/admin/menus', '/admin/comments', '/admin/trash', '/account'];
const publicPaths = ['/', '/posts', '/search?q=a', '/login', '/no-such-page-for-jelly-ui'];

async function runAdmin(browser) {
  if (!adminPassword) throw new Error('set JELLY_ADMIN_PASSWORD (and JELLY_ADMIN_USER) for the admin checks');
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await login(context);

  await check('admin: every page loads offline with no failed request and the self-hosted fonts', async () => {
    const failures = [];
    // The sidebar links are walked too, so a page added later is covered without editing this list.
    const seed = await openPage(context, [baseOrigin]);
    await seed.goto(`${base}/admin`);
    const nav = await seed.$$eval('a[href^="/admin"]', (links) => links.map((a) => a.getAttribute('href').split('#')[0]));
    await seed.close();
    const listed = new Set(adminPaths);
    const walked = [...new Set([...adminPaths, ...nav])].filter((p) => !/\/:|\/\d+$/.test(p));
    for (const p of walked) {
      const page = await openPage(context, [baseOrigin]);
      const status = await visit(page, base + p);
      if (status !== 200) {
        // A sidebar entry whose page does not exist is a separate defect; only the listed pages must answer 200.
        if (listed.has(p)) failures.push(`${p}: HTTP ${status}, want 200`);
        else console.log(`WARN  sidebar link ${p} answers HTTP ${status}; skipped`);
        await page.close();
        continue;
      }
      if (!(await fontsLoaded(page))) failures.push(`${p}: document.fonts.check for Noto Sans TC / Bricolage Grotesque is false`);
      for (const problem of problemsOf(page)) failures.push(`${p}: ${problem}`);
      await page.close();
    }
    assert(failures.length === 0, failures.join('\n'));
  });

  await check('toast: Jelly.toast renders a Jelly UI toast and keeps message, detail, action and kind', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-toaster', 'jelly-switch', 'jelly-dialog');
    await page.evaluate(() => { window.__clicked = 0; Jelly.toast('Saved ok', { kind: 'success' });
      window.__failed = Jelly.toast('Save failed', { kind: 'error', detail: 'link down', action: { label: 'Retry now', onClick: () => { window.__clicked++; } } }); });
    const toasts = page.locator('jelly-toaster .toast');
    assert(await toasts.count() === 2, `expected 2 toasts inside <jelly-toaster>, found ${await toasts.count()}`);
    const first = await toasts.nth(0).innerText();
    assert(first.includes('Saved ok'), `first toast text was ${JSON.stringify(first)}`);
    const second = await toasts.nth(1).innerText();
    assert(second.includes('Save failed') && second.includes('link down'), `error toast text was ${JSON.stringify(second)}`);
    assert(await page.evaluate(() => typeof window.__failed.dismiss === 'function' && window.__failed.element instanceof HTMLElement), 'Jelly.toast must return {dismiss(), element}');
    assert(await page.evaluate(() => document.querySelectorAll('.toasts, .toast-dot').length === 0), 'the old hand-made toast container is still in the page');
    const toastRadii = await page.evaluate(() => {
      const style = getComputedStyle(window.__failed.element);
      return [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius];
    });
    assert(JSON.stringify(toastRadii) === JSON.stringify(['0px', '12px', '0px', '12px']),
      `toast corners should follow the asymmetric brand radius, got ${JSON.stringify(toastRadii)}`);
    await page.locator('jelly-toaster .toast button:has-text("Retry now")').click();
    assert(await page.evaluate(() => window.__clicked) === 1, 'the toast action onClick did not run exactly once');
    // the success toast leaves by itself; an error toast stays until dismissed
    await page.waitForTimeout(4500);
    const left = await page.locator('jelly-toaster .toast').allInnerTexts();
    assert(!left.some((t) => t.includes('Saved ok')), 'the success toast did not auto-dismiss');
    await page.evaluate(() => Jelly.toast('Stays', { kind: 'error' }));
    await page.waitForTimeout(4500);
    assert((await page.locator('jelly-toaster .toast').allInnerTexts()).some((t) => t.includes('Stays')), 'an error toast must not auto-dismiss');
    assert(await page.evaluate(() => typeof window.jellyToast === 'function'), 'jellyToast() is not available as a global');
    await page.close();
  });

  await check('toast: a call before jelly.js has loaded is queued and shown', async () => {
    const page = await openPage(context, [baseOrigin]);
    // Hold jelly.js back so the call below is certain to happen before the module has run. The server-side
    // "toast" template and early page scripts call Jelly.toast while the page is still parsing.
    await page.route(/\/admin\/assets\/jelly-ui\/jelly\.js(\?|$)/, async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
    await page.goto(`${base}/admin`, { waitUntil: 'commit' });
    await page.waitForFunction(() => window.Jelly && typeof window.Jelly.toast === 'function', null, { timeout: 10000 });
    const early = await page.evaluate(() => { const early = !customElements.get('jelly-toaster'); window.Jelly.toast('Queued early', { kind: 'info' }); return early; });
    assert(early, 'jelly.js had already run when the early call was made; the test could not exercise the queue');
    await jellyReady(page, 'jelly-toaster');
    await page.waitForFunction(() => {
      const toaster = document.querySelector('jelly-toaster');
      return [...(toaster?.shadowRoot?.querySelectorAll('.toast') || [])].some((toast) => toast.textContent.includes('Queued early'));
    }, null, { timeout: 5000 });
    await page.close();
  });

  await check('dialog: Jelly.confirm opens a jelly-dialog; Escape resolves false, Enter on the default button resolves true', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-dialog');
    const ask = (options) => page.evaluate((o) => { window.__r = 'pending'; Jelly.confirm(o).then((v) => { window.__r = v; }); }, options);
    const result = () => page.evaluate(() => window.__r);
    const dialog = page.locator('jelly-dialog[open]');

    await ask({ title: 'Publish this?', message: 'It goes live now.', confirmLabel: 'Publish', cancelLabel: 'Not yet', tone: 'primary' });
    await dialog.waitFor({ state: 'visible', timeout: 5000 });
    const text = await dialog.innerText();
    assert(text.includes('Publish this?') && text.includes('It goes live now.'), `dialog text was ${JSON.stringify(text)}`);
    assert(await page.locator('jelly-dialog[open] button[data-jelly-confirm]').innerText() === 'Publish', 'confirm button label');
    assert(await page.locator('jelly-dialog[open] button[data-jelly-cancel]').innerText() === 'Not yet', 'cancel button label');
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await result() === false, `Escape resolved ${await result()}, want false`);
    await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]') && !document.querySelector('jelly-dialog[data-jelly-confirm]'), null, { timeout: 5000 });

    await ask({ title: 'Publish this?', confirmLabel: 'Publish', tone: 'primary' });
    await dialog.waitFor({ state: 'visible', timeout: 5000 });
    assert(await page.evaluate(() => document.activeElement && document.activeElement.matches('[data-jelly-confirm]')), 'a primary confirm focuses the confirm button, so Enter confirms');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await result() === true, `Enter resolved ${await result()}, want true`);
    await page.close();
  });

  await check('dialog: a danger confirm focuses Cancel; the cancel button, the backdrop and the close button resolve false', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-dialog');
    const ask = () => page.evaluate(() => { window.__r = 'pending'; Jelly.confirm({ title: 'Delete?', confirmLabel: 'Delete', tone: 'danger' }).then((v) => { window.__r = v; }); });
    await ask();
    await page.locator('jelly-dialog[open]').waitFor({ state: 'visible', timeout: 5000 });
    assert(await page.evaluate(() => document.activeElement && document.activeElement.matches('[data-jelly-cancel]')), 'a danger confirm must focus Cancel');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__r) === false, 'Enter on the focused Cancel must resolve false');
    await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]'), null, { timeout: 5000 });
    await ask();
    await page.locator('jelly-dialog[open] button[data-jelly-confirm]').click();
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__r) === true, 'clicking the confirm button must resolve true');
    await page.close();
  });

  await check('dialog: requireText trims surrounding whitespace before enabling confirmation', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-dialog');
    await page.evaluate(() => { window.__r = 'pending'; Jelly.confirm({ title: 'Delete forever?', confirmLabel: 'Delete', tone: 'danger', requireText: 'DELETE' }).then((v) => { window.__r = v; }); });
    const confirm = page.locator('jelly-dialog[open] button[data-jelly-confirm]');
    const input = page.locator('jelly-dialog[open] input[data-jelly-confirm-input]');
    await input.waitFor({ state: 'visible', timeout: 5000 });
    assert(await confirm.isDisabled(), 'confirm must start disabled');
    assert(await page.evaluate(() => document.activeElement && document.activeElement.matches('[data-jelly-confirm-input]')), 'the word input takes the focus');
    await input.fill('delet');
    await page.keyboard.press('Enter');
    assert(await page.evaluate(() => window.__r) === 'pending', 'Enter with the wrong word must not resolve');
    await input.fill(' DELETE ');
    assert(await confirm.isEnabled(), 'surrounding whitespace must be trimmed before comparing the required word');
    await confirm.click();
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__r) === true, 'the trimmed required word must resolve true');
    await page.close();
  });

  await check('dialog: cancellation restores focus and concurrent confirmations resolve independently', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-dialog');
    await page.evaluate(() => {
      const trigger = document.createElement('button');
      trigger.id = 'confirm-focus-origin'; trigger.textContent = 'Open confirmations'; document.body.append(trigger); trigger.focus();
      window.__confirmResults = [];
      window.__firstConfirm = Jelly.confirm({ title: 'First request', tone: 'primary' }).then((v) => window.__confirmResults.push(['first', v]));
      window.__secondConfirm = Jelly.confirm({ title: 'Second request', tone: 'primary' }).then((v) => window.__confirmResults.push(['second', v]));
    });
    await page.waitForFunction(() => document.querySelectorAll('jelly-dialog[open][data-jelly-confirm]').length === 1, null, { timeout: 5000 });
    const first = page.locator('jelly-dialog[open][data-jelly-confirm]').filter({ hasText: 'First request' });
    const firstText = await page.locator('jelly-dialog[open][data-jelly-confirm]').innerText();
    assert(firstText.includes('First request'), 'the first concurrent request must be shown first');
    await first.locator('button[data-jelly-cancel]').click();
    await page.waitForFunction(() => window.__confirmResults.some(([name]) => name === 'first'), null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__confirmResults.find(([name]) => name === 'first')[1]) === false, 'cancelling the first request must resolve only it as false');
    const second = page.locator('jelly-dialog[open][data-jelly-confirm]').filter({ hasText: 'Second request' });
    await second.waitFor({ state: 'visible', timeout: 5000 });
    await second.locator('button[data-jelly-confirm]').click();
    await page.waitForFunction(() => window.__confirmResults.length === 2, null, { timeout: 5000 });
    assert(JSON.stringify(await page.evaluate(() => window.__confirmResults)) === JSON.stringify([['first', false], ['second', true]]), 'the concurrent requests did not resolve independently in request order');
    assert(await page.evaluate(() => !document.querySelector('jelly-dialog[data-jelly-confirm]')), 'resolved confirmations must be removed from the document');

    await page.evaluate(() => { window.__cancelResult = Jelly.confirm({ title: 'Cancel for focus', tone: 'primary' }); });
    await page.locator('jelly-dialog[open][data-jelly-confirm]').waitFor({ state: 'visible', timeout: 5000 });
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('jelly-dialog[data-jelly-confirm]'), null, { timeout: 5000 });
    assert(await page.evaluate(() => document.activeElement && document.activeElement.id === 'confirm-focus-origin'), 'closing a confirmation must restore focus to the element that opened it');
    assert(await page.evaluate(async () => await window.__cancelResult) === false, 'Escape must resolve false');
    await page.close();
  });

  await check('dialog: a confirmation requested before jelly-dialog upgrades still opens and resolves', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.route(/\/admin\/assets\/jelly-ui\/jelly\.js(\?|$)/, async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
    await page.goto(`${base}/admin`, { waitUntil: 'commit' });
    await page.waitForFunction(() => window.Jelly && typeof window.Jelly.confirm === 'function', null, { timeout: 10000 });
    const beforeUpgrade = await page.evaluate(() => {
      const early = !customElements.get('jelly-dialog');
      window.__earlyConfirm = 'pending';
      Jelly.confirm({ title: 'Early confirmation', tone: 'primary' }).then((value) => { window.__earlyConfirm = value; });
      return early;
    });
    assert(beforeUpgrade, 'Jelly.confirm was not called before jelly-dialog upgraded');
    await jellyReady(page, 'jelly-dialog');
    await page.locator('jelly-dialog[open][data-jelly-confirm]').waitFor({ state: 'visible', timeout: 5000 });
    await page.locator('jelly-dialog[open][data-jelly-confirm] button[data-jelly-confirm]').click();
    await page.waitForFunction(() => window.__earlyConfirm !== 'pending', null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__earlyConfirm) === true, 'the early confirmation did not resolve true after upgrade');
    await page.close();
  });

  await check('toast: dismissing a queued toast prevents it from appearing after Jelly UI loads', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.route(/\/admin\/assets\/jelly-ui\/jelly\.js(\?|$)/, async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
    await page.goto(`${base}/admin`, { waitUntil: 'commit' });
    await page.waitForFunction(() => window.Jelly && typeof window.Jelly.toast === 'function', null, { timeout: 10000 });
    const early = await page.evaluate(() => {
      const handle = Jelly.toast('Dismiss before load', { kind: 'info' });
      const beforeUpgrade = !customElements.get('jelly-toaster');
      if (typeof handle.dismiss === 'function') handle.dismiss();
      return { beforeUpgrade, canDismiss: typeof handle.dismiss === 'function' };
    });
    assert(early.beforeUpgrade, 'the toast call must occur before jelly-toaster upgrades');
    assert(early.canDismiss, 'a queued toast must provide a dismissal action');
    await jellyReady(page, 'jelly-toaster');
    await page.waitForTimeout(500);
    assert(!(await page.locator('jelly-toaster .toast').allInnerTexts()).some((t) => t.includes('Dismiss before load')), 'a dismissed queued toast was shown after load');
    await page.close();
  });

  await check('toast: the visible queue is capped at four and pointer hover pauses its timer', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-toaster');
    await page.evaluate(() => {
      window.__queueHandles = [];
      for (let i = 0; i < 5; i++) window.__queueHandles.push(Jelly.toast(`Queue limit ${i}`, { kind: 'error' }));
    });
    assert(await page.locator('jelly-toaster .toast').count() === 4, 'no more than four notifications should be visible at once');
    await page.evaluate(() => window.__queueHandles.forEach((handle) => handle.dismiss()));
    await page.waitForFunction(() => document.querySelector('jelly-toaster')?.shadowRoot?.querySelectorAll('.toast').length === 0, null, { timeout: 5000 });
    const replacement = await page.evaluate(() => {
      const toaster = document.querySelector('jelly-toaster');
      window.__replacementHandles = [];
      for (let i = 0; i < 4; i++) window.__replacementHandles.push(Jelly.toast(`Replacement queue ${i}`, { kind: 'error' }));
      const before = toaster.shadowRoot.querySelectorAll('.toast').length;
      window.__replacementHandles[0].dismiss();
      window.__replacementHandles.push(Jelly.toast('Replacement queue newest', { kind: 'error' }));
      return { before, immediate: toaster.shadowRoot.querySelectorAll('.toast').length };
    });
    assert(replacement.before === 4, `replacement edge fixture started with ${replacement.before} visible toasts, want 4`);
    assert(replacement.immediate <= 4, `manual close followed by an immediate toast briefly rendered ${replacement.immediate} entries, over the four-toast limit`);
    await page.evaluate(() => window.__replacementHandles.forEach((handle) => handle.dismiss()));
    await page.waitForFunction(() => document.querySelector('jelly-toaster')?.shadowRoot?.querySelectorAll('.toast').length === 0, null, { timeout: 5000 });

    await page.evaluate(() => Jelly.toast('Pause on hover', { kind: 'success' }));
    const toast = page.locator('jelly-toaster .toast').filter({ hasText: 'Pause on hover' });
    await toast.waitFor({ state: 'visible', timeout: 5000 });
    await toast.hover();
    await toast.locator('button').last().focus();
    await page.mouse.move(0, 0);
    await page.waitForTimeout(4500);
    assert(await toast.isVisible(), 'a success toast disappeared while keyboard focus remained inside it after pointer exit');
    await page.locator('#paletteTrigger').focus();
    await toast.waitFor({ state: 'hidden', timeout: 5500 });

    await page.setViewportSize({ width: 390, height: 800 });
    await page.evaluate(() => Jelly.toast('Retry preview', {
      kind: 'error',
      detail: `https://example.invalid/${'unbrokenfilename0123456789'.repeat(12)}`,
      action: { label: 'Retry now', onClick: () => {} },
    }));
    const longToast = page.locator('jelly-toaster .toast').filter({ hasText: 'Retry preview' });
    await longToast.waitFor({ state: 'visible', timeout: 5000 });
    await settleVisualElement(page, longToast, 'long mobile toast');
    const longToastLayout = await longToast.evaluate((el) => ({
      toast: (() => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right }; })(),
      buttons: [...el.querySelectorAll('button')].map((button) => {
        const r = button.getBoundingClientRect();
        return { text: button.innerText, left: r.left, right: r.right, visible: r.width > 0 && r.height > 0 };
      }),
      viewportWidth: document.documentElement.clientWidth,
      documentWidth: document.documentElement.scrollWidth,
      bodyWidth: document.body.scrollWidth,
    }));
    assert(longToastLayout.toast.left >= 0 && longToastLayout.toast.right <= 390,
      `a long toast must stay within the mobile viewport: ${JSON.stringify(longToastLayout)}`);
    assert(longToastLayout.buttons.length >= 2 && longToastLayout.buttons.every((button) =>
      button.visible && button.left >= 0 && button.right <= 390),
    `the toast action and close controls must remain visible: ${JSON.stringify(longToastLayout)}`);
    assert(longToastLayout.documentWidth <= longToastLayout.viewportWidth && longToastLayout.bodyWidth <= longToastLayout.viewportWidth,
      `a long toast must not create horizontal page overflow: ${JSON.stringify(longToastLayout)}`);
    await page.close();
  });

  await check('switch: a jelly-switch toggles with Space, keeps its form value, and the page reads it as a boolean', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin/settings`);
    await jellyReady(page, 'jelly-switch');
    const tab = page.locator('.tabs .tab', { hasText: 'OAuth' });
    await tab.first().click();
    await page.waitForSelector('#googleEnabled', { state: 'visible', timeout: 5000 });
    const on = () => page.evaluate(() => document.getElementById('googleEnabled').hasAttribute('checked'));
    const formValue = () => page.evaluate(() => new FormData(document.getElementById('googleForm')).get('google_enabled'));
    const before = await on();
    await page.focus('#googleEnabled');
    await page.keyboard.press('Space');
    assert(await on() === !before, `Space did not toggle the switch (was ${before})`);
    assert(await page.evaluate(() => document.getElementById('googleEnabled').checked) === !before, 'the .checked property disagrees with the attribute');
    assert(await formValue() === (before ? null : 'on'), `FormData google_enabled = ${await formValue()} after toggling`);
    await page.keyboard.press('Space');
    assert(await on() === before, 'a second Space did not toggle back');
    assert(await formValue() === (before ? 'on' : null), 'FormData after toggling back');
    // the page's own change handler must still run: turning the provider on enables its text fields
    await page.keyboard.press('Space');
    const enabled = await on();
    const disabledFields = await page.evaluate(() => [...document.querySelectorAll('#googleForm input[type=text]')].map((i) => i.disabled));
    assert(disabledFields.every((d) => d === !enabled), `provider fields disabled=${disabledFields} while the switch is ${enabled ? 'on' : 'off'}`);
    await page.close();
  });

  await check('switch: every boolean setting in the system settings tabs is a Jelly switch in both locales', async () => {
    const settings = [
      { page: 'routes', keys: ['enabled', 'on_commerce'] },
      { page: 'crawlers', keys: ['seo_index_site', 'seo_images_index', 'seo_indexnow_enabled', 'webmcp_enabled', 'ai_block_server_side', 'tdm_reservation', 'llms_txt_enabled'] },
      { page: 'comments', keys: ['comments_enabled', 'comments_registered_only', 'comments_require_approval'] },
      { page: 'mail', keys: ['mail_enabled'] },
      { page: 'infra', keys: ['object_enabled'] },
      { page: 'update', keys: ['auto_check'] },
    ];
    for (const locale of ['zh-TW', 'en']) {
      for (const item of settings) {
        const page = await openPage(context, [baseOrigin]);
        await page.goto(`${base}/admin/settings/${item.page}?lang=${encodeURIComponent(locale)}`);
        await page.waitForFunction(() => {
          const root = document.querySelector('#systemRoot');
          return root && root.getAttribute('aria-busy') === 'false';
        }, null, { timeout: 15000 });
        assert(await page.locator('html').getAttribute('lang') === locale, `requested ${locale} but the settings page did not render that locale`);
        for (const key of item.keys) {
          const control = page.locator(`#systemRoot jelly-switch[name="${key}"]`);
          await control.waitFor({ state: 'attached', timeout: 5000 });
          assert(await control.count() === 1, `${item.page}/${locale}: expected one Jelly switch for ${key}`);
          const lightDOMCheckboxes = await page.locator('#systemRoot').evaluate((root) =>
            [...root.querySelectorAll('input[type="checkbox"]')].map((input) => input.name));
          assert(!lightDOMCheckboxes.includes(key), `${item.page}/${locale}: ${key} is still a page-level native checkbox`);
          const label = await control.evaluate((el) => el.closest('.sys-row')?.querySelector('label')?.textContent.trim());
          assert(label, `${item.page}/${locale}: ${key} has no translated visible label`);
        }
        if (item.page === 'crawlers') {
          const translation = await page.evaluate(() => JSON.parse(document.querySelector('#systemI18n').textContent).sys_indexSite);
          const label = await page.locator('#systemRoot jelly-switch[name="seo_index_site"]').evaluate((el) => el.closest('.sys-row')?.querySelector('label')?.textContent.trim());
          assert(label === translation, `${locale}: the index setting label ${JSON.stringify(label)} does not match this locale's translated value ${JSON.stringify(translation)}`);
          const generatedForm = page.locator('#systemRoot form[data-form-key="indexTypes"]');
          await generatedForm.waitFor({ state: 'visible', timeout: 5000 });
          const indexTypes = ['home', 'post', 'page', 'category', 'tag', 'author', 'archive', 'product', 'search', 'commerce', 'account'];
          const actualSwitches = await generatedForm.locator('jelly-switch').evaluateAll((elements) => elements.map((element) => element.getAttribute('name')));
          assert(JSON.stringify(actualSwitches) === JSON.stringify(indexTypes), `${locale}: generated index-type switches are ${JSON.stringify(actualSwitches)}, want ${JSON.stringify(indexTypes)}`);
          const generatedCheckboxCount = await generatedForm.evaluate((form) => form.querySelectorAll('input[type="checkbox"]').length);
          assert(generatedCheckboxCount === 0, `${locale}: generated index-type booleans render ${generatedCheckboxCount} page-level native checkboxes`);
        }
        await page.close();
      }
    }
  });

  await check('switch: changing a saved boolean marks settings dirty and preserves it through refresh and save failure', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.route('**/api/admin/settings', async (route) => {
      if (route.request().method() === 'PUT') await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Settings are temporarily unavailable' }) });
      else await route.continue();
    });
    await page.goto(`${base}/admin/settings/comments?lang=en`);
    await jellyReady(page, 'jelly-switch', 'jelly-dialog');
    const toggle = page.locator('#systemRoot jelly-switch[name="comments_registered_only"]');
    await toggle.waitFor({ state: 'visible', timeout: 10000 });
    const before = await toggle.evaluate((el) => el.hasAttribute('checked'));
    await toggle.focus();
    await page.keyboard.press('Space');
    const expected = !before;
    assert(await toggle.evaluate((el) => el.hasAttribute('checked')) === expected, 'Space did not change the saved boolean');
    const form = page.locator('#systemRoot form[data-form-key="commentModeration"]');
    await page.waitForFunction(() => document.querySelector('#systemRoot form[data-form-key="commentModeration"]')?.dataset.dirty === 'true', null, { timeout: 5000 });
    assert(await page.evaluate(() => { const e = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(e); return e.defaultPrevented; }), 'a changed Jelly switch must warn before leaving the page');

    const [pageshowRefresh] = await Promise.all([
      page.waitForResponse((response) => response.request().method() === 'GET' && response.url().endsWith('/api/admin/settings'), { timeout: 5000 }),
      page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))),
    ]);
    assert(pageshowRefresh.ok(), `a persisted pageshow refresh returned HTTP ${pageshowRefresh.status()}`);
    await page.waitForFunction(() => document.querySelector('#systemRoot')?.getAttribute('aria-busy') === 'false', null, { timeout: 10000 });
    assert(await toggle.evaluate((el) => el.hasAttribute('checked')) === expected, 'a persisted pageshow refresh lost the changed boolean');
    assert(await form.evaluate((el) => el.dataset.dirty === 'true'), 'a persisted pageshow refresh cleared the unsaved form state');

    await page.locator('#systemRoot [data-action="refresh"]').click();
    await page.locator('jelly-dialog[open]').waitFor({ state: 'visible', timeout: 5000 });
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]'), null, { timeout: 5000 });
    assert(await toggle.evaluate((el) => el.hasAttribute('checked')) === expected, 'cancelling refresh lost the changed boolean');
    assert(await form.evaluate((el) => el.dataset.dirty === 'true'), 'cancelling refresh cleared the unsaved state');

    await form.locator('button[type="submit"]').click();
    await page.locator('#systemRoot [role="alert"]').waitFor({ state: 'visible', timeout: 10000 });
    assert(await toggle.evaluate((el) => el.hasAttribute('checked')) === expected, 'a failed save lost the changed boolean');
    assert(await form.evaluate((el) => el.dataset.dirty === 'true'), 'a failed save cleared the unsaved state');
    await page.close();
  });

  await check('switch: saved booleans and environment survive setting attributes before custom-element upgrade', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.route('**/api/admin/shop/settings', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ settings: { payment_ecpay_enabled: 'true', payment_ecpay_test_mode: 'false' } }) });
      } else await route.continue();
    });
    await page.route(/\/admin\/assets\/jelly-ui\/jelly\.js(\?|$)/, async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
    await page.goto(`${base}/admin/settings/payment?lang=zh-TW`, { waitUntil: 'commit' });
    await page.locator('#ecpayEnabled').waitFor({ state: 'attached', timeout: 5000 });
    await page.waitForFunction(() => {
      const enabled = document.getElementById('ecpayEnabled');
      const mode = document.getElementById('ecpayTestMode');
      return enabled && mode && enabled.hasAttribute('checked') && mode.getAttribute('value') === 'live';
    }, null, { timeout: 10000 });
    assert(await page.evaluate(() => !customElements.get('jelly-switch') && !customElements.get('jelly-segmented')), 'the saved settings were not assigned before the controls upgraded');
    assert(await page.evaluate(() => document.getElementById('ecpayEnabled').hasAttribute('checked')), 'the saved true boolean was not reflected in the checked attribute before upgrade');
    assert(await page.evaluate(() => document.getElementById('ecpayTestMode').getAttribute('value')) === 'live', 'the saved false test_mode was not mapped to the live attribute before upgrade');
    await jellyReady(page, 'jelly-switch', 'jelly-segmented');
    assert(await page.evaluate(() => document.getElementById('ecpayEnabled').checked) === true, 'the upgraded switch does not retain the saved true boolean');
    assert(await page.evaluate(() => document.getElementById('ecpayTestMode').value) === 'live', 'the upgraded segmented control does not retain the saved live value');
    await page.close();
  });

  await check('dialog: shared shop confirmations use Jelly UI and cancelling keeps a coupon', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    const code = `JUI${Date.now().toString().slice(-9)}`;
    const created = await page.request.post(`${base}/api/admin/coupons`, {
      data: { code, kind: 'fixed', value: 500 }, headers: { 'X-Jelly-CSRF': '1' },
    });
    assert(created.ok(), `creating coupon fixture: HTTP ${created.status()}`);
    const coupon = await created.json();
    const deletes = [];
    page.on('request', (request) => { if (request.method() === 'DELETE' && request.url().endsWith(`/api/admin/coupons/${coupon.id}`)) deletes.push(request.url()); });
    try {
      await page.goto(`${base}/admin/coupons`);
      const remove = page.locator(`#list [data-del="${coupon.id}"]`);
      await remove.waitFor({ state: 'visible', timeout: 10000 });
      await remove.click();
      const dialog = page.locator('jelly-dialog[open][data-jelly-confirm]');
      await dialog.waitFor({ state: 'visible', timeout: 5000 });
      await dialog.locator('button[data-jelly-cancel]').click();
      await dialog.waitFor({ state: 'hidden', timeout: 5000 });
      assert(deletes.length === 0, 'cancelling a coupon confirmation must not send a delete request');
      const listed = await page.request.get(`${base}/api/admin/coupons?q=${encodeURIComponent(code)}`);
      assert(listed.ok() && JSON.stringify(await listed.json()).includes(code), 'the coupon remained after cancelling its confirmation');
    } finally {
      await page.request.delete(`${base}/api/admin/coupons/${coupon.id}`, { headers: { 'X-Jelly-CSRF': '1' } });
      await page.close();
    }
  });

  await check('dialog: deleting an integration requires its exact name in the Jelly confirmation', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    const name = `Jelly UI ${Date.now()}`;
    const created = await page.request.post(`${base}/api/admin/integrations`, {
      data: { name, description: 'Browser confirmation fixture', permissions: ['posts.read'] },
      headers: { 'X-Jelly-CSRF': '1' },
    });
    assert(created.ok(), `creating integration fixture: HTTP ${created.status()}`);
    const createdPayload = await created.json();
    const integration = createdPayload.integration;
    assert(integration?.id, 'creating the integration fixture must return an integration with an id');
    const deleteStatuses = [];
    await page.route(`**/api/admin/integrations/${integration.id}`, async (route) => {
      if (route.request().method() !== 'DELETE') return route.continue();
      deleteStatuses.push(503);
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Injected integration delete failure' }) });
    });
    try {
      await page.goto(`${base}/admin/integrations/${integration.id}`);
      await page.locator('#integrationToggle').waitFor({ state: 'visible', timeout: 10000 });
      const deleteButton = page.locator('#integrationsContent > .integration-actions button').nth(2);
      const openDeleteConfirm = async () => {
        await deleteButton.click();
        const dialog = page.locator('jelly-dialog[open][data-jelly-confirm]');
        await dialog.waitFor({ state: 'visible', timeout: 5000 });
        const input = dialog.locator('input[data-jelly-confirm-input]');
        await input.waitFor({ state: 'visible', timeout: 5000 });
        return { dialog, input, confirm: dialog.locator('button[data-jelly-confirm]') };
      };

      let { dialog, input, confirm } = await openDeleteConfirm();
      await input.fill(`${name}x`);
      assert(await confirm.isDisabled(), 'an incorrect integration name must keep deletion disabled');
      await input.fill(name);
      assert(await confirm.isEnabled(), 'the exact integration name must enable deletion');
      await dialog.locator('button[data-jelly-cancel]').click();
      await dialog.waitFor({ state: 'hidden', timeout: 5000 });
      assert(deleteStatuses.length === 0, 'cancelling the integration confirmation must not send a delete request');

      for (let attempt = 1; attempt <= 2; attempt++) {
        ({ dialog, input, confirm } = await openDeleteConfirm());
        await input.fill(name);
        const [response] = await Promise.all([
          page.waitForResponse((res) => res.request().method() === 'DELETE' && res.url().endsWith(`/api/admin/integrations/${integration.id}`), { timeout: 5000 }),
          confirm.click(),
        ]);
        assert(response.status() === 503, `simulated delete attempt ${attempt} returned HTTP ${response.status()}, want 503`);
        const mainStatus = page.locator('#integrationsStatus[role="status"]');
        await page.waitForFunction(() => document.querySelector('#integrationsStatus')?.textContent.includes('503'), null, { timeout: 5000 });
        assert(await mainStatus.isVisible(), 'the failed delete message must be visible in the main integration page status');
        assert((await mainStatus.innerText()).includes('503'), 'the main page must identify the simulated HTTP failure');
        assert(await deleteButton.isEnabled(), 'the delete action must be available to retry after a failed request');
        assert(deleteStatuses.length === attempt, `expected ${attempt} intercepted delete attempt(s), found ${deleteStatuses.length}`);
      }
    } finally {
      await page.request.delete(`${base}/api/admin/integrations/${integration.id}`, { headers: { 'X-Jelly-CSRF': '1' } });
      await page.close();
    }
  });

  await check('menus: Escape cancels the nested Jelly confirmation and preserves the menu editor', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    const name = `Jelly UI menu ${Date.now()}`;
    const created = await page.request.post(`${base}/api/admin/menus`, {
      data: { name }, headers: { 'X-Jelly-CSRF': '1' },
    });
    assert(created.ok(), `creating menu fixture: HTTP ${created.status()}`);
    const menu = await created.json();
    const deletes = [];
    page.on('request', (request) => {
      if (request.method() === 'DELETE' && request.url().endsWith(`/api/admin/menus/${menu.id}`)) deletes.push(request.url());
    });
    try {
      await page.goto(`${base}/admin/menus`);
      const row = page.locator(`#menuList button[data-id="${menu.id}"]`);
      await row.waitFor({ state: 'visible', timeout: 10000 });
      await row.click();
      await page.locator('#treeMount #menuSettings').waitFor({ state: 'visible', timeout: 5000 });
      await page.locator('#treeMount #menuSettings').click();
      const form = page.locator('jelly-dialog[open]:not([data-jelly-confirm]) form:has(#msName)');
      await form.waitFor({ state: 'visible', timeout: 5000 });
      const nameInput = form.locator('#msName');
      const unsavedName = `${name} unsaved edit`;
      await nameInput.fill(unsavedName);
      const deleteButton = form.locator('[data-del]');
      await deleteButton.click();
      const dialog = page.locator('jelly-dialog[open][data-jelly-confirm]');
      await dialog.waitFor({ state: 'visible', timeout: 5000 });
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'hidden', timeout: 5000 });
      assert(await form.isVisible(), 'Escape must cancel the inner confirmation without closing the menu editor');
      assert(await nameInput.inputValue() === unsavedName, 'cancelling the inner confirmation must preserve the unsaved menu name');
      assert(await deleteButton.evaluate((el) => document.activeElement === el), 'focus must return to the delete button that opened Jelly.confirm');
      assert(deletes.length === 0, `cancelling with Escape sent a delete request: ${JSON.stringify(deletes)}`);
      await form.locator('[data-cancel]').click();
      await form.waitFor({ state: 'hidden', timeout: 5000 });
      const readback = await page.request.get(`${base}/api/admin/menus`);
      const menus = await readback.json();
      assert(readback.ok() && menus.some((item) => item.id === menu.id && item.name === name), 'the menu must remain unchanged after cancelling deletion');
    } finally {
      await page.request.delete(`${base}/api/admin/menus/${menu.id}`, { headers: { 'X-Jelly-CSRF': '1' } });
      await page.close();
    }
  });

  await check('segmented: the ECPay 環境 control (測試／正式) is saved as the same boolean test_mode', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin/settings/payment?lang=zh-TW`);
    await jellyReady(page, 'jelly-switch', 'jelly-segmented');
    await page.waitForSelector('#ecpayTestMode', { state: 'visible', timeout: 5000 });
    const env = () => page.evaluate(() => document.getElementById('ecpayTestMode').getAttribute('value'));
    const readShopSettings = async () => {
      const response = await page.request.get(`${base}/api/admin/shop/settings`);
      assert(response.ok(), `reading saved shop settings returned HTTP ${response.status()}`);
      return response.json();
    };
    const initialSettings = await readShopSettings();
    const initialMode = initialSettings.payment_ecpay_test_mode === 'false' ? 'live' : 'test';
    const initialEnabled = initialSettings.payment_ecpay_enabled === 'true';
    assert(await env() === initialMode, `the environment control shows ${await env()} but saved settings say ${initialMode}`);
    assert(await page.evaluate(() => document.getElementById('ecpayEnabled').hasAttribute('checked')) === initialEnabled,
      'the ECPay switch does not match its authenticated saved-settings readback');
    const labels = await page.locator('#ecpayTestMode button[role=radio]').allInnerTexts();
    assert(labels.map((l) => l.trim()).join(',') === '測試,正式', `segment labels were ${JSON.stringify(labels)}`);
    // Turn the gateway on if needed, then provide the credentials required by the enabled-payment settings.
    await page.focus('#ecpayEnabled');
    if (!initialEnabled) await page.keyboard.press('Space');
    await page.fill('#ecpayMid', '2000132');
    for (const [id, key, value] of [
      ['ecpayKey', 'payment_ecpay_hash_key', 'key-for-acceptance'],
      ['ecpayIv', 'payment_ecpay_hash_iv', 'iv-for-acceptance'],
    ]) {
      const input = page.locator(`#${id}`);
      if (await input.evaluate((el) => el.readOnly)) {
        await page.locator(`[data-secret="${key}"] button[data-act="replace"]`).click();
      }
      await input.fill(value);
    }
    const saveMode = async (target) => {
      const label = target === 'test' ? '測試' : '正式';
      await page.locator('#ecpayTestMode button[role=radio]', { hasText: label }).click();
      assert(await env() === target, `clicking ${label} did not select ${target}`);
      const [response, request] = await Promise.all([
        page.waitForResponse((res) => res.request().method() === 'PUT' && res.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
        page.waitForRequest((req) => req.method() === 'PUT' && req.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
        page.click('#shopSave'),
      ]);
      assert(response.ok(), `saving ${label} returned HTTP ${response.status()}`);
      const expected = target === 'test' ? 'true' : 'false';
      const body = request.postDataJSON();
      const expectedBoolean = target === 'test';
      assert(body.payment_ecpay_test_mode === expectedBoolean,
        `the ${label} save diff has payment_ecpay_test_mode=${JSON.stringify(body.payment_ecpay_test_mode)}, want JSON boolean ${expectedBoolean}; fields=${Object.keys(body).join(',')}`);
      const saved = await readShopSettings();
      assert(saved.payment_ecpay_test_mode === expected, `authenticated readback after ${label} is ${JSON.stringify(saved.payment_ecpay_test_mode)}, want ${expected}`);
      assert(saved.payment_ecpay_enabled === 'true', `authenticated readback after ${label} says ECPay enabled=${JSON.stringify(saved.payment_ecpay_enabled)}, want true`);
    };
    const modes = initialMode === 'test' ? ['live', 'test'] : ['test', 'live'];
    for (const mode of modes) await saveMode(mode);
    assert(new Set(modes.map((mode) => mode === 'test' ? 'true' : 'false')).size === 2,
      'both persisted boolean environment values must be exercised');
    const shippingFixture = [{
      id: 'acceptance-home', label: '驗收宅配', fee: 80, free_over: 1200,
      eta_days: 2, requires_address: true, enabled: true,
    }];
    const paymentFixture = { credit_card: 'ecpay', atm: 'ecpay', cvs: 'ecpay' };
    const fixtureResponse = await page.request.put(`${base}/api/admin/shop/settings`, {
      data: { shop_shipping_methods: shippingFixture, payment_methods: paymentFixture },
      headers: { 'X-Jelly-CSRF': '1' },
    });
    assert(fixtureResponse.ok(), `setting up disposable shop-setting fixtures returned HTTP ${fixtureResponse.status()}`);
    await page.goto(`${base}/admin/settings/shipping?lang=zh-TW`);
    await jellyReady(page, 'jelly-switch');
    const homeAddress = page.locator('#shipRows tr[data-i="0"] jelly-switch[data-k="requires_address"]');
    await homeAddress.waitFor({ state: 'visible', timeout: 5000 });
    const addressBefore = await homeAddress.evaluate((el) => el.hasAttribute('checked'));
    assert(addressBefore, 'the authenticated shipping fixture should begin with requires_address enabled');
    await homeAddress.click();
    assert(await homeAddress.evaluate((el) => el.hasAttribute('checked')) !== addressBefore,
      'clicking the home-shipping address switch must change its visible state');
    const [shippingResponse, shippingRequest] = await Promise.all([
      page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
      page.waitForRequest((request) => request.method() === 'PUT' && request.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
      page.click('#shopSave'),
    ]);
    assert(shippingResponse.ok(), `saving the shipping switch returned HTTP ${shippingResponse.status()}`);
    const shippingBody = shippingRequest.postDataJSON();
    assert(Array.isArray(shippingBody.shop_shipping_methods),
      `the shipping UI must send shop_shipping_methods as a JSON array, got ${JSON.stringify(shippingBody.shop_shipping_methods)}`);
    const postedHome = shippingBody.shop_shipping_methods[0];
    assert(typeof postedHome.requires_address === 'boolean' && postedHome.requires_address !== addressBefore,
      `the shipping array must contain the changed requires_address boolean, got ${JSON.stringify(postedHome)}`);
    assert(typeof postedHome.enabled === 'boolean', `the shipping array must retain its nested enabled boolean, got ${JSON.stringify(postedHome)}`);
    const shippingReadback = await readShopSettings();
    const savedShipping = JSON.parse(shippingReadback.shop_shipping_methods);
    assert(Array.isArray(savedShipping) && JSON.stringify(savedShipping) === JSON.stringify(shippingBody.shop_shipping_methods),
      'authenticated readback must preserve the saved shipping array and nested booleans');

    await page.goto(`${base}/admin/settings/payment?lang=zh-TW`);
    await jellyReady(page, 'jelly-switch', 'jelly-segmented');
    const creditCard = page.locator('#pm_credit_card');
    await creditCard.waitFor({ state: 'visible', timeout: 5000 });
    const oldProvider = await creditCard.inputValue();
    assert(oldProvider === 'ecpay', `the authenticated payment fixture selected ${oldProvider}, want ecpay`);
    const nextProvider = '';
    await creditCard.selectOption(nextProvider);
    const transfer = page.locator('#transferOn');
    const transferBefore = await transfer.evaluate((el) => el.hasAttribute('checked'));
    assert(!transferBefore, 'the authenticated payment fixture should begin with bank transfer disabled');
    await transfer.click();
    const transferAfter = await transfer.evaluate((el) => el.hasAttribute('checked'));
    assert(transferAfter !== transferBefore, 'clicking the bank-transfer switch must change its visible state');
    const [paymentResponse, paymentRequest] = await Promise.all([
      page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
      page.waitForRequest((request) => request.method() === 'PUT' && request.url().endsWith('/api/admin/shop/settings'), { timeout: 5000 }),
      page.click('#shopSave'),
    ]);
    assert(paymentResponse.ok(), `saving the payment-method controls returned HTTP ${paymentResponse.status()}`);
    const paymentBody = paymentRequest.postDataJSON();
    assert(paymentBody.payment_methods && typeof paymentBody.payment_methods === 'object' && !Array.isArray(paymentBody.payment_methods),
      `the payment-method UI must send payment_methods as a JSON object, got ${JSON.stringify(paymentBody.payment_methods)}`);
    assert(!Object.prototype.hasOwnProperty.call(paymentBody.payment_methods, 'credit_card'),
      `choosing Not offered must omit the credit-card key from the payment-method object, got ${JSON.stringify(paymentBody.payment_methods)}`);
    assert((paymentBody.payment_methods.bank_transfer === 'manual_transfer') === transferAfter,
      `the payment-method object must follow the transfer switch state, got ${JSON.stringify(paymentBody.payment_methods)}`);
    const paymentReadback = await readShopSettings();
    const savedPayments = JSON.parse(paymentReadback.payment_methods);
    assert(savedPayments && typeof savedPayments === 'object' && !Array.isArray(savedPayments) &&
      JSON.stringify(savedPayments) === JSON.stringify(paymentBody.payment_methods),
    'authenticated readback must preserve the saved payment-method object');

    await page.goto(`${base}/admin/settings/payment?lang=zh-TW`);
    await jellyReady(page, 'jelly-segmented');
    // Keyboard: Right arrow from 測試 selects 正式 (RTL flips it, this page is LTR).
    const testOption = page.locator('#ecpayTestMode button[role=radio]', { hasText: '測試' });
    if (await env() !== 'test') await testOption.click();
    await testOption.focus();
    await page.keyboard.press('ArrowRight');
    assert(await env() === 'live', 'ArrowRight did not move the 環境 control to 正式');
    await page.close();
  });

  await check('segmented: the posts status filter filters rows by click and by arrow keys', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    const stamp = Date.now();
    const make = async (title, status) => {
      const res = await page.request.post(`${base}/api/admin/posts`, { data: { title, status, content: 'x' }, headers: { 'X-Jelly-CSRF': '1' } });
      assert(res.ok(), `creating ${title}: HTTP ${res.status()}`);
      return (await res.json()).id;
    };
    const ids = [await make(`UI draft ${stamp}`, 'draft'), await make(`UI live ${stamp}`, 'published')];
    try {
      await page.goto(`${base}/admin/posts`);
      await jellyReady(page, 'jelly-segmented');
      await page.waitForSelector(`text=UI live ${stamp}`);
      const rows = () => page.locator('#postsList tr').allInnerTexts();
      assert((await page.locator('jelly-segmented jelly-segment').count()) === 4, 'expected 4 segments');
      assert(await page.evaluate(() => document.querySelector('jelly-segmented').getAttribute('value')) === 'all', 'initial value must be all');
      await page.locator('jelly-segmented button[role=radio]', { hasText: /draft|草稿/i }).click();
      assert(await page.evaluate(() => document.querySelector('jelly-segmented').getAttribute('value')) === 'draft', 'clicking Draft did not select it');
      let texts = (await rows()).join('\n');
      assert(texts.includes(`UI draft ${stamp}`) && !texts.includes(`UI live ${stamp}`), 'the draft filter must keep drafts and hide published rows');
      await page.keyboard.press('ArrowLeft');   // roving tabindex: Left moves to the previous segment (published)
      assert(await page.evaluate(() => document.querySelector('jelly-segmented').getAttribute('value')) === 'published', 'ArrowLeft did not move the selection');
      texts = (await rows()).join('\n');
      assert(texts.includes(`UI live ${stamp}`) && !texts.includes(`UI draft ${stamp}`), 'the published filter must keep published rows and hide drafts');
      await page.keyboard.press('Home');
      assert(await page.evaluate(() => document.querySelector('jelly-segmented').getAttribute('value')) === 'all', 'Home did not select the first segment');
      texts = (await rows()).join('\n');
      assert(texts.includes(`UI live ${stamp}`) && texts.includes(`UI draft ${stamp}`), 'All must show every row');
    } finally {
      for (const id of ids) await page.request.delete(`${base}/api/admin/posts/${id}`, { headers: { 'X-Jelly-CSRF': '1' } });
    }
    await page.close();
  });

  await check('segmented: an empty posts result is distinct from a failed posts request', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    await page.route('**/api/admin/posts', async (route) => {
      if (route.request().method() === 'GET') await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
      else await route.continue();
    });
    await page.goto(`${base}/admin/posts`);
    await jellyReady(page, 'jelly-segmented');
    await page.locator('#postsList .table-empty').waitFor({ state: 'visible', timeout: 5000 });
    const emptyMessage = (await page.locator('#postsList').innerText()).trim();
    assert(emptyMessage.length > 0, 'a successful empty response must show the localized empty state');
    await page.close();

    const failedPage = await openPage(context, [baseOrigin]);
    await failedPage.goto(`${base}/admin`);
    await failedPage.route('**/api/admin/posts', async (route) => {
      if (route.request().method() === 'GET') await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Temporarily unavailable' }) });
      else await route.continue();
    });
    await failedPage.goto(`${base}/admin/posts`);
    await jellyReady(failedPage, 'jelly-segmented');
    await failedPage.locator('#postsList').waitFor({ state: 'visible', timeout: 5000 });
    assert(!(await failedPage.locator('#postsList').innerText()).includes(emptyMessage), 'a failed request must not be presented as a successful empty result');
    const errorText = await failedPage.locator('[role="alert"], [role="status"]').evaluateAll((items) => items.filter((item) => item.getClientRects().length > 0).map((item) => item.innerText.trim()).filter(Boolean).join('\n'));
    assert(errorText.length > 0, 'a failed posts request must show a non-empty user-visible error');
    await failedPage.close();
  });

  if (screenshotDir) {
    await check('visual matrix: 24 real page screenshots across locale, width and theme', () => runVisualScreenshotMatrix(context));
  }

  await check('dark mode: data-jelly-mode follows the colour scheme and the switch repaints', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.goto(`${base}/admin/settings`);
    await jellyReady(page, 'jelly-switch');
    assert(await page.evaluate(() => document.documentElement.getAttribute('data-jelly-mode')) === 'dark', 'data-jelly-mode should be dark under a dark colour scheme');
    const dialogSurface = async () => {
      await page.evaluate(() => { Jelly.confirm({ title: 'Surface', tone: 'primary' }); });
      await page.locator('jelly-dialog[open]').waitFor({ state: 'visible' });
      const bg = await page.evaluate(() => getComputedStyle(document.querySelector('jelly-dialog[open]').shadowRoot.querySelector('.dialog')).backgroundColor);
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]'));
      return bg;
    };
    const dark = await dialogSurface();
    await page.emulateMedia({ colorScheme: 'light' });
    await page.waitForFunction(() => document.documentElement.getAttribute('data-jelly-mode') === 'light', null, { timeout: 5000 });
    const light = await dialogSurface();
    assert(dark !== light, `the dialog surface did not change between dark (${dark}) and light (${light})`);
    await page.close();
  });

  await check('dark mode: an explicit theme setting overrides the opposite system colour scheme', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.emulateMedia({ colorScheme: 'light' });
    await page.goto(`${base}/admin`);
    await jellyReady(page, 'jelly-dialog');
    const dialogSurface = async () => {
      await page.evaluate(() => { Jelly.confirm({ title: 'Theme check', tone: 'primary' }); });
      await page.locator('jelly-dialog[open]').waitFor({ state: 'visible', timeout: 5000 });
      const bg = await page.evaluate(() => getComputedStyle(document.querySelector('jelly-dialog[open]').shadowRoot.querySelector('.dialog')).backgroundColor);
      await page.keyboard.press('Escape');
      await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]'), null, { timeout: 5000 });
      return bg;
    };

    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    await page.waitForFunction(() => document.documentElement.getAttribute('data-jelly-mode') === 'dark', null, { timeout: 5000 });
    const explicitDark = await dialogSurface();
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
    await page.waitForFunction(() => document.documentElement.getAttribute('data-jelly-mode') === 'light', null, { timeout: 5000 });
    const explicitLight = await dialogSurface();
    assert(explicitDark !== explicitLight, `explicit light/dark themes produced the same dialog surface (${explicitDark})`);
    await page.close();
  });

  await check('RTL: with dir=rtl the switch toggles by keyboard, the dialog opens and closes, nothing throws', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin/settings`);
    await jellyReady(page, 'jelly-switch', 'jelly-dialog');
    await page.evaluate(() => { document.documentElement.dir = 'rtl'; });
    await page.locator('.tabs .tab', { hasText: 'OAuth' }).first().click();
    await page.waitForSelector('#githubEnabled', { state: 'visible' });
    const before = await page.evaluate(() => document.getElementById('githubEnabled').hasAttribute('checked'));
    await page.focus('#githubEnabled');
    await page.keyboard.press('Space');
    assert(await page.evaluate(() => document.getElementById('githubEnabled').hasAttribute('checked')) === !before, 'the switch did not toggle under rtl');
    await page.evaluate(() => { window.__r = 'pending'; Jelly.confirm({ title: 'RTL', tone: 'primary' }).then((v) => { window.__r = v; }); });
    await page.locator('jelly-dialog[open]').waitFor({ state: 'visible' });
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => window.__r === false, null, { timeout: 5000 });
    assert(problemsOf(page).length === 0, problemsOf(page).join('\n'));
    await page.close();
  });

  await check('no page other than the admin loads Jelly UI', async () => {
    const failures = [];
    for (const p of ['/login', '/']) {
      const page = await openPage(context, [baseOrigin]);
      await page.goto(base + p);
      if (await page.evaluate(() => !!customElements.get('jelly-switch'))) failures.push(`${p} defines jelly-switch`);
      await page.close();
    }
    assert(failures.length === 0, failures.join('\n'));
  });

  await context.close();
}

// ---------------------------------------------------------------------------------------------------------
// Ticket 26 (docs/tickets/26-jelly-ui-consistency.md), slot 26-B: JELLY_ONLY=consistency. Every admin page
// shows only Jelly UI selection controls and dialogs, keyboard operation works on the replaced controls, the
// requests the pages send keep the same JSON fields and value types (expected bodies recorded on main
// fc6ab71 before the replacement), notices that used alert() are Jelly toasts, and nothing overflows at
// 390px in light or dark. These checks are expected to fail until slot 26-C lands; each failure names the
// legacy control it found. Fixtures (a draft post, a menu, coupons, a category, a product) are created
// through the admin API and removed again.

// The pages of 〈逐頁清單〉 with the Jelly elements each must show once loaded (inner arrays are "any of").
// '{post}' is replaced by the id of a draft post fixture.
const consistencyPages = [
  { path: '/admin/taxonomy', need: ['jelly-switch', 'jelly-segmented'] },
  { path: '/admin/menus', need: ['jelly-switch', 'jelly-segmented'] },
  { path: '/admin/coupons', need: ['jelly-switch', 'jelly-segmented'] },
  { path: '/admin/posts/new', need: ['jelly-switch', 'jelly-segmented', 'jelly-checkbox', 'jelly-radio'] },
  { path: '/admin/posts/{post}', need: ['jelly-switch', 'jelly-segmented', 'jelly-checkbox', 'jelly-radio'] },
  { path: '/admin/posts/{post}/revisions', need: ['jelly-segmented', ['jelly-switch', 'jelly-checkbox']] },
  { path: '/admin/pages/new', need: ['jelly-switch', 'jelly-segmented', 'jelly-radio'] },
  { path: '/admin/products/new', need: ['jelly-segmented', 'jelly-checkbox'] },
  { path: '/admin/products', need: ['jelly-segmented'] },
  { path: '/admin/orders', need: ['jelly-segmented'] },
  { path: '/admin/comments', need: ['jelly-segmented', 'jelly-checkbox'] },
  { path: '/admin/trash', need: ['jelly-segmented', 'jelly-checkbox'] },
  { path: '/admin/users', need: ['jelly-segmented', 'jelly-checkbox', 'jelly-radio-group', 'jelly-radio'] },
  { path: '/admin/media', need: [] },
  { path: '/admin/plugins', need: [] },
  { path: '/admin/themes', need: [] },
  { path: '/admin/account?tab=notifications', need: [['jelly-checkbox', 'jelly-switch']] },
  { path: '/admin/integrations', need: ['jelly-checkbox'] },
  { path: '/admin/settings/auth', need: ['jelly-switch'] },
  { path: '/admin/logs', need: ['jelly-checkbox'] },
];

// Page-context helpers, passed as source so each evaluate can use them.
const describeSource = `(el) => {
  if (!el) return 'nothing';
  const cls = typeof el.className === 'string' && el.className.trim() ? '.' + el.className.trim().split(/\\s+/).join('.') : '';
  const attrs = ['name', 'type', 'value'].filter((a) => el.getAttribute(a) !== null).map((a) => '[' + a + '=' + el.getAttribute(a) + ']').join('');
  return el.localName + (el.id ? '#' + el.id : '') + cls + attrs;
}`;

async function consistencyPage(context, width = 1280) {
  const page = await openPage(context, [baseOrigin]);
  page.nativeDialogs = [];
  page.on('dialog', (dialog) => {
    page.nativeDialogs.push(`${dialog.type()}(${JSON.stringify(dialog.message())})`);
    dialog.dismiss().catch(() => {});
  });
  await page.setViewportSize({ width, height: 800 });
  return page;
}

// Fails with a message that names what the page shows instead (the legacy control), when there is one.
async function need(page, selector, what, legacySelector) {
  const locator = page.locator(selector).first();
  try {
    await locator.waitFor({ state: 'attached', timeout: 5000 });
  } catch {
    let legacy = '';
    if (legacySelector) {
      legacy = await page.evaluate(({ sel, src }) => {
        const describe = eval(src);
        const found = [...document.querySelectorAll(sel)].slice(0, 4).map(describe);
        return found.length ? `; the page still shows ${found.join(', ')}` : '';
      }, { sel: legacySelector, src: describeSource }).catch(() => '');
    }
    throw new Error(`${what}: nothing matches ${selector} on ${new URL(page.url()).pathname}${legacy}`);
  }
  return locator;
}

function problemsExcept(page, expected = []) {
  return problemsOf(page).filter((p) => !expected.some((re) => re.test(p)));
}

function assertCleanPage(page, expected = []) {
  assert(page.nativeDialogs.length === 0, `the page opened browser dialogs: ${page.nativeDialogs.join(', ')}; use Jelly.toast / Jelly.confirm`);
  const problems = problemsExcept(page, expected);
  assert(problems.length === 0, `unexpected page problems:\n${problems.join('\n')}`);
}

// The JSON shape of a value: every key mapped to string, number, boolean, null, array or object.
function shapeOf(value) {
  const kind = (v) => v === null ? 'null' : Array.isArray(v) ? 'array' : typeof v;
  return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, kind(v)]));
}

function assertShape(label, body, expected) {
  const got = shapeOf(body);
  const keys = (o) => Object.keys(o).sort().join(',');
  assert(keys(got) === keys(expected), `${label}: fields changed\n  sent:     ${keys(got)}\n  expected: ${keys(expected)}`);
  const wrong = Object.keys(expected).filter((k) => got[k] !== expected[k]).map((k) => `${k} is ${got[k]}, want ${expected[k]}`);
  assert(wrong.length === 0, `${label}: value types changed: ${wrong.join('; ')}`);
}

// The pressed state of a Jelly switch, checkbox or radio is its checked attribute (ticket 12 〈上游事實〉 6).
const isChecked = (locator) => locator.evaluate((el) => el.hasAttribute('checked'));

async function toastAfter(page, action, what) {
  const before = await page.locator('jelly-toaster .toast').count();
  await action();
  try {
    await page.waitForFunction((n) => (document.querySelector('jelly-toaster')?.shadowRoot?.querySelectorAll('.toast').length || 0) > n, before, { timeout: 5000 });
  } catch {
    throw new Error(`${what}: no Jelly toast appeared${page.nativeDialogs.length ? `; the page used ${page.nativeDialogs.join(', ')} instead` : ' and no browser dialog either (the action did nothing)'}`);
  }
  const toast = page.locator('jelly-toaster .toast').last();
  return { text: (await toast.innerText()).trim(), tone: (await toast.locator('.sr-tone').innerText().catch(() => '')).replace(/:$/, '') };
}

// After the opener was clicked: exactly one open <jelly-dialog> (not counting Jelly.confirm) and no
// hand-made .modal-overlay or native <dialog> on screen. Returns the open dialog.
async function openedJellyDialog(page, what) {
  const state = await page.waitForFunction((src) => {
    const describe = eval(src);
    const jelly = [...document.querySelectorAll('jelly-dialog[open]:not([data-jelly-confirm])')];
    const legacy = [
      ...[...document.querySelectorAll('.modal-overlay')].filter((el) => getComputedStyle(el).display !== 'none' && !el.hidden),
      ...document.querySelectorAll('dialog[open]'),
    ];
    if (!jelly.length && !legacy.length) return null;
    return {
      jelly: jelly.length,
      legacy: legacy.map(describe),
      names: jelly.map((d) => d.shadowRoot?.querySelector('[role="dialog"]')?.getAttribute('aria-label') || ''),
    };
  }, describeSource, { timeout: 5000 }).then((handle) => handle.jsonValue()).catch(() => null);
  assert(state, `${what}: nothing opened`);
  assert(state.jelly === 1 && state.legacy.length === 0,
    `${what}: want one open <jelly-dialog> and no hand-made overlay or native <dialog>; found ${state.jelly} open <jelly-dialog>${state.legacy.length ? ` and ${state.legacy.join(', ')}` : ''}`);
  assert(state.names[0].trim(), `${what}: the open <jelly-dialog> has no accessible name (give it a label attribute or a heading)`);
  return page.locator('jelly-dialog[open]:not([data-jelly-confirm])');
}

async function closedAndFocusReturned(page, opener, how, what) {
  try {
    await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]:not([data-jelly-confirm])'), null, { timeout: 5000 });
  } catch {
    throw new Error(`${what}: ${how} did not close the <jelly-dialog>`);
  }
  const focus = await page.evaluate((src) => eval(src)(document.activeElement), describeSource);
  assert(await opener.evaluate((el) => el === document.activeElement || el.contains(document.activeElement)),
    `${what}: after ${how} the focus is on ${focus}, want the button that opened the dialog`);
}

async function apiJSON(page, method, url, data) {
  const response = await page.request.fetch(`${base}${url}`, { method, data, headers: { 'X-Jelly-CSRF': '1' } });
  let body = null;
  try { body = await response.json(); } catch { body = null; }
  return { ok: response.ok(), status: response.status(), body };
}

// The panel of an open <jelly-dialog> (its shadow part="dialog") in viewport coordinates.
async function panelOf(dialog) {
  return dialog.evaluate((el) => {
    const r = el.shadowRoot?.querySelector('[part="dialog"]')?.getBoundingClientRect();
    return r ? { left: r.left, top: r.top, right: r.right, bottom: r.bottom } : null;
  });
}

// A point of the backdrop: inside the viewport and outside the panel. It waits 400 ms first so the
// 150 ms open animation has finished and the panel is measured where it rests.
async function backdropPoint(page, dialog, what) {
  await page.waitForTimeout(400);
  const rect = await panelOf(dialog);
  assert(rect, `${what}: the open dialog has no panel to measure`);
  const { width, height } = page.viewportSize();
  const midX = (rect.left + rect.right) / 2, midY = (rect.top + rect.bottom) / 2;
  const candidates = [[rect.left - 24, midY], [rect.right + 24, midY], [midX, rect.top - 24], [midX, rect.bottom + 24], [8, 8], [width - 8, height - 8]];
  const outside = candidates.find(([x, y]) => x > 0 && x < width && y > 0 && y < height && (x < rect.left || x > rect.right || y < rect.top || y > rect.bottom));
  assert(outside, `${what}: no point of the backdrop is visible outside the ${Math.round(rect.right - rect.left)}×${Math.round(rect.bottom - rect.top)} panel`);
  return { x: outside[0], y: outside[1] };
}

async function clickBackdrop(page, dialog, what) {
  const point = await backdropPoint(page, dialog, what);
  await page.mouse.click(point.x, point.y);
}

// After an action: the dialog selector must still match exactly one open element.
async function staysOpen(page, selector, what) {
  await page.waitForTimeout(400);
  assert(await page.locator(`${selector}[open]`).count() === 1, `${what} closed the dialog; it must stay open`);
}

// Submits the inline create form on /admin/integrations. Resolves with the response (or throws when none comes).
async function submitIntegrationCreate(page) {
  const [response] = await Promise.all([
    page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/integrations', { timeout: 10000 }),
    page.locator('#integrationCreate button[data-mutation]').click(),
  ]);
  const body = await response.json().catch(() => null);
  return { ok: response.ok(), status: response.status(), body };
}

async function runConsistency(browser) {
  if (!adminPassword) throw new Error('set JELLY_ADMIN_PASSWORD (and JELLY_ADMIN_USER) for the consistency checks');
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: baseOrigin }).catch(() => {});
  await login(context);
  const stamp = Date.now().toString().slice(-8);
  const setup = await context.newPage();
  await setup.goto(`${base}/admin`);
  const post = await apiJSON(setup, 'POST', '/api/admin/posts', { title: `Jelly consistency ${stamp}`, status: 'draft', content: 'Consistency fixture.' });
  if (!post.ok || !post.body?.id) throw new Error(`creating the draft post fixture: HTTP ${post.status}`);
  const postID = post.body.id;
  const cleanups = [async () => { await apiJSON(setup, 'DELETE', `/api/admin/posts/${postID}`); }];
  const pagePath = (p) => p.replace('{post}', String(postID));

  try {
    await check('consistency: every listed admin page shows Jelly controls and no native checkbox/radio, .toggle-switch, role="switch", .modal-overlay or <dialog>', async () => {
      const failures = [];
      for (const item of consistencyPages) {
        const page = await consistencyPage(context);
        const path = pagePath(item.path);
        const status = await visit(page, base + path);
        if (status !== 200) { failures.push(`${path}: HTTP ${status}`); await page.close(); continue; }
        await jellyReady(page, 'jelly-toaster');
        await page.waitForTimeout(300);
        const found = await page.evaluate(({ needs, src }) => {
          const describe = eval(src);
          const shown = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden'; };
          // Light DOM only: a Jelly checkbox keeps its own native input inside its shadow root, which is fine.
          // #editorPreview holds the post's rendered content, whose task lists are not admin controls.
          const native = [...document.querySelectorAll('input[type="checkbox"], input[type="radio"]')].filter((el) => !el.closest('#editorPreview'));
          const list = (sel) => [...document.querySelectorAll(sel)].filter((el) => !el.closest('#editorPreview'));
          return {
            native: native.map((el) => describe(el) + (shown(el) ? ' (visible)' : ' (hidden)')),
            toggles: list('.toggle-switch').map(describe),
            switches: list('[role="switch"]').map(describe),
            overlays: list('.modal-overlay').map(describe),
            dialogs: list('dialog').map(describe),
            missing: needs.filter((anyOf) => !(Array.isArray(anyOf) ? anyOf : [anyOf]).some((tag) => document.querySelector(tag)))
              .map((anyOf) => (Array.isArray(anyOf) ? anyOf : [anyOf]).map((tag) => `<${tag}>`).join(' or ')),
          };
        }, { needs: item.need, src: describeSource });
        const show = (items) => items.slice(0, 4).join(', ') + (items.length > 4 ? `, … ${items.length - 4} more` : '');
        if (found.native.length) failures.push(`${path}: ${found.native.length} native checkbox/radio (${found.native.filter((d) => d.endsWith('(visible)')).length} visible): ${show(found.native)}`);
        if (found.toggles.length) failures.push(`${path}: .toggle-switch: ${show(found.toggles)}`);
        if (found.switches.length) failures.push(`${path}: hand-made role="switch": ${show(found.switches)}`);
        if (found.overlays.length) failures.push(`${path}: .modal-overlay: ${show(found.overlays)}`);
        if (found.dialogs.length) failures.push(`${path}: native <dialog>: ${show(found.dialogs)}`);
        if (found.missing.length) failures.push(`${path}: missing ${found.missing.join(', ')}`);
        for (const problem of problemsOf(page)) failures.push(`${path}: ${problem}`);
        if (page.nativeDialogs.length) failures.push(`${path}: browser dialogs on load: ${page.nativeDialogs.join(', ')}`);
        await page.close();
      }
      assert(failures.length === 0, failures.join('\n'));
    });

    await check('consistency: Jelly checkboxes and radios are size="sm", switches and segmented controls keep the default size, and a checked radio is visibly selected', async () => {
      const failures = [];
      for (const path of ['/admin/users', '/admin/products/new', `/admin/posts/${postID}`, '/admin/comments', '/admin/trash']) {
        const page = await consistencyPage(context);
        await visit(page, base + path);
        await jellyReady(page, 'jelly-toaster');
        await page.waitForTimeout(800); // let the pop animation of a selected control settle
        const report = await page.evaluate(() => {
          const out = { count: 0, problems: [] };
          const id = (el) => el.localName + (el.id ? '#' + el.id : '') + (el.getAttribute('name') ? `[name=${el.getAttribute('name')}]` : '') + (el.getAttribute('value') ? `[value=${el.getAttribute('value')}]` : '');
          for (const el of document.querySelectorAll('jelly-checkbox, jelly-radio, jelly-radio-group')) {
            out.count++;
            // jelly.js rewrites size="sm" to its canonical size="small" once the element is connected.
            if (!['sm', 'small'].includes(el.getAttribute('size'))) out.problems.push(`${id(el)} size=${JSON.stringify(el.getAttribute('size'))}, want "sm"`);
          }
          for (const el of document.querySelectorAll('jelly-switch, jelly-segmented')) {
            if (el.hasAttribute('size')) out.problems.push(`${id(el)} has size=${JSON.stringify(el.getAttribute('size'))}; switches and segmented controls keep the default size`);
          }
          const groups = new Map();
          for (const radio of document.querySelectorAll('jelly-radio')) {
            const name = radio.getAttribute('name') || '';
            if (!name) out.problems.push(`${id(radio)} has no name; radios join a group by name`);
            if (!groups.has(name)) groups.set(name, []);
            groups.get(name).push(radio);
          }
          // A chosen radio is the one with the checked attribute; its inner control must report it and its
          // canvas must paint differently from an unchosen sibling (the group element has no value upstream).
          const centre = (radio) => {
            const canvas = radio.shadowRoot?.querySelector('canvas');
            if (!canvas || !canvas.width) return null;
            const ctx = canvas.getContext('2d');
            const box = radio.shadowRoot.querySelector('[data-jelly-box]')?.getBoundingClientRect();
            const host = canvas.getBoundingClientRect();
            const x = box ? Math.round((box.left + box.width / 2 - host.left) * canvas.width / host.width) : Math.round(canvas.width / 2);
            const y = box ? Math.round((box.top + box.height / 2 - host.top) * canvas.height / host.height) : Math.round(canvas.height / 2);
            return [...ctx.getImageData(x, y, 1, 1).data].join(',');
          };
          for (const [name, radios] of groups) {
            const chosen = radios.filter((r) => r.hasAttribute('checked'));
            if (chosen.length > 1) out.problems.push(`radio group ${name}: ${chosen.length} radios carry checked`);
            for (const radio of radios) {
              const aria = radio.shadowRoot?.querySelector('[role="radio"]')?.getAttribute('aria-checked');
              if (aria !== String(radio.hasAttribute('checked'))) out.problems.push(`${id(radio)}: checked attribute ${radio.hasAttribute('checked')} but the control reports aria-checked=${aria}`);
            }
            const other = radios.find((r) => !r.hasAttribute('checked'));
            if (chosen[0] && other && chosen[0].getClientRects().length && other.getClientRects().length) {
              const a = centre(chosen[0]), b = centre(other);
              if (a !== null && a === b) out.problems.push(`radio group ${name}: the checked ${id(chosen[0])} paints the same as ${id(other)} (${a}); the selection is not visible`);
            }
          }
          return out;
        });
        if (report.count === 0) failures.push(`${path}: no <jelly-checkbox>, <jelly-radio-group> or <jelly-radio> to check`);
        for (const problem of report.problems) failures.push(`${path}: ${problem}`);
        await page.close();
      }
      assert(failures.length === 0, failures.join('\n'));
    });

    await check('consistency: a category flag switch toggles with Space and the save sends the same JSON booleans', async () => {
      const page = await consistencyPage(context);
      let createdID = null;
      try {
        await page.goto(`${base}/admin/taxonomy`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-switch');
        await page.click('#newBtn');
        await page.locator('#fName').waitFor({ state: 'visible', timeout: 5000 });
        await page.fill('#fName', `JUI cat ${stamp}`);
        const hide = await need(page, '#form jelly-switch[name="hide_from_list"]', 'the 不在列表顯示 flag', '#form input[name="hide_from_list"]');
        const cascade = await need(page, '#form jelly-switch[name="no_cascade"]', 'the 不併入上層 flag', '#form input[name="no_cascade"]');
        assert(!(await isChecked(hide)) && !(await isChecked(cascade)), 'a new category must start with both flags off');
        await hide.focus();
        await page.keyboard.press('Space');
        assert(await isChecked(hide), 'Space did not turn the hide_from_list switch on');
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/categories'), { timeout: 5000 }),
          page.click('#saveBtn'),
        ]);
        const body = request.postDataJSON();
        createdID = (await (await request.response())?.json().catch(() => null))?.id ?? null;
        assertShape('POST /api/admin/categories', body, {
          name: 'string', slug: 'string', color: 'string', description: 'string', parent_id: 'null', cover: 'string',
          hide_from_list: 'boolean', no_cascade: 'boolean',
        });
        assert(body.hide_from_list === true && body.no_cascade === false, `flags sent hide_from_list=${body.hide_from_list} no_cascade=${body.no_cascade}, want true and false`);
        assertCleanPage(page);
      } finally {
        if (createdID) await apiJSON(page, 'DELETE', `/api/admin/categories/${createdID}`);
        await page.close();
      }
    });

    await check('consistency: the coupon type segmented control moves with the arrow keys and the create request keeps its fields and types', async () => {
      const page = await consistencyPage(context);
      let createdID = null;
      try {
        await page.goto(`${base}/admin/coupons`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-segmented', 'jelly-switch');
        await page.click('#newBtn');
        await page.locator('#fCode').waitFor({ state: 'visible', timeout: 5000 });
        const kind = await need(page, '#cf jelly-segmented[name="kind"]', 'the coupon 類型 choice', '#cf input[name="kind"]');
        await need(page, '#cf jelly-segmented[name="scope"]', 'the coupon 適用 choice', '#cf input[name="scope"]');
        assert(await kind.getAttribute('value') === 'percent', `a new coupon starts as ${await kind.getAttribute('value')}, want percent`);
        await kind.locator('button[role="radio"][aria-checked="true"]').focus();
        await page.keyboard.press('ArrowRight');
        assert(await kind.getAttribute('value') === 'fixed', `ArrowRight moved the type to ${await kind.getAttribute('value')}, want fixed`);
        await page.locator('#fFixed').waitFor({ state: 'visible', timeout: 3000 }).catch(() => { throw new Error('choosing 固定金額 by keyboard did not show the amount field (the page must listen to the segmented change event)'); });
        await page.fill('#fCode', `JUIC${stamp}`);
        await page.fill('#fFixed', '120');
        const stack = await need(page, '#fStack', 'the 可與免運疊加 switch');
        assert(await stack.evaluate((el) => el.localName) === 'jelly-switch', '#fStack is not a <jelly-switch>');
        await stack.focus();
        await page.keyboard.press('Space');
        assert(await isChecked(stack), 'Space did not turn the stackable switch on');
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/coupons'), { timeout: 5000 }),
          page.click('#saveBtn'),
        ]);
        const body = request.postDataJSON();
        createdID = (await (await request.response())?.json().catch(() => null))?.id ?? null;
        assertShape('POST /api/admin/coupons', body, {
          label: 'string', kind: 'string', value: 'number', buy_qty: 'number', get_qty: 'number', scope: 'string', code: 'string',
          min_subtotal: 'number', usage_limit: 'null', per_user_limit: 'null', starts_at: 'null', ends_at: 'null', bound_email: 'string',
          stackable_with_free_shipping: 'boolean', members_only: 'boolean', status: 'string', targets: 'array',
        });
        const want = { kind: 'fixed', value: 12000, scope: 'all', stackable_with_free_shipping: true, members_only: false, status: 'active' };
        const wrong = Object.entries(want).filter(([k, v]) => body[k] !== v).map(([k, v]) => `${k}=${JSON.stringify(body[k])} (want ${JSON.stringify(v)})`);
        assert(wrong.length === 0 && body.targets.length === 0, `coupon values changed: ${wrong.join(', ')} targets=${JSON.stringify(body.targets)}`);
        assertCleanPage(page);
      } finally {
        if (createdID) await apiJSON(page, 'DELETE', `/api/admin/coupons/${createdID}`);
        await page.close();
      }
    });

    await check('consistency: the invite role radio cards move with the arrow keys and the invite sends the same email, role and note', async () => {
      const page = await consistencyPage(context);
      try {
        // The invitation is answered here so the review database gets no invited user.
        await page.route('**/api/admin/users/invite', (route) => route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 0, invite_url: '/invite/consistency-check', emailed: false }) }));
        await page.goto(`${base}/admin/users`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-radio', 'jelly-radio-group');
        await need(page, '#inviteForm jelly-radio-group', 'the invite 角色 group', '#inviteForm input[name="role"]');
        const editor = await need(page, '#inviteForm jelly-radio[name="role"][value="editor"]', 'the 編輯 role card', '#inviteForm input[name="role"]');
        const author = page.locator('#inviteForm jelly-radio[name="role"][value="author"]');
        assert(await isChecked(editor) && !(await isChecked(author)), 'the invite starts with 編輯 (editor) chosen');
        await editor.focus();
        await page.keyboard.press('ArrowDown');
        assert(await isChecked(author) && !(await isChecked(editor)), 'ArrowDown from 編輯 did not choose 作者 (author) and clear 編輯');
        await page.fill('#inviteEmail', `jui-${stamp}@example.test`);
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/users/invite'), { timeout: 5000 }),
          page.click('#sendInvite'),
        ]);
        const body = request.postDataJSON();
        assertShape('POST /api/admin/users/invite', body, { email: 'string', role: 'string', note: 'string' });
        assert(body.role === 'author' && body.email === `jui-${stamp}@example.test` && body.note === '', `invite sent ${JSON.stringify(body)}`);
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });

    await check('consistency: the 需要運送 checkbox toggles with Space and the product save sends the same booleans', async () => {
      const page = await consistencyPage(context);
      let createdID = null;
      try {
        await page.goto(`${base}/admin/products/new`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-checkbox', 'jelly-segmented');
        const ship = await need(page, 'jelly-checkbox#fShip', 'the 需要運送 checkbox', 'input#fShip');
        const cold = await need(page, 'jelly-checkbox#fCold', 'the 冷藏 checkbox', 'input#fCold');
        await need(page, 'jelly-segmented[name="status"]', 'the product 狀態 choice', 'input[name="status"]');
        assert(await isChecked(ship) && !(await isChecked(cold)), 'a new product starts with 需要運送 on and 冷藏 off');
        await page.fill('#fTitle', `JUI product ${stamp}`);
        await page.fill('#fPrice', '100');
        await ship.focus();
        await page.keyboard.press('Space');
        assert(!(await isChecked(ship)), 'Space did not untick 需要運送');
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/products'), { timeout: 5000 }),
          page.click('#saveBtn'),
        ]);
        const body = request.postDataJSON();
        const response = await (await request.response())?.json().catch(() => null);
        createdID = response?.id ?? response?.product?.id ?? null;
        assertShape('POST /api/admin/products', body, {
          title: 'string', slug: 'string', description: 'string', price: 'string', compare_at_price: 'string', status: 'string',
          low_stock_threshold: 'number', requires_shipping: 'boolean', cold_chain: 'boolean', images: 'array', categories: 'array',
          variants: 'array', seo_title: 'string', seo_description: 'string', seo_index: 'string', seo_ai: 'string',
        });
        assert(body.requires_shipping === false && body.cold_chain === false && body.status === 'draft',
          `product sent requires_shipping=${body.requires_shipping} cold_chain=${body.cold_chain} status=${body.status}, want false, false, draft`);
        assertCleanPage(page);
      } finally {
        if (createdID) await apiJSON(page, 'DELETE', `/api/admin/products/${createdID}`);
        await page.close();
      }
    });

    await check('consistency: the theme customizer is a jelly-dialog, its layout checkboxes post the same JSON, and saving shows a Jelly toast', async () => {
      const page = await consistencyPage(context);
      try {
        // Known saved settings, so the checkbox states do not depend on the review database; the save is answered here.
        await page.route('**/api/admin/themes/default/settings', (route) => route.request().method() === 'GET'
          ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ layout: JSON.stringify({ sidebar: false, footer: true }) }) })
          : route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }));
        await page.goto(`${base}/admin/themes`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog', 'jelly-checkbox');
        const opener = page.getByRole('button', { name: /客製化|Customize/ }).first();
        await opener.click();
        const dialog = await openedJellyDialog(page, 'the theme customizer');
        await need(page, 'jelly-dialog[open] #customizeForm', 'the customizer form inside the dialog');
        const sidebar = await need(page, '#customizeForm jelly-checkbox[name="layout_sidebar"]', 'the Sidebar layout checkbox', '#customizeForm input[name="layout_sidebar"]');
        const footer = await need(page, '#customizeForm jelly-checkbox[name="layout_footer"]', 'the Footer layout checkbox', '#customizeForm input[name="layout_footer"]');
        assert(!(await isChecked(sidebar)) && await isChecked(footer), 'the saved layout (sidebar off, footer on) is not shown');
        await sidebar.click();
        await footer.focus();
        await page.keyboard.press('Space');
        assert(await isChecked(sidebar) && !(await isChecked(footer)), 'clicking Sidebar and Space on Footer did not flip both');
        let request;
        const toast = await toastAfter(page, async () => {
          [request] = await Promise.all([
            page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/themes/default/settings'), { timeout: 5000 }),
            dialog.locator('#customizeForm button[type="submit"]').click(),
          ]);
        }, 'saving theme settings');
        const body = request.postDataJSON();
        assertShape('POST /api/admin/themes/default/settings', body, { colors: 'string', layout: 'string', custom: 'string' });
        const layout = JSON.parse(body.layout);
        assert(layout.sidebar === true, `layout.sidebar = ${JSON.stringify(layout.sidebar)}, want the boolean true`);
        assert(!Object.prototype.hasOwnProperty.call(layout, 'footer'), `layout.footer = ${JSON.stringify(layout.footer)}; an unticked layout checkbox is left out, as before`);
        assert(toast.tone === 'Success', `the save toast is ${toast.tone || 'untoned'}: ${toast.text}; want kind success`);
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });

    await check('consistency: the notices that used alert() are Jelly toasts (media copy URL, theme activation, OAuth save failure)', async () => {
      const failures = [];
      const attempt = async (what, run) => { try { await run(); } catch (error) { failures.push(`${what}: ${error.message}`); } };

      await attempt('media: copy URL', async () => {
        const page = await consistencyPage(context);
        try {
          await page.route('**/api/admin/media', (route) => route.request().method() === 'GET'
            ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(['/uploads/jelly-consistency.txt']) })
            : route.continue());
          await page.goto(`${base}/admin/media?lang=zh-TW`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-toaster');
          const copy = page.locator('#mediaGrid .media-item button').first();
          await copy.waitFor({ state: 'visible', timeout: 5000 });
          const toast = await toastAfter(page, () => copy.click(), 'copying a media URL');
          const copied = await page.evaluate(() => navigator.clipboard.readText().catch(() => null));
          if (copied === '/uploads/jelly-consistency.txt') assert(toast.tone === 'Success', `the copy toast is ${toast.tone}: ${toast.text}; want kind success`);
          assert(/[\u4e00-\u9fff]/.test(toast.text), `the zh-TW copy toast reads ${JSON.stringify(toast.text)}; the hard-coded English message must become an i18n key`);
          assertCleanPage(page);
        } finally { await page.close(); }
      });

      await attempt('themes: activation success and failure', async () => {
        const page = await consistencyPage(context);
        try {
          let activation = 200;
          await page.route('**/api/admin/themes', (route) => route.request().method() === 'GET'
            ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(['default', 'jelly-consistency']) })
            : route.continue());
          await page.route('**/api/admin/themes/jelly-consistency/config', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ name: 'Jelly Consistency', version: '1.0.0' }) }));
          await page.route('**/api/admin/themes/activate', (route) => route.fulfill({ status: activation, contentType: 'application/json', body: JSON.stringify(activation === 200 ? { ok: true } : { error: 'Injected activation failure' }) }));
          await page.goto(`${base}/admin/themes`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-toaster');
          const card = page.locator('.theme-card', { hasText: 'Jelly Consistency' });
          const activate = card.getByRole('button', { name: /啟用|Activate/ });
          await activate.waitFor({ state: 'visible', timeout: 5000 });
          const ok = await toastAfter(page, () => activate.click(), 'activating a theme');
          assert(ok.tone === 'Success', `the activation toast is ${ok.tone}: ${ok.text}; want kind success`);
          activation = 500;
          await activate.waitFor({ state: 'visible', timeout: 5000 });
          const bad = await toastAfter(page, () => activate.click(), 'a failed theme activation');
          assert(bad.tone === 'Error', `the failed activation toast is ${bad.tone}: ${bad.text}; want kind error`);
          assertCleanPage(page, [/HTTP 500: .*\/api\/admin\/themes\/activate/]);
        } finally { await page.close(); }
      });

      await attempt('settings: Google OAuth save failure', async () => {
        const page = await consistencyPage(context);
        try {
          await page.route('**/api/admin/oauth/providers/google', (route) => route.request().method() === 'POST'
            ? route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'Injected OAuth failure' }) })
            : route.continue());
          await page.goto(`${base}/admin/settings/auth?lang=zh-TW`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-switch', 'jelly-toaster');
          await page.locator('#googleEnabled').waitFor({ state: 'visible', timeout: 5000 });
          if (!(await isChecked(page.locator('#googleEnabled')))) { await page.focus('#googleEnabled'); await page.keyboard.press('Space'); }
          await page.fill('#googleClientID', `jelly-consistency-${stamp}`);
          await page.fill('#googleClientSecret', 'jelly-consistency-secret');
          const bad = await toastAfter(page, () => page.click('#googleSaveBtn'), 'a failed Google OAuth save');
          assert(bad.tone === 'Error', `the OAuth failure toast is ${bad.tone}: ${bad.text}; want kind error`);
          assert(/[\u4e00-\u9fff]/.test(bad.text), `the zh-TW OAuth failure toast reads ${JSON.stringify(bad.text)}; the hard-coded English message must become an i18n key`);
          assertCleanPage(page, [/HTTP 500: .*\/api\/admin\/oauth\/providers\/google/]);
        } finally { await page.close(); }
      });

      assert(failures.length === 0, failures.join('\n'));
    });

    await check('consistency: the coupon batch sheet is a jelly-dialog; Escape and ✕ close it, focus returns, and its form still submits', async () => {
      const page = await consistencyPage(context);
      try {
        await page.route('**/api/admin/coupons/generate', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ codes: [`JUIB${stamp}A`, `JUIB${stamp}B`] }) }));
        await page.goto(`${base}/admin/coupons`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog');
        await page.click('#newBtn');
        await page.locator('#fPercent').waitFor({ state: 'visible', timeout: 5000 });
        await page.fill('#fPercent', '10'); // the batch uses the coupon form as its template
        const opener = page.locator('#batchOpen');
        await opener.click();
        await openedJellyDialog(page, 'the coupon batch sheet');
        await need(page, 'jelly-dialog[open] form#bf', 'the batch form inside the dialog');
        await page.keyboard.press('Escape');
        await closedAndFocusReturned(page, opener, 'Escape', 'the coupon batch sheet');
        await opener.click();
        await openedJellyDialog(page, 'the coupon batch sheet (second time)');
        await page.locator('jelly-dialog[open]:not([data-jelly-confirm]) button.close').click();
        await closedAndFocusReturned(page, opener, 'the ✕ button', 'the coupon batch sheet');
        await opener.click();
        const dialog = await openedJellyDialog(page, 'the coupon batch sheet (third time)');
        await page.fill('#bPrefix', 'JUIB');
        await page.fill('#bSize', '2');
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/coupons/generate'), { timeout: 5000 }),
          page.click('#batchBtn'),
        ]);
        const body = request.postDataJSON();
        assertShape('POST /api/admin/coupons/generate', body, { prefix: 'string', size: 'number', template: 'object' });
        assert(body.prefix === 'JUIB' && body.size === 2 && body.template.kind === 'percent' && body.template.value === 10 &&
          body.template.usage_limit === 1 && body.template.per_user_limit === 1, `batch sent ${JSON.stringify(body)}`);
        await page.locator('#batchOut').waitFor({ state: 'visible', timeout: 5000 });
        assert(await dialog.isVisible(), 'the batch result must stay in the open dialog');
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });

    await check('consistency: the new-menu sheet is a jelly-dialog; a backdrop click closes it, focus returns, and its form still creates the menu', async () => {
      const page = await consistencyPage(context);
      let createdID = null;
      try {
        await page.goto(`${base}/admin/menus`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog');
        const opener = page.locator('#newMenuBtn');
        await opener.click();
        await openedJellyDialog(page, 'the new-menu sheet');
        await need(page, 'jelly-dialog[open] #msName', 'the menu name field inside the dialog');
        await page.mouse.click(4, 400);
        await closedAndFocusReturned(page, opener, 'a backdrop click', 'the new-menu sheet');
        await opener.click();
        await openedJellyDialog(page, 'the new-menu sheet (second time)');
        await page.fill('jelly-dialog[open] #msName', `JUI menu ${stamp}`);
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'POST' && r.url().endsWith('/api/admin/menus'), { timeout: 5000 }),
          page.press('jelly-dialog[open] #msName', 'Enter'),
        ]);
        const body = request.postDataJSON();
        createdID = (await (await request.response())?.json().catch(() => null))?.id ?? null;
        assertShape('POST /api/admin/menus', body, { name: 'string', location: 'string' });
        assert(body.name === `JUI menu ${stamp}`, `menu sent ${JSON.stringify(body)}`);
        await page.waitForFunction(() => !document.querySelector('jelly-dialog[open]:not([data-jelly-confirm])'), null, { timeout: 5000 })
          .catch(() => { throw new Error('the dialog stayed open after the menu was created'); });
        assertCleanPage(page);
      } finally {
        if (createdID) await apiJSON(page, 'DELETE', `/api/admin/menus/${createdID}`);
        await page.close();
      }
    });

    await check('consistency: Escape in a confirmation over the menu settings jelly-dialog closes only the confirmation', async () => {
      const page = await consistencyPage(context);
      await page.goto(`${base}/admin`, { waitUntil: 'networkidle' });
      const made = await apiJSON(page, 'POST', '/api/admin/menus', { name: `JUI nested ${stamp}` });
      assert(made.ok && made.body?.id, `creating the menu fixture: HTTP ${made.status}`);
      const deletes = [];
      page.on('request', (r) => { if (r.method() === 'DELETE' && r.url().endsWith(`/api/admin/menus/${made.body.id}`)) deletes.push(r.url()); });
      try {
        await page.goto(`${base}/admin/menus`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog');
        await page.locator(`#menuList button[data-id="${made.body.id}"]`).click();
        const settings = page.locator('#treeMount #menuSettings');
        await settings.waitFor({ state: 'visible', timeout: 5000 });
        await settings.click();
        const outer = await openedJellyDialog(page, 'the menu settings sheet');
        const name = await need(page, 'jelly-dialog[open] #msName', 'the menu name field inside the dialog');
        await name.fill(`JUI nested ${stamp} unsaved`);
        const del = outer.locator('[data-del]');
        await del.click();
        const confirm = page.locator('jelly-dialog[open][data-jelly-confirm]');
        await confirm.waitFor({ state: 'visible', timeout: 5000 });
        await page.keyboard.press('Escape');
        await confirm.waitFor({ state: 'hidden', timeout: 5000 });
        await page.waitForTimeout(400);
        assert(await page.locator('jelly-dialog[open]:not([data-jelly-confirm])').count() === 1, 'Escape in the confirmation also closed the menu settings dialog; only the top dialog may close');
        assert(await name.inputValue() === `JUI nested ${stamp} unsaved`, 'cancelling the confirmation lost the unsaved menu name');
        assert(await del.evaluate((el) => document.activeElement === el), 'focus must return to the delete button that opened the confirmation');
        assert(deletes.length === 0, 'cancelling the confirmation sent a delete request');
        await page.keyboard.press('Escape');
        await closedAndFocusReturned(page, settings, 'a second Escape', 'the menu settings sheet');
        assertCleanPage(page);
      } finally {
        await apiJSON(page, 'DELETE', `/api/admin/menus/${made.body.id}`);
        await page.close();
      }
    });

    await check('consistency: the editor export is a jelly-dialog with radio format cards and a frontmatter checkbox, and downloads with the same query', async () => {
      const page = await consistencyPage(context);
      try {
        await page.route(`**/api/admin/posts/${postID}/export?**`, (route) => route.fulfill({ status: 200, contentType: 'text/markdown', body: '# Consistency\n', headers: { 'Content-Disposition': 'attachment; filename="consistency.md"' } }));
        await page.goto(`${base}/admin/posts/${postID}`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog', 'jelly-radio', 'jelly-checkbox');
        const opener = page.locator('[data-action="export"]').first();
        await opener.click();
        const dialog = await openedJellyDialog(page, 'the editor export dialog');
        assert(await dialog.getAttribute('id') === 'editorExport', `the open export dialog is #${await dialog.getAttribute('id')}, want <jelly-dialog id="editorExport">`);
        const jmd = await need(page, '#editorExport jelly-radio[value="jmd"]', 'the Jelly Markdown format card');
        const md = page.locator('#editorExport jelly-radio[value="md"]');
        assert(await isChecked(jmd) && !(await isChecked(md)), 'the export starts with Jelly Markdown chosen');
        await jmd.focus();
        await page.keyboard.press('ArrowRight');
        assert(await isChecked(md) && !(await isChecked(jmd)), 'ArrowRight from Jelly Markdown did not choose 純 Markdown (md)');
        await page.locator('#editorExport [data-format="html"]').click();
        assert(await isChecked(page.locator('#editorExport jelly-radio[value="html"]')) && !(await isChecked(md)), 'clicking the HTML card did not choose html');
        await page.locator('#editorExport [data-format="md"]').click();
        const front = await need(page, '#editorExport jelly-checkbox#exportFrontmatter', 'the frontmatter checkbox', '#exportFrontmatter');
        await front.focus();
        await page.keyboard.press('Space');
        assert(await isChecked(front), 'Space did not tick the frontmatter checkbox');
        const [request] = await Promise.all([
          page.waitForRequest((r) => r.method() === 'GET' && r.url().includes(`/api/admin/posts/${postID}/export?`), { timeout: 10000 }),
          page.click('#exportDownload'),
        ]);
        const query = new URL(request.url()).searchParams;
        assert(query.toString() === 'format=md&frontmatter=1', `the export asked for ?${query}, want ?format=md&frontmatter=1`);
        await page.keyboard.press('Escape');
        await closedAndFocusReturned(page, opener, 'Escape', 'the editor export dialog');
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });

    await check('consistency: no listed page overflows sideways at 390px in light or dark mode', async () => {
      const failures = [];
      for (const scheme of ['light', 'dark']) {
        for (const item of consistencyPages) {
          const page = await consistencyPage(context, 390);
          await page.emulateMedia({ colorScheme: scheme });
          const path = pagePath(item.path);
          await visit(page, base + path);
          await jellyReady(page, 'jelly-toaster');
          await page.waitForTimeout(200);
          const m = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, inner: window.innerWidth, mode: document.documentElement.getAttribute('data-jelly-mode') }));
          if (m.mode !== scheme) failures.push(`${scheme} ${path}: data-jelly-mode is ${m.mode}`);
          if (m.scroll > m.inner) failures.push(`${scheme} ${path}: scrollWidth ${m.scroll} > innerWidth ${m.inner}`);
          await page.close();
        }
      }
      assert(failures.length === 0, failures.join('\n'));
    });

    await check('consistency: the integration create form resets after a create: no permission stays ticked, and the next create sends no permission it did not ask for', async () => {
      const page = await consistencyPage(context);
      const made = [];
      try {
        await page.goto(`${base}/admin/integrations`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-checkbox', 'jelly-dialog');
        const form = await need(page, '#integrationCreate', 'the create integration card');
        await page.locator('#integrationCreate input[name="name"]').fill(`JUI reset A ${stamp}`);
        const first = await need(page, '#integrationCreate jelly-checkbox[name="permission"]', 'a permission checkbox in the create form');
        await first.click();
        assert(await first.evaluate((el) => el.hasAttribute('checked')), 'the first permission did not tick');
        const created = await submitIntegrationCreate(page);
        assert(created.ok && created.body?.integration?.id, `creating integration A: HTTP ${created.status}`);
        made.push(created.body.integration.id);
        await page.getByRole('button', { name: /我已複製，關閉|I have copied it, close/ }).click();
        await page.waitForFunction(() => !document.querySelector('jelly-dialog#integrationDialog[open]'), null, { timeout: 5000 });
        const stillTicked = await form.locator('jelly-checkbox[name="permission"][checked]').count();
        assert(stillTicked === 0, `after a successful create ${stillTicked} permission checkbox(es) in the create form are still ticked; the form must return to its first state`);

        await page.locator('#integrationCreate input[name="name"]').fill(`JUI reset B ${stamp}`);
        const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/admin/integrations', { timeout: 5000 }).catch(() => null);
        await page.locator('#integrationCreate button[data-mutation]').click();
        const request = await posted;
        if (request) {
          // main never sends a create with no permission ticked (it shows the one-permission message), so any request must carry []
          const body = request.postDataJSON();
          assert(JSON.stringify(body.permissions) === '[]', `the second create sent permissions ${JSON.stringify(body.permissions)}; with none ticked the payload has permissions []`);
          const json = await (await request.response())?.json().catch(() => null);
          if (json?.integration?.id) made.push(json.integration.id);
        } else {
          // main never sends a create with no permission ticked: it shows the one-permission message instead
          const feedback = await page.locator('#integrationCreate .integration-error').innerText();
          assert(/至少選擇一項權限|at least one permission/i.test(feedback), `the second create sent no request and the form says "${feedback}"; want the one-permission message`);
        }
        assertCleanPage(page);
      } finally {
        for (const id of made) await apiJSON(page, 'DELETE', `/api/admin/integrations/${id}`);
        await page.close();
      }
    });

    await check('consistency: the coupon batch sheet ignores a press inside it that is released on the backdrop, and a plain backdrop click still closes it', async () => {
      const page = await consistencyPage(context);
      try {
        await page.goto(`${base}/admin/coupons`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-dialog');
        const opener = page.locator('#batchOpen');
        await opener.click();
        const dialog = await openedJellyDialog(page, 'the coupon batch sheet');
        const field = await need(page, 'jelly-dialog[open] #bPrefix', 'the batch prefix field inside the dialog');
        const box = await field.boundingBox();
        assert(box, 'the batch prefix field has no box to press');
        const point = await backdropPoint(page, dialog, 'the coupon batch sheet');
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.down();
        await page.mouse.move(point.x, point.y, { steps: 10 });
        await page.mouse.up();
        await staysOpen(page, 'jelly-dialog#batchSheet', 'pressing inside the sheet and releasing on the backdrop');
        await clickBackdrop(page, dialog, 'the coupon batch sheet');
        await closedAndFocusReturned(page, opener, 'a plain backdrop click', 'the coupon batch sheet');
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });

    await check('consistency: dialogs that do not close on a backdrop click on main stay open on one and still close with Escape', async () => {
      const page = await consistencyPage(context);
      const made = [];
      try {
        const failures = [];
        const part = async (label, fn) => { try { await fn(); } catch (e) { failures.push(`${label}: ${e.message}`); } };
        await part('(a) the one-time key dialog', async () => {
          await page.goto(`${base}/admin/integrations`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-checkbox', 'jelly-dialog');
          await page.locator('#integrationCreate input[name="name"]').fill(`JUI key ${stamp}`);
          await need(page, '#integrationCreate jelly-checkbox[name="permission"]', 'a permission checkbox in the create form');
          await page.locator('#integrationCreate jelly-checkbox[name="permission"]').first().click();
          const created = await submitIntegrationCreate(page);
          assert(created.ok && created.body?.integration?.id, `creating the integration for the key dialog: HTTP ${created.status}`);
          made.push(created.body.integration.id);
          await page.waitForSelector('jelly-dialog#integrationDialog[open] #integrationSecret', { timeout: 5000 }).catch(() => { throw new Error('(a) the one-time key dialog did not open with the key'); });
          const key = await page.locator('#integrationSecret').innerText();
          assert(key.startsWith('jly_'), `(a) the one-time key starts with ${key.slice(0, 6)}, want jly_`);
          await clickBackdrop(page, page.locator('jelly-dialog#integrationDialog'), '(a) the one-time key dialog');
          await staysOpen(page, 'jelly-dialog#integrationDialog', '(a) a backdrop click on the one-time key dialog');
          assert(await page.locator('#integrationSecret').innerText() === key, '(a) the key text changed or disappeared after a backdrop click');
          await page.keyboard.press('Escape');
          await page.waitForFunction(() => !document.querySelector('jelly-dialog#integrationDialog[open]'), null, { timeout: 5000 })
            .catch(() => { throw new Error('(a) Escape did not close the one-time key dialog'); });
        });

        await part('(b) the theme customizer', async () => {
          await page.goto(`${base}/admin/themes`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-dialog', 'jelly-toaster');
          const customizeOpener = page.getByRole('button', { name: /客製化|Customize/ }).first();
          await customizeOpener.click();
          const customizer = await openedJellyDialog(page, '(b) the theme customizer');
          const sentinel = `JUI backdrop ${stamp}`;
          const value = await need(page, 'jelly-dialog[open] #customizeForm input[type="text"]:visible', '(b) a text field inside the theme customizer');
          await value.fill(sentinel);
          await clickBackdrop(page, customizer, '(b) the theme customizer');
          await staysOpen(page, 'jelly-dialog#customizeModal', '(b) a backdrop click on the theme customizer');
          assert(await value.inputValue() === sentinel, '(b) the edited theme value was lost after a backdrop click');
          await page.keyboard.press('Escape');
          await closedAndFocusReturned(page, customizeOpener, 'Escape', '(b) the theme customizer');
        });

        await part('(c) the editor autosave-restore prompt', async () => {
          await page.goto(`${base}/admin/posts/new`, { waitUntil: 'networkidle' });
          const draftKey = await page.evaluate(() => {
            const user = window.JellyEditorUser || {};
            return 'jelly.editor.' + (user.ID || user.id || user.Username || user.username || 'self') + '.draft.0';
          });
          try {
            await page.evaluate(([k, title]) => localStorage.setItem(k, JSON.stringify({ title, source: 'Restore fixture.', excerpt: '', draft: null, at: Date.now(), base: null })), [draftKey, `JUI restore ${stamp}`]);
            await page.reload({ waitUntil: 'networkidle' });
            await page.waitForSelector('jelly-dialog#editorRestore[open]', { timeout: 5000 }).catch(() => { throw new Error('(c) the autosave restore prompt did not open for a local draft'); });
            await clickBackdrop(page, page.locator('jelly-dialog#editorRestore'), '(c) the autosave restore prompt');
            await staysOpen(page, 'jelly-dialog#editorRestore', '(c) a backdrop click on the autosave restore prompt');
            assert(await page.locator('#restoreAutosave').isVisible(), '(c) the restore button is gone after a backdrop click');
            await page.keyboard.press('Escape');
            await page.waitForFunction(() => !document.querySelector('jelly-dialog#editorRestore[open]'), null, { timeout: 5000 })
              .catch(() => { throw new Error('(c) Escape did not close the autosave restore prompt'); });
          } finally {
            await page.evaluate((k) => localStorage.removeItem(k), draftKey).catch(() => {});
          }
        });

        await part('(d) the custom crawler rule form', async () => {
          await page.goto(`${base}/admin/settings/crawlers`, { waitUntil: 'networkidle' });
          await jellyReady(page, 'jelly-dialog');
          const ruleOpener = page.locator('[data-action="add-ai-rule"]').first();
          await ruleOpener.click();
          const ruleDialog = await openedJellyDialog(page, '(d) the custom crawler rule form');
          const agent = await need(page, 'jelly-dialog[open] input[name="user_agent"]', '(d) the user agent field of the custom rule');
          await agent.fill(`JUIBot-${stamp}`);
          await clickBackdrop(page, ruleDialog, '(d) the custom crawler rule form');
          await staysOpen(page, 'jelly-dialog.sys-dialog', '(d) a backdrop click on the custom crawler rule form');
          assert(await agent.inputValue() === `JUIBot-${stamp}`, '(d) the typed user agent was lost after a backdrop click');
          await page.keyboard.press('Escape');
          await closedAndFocusReturned(page, ruleOpener, 'Escape', '(d) the custom crawler rule form');
        });

        assert(failures.length === 0, failures.join('\n'));
        assertCleanPage(page);
      } finally {
        for (const id of made) await apiJSON(page, 'DELETE', `/api/admin/integrations/${id}`);
        await page.close();
      }
    });

    await check('consistency: changing the notification filter while a mark-read request is in flight reloads the list for the new category once that request fails', async () => {
      const page = await consistencyPage(context);
      const fixtureID = 9000001;
      const listed = [];
      let failedAt = 0;
      try {
        const fixture = { items: [{ id: fixtureID, kind: 'comment.pending', category: 'comments', params: { message: `JUI fixture ${stamp}` }, read_at: null, created_at: new Date().toISOString() }], counts: { all: 1, unread: 1, comments: 1, orders: 0, system: 0, security: 0 } };
        await page.route(/\/api\/account\/notifications\?/, (route) => {
          listed.push({ category: new URL(route.request().url()).searchParams.get('category') || '', at: Date.now() });
          route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(fixture) });
        });
        // The mark-read request takes about 1.5 s and then fails with 500 (the list is not refreshed by a failure).
        await page.route(`**/api/account/notifications/${fixtureID}/read`, async (route) => {
          await new Promise((resolve) => setTimeout(resolve, 1500));
          failedAt = Date.now();
          await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'JUI forced failure' }) });
        });
        await page.goto(`${base}/admin/notifications`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-segmented', 'jelly-switch');
        const markRead = await need(page, '#notificationList [data-mark-read]', 'a Mark read button in the list');
        await markRead.click();
        // Change the filter while the mark-read request is still in flight (the segment is chosen by its value).
        await page.locator('#notificationFilters button[role="radio"]', { hasText: /訂單|Orders/ }).first().click();
        // The request fails, then the list must be requested again for orders within 5 s.
        const waitingFrom = Date.now();
        while (!failedAt && Date.now() - waitingFrom < 10000) await new Promise((r) => setTimeout(r, 100));
        assert(failedAt, 'the mark-read request never reached the server');
        while (Date.now() - failedAt < 5000 && !listed.some((l) => l.category === 'orders' && l.at >= failedAt)) await new Promise((r) => setTimeout(r, 100));
        const reload = listed.find((l) => l.category === 'orders' && l.at >= failedAt);
        const since = listed.filter((l) => l.at >= failedAt).map((l) => l.category || 'all');
        assert(reload, `no list request for category=orders within 5 s after the failed mark-read (list requests after it: ${since.length ? since.join(', ') : 'none'}); the list did not reload for the new filter`);
        const selected = await page.locator('#notificationFilters').getAttribute('value');
        assert(selected === 'orders', `the filter shows ${selected}, want orders`);
        assertCleanPage(page, [new RegExp(`HTTP 500: .*/api/account/notifications/${fixtureID}/read`)]);
      } finally {
        await page.close();
      }
    });

    await check('consistency: clicking the selected period segment while a search is typed clears the search and reloads that period without q', async () => {
      const page = await consistencyPage(context);
      try {
        await page.goto(`${base}/admin/coupons`, { waitUntil: 'networkidle' });
        await jellyReady(page, 'jelly-segmented');
        const period = await page.locator('#segs').getAttribute('value');
        const query = `JUIQ${stamp}`;
        await Promise.all([
          page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === '/api/admin/coupons' && new URL(r.url()).searchParams.get('q') === query, { timeout: 5000 }),
          page.fill('#q', query),
        ]);
        const listed = page.waitForRequest((r) => r.method() === 'GET' && new URL(r.url()).pathname === '/api/admin/coupons' && !new URL(r.url()).searchParams.has('q'), { timeout: 5000 }).catch(() => null);
        await page.locator('#segs button[role="radio"][aria-checked="true"]').click();
        const request = await listed;
        assert(request, `clicking the selected period (${period}) sent no list request without q; the search is still on screen`);
        const params = new URL(request.url()).searchParams;
        assert(params.get('status') === period, `the list request asked status=${params.get('status')}, want status=${period}`);
        assert(await page.locator('#q').inputValue() === '', 'clicking the selected period did not clear the search box');
        assertCleanPage(page);
      } finally {
        await page.close();
      }
    });
  } finally {
    for (const cleanup of cleanups.reverse()) await cleanup().catch(() => {});
    await setup.close();
    await context.close();
  }
}

async function runPublic(browser) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  await check('public: login, home, posts, search and 404 load offline with the self-hosted fonts', async () => {
    const failures = [];
    for (const p of publicPaths) {
      const page = await openPage(context, [baseOrigin]);
      const want = p.startsWith('/no-such') ? 404 : 200;
      const status = await visit(page, base + p);
      if (status !== want) failures.push(`${p}: HTTP ${status}, want ${want}`);
      if (!(await fontsLoaded(page))) failures.push(`${p}: fonts did not load`);
      for (const problem of problemsOf(page)) failures.push(`${p}: ${problem}`);
      await page.close();
    }
    assert(failures.length === 0, failures.join('\n'));
  });
  await context.close();
}

function serveDesign() {
  const types = { '.html': 'text/html; charset=utf-8', '.css': 'text/css', '.woff2': 'font/woff2', '.json': 'application/json', '.js': 'text/javascript' };
  const server = http.createServer((req, res) => {
    const file = path.join(designDir, decodeURIComponent(new URL(req.url, 'http://x').pathname));
    if (!file.startsWith(designDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) { res.writeHead(404); res.end(); return; }
    res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' });
    fs.createReadStream(file).pipe(res);
  });
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

async function runDesign(browser) {
  for (const f of ['fonts/bricolage-grotesque.woff2', 'fonts/noto-sans-tc.woff2']) {
    if (!fs.existsSync(path.join(designDir, f))) throw new Error(`design/project/${f} is missing: run scripts/vendor/fonts.sh`);
  }
  const server = await serveDesign();
  const origin = `http://127.0.0.1:${server.address().port}`;
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  try {
    await check('design canvas: every board loads its fonts with all external hosts blocked', async () => {
      const boards = fs.readdirSync(designDir).filter((f) => f.endsWith('.dc.html')).sort();
      assert(boards.length >= 60, `found only ${boards.length} boards`);
      const failures = [];
      for (const board of boards) {
        const page = await openPage(context, [origin]);
        await page.goto(`${origin}/${board}`, { waitUntil: 'networkidle' });
        if (!(await fontsLoaded(page))) failures.push(`${board}: fonts did not load`);
        for (const problem of problemsOf(page)) failures.push(`${board}: ${problem}`);
        await page.close();
      }
      assert(failures.length === 0, failures.slice(0, 20).join('\n') + (failures.length > 20 ? `\n... and ${failures.length - 20} more` : ''));
    });
  } finally {
    await context.close();
    server.close();
  }
}

const browser = await chromium.launch();
try {
  if (!only || only === 'admin') await runAdmin(browser).catch((e) => { results.push({ name: 'admin setup', ok: false, error: e }); console.log(`FAIL  admin setup\n      ${e.message}`); });
  if (!only || only === 'public') await runPublic(browser);
  if (!only || only === 'design') await runDesign(browser).catch((e) => { results.push({ name: 'design setup', ok: false, error: e }); console.log(`FAIL  design setup\n      ${e.message}`); });
  if (!only || only === 'consistency') await runConsistency(browser).catch((e) => { results.push({ name: 'consistency setup', ok: false, error: e }); console.log(`FAIL  consistency setup\n      ${e.message}`); });
} finally {
  await browser.close();
}
const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
process.exit(failed.length ? 1 : 0);
