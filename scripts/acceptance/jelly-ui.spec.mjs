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
// (run scripts/vendor/fonts.sh). JELLY_ONLY=admin|public|design runs one group. JELLY_SCREENSHOT_DIR enables
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
    await page.route('**/admin/assets/jelly-ui/jelly.js', async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
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
    await page.route('**/admin/assets/jelly-ui/jelly.js', async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
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
    await page.route('**/admin/assets/jelly-ui/jelly.js', async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
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
    await page.route('**/admin/assets/jelly-ui/jelly.js', async (route) => { await new Promise((r) => setTimeout(r, 2500)); await route.continue(); });
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

  await check('dialog: deleting an agent requires its exact name in the Jelly confirmation', async () => {
    const page = await openPage(context, [baseOrigin]);
    await page.goto(`${base}/admin`);
    const name = `Jelly UI ${Date.now()}`;
    const created = await page.request.post(`${base}/api/admin/agents`, {
      data: { name, description: 'Browser confirmation fixture', permissions: ['posts.read'] },
      headers: { 'X-Jelly-CSRF': '1' },
    });
    assert(created.ok(), `creating agent fixture: HTTP ${created.status()}`);
    const createdPayload = await created.json();
    const agent = createdPayload.agent;
    assert(agent?.id, 'creating the agent fixture must return an agent with an id');
    const deleteStatuses = [];
    await page.route(`**/api/admin/agents/${agent.id}`, async (route) => {
      if (route.request().method() !== 'DELETE') return route.continue();
      deleteStatuses.push(503);
      await route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Injected agent delete failure' }) });
    });
    try {
      await page.goto(`${base}/admin/agents/${agent.id}`);
      await page.locator('#agentToggle').waitFor({ state: 'visible', timeout: 10000 });
      const deleteButton = page.locator('#agentsContent > .agent-actions button').nth(2);
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
      assert(await confirm.isDisabled(), 'an incorrect agent name must keep deletion disabled');
      await input.fill(name);
      assert(await confirm.isEnabled(), 'the exact agent name must enable deletion');
      await dialog.locator('button[data-jelly-cancel]').click();
      await dialog.waitFor({ state: 'hidden', timeout: 5000 });
      assert(deleteStatuses.length === 0, 'cancelling the agent confirmation must not send a delete request');

      for (let attempt = 1; attempt <= 2; attempt++) {
        ({ dialog, input, confirm } = await openDeleteConfirm());
        await input.fill(name);
        const [response] = await Promise.all([
          page.waitForResponse((res) => res.request().method() === 'DELETE' && res.url().endsWith(`/api/admin/agents/${agent.id}`), { timeout: 5000 }),
          confirm.click(),
        ]);
        assert(response.status() === 503, `simulated delete attempt ${attempt} returned HTTP ${response.status()}, want 503`);
        const mainStatus = page.locator('#agentsStatus[role="status"]');
        await page.waitForFunction(() => document.querySelector('#agentsStatus')?.textContent.includes('503'), null, { timeout: 5000 });
        assert(await mainStatus.isVisible(), 'the failed delete message must be visible in the main agent page status');
        assert((await mainStatus.innerText()).includes('503'), 'the main page must identify the simulated HTTP failure');
        assert(await deleteButton.isEnabled(), 'the delete action must be available to retry after a failed request');
        assert(deleteStatuses.length === attempt, `expected ${attempt} intercepted delete attempt(s), found ${deleteStatuses.length}`);
      }
    } finally {
      await page.request.delete(`${base}/api/admin/agents/${agent.id}`, { headers: { 'X-Jelly-CSRF': '1' } });
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
      const form = page.locator('.modal-overlay.open form.mn-sheet-form');
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
} finally {
  await browser.close();
}
const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
process.exit(failed.length ? 1 : 0);
