// Focused browser regressions for WebMCP edge behavior and the crawlers settings switch.
// Run against an installed test server using the same environment as webmcp.spec.mjs.
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium, request } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const baseURL = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const username = process.env.JELLY_ADMIN_USER;
const password = process.env.JELLY_ADMIN_PASSWORD;
if (!username || !password) {
  console.error('set JELLY_ADMIN_USER and JELLY_ADMIN_PASSWORD');
  process.exit(2);
}

const failures = [];
let checks = 0;
function check(ok, message) {
  checks++;
  if (ok) console.log(`ok   ${message}`);
  else { failures.push(message); console.log(`FAIL ${message}`); }
}

function fakeHost(mode) {
  window.__webmcpEdge = { calls: [] };
  if (mode === 'none') return;
  if (mode === 'getter-throws') {
    Object.defineProperty(Document.prototype, 'modelContext', { configurable: true, get() { throw new Error('getter probe'); } });
    return;
  }
  const host = {
    registerTool(tool, options) {
      window.__webmcpEdge.calls.push({ tool, signal: options && options.signal });
      if (mode === 'sync-throw') throw new Error('synchronous registerTool probe');
      if (mode === 'promise-reject') return Promise.reject(new DOMException('rejected by test host', 'NotAllowedError'));
      return Promise.resolve(undefined);
    },
  };
  if (mode === 'document-invalid-host') {
    Object.defineProperty(Document.prototype, 'modelContext', { configurable: true, get() { return {}; } });
    Object.defineProperty(Navigator.prototype, 'modelContext', { configurable: true, get() { return host; } });
  } else if (mode === 'document-register-getter-throws') {
    const invalid = Object.defineProperty({}, 'registerTool', { configurable: true, get() { throw new Error('registerTool getter probe'); } });
    Object.defineProperty(Document.prototype, 'modelContext', { configurable: true, get() { return invalid; } });
    Object.defineProperty(Navigator.prototype, 'modelContext', { configurable: true, get() { return host; } });
  } else if (mode === 'document-getter-throws-navigator') {
    Object.defineProperty(Document.prototype, 'modelContext', { configurable: true, get() { throw new Error('document modelContext getter probe'); } });
    Object.defineProperty(Navigator.prototype, 'modelContext', { configurable: true, get() { return host; } });
  } else {
    Object.defineProperty(Document.prototype, 'modelContext', { configurable: true, get() { return host; } });
  }
}

const csrf = { 'X-Jelly-CSRF': '1' };
const api = await request.newContext({ baseURL });
let browser;
let adminContext;
let oldSetting = 'true';
let settingWasRead = false;

async function setEnabled(value) {
  const res = await api.put('/api/admin/settings', { data: { webmcp_enabled: value }, headers: csrf });
  if (!res.ok()) throw new Error(`PUT /api/admin/settings answered ${res.status()}: ${await res.text()}`);
}

function normalizedPath(raw) {
  const url = new URL(raw, baseURL);
  try { return new URL(decodeURIComponent(url.pathname), url.origin).pathname; }
  catch { return url.pathname; }
}

function safeAdminAsset(url) {
  return url.pathname.startsWith('/admin/assets/') && normalizedPath(url.href).startsWith('/admin/assets/');
}

async function pageWithHost(mode, path = '/', toolListOverride = null) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const consoleProblems = [];
  const requests = [];
  page.on('console', (message) => { if (message.type() === 'error' || message.type() === 'warning') consoleProblems.push(message.text()); });
  page.on('pageerror', (error) => consoleProblems.push(`pageerror: ${error.message}`));
  page.on('request', (request) => requests.push(request.url()));
  const origin = new URL(baseURL).origin;
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url());
    const path = normalizedPath(url.href);
    const allowedInternal = url.pathname === '/_jelly/webmcp.js' || url.pathname === '/_jelly/webmcp-tools.json';
    const forbidden = /^\/(?:admin|api\/admin|auth|api\/auth|install)(?:\/|$)/i.test(path) || (path.startsWith('/_jelly/') && !allowedInternal);
    if (url.origin !== origin || (forbidden && !safeAdminAsset(url))) {
      await route.fulfill({ status: 403, contentType: 'application/json', body: '{"code":"blocked_test_probe"}' });
      return;
    }
    await route.continue();
  });
  if (toolListOverride) {
    await page.route('**/_jelly/webmcp-tools.json', (route) => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ tools: toolListOverride }),
    }));
  }
  await page.addInitScript(fakeHost, mode);
  await page.goto(baseURL + path, { waitUntil: 'load' });
  return { context, page, consoleProblems, requests };
}

async function waitForTools(page, expected) {
  await page.waitForFunction((n) => window.__webmcpEdge && window.__webmcpEdge.calls.length >= n, expected, { timeout: 7000 });
}

async function runTool(page, name, input, abortBeforeCall = false) {
  return page.evaluate(async ([toolName, toolInput, abortBefore]) => {
    const registered = window.__webmcpEdge.calls.find((entry) => entry.tool.name === toolName);
    if (!registered) throw new Error(`missing tool ${toolName}`);
    const controller = new AbortController();
    if (abortBefore) controller.abort();
    return registered.tool.execute(toolInput, { signal: controller.signal });
  }, [name, input, abortBeforeCall]);
}

async function setSwitchThroughUI(page, enabled) {
  const input = page.locator('#webmcpEnabled');
  await input.waitFor({ state: 'visible', timeout: 7000 });
  const metadata = await input.evaluate((el) => ({ type: el.type, name: el.name, checked: el.checked }));
  check(metadata.type === 'checkbox' && metadata.name === 'webmcp_enabled', 'crawlers UI exposes the WebMCP checkbox with its settings key');
  if (metadata.checked !== enabled) {
    if (enabled) await input.check(); else await input.uncheck();
    const form = input.locator('xpath=ancestor::form');
    const saved = page.waitForResponse((response) => response.url().includes('/api/admin/settings') && response.request().method() === 'PUT');
    await form.locator('button[type="submit"]').click();
    const response = await saved;
    check(response.ok(), `saving the WebMCP checkbox uses PUT and succeeds (${response.status()})`);
  }
}

browser = await chromium.launch();
try {
  const login = await api.post('/auth/login', { data: { username, password }, headers: csrf });
  if (!login.ok()) throw new Error(`login answered ${login.status()}`);
  const optionsResponse = await api.get('/api/admin/settings');
  if (!optionsResponse.ok()) throw new Error(`GET /api/admin/settings answered ${optionsResponse.status()}`);
  const options = await optionsResponse.json();
  const savedWebMCP = Array.isArray(options) ? options.find((option) => option.key === 'webmcp_enabled') : null;
  oldSetting = savedWebMCP ? savedWebMCP.value : 'true';
  settingWasRead = true;
  await setEnabled('true');

  // A throwing capability getter is treated as an unavailable host: no tool-list fetch and no console noise.
  {
    const { context, page, consoleProblems, requests } = await pageWithHost('getter-throws');
    await page.waitForTimeout(700);
    check(!requests.some((url) => url.includes('/_jelly/webmcp-tools.json')), 'throwing host getter triggers no tool-list fetch');
    check(consoleProblems.length === 0, `throwing host getter adds no console error or warning: ${JSON.stringify(consoleProblems)}`);
    await context.close();
  }

  // Invalid capability objects and throwing capability getters must fall through to navigator.modelContext.
  const fallbackTool = [{ name: 'fallback_probe', title: 'Fallback probe', description: 'Check host fallback.', input_schema: { type: 'object' }, endpoint: '/api/public/site' }];
  for (const mode of ['document-invalid-host', 'document-register-getter-throws', 'document-getter-throws-navigator']) {
    const { context, page, consoleProblems } = await pageWithHost(mode, '/', fallbackTool);
    await waitForTools(page, 1).catch(() => {});
    const names = await page.evaluate(() => window.__webmcpEdge.calls.map((entry) => entry.tool.name));
    check(names.includes('fallback_probe'), `${mode} falls back to navigator.modelContext: ${JSON.stringify(names)}`);
    check(consoleProblems.length === 0, `${mode} adds no console error or warning: ${JSON.stringify(consoleProblems)}`);
    await context.close();
  }

  // Both synchronous throws and rejected registerTool promises must be consumed by the page script.
  for (const mode of ['sync-throw', 'promise-reject']) {
    const { context, page, consoleProblems } = await pageWithHost(mode);
    await page.waitForTimeout(900);
    check(consoleProblems.length === 0, `${mode} from registerTool adds no console error or warning: ${JSON.stringify(consoleProblems)}`);
    await context.close();
  }

  // Synthetic lifecycle events model BFCache restoration; this does not prove that Chromium used its native BFCache.
  {
    const lifecycleTool = [{ name: 'lifecycle_probe', title: 'Lifecycle probe', description: 'Check restored registration.', input_schema: { type: 'object' }, endpoint: '/api/public/site' }];
    const { context, page, requests } = await pageWithHost('ok', '/', lifecycleTool);
    await waitForTools(page, 1);
    const initialDiscoveryCount = requests.filter((url) => url.includes('/_jelly/webmcp-tools.json')).length;
    const firstSignalAborted = await page.evaluate(() => {
      const signal = window.__webmcpEdge.calls[0]?.signal;
      window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }));
      return Boolean(signal?.aborted);
    });
    check(firstSignalAborted, 'synthetic persisted pagehide aborts the initial registration signal');

    await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true })));
    await page.waitForFunction(() => window.__webmcpEdge.calls.length === 2, null, { timeout: 7000 }).catch(() => {});
    check(await page.evaluate(() => window.__webmcpEdge.calls.length === 2),
      'synthetic persisted pageshow registers one fresh tool generation');
    const restoredDiscoveryCount = requests.filter((url) => url.includes('/_jelly/webmcp-tools.json')).length;
    check(restoredDiscoveryCount === initialDiscoveryCount + 1,
      'synthetic restoration refetches the discovery list');

    await page.evaluate(() => {
      window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: false }));
      window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }));
    });
    await page.waitForTimeout(300);
    check(await page.evaluate(() => window.__webmcpEdge.calls.length === 2),
      'ordinary and duplicate synthetic pageshow events do not duplicate active registrations');
    const nextGenerationAborted = await page.evaluate(() => {
      const signals = window.__webmcpEdge.calls.map((entry) => entry.signal);
      window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }));
      return signals.every((signal) => signal?.aborted);
    });
    check(nextGenerationAborted, 'the next synthetic pagehide aborts all prior registration generations');
    await context.close();
  }

  // Exercise the real get_post execute callback, but keep malformed paths away from any privileged or external target.
  {
    const { context, page, requests } = await pageWithHost('ok');
    const toolsResponse = await api.get('/_jelly/webmcp-tools.json');
    const tools = (await toolsResponse.json()).tools;
    await waitForTools(page, tools.length);
    const baseOrigin = new URL(baseURL).origin;
    const firstProbeRequest = requests.length;
    const probes = ['..', '../admin/settings', '%2e%2e%2fadmin%2fsettings', '%2F%2Fevil.invalid%2Fpath', 'https://evil.invalid/path'];
    for (const slug of probes) {
      await runTool(page, 'get_post', { slug }).catch(() => null);
    }
    const probeRequests = requests.slice(firstProbeRequest);
    const suspicious = probeRequests.filter((raw) => {
      const url = new URL(raw);
      const path = normalizedPath(raw);
      const isAdminAsset = safeAdminAsset(url);
      const forbidden = /^\/(?:admin|api\/admin|auth|api\/auth|install)(?:\/|$)/i.test(path) ||
        (path.startsWith('/_jelly/') && !['/_jelly/webmcp.js', '/_jelly/webmcp-tools.json'].includes(path));
      return url.origin !== baseOrigin || (forbidden && !isAdminAsset) || !path.startsWith('/api/public/content/');
    });
    check(suspicious.length === 0, `malicious slug inputs issue no privileged or cross-origin requests: ${JSON.stringify(suspicious)}`);
    await context.close();
  }

  // Execute error handling against controlled same-origin responses.
  {
    const { context, page } = await pageWithHost('ok');
    const toolsResponse = await api.get('/_jelly/webmcp-tools.json');
    await waitForTools(page, (await toolsResponse.json()).tools.length);
    const testSlug404 = 'webmcp-edge-json-404';
    await page.route(`**/api/public/content/${testSlug404}`, (route) => route.fulfill({ status: 404, contentType: 'application/json', body: '{"code":"not_found","probe":true}' }));
    const non2xx = await runTool(page, 'get_post', { slug: testSlug404 });
    check(non2xx && non2xx.code === 'not_found' && non2xx.probe === true, `non-2xx JSON response is returned as-is: ${JSON.stringify(non2xx)}`);

    const nonJSONSlug = 'webmcp-edge-non-json';
    await page.route(`**/api/public/content/${nonJSONSlug}`, (route) => route.fulfill({ status: 200, contentType: 'text/plain', body: 'not json' }));
    const nonJSONRejected = await runTool(page, 'get_post', { slug: nonJSONSlug }).then(() => false, () => true);
    check(nonJSONRejected, 'non-JSON API response rejects execute()');

    const networkSlug = 'webmcp-edge-network-failure';
    await page.route(`**/api/public/content/${networkSlug}`, (route) => route.abort('failed'));
    const networkRejected = await runTool(page, 'get_post', { slug: networkSlug }).then(() => false, () => true);
    check(networkRejected, 'network failure rejects execute()');

    const callerAbortRejected = await runTool(page, 'get_post', { slug: 'webmcp-edge-aborted' }, true).then(() => false, () => true);
    check(callerAbortRejected, 'caller abort rejects execute()');

    const pagehideAborted = await page.evaluate(() => {
      window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: false }));
      return window.__webmcpEdge.calls.length > 0 && window.__webmcpEdge.calls.every((entry) => entry.signal && entry.signal.aborted);
    });
    check(pagehideAborted, 'pagehide aborts every WebMCP registration signal');
    await context.close();
  }

  // A declaration may target any safe same-origin path; placeholder values and query properties stay encoded and use JSON Accept.
  {
    const tool = [{
      name: 'event_detail', title: 'Event detail', description: 'Read one public event.',
      input_schema: { type: 'object' }, endpoint: '/public-events/{id}',
    }];
    const { context, page } = await pageWithHost('ok', '/', tool);
    await waitForTools(page, 1);
    let requestInfo = null;
    await page.route('**/public-events/**', async (route) => {
      const request = route.request();
      requestInfo = {
        url: request.url(),
        method: request.method(),
        accept: request.headers().accept,
        origin: new URL(request.url()).origin,
      };
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"id":"event 42","ok":true}' });
    });
    let answer = null;
    try {
      answer = await runTool(page, 'event_detail', { id: 'event 42', language: 'zh-TW' });
    } catch (error) {
      check(false, `same-origin non-/api endpoint executes: ${error.message}`);
    }
    check(answer && answer.id === 'event 42' && answer.ok === true, `custom endpoint returns JSON: ${JSON.stringify(answer)}`);
    check(requestInfo && requestInfo.origin === new URL(baseURL).origin && requestInfo.method === 'GET', `custom endpoint remains a same-origin GET: ${JSON.stringify(requestInfo)}`);
    if (requestInfo) {
      const url = new URL(requestInfo.url);
      check(url.pathname === '/public-events/event%2042' && url.searchParams.get('language') === 'zh-TW',
        `placeholder and query inputs are encoded in the custom endpoint: ${requestInfo.url}`);
      check(requestInfo.accept === 'application/json', `custom endpoint sends Accept: application/json (got ${requestInfo.accept})`);
    }
    await context.close();
  }

  // Use a real authenticated browser session and the rendered crawlers switch, then verify the saved state after reload.
  {
    adminContext = await browser.newContext({ storageState: await api.storageState() });
    const page = await adminContext.newPage();
    await page.goto(`${baseURL}/admin/settings/crawlers`, { waitUntil: 'load' });
    await setSwitchThroughUI(page, true);
    await page.reload({ waitUntil: 'load' });
    check(await page.locator('#webmcpEnabled').isChecked(), 'WebMCP switch remains enabled after reload');

    await setSwitchThroughUI(page, false);
    await page.reload({ waitUntil: 'load' });
    check(!(await page.locator('#webmcpEnabled').isChecked()), 'WebMCP switch remains disabled after reload');
    const off = await adminContext.newPage();
    const offResponse = await off.goto(`${baseURL}/`, { waitUntil: 'load' });
    const offPolicy = (offResponse && (await offResponse.headerValue('permissions-policy'))) || '';
    check(!(await off.content()).includes('/_jelly/webmcp.js') && offPolicy.replace(/\s/g, '').split(',').includes('tools=()'),
      'disabling from the crawlers UI removes the script and sends tools=()');
    await off.close();

    await setSwitchThroughUI(page, true);
    await page.reload({ waitUntil: 'load' });
    check(await page.locator('#webmcpEnabled').isChecked(), 'WebMCP switch can be re-enabled from the crawlers UI');
    const on = await adminContext.newPage();
    await on.addInitScript(fakeHost, 'ok');
    await on.goto(`${baseURL}/`, { waitUntil: 'load' });
    await on.waitForFunction(() => window.__webmcpEdge.calls.some((entry) => entry.tool.name === 'get_site_info'), null, { timeout: 7000 });
    const names = await on.evaluate(async () => {
      return window.__webmcpEdge.calls.map((entry) => entry.tool.name);
    });
    check(names.includes('get_site_info'), 're-enabling from the crawlers UI restores tools on the next page load');
    await on.close();
  }
} catch (error) {
  failures.push(`aborted: ${error.message}`);
  console.log(`FAIL aborted: ${error.stack || error.message}`);
} finally {
  if (settingWasRead) await setEnabled(oldSetting).catch(() => {});
  if (adminContext) await adminContext.close().catch(() => {});
  if (browser) await browser.close();
  await api.dispose();
}

console.log(`\n${checks - failures.length}/${checks} checks passed`);
if (failures.length) {
  console.log(`${failures.length} failed:\n- ${failures.join('\n- ')}`);
  process.exit(1);
}
