// Black-box browser acceptance for ticket 09's author editor.
// Run: EDITOR_BASE_URL=http://127.0.0.1:8080 EDITOR_USERNAME=... EDITOR_PASSWORD=... node design/tools/editor.spec.mjs
// PLAYWRIGHT_MODULE may point at Playwright's index.mjs. Screenshots default to /tmp/jelly-editor.
import fs from 'node:fs/promises';
import path from 'node:path';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const baseURL = process.env.EDITOR_BASE_URL || 'http://127.0.0.1:8080';
const username = process.env.EDITOR_USERNAME;
const password = process.env.EDITOR_PASSWORD;
const outDir = path.resolve(process.env.EDITOR_SCREENSHOTS || '/tmp/jelly-editor');
if (!username || !password) throw new Error('Set EDITOR_USERNAME and EDITOR_PASSWORD; credentials are read only from environment variables.');
await fs.mkdir(outDir, { recursive: true });

const browser = await chromium.launch({ headless: process.env.EDITOR_HEADLESS !== '0' });
const errors = [];
const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, locale: 'zh-TW', colorScheme: 'light' });
page.on('pageerror', e => errors.push({ kind: 'pageerror', message: e.message, expected: false }));
let phase = 'setup';
page.on('console', msg => {
  if (msg.type() !== 'error') return;
  const message = msg.text();
  const locationURL = msg.location().url;
  const expectedOffline = phase === 'offline' && /ERR_INTERNET_DISCONNECTED|Failed to fetch|NetworkError/i.test(message);
  const postURL = postId ? new URL(`/api/admin/posts/${postId}`, baseURL).href : '';
  const pdfURL = postId ? new URL(`/api/admin/posts/${postId}/export?format=pdf`, baseURL).href : '';
  const renderURL = new URL('/api/admin/render', baseURL).href;
  const expectedConflict = phase === 'conflict' && locationURL === postURL && /\b409\b/.test(message);
  const expectedPDFUnavailable = phase === 'pdf' && locationURL === pdfURL && /\b501\b/.test(message);
  const expectedAbortedPreview = locationURL === renderURL && /net::ERR_ABORTED/i.test(message);
  errors.push({ kind: 'console', message, expected: expectedOffline || expectedConflict || expectedPDFUnavailable || expectedAbortedPreview });
});
page.on('requestfailed', request => {
  const message = request.failure()?.errorText || '';
  const expectedOffline = phase === 'offline' && /ERR_INTERNET_DISCONNECTED|ERR_FAILED/i.test(message) && /\/api\/(admin\/)?(render|posts|media)/.test(request.url());
  const requestURL = new URL(request.url());
  const expectedAbortedPreview = request.method() === 'POST' && requestURL.pathname === '/api/admin/render' && /ERR_ABORTED/i.test(message);
  errors.push({ kind: 'requestfailed', message: `${request.method()} ${request.url()} ${message}`, expected: expectedOffline || expectedAbortedPreview });
});
let postId;
const uploadedMediaNamesForCleanup = [];

function check(ok, message) { if (!ok) throw new Error(message); }
async function snap(name) { await page.screenshot({ path: path.join(outDir, `${name}.png`), fullPage: true }); }
async function login() {
  await page.goto(new URL('/login', baseURL).href, { waitUntil: 'domcontentloaded' });
  const u = page.locator('input[name="username"], input[autocomplete="username"], input[type="email"]').first();
  const p = page.locator('input[name="password"], input[autocomplete="current-password"], input[type="password"]').first();
  await u.fill(username); await p.fill(password);
  const submit = page.locator('button[type="submit"], input[type="submit"]').first();
  await submit.waitFor({ state: 'visible' });
  await Promise.all([
    page.waitForURL(url => !url.pathname.includes('/login'), { timeout: 15000 }),
    submit.click(),
  ]);
}
async function openEditor(language = 'zh-TW') {
  await page.goto(new URL(`/admin/posts/${postId}?lang=${language}`, baseURL).href, { waitUntil: 'domcontentloaded' });
  await page.locator('#postTitle').waitFor({ state: 'visible' });
  // CodeMirror exposes a contenteditable textbox; the legacy textarea remains a fallback locator.
  const editor = page.locator('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]').first();
  await editor.waitFor({ state: 'visible', timeout: 15000 });
  return editor;
}
async function currentSource(editor = sourceLocator()) {
  return editor.evaluate(el => {
    if (typeof window.JellyEditor?.getValue === 'function') return window.JellyEditor.getValue();
    return typeof el.value === 'string' ? el.value : el.innerText;
  });
}
function sourceLocator() {
  return page.locator('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]').first();
}
async function sourceMode(editor = sourceLocator()) {
  const sourceButton = page.getByRole('button', { name: /原稿|文字|Source/i }).first();
  if (!(await editor.isVisible().catch(() => false))) await sourceButton.click();
  await editor.waitFor({ state: 'visible' });
  return editor;
}
async function setSource(editor, value) {
  if (await editor.getAttribute('contenteditable') === 'true') {
    await editor.click(); await page.keyboard.press('ControlOrMeta+A'); await editor.fill(value);
  } else await editor.fill(value);
}
async function expectAny(selectors, label) {
  const loc = page.locator(selectors);
  await loc.first().waitFor({ state: 'visible', timeout: 10000 });
  return loc.first();
}
async function clickText(text) { await page.getByRole('button', { name: new RegExp(text, 'i') }).first().click(); }
async function downloadFormat(format, extension) {
  const option = page.locator(`[data-format="${format}"]`).first();
  if (await option.count()) await option.click();
  else {
    const labels = { jmd: /Jelly Markdown|JMD/i, md: /純 Markdown|Plain Markdown/i, html: /HTML/i };
    await page.getByRole('radio', { name: labels[format] || new RegExp(format, 'i') }).check();
  }
  const downloadPromise = page.waitForEvent('download', { timeout: 15000 });
  await page.getByRole('button', { name: /下載|匯出|Download|Export/i }).last().click();
  const download = await downloadPromise;
  const output = path.join(outDir, `export.${extension}`);
  await download.saveAs(output);
  return fs.readFile(output);
}

try {
  await login();
  // Create a disposable owned draft through the documented API so autosave and conflict checks
  // exercise an existing post. It is removed in finally, including when an assertion fails.
  postId = await page.evaluate(async () => {
    const res = await fetch('/api/admin/posts', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: `Editor acceptance ${Date.now()}`, type: 'post', status: 'draft', source: '# Acceptance draft\n', format: 'jmd' }) });
    if (!res.ok) throw new Error(`could not create editor test draft: ${res.status} ${await res.text()}`);
    return (await res.json()).id;
  });
  await openEditor();
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    for (const lang of ['zh-TW', 'en']) {
      for (const theme of ['light', 'dark']) {
        await page.emulateMedia({ colorScheme: theme });
        await openEditor(lang);
        const actualLanguage = await page.locator('html').getAttribute('lang');
        check(actualLanguage === lang, `requested ${lang}, page rendered lang=${actualLanguage}`);
        await page.waitForTimeout(150);
        await snap(`editor-${width}-${theme}-${lang}`);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1);
        check(!overflow, `horizontal overflow at ${width}px, ${theme}, ${lang}`);
      }
    }
  }

  // Seed source while the source editor is visible, then switch views and focus mode.
  await page.setViewportSize({ width: 1280, height: 900 });
  await openEditor('zh-TW');
  let editor = await sourceMode();
  const preservedColumnsAttrs = String.raw`unknown="brace {inside=1}" escaped="quote\"inside"`;
  await setSource(editor, `:::columns {ratio=2:1 ${preservedColumnsAttrs}}\n::: col\nLEFT_SENTINEL\n:::\n::: col\nRIGHT_SENTINEL\n:::\n:::\n\n:::section {bg=mint}\nTHIRD_SENTINEL\n:::\n`);
  // View switching and focus mode.
  await page.setViewportSize({ width: 1280, height: 900 });
  for (const label of [/原稿|文字|Source/i, /分割|Split/i, /預覽|Preview/i]) await page.getByRole('button', { name: label }).first().click();
  await clickText(/專心|focus/i);
  await expectAny('[data-testid="focus-mode"], .focus-mode, [aria-label*="專心"], [aria-label*="Focus"]', 'focus mode');
  await page.keyboard.press('Escape');

  // Layout mode must reorder source ranges and limit inspector edits to the opening line.
  await sourceMode(editor);
  const beforeMove = await currentSource(editor);
  const layoutRenderResponse = page.waitForResponse(response => {
    const request = response.request();
    if (request.method() !== 'POST' || new URL(request.url()).pathname !== '/api/admin/render' || !response.ok()) return false;
    return request.postDataJSON()?.source === beforeMove;
  }, { timeout: 10000 });
  await clickText(/版面|Layout/i);
  const blocks = page.locator('[data-testid="layout-block"], .layout-block, [data-block-start]');
  const layoutRender = await layoutRenderResponse;
  const layoutResult = await layoutRender.json();
  const renderedBlocks = layoutResult.blocks || layoutResult.Blocks || [];
  const columnsSourceBlock = renderedBlocks.find(block => (block.Name ?? block.name) === 'columns');
  const sectionSourceBlock = renderedBlocks.find(block => (block.Name ?? block.name) === 'section');
  check(Boolean(columnsSourceBlock && sectionSourceBlock), `layout render omitted expected source blocks: ${JSON.stringify(renderedBlocks)}`);
  const columnsStart = Number(columnsSourceBlock.StartLine ?? columnsSourceBlock.start_line);
  const sectionStart = Number(sectionSourceBlock.StartLine ?? sectionSourceBlock.start_line);
  check(columnsStart > 0 && sectionStart > 0, `layout render returned invalid source ranges: ${JSON.stringify(renderedBlocks)}`);
  await page.waitForFunction(({ columnStart, sectionStart: wantedSectionStart }) => {
    const cards = Array.from(document.querySelectorAll('[data-testid="layout-block"], .layout-block, [data-block-start]'));
    return cards.some(card => Number(card.dataset.blockStart) === columnStart && card.textContent.includes('LEFT_SENTINEL'))
      && cards.some(card => Number(card.dataset.blockStart) === wantedSectionStart && card.textContent.includes('THIRD_SENTINEL'));
  }, { columnStart: columnsStart, sectionStart }, { timeout: 10000 });
  await blocks.nth(1).waitFor({ state: 'visible', timeout: 10000 });
  check(await blocks.count() >= 3, `layout shows ${await blocks.count()} blocks, want at least three`);
  const thirdSection = page.locator(`[data-testid="layout-block"][data-block-start="${sectionStart}"], .layout-block[data-block-start="${sectionStart}"], [data-block-start="${sectionStart}"]`).first();
  const columnsBlock = page.locator(`[data-testid="layout-block"][data-block-start="${columnsStart}"], .layout-block[data-block-start="${columnsStart}"], [data-block-start="${columnsStart}"]`).first();
  await thirdSection.waitFor({ state: 'visible', timeout: 10000 });
  await columnsBlock.waitFor({ state: 'visible', timeout: 10000 });
  const movedRenderResponse = page.waitForResponse(response => {
    const request = response.request();
    if (request.method() !== 'POST' || new URL(request.url()).pathname !== '/api/admin/render' || !response.ok()) return false;
    const body = request.postDataJSON();
    const source = body?.source;
    const third = typeof source === 'string' ? source.indexOf('THIRD_SENTINEL') : -1;
    const left = typeof source === 'string' ? source.indexOf('LEFT_SENTINEL') : -1;
    return third >= 0 && left >= 0 && third < left;
  }, { timeout: 10000 });
  // Start and drop in the cards' headers. Their centers can be far apart because
  // the columns card is tall, which makes a center-to-center drag scroll the page
  // during the gesture and can replace the target card before drop.
  await thirdSection.dragTo(columnsBlock, {
    sourcePosition: { x: 30, y: 15 },
    targetPosition: { x: 30, y: 15 },
    timeout: 10000,
  });
  await movedRenderResponse;
  await page.waitForFunction(() => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const s = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return s.indexOf('THIRD_SENTINEL') < s.indexOf('LEFT_SENTINEL');
  }, null, { timeout: 10000 });
  await sourceMode(editor);
  const afterMove = await currentSource(editor);
  check(beforeMove !== afterMove && afterMove.includes('LEFT_SENTINEL') && afterMove.includes('RIGHT_SENTINEL') && afterMove.includes('THIRD_SENTINEL'), 'dragging blocks did not reorder source ranges without losing content');
  await clickText(/版面|Layout/i);
  await expectAny('[data-testid="block-inspector"], .block-inspector, [aria-label*="檢視器"], [aria-label*="Inspector"]', 'layout inspector');
  // A drag can leave selection state on the moved section, and the first layout
  // paint may run while the preview render is still catching up. Select columns
  // explicitly before asserting its inspector fields.
  const columnsCard = page.locator('[data-testid="layout-block"], .layout-block, [data-block-start]').filter({ hasText: 'LEFT_SENTINEL' }).first();
  await columnsCard.waitFor({ state: 'visible', timeout: 10000 });
  await columnsCard.click();
  const beforeAttrEdit = afterMove.split('\n');
  const columnsOpeningLine = beforeAttrEdit.findIndex(line => /^:::columns\b/.test(line));
  check(columnsOpeningLine >= 0, 'layout source has no columns opener to inspect');
  const ratio = page.locator('[data-testid="inspector-ratio"], [name="ratio"], [data-attribute="ratio"] input').first();
  await ratio.waitFor({ state: 'visible' });
  await ratio.fill('1:2'); await ratio.press('Enter');
  await page.waitForFunction(() => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const s = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return /^:::columns .*ratio=1:2/m.test(s);
  }, null, { timeout: 10000 });
  await sourceMode(editor);
  const afterAttrEdit = (await currentSource(editor)).split('\n');
  check(beforeAttrEdit.length === afterAttrEdit.length && beforeAttrEdit.every((line, i) => i === columnsOpeningLine || line === afterAttrEdit[i]), 'inspector attribute edit changed lines beyond the columns opening line');
  check(/ratio=1:2/.test(afterAttrEdit[columnsOpeningLine]), 'inspector did not update the columns opening line');
  check(afterAttrEdit[columnsOpeningLine].includes(preservedColumnsAttrs), 'inspector did not preserve unknown quoted-brace and escaped-quote attributes byte-for-byte');

  // Slash menu: open, filtered, and empty results.
  editor = await sourceMode();
  await setSource(editor, '/');
  await page.keyboard.press('End');
  await expectAny('[role="menu"], [role="listbox"], [data-testid="slash-menu"]', 'slash menu open state');
  await page.keyboard.type('table');
  check(await page.getByText(/table|表格/i).first().isVisible(), 'slash menu filtered result is missing');
  await page.keyboard.press('ControlOrMeta+A'); await page.keyboard.type('/no-such-editor-command-xyz');
  await expectAny('[data-testid="slash-empty"]', 'slash menu empty state');
  check(/沒有符合|找不到|no results|no commands/i.test(await page.locator('[data-testid="slash-empty"]').innerText()), 'slash empty state does not explain that there are no matches');

  // Preview is server-rendered; wait for the render API and resulting UI update.
  const renderResponsePromise = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/render' && (r.request().postDataJSON()?.source || '').includes('PREVIEW_SENTINEL'), { timeout: 10000 });
  await setSource(editor, '# Browser acceptance\n\nPREVIEW_SENTINEL\n');
  await page.getByRole('button', { name: /分割|Split/i }).first().click();
  await expectAny('[data-testid="editor-preview"], .editor-preview, iframe[title*="預覽"], iframe[title*="Preview"]', 'preview pane');
  const renderResponse = await renderResponsePromise;
  check(renderResponse.status() === 200, `render API returned ${renderResponse.status()}`);
  await page.getByText('PREVIEW_SENTINEL').last().waitFor({ state: 'visible', timeout: 10000 });

  // Blur triggers autosave; require its HTTP 200 before reloading and restoring.
  const title = page.locator('#postTitle');
  await title.fill(`Editor acceptance ${Date.now()}`);
  await setSource(editor, '# Restore sentinel\n\nAUTOSAVE_SENTINEL\n');
  const autosaveResponsePromise = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/admin/posts/${postId}/autosave`, { timeout: 15000 });
  await page.locator('#postExcerpt').click();
  const autosaveResponse = await autosaveResponsePromise;
  check(autosaveResponse.status() === 200, `blur autosave returned ${autosaveResponse.status()}`);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expectAny('[data-testid="autosave-restore"], [role="dialog"]', 'autosave restore prompt');
  await page.getByRole('button', { name: /還原|Restore/i }).click();
  const restoredEditor = await sourceMode(sourceLocator());
  const restoredSource = await currentSource(restoredEditor);
  check(restoredSource.includes('AUTOSAVE_SENTINEL'), 'restore action did not put the autosaved source back into the editor');

  // A save response for an older submission must not clear edits made while PUT is pending.
  let releaseDelayedPut;
  let delayedPutSeen;
  const delayedPutGate = new Promise(resolve => { releaseDelayedPut = resolve; });
  const delayedPutRequest = new Promise(resolve => { delayedPutSeen = resolve; });
  const delayedPutResponsePromise = page.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === `/api/admin/posts/${postId}`, { timeout: 15000 });
  await page.route(`**/api/admin/posts/${postId}`, async route => {
    if (route.request().method() === 'PUT') {
      delayedPutSeen();
      await delayedPutGate;
    }
    await route.continue();
  });
  const saveDuringEdit = page.getByRole('button', { name: /儲存|Save/i }).first();
  const saveDuringEditPromise = saveDuringEdit.click();
  try {
    await Promise.race([delayedPutRequest, page.waitForTimeout(10000).then(() => { throw new Error('manual save did not reach the delayed PUT route'); })]);
    await title.fill('Delayed save submitted title');
    await page.locator('#postExcerpt').fill('Changed while save is pending');
    await page.locator('#seoTitle').fill('Pending SEO setting');
    await page.locator('#optVis').click();
    await page.waitForFunction(id => {
      const key = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)).find(name => name?.endsWith('.draft.' + id));
      if (!key) return false;
      const draft = JSON.parse(localStorage.getItem(key));
      return draft?.title === 'Delayed save submitted title' && draft?.excerpt === 'Changed while save is pending' &&
        draft?.draft?.data?.visibility === 'private' && draft?.draft?.data?.meta?.seo_title === 'Pending SEO setting';
    }, postId, { timeout: 5000 });
    const leaveWarning = await page.evaluate(() => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    });
    check(leaveWarning, 'dirty edits made during a pending save did not request a leave warning');
  } finally {
    releaseDelayedPut();
    await saveDuringEditPromise;
  }
  try {
    const delayedPutResponse = await delayedPutResponsePromise;
    check(delayedPutResponse.status() === 200, `delayed manual save returned ${delayedPutResponse.status()}`);
    await page.waitForFunction(() => !document.querySelector('#saveBtn')?.disabled, null, { timeout: 10000 });
    await page.waitForFunction(id => {
      const key = Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)).find(name => name?.endsWith('.draft.' + id));
      if (!key) return false;
      const draft = JSON.parse(localStorage.getItem(key));
      return draft?.title === 'Delayed save submitted title' && draft?.excerpt === 'Changed while save is pending' &&
        draft?.draft?.data?.visibility === 'private' && draft?.draft?.data?.meta?.seo_title === 'Pending SEO setting';
    }, postId, { timeout: 5000 });
    const leaveWarning = await page.evaluate(() => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    });
    check(leaveWarning, 'dirty edits made during a pending save did not request a leave warning after the save response');
  } finally {
    await page.unroute(`**/api/admin/posts/${postId}`);
  }

  // Conflict and offline behavior must preserve the draft and keep typing available.
  const sourceAgain = await sourceMode(sourceLocator());
  await sourceAgain.waitFor({ state: 'visible' });
  phase = 'offline';
  await page.context().setOffline(true);
  await setSource(sourceAgain, 'OFFLINE_SENTINEL');
  await expectAny('[data-testid="offline-status"]', 'offline status');
  await page.context().setOffline(false);
  phase = 'conflict';
  await setSource(sourceAgain, 'CONFLICT_SENTINEL');
  await page.route('**/api/admin/posts/**', route => {
    const request = route.request();
    if (request.method() === 'PUT' && new URL(request.url()).pathname === `/api/admin/posts/${postId}`) {
      return route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"conflict"}' });
    }
    return route.continue();
  });
  const save = page.getByRole('button', { name: /儲存|Save/i }).first();
  const conflictResponsePromise = page.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === `/api/admin/posts/${postId}`, { timeout: 10000 });
  if (await save.count()) await save.click();
  const conflictResponse = await conflictResponsePromise;
  check(conflictResponse.status() === 409, `conflict save returned ${conflictResponse.status()}`);
  await expectAny('[data-testid="save-conflict"]', 'conflict state');
  check((await page.locator('body').innerText()).includes('CONFLICT_SENTINEL'), 'conflict discarded source text');
  await page.unroute('**/api/admin/posts/**');
  phase = 'normal';

  // Clipboard image upload replaces an editor placeholder with the returned media URL.
  await page.context().setOffline(false);
  const pastedFilename = `editor-acceptance-${Date.now()}.png`;
  const uploadedMultipartNames = [];
  const uploadedPayloads = [];
  await page.route('**/api/admin/media', async route => {
    const multipart = route.request().postDataBuffer() || Buffer.alloc(0);
    const multipartText = multipart.toString('latin1');
    const filename = multipartText.match(/name="file"; filename="([^"]+)"/)?.[1] || '';
    const payloadStart = multipart.indexOf(Buffer.from('\r\n\r\n')) + 4;
    const payloadEnd = multipart.lastIndexOf(Buffer.from('\r\n--'));
    uploadedMultipartNames.push(filename);
    uploadedPayloads.push(payloadStart >= 4 && payloadEnd >= payloadStart ? Buffer.from(multipart.subarray(payloadStart, payloadEnd)) : Buffer.alloc(0));
    await route.continue();
  });
  await sourceAgain.click();
  const mediaResponsePromise = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/media', { timeout: 10000 });
  await page.keyboard.press('ControlOrMeta+End');
  const image = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j7S8AAAAASUVORK5CYII=', 'base64');
  const secondImage = Buffer.from('second distinct editor paste bytes');
  await page.evaluate(async ({ base64, filename }) => {
    const raw = atob(base64), bytes = Uint8Array.from(raw, c => c.charCodeAt(0));
    const file = new File([bytes], filename, { type: 'image/png' });
    const transfer = new DataTransfer(); transfer.items.add(file);
    document.activeElement.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, clipboardData: transfer }));
  }, { base64: image.toString('base64'), filename: pastedFilename });
  const mediaResponse = await mediaResponsePromise;
  check(mediaResponse.status() === 200, `pasted image upload returned ${mediaResponse.status()}`);
  const firstPasteURL = (await mediaResponse.json()).url;
  check(typeof firstPasteURL === 'string' && firstPasteURL.startsWith('/uploads/'), `first paste upload returned no local media URL: ${firstPasteURL}`);
  uploadedMediaNamesForCleanup.push(decodeURIComponent(firstPasteURL.split('/').pop()));
  const firstStoredImage = await page.evaluate(async url => {
    const response = await fetch(url);
    return { status: response.status, bytes: Array.from(new Uint8Array(await response.arrayBuffer())) };
  }, firstPasteURL);
  check(firstStoredImage.status === 200 && Buffer.from(firstStoredImage.bytes).equals(image), 'first pasted upload URL did not serve the exact uploaded bytes');
  await page.waitForFunction(url => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const value = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return value.includes(url);
  }, firstPasteURL, { timeout: 10000 });
  await sourceAgain.click();
  await page.keyboard.press('ControlOrMeta+End');
  const secondMediaResponsePromise = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/media', { timeout: 10000 });
  await page.evaluate(async ({ base64, filename }) => {
    const raw = atob(base64), bytes = Uint8Array.from(raw, c => c.charCodeAt(0));
    const file = new File([bytes], filename, { type: 'image/png' });
    const transfer = new DataTransfer(); transfer.items.add(file);
    document.activeElement.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, clipboardData: transfer }));
  }, { base64: secondImage.toString('base64'), filename: pastedFilename });
  const secondMediaResponse = await secondMediaResponsePromise;
  check(secondMediaResponse.status() === 200, `second same-name paste upload returned ${secondMediaResponse.status()}`);
  const secondPasteURL = (await secondMediaResponse.json()).url;
  check(typeof secondPasteURL === 'string' && secondPasteURL.startsWith('/uploads/') && secondPasteURL !== firstPasteURL,
    `second same-name paste did not receive a distinct local media URL: ${secondPasteURL}`);
  uploadedMediaNamesForCleanup.push(decodeURIComponent(secondPasteURL.split('/').pop()));
  const secondStoredImage = await page.evaluate(async url => {
    const response = await fetch(url);
    return { status: response.status, bytes: Array.from(new Uint8Array(await response.arrayBuffer())) };
  }, secondPasteURL);
  check(secondStoredImage.status === 200 && Buffer.from(secondStoredImage.bytes).equals(secondImage), 'second pasted upload URL did not serve the exact distinct uploaded bytes');
  await page.waitForFunction(url => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const value = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return value.includes(url);
  }, secondPasteURL, { timeout: 10000 });
  const pastedSource = await currentSource(sourceAgain);
  check(uploadedMultipartNames.length === 2 && uploadedPayloads.length === 2 &&
    uploadedMultipartNames.every(name => name && name !== pastedFilename && name.endsWith('.png')) &&
    new Set(uploadedMultipartNames).size === 2,
  `same-name paste uploads must use two unique multipart filenames, got ${JSON.stringify(uploadedMultipartNames)} for ${pastedFilename}`);
  check(uploadedPayloads[0].equals(image) && uploadedPayloads[1].equals(secondImage), 'same-name paste requests did not carry their distinct intended byte payloads');
  const firstPasteMarkdown = `![${pastedFilename}](${firstPasteURL})`;
  const secondPasteMarkdown = `![${pastedFilename}](${secondPasteURL})`;
  check((pastedSource.split(firstPasteMarkdown).length - 1) === 1 && (pastedSource.split(secondPasteMarkdown).length - 1) === 1,
  'pasting the same filename twice overwrote or lost one of the distinct uploaded URLs');
  await page.unroute('**/api/admin/media');

  // Moving first/last plain paragraphs must retain all three source ranges and rendered blocks.
  const abcSource = 'A_SENTINEL\n\nB_SENTINEL\n\nC_SENTINEL\n';
  await setSource(await sourceMode(sourceAgain), abcSource);
  await clickText(/版面|Layout/i);
  const abcBlocks = page.locator('[data-testid="layout-block"], .layout-block, [data-block-start]');
  await abcBlocks.filter({ hasText: 'A_SENTINEL' }).waitFor({ state: 'visible' });
  await abcBlocks.filter({ hasText: 'B_SENTINEL' }).waitFor({ state: 'visible' });
  await abcBlocks.filter({ hasText: 'C_SENTINEL' }).waitFor({ state: 'visible' });
  check(await abcBlocks.count() === 3, `A/B/C layout has ${await abcBlocks.count()} blocks, want exactly three`);
  await abcBlocks.filter({ hasText: 'A_SENTINEL' }).dragTo(abcBlocks.filter({ hasText: 'C_SENTINEL' }));
  await page.waitForFunction(() => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const value = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return value.indexOf('B_SENTINEL') < value.indexOf('A_SENTINEL');
  }, null, { timeout: 10000 });
  await abcBlocks.filter({ hasText: 'B_SENTINEL' }).waitFor({ state: 'visible' });
  await abcBlocks.filter({ hasText: 'C_SENTINEL' }).waitFor({ state: 'visible' });
  await abcBlocks.filter({ hasText: 'C_SENTINEL' }).dragTo(abcBlocks.filter({ hasText: 'B_SENTINEL' }));
  await page.waitForFunction(() => {
    const editor = document.querySelector('[data-testid="editor-source"], .cm-content[contenteditable="true"], textarea[name="source"], textarea[name="content"]');
    const value = typeof window.JellyEditor?.getValue === 'function' ? window.JellyEditor.getValue() : (typeof editor?.value === 'string' ? editor.value : editor?.innerText ?? '');
    return value.indexOf('C_SENTINEL') < value.indexOf('B_SENTINEL');
  }, null, { timeout: 10000 });
  await sourceMode(sourceAgain);
  const reorderedABC = await currentSource(sourceAgain);
  check(reorderedABC.split(/\n\s*\n/).filter(part => part.trim()).length === 3, 'dragging A/B/C changed the number of plain paragraph source ranges');
  for (const marker of ['A_SENTINEL', 'B_SENTINEL', 'C_SENTINEL']) {
    check((reorderedABC.match(new RegExp(marker, 'g')) || []).length === 1, `dragging A/B/C lost or duplicated ${marker}`);
  }
  const abcRender = await page.evaluate(async source => {
    const response = await fetch('/api/admin/render', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source, mode: 'fragment', sourcemap: false, expand: false })
    });
    const result = await response.json();
    return { status: response.status, html: result.html || '' };
  }, reorderedABC);
  check(abcRender.status === 200, `render after A/B/C paragraph drags returned ${abcRender.status}`);
  const renderedParagraphs = await page.evaluate(html => {
    const document = new DOMParser().parseFromString(html, 'text/html');
    return Array.from(document.body.children).filter(node => node.tagName === 'P').map(node => node.textContent.trim());
  }, abcRender.html);
  check(JSON.stringify(renderedParagraphs) === JSON.stringify(['C_SENTINEL', 'B_SENTINEL', 'A_SENTINEL']),
    `render after A/B/C drags produced ${JSON.stringify(renderedParagraphs)}, want three paragraphs in C/B/A order`);

  // The built-in countdown frame receives the editor's real theme and accepts
  // messages only from its parent with bounded CSS color values.
  const countdownSource = '::countdown{to="2031-07-08T00:00:00Z"}\n';
  const countdownRenderPromise = page.waitForResponse(r => r.request().method() === 'POST' &&
    new URL(r.url()).pathname === '/api/admin/render' && (r.request().postDataJSON()?.source || '').includes('2031-07-08T00:00:00Z'), { timeout: 10000 });
  await setSource(await sourceMode(sourceAgain), countdownSource);
  await page.getByRole('button', { name: /分割|Split/i }).first().click();
  const countdownRender = await countdownRenderPromise;
  check(countdownRender.status() === 200, `countdown theme render returned ${countdownRender.status()}`);
  const countdownPreviewPane = await expectAny('[data-testid="editor-preview"], .editor-preview', 'countdown preview');
  await countdownPreviewPane.locator('.jc-widget[data-builtin="countdown"] iframe').waitFor({ state: 'attached', timeout: 10000 });
  let countdownFrame;
  for (let attempt = 0; attempt < 100; attempt++) {
    countdownFrame = page.frames().find(frame => {
      try { return new URL(frame.url()).pathname === '/widgets/countdown/1/frame'; } catch { return false; }
    });
    if (countdownFrame) break;
    await page.waitForTimeout(100);
  }
  check(!!countdownFrame, 'native countdown frame did not attach to the preview');
  await countdownFrame.waitForLoadState('load', { timeout: 10000 });
  check(await countdownFrame.evaluate(() => document.readyState) === 'complete', 'native countdown frame did not complete its real page load');
  const countdownIframe = countdownPreviewPane.locator('.jc-widget[data-builtin="countdown"] iframe');
  const readParentFrameColors = () => countdownIframe.evaluate(frame => {
    const style = getComputedStyle(frame);
    return {
      primary: style.getPropertyValue('--primary').trim(),
      ink: style.getPropertyValue('--ink').trim(),
      surface: style.getPropertyValue('--surface').trim(),
    };
  });
  const readCountdownRootColors = () => countdownFrame.evaluate(() => {
    const style = getComputedStyle(document.documentElement);
    return {
      primary: style.getPropertyValue('--primary').trim(),
      ink: style.getPropertyValue('--color-ink').trim(),
      surface: style.getPropertyValue('--color-surface').trim(),
    };
  });
  const waitForCountdownTheme = async () => {
    for (let attempt = 0; attempt < 50; attempt++) {
      const parentColors = await readParentFrameColors();
      const frameColors = await readCountdownRootColors();
      if (frameColors.primary === parentColors.primary && frameColors.ink === parentColors.ink && frameColors.surface === parentColors.surface) {
        return { parentColors, frameColors };
      }
      await page.waitForTimeout(100);
    }
    const parentColors = await readParentFrameColors();
    const frameColors = await readCountdownRootColors();
    throw new Error(`countdown frame theme did not sync: parent=${JSON.stringify(parentColors)} frame=${JSON.stringify(frameColors)}`);
  };
  const initialCountdownTheme = await waitForCountdownTheme();
  const invalidThemeMessages = [
    { '--primary': 'url(javascript:alert(1))' },
    { '--ink': { injected: true } },
    { '--surface': 'x'.repeat(129) },
    { '--unknown-theme-prop': '#ff00ff' },
  ];
  for (const colors of invalidThemeMessages) {
    await countdownIframe.evaluate((frame, invalidColors) => {
      frame.contentWindow.postMessage({ type: 'jelly-widget-theme', colors: invalidColors }, '*');
    }, colors);
  }
  await page.waitForTimeout(100);
  const afterInvalidTheme = await readCountdownRootColors();
  check(JSON.stringify(afterInvalidTheme) === JSON.stringify(initialCountdownTheme.frameColors),
    `invalid parent theme values changed countdown colors: ${JSON.stringify(afterInvalidTheme)}`);
  const unknownThemeValue = await countdownFrame.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--unknown-theme-prop').trim());
  check(unknownThemeValue === '', `unknown theme property reached countdown frame: ${unknownThemeValue}`);
  await countdownFrame.evaluate(() => {
    window.dispatchEvent(new MessageEvent('message', {
      source: window,
      data: { type: 'jelly-widget-theme', colors: { '--primary': '#010203' } },
    }));
  });
  await page.waitForTimeout(100);
  check(JSON.stringify(await readCountdownRootColors()) === JSON.stringify(initialCountdownTheme.frameColors),
    'countdown frame accepted a theme message whose source was not its parent');

  await page.locator('#jellyEditor [data-action="focus"]').click();
  await page.locator('#focusTools [data-action="dark"]').click();
  await page.waitForFunction(() => document.querySelector('#jellyEditor')?.classList.contains('focus-dark'), null, { timeout: 5000 });
  const darkCountdownTheme = await waitForCountdownTheme();
  check(darkCountdownTheme.parentColors.ink !== initialCountdownTheme.parentColors.ink ||
    darkCountdownTheme.parentColors.surface !== initialCountdownTheme.parentColors.surface,
  'focus dark mode did not change the editor iframe theme');
  await page.keyboard.press('Escape');

  // Rendered widgets and all export formats.
  const exportSource = ':::tabs\n::: tab {title="A"}\nWidget A\n:::\n::: tab {title="B"}\nWidget B\n:::\n:::\n\n::counter{to=1200}\n';
  const widgetRenderPromise = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/admin/render' && (r.request().postDataJSON()?.source || '').includes('Widget A'), { timeout: 10000 });
  await setSource(sourceAgain, exportSource);
  check((await widgetRenderPromise).status() === 200, 'widget preview render request did not return HTTP 200');
  const preview = await expectAny('[data-testid="editor-preview"], .editor-preview', 'widget preview');
  check((await preview.innerText()).includes('Widget A'), 'widget fallback content is absent from preview');
  const exportSavePromise = page.waitForResponse(r => r.request().method() === 'PUT' && new URL(r.url()).pathname === `/api/admin/posts/${postId}`, { timeout: 15000 });
  await clickText(/儲存|Save/i);
  const exportSave = await exportSavePromise;
  check(exportSave.status() === 200, `saving export source returned ${exportSave.status()}`);
  await clickText(/匯出|Export/i);

  const jmd = (await downloadFormat('jmd', 'md')).toString('utf8');
  check(jmd === exportSource, 'Jelly Markdown export did not preserve the source byte-for-byte');
  const md = (await downloadFormat('md', 'md')).toString('utf8');
  check(md.includes('Widget A') && md.includes('Widget B') && !md.includes(':::tabs') && !md.includes('::counter'), 'plain Markdown export was not flattened as required');
  const html = (await downloadFormat('html', 'html')).toString('utf8');
  check(/<html\b/i.test(html) && /<body\b/i.test(html) && /<style\b/i.test(html) && html.includes('Widget A'), 'HTML export lacks a standalone document, embedded CSS, or rendered body');
  const pdfOption = page.locator('[data-format="pdf"]').first();
  if (await pdfOption.count()) await pdfOption.click();
  else await page.getByRole('radio', { name: /PDF/i }).check();
  phase = 'pdf';
  const beforePrint = page.evaluate(() => new Promise(resolve => window.addEventListener('beforeprint', resolve, { once: true })));
  await page.getByRole('button', { name: /列印|Print|PDF/i }).last().click();
  await Promise.race([beforePrint, page.waitForTimeout(10000).then(() => { throw new Error('PDF export did not invoke browser print'); })]);
  const cdp = await page.context().newCDPSession(page);
  const printed = await cdp.send('Page.printToPDF', { printBackground: true, transferMode: 'ReturnAsBase64' });
  const pdf = Buffer.from(printed.data, 'base64');
  check(pdf.subarray(0, 5).toString() === '%PDF-', 'browser print output is not a PDF document');
  await fs.writeFile(path.join(outDir, 'export.pdf'), pdf);
  await snap('editor-export-dialog');
  const unexpected = errors.filter(e => !e.expected);
  check(unexpected.length === 0, `unexpected browser errors: ${unexpected.map(e => `${e.kind}: ${e.message}`).join(' | ')}`);
  console.log(`Editor browser acceptance reached all requested flows. Screenshots: ${outDir}`);
} finally {
  if (postId) {
    await page.context().setOffline(false).catch(() => {});
    await page.evaluate(async id => { await fetch(`/api/admin/posts/${id}`, { method: 'DELETE' }); }, postId).catch(() => {});
  }
  if (uploadedMediaNamesForCleanup.length) {
    await page.context().setOffline(false).catch(() => {});
    await page.evaluate(async names => {
      await Promise.all(names.map(name => fetch(`/api/admin/media/${encodeURIComponent(name)}`, { method: 'DELETE' })));
    }, uploadedMediaNamesForCleanup).catch(() => {});
  }
  await browser.close();
}
