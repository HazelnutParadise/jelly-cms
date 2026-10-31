// Browser acceptance for ticket 11 (docs/tickets/11-mermaid.md): Mermaid diagrams drawn in the browser by
// the vendored Pele build through web/admin/assets/jelly-mermaid.js. These checks are expected to fail until
// slots M1–M3 are implemented. Do not edit them to match an implementation.
//
// How to run (the main agent runs this during acceptance; it is not part of CI):
//
//   1. Start Jelly CMS from the repository root against a dedicated development database, never real data:
//        go run ./cmd/server            # listens on :8080; finish /install first on a fresh database
//      Any admin or editor account works; the script creates its own fixture posts and pages through
//      POST /api/admin/posts and moves them to the recycle bin (DELETE) when it finishes.
//   2. Run:
//        JELLY_BASE_URL=http://127.0.0.1:8080 JELLY_ADMIN_USER=<username or email> \
//        JELLY_ADMIN_PASSWORD=<password> node scripts/acceptance/mermaid.spec.mjs
//      PLAYWRIGHT_MODULE may name the playwright module (default: "playwright", resolved through NODE_PATH).
//      Exit status 0 means every check passed; each check prints PASS or FAIL with the reason.
//
// Every request to a host other than JELLY_BASE_URL is aborted and recorded; any such request fails the run,
// so the diagrams must render with all external domains blocked.

import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const base = (process.env.JELLY_BASE_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const origin = new URL(base).origin;
const user = process.env.JELLY_ADMIN_USER;
const password = process.env.JELLY_ADMIN_PASSWORD;
if (!user || !password) {
  console.error('set JELLY_ADMIN_USER and JELLY_ADMIN_PASSWORD (an admin or editor account on JELLY_BASE_URL)');
  process.exit(2);
}
const run = Date.now().toString(36);

// ---- fixtures --------------------------------------------------------------------------------------------

const FLOW = `flowchart TD
  草稿[撰寫草稿] --> 審稿{編輯審稿}
  審稿 -->|通過| 排程[排程發布]
  審稿 -->|退回修改| 草稿
  排程 --> 公開([公開文章])`;
const SEQUENCE = `sequenceDiagram
  participant 訪客
  participant 網站
  participant 資料庫
  訪客->>網站: 開啟文章
  網站->>資料庫: 查詢已發布內容
  資料庫-->>網站: 文章與圖表原文
  網站-->>訪客: 回傳 HTML`;
const ER = `erDiagram
  作者 ||--o{ 文章 : 撰寫
  文章 }o--o{ 標籤 : 標記
  作者 {
    string 名稱
    string 信箱
  }`;
const SYNTAX_ERROR = `flowchart TD
  A[開始 --> B{是否登入 <img src=x onerror="window.__jcPwned='error-text'">`;
const UNSUPPORTED = `notADiagram
  甲 --> 乙`;
const TOO_LONG = 'flowchart TD\n' + Array.from({ length: 6000 }, (_, i) => `  n${i} --> n${i + 1}`).join('\n');
const MALICIOUS = `flowchart TD
  A["<script>window.__jcPwned='label-script'</script>點我"] --> B["<img src=x onerror=window.__jcPwned='label-img'>"]
  B --> C[外部連結]
  C --> D[站內連結]
  D --> E[網路路徑]
  E --> F[電話]
  click A "javascript:window.__jcPwned='click-js'"
  click B href "javascript:window.__jcPwned='href-js'"
  click C "https://example.com/外部" _blank
  click D "/post/站內"
  click E "//evil.example/x"
  click F "tel:+886212345678"`;
const WIDE = `sequenceDiagram
  participant 訪客
  participant 瀏覽器
  participant 網站
  participant 快取
  participant 資料庫
  participant 附件庫
  participant 搜尋索引
  participant 通知中心
  participant 統計外掛
  participant 稽核紀錄
  訪客->>瀏覽器: 點開文章連結
  瀏覽器->>網站: 請求文章頁面
  網站->>快取: 讀取頁面快取
  網站->>資料庫: 查詢已發布文章
  網站->>附件庫: 取得封面網址
  網站->>搜尋索引: 更新熱門關鍵字
  網站->>通知中心: 通知作者有新讀者
  網站->>統計外掛: 記錄一次閱讀
  網站->>稽核紀錄: 寫入存取紀錄
  網站-->>瀏覽器: 回傳 HTML`;

const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
// Same shape as the editor's Markdown converter (marked 15) produces for a ```mermaid fence.
const block = src => `<pre><code class="language-mermaid">${esc(src)}\n</code></pre>\n`;
const fence = src => '```mermaid\n' + src + '\n```\n';

const FIXTURES = {
  three: { type: 'post', title: `Mermaid 三種圖 ${run}`, parts: ['<p>流程圖、循序圖與 ER 圖。</p>\n', FLOW, '<p>中間的段落。</p>\n', SEQUENCE, ER] },
  page: { type: 'page', title: `Mermaid 頁面 ${run}`, parts: ['<p>頁面裡的圖。</p>\n', FLOW] },
  errors: { type: 'post', title: `Mermaid 錯誤 ${run}`, parts: [SYNTAX_ERROR, UNSUPPORTED, TOO_LONG, SEQUENCE] },
  malicious: { type: 'post', title: `Mermaid 惡意 ${run}`, parts: [MALICIOUS] },
  wide: { type: 'post', title: `Mermaid 過寬 ${run}`, parts: ['<p>參與者很多的循序圖。</p>\n', WIDE] },
};

// ---- API -------------------------------------------------------------------------------------------------

async function login() {
  const res = await fetch(`${base}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-Jelly-CSRF': '1', Origin: origin },
    body: JSON.stringify({ username: user, password }),
  });
  if (!res.ok) throw new Error(`POST /auth/login answered ${res.status}: ${await res.text()}`);
  // The admin API accepts the browser login only (ticket 23), so keep the jwt_token cookie, not a Bearer token.
  const cookie = (res.headers.getSetCookie?.() ?? []).map(c => c.split(';')[0]).find(c => c.startsWith('jwt_token='));
  if (!cookie) throw new Error('POST /auth/login set no jwt_token cookie');
  return cookie;
}

async function api(session, method, path, payload) {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: { Cookie: session, Origin: origin, 'Content-Type': 'application/json', Accept: 'application/json', 'X-Jelly-CSRF': '1' },
    body: payload === undefined ? undefined : JSON.stringify(payload),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path} answered ${res.status}: ${text}`);
  return text ? JSON.parse(text) : null;
}

async function createFixtures(session) {
  const created = {};
  for (const [key, f] of Object.entries(FIXTURES)) {
    const slug = `mermaid-acceptance-${key}-${run}`;
    const html = f.parts.map(p => (p.startsWith('<') ? p : block(p))).join('');
    const markdown = f.parts.map(p => (p.startsWith('<') ? p.replace(/<\/?p>/g, '') + '\n' : fence(p))).join('\n');
    const post = await api(session, 'POST', '/api/admin/posts', {
      title: f.title, slug, type: f.type, status: 'published', visibility: 'public', content: html, meta: { markdown },
    });
    created[key] = { id: post.id, markdown, path: f.type === 'page' ? `/${slug}` : `/post/${slug}` };
  }
  return created;
}

// ---- runner ----------------------------------------------------------------------------------------------

const results = [];
async function check(name, fn) {
  try {
    await fn();
    results.push([name, true]);
    console.log(`PASS ${name}`);
  } catch (err) {
    results.push([name, false]);
    console.log(`FAIL ${name}\n     ${String(err && err.stack || err).split('\n').slice(0, 3).join('\n     ')}`);
  }
}
function assert(cond, message) {
  if (!cond) throw new Error(message);
}

const blocked = [];
async function openPage(browser, path, { width = 1280, colorScheme = 'light' } = {}) {
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme });
  await context.route('**/*', route => {
    const url = route.request().url();
    if (new URL(url).origin === origin) return route.continue();
    blocked.push(url);
    return route.abort();
  });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.on('dialog', d => { errors.push(`dialog opened: ${d.message()}`); d.dismiss(); });
  const res = await page.goto(base + path, { waitUntil: 'load' });
  assert(res && res.ok(), `GET ${path} answered ${res && res.status()}`);
  // Every mermaid block ends up followed by its figure or its error notice.
  await page.waitForFunction(() => [...document.querySelectorAll('pre > code.language-mermaid')].every(code => {
    const next = code.parentElement.nextElementSibling;
    return next && (next.matches('figure.jc-mermaid') || next.matches('p.jc-mermaid-error'));
  }), null, { timeout: 15000 });
  return { page, context, errors };
}

// Summary of every mermaid block on the page, in order.
const blocksOf = page => page.evaluate(() => [...document.querySelectorAll('pre > code.language-mermaid')].map(code => {
  const pre = code.parentElement;
  const next = pre.nextElementSibling;
  const figure = next && next.matches('figure.jc-mermaid') ? next : null;
  const error = next && next.matches('p.jc-mermaid-error') ? next : null;
  return {
    hidden: pre.hidden,
    svg: !!(figure && figure.querySelector(':scope > svg.pele, :scope svg.pele')),
    text: figure ? figure.textContent : '',
    error: error ? error.textContent : null,
    errorRole: error ? error.getAttribute('role') : null,
  };
}));

// ---- checks ----------------------------------------------------------------------------------------------

const session = await login();
const fx = await createFixtures(session);
const browser = await chromium.launch();

try {
  await check('三種圖（中文標籤）在同一頁都畫成 SVG，原文保留在隱藏的 <pre>', async () => {
    const { page, context, errors } = await openPage(browser, fx.three.path);
    const blocks = await blocksOf(page);
    assert(blocks.length === 3, `want 3 mermaid blocks, found ${blocks.length}`);
    blocks.forEach((b, i) => {
      assert(b.svg, `block ${i}: no figure.jc-mermaid with svg.pele after the <pre>`);
      assert(b.hidden, `block ${i}: the source <pre> must be hidden after a successful render`);
    });
    for (const [i, label] of [[0, '撰寫草稿'], [1, '查詢已發布內容'], [2, '作者']]) {
      assert(blocks[i].text.includes(label), `block ${i}: the SVG does not show the Chinese label ${label}`);
    }
    const figures = await page.locator('figure.jc-mermaid').count();
    assert(figures === 3, `want 3 figures, found ${figures}`);
    const source = await page.evaluate(() => document.querySelector('pre > code.language-mermaid').textContent);
    assert(source.includes('草稿[撰寫草稿] --> 審稿{編輯審稿}'), 'the original source is not kept in the code element');
    assert(errors.length === 0, `page errors: ${errors.join(' | ')}`);
    await context.close();
  });

  await check('頁面（type=page）同樣畫出圖表', async () => {
    const { page, context } = await openPage(browser, fx.page.path);
    const blocks = await blocksOf(page);
    assert(blocks.length === 1 && blocks[0].svg, 'the static page diagram did not render');
    await context.close();
  });

  await check('重複呼叫 JellyMermaid.render(document) 不重畫也不重複產生', async () => {
    const { page, context } = await openPage(browser, fx.three.path);
    const same = await page.evaluate(async () => {
      const before = [...document.querySelectorAll('figure.jc-mermaid')];
      const svgs = before.map(f => f.querySelector('svg'));
      await window.JellyMermaid.render(document);
      await window.JellyMermaid.render(document);
      const after = [...document.querySelectorAll('figure.jc-mermaid')];
      return { before: before.length, after: after.length, sameFigures: before.every((f, i) => f === after[i]), sameSvgs: after.every((f, i) => f.querySelector('svg') === svgs[i]) };
    });
    assert(same.after === same.before, `figure count changed from ${same.before} to ${same.after}`);
    assert(same.sameFigures && same.sameSvgs, 'unchanged sources must keep their existing figure and SVG');
    await context.close();
  });

  await check('改原文後重畫且只剩一個 figure；移除區塊時清掉對應的 figure', async () => {
    const { page, context } = await openPage(browser, fx.three.path);
    const result = await page.evaluate(async () => {
      const code = document.querySelector('pre > code.language-mermaid');
      code.textContent = 'flowchart LR\n  甲[改過的節點] --> 乙[第二個節點]\n';
      await window.JellyMermaid.render(document);
      const pre = code.parentElement;
      const following = [];
      for (let n = pre.nextElementSibling; n && n.matches('figure.jc-mermaid, p.jc-mermaid-error'); n = n.nextElementSibling) following.push(n);
      const edited = { count: following.length, text: following[0] ? following[0].textContent : '', total: document.querySelectorAll('figure.jc-mermaid').length };
      const last = [...document.querySelectorAll('pre > code.language-mermaid')].pop().parentElement;
      last.remove();
      await window.JellyMermaid.render(document);
      return { edited, afterRemove: document.querySelectorAll('figure.jc-mermaid').length, orphan: [...document.querySelectorAll('figure.jc-mermaid')].filter(f => !(f.previousElementSibling && f.previousElementSibling.matches('pre') && f.previousElementSibling.querySelector('code.language-mermaid'))).length };
    });
    assert(result.edited.count === 1, `want exactly one figure after the edited block, found ${result.edited.count}`);
    assert(result.edited.text.includes('改過的節點'), 'the edited diagram was not redrawn with the new label');
    assert(result.edited.total === 3, `want 3 figures after the edit, found ${result.edited.total}`);
    assert(result.afterRemove === 2, `want 2 figures after removing a block, found ${result.afterRemove}`);
    assert(result.orphan === 0, `${result.orphan} figure(s) left without their source block`);
    await context.close();
  });

  await check('錯誤語法、不支援的圖種與過長原文：保留原文並提示，其他圖照常', async () => {
    const { page, context, errors } = await openPage(browser, fx.errors.path);
    const blocks = await blocksOf(page);
    assert(blocks.length === 4, `want 4 mermaid blocks, found ${blocks.length}`);
    for (const [i, kind] of [[0, '語法錯誤'], [1, '不支援的圖種'], [2, '過長原文']]) {
      const b = blocks[i];
      assert(!b.hidden, `${kind}: the source <pre> must stay visible`);
      assert(!b.svg, `${kind}: no figure may be drawn`);
      assert(b.error && b.error.startsWith('圖表無法顯示：') && b.error.length > '圖表無法顯示：'.length, `${kind}: want a notice starting with 圖表無法顯示： and the reason, got ${JSON.stringify(b.error)}`);
      assert(b.errorRole === 'status', `${kind}: the notice must have role="status"`);
    }
    assert(blocks[2].error.includes('50000'), `too long: the notice must name the 50000 character limit, got ${JSON.stringify(blocks[2].error)}`);
    const injected = await page.evaluate(() => ({
      pwned: window.__jcPwned,
      // A decorative icon <svg> is allowed; any other element means the message was parsed as HTML.
      imgs: [...document.querySelectorAll('p.jc-mermaid-error')].reduce((n, p) => n + p.querySelectorAll('*:not(svg):not(svg *)').length, 0),
    }));
    assert(injected.pwned === undefined, `error text executed markup: window.__jcPwned = ${injected.pwned}`);
    assert(injected.imgs === 0, 'the error message must be inserted as text, but the notice contains HTML elements');
    assert(blocks[3].svg && blocks[3].hidden, 'the valid diagram after the failing ones must still render');
    assert(errors.length === 0, `page errors: ${errors.join(' | ')}`);
    await context.close();
  });

  await check('錯誤修正後重畫：提示移除、圖表出現、原文隱藏', async () => {
    const { page, context } = await openPage(browser, fx.errors.path);
    const fixed = await page.evaluate(async () => {
      const code = document.querySelector('pre > code.language-mermaid');
      code.textContent = 'flowchart TD\n  A[開始] --> B{是否登入}\n';
      await window.JellyMermaid.render(document);
      const pre = code.parentElement;
      return { hidden: pre.hidden, next: pre.nextElementSibling && pre.nextElementSibling.className, notices: pre.parentElement.querySelectorAll('p.jc-mermaid-error').length };
    });
    assert(fixed.hidden, 'the fixed block must hide its source');
    assert(fixed.next === 'jc-mermaid', `the fixed block must be followed by figure.jc-mermaid, got ${fixed.next}`);
    assert(fixed.notices === 2, `want the 2 remaining notices only, found ${fixed.notices}`);
    await context.close();
  });

  await check('英文介面的錯誤提示', async () => {
    const { page, context } = await openPage(browser, fx.three.path);
    const text = await page.evaluate(async () => {
      document.documentElement.lang = 'en';
      const pre = document.createElement('pre');
      const code = document.createElement('code');
      code.className = 'language-mermaid';
      code.textContent = 'flowchart TD\n  A[start --> B';
      pre.append(code);
      document.querySelector('article, main, body').append(pre);
      await window.JellyMermaid.render(document);
      const next = pre.nextElementSibling;
      return next && next.matches('p.jc-mermaid-error') ? next.textContent : null;
    });
    assert(text && text.startsWith('Diagram could not be displayed: '), `want an English notice, got ${JSON.stringify(text)}`);
    await context.close();
  });

  await check('惡意圖表：沒有腳本執行，輸出沒有 javascript:、on* 屬性，連結經過過濾', async () => {
    const { page, context, errors } = await openPage(browser, fx.malicious.path);
    const report = await page.evaluate(() => {
      const figures = [...document.querySelectorAll('figure.jc-mermaid')];
      const all = figures.flatMap(f => [f, ...f.querySelectorAll('*')]);
      const links = all.filter(el => el.hasAttribute('href') || el.hasAttributeNS('http://www.w3.org/1999/xlink', 'href'))
        .map(el => ({ href: el.getAttribute('href') || el.getAttributeNS('http://www.w3.org/1999/xlink', 'href'), rel: el.getAttribute('rel') || '' }));
      return {
        figures: figures.length,
        pwned: window.__jcPwned,
        html: figures.map(f => f.innerHTML).join(''),
        onAttrs: all.flatMap(el => [...el.attributes].filter(a => /^on/i.test(a.name)).map(a => `${el.tagName}[${a.name}]`)),
        scripts: all.filter(el => /^(script|foreignobject|iframe|object|embed)$/i.test(el.tagName)).map(el => el.tagName),
        links,
      };
    });
    assert(report.figures === 1, `the malicious diagram should still render as one figure, found ${report.figures}`);
    assert(report.pwned === undefined, `script ran: window.__jcPwned = ${report.pwned}`);
    assert(!/javascript:/i.test(report.html), 'the SVG output contains javascript:');
    assert(report.onAttrs.length === 0, `event handler attributes in the output: ${report.onAttrs.join(', ')}`);
    assert(report.scripts.length === 0, `forbidden elements in the output: ${report.scripts.join(', ')}`);
    for (const { href, rel } of report.links) {
      const hasScheme = /^[a-z][a-z0-9+.-]*:/i.test(href);
      const ok = /^https?:\/\//i.test(href) || /^mailto:/i.test(href) || (!hasScheme && !href.startsWith('//'));
      assert(ok, `link with a disallowed target kept: ${href}`);
      if (/^https?:\/\//i.test(href) && new URL(href).origin !== origin) {
        assert(/\bnoopener\b/.test(rel) && /\bnoreferrer\b/.test(rel), `external link ${href} lacks rel="noopener noreferrer" (rel="${rel}")`);
      }
    }
    assert(report.links.some(l => l.href.startsWith('https://example.com/')), 'the allowed https link was dropped');
    assert(report.links.some(l => l.href.startsWith('/post/')), 'the allowed same-site link was dropped');
    assert(!report.links.some(l => /^tel:/i.test(l.href) || l.href.startsWith('//')), 'tel: and network-path links must be removed');
    await page.waitForTimeout(500);
    const late = await page.evaluate(() => window.__jcPwned);
    assert(late === undefined, `script ran after render: window.__jcPwned = ${late}`);
    assert(errors.length === 0, `page errors or dialogs: ${errors.join(' | ')}`);
    await context.close();
  });

  await check('手機 390：整頁沒有橫向溢出；過寬的圖在圖表框內橫向捲動', async () => {
    for (const key of ['three', 'wide', 'errors']) {
      const { page, context } = await openPage(browser, fx[key].path, { width: 390 });
      const width = await page.evaluate(() => document.documentElement.scrollWidth);
      assert(width <= 390, `${key}: document.documentElement.scrollWidth = ${width}, want <= 390`);
      if (key === 'wide') {
        const fig = await page.evaluate(() => {
          const f = document.querySelector('figure.jc-mermaid');
          const box = f.querySelector('svg').viewBox.baseVal;
          return { scroll: f.scrollWidth, client: f.clientWidth, natural: box ? box.width : 0, overflowX: getComputedStyle(f).overflowX, maxWidth: getComputedStyle(f).maxWidth };
        });
        assert(fig.natural * 0.75 > fig.client, `fixture not wide enough: natural width ${fig.natural}, figure ${fig.client}`);
        assert(fig.scroll > fig.client, `the wide diagram must scroll inside its figure (scrollWidth ${fig.scroll}, clientWidth ${fig.client})`);
        assert(fig.overflowX === 'auto', `figure overflow-x = ${fig.overflowX}, want auto`);
      }
      await context.close();
    }
  });

  for (const scheme of ['light', 'dark']) {
    await check(`配色跟隨主題（${scheme}）：圖表文字用主題墨色與字型，和背景不同`, async () => {
      const { page, context } = await openPage(browser, fx.three.path, { colorScheme: scheme });
      const colors = await page.evaluate(() => {
        const label = document.querySelector('figure.jc-mermaid svg text.pele-label') || document.querySelector('figure.jc-mermaid svg text');
        const body = getComputedStyle(document.body);
        return { text: getComputedStyle(label).fill, ink: body.color, background: body.backgroundColor, font: getComputedStyle(label).fontFamily };
      });
      assert(colors.text === colors.ink, `label fill ${colors.text} must equal the theme ink ${colors.ink}`);
      assert(colors.text !== colors.background, `label fill ${colors.text} equals the page background`);
      assert(/Noto Sans TC/.test(colors.font), `label font ${colors.font} must use the theme font (Noto Sans TC)`);
      await context.close();
    });
  }

  await check('儲存後重新開啟：原文不遺失', async () => {
    for (const key of ['three', 'page']) {
      const post = await api(session, 'GET', `/api/admin/posts/${fx[key].id}`);
      assert(post.meta && post.meta.markdown === fx[key].markdown, `${key}: meta.markdown changed after save`);
      assert(!/<svg/i.test(post.content), `${key}: content must never store SVG`);
    }
  });

  await check('封鎖所有外部網域後仍完整渲染（沒有任何外部請求）', async () => {
    assert(blocked.length === 0, `requests to other origins: ${[...new Set(blocked)].join(', ')}`);
  });
} finally {
  await browser.close();
  for (const f of Object.values(fx)) {
    await api(session, 'DELETE', `/api/admin/posts/${f.id}`).catch(err => console.error(`cleanup: ${err.message}`));
  }
}

const failed = results.filter(([, ok]) => !ok).length;
console.log(`\n${results.length - failed}/${results.length} checks passed`);
process.exit(failed ? 1 : 0);
