// Assembles design/project/Mermaid.dc.html from the Pele output in svgs.json (see render.cjs).
const fs = require('fs');
const path = require('path');
const S = JSON.parse(fs.readFileSync(path.join(__dirname, 'svgs.json'), 'utf8'));
const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
const MARK = '<svg class="mark" viewBox="0 0 85 100" aria-hidden="true"><path d="M10.8 9.2H36.3A6.5 6.5 0 0 1 42.8 15.7V41.2H17.3A6.5 6.5 0 0 1 10.8 34.7Z" fill="#2F6BFF" fill-opacity=".8"/><path d="M26.7 30.5H52.2A6.5 6.5 0 0 1 58.7 37V62.5H33.2A6.5 6.5 0 0 1 26.7 56Z" fill="#13C2A3" fill-opacity=".82"/><path d="M42 52H67.5A6.5 6.5 0 0 1 74 58.5V84H48.5A6.5 6.5 0 0 1 42 77.5Z" fill="#E8285B" fill-opacity=".9" transform="rotate(-7 58 68)"/><path d="M12 77H23.5A3 3 0 0 1 26.5 80V91.5H15A3 3 0 0 1 12 88.5Z" fill="#F5B800" fill-opacity=".95"/><path d="M62.5 16.5H71.1A2.2 2.2 0 0 1 73.3 18.7V27.3H64.7A2.2 2.2 0 0 1 62.5 25.1Z" fill="currentColor" fill-opacity=".9"/></svg>';
const ICON = {
  search: '<svg class="ic" viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/></svg>',
  menu: '<svg class="ic" viewBox="0 0 24 24"><path d="M4 7h16M4 12h16M4 17h16"/></svg>',
  swipe: '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12h16M8 8l-4 4 4 4M16 8l4 4-4 4"/></svg>',
};
const fig = (id, minWidth) => {
  const svg = S[id].svg;
  const styled = minWidth ? svg.replace('style="max-width:100%;', `style="min-width:${minWidth}px;max-width:100%;`) : svg;
  return `<figure class="jc-mermaid">${styled}</figure>`;
};
const pre = src => `<pre><code class="language-mermaid">${esc(src)}</code></pre>`;
const errorP = msg => `<p class="jc-mermaid-error" role="status">圖表無法顯示：${esc(msg)}</p>`;

const desktop = (dark) => {
  const k = dark ? 'D' : 'L';
  return `<div class="fr${dark ? ' dk' : ''}" style="width:1280px">
  <div class="topline"></div>
  <header class="mast"><span class="brand d">${MARK}榛果繽紛樂</span><nav class="nav"><a>首頁</a><a class="on">文章</a><a>關於</a></nav><span class="btn t i" aria-label="搜尋">${ICON.search}</span></header>
  <section class="head"><span class="stripe"></span><div style="display:flex;gap:8px;align-items:center"><span class="chip" style="background:#E8285B;color:#fff">開發日誌</span><span class="chip dim">2026年10月24日</span><span class="meta">小榛 · 4 分鐘</span></div><h1 class="d">用圖表說清楚發布流程</h1><p>文章裡寫 Mermaid，前台直接畫成圖。原文保存在文章裡，隨時可以改。</p></section>
  <main class="body">
    <article class="prose">
      <p>寫作時只要在程式碼區塊標上 <code>mermaid</code>，儲存後訪客看到的就是圖，不需要另外上傳圖片。</p>
      <h2>流程圖</h2>
      ${fig('flow' + k)}
      <h2>循序圖</h2>
      ${fig('seq' + k)}
      <h2>ER 圖</h2>
      ${fig('er' + k)}
      <p>圖表的顏色與字型跟著主題走；切換深色模式時不用重畫。</p>
    </article>
    <aside class="toc"><div class="lbl">目錄</div><a class="on">流程圖</a><a>循序圖</a><a>ER 圖</a></aside>
  </main>
</div>`;
};

const phone = (title, dark, inner, height) => `<div class="cap"><div class="lbl">${title}</div><div class="fr phone${dark ? ' dk' : ''}" style="width:390px;height:${height}px">
  <div class="topline"></div>
  <header class="mast m"><span class="brand d">${MARK}榛果繽紛樂</span><span style="margin-left:auto;display:flex;gap:6px"><span class="btn t i sm" aria-label="搜尋">${ICON.search}</span><span class="btn t i sm" aria-label="選單">${ICON.menu}</span></span></header>
  <section class="head m"><span class="stripe"></span><h1 class="d">用圖表說清楚發布流程</h1><span class="meta">小榛 · 4 分鐘</span></section>
  <article class="prose m">${inner}</article>
</div></div>`;

const three = k => `<p>標上 <code>mermaid</code> 的程式碼區塊會畫成圖。</p>
  <h2>流程圖</h2>${fig('flowM' + k)}
  <h2>循序圖</h2>${fig('seqM' + k)}
  <h2>ER 圖</h2>${fig('erM' + k)}`;

const wide = `<p>參與者很多的循序圖縮到原寬 75% 後仍放不下，改成在圖表框內橫向捲動；整頁不會變寬。</p>
  ${fig('wideM', Math.round(S.wideM.width * 0.75))}
`;

const broken = `<p>第一個區塊有語法錯誤：保留原文並提示，下一張圖照常顯示。</p>
  ${pre(S.sources.bad)}
  ${errorP(S.badMsg.split('\n')[0])}
  ${fig('seqML')}`;

const tooLong = `<p>原文超過 50 000 字元時不交給 Pele，直接提示。</p>
  <pre><code class="language-mermaid">flowchart TD
  n1 --&gt; n2
  n2 --&gt; n3
  …（共 51 230 字元）</code></pre>
  ${errorP('圖表原文超過 50000 字元')}
  <p>英文介面的同一行文字：</p>
  <p class="jc-mermaid-error" role="status">Diagram could not be displayed: Diagram source exceeds 50000 characters</p>`;

const spec = `<div class="spec">
  <div class="lbl">規格摘要（契約見 docs/tickets/11-mermaid.md）</div>
  <h3 class="d">輸出結構</h3>
  <pre class="code">&lt;pre hidden&gt;&lt;code class="language-mermaid"&gt;原文&lt;/code&gt;&lt;/pre&gt;
&lt;figure class="jc-mermaid"&gt;&lt;svg class="pele"&gt;…&lt;/svg&gt;&lt;/figure&gt;

失敗時：
&lt;pre&gt;…原文…&lt;/pre&gt;
&lt;p class="jc-mermaid-error" role="status"&gt;
  圖表無法顯示：錯誤訊息&lt;/p&gt;</pre>
  <h3 class="d">配色對應（亮暗共用，跟著主題變數走）</h3>
  <table class="map">
    <tr><td>--pele-bg</td><td>--surface-low（圖表框底色）</td></tr>
    <tr><td>--pele-fg</td><td>--ink</td></tr>
    <tr><td>--pele-muted</td><td>--muted</td></tr>
    <tr><td>--pele-line</td><td>墨色 72%</td></tr>
    <tr><td>--pele-surface</td><td>--surface（節點）</td></tr>
    <tr><td>--pele-surface-alt</td><td>--surface-mid（群組）</td></tr>
    <tr><td>--pele-border</td><td>墨色 30%</td></tr>
    <tr><td>--pele-accent</td><td>--secondary</td></tr>
    <tr><td>--pele-critical／-error</td><td>--error</td></tr>
    <tr><td>--pele-font</td><td>--font（Noto Sans TC）</td></tr>
    <tr><td>--pele-series-1…5</td><td>primary、secondary、accent、warning、ink</td></tr>
  </table>
  <h3 class="d">版面</h3>
  <ul><li>圖表框 <code>max-width:100%</code>、<code>overflow-x:auto</code>，只有框內捲動。</li><li>SVG 隨框寬縮放，最小為原寬 75%。</li><li>列印或存成 PDF 時縮至紙張寬度，整張圖完整保留。</li><li>圖表保留 Pele 的粗體與標題字重。</li><li>原文超過 50 000 字元、語法錯誤、不支援的圖種：保留原文並提示，同頁其他圖照常。</li><li>連結只留 http(s)、mailto 與站內路徑，外部連結加 <code>rel="noopener noreferrer"</code>。</li></ul>
</div>`;

const FONT_CSS = ['noto-sans-tc', 'bricolage-grotesque'].map((name, index) => {
  const bytes = fs.readFileSync(path.join(__dirname, '../../../web/admin/assets/fonts', name + '.woff2'));
  const family = index === 0 ? 'Noto Sans TC' : 'Bricolage Grotesque';
  return `@font-face{font-family:"${family}";src:url(data:font/woff2;base64,${bytes.toString('base64')}) format("woff2");font-weight:100 900;font-style:normal;font-display:swap}`;
}).join('\n');
const editorPreview = dark => `<div class="fr editor-example${dark ? ' dk' : ''}">
  <div class="topline"></div>
  <div class="editor-example-toolbar"><strong>用圖表說清楚發布流程</strong><div><span>原稿</span><span class="on">分割</span><span>預覽</span></div><span>已儲存</span></div>
  <div class="editor-example-panes"><pre><code>${esc('```mermaid\n' + S.sources.flow + '\n```')}</code></pre><article class="prose"><h2>流程圖</h2>${fig('flow' + (dark ? 'D' : 'L'))}<p>原稿更新後，預覽使用同一支繪圖腳本重新繪製。</p></article></div>
</div>`;

const CSS = fs.readFileSync(path.join(__dirname, 'board.css'), 'utf8');
const W = 2680, H = 5000;
const html = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<title>前台 · Mermaid 圖表</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
<style>
${FONT_CSS}
${CSS}
</style>
</helmet>
<div class="board">
  <div class="intro"><span class="stripe"></span><div class="lbl">前台 · 內容元件（工作單 11）</div><h1 class="d">Mermaid 圖表</h1><p>文章與頁面裡的 <code>mermaid</code> 程式碼區塊在瀏覽器以自帶的 Pele 畫成 SVG。下圖的圖表都是 Pele 1.9.0 的實際輸出，套上預設主題的配色對應。</p></div>
  <section><div class="row-title"><span class="d">桌面 1280</span><span class="chip">亮色</span><span class="chip dk">深色</span></div>
    <div class="row">${desktop(false)}${desktop(true)}</div></section>
  <section><div class="row-title"><span class="d">編輯預覽</span><span class="chip">與公開內容共用</span></div><div class="row">${editorPreview(false)}${editorPreview(true)}</div></section>
  <section><div class="row-title"><span class="d">手機 390</span><span class="chip">三種圖</span><span class="chip">過寬</span><span class="chip warn">錯誤狀態</span></div>
    <div class="row">
      ${phone('亮色 · 流程圖、循序圖、ER 圖', false, three("L"), 1900)}
      ${phone('深色 · 同一份內容', true, three("D"), 1900)}
      ${phone('過寬的圖：框內橫向捲動（捲軸只在圖表框）', false, wide, 1100)}
      ${phone('語法錯誤：保留原文並提示', false, broken, 1100)}
      ${phone('深色 · 原文過長', true, tooLong, 1100)}
      ${spec}
    </div></section>
</div>
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{"$preview":{"width":${W},"height":${H}}}'>
class Component extends DCLogic {
  renderVals() { return {}; }
}
</script>
</body>
</html>
`;
fs.writeFileSync(path.join(__dirname, '../../project/Mermaid.dc.html'), html);
console.log('wrote', html.length);
