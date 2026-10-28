#!/usr/bin/env node
// Browser and HTTP acceptance for ticket 15. Run against an isolated Jelly server/database.
// Optional JELLY_SITE_DIR enables a reversible theme asset mutation in that runtime copy only.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { createRequire } from 'node:module';

const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const origin = new URL(base).origin;
const user = process.env.JELLY_ADMIN_USER;
const password = process.env.JELLY_ADMIN_PASSWORD;
if (!user || !password) { console.error('Set JELLY_ADMIN_USER and JELLY_ADMIN_PASSWORD'); process.exit(2); }
const evidenceDir = process.env.JELLY_EVIDENCE_DIR || fs.mkdtempSync(path.join(os.tmpdir(), 'jelly-asset-hash-'));
fs.mkdirSync(evidenceDir, { recursive: true });
const results = [];
const failures = [];
const assert = (condition, message) => { if (!condition) throw new Error(message); };
async function check(name, fn) {
  try { const detail = await fn(); results.push({ name, ok: true, detail: detail || undefined }); console.log(`PASS ${name}`); }
  catch (error) { results.push({ name, ok: false, error: String(error?.stack || error) }); failures.push(name); console.error(`FAIL ${name}: ${error?.message || error}`); }
}
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
const pageErrors = [], consoleErrors = [], externalRequests = [], failedRequests = [], requests = [];
let fixtureId = 0, fixtureSlug = '';
page.on('pageerror', error => pageErrors.push(error.message));
page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
context.on('request', request => {
  requests.push({ url: request.url(), method: request.method(), type: request.resourceType() });
  if (new URL(request.url()).origin !== origin) externalRequests.push(request.url());
});
context.on('requestfailed', request => failedRequests.push(`${request.url()} ${request.failure()?.errorText || ''}`));
context.on('page', p => {
  p.on('pageerror', error => pageErrors.push(error.message));
  p.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
});

async function login() {
  await page.goto(`${base}/login`, { waitUntil: 'domcontentloaded' });
  await page.locator('#loginUsername').fill(user);
  await page.locator('#loginPassword').fill(password);
  await Promise.all([
    page.waitForURL(url => !url.pathname.includes('/login'), { timeout: 20000 }),
    page.locator('#localLoginForm button[type=submit]').click(),
  ]);
}
function assetRefs(html) {
  const refs = new Set();
  const add = (raw, context) => {
    if (!raw || /^(?:data:|blob:|#)/i.test(raw)) return;
    let u;
    try {
      u = new URL(raw, base);
    } catch { /* malformed attributes are not asset references */ }
    if (!u) return;
    assert(u.origin === origin, `external ${context} resource found: ${raw}`);
    if (/\.(?:js|css|woff2?|ttf|otf|svg|png|jpe?g|webp|ico)(?:$|\?)/i.test(u.pathname + u.search)) refs.add(u.href);
  };
  const attrPattern = /([a-z:-]+)\s*=\s*(["'])(.*?)\2/gi;
  const resourceTagPattern = /<(script|img|source|video|audio|iframe|embed|link)\b([^>]*)>/gi;
  for (const [, tagName, source] of html.matchAll(resourceTagPattern)) {
    const attrs = Object.fromEntries([...source.matchAll(attrPattern)].map(([, name, , value]) => [name.toLowerCase(), value]));
    const tag = tagName.toLowerCase();
    if (tag === 'link') {
      const rel = (attrs.rel || '').toLowerCase().split(/\s+/);
      if (!rel.some(value => ['stylesheet', 'icon', 'preload', 'modulepreload'].includes(value))) continue;
      if (attrs.href) add(attrs.href, 'link');
    } else if (attrs.src) add(attrs.src, tag);
  }
  const inspectCSS = (css, context) => {
    for (const [, raw] of css.matchAll(/url\(\s*["']?([^"')]+)["']?\s*\)/gi)) add(raw.trim(), context);
  };
  for (const [, css] of html.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style\s*>/gi)) inspectCSS(css, 'inline CSS');
  for (const [, , css] of html.matchAll(/\bstyle\s*=\s*(["'])(.*?)\1/gi)) inspectCSS(css, 'style attribute');
  return [...refs];
}
function cacheControl(response) { return (response.headers()['cache-control'] || '').toLowerCase(); }
function hashParam(url) { return new URL(url).searchParams.get('v'); }
async function downloadAsset(url) {
  const response = await context.request.get(url);
  assert(response.ok(), `asset request returned ${response.status()}: ${url}`);
  const bytes = await response.body();
  const expected = hashParam(url);
  assert(expected && /^[a-f0-9]{64}$/i.test(expected), `asset URL lacks a 64-character SHA-256: ${url}`);
  const actual = crypto.createHash('sha256').update(bytes).digest('hex');
  assert(actual === expected.toLowerCase(), `asset bytes hash mismatch for ${url}: ${actual} != ${expected}`);
  assert(/public/.test(cacheControl(response)) && /max-age=\s*31536000/.test(cacheControl(response)) && /immutable/.test(cacheControl(response)),
    `versioned asset lacks one-year immutable public caching: ${url} (${cacheControl(response)})`);
  return { url, hash: actual, bytes: bytes.length, etag: response.headers().etag || '' };
}
async function validateReferences(html, label) {
  const refs = assetRefs(html);
  assert(refs.length > 0, `${label} exposed no local JS/CSS/font references, including inline CSS`);
  const reports = [];
  const pending = [...refs], seen = new Set();
  while (pending.length) {
    const ref = pending.shift();
    if (seen.has(ref)) continue;
    seen.add(ref);
    const report = await downloadAsset(ref);
    reports.push(report);
    if (/\.css(?:$|\?)/i.test(new URL(ref).pathname + new URL(ref).search)) {
      const response = await context.request.get(ref);
      const css = await response.text();
      for (const match of css.matchAll(/(?:@import\s+(?:url\()?\s*["']?([^"')\s;]+)|url\(\s*["']?([^"')]+))/gi)) {
        const raw = (match[1] || match[2] || '').trim();
        if (!raw || /^(?:data:|blob:|#)/i.test(raw)) continue;
        assert(!/^(?:https?:|\/\/)/i.test(raw), `external CSS dependency found in ${ref}: ${raw}`);
        const dependency = new URL(raw, ref);
        if (dependency.origin === origin && /^(?:https?:)?\/\//i.test(dependency.href)) {
          const optionalFallback = dependency.pathname.startsWith('/vendor/katex/fonts/') && /\.(?:woff|ttf)$/i.test(dependency.pathname);
          if (optionalFallback) {
            const localFallback = process.env.JELLY_SITE_DIR
              ? path.join(process.env.JELLY_SITE_DIR, 'web', dependency.pathname.replace(/^\//, ''))
              : null;
            if (localFallback && !fs.existsSync(localFallback)) continue;
            if (!localFallback) {
              const fallbackResponse = await context.request.get(dependency.href);
              if (fallbackResponse.status() === 404) continue;
            }
          }
          pending.push(dependency.href);
        }
      }
    }
  }
  return reports;
}
async function htmlPage(url, label) {
  const response = await context.request.get(url);
  assert(response.ok(), `${label} returned HTTP ${response.status()}`);
  const cc = cacheControl(response);
  assert(/no-cache/.test(cc) || /no-store/.test(cc), `${label} HTML must revalidate or not be stored, got Cache-Control: ${cc}`);
  const body = await response.text();
  const refs = await validateReferences(body, label);
  return { body, refs, cacheControl: cc };
}
try {
  await check('public, login, admin, and editor HTML use hashed local assets and revalidating cache', async () => {
    const pages = [];
    pages.push(await htmlPage(`${base}/`, 'public home'));
    pages.push(await htmlPage(`${base}/login`, 'login'));
    await login();
    pages.push(await htmlPage(`${base}/admin`, 'admin dashboard'));
    pages.push(await htmlPage(`${base}/admin/posts`, 'admin posts'));
    const created = await page.evaluate(async () => {
      const suffix = Date.now().toString(36);
      const source = ['# Asset hash acceptance', '', 'Inline math $x^2 + y^2 = z^2$.', '', '\\[E = mc^2\\]', '', '```mermaid', 'flowchart TD', '  A["hash fixture"] --> B["rendered"]', '```', ''].join('\n');
      const response = await fetch('/api/admin/posts', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Jelly-CSRF': '1' },
        body: JSON.stringify({ title: `Asset hash acceptance ${suffix}`, slug: `asset-hash-${suffix}`, status: 'published', source, format: 'jmd' }) });
      if (!response.ok) throw new Error(`create fixture HTTP ${response.status}: ${await response.text()}`);
      return response.json();
    });
    fixtureId = created.id; fixtureSlug = created.slug;
    pages.push(await htmlPage(`${base}/admin/posts/${fixtureId}`, 'editor'));
    await page.goto(`${base}/admin/posts/${fixtureId}`, { waitUntil: 'networkidle' });
    return { refs: pages.map(p => p.refs.length), pages: 5 };
  });

  await check('asset bytes, stable reload URLs, conditional requests, bad hash, and unhashed compatibility', async () => {
    assert(fixtureId, 'fixture creation did not complete');
    const page = await context.newPage();
    await page.goto(`${base}/admin`, { waitUntil: 'networkidle' });
    const firstRefs = assetRefs(await page.locator('html').evaluate(el => el.outerHTML));
    const firstURLs = [...new Set(firstRefs)];
    const valid = firstURLs.filter(url => hashParam(url));
    assert(valid.length > 0, 'browser did not request any versioned local assets');
    const checked = [];
    for (const url of valid) checked.push(await downloadAsset(url));
    await page.reload({ waitUntil: 'networkidle' });
    const secondHTML = await page.locator('html').evaluate(el => el.outerHTML);
    const secondURLs = assetRefs(secondHTML);
    assert(secondURLs.length > 0, 'reload did not produce asset references');
    assert(firstRefs.length === secondURLs.length && firstRefs.every(url => secondURLs.includes(url)),
      `same admin page asset URLs changed on reload: before=${JSON.stringify(firstRefs)} after=${JSON.stringify(secondURLs)}`);

    const etagAsset = checked.find(item => item.etag);
    assert(etagAsset, 'no versioned asset response exposed an ETag');
    const notModified = await context.request.get(etagAsset.url, { headers: { 'If-None-Match': etagAsset.etag } });
    assert(notModified.status() === 304, `If-None-Match returned ${notModified.status()}, expected 304`);
    assert(/immutable/.test(cacheControl(notModified)), `304 did not preserve immutable caching: ${cacheControl(notModified)}`);

    const forged = new URL(etagAsset.url); forged.searchParams.set('v', '0'.repeat(64));
    const bad = await context.request.get(forged.href);
    assert(bad.status() === 404 && /no-store/.test(cacheControl(bad)), `forged hash returned ${bad.status()} with ${cacheControl(bad)}`);
    const unhashed = new URL(etagAsset.url); unhashed.searchParams.delete('v');
    const legacy = await context.request.get(unhashed.href);
    assert(legacy.ok(), `unhashed compatibility URL returned ${legacy.status()}`);
    assert(/no-cache|no-store/.test(cacheControl(legacy)), `unhashed asset may be cached without revalidation: ${cacheControl(legacy)}`);
    return { versionedAssets: checked.length, conditionalStatus: notModified.status(), forgedStatus: bad.status(), unhashedStatus: legacy.status() };
  });

  await check('public post and editor render real math and Mermaid using versioned lazy assets', async () => {
    assert(fixtureId && fixtureSlug, 'published fixture is unavailable');
    await page.goto(`${base}/admin/posts/${fixtureId}`, { waitUntil: 'networkidle' });
    const previewButton = page.locator('button[data-view=preview]');
    if (await previewButton.count()) await previewButton.click();
    await page.locator('#editorPreview .katex').first().waitFor({ timeout: 60000 });
    await page.locator('#editorPreview figure.jc-mermaid svg').waitFor({ timeout: 60000 });
    const publicPage = await context.newPage();
    try {
      await publicPage.goto(`${base}/post/${fixtureSlug}`, { waitUntil: 'networkidle', timeout: 60000 });
      await publicPage.locator('.katex').first().waitFor({ timeout: 60000 });
      await publicPage.locator('figure.jc-mermaid svg').waitFor({ timeout: 60000 });
      await publicPage.setViewportSize({ width: 1280, height: 900 });
      await publicPage.screenshot({ path: path.join(evidenceDir, 'public-desktop.png'), fullPage: true });
      await publicPage.setViewportSize({ width: 390, height: 844 });
      await publicPage.screenshot({ path: path.join(evidenceDir, 'public-mobile.png'), fullPage: true });
    } finally { await publicPage.close(); }
    const relevant = requests.filter(r => /pele|mermaid|katex|\.woff2?(?:\?|$)/i.test(r.url));
    assert(relevant.length > 0, 'math/Mermaid/font assets were not observed in browser requests');
    for (const request of relevant.filter(r => /\.(?:js|css|woff2?|ttf|otf)(?:\?|$)/i.test(r.url))) {
      assert(hashParam(request.url), `lazy math/Mermaid/font request lacks hash: ${request.url}`);
    }
    return { relevantRequests: relevant.length, screenshots: 2 };
  });

  if (process.env.JELLY_SITE_DIR) {
    await check('theme CSS mutation changes only its content hash in isolated runtime copy', async () => {
      const siteDir = path.resolve(process.env.JELLY_SITE_DIR);
      const themesDir = path.join(siteDir, 'web', 'themes');
      const defaultDir = path.join(themesDir, 'default');
      const alternateName = 'hash-acceptance-alt';
      const alternateDir = path.join(themesDir, alternateName);
      const themeCSS = path.join(alternateDir, 'assets', 'content.css');
      assert(fs.existsSync(defaultDir), `default theme not found at ${defaultDir}`);
      assert(!fs.existsSync(alternateDir), `refusing to overwrite existing alternate theme ${alternateDir}`);
      const baseline = await context.request.get(`${base}/`);
      assert(baseline.ok(), `baseline home returned ${baseline.status()}`);
      const baselineRefs = assetRefs(await baseline.text());
      const baselineCSS = baselineRefs.find(url => new URL(url).pathname === '/theme/assets/content.css');
      const baselineScript = baselineRefs.find(url => /\.js(?:\?|$)/i.test(url));
      assert(baselineCSS, 'default theme CSS reference not found');
      fs.cpSync(defaultDir, alternateDir, { recursive: true, errorOnExist: true });
      const original = fs.readFileSync(themeCSS);
      const changedContents = Buffer.concat([original, Buffer.from(`\n/* acceptance ${crypto.randomUUID()} */\n`)]);
      fs.writeFileSync(themeCSS, changedContents);
      const activate = async name => {
        const response = await context.request.post(`${base}/api/admin/themes/activate`, {
          data: { name }, headers: { 'Content-Type': 'application/json', 'X-Jelly-CSRF': '1' },
        });
        assert(response.ok(), `activate theme ${name} returned ${response.status()}: ${await response.text()}`);
      };
      let alternateActivated = false;
      try {
        await activate(alternateName);
        alternateActivated = true;
        const activated = await context.request.get(`${base}/`);
        assert(activated.ok(), `home after activation returned ${activated.status()}`);
        const afterHTML = await activated.text();
        const afterCSS = assetRefs(afterHTML).find(url => new URL(url).pathname === '/theme/assets/content.css');
        assert(afterCSS && hashParam(afterCSS) !== hashParam(baselineCSS), 'alternate theme CSS did not receive a changed content hash');
        const afterScript = assetRefs(afterHTML).find(url => /\.js(?:\?|$)/i.test(url));
        assert(!baselineScript || (afterScript && hashParam(afterScript) === hashParam(baselineScript)), 'unmodified JS hash changed with theme CSS');
        await downloadAsset(afterCSS);
        return { before: hashParam(baselineCSS), after: hashParam(afterCSS) };
      } finally {
        if (alternateActivated) await activate('default');
        if (fs.existsSync(themeCSS)) {
          const current = fs.readFileSync(themeCSS);
          assert(current.equals(changedContents), 'alternate CSS changed concurrently; refusing to overwrite it during cleanup');
        }
        fs.rmSync(alternateDir, { recursive: true, force: true });
      }
    });
  } else {
    results.push({ name: 'theme CSS mutation', ok: true, skipped: 'JELLY_SITE_DIR not set; runtime theme mutation was not attempted' });
    console.log('SKIP theme CSS mutation (JELLY_SITE_DIR not set)');
  }

  await check('no external loads, failed requests, or JavaScript exceptions', async () => {
    assert(externalRequests.length === 0, `external requests: ${externalRequests.join(', ')}`);
    assert(failedRequests.length === 0, `failed requests: ${failedRequests.join(', ')}`);
    assert(pageErrors.length === 0, `page errors: ${pageErrors.join(' | ')}`);
    assert(consoleErrors.length === 0, `console errors: ${consoleErrors.join(' | ')}`);
  });
} finally {
  if (fixtureId) {
    const deleted = await page.evaluate(async id => {
      const response = await fetch(`/api/admin/posts/${id}`, { method: 'DELETE', headers: { 'X-Jelly-CSRF': '1' } });
      return { status: response.status, ok: response.ok };
    }, fixtureId).catch(error => ({ status: 0, error: String(error) }));
    if (!deleted.ok) console.error(`Fixture cleanup not confirmed (HTTP ${deleted.status || 0})`);
    results.push({ name: 'fixture cleanup', ok: Boolean(deleted.ok), detail: deleted });
  }
  await context.close();
  await browser.close();
  fs.writeFileSync(path.join(evidenceDir, 'asset-hash-results.json'), JSON.stringify({ base, results, failures, externalRequests, failedRequests, pageErrors, consoleErrors, requests }, null, 2));
}
console.log(`Evidence: ${evidenceDir}`);
if (failures.length) process.exitCode = 1;
