// Renders the Mermaid board's diagrams with the real Pele build so the board shows actual output.
// Run from the repo root: node design/tools/mermaid-board/render.cjs && node design/tools/mermaid-board/build.cjs
// Needs the vendored Pele build (web/admin/assets/pele/pele.global.js, ticket 11 slot M2) or PELE_JS=<path>.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const repo = path.join(__dirname, '../../..');
const pele = fs.readFileSync(process.env.PELE_JS || path.join(repo, 'web/admin/assets/pele/pele.global.js'), 'utf8');
const D = {
  flow: `flowchart LR
  草稿[撰寫草稿] --> 審稿{編輯審稿}
  審稿 -->|通過| 排程[排程發布]
  審稿 -->|退回修改| 草稿
  排程 --> 公開([公開文章])`,
  flowM: `flowchart TD
  草稿[撰寫草稿] --> 審稿{編輯審稿}
  審稿 -->|通過| 排程[排程發布]
  審稿 -->|退回修改| 草稿
  排程 --> 公開([公開文章])`,
  seq: `sequenceDiagram
  participant 訪客
  participant 網站
  participant 資料庫
  訪客->>網站: 開啟文章
  網站->>資料庫: 查詢已發布內容
  資料庫-->>網站: 文章與圖表原文
  網站-->>訪客: 回傳 HTML
  Note over 訪客: 瀏覽器以 Pele 畫出圖表`,
  er: `erDiagram
  作者 ||--o{ 文章 : 撰寫
  文章 }o--o{ 標籤 : 標記
  文章 ||--o{ 留言 : 收到
  作者 {
    string 名稱
    string 信箱
  }
  文章 {
    string 標題
    text 內文
  }`,
  wide: `sequenceDiagram
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
  快取-->>網站: 未命中
  網站->>資料庫: 查詢已發布文章
  網站->>附件庫: 取得封面網址
  網站->>搜尋索引: 更新熱門關鍵字
  網站->>通知中心: 通知作者有新讀者
  網站->>統計外掛: 記錄一次閱讀
  網站->>稽核紀錄: 寫入存取紀錄
  網站-->>瀏覽器: 回傳 HTML
  瀏覽器-->>訪客: 顯示文章與圖表`,
  bad: `flowchart TD
  A[開始 --> B{是否登入`,
};
(async () => {
  const b = await chromium.launch();
  const p = await b.newPage();
  await p.route('http://board.local/**', r => {
    const name = path.basename(new URL(r.request().url()).pathname);
    if (name === 'index.html') return r.fulfill({ body: '<html><body></body></html>', contentType: 'text/html' });
    r.fulfill({ body: fs.readFileSync(path.join(repo, 'web/admin/assets/fonts', name)), contentType: 'font/woff2' });
  });
  await p.goto('http://board.local/index.html').catch(() => {});
  await p.evaluate(html => { document.open(); document.write(html); document.close(); }, `<html><head><style>
  @font-face{font-family:"Noto Sans TC";src:url(http://board.local/noto-sans-tc.woff2) format("woff2");font-weight:100 900}
  body{font-family:"Noto Sans TC",sans-serif;font-size:17px}</style></head><body><p style="font-family:'Noto Sans TC'">字型載入測試 開始</p></body></html>`);
  await p.evaluate(() => document.fonts.load('16px "Noto Sans TC"', '測試'));
  await p.evaluate(() => document.fonts.ready);
  await p.addScriptTag({ content: pele });
  const jobs = [
    ['flowL', D.flow, 672], ['seqL', D.seq, 672], ['erL', D.er, 672],
    ['flowD', D.flow, 672], ['seqD', D.seq, 672], ['erD', D.er, 672],
    ['flowML', D.flowM, 326], ['seqML', D.seq, 326], ['erML', D.er, 326],
    ['flowMD', D.flowM, 326], ['seqMD', D.seq, 326], ['erMD', D.er, 326],
    ['wideM', D.wide, 326], ['wideMD', D.wide, 326], ['badL', D.flowM, 326],
  ];
  const out = await p.evaluate(({ jobs, bad }) => {
    const o = {};
    for (const [id, src, w] of jobs) {
      const r = window.pele.render(src, { maxWidth: w, idPrefix: 'mb-' + id + '-', fontFamily: '"Noto Sans TC", sans-serif', fontSize: 15, linkSchemes: ['http', 'https', 'mailto'], linkRel: 'noopener noreferrer' });
      o[id] = { svg: r.svg, width: r.width, height: r.height };
    }
    try { window.pele.render(bad); } catch (e) { o.badMsg = e.message; o.badCode = e.code; }
    return o;
  }, { jobs, bad: D.bad });
  out.sources = D;
  fs.writeFileSync(path.join(__dirname, 'svgs.json'), JSON.stringify(out));
  for (const k of Object.keys(out)) if (out[k] && out[k].width) console.log(k, out[k].width, out[k].height);
  console.log(out.badCode, JSON.stringify(out.badMsg));
  await b.close();
})();
