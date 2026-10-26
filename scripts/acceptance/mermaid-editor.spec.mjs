// Editor-side Mermaid acceptance for ticket 11 joined with ticket 09.
// Run against a task-local server and PostgreSQL database:
// JELLY_BASE_URL=... JELLY_ADMIN_USER=... JELLY_ADMIN_PASSWORD=... node scripts/acceptance/mermaid-editor.spec.mjs
import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const origin = new URL(base).origin;
const user = process.env.JELLY_ADMIN_USER;
const password = process.env.JELLY_ADMIN_PASSWORD;
if (!user || !password) { console.error('Set JELLY_ADMIN_USER and JELLY_ADMIN_PASSWORD'); process.exit(2); }
const run = Date.now().toString(36);
const printDiagram = ['sequenceDiagram', ...Array.from({ length: 12 }, (_, i) => `participant P${i} as UNIQUEPARTICIPANT${i}`), 'P0->>P11: Published'].join('\n');
const source = `# Mermaid editor ${run}\n\n\`\`\`mermaid\n${printDiagram}\n\`\`\`\n`;
const failures = [];
let fixtureId = 0;
const check = async (name, fn) => { try { await fn(); console.log(`PASS ${name}`); } catch (e) { failures.push(name); console.error(`FAIL ${name}: ${e.stack || e}`); } };
const assert = (value, message) => { if (!value) throw Error(message); };
async function login(page) {
  await page.goto(`${base}/login`, { waitUntil: 'domcontentloaded' });
  await page.locator('#loginUsername').fill(user);
  await page.locator('#loginPassword').fill(password);
  await page.locator('#localLoginForm button[type=submit]').click();
  await page.waitForURL(url => !url.pathname.includes('login'), { timeout: 15000 });
}
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: 'light' });
const consoleErrors = [], externalRequests = [], assetRequests = [];
let pdfNotImplementedObserved = false, printCallbackObserved = false;
await context.route('**/*', route => {
  const url = route.request().url();
  if (new URL(url).origin !== origin) { externalRequests.push(url); return route.abort(); }
  if (/pele\.global\.js|jelly-mermaid\.js/.test(url)) assetRequests.push(url);
  return route.continue();
});
const page = await context.newPage();
page.on('pageerror', e => consoleErrors.push(e.message));
page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push(msg.text()); });
page.on('response', response => { const url=new URL(response.url()); if(url.pathname===`/api/admin/posts/${fixtureId}/export`&&url.searchParams.get('format')==='pdf'&&response.status()===501)pdfNotImplementedObserved=true; });
try {
  await check('authenticated editor loads without Mermaid assets before preview', async () => {
    await login(page);
    assert(assetRequests.length === 0, `Mermaid assets loaded before preview: ${assetRequests.join(', ')}`);
  });
  await check('CodeMirror preview, edit, repeat preview, save and reopen preserve source', async () => {
    const created = await page.evaluate(async source => {
      const r = await fetch('/api/admin/posts', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({title:'Mermaid acceptance', slug:`mermaid-editor-${Date.now().toString(36)}`, status:'draft', source, format:'jmd'}) });
      if (!r.ok) throw Error(`create post ${r.status}: ${await r.text()}`); return r.json();
    }, source);
    fixtureId = created.id;
    await page.goto(`${base}/admin/posts/${created.id}`, { waitUntil: 'networkidle' });
    const editor = page.locator('[data-testid=editor-source]');
    await editor.waitFor();
    await page.getByRole('button', { name: /Preview|預覽/ }).click();
    await page.waitForTimeout(500);
    const preview = page.locator('#editorPreview');
    await preview.locator('figure.jc-mermaid svg.pele').waitFor({ timeout: 10000 });
    assert(await page.locator('pre > code.language-mermaid').evaluate(el => el.parentElement.hidden), 'rendered source pre was not hidden');
    await page.locator('button[data-view=source]').click();
    const editedSource=source+'\n\nChanged\n';
    await page.locator('#editorSource .cm-content').click(); await page.keyboard.press(process.platform==='darwin'?'Meta+A':'Control+A'); await page.keyboard.insertText(editedSource);
    assert(await page.evaluate(()=>window.JellyEditor?.getValue())===editedSource,'CodeMirror did not replace the complete source with the expected valid diagram');
    await page.locator('button[data-view=preview]').click(); await page.locator('#editorPreview figure.jc-mermaid svg.pele').waitFor({ timeout: 10000 });
    await page.locator('label').filter({ has: page.locator('input[name=status][value=published]') }).locator('span').click();
    const publishResponse=page.waitForResponse(response=>response.url().includes(`/api/admin/posts/${created.id}`)&&response.request().method()==='PUT');
    await page.locator('button[data-action=save]').click();
    const publishResult=await publishResponse;
    assert(publishResult.ok(),`publishing editor save returned HTTP ${publishResult.status()}`);
    const reopened = await page.evaluate(async id => { const r=await fetch(`/api/admin/posts/${id}`); return r.json(); }, created.id);
    assert(reopened.source===editedSource, 'saved source did not survive reopen byte-for-byte');
    const publicPage = await context.newPage();
    await publicPage.goto(`${base}/post/${created.slug}`, { waitUntil: 'domcontentloaded' });
    const publicSource = publicPage.locator('pre > code.language-mermaid');
    await publicSource.waitFor({ state: 'attached' });
    await publicPage.locator('figure.jc-mermaid svg.pele').waitFor({ timeout: 10000 });
    assert(await publicSource.evaluate(el => el.parentElement.hidden), 'public diagram rendered without hiding its source');
    await publicPage.close();
    assert(assetRequests.length > 0, 'Mermaid preview never requested local rendering assets');
  });
  await check('editor reports first local asset failure and retries successfully', async () => {
    assert(fixtureId, 'editor fixture is unavailable');
    const state=await context.storageState(), retryContext=await browser.newContext({storageState:state,viewport:{width:1280,height:900}});
    let failedOnce=false;
    let blockRenderer=true;
    await retryContext.route('**/*',route=>{
      const url=new URL(route.request().url());
      if(url.origin!==origin)return route.abort();
      if(url.pathname==='/admin/assets/pele/pele.global.js'&&blockRenderer){failedOnce=true;return route.abort();}
      return route.continue();
    });
    const retryPage=await retryContext.newPage();
    try {
      await retryPage.goto(`${base}/admin/posts/${fixtureId}`,{waitUntil:'networkidle'});
      await retryPage.locator('#editorNotice[data-testid="render-error"]').waitFor({timeout:10000});
      assert((await retryPage.locator('#editorPreview pre > code.language-mermaid').count())>0,'asset failure removed the source block');
      blockRenderer=false;
      const retrySource=source+'\n\nRetry succeeded\n';
      await retryPage.locator('#editorSource .cm-content').click();await retryPage.keyboard.press(process.platform==='darwin'?'Meta+A':'Control+A');await retryPage.keyboard.insertText(retrySource);
      assert(await retryPage.evaluate(()=>window.JellyEditor?.getValue())===retrySource,'CodeMirror retry did not replace the complete valid source');
      await retryPage.waitForResponse(response=>response.url().endsWith('/api/admin/render')&&response.request().method()==='POST'&&response.ok());
      await retryPage.locator('#editorPreview figure.jc-mermaid svg.pele').waitFor({timeout:15000});
      assert(await retryPage.locator('#editorPreview pre > code.language-mermaid').evaluate(el=>el.parentElement.hidden),'retry did not hide the successfully rendered source');
      assert(failedOnce,'test never intercepted the first Pele asset request');
    } finally { await retryContext.close(); }
  });
  await check('late asset load cannot restore a Mermaid preview after source changes', async () => {
    assert(fixtureId, 'editor fixture is unavailable');
    const state=await context.storageState(), delayedContext=await browser.newContext({storageState:state,viewport:{width:1280,height:900}});
    let startLoad,releaseLoad;const loadStarted=new Promise(resolve=>{startLoad=resolve;}),loadGate=new Promise(resolve=>{releaseLoad=resolve;});
    let delayed=false,loadTimeout;
    await delayedContext.route('**/*',async route=>{
      const url=new URL(route.request().url());if(url.origin!==origin)return route.abort();
      if(url.pathname==='/admin/assets/pele/pele.global.js'&&!delayed){delayed=true;const response=await route.fetch();startLoad();await loadGate;return route.fulfill({response});}
      return route.continue();
    });
    const delayedPage=await delayedContext.newPage();
    try {
      await delayedPage.goto(`${base}/admin/posts/${fixtureId}`,{waitUntil:'domcontentloaded'});
      await Promise.race([loadStarted,new Promise((_,reject)=>{loadTimeout=setTimeout(()=>reject(Error('Pele asset request did not start')),15000);})]);clearTimeout(loadTimeout);
      const cm=delayedPage.locator('#editorSource .cm-content');await cm.click();await delayedPage.keyboard.press(process.platform==='darwin'?'Meta+A':'Control+A');await delayedPage.keyboard.insertText('Plain source after delayed request');
      const sourceBeforeRelease=await delayedPage.evaluate(()=>window.JellyEditor?.getValue());
      assert(sourceBeforeRelease==='Plain source after delayed request',`CodeMirror source was not replaced before releasing Pele: ${sourceBeforeRelease}`);
      releaseLoad();
      await delayedPage.waitForFunction(()=>document.querySelector('#editorPreview')?.textContent.includes('Plain source after delayed request'),null,{timeout:10000});
      const view=await delayedPage.evaluate(()=>({text:document.querySelector('#editorPreview')?.innerText||'',figures:document.querySelectorAll('#editorPreview figure.jc-mermaid').length}));
      assert(view.text.includes('Plain source after delayed request'),`new plain source was lost: ${view.text}`);
      assert(view.figures===0,`late Mermaid load restored an old preview (${view.figures} figure(s))`);
    } finally { clearTimeout(loadTimeout);releaseLoad();await delayedContext.close(); }
  });
  await check('preview renderer handles repeated calls, removed blocks, retries, and unsafe SVG', async () => {
    const result = await page.evaluate(async () => {
      const probeRoots=[];
      const makeRoot=()=>{const root=document.createElement('div');root.className='mermaid-editor-probe';document.body.append(root);probeRoots.push(root);return root;};
      const firstRoot=makeRoot(),src = document.createElement('pre'); src.innerHTML='<code class="language-mermaid">flowchart TD\n A-->B</code>'; firstRoot.append(src);
      for (const path of ['/admin/assets/pele/pele.global.js','/admin/assets/jelly-mermaid.js']) {
        await new Promise((resolve, reject) => { const s=document.createElement('script'); s.src=path; s.onload=resolve; s.onerror=()=>reject(Error(`failed to load ${path}`)); document.head.append(s); });
      }
      if (!window.JellyMermaid) return { missing: true };
      const savedPele=window.pele, savedJelly=window.JellyMermaid, fontsDescriptor=Object.getOwnPropertyDescriptor(document.fonts,'ready'), colorResults=[];
      try {
      const realPele=window.pele;
      for (const color of ['rgb(21, 22, 26)', 'rgb(236, 237, 241)']) {
        const parent=makeRoot(); parent.style.setProperty('--ink',color);
        const themed=document.createElement('pre'); themed.innerHTML='<code class="language-mermaid">flowchart TD\n A-->B</code>'; parent.append(themed);
        window.pele=realPele; await window.JellyMermaid.render(parent);
        const label=parent.querySelector('figure.jc-mermaid svg.pele text');
        colorResults.push(label ? getComputedStyle(label).fill : 'missing-text'); parent.remove();
      }
      window.pele = { render: () => ({ svg: '<svg xmlns="http://www.w3.org/2000/svg"><script>bad()</script><foreignObject/><image href="https://evil.test/x"/><use href="//evil.test/x"/><a href="java&#x09;script:bad"><text>bad</text></a><animate attributeName="fill"/><path style="fill:url(\\6a avascript:bad)" onload="bad()"/></svg>', width: 100, height: 40 }) };
      await window.JellyMermaid.render(document); const first=src.nextElementSibling;
      await window.JellyMermaid.render(document); const stable=first===src.nextElementSibling;
      const unsafe=src.nextElementSibling?.outerHTML||'';
      src.querySelector('code').className='language-text'; await window.JellyMermaid.render(firstRoot);
      const classRemovalRestoredSource=!src.hidden&&!src.nextElementSibling;
      src.remove(); await window.JellyMermaid.render(document);
      const retryRoot=makeRoot(),retry = document.createElement('pre'); retry.innerHTML='<code class="language-mermaid">flowchart TD\n C-->D</code>'; retryRoot.append(retry);
      window.pele = undefined; await window.JellyMermaid.render(retry);
      const errorShown = retry.nextElementSibling?.matches('p.jc-mermaid-error');
      let renderedSource=''; window.pele = { render: source => { renderedSource=source; return { svg:'<svg xmlns="http://www.w3.org/2000/svg"><text>ok</text></svg>', width:40, height:20 }; } };
      await window.JellyMermaid.render(retry);
      const recovered = retry.nextElementSibling?.matches('figure.jc-mermaid'); retryRoot.remove();
      let release; const gate = new Promise(resolve => { release=resolve; });
      Object.defineProperty(document.fonts, 'ready', { configurable:true, get:()=>gate });
      const delayedRoot=makeRoot(),delayed = document.createElement('pre'); delayed.innerHTML='<code class="language-mermaid">OLD SOURCE</code>'; delayedRoot.append(delayed);
      const pending = window.JellyMermaid.render(delayed); delayed.querySelector('code').textContent='NEW SOURCE'; release(); await pending;
      const changedSourceUsed = renderedSource === 'NEW SOURCE'; delayedRoot.remove();
      const detachedRoot=makeRoot(), detached=document.createElement('pre'); detached.innerHTML='<code class="language-mermaid">REMOVED ROOT</code>'; detachedRoot.append(detached);
      const removedPending=window.JellyMermaid.render(detachedRoot); detachedRoot.remove(); await window.JellyMermaid.render(document);
      await removedPending; const removedRootStayedGone=!document.body.contains(detachedRoot);
      const direct=document.createElement('pre');direct.className='mermaid-editor-probe';probeRoots.push(direct);direct.innerHTML='<code class="language-mermaid">DIRECT ROOT</code>';document.body.append(direct);await window.JellyMermaid.render(direct);direct.querySelector('code').remove();await window.JellyMermaid.render(direct);const directRootRestored=!direct.hidden&&!direct.nextElementSibling;direct.remove();
      return { stable, unsafe, removed: !document.body.contains(first), errorShown, recovered, changedSourceUsed, removedRootStayedGone, directRootRestored, classRemovalRestoredSource, colorResults };
      } finally {
        window.pele=savedPele; window.JellyMermaid=savedJelly;
        if(fontsDescriptor)Object.defineProperty(document.fonts,'ready',fontsDescriptor);else delete document.fonts.ready;
        probeRoots.forEach(root=>{const next=root.nextElementSibling;if(next?.matches('figure.jc-mermaid,p.jc-mermaid-error'))next.remove();root.remove();});
      }
    });
    assert(!result.missing, 'JellyMermaid editor integration is not available');
    assert(result.stable && result.removed, 'repeat rendering or removed-root cleanup failed');
    assert(result.errorShown && result.recovered, 'missing renderer did not report an error and recover on retry');
    assert(result.changedSourceUsed && result.removedRootStayedGone, 'delayed font wait rendered stale source or resurrected a removed root');
    assert(result.classRemovalRestoredSource&&result.directRootRestored,'removing the Mermaid class/code left source hidden or orphaned a figure');
    assert(result.colorResults[0] === 'rgb(21, 22, 26)' && result.colorResults[1] === 'rgb(236, 237, 241)', `parent theme colors did not reach SVG text: ${result.colorResults}`);
    assert(!/script|foreignObject|onload|evil\.test|javascript|animate/i.test(result.unsafe), `unsafe SVG survived: ${result.unsafe}`);
  });
  await check('localized responsive views and offline export controls work', async () => {
    const oldProfile=await page.evaluate(async()=>{const r=await fetch('/api/account/profile');if(!r.ok)throw Error(`profile GET ${r.status}`);return r.json();});
    try { for (const lang of ['zh-TW', 'en']) {
      const changed=await page.evaluate(async locale=>{const r=await fetch('/api/account/profile',{method:'PATCH',headers:{'Content-Type':'application/json','X-Jelly-CSRF':'1'},body:JSON.stringify({locale})});return {ok:r.ok,status:r.status};},lang);
      assert(changed.ok,`could not set actual account locale ${lang}: ${changed.status}`);
      await page.reload({waitUntil:'networkidle'});
      const uiText=await page.locator('button[data-action=save]').innerText();
      assert(uiText===(lang==='en'?'Save':'儲存'),`editor UI locale ${lang} did not update: ${uiText}`);
      for (const width of [390, 1280]) for (const colorScheme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: 844 }); await page.emulateMedia({ colorScheme });
      const view=await page.evaluate(async lang=>{
        const savedPele=window.pele, descriptor=Object.getOwnPropertyDescriptor(document.fonts,'ready');
        try { document.documentElement.lang=lang; const root=document.createElement('div'),pre=document.createElement('pre');pre.lang=lang;pre.innerHTML='<code class="language-mermaid">flowchart TD\n A-->B</code>';root.append(pre);document.body.append(root);window.pele=undefined;await window.JellyMermaid.render(pre);const error=pre.nextElementSibling?.textContent||'';root.remove();return {error,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,svg:!!document.querySelector('#editorPreview figure.jc-mermaid svg.pele'),hidden:[...document.querySelectorAll('#editorPreview pre > code.language-mermaid')].every(code=>code.parentElement.hidden)}; }
        finally {window.pele=savedPele;if(descriptor)Object.defineProperty(document.fonts,'ready',descriptor);else delete document.fonts.ready;}
      },lang);
      assert(view.error.startsWith(lang==='en'?'Diagram could not be displayed: ':'圖表無法顯示：'),`${lang} renderer error was not localized: ${view.error}`);
      assert(!view.overflow,`page overflows at ${width}px in ${colorScheme} mode (${lang})`);
      assert(view.svg&&view.hidden,`rendered diagram/pre state missing at ${width}px ${colorScheme} ${lang}`);
      }
    }} finally {
      await page.evaluate(async locale=>{await fetch('/api/account/profile',{method:'PATCH',headers:{'Content-Type':'application/json','X-Jelly-CSRF':'1'},body:JSON.stringify({locale})});},oldProfile.locale);
      await page.reload({waitUntil:'networkidle'});
    }
    await page.locator('button[data-view=layout]').click();
    await page.locator('[data-action=mobile]').click();
    await page.locator('[data-action=focus]').click();
    await page.locator('[data-action=dark]').click();
    await page.locator('[data-action=focus]').click();
    await page.locator('[data-action=export]').click();
    assert(await page.locator('#editorExport').isVisible(), 'export dialog did not open');
    await page.locator('[data-format=html]').click();
    const html = await page.evaluate(async id => { const r=await fetch(`/api/admin/posts/${id}/export?format=html`); if(!r.ok) throw Error(`HTML export ${r.status}`); return r.text(); }, fixtureId);
    assert(!/<script[^>]+src\s*=\s*["']https?:/i.test(html), 'HTML export references an external script');
    const exportPage = await context.newPage();
    await exportPage.setContent(html, { waitUntil: 'load' });
    await exportPage.locator('figure.jc-mermaid svg.pele').waitFor({ timeout: 10000 });
    await exportPage.close();
    await page.evaluate(() => { window.__printedWithDiagram = false; window.print = () => { window.__printedWithDiagram = !!document.querySelector('#editorPrintDocument figure.jc-mermaid svg.pele'); }; });
    await page.locator('[data-format=pdf]').click();
    await page.locator('#exportDownload').click();
    await page.waitForFunction(() => window.__printedWithDiagram === true, null, { timeout: 10000 });
    printCallbackObserved=true;
    assert(pdfNotImplementedObserved,'PDF export did not take the documented 501-to-HTML print fallback');
    const pdf = await page.pdf({ format: 'A4', printBackground: true });
    const extracted = spawnSync('/opt/homebrew/bin/pdftotext', ['-', '-'], { input: pdf, encoding: 'utf8' });
    assert(extracted.status === 0, `pdftotext failed: ${extracted.stderr}`);
    const missing = Array.from({ length: 12 }, (_, i) => `UNIQUEPARTICIPANT${i}`).filter(label => !extracted.stdout.includes(label));
    assert(missing.length === 0, `printed PDF clipped diagram labels: ${missing.join(', ')}`);
    await page.locator('#editorExport [data-action=close]').click();
  });
  await check('no browser errors or external network requests', async () => {
    const expected501='Failed to load resource: the server responded with a status of 501 (Not Implemented)';
    const unexpected=consoleErrors.filter(message=>message!==expected501);
    if(consoleErrors.includes(expected501))assert(pdfNotImplementedObserved,'501 console entry has no matching PDF export response');
    if(pdfNotImplementedObserved)assert(printCallbackObserved,'PDF fallback returned 501 but did not invoke print callback');
    assert(unexpected.length === 0, unexpected.join(' | '));
    assert(externalRequests.length === 0, externalRequests.join(' | '));
  });
} finally {
  if (fixtureId) {
    const cleanup = await page.evaluate(async id => { const r = await fetch(`/api/admin/posts/${id}`, { method: 'DELETE' }); return { ok: r.ok, status: r.status }; }, fixtureId).catch(() => null);
    if (!cleanup?.ok) console.error(`Fixture cleanup not confirmed for post ${fixtureId}: HTTP ${cleanup?.status || 'network error'}`);
  }
  await context.close(); await browser.close();
}
if (failures.length) process.exitCode = 1;
