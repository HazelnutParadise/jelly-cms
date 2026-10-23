// 後台截圖：node design/tools/preview-admin.mjs <jellypreview 輸出目錄>
//   先產生靜態頁：go run ./cmd/jellypreview -out <dir>
//   PLAYWRIGHT_MODULE 可指定 playwright 的 index.mjs 路徑；
//   PAGES=index,posts 只拍指定頁；OUT 可改輸出目錄（預設 design/shots）。
// 每頁在 1280×900 與 390×844、light 與 dark 各拍一張：admin-<name>-<w>-<scheme>.png，
// 另拍 index 的 Ctrl+K 面板、手機抽屜與 toast 疊加。
import path from 'node:path';
import fs from 'node:fs/promises';
import http from 'node:http';
import { fileURLToPath } from 'node:url';

const mod = process.env.PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(mod);
const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(process.argv[2] || '');
if (!process.argv[2]) { console.error('用法：node design/tools/preview-admin.mjs <預覽目錄>'); process.exit(1); }
const outDir = path.resolve(process.env.OUT || path.join(here, '..', 'shots'));
await fs.mkdir(outDir, { recursive: true });

const all = (await fs.readdir(root)).filter(f => /^admin-.*\.html$/.test(f)).map(f => f.slice(6, -5)).sort();
const names = process.env.PAGES ? process.env.PAGES.split(',') : all;

// 一個小靜態伺服器，讓 /admin/assets/* 這類絕對路徑可用。
const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.svg': 'image/svg+xml', '.css': 'text/css' };
const server = http.createServer(async (req, res) => {
  const p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
  try {
    const file = path.join(root, p);
    if (!file.startsWith(root)) throw new Error('outside');
    const body = await fs.readFile(file);
    res.writeHead(200, { 'Content-Type': types[path.extname(file)] || 'application/octet-stream' });
    res.end(body);
  } catch { res.writeHead(404); res.end('not found'); }
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const base = `http://127.0.0.1:${server.address().port}`;

const palette = {
  query: '',
  groups: [
    { key: 'posts', label: '文章', items: [
      { label: '零失敗焦糖布丁，重點在火', hint: '/post/caramel', href: '/admin/posts/1', icon: 'i-file-text', badge: 'published' },
      { label: '焦糖榛果醬', hint: '/post/hazelnut-jam', href: '/admin/posts/2', icon: 'i-file-text', badge: 'draft' } ] },
    { key: 'commands', label: '指令', items: [
      { label: '創建文章', hint: '⌘N', href: '/admin/posts/new', icon: 'i-plus' },
      { label: '查看網站', hint: '⌘⇧V', href: '/', icon: 'i-globe' } ] },
    { key: 'pages-nav', label: '前往', items: [
      { label: '儀表板', hint: '內容', href: '/admin', icon: 'i-grid' },
      { label: '設置', hint: '系統', href: '/admin/settings', icon: 'i-gear' } ] },
  ],
};

// /api/** 的假資料。預設給幾筆範例，讓列表、徽章、編輯器看得到版面；
// EMPTY=1 時一律回空陣列／空物件（看空白狀態）。
const empty = process.env.EMPTY === '1';
const fail = process.env.FAIL === '1';   // FAIL=1：/api/** 一律回 500（看錯誤狀態）
const now = Date.now();
const cat = (id, name, slug, parent_id, position, post_count, color) => ({ id, name, slug, parent_id, position, post_count, color, description: '做過才敢寫。每篇食譜都有份量、時間，和哪一步最容易失敗。', cover: '', hide_from_list: false, no_cascade: false });
const tagNames = [['榛果', 14], ['新手', 11], ['吉利丁', 9], ['焦糖', 7], ['冰', 6], ['失敗紀錄', 5], ['台中', 5], ['禮盒', 3], ['布丁', 2], ['烤箱', 2], ['奶油', 2], ['器具', 2]];
for (let i = 1; i <= 24; i++) tagNames.push(['雜記 ' + i, 1]);
for (let i = 1; i <= 6; i++) tagNames.push(['草稿標籤 ' + i, 0]);
const mi = (id, label, type, ref_id, extra = {}) => ({ id, label, type, ref_id, url: '', icon: '', new_tab: false, login_only: false, css_class: '', ...extra });
const sample = {
  categories: [
    cat(1, '食譜', 'recipes', null, 0, 18, 'secondary'), cat(2, '甜點', 'desserts', 1, 0, 11, ''), cat(3, '抹醬', 'spreads', 1, 1, 5, ''), cat(4, '飲料', 'drinks', 1, 2, 2, ''),
    cat(5, '開店日記', 'diary', null, 1, 9, 'primary'), cat(6, '產地', 'origin', null, 2, 6, 'accent'), cat(7, '公告', 'news', null, 3, 3, 'warning'),
  ],
  tags: tagNames.map(([name, post_count], i) => ({ id: i + 1, name, slug: 'tag-' + (i + 1), color: '', description: '', post_count })),
  menus: [
    { id: 1, name: '主選單', slug: 'main', location: 'header', items: [
      mi(1, '首頁', 'home', 0), mi(2, '文章', 'category', 1, { children: [mi(3, '食譜', 'category', 1), mi(4, '開店日記', 'category', 5)] }),
      mi(5, '商店', 'shop', 0), mi(6, '關於我們', 'page', 11, { children: [mi(7, '常見問題', 'page', 13)] }),
      mi(8, 'IG', 'custom', 0, { url: 'https://instagram.com/jelly', new_tab: true, icon: 'user' }) ] },
    { id: 2, name: '頁尾', slug: 'footer', location: 'footer', items: [mi(9, '隱私權政策', 'page', 14), mi(10, '退換貨說明', 'page', 15)] },
    { id: 3, name: '手機側欄', slug: 'mobile', location: '', items: [] },
  ],
  targets: {
    page: [{ id: 11, label: '關於我們', url: '/about' }, { id: 12, label: '門市資訊', url: '/stores' }, { id: 13, label: '常見問題', url: '/faq' }, { id: 14, label: '隱私權政策', url: '/privacy' }, { id: 15, label: '退換貨說明', url: '/returns' }],
    category: [{ id: 1, label: '食譜', url: '/c/recipes' }, { id: 5, label: '開店日記', url: '/c/diary' }, { id: 6, label: '產地', url: '/c/origin' }],
    post: [{ id: 1, label: '為什麼我們決定用 Go 重寫一個 CMS', url: '/post/go-cms' }],
    tag: [{ id: 1, label: '榛果', url: '/t/tag-1' }, { id: 2, label: '新手', url: '/t/tag-2' }],
  },
  posts: [
    { id: 1, title: '為什麼我們決定用 Go 重寫一個 CMS', slug: 'go-cms', status: 'published', content: '<p>內文</p>', meta: { markdown: '# 標題\n\n用 **Markdown** 寫。\n\n- 清單一\n- 清單二' }, excerpt: '現有的選擇不是太重，就是太難客製。', cover: '', visibility: 'public', pinned: false, allow_comments: true, categories: [{ id: 2, name: '甜點' }], tags: [{ id: 1, name: '榛果' }, { id: 2, name: '新手' }], created_at: new Date(now - 7200e3).toISOString() },
    { id: 2, title: '果凍丁與裝飾線：預設主題的設計筆記', slug: 'jelly-theme', status: 'published', created_at: new Date(now - 86400e3).toISOString() },
    { id: 3, title: '外掛失敗了，主程式不該跟著死', slug: 'plugin-isolation', status: 'draft', created_at: new Date(now - 3 * 86400e3).toISOString() },
    { id: 4, title: '從 WordPress 搬家：內容匯入實錄', slug: 'wp-migrate', status: 'pending', created_at: new Date(now - 7 * 86400e3).toISOString() },
  ],
  products: [
    { id: 1, title: '榛果焦糖醬', price: 320, stock: 42, status: 'active', description: '' },
    { id: 2, title: '果凍丁貼紙組', price: 120, stock: 0, status: 'draft', description: '' },
  ],
  orders: [
    { id: 1024, user: { username: '小榛' }, total_amount: 32000, status: 'pending', created_at: new Date(now - 3600e3).toISOString() },
    { id: 1023, user: null, total_amount: 128000, status: 'paid', created_at: new Date(now - 86400e3).toISOString() },
  ],
  media: ['/uploads/cover.png', '/uploads/guide.pdf'],
  themes: ['default', 'paper'],
  plugins: [{ id: 'sitemap', name: 'Sitemap', description: '產生 sitemap.xml', version: '1.2.0', author: 'Hazel' }],
  settings: [{ key: 'active_theme', value: 'default' }, { key: 'site_title', value: 'Jelly CMS' }, { key: 'site_language', value: 'zh-TW' }],
  themeConfig: { name: 'Jelly', description: '預設主題', version: '1.0.0', author: 'Hazel', colors: { primary: '#E8285B', secondary: '#2F6BFF', accent: '#13C2A3', background: '#FFFFFF', text: '#15161A' }, layout: { sidebar: true, sidebar_position: 'right' } },
};

function mockBody(p, url) {
  if (p === '/api/admin/palette') return palette;
  if (empty) return /\/(config|settings)$/.test(p) ? {} : [];
  if (p === '/api/admin/menus/targets') return sample.targets[url.searchParams.get('type')] || [];
  if (p === '/api/admin/tags') { const q = (url.searchParams.get('q') || '').toLowerCase(); return sample.tags.filter(t => t.name.toLowerCase().startsWith(q)); }
  if (/^\/api\/admin\/themes\/[^/]+\/(config|settings)$/.test(p)) return sample.themeConfig;
  if (/^\/api\/admin\/posts\/[^/]+$/.test(p)) return sample.posts[0];
  if (/^\/api\/admin\/products\/[^/]+$/.test(p)) return sample.products[0];
  const m = p.match(/^\/api\/admin\/([a-z]+)$/);
  return m && sample[m[1]] ? sample[m[1]] : [];
}

async function mock(page) {
  await page.route('**/api/**', route => {
    const url = new URL(route.request().url());
    if (fail && url.pathname !== '/api/admin/palette') { route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"boom"}' }); return; }
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(mockBody(url.pathname, url)) });
  });
  await page.route('**/auth/**', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{}' }));
  // 外部字型不可用時不要卡住截圖。
  await page.route(/fonts\.(googleapis|gstatic)\.com/, route => route.abort());
}

const browser = await chromium.launch();
const sizes = [[1280, 900], [390, 844]];
const schemes = ['light', 'dark'];
const consoleErrors = [];

for (const scheme of schemes) {
  for (const [w, h] of sizes) {
    const ctx = await browser.newContext({ viewport: { width: w, height: h }, colorScheme: scheme, deviceScaleFactor: 1 });
    for (const name of names) {
      const page = await ctx.newPage();
      page.on('pageerror', e => consoleErrors.push(`${name} ${w} ${scheme}: ${e.message}`));
      await mock(page);
      await page.goto(`${base}/admin-${name}.html`, { waitUntil: 'load' });
      await page.waitForTimeout(250);
      const file = path.join(outDir, `admin-${name}-${w}-${scheme}.png`);
      await page.screenshot({ path: file, fullPage: true });
      console.log('shot', path.relative(process.cwd(), file));
      await page.close();
    }

    // 互動畫面：設定分頁、主題客製化抽屜、Markdown 編輯器預覽
    async function shotAfter(name, tag, act) {
      if (!names.includes(name)) return;
      const pg = await ctx.newPage();
      pg.on('pageerror', e => consoleErrors.push(`${name}-${tag} ${w} ${scheme}: ${e.message}`));
      await mock(pg);
      await pg.goto(`${base}/admin-${name}.html`, { waitUntil: 'load' });
      await pg.waitForTimeout(300);
      await act(pg);
      await pg.waitForTimeout(350);
      const file = path.join(outDir, `admin-${name}-${tag}-${w}-${scheme}.png`);
      await pg.screenshot({ path: file });
      console.log('shot', path.relative(process.cwd(), file));
      await pg.close();
    }
    if (!empty && !fail) {
      await shotAfter('taxonomy', 'edit', async pg => { await pg.locator('tr[data-id="2"] [data-act="edit"]').first().click(); });
      await shotAfter('taxonomy', 'sort', async pg => { await pg.locator('#sortBtn').click(); });
      await shotAfter('taxonomy', 'tags', async pg => { await pg.locator('#tabTags').click(); });
      await shotAfter('menus', 'item', async pg => { await pg.locator('[data-uid="i6"]').click(); });
      await shotAfter('menus', 'sheet', async pg => { await pg.locator('#newMenuBtn').click(); await pg.waitForSelector('#msName'); });
      await shotAfter('menus', 'custom', async pg => { await pg.locator('#addTabs [data-type="custom"]').click(); });
      await shotAfter('post_editor', 'tags', async pg => { await pg.locator('#tagInput').fill('榛'); await pg.waitForSelector('#tagList:not([hidden])'); });
      await shotAfter('settings', 'tab-oauth', pg => pg.getByRole('button', { name: 'OAuth' }).first().click());
      await shotAfter('themes', 'drawer', async pg => { await pg.locator('.theme-actions .btn-secondary').first().click(); await pg.waitForSelector('#customizeModal'); });
      await shotAfter('post_editor', 'md', async pg => {
        await pg.fill('#postContent', '# 標題\n\n用 **Markdown** 寫，`code` 也可以。\n\n> 引用\n\n- 清單一\n- 清單二');
        const t = pg.locator('.md-preview-toggle');
        if ((await t.getAttribute('aria-pressed')) !== 'true') await t.click();
      });
    }

    if (!names.includes('index')) { await ctx.close(); continue; }
    const page = await ctx.newPage();
    page.on('pageerror', e => consoleErrors.push(`index-extras ${w} ${scheme}: ${e.message}`));
    await mock(page);
    await page.goto(`${base}/admin-index.html`, { waitUntil: 'load' });

    // Ctrl+K 開面板
    await page.keyboard.press('Control+k');
    await page.waitForSelector('#palette:not([hidden]) .pal');
    await page.waitForTimeout(150);
    let file = path.join(outDir, `admin-index-palette-${w}-${scheme}.png`);
    await page.screenshot({ path: file });
    console.log('shot', path.relative(process.cwd(), file));
    // ↓ 移動、Esc 關閉並還原焦點
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Escape');
    await page.waitForSelector('#palette', { state: 'hidden' });

    // toast 疊加
    await page.evaluate(() => {
      Jelly.toast('已發布', { kind: 'success', detail: '', action: { label: '看前台 ↗', href: '/' } });
      Jelly.toast('儲存失敗', { kind: 'error', detail: '連線中斷', action: { label: '重試', onClick() {} } });
      Jelly.toast('已移到回收桶', { kind: 'warning', action: { label: '復原', onClick() {} } });
      Jelly.toast('離線', { kind: 'info', detail: '會自動重連，草稿留在本機' });
    });
    await page.waitForTimeout(300);
    file = path.join(outDir, `admin-index-toasts-${w}-${scheme}.png`);
    await page.screenshot({ path: file });
    console.log('shot', path.relative(process.cwd(), file));

    if (w < 900) {
      await page.reload({ waitUntil: 'load' });
      await page.click('#menuToggle');
      await page.waitForTimeout(350);
      file = path.join(outDir, `admin-index-drawer-${w}-${scheme}.png`);
      await page.screenshot({ path: file });
      console.log('shot', path.relative(process.cwd(), file));
    }
    await page.close();
    await ctx.close();
  }
}
await browser.close();
server.close();
if (consoleErrors.length) { console.log('頁面錯誤：\n' + consoleErrors.join('\n')); process.exitCode = 1; }
