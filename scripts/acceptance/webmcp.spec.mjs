// WebMCP browser acceptance for ticket 13 (docs/tickets/13-webmcp.md, slot W6).
//
// Runs against a real, installed Jelly CMS server with the default theme active. It logs in as an admin
// through the API, seeds its own posts (one public, one draft, one private) and drives Chromium with fake
// WebMCP hosts injected before any page script runs:
//   1. document.modelContext only: the core tools (and every tool in /_jelly/webmcp-tools.json) are
//      registered on the home, post list, single post and search pages; execute() answers match the pages;
//      draft, private and unknown slugs answer not_found; pagehide aborts every registration signal.
//   2. navigator.modelContext only: the same tools are registered through the fallback.
//   3. neither: the script does nothing, fetches no tool list and adds no console error.
//   4. a host whose registerTool rejects: no uncaught error reaches the console.
//   5. webmcp_enabled=false: no script tag, nothing registered, Permissions-Policy tools=(); switching back
//      on registers the tools again.
// These checks are expected to fail until ticket 13 is implemented. Do not edit them to match an
// implementation.
//
// Usage (see the ticket for starting a server):
//   JELLY_BASE_URL=http://127.0.0.1:8080 JELLY_ADMIN_USER=admin JELLY_ADMIN_PASSWORD=secret \
//     node scripts/acceptance/webmcp.spec.mjs
// PLAYWRIGHT_MODULE may point at another Playwright install, as in design/tools/shot.mjs.

import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium, request } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const baseURL = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const adminUser = process.env.JELLY_ADMIN_USER;
const adminPassword = process.env.JELLY_ADMIN_PASSWORD;
if (!adminUser || !adminPassword) {
  console.error('set JELLY_ADMIN_USER and JELLY_ADMIN_PASSWORD (an admin account of the server under test)');
  process.exit(2);
}

const CORE_TOOLS = ['search_content', 'list_posts', 'get_post', 'get_site_info'];
const UNTRUSTED = new Set(['search_content', 'list_posts', 'get_post']);
const run = Date.now().toString(36);
const word = `webmcpacc${run}`;
const slug = `webmcp-acc-${run}`;
const draftSlug = `webmcp-acc-draft-${run}`;
const privateSlug = `webmcp-acc-private-${run}`;
const title = `WebMCP 驗收 ${run}`;

const failures = [];
let checks = 0;
function check(ok, message) {
  checks++;
  if (ok) {
    console.log(`ok   ${message}`);
  } else {
    failures.push(message);
    console.log(`FAIL ${message}`);
  }
}

// Installed with page.addInitScript before any page script. mode: document | navigator | none | reject.
function fakeHost(mode) {
  window.__webmcp = { calls: [] };
  if (mode === 'none') return;
  const host = {
    registerTool(tool, options) {
      window.__webmcp.calls.push({ tool, signal: options && options.signal });
      if (mode === 'reject') return Promise.reject(new DOMException('blocked by the test host', 'NotAllowedError'));
      return Promise.resolve(undefined);
    },
  };
  const target = mode === 'navigator' ? Navigator.prototype : Document.prototype;
  Object.defineProperty(target, 'modelContext', { configurable: true, get() { return host; } });
}

const csrf = { 'X-Jelly-CSRF': '1' };
const api = await request.newContext({ baseURL });
const created = [];

async function adminPost(path, data) {
  const res = await api.post(path, { data, headers: csrf });
  if (!res.ok()) throw new Error(`POST ${path} answered ${res.status()}: ${await res.text()}`);
  return res.json();
}

async function setEnabled(value) {
  await adminPost('/api/admin/settings', { webmcp_enabled: value });
}

async function toolList() {
  const res = await api.get('/_jelly/webmcp-tools.json');
  if (!res.ok()) throw new Error(`GET /_jelly/webmcp-tools.json answered ${res.status()}`);
  return (await res.json()).tools;
}

async function openPage(browser, mode, path) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const consoleErrors = [];
  const toolListRequests = [];
  page.on('console', (msg) => { if (msg.type() === 'error' || msg.type() === 'warning') consoleErrors.push(msg.text()); });
  page.on('pageerror', (err) => consoleErrors.push(`pageerror: ${err.message}`));
  page.on('request', (req) => { if (req.url().includes('/_jelly/webmcp-tools.json')) toolListRequests.push(req.url()); });
  await page.addInitScript(fakeHost, mode);
  const response = await page.goto(baseURL + path, { waitUntil: 'load' });
  return { context, page, response, consoleErrors, toolListRequests };
}

async function waitForCalls(page, count) {
  try {
    await page.waitForFunction((n) => window.__webmcp.calls.length >= n, count, { timeout: 5000 });
  } catch {
    // fall through; the caller reports the count it saw
  }
  return page.evaluate(() => window.__webmcp.calls.map((c) => ({
    name: c.tool.name,
    title: c.tool.title,
    description: c.tool.description,
    hasExecute: typeof c.tool.execute === 'function',
    schemaType: c.tool.inputSchema && c.tool.inputSchema.type,
    annotations: c.tool.annotations || {},
    signalOk: c.signal instanceof AbortSignal && !c.signal.aborted,
  })));
}

async function execute(page, name, input) {
  return page.evaluate(async ([toolName, toolInput]) => {
    const call = window.__webmcp.calls.find((c) => c.tool.name === toolName);
    if (!call) return { __missing: toolName };
    return call.tool.execute(toolInput, { signal: new AbortController().signal });
  }, [name, input]);
}

async function pageText(browser, path) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(baseURL + path, { waitUntil: 'load' });
  const text = await page.evaluate(() => document.body.innerText);
  await context.close();
  return text;
}

const browser = await chromium.launch();
try {
  const login = await api.post('/auth/login', { data: { username: adminUser, password: adminPassword }, headers: csrf });
  if (!login.ok()) throw new Error(`login answered ${login.status()}`);
  await setEnabled('true');
  for (const post of [
    { title, slug, content: `<p>${word} 第一段文字</p><p>第二段文字</p>`, status: 'published', visibility: 'public' },
    { title: `${title} 草稿`, slug: draftSlug, content: `<p>${word} draft</p>`, status: 'draft', visibility: 'public' },
    { title: `${title} 私密`, slug: privateSlug, content: `<p>${word} private</p>`, status: 'published', visibility: 'private' },
  ]) {
    created.push(await adminPost('/api/admin/posts', post));
  }

  const listed = await toolList();
  const listedNames = listed.map((t) => t.name);
  check(CORE_TOOLS.every((n) => listedNames.includes(n)), `tool list holds the four core tools (${listedNames.join(', ')})`);

  // 1. document.modelContext
  const pages = ['/', '/posts', `/post/${slug}`, `/search?q=${encodeURIComponent(word)}`];
  for (const path of pages) {
    const { context, page } = await openPage(browser, 'document', path);
    const calls = await waitForCalls(page, listed.length);
    const names = calls.map((c) => c.name);
    check(names.length === listed.length && listedNames.every((n) => names.includes(n)),
      `document.modelContext on ${path}: registered [${names.join(', ')}], want every listed tool once`);
    for (const c of calls.filter((x) => CORE_TOOLS.includes(x.name))) {
      check(c.hasExecute && c.schemaType === 'object' && c.annotations.readOnlyHint === true && c.signalOk &&
        typeof c.title === 'string' && c.description,
        `${path}: ${c.name} has execute, an object inputSchema, readOnlyHint true and a live signal`);
      if (UNTRUSTED.has(c.name)) check(c.annotations.untrustedContentHint === true, `${path}: ${c.name} marks untrustedContentHint`);
      check(!('consequentialHint' in c.annotations) || c.annotations.consequentialHint === false, `${path}: ${c.name} sets no consequentialHint`);
    }

    if (path === `/post/${slug}`) {
      const postText = await page.evaluate(() => document.body.innerText);
      const post = await execute(page, 'get_post', { slug });
      check(post && post.type === 'post' && post.title === title && post.url === `/post/${slug}`,
        `get_post(${slug}) answers the post: ${JSON.stringify(post).slice(0, 160)}`);
      check(post && typeof post.text === 'string' && !post.text.includes('<') &&
        post.text.includes('第一段文字') && post.text.includes('第二段文字') && postText.includes('第一段文字') && postText.includes('第二段文字'),
        'get_post text matches the paragraphs shown on the post page');
      check(postText.includes(title), 'the post page shows the same title as get_post');
      for (const hidden of [draftSlug, privateSlug, `webmcp-acc-missing-${run}`]) {
        const answer = await execute(page, 'get_post', { slug: hidden });
        check(answer && answer.code === 'not_found', `get_post(${hidden}) answers not_found: ${JSON.stringify(answer).slice(0, 120)}`);
      }

      const search = await execute(page, 'search_content', { q: word });
      const searchText = await pageText(browser, `/search?q=${encodeURIComponent(word)}`);
      const found = (search && search.posts) || [];
      check(found.length === 1 && found[0].slug === slug && found[0].title === title,
        `search_content(${word}) finds only the public post: ${JSON.stringify(found).slice(0, 160)}`);
      check(found.every((p) => searchText.includes(p.title)) && !searchText.includes(`${title} 草稿`) && !searchText.includes(`${title} 私密`),
        'search_content results match the search page');

      const list = await execute(page, 'list_posts', { per_page: 50 });
      check(list && Array.isArray(list.items) && list.per_page === 50 && list.items.some((i) => i.url === `/post/${slug}`) &&
        !list.items.some((i) => i.url === `/post/${draftSlug}` || i.url === `/post/${privateSlug}`),
        'list_posts lists the public post and hides the draft and private posts');
      const capped = await execute(page, 'list_posts', { per_page: 500 });
      check(capped && capped.per_page === 50, `list_posts caps per_page at 50 (got ${capped && capped.per_page})`);

      const site = await execute(page, 'get_site_info', {});
      check(site && typeof site.name === 'string' && site.name.length > 0 && typeof site.url === 'string' && site.url.startsWith(baseURL),
        `get_site_info answers name and url: ${JSON.stringify(site).slice(0, 160)}`);

      const aborted = await page.evaluate(() => {
        window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: false }));
        return window.__webmcp.calls.length > 0 && window.__webmcp.calls.every((c) => c.signal && c.signal.aborted);
      });
      check(aborted, 'pagehide aborts the signal of every registration');
    }
    await context.close();
  }

  // 2. navigator.modelContext only
  {
    const { context, page } = await openPage(browser, 'navigator', '/');
    const names = (await waitForCalls(page, listed.length)).map((c) => c.name);
    check(names.length === listed.length && CORE_TOOLS.every((n) => names.includes(n)),
      `navigator.modelContext fallback registers the tools: [${names.join(', ')}]`);
    await context.close();
  }

  // 3. no WebMCP host: compare console output with WebMCP switched off, so only new messages count
  const baseline = {};
  await setEnabled('false');
  for (const path of pages) {
    const { context, page, consoleErrors } = await openPage(browser, 'none', path);
    await page.waitForTimeout(800);
    baseline[path] = consoleErrors.slice();
    await context.close();
  }
  await setEnabled('true');
  for (const path of pages) {
    const { context, page, consoleErrors, toolListRequests } = await openPage(browser, 'none', path);
    await page.waitForTimeout(800);
    const html = await page.content();
    check(html.includes('/_jelly/webmcp.js'), `${path} without a host still loads the WebMCP script (so the check is real)`);
    check(toolListRequests.length === 0, `${path} without a host fetches no tool list (${toolListRequests.length} requests)`);
    const added = consoleErrors.filter((m) => !baseline[path].includes(m));
    check(added.length === 0, `${path} without a host adds no console error or warning: ${JSON.stringify(added)}`);
    check(await page.evaluate(() => document.body.innerText.length > 0), `${path} without a host still renders`);
    await context.close();
  }

  // 4. a host that rejects registerTool
  {
    const { context, page, consoleErrors } = await openPage(browser, 'reject', '/');
    await waitForCalls(page, listed.length);
    await page.waitForTimeout(500);
    const added = consoleErrors.filter((m) => !baseline['/'].includes(m));
    check(added.length === 0, `a rejecting registerTool leaves no uncaught error: ${JSON.stringify(added)}`);
    await context.close();
  }

  // 5. switched off, then back on
  await setEnabled('false');
  {
    const { context, page, response } = await openPage(browser, 'document', '/');
    await page.waitForTimeout(1000);
    const calls = await page.evaluate(() => window.__webmcp.calls.length);
    const policy = (response && (await response.headerValue('permissions-policy'))) || '';
    check(calls === 0, `with WebMCP off nothing is registered (${calls})`);
    check(policy.replace(/\s/g, '').split(',').includes('tools=()'), `with WebMCP off the page sends Permissions-Policy tools=() (got "${policy}")`);
    check(!(await page.content()).includes('/_jelly/webmcp.js'), 'with WebMCP off the page has no WebMCP script');
    const js = await api.get('/_jelly/webmcp.js');
    check(js.status() === 404, `with WebMCP off /_jelly/webmcp.js answers 404 (got ${js.status()})`);
    await context.close();
  }
  await setEnabled('true');
  {
    const { context, page, response } = await openPage(browser, 'document', '/');
    const names = (await waitForCalls(page, CORE_TOOLS.length)).map((c) => c.name);
    const policy = (response && (await response.headerValue('permissions-policy'))) || '';
    check(CORE_TOOLS.every((n) => names.includes(n)), `after switching back on the tools are registered again: [${names.join(', ')}]`);
    check(!policy.replace(/\s/g, '').split(',').includes('tools=()'), 'after switching back on tools=() is gone');
    await context.close();
  }
} catch (err) {
  failures.push(`aborted: ${err.message}`);
  console.log(`FAIL aborted: ${err.stack || err.message}`);
} finally {
  try { await setEnabled('true'); } catch { /* keep the original failure */ }
  for (const post of created) {
    if (post && post.id) await api.delete(`/api/admin/posts/${post.id}`, { headers: csrf }).catch(() => {});
  }
  await browser.close();
  await api.dispose();
}

console.log(`\n${checks - failures.length}/${checks} checks passed`);
if (failures.length) {
  console.log(`${failures.length} failed:\n- ${failures.join('\n- ')}`);
  process.exit(1);
}
