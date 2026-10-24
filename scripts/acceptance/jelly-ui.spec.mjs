#!/usr/bin/env node
// Browser acceptance for ticket 12 (docs/tickets/12-zero-cdn-jelly-ui.md), slot Z7. Not part of CI: the main
// agent runs it against a real server. A plain node script, not @playwright/test. Playwright only loads as
// CommonJS, so it is required through createRequire (an ESM import('playwright') fails).
//
// These checks are expected to fail until tickets 12 slots Z1 to Z6 are implemented; do not edit them to
// match an implementation.
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
// The design canvas check starts its own static server on design/project and needs design/project/fonts/
// (run scripts/vendor/fonts.sh). JELLY_ONLY=admin|public|design runs one group. Exit code 1 on any failure.

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

async function login(context) {
  const page = await openPage(context, [baseOrigin]);
  await page.goto(`${base}/login`);
  await page.fill('#loginUsername', adminUser);
  await page.fill('#loginPassword', adminPassword);
  await Promise.all([page.waitForURL(/\/admin/, { timeout: 15000 }), page.click('#localLoginForm button[type=submit]')]);
  await page.close();
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
    await page.waitForFunction(() => [...document.querySelectorAll('jelly-toaster .toast')].some((t) => t.textContent.includes('Queued early')), null, { timeout: 5000 });
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

  await check('dialog: requireText keeps the confirm button disabled until the word is typed', async () => {
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
    await input.fill('DELETE');
    assert(await confirm.isEnabled(), 'confirm must enable once the word matches');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => window.__r !== 'pending', null, { timeout: 5000 });
    assert(await page.evaluate(() => window.__r) === true, 'Enter with the right word must resolve true');
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
