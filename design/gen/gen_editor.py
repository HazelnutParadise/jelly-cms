import sys, os
sys.path.insert(0, os.path.dirname(__file__))
import gen
from gen import I, doc, ADMIN_CSS, MARK, OUT

# The editor uses the shared local content stylesheet and server render response.
# Save/conflict/offline states retain the source; export saves before downloading.
CSS = ADMIN_CSS + '''
.tool{width:30px;height:30px;display:inline-grid;place-items:center;border:0;background:transparent;border-radius:4px;color:#3A3D45;cursor:pointer}
.tool:hover{background:#EDEEF1}.tool.on{background:#E6E7EB;color:#15161A}.tool .ic{width:16px;height:16px;stroke-width:1.6}
.mono2{font-family:ui-monospace,"SF Mono",Menlo,Consolas,"Noto Sans Mono CJK TC",monospace}
.src{font-size:14px;line-height:1.8;color:#15161A;white-space:pre-wrap;margin:0}
.gut{text-align:right;color:#B0B4BF;user-select:none;white-space:pre;font-size:12px;line-height:1.8;padding-top:2px}
.gut b{color:#15161A;font-weight:500}
.prev{font-size:16px;line-height:1.85;color:#15161A}
.prev h2{font-size:24px;font-weight:700;margin:28px 0 12px;line-height:1.35}.prev h3{font-size:18px;font-weight:700;margin:22px 0 8px}
.prev p{margin:0 0 16px}.prev ul{margin:0 0 16px;padding-left:22px}
.prev blockquote{margin:0 0 16px;padding:0 0 0 14px;border-left:3px solid #D9DBE0;color:#3A3D45}
.prev pre{margin:0 0 16px;padding:12px 14px;background:#F6F7F9;border:1px solid #E6E7EB;border-radius:4px;font-family:ui-monospace,"SF Mono",Menlo,Consolas,monospace;font-size:13px}
.ol a{display:flex;align-items:center;gap:8px;min-height:28px;padding:0 8px;border-radius:4px;color:#5F6470;font-size:13px}
.ol a.h3{padding-left:22px;font-size:12.5px}.ol a.on{background:#EDEEF1;color:#15161A;font-weight:600}
.ol a .n{margin-left:auto;font-size:11px;color:#B0B4BF}
.float{position:absolute;display:inline-flex;align-items:center;gap:1px;padding:3px;background:#15161A;border-radius:6px;box-shadow:0 8px 24px rgba(21,22,26,.22)}
.float .tool{color:#fff;width:28px;height:28px}.float .tool:hover{background:#2A2B31}.float .sep{width:1px;height:16px;background:#3A3D45;margin:0 3px}
.slash{position:absolute;width:280px;background:#fff;border:1px solid #D9DBE0;border-radius:6px;box-shadow:0 12px 32px rgba(21,22,26,.14);overflow:hidden;font-size:13px}
.slash .q{padding:8px 12px;border-bottom:1px solid #EEEFF2;color:#5F6470;display:flex;gap:6px;align-items:center}
.slash a{display:flex;align-items:center;gap:10px;padding:7px 12px;color:#15161A}
.slash a.on{background:#F3F3F5}.slash a .k{margin-left:auto;font-size:11px;color:#9A9FAB}
.slash a .d2{font-size:11px;color:#5F6470;font-weight:400}
.status{display:flex;align-items:center;gap:16px;padding:5px 16px;border-top:1px solid #E6E7EB;font-size:12px;color:#5F6470;background:#FAFAFB}
.status b{font-weight:600;color:#3A3D45}
.dim{color:#B9BCC6}
.pop{position:absolute;background:#fff;border:1px solid #D9DBE0;border-radius:6px;box-shadow:0 12px 32px rgba(21,22,26,.14)}
'''

# 斜線選單：右上、左下圓角（對角對稱）；只給 EditorFull 與 EditorSlash 用，其他板的 CSS 不動
SLASH_CSS = CSS + '''
.slash{border-radius:0 12px 0 12px}
.slash.c a{padding:5px 12px}
.slash .hl{background:#F5B800;color:#15161A;border-radius:0 3px 0 3px;padding:0 1px}
'''

TOP = lambda extra_right="": f'''
    <header style="display:flex;align-items:center;gap:10px;padding:6px 14px;border-bottom:1px solid #E6E7EB;background:#fff">
      <a class="btn x" href="Editor.dc.html" style="padding:0 8px;color:#5F6470">{I("x")}離開滿版<span class="kbd">ESC</span></a>
      <span style="width:1px;height:18px;background:#E2E3E8"></span>
      <b style="font-size:14px">為什麼我們決定用 Go 重寫一個 CMS</b>
      <span class="chip warn">草稿</span>
      <span style="font-size:12px;color:#5F6470;display:inline-flex;align-items:center;gap:5px">{I("check",13)}已儲存 · 剛剛</span>
      <div style="margin-left:auto;display:flex;align-items:center;gap:6px">
        {extra_right}
        <button class="btn t" type="button">{I("history")}版本</button>
        <button class="btn t" type="button">{I("eye")}預覽<span class="kbd">⌘⇧P</span></button>
        <button class="btn p" type="button">{I("check")}發布 <span style="opacity:.7">▾</span></button>
      </div>
    </header>'''

SRC_LINES = [
 "## 為什麼要再做一個 CMS","",
 "因為現有的選擇不是太重，就是太難客製。我們想要一個**安裝五分鐘**、寫作零阻力的東西。","",
 "### 我們不要的","",
 "- [x] 編輯器把每一段都包成區塊",
 "- [ ] 一個外掛拖慢整站，還不知道是哪一個[^1]","",
 "> [!NOTE]","> 寫下來，才算數。","",
 "### 我們想要的","",
 "/",
 "",
 "首頁回應時間要壓在 $p_{95} < 100\\,\\text{ms}$，總時間是：","",
 "$$","T = \\sum_{i=1}^{n} t_i + t_{\\text{render}}","$$","",
 "```latex","\\documentclass[a4paper]{article}","\\begin{document}","\\section{壓測報告}","\\end{document}","```","",
 "```html","<details><summary>完整數據</summary>…</details>","```","",
 "```table","caption: 壓測結果","align: [left, right, right]","columns: [版本, p95, 記憶體]","rows:","  - [0.9.3, 86 ms, 48 MB]","  - [0.9.4, 71 ms, 51 MB]","```","",
 "> [!TIP]","> 輸入 / 就能插入表格與公式。","",
 ":::columns {ratio=2:1}","::: col","**寫作**：專心、不被打斷。",":::","::: col","**發布**：一鍵上線。",":::",":::","",
 "[^1]: 後來做了外掛隔離。",
]
SIGMA = '<span style="font:italic 700 15px Georgia,serif;width:15px;text-align:center">∑</span>'
HTMLIC = '<span style="font:700 12px ui-monospace,Menlo,monospace;width:15px;text-align:center">&lt;/&gt;</span>'
SLASH_ITEMS = [
 (I("image",15),"圖片","上傳、貼網址或從附件庫選"),
 (I("grid",15),"表格","用 YAML 寫欄和列，不用畫直線對齊"),
 (I("terminal",15),"程式碼區塊","指定語言會上色"),
 (SIGMA,"數學公式","$…$ 行內、$$…$$ 區塊，KaTeX"),
 (I("page",15),"LaTeX 文件","整份 .tex，伺服器排版成頁面"),
 (HTMLIC,"HTML 區塊","原始 HTML，會過濾 script"),
 (I("comment",15),"引用",""),
 (I("check",15),"待辦清單",""),
 (I("external",15),"嵌入","YouTube、IG、推文，貼網址就好"),
 (I("more",15),"「閱讀更多」分隔線","列表頁只顯示這條以上"),
 (I("box",15),"商品卡","插入商店裡的商品"),
 (I("plug",15),"自訂元件","管理員登錄的元件，例如預購倒數"),
]
def slash_items(items, on=0, label_html=None):
    out = []
    for i, (ic, label, desc) in enumerate(items):
        lab = (label_html or {}).get(label, label)
        d2 = f'<div class="d2">{desc}</div>' if desc else ""
        k = '<span class="k">↵</span>' if i == on else ""
        cls = ' class="on"' if i == on else ""
        out.append(f'<a{cls} href="EditorFull.dc.html">{ic}<span>{lab}{d2}</span>{k}</a>')
    return "\n          ".join(out)

def src_html(cur=15):
    gut = "\n".join((f"<b>{i}</b>" if i==cur else str(i)) for i in range(1,len(SRC_LINES)+1))
    body = "\n".join(SRC_LINES).replace("&","&amp;").replace("<","&lt;").replace(">","&gt;")
    return gut, body

# ===================== EditorFull: 滿版分割 + 大綱 + 斜線選單 + 浮動工具列 =====================
gut, body = src_html(16)
full = f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#fff;position:relative">
    <div class="topline"></div>
    {TOP('<div class="seg"><span>' + I("page",13) + '原稿</span><span class="on">' + I("grid",13) + '分割</span><span>' + I("eye",13) + '預覽</span></div><button class="tool on" type="button" aria-label="大綱">' + I("list") + '</button><button class="tool" type="button" aria-label="專心模式">' + I("star") + '</button><button class="tool" type="button" aria-label="搜尋取代">' + I("search") + '</button>')}
    <div style="flex:1;display:grid;grid-template-columns:220px minmax(0,1fr) minmax(0,1fr);min-height:0">
      <aside style="border-right:1px solid #E6E7EB;padding:14px 10px;display:flex;flex-direction:column;gap:14px;background:#FAFAFB">
        <div><div class="lbl" style="padding:0 8px 6px">大綱</div><nav class="ol" style="display:flex;flex-direction:column;gap:1px">
          <a href="EditorFull.dc.html">為什麼要再做一個 CMS<span class="n">H2</span></a>
          <a href="EditorFull.dc.html" class="h3">我們不要的</a>
          <a href="EditorFull.dc.html" class="h3 on">我們想要的</a>
          <a href="EditorFull.dc.html">為什麼是 Markdown<span class="n">H2</span></a>
          <a href="EditorFull.dc.html" class="dim">（接著寫…）</a>
        </nav></div>
        <div><div class="lbl" style="padding:0 8px 6px">這篇</div><div style="display:flex;flex-direction:column;gap:6px;font-size:12.5px;color:#5F6470;padding:0 8px">
          <span style="display:flex;justify-content:space-between"><span>字數</span><b class="mono" style="color:#15161A">1,284</b></span>
          <span style="display:flex;justify-content:space-between"><span>閱讀</span><b class="mono" style="color:#15161A">6 分</b></span>
          <span style="display:flex;justify-content:space-between"><span>圖片</span><b class="mono" style="color:#15161A">1</b></span>
          <span style="display:flex;justify-content:space-between"><span>連結</span><b class="mono" style="color:#15161A">0</b></span>
          <span style="display:flex;justify-content:space-between;color:#9C2318"><span>待補（TODO）</span><b class="mono">2</b></span>
        </div></div>
        <div><div class="lbl" style="padding:0 8px 6px">設定</div><div style="display:flex;flex-direction:column;gap:2px" class="ol">
          <a href="EditorFull.dc.html">{I("link",13)}/post/go-cms</a>
          <a href="EditorFull.dc.html">{I("tag",13)}開店日記 · 2 個標籤</a>
          <a href="EditorFull.dc.html">{I("image",13)}封面：自動</a>
          <a href="EditorFull.dc.html">{I("calendar",13)}立即發布</a>
        </div>
        <div role="status" style="margin:10px 8px 0;padding:8px 10px;background:#FCF0C8;color:#7A5200;border-radius:0 10px 0 10px;font-size:12px;font-weight:600;line-height:1.5;display:flex;flex-wrap:wrap;align-items:center;gap:6px 0">{I("history",14)}<span style="margin-left:6px">本機有 12:03 的備份副本 · </span><button class="btn t s" type="button" style="margin-top:2px">{I("undo",13)}還原</button></div></div>
      </aside>
      <section style="position:relative;border-right:1px solid #E6E7EB;display:grid;grid-template-columns:40px minmax(0,1fr);padding:18px 0 0;min-width:0" class="mono2">
        <div class="gut" style="padding-right:10px">{gut}</div>
        <pre class="src" style="padding:0 28px 0 18px;font-family:inherit">{body}</pre>
        <!-- 浮動工具列：選取「安裝五分鐘」時出現 -->
        <div class="float" style="left:440px;top:38px" role="toolbar" aria-label="格式">
          <button class="tool" type="button" aria-label="粗體"><svg class="ic" viewBox="0 0 24 24"><path d="M7 4h7a3.5 3.5 0 0 1 0 7H7zM7 11h8a3.5 3.5 0 0 1 0 7H7z"/></svg></button>
          <button class="tool" type="button" aria-label="斜體"><svg class="ic" viewBox="0 0 24 24"><path d="M10 4h8M6 20h8M14 4l-4 16"/></svg></button>
          <button class="tool" type="button" aria-label="行內程式碼"><svg class="ic" viewBox="0 0 24 24"><path d="M8 6l-6 6 6 6M16 6l6 6-6 6"/></svg></button>
          <span class="sep"></span>
          <button class="tool" type="button" aria-label="連結">{I("link")}</button>
          <button class="tool" type="button" aria-label="標題"><svg class="ic" viewBox="0 0 24 24"><path d="M5 4v16M19 4v16M5 12h14"/></svg></button>
          <button class="tool" type="button" aria-label="引用"><svg class="ic" viewBox="0 0 24 24"><path d="M5 15h4V9H5v6zM5 15c0 3 1 4 3 5M15 15h4V9h-4v6zM15 15c0 3 1 4 3 5"/></svg></button>
        </div>
        <span style="position:absolute;left:548px;top:70px;width:82px;height:24px;background:#DCE6FF;opacity:.7;pointer-events:none;border-radius:2px"></span>
        <!-- 斜線選單：在空行輸入 / -->
        <div class="slash" style="left:70px;top:450px" role="menu">
          <div class="q">{I("search",13)}<span>輸入 <b style="color:#15161A">/</b> 後接著打字篩選</span><span class="kbd" style="margin-left:auto">ESC</span></div>
          {slash_items(SLASH_ITEMS, on=0)}
        </div>
      </section>
      <section style="padding:18px 48px 40px;overflow:auto;position:relative">
        <div style="position:absolute;right:0;top:0;bottom:0;width:3px;background:#F3F3F5"><span style="position:absolute;top:38%;height:18%;width:3px;background:#2F6BFF" title="與原稿同步捲動"></span></div>
        <article class="prev" style="max-width:620px;margin:0 auto">
          <h2 style="margin-top:0">為什麼要再做一個 CMS</h2>
          <p>因為現有的選擇不是太重，就是太難客製。我們想要一個<strong>安裝五分鐘</strong>、寫作零阻力的東西。</p>
          <h3>我們不要的</h3>
          <ul style="list-style:none;padding-left:4px"><li style="display:flex;gap:8px;align-items:baseline"><span class="cb on" style="position:relative;top:2px"></span>編輯器把每一段都包成區塊</li><li style="display:flex;gap:8px;align-items:baseline"><span class="cb" style="position:relative;top:2px"></span>一個外掛拖慢整站，還不知道是哪一個<sup><a href="#">1</a></sup></li></ul>
          <div style="border-left:3px solid #2F6BFF;background:#F0F4FF;padding:10px 14px;margin:0 0 16px;border-radius:0 4px 4px 0"><b style="color:#1A3F9E;font-size:13px;display:flex;align-items:center;gap:6px">{I("alert",14)}NOTE</b><div>寫下來，才算數。</div></div>
          <h3 style="background:#F6F7F9;margin-left:-12px;padding:4px 12px;border-radius:4px">我們想要的</h3>
          <p class="dim" style="font-size:13px">（游標在這裡）</p>
          <p>首頁回應時間要壓在 <span style="font-family:'Latin Modern Math','STIX Two Math',Cambria,Georgia,serif;font-style:italic">p</span><sub style="font-family:Georgia,serif">95</sub> <span style="font-family:Georgia,serif">&lt; 100 ms</span>，總時間是：</p>
          <div style="text-align:center;font:italic 22px 'Latin Modern Math','STIX Two Math',Cambria,Georgia,serif;margin:0 0 16px;padding:8px 0"><i>T</i> = <span style="font-style:normal;font-size:30px;vertical-align:-6px">∑</span><sub style="font-size:12px;font-style:normal">i=1</sub><sup style="font-size:12px;font-style:normal;margin-left:-14px">n</sup> <i>t<sub>i</sub></i> + <i>t</i><sub style="font-style:normal;font-size:13px">render</sub></div>
          <figure style="margin:0 0 16px;border:1px solid #E6E7EB;border-radius:4px;overflow:hidden">
            <div style="display:flex;align-items:center;gap:8px;padding:6px 10px;background:#FAFAFB;border-bottom:1px solid #E6E7EB;font-size:12px;color:#5F6470">{I("page",13)}<b style="color:#15161A">LaTeX 文件</b><span>A4 · 1 頁 · 已排版（tectonic，快取 2 分鐘前）</span><span class="chip ok" style="margin-left:auto">成功</span><a href="#" style="font-size:12px">PDF</a></div>
            <div style="background:#EDEEF1;padding:16px;display:grid;place-items:center"><div style="width:210px;height:297px;background:#fff;box-shadow:0 2px 10px rgba(21,22,26,.12);padding:28px 22px;box-sizing:border-box;font:9px/1.5 'Latin Modern Roman',Georgia,serif;color:#15161A"><div style="font-weight:700;font-size:11px;margin-bottom:8px">1 壓測報告</div><div style="height:4px;background:#E6E7EB;margin:4px 0;width:90%"></div><div style="height:4px;background:#E6E7EB;margin:4px 0;width:80%"></div><div style="height:4px;background:#E6E7EB;margin:4px 0;width:86%"></div></div></div>
          </figure>
          <details style="margin:0 0 16px;border:1px solid #E6E7EB;border-radius:4px;padding:8px 12px"><summary style="cursor:pointer;font-weight:600">完整數據</summary><span class="dim" style="font-size:13px">HTML 區塊：原樣輸出，script、on* 屬性會被濾掉</span></details>
          <figure style="margin:0 0 16px"><table style="font-size:14px;width:100%"><thead><tr><th>版本</th><th style="text-align:right">p95</th><th style="text-align:right">記憶體</th></tr></thead><tbody><tr><td>0.9.3</td><td style="text-align:right">86 ms</td><td style="text-align:right">48 MB</td></tr><tr><td>0.9.4</td><td style="text-align:right">71 ms</td><td style="text-align:right">51 MB</td></tr></tbody></table><figcaption style="font-size:12.5px;color:#5F6470;margin-top:6px">表 1 · 壓測結果</figcaption></figure>
          <div style="border-left:3px solid #13C2A3;background:#E9F9F5;padding:10px 14px;margin:0 0 16px;border-radius:0 4px 4px 0"><b style="color:#0B5E4E;font-size:13px;display:flex;align-items:center;gap:6px">{I("star",14)}TIP</b><div>輸入 / 就能插入表格與公式。</div></div>
          <div style="display:grid;grid-template-columns:2fr 1fr;gap:16px;margin:0 0 16px"><div style="padding:10px 14px;background:#F6F7F9;border-radius:4px"><b>寫作</b>：專心、不被打斷。</div><div style="padding:10px 14px;background:#F6F7F9;border-radius:4px"><b>發布</b>：一鍵上線。</div></div>
          <div style="border-top:1px solid #E6E7EB;padding-top:8px;font-size:12.5px;color:#5F6470"><b>1.</b> 後來做了外掛隔離。</div>
        </article>
      </section>
    </div>
    <div class="status"><span>Markdown · GFM + 數學 + YAML 表格 + LaTeX / HTML 區塊</span><span>第 <b>16</b> 行，第 <b>2</b> 欄</span><span><b>1,284</b> 字 · 約 6 分鐘</span><span>已選取 5 字</span><span style="margin-left:auto">自動儲存 12:03 · 本機稿已保留 {I("check",12)}</span><span><span class="kbd">⌘/</span> 快速鍵</span></div>
  </div>'''

# ===================== EditorFocus: 專心模式（單欄、打字機、淡出介面） =====================
SHEET = f'''      <!-- 快速鍵小抄 ⌘/ -->
      <div class="pop" style="right:28px;bottom:70px;width:300px;padding:12px 14px;font-size:12.5px">
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px"><b>快速鍵</b><span class="kbd">⌘/</span><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x",13)}</button></div>
        <div style="display:grid;grid-template-columns:1fr auto;gap:5px 16px;color:#3A3D45">
          <span>粗體 / 斜體 / 連結</span><span><span class="kbd">⌘B</span> <span class="kbd">⌘I</span> <span class="kbd">⌘K</span></span>
          <span>標題升降</span><span><span class="kbd">⌘⌥↑↓</span></span>
          <span>插入東西</span><span><span class="kbd">/</span></span>
          <span>搜尋取代</span><span><span class="kbd">⌘F</span> <span class="kbd">⌘H</span></span>
          <span>切換預覽 / 專心</span><span><span class="kbd">⌘⇧P</span> <span class="kbd">⌘⇧F</span></span>
          <span>儲存 / 發布</span><span><span class="kbd">⌘S</span> <span class="kbd">⌘⏎</span></span>
          <span>上一個版本</span><span><span class="kbd">⌘⌥Z</span></span>
        </div>
      </div>
'''
COLLAPSED = '''      <!-- 快速鍵小抄收起：只剩一顆小按鈕，按它或 ⌘/ 再打開 -->
      <div style="position:absolute;right:28px;bottom:70px;display:flex;flex-direction:column;align-items:flex-end;gap:10px">
        <span style="position:relative;font-size:11.5px;background:#15161A;color:#fff;padding:5px 9px;border-radius:4px;box-shadow:0 6px 16px rgba(21,22,26,.18)">快速鍵 <span class="kbd" style="background:#2A2B31;color:#C9CBD3">⌘/</span><span style="position:absolute;right:12px;bottom:-4px;width:8px;height:8px;background:#15161A;transform:rotate(45deg)"></span></span>
        <button type="button" aria-label="打開快速鍵小抄" style="width:34px;height:34px;display:grid;place-items:center;border:1px solid #D9DBE0;border-radius:4px;background:#fff;color:#5F6470;cursor:pointer"><svg class="ic" viewBox="0 0 24 24"><path d="M3 6h18v12H3zM6 9h1M9 9h1M12 9h1M15 9h1M6 12h1M9 12h1M12 12h1M15 12h1M8 15h8"/></svg></button>
      </div>
'''

def focus_html(open_sheet):
    return f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#fff;position:relative">
    <div class="topline" style="opacity:.35"></div>
    <header style="display:flex;align-items:center;gap:10px;padding:6px 14px;opacity:.45">
      <a class="btn x" href="EditorFull.dc.html" style="padding:0 8px;color:#5F6470">{I("x")}離開專心模式<span class="kbd">ESC</span></a>
      <span style="margin-left:auto;font-size:12px;color:#5F6470;display:inline-flex;align-items:center;gap:5px">{I("check",13)}已儲存 · 剛剛</span>
      <span style="font-size:11px;color:#9A9FAB">滑鼠移開 3 秒後整條淡出</span>
    </header>
    <main style="flex:1;display:flex;justify-content:center;padding:40px 24px 120px;position:relative">
      <div style="width:100%;max-width:680px;font-size:17px;line-height:1.95;position:relative" class="mono2">
        <p class="dim" style="margin:0 0 .6em;font-size:12px;letter-spacing:.06em">### 我們想要的</p>
        <p class="dim" style="margin:0 0 1.2em">- 打開就能寫，格式用鍵盤打出來，不用找按鈕</p>
        <p class="dim" style="margin:0 0 1.2em">- 貼圖片直接上傳，貼網址自動變連結或嵌入</p>
        <p style="margin:0 0 1.2em;color:#15161A">- 寫到哪，畫面就停在哪。段落自己往上捲，眼睛不用追游標<span style="display:inline-block;width:2px;height:1.1em;background:#E8285B;vertical-align:-3px;margin-left:1px"></span></p>
        <p class="dim" style="margin:0 0 1.2em">- 斷線也不會掉字，瀏覽器關掉再開還在</p>
        <p class="dim" style="margin:0 0 1.2em">&gt; 寫下來，才算數。</p>
        <p class="dim" style="margin:0 0 1.2em;font-size:12px;letter-spacing:.06em">## 為什麼是 Markdown</p>
        <p class="dim" style="margin:0 0 1.2em">因為它跟得上打字速度。你不用離開鍵盤，也不用等區塊載入。</p>
        <!-- 貼上圖片時 -->
        <div style="display:flex;align-items:center;gap:12px;padding:10px 12px;border:1px dashed #B9BCC6;border-radius:4px;color:#5F6470;font-size:13px;font-family:'Noto Sans TC',sans-serif;margin-top:8px">{I("upload",16)}<span style="flex:1">IMG_0421.jpg · 2.1 MB → 正在上傳與壓縮<span style="display:block;height:3px;background:#EDEEF1;margin-top:6px;border-radius:2px"><span style="display:block;width:68%;height:3px;background:#2F6BFF"></span></span></span><span class="mono" style="color:#15161A">68%</span></div>
        <p class="dim" style="margin:12px 0 0;font-size:12px;font-family:'Noto Sans TC',sans-serif">完成後這裡自動變成 <span class="mono2">![IMG_0421](/uploads/2026/10/img-0421.webp)</span>，並轉成 WebP、產生縮圖。</p>
      </div>
      <!-- 右側迷你大綱 -->
      <nav aria-label="大綱" style="position:absolute;right:28px;top:60px;display:flex;flex-direction:column;gap:14px;align-items:flex-end">
        <span style="width:22px;height:2px;background:#D9DBE0"></span><span style="width:14px;height:2px;background:#D9DBE0"></span><span style="width:14px;height:2px;background:#15161A"></span><span style="width:22px;height:2px;background:#D9DBE0"></span>
      </nav>
{SHEET if open_sheet else COLLAPSED}
    </main>
    <div class="status" style="background:transparent;border:0;opacity:.6"><span><b>1,312</b> 字</span><span>今天 +428</span><span>目標 1,500 <span style="display:inline-block;width:80px;height:3px;background:#EDEEF1;vertical-align:middle;margin-left:4px"><span style="display:block;width:87%;height:3px;background:#13C2A3"></span></span></span><span style="margin-left:auto">打字機模式 · 深色 <span class="kbd">⌘⇧D</span></span></div>
  </div>'''

# ===================== 共用範例稿：斜線選單與診斷、手機版都用這一份（行號要對得上診斷） =====================
DOC = [
 "## 為什麼要再做一個 CMS","",
 "因為現有的選擇不是太重，就是太難客製。","",
 "### 我們不要的","",
 "- [x] 編輯器把每一段都包成區塊",
 "- [ ] 一個外掛拖慢整站[^1]","",
 "> [!NOTE]","> 寫下來，才算數。","",
 "### 我們想要的","",
 "- 打開就能寫，格式用鍵盤打",
 "- 貼圖片直接上傳","",
 ":::cta {bg=mint colour=blue}",
 "打開就能寫，不用找按鈕。",
 ":::","",
 "/","",
 "回應時間要壓在 $p_{95} < 100\\,\\text{ms}$：","",
 "$$","T = \\sum_{i=1}^{n} t_i","$$","",
 "> [!TIP]","> 表格用 YAML 寫，不用畫直線。","",
 "以下是最近三版的壓測結果：","",
 "```table","caption: 壓測結果","align: [left, right, right]","columns: [版本, p95, 記憶體]","rows:",
 "  - [0.9.3, 86 ms, 48 MB]","  - [0.9.4, 71 ms, 51 MB]","  - [0.9.5, 66 ms]","```","",
]
SLASH_LINE, COLOUR_LINE, TABLE_LINE = 22, 18, 42
assert DOC[SLASH_LINE-1] == "/" and "colour" in DOC[COLOUR_LINE-1] and DOC[TABLE_LINE-1].startswith("  - [0.9.5")
esc = lambda t: t.replace("&","&amp;").replace("<","&lt;").replace(">","&gt;")

def tint(i, text):
    t = esc(text)
    if i == COLOUR_LINE:
        return t.replace("colour=blue", '<span class="sq-i">colour=blue</span>')
    if i == TABLE_LINE:
        return f'<span class="sq-w">{t}</span>'
    if text.startswith(":::"): return f'<span style="color:#0B5E4E">{t}</span>'
    if text.startswith("#"): return f'<b>{t}</b>'
    if text.startswith(">") or text.startswith("```"): return f'<span style="color:#7E838F">{t}</span>'
    return t

# ===================== EditorSlash: 斜線選單三種狀態（開啟／篩選／空）＋ 診斷列 =====================
SLASH_X = '''
.lane{position:relative;display:flex;flex-direction:column;min-width:0;border-right:1px solid #E6E7EB}.lane:last-child{border-right:0}
.lh{display:flex;align-items:center;gap:8px;min-height:44px;box-sizing:border-box;padding:0 14px;border-bottom:1px solid #EEEFF2;background:#FAFAFB;font-size:12.5px;color:#5F6470}
.lh b{color:#15161A;font-size:14px}
.ed{position:relative;height:716px;padding:10px 0;overflow:hidden;font-family:ui-monospace,"SF Mono",Menlo,Consolas,"Noto Sans Mono CJK TC",monospace;font-size:12.5px;color:#15161A}
.ln{display:flex;height:22px;line-height:22px;white-space:pre}
.ln.cur{background:#F6F7F9}
.ln .gn{position:relative;flex:0 0 38px;box-sizing:border-box;text-align:right;padding-right:10px;color:#B0B4BF;font-size:11.5px}
.ln.cur .gn{color:#15161A;font-weight:700}
.ln .tx{flex:1;min-width:0;padding-left:12px;overflow:hidden}
.gn.dk:before,.gn.dy:before{content:"";position:absolute;left:4px;top:8px;width:6px;height:6px;border-radius:0 2px 0 2px}
.gn.dk:before{background:#15161A}.gn.dy:before{background:#F5B800}
.sq-i{text-decoration:underline wavy #15161A;text-underline-offset:2px}
.sq-w{text-decoration:underline wavy #D99A00;text-underline-offset:2px}
.trig{background:#DCE6FF;color:#1A3F9E;font-weight:700;border-radius:0 4px 0 4px;padding:0 3px}
.caret{display:inline-block;width:2px;height:15px;background:#E8285B;vertical-align:-3px;margin-left:1px}
.ty{color:#15161A;font-weight:700}
.dg{display:flex;align-items:center;gap:10px;min-height:42px;padding:0 14px;font-size:13px;font-weight:600}
.dg.w{background:#FCF0C8;color:#7A5200}.dg.k{background:#15161A;color:#fff}
.dg .where{font:700 11px "Bricolage Grotesque",sans-serif;padding:2px 6px;border-radius:0 5px 0 5px;background:rgba(21,22,26,.1)}
.dg.k .where{background:#2A2B31;color:#C9CBD3}
.dg .btn{margin-left:auto}
'''
def editor_lane(first=16, last=44, cur_text="/"):
    rows = []
    for i in range(first, last+1):
        if i == SLASH_LINE:
            body = f'<span class="trig">{esc(cur_text)}</span><i class="caret"></i>'
        else:
            body = tint(i, DOC[i-1])
        gcls = " dk" if i == COLOUR_LINE else (" dy" if i == TABLE_LINE else "")
        rows.append(f'<div class="ln{" cur" if i == SLASH_LINE else ""}"><span class="gn{gcls}">{i}</span><span class="tx">{body}</span></div>')
    return "\n".join(rows)

SL_TOP = 10 + (SLASH_LINE - 16 + 1) * 22 + 4   # 選單貼在輸入行下方
def lane(num, cls, name, desc, hint, cur_text, popup):
    return f'''<section class="lane" aria-label="{name}">
        <div class="lh"><span class="chip {cls} nd">{num}</span><b>{name}</b><span>{desc}</span><span style="margin-left:auto;display:inline-flex;gap:6px;align-items:center">{hint}</span></div>
        <div class="ed">
{editor_lane(cur_text=cur_text)}
{popup}
        </div>
      </section>'''

POP_STYLE = f'left:50px;top:{SL_TOP}px'
pop_open = f'''<div class="slash c" style="{POP_STYLE}" role="menu" aria-label="插入">
          <div class="q">{I("search",13)}<b class="ty">/</b><span>接著打字篩選</span><span class="kbd" style="margin-left:auto">ESC</span></div>
          {slash_items(SLASH_ITEMS, on=0)}
        </div>'''
pop_filter = f'''<div class="slash c" style="{POP_STYLE}" role="menu" aria-label="插入">
          <div class="q">{I("search",13)}<b class="ty">/表</b><span style="margin-left:auto;font-size:11px">1 項符合</span><span class="kbd">ESC</span></div>
          {slash_items([SLASH_ITEMS[1]], on=0, label_html={"表格": '<span class="hl">表</span>格'})}
        </div>'''
pop_empty = f'''<div class="slash c" style="{POP_STYLE}" role="status">
          <div class="q">{I("search",13)}<b class="ty">/xyz</b><span class="kbd" style="margin-left:auto">ESC</span></div>
          <div style="display:flex;align-items:center;gap:10px;padding:14px 12px;color:#5F6470">{I("spam",15)}<span>沒有符合的指令，按 <span class="kbd">Esc</span> 關閉</span></div>
        </div>'''
K = lambda t: f'<span class="kbd">{t}</span>'
lanes = "\n      ".join([
  lane("1","info","開啟","在空行輸入 /，列出全部指令",f'{K("↑↓")} 選擇 {K("↵")} 插入',"/",pop_open),
  lane("2","mint","篩選","繼續打字，只留下符合的",f'{K("↵")} 插入',"/表",pop_filter),
  lane("3","warn","空","打了找不到的字",f'{K("Esc")} 關閉',"/xyz",pop_empty),
])
slash_board = f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#fff;position:relative">
    <div class="topline"></div>
    {TOP('<div class="seg"><span class="on">' + I("page",13) + '原稿</span><span>' + I("grid",13) + '分割</span><span>' + I("eye",13) + '預覽</span></div><button class="tool on" type="button" aria-label="大綱">' + I("list") + '</button><button class="tool" type="button" aria-label="專心模式">' + I("star") + '</button><button class="tool" type="button" aria-label="搜尋取代">' + I("search") + '</button>')}
    <div style="flex:1;display:grid;grid-template-columns:200px minmax(0,1fr);min-height:0">
      <aside style="border-right:1px solid #E6E7EB;padding:14px 10px;display:flex;flex-direction:column;gap:14px;background:#FAFAFB">
        <div><div class="lbl" style="padding:0 8px 6px">大綱</div><nav class="ol" style="display:flex;flex-direction:column;gap:1px">
          <a href="EditorSlash.dc.html">為什麼要再做一個 CMS<span class="n">H2</span></a>
          <a href="EditorSlash.dc.html" class="h3">我們不要的</a>
          <a href="EditorSlash.dc.html" class="h3 on">我們想要的</a>
          <a href="EditorSlash.dc.html" class="dim">（接著寫…）</a>
        </nav></div>
        <div><div class="lbl" style="padding:0 8px 6px">診斷</div><div style="display:flex;flex-direction:column;gap:6px;font-size:12.5px;color:#5F6470;padding:0 8px">
          <span style="display:flex;justify-content:space-between"><span>警告</span><b class="mono" style="color:#7A5200">1</b></span>
          <span style="display:flex;justify-content:space-between"><span>提示</span><b class="mono" style="color:#15161A">1</b></span>
        </div></div>
      </aside>
      <div style="display:flex;flex-direction:column;min-width:0">
        <div style="flex:1;display:grid;grid-template-columns:repeat(3,minmax(0,1fr))">
      {lanes}
        </div>
        <!-- 診斷列：伺服器每次渲染回報，點「跳到該行」游標就過去 -->
        <div role="region" aria-label="診斷" style="border-top:1px solid #E6E7EB">
          <div style="display:flex;align-items:center;gap:8px;min-height:32px;padding:0 14px;background:#FAFAFB;font-size:12px;color:#5F6470"><b style="color:#15161A">診斷</b><span class="chip nd">2 則</span><span>不影響存檔與發布，改好後會自動消失</span><button class="btn x s i" type="button" aria-label="收合診斷" style="margin-left:auto">{I("chevron-d",14)}</button></div>
          <div class="dg w">{I("alert",16)}<span class="where">L{TABLE_LINE}</span><span>第 {TABLE_LINE} 行：YAML 表格第 3 列欄數不符</span><button class="btn s" type="button">{I("arrow-r",14)}跳到該行</button></div>
          <div class="dg k">{I("spam",16)}<span class="where">L{COLOUR_LINE}</span><span>第 {COLOUR_LINE} 行：未知屬性 colour，已忽略</span><button class="btn s" type="button" style="background:#fff;color:#15161A">{I("arrow-r",14)}跳到該行</button></div>
        </div>
      </div>
    </div>
    <div class="status"><span>Markdown · GFM + 數學 + YAML 表格 + LaTeX / HTML 區塊</span><span>第 <b>{SLASH_LINE}</b> 行，第 <b>2</b> 欄</span><span><b>1,284</b> 字 · 約 6 分鐘</span><span style="display:inline-flex;align-items:center;gap:5px;color:#7A5200">{I("alert",12)}2 則診斷</span><span style="margin-left:auto">自動儲存 12:03 · 本機稿已保留 {I("check",12)}</span><span><span class="kbd">⌘/</span> 快速鍵</span></div>
  </div>'''

# ===================== EditorMobile: 手機單欄編輯器 =====================
MB_CSS = SLASH_CSS + SLASH_X + '''
.mb .btn{min-height:40px;font-size:14px;padding:0 14px}.mb .btn.i{width:40px;padding:0}
.mb .ic{width:18px;height:18px}
.mb .ed{height:auto;flex:1;padding:6px 0;font-size:14px}
.mb .ln{height:auto;min-height:26px;line-height:26px;white-space:pre-wrap;word-break:break-all}
.mb .ln .gn{flex:0 0 32px;font-size:11px}
.mb .ln .tx{padding-left:8px;padding-right:14px}
.mb .ln .caret{height:17px;vertical-align:-3px}
.mt{display:flex;flex-direction:column;align-items:center;gap:4px;font-size:10.5px;font-weight:700;color:#3A3D45;background:transparent;border:0;font-family:inherit;padding:0;cursor:pointer}
.mt i{width:44px;height:40px;display:grid;place-items:center;background:#F3F3F5;border-radius:0 10px 0 10px;color:#15161A}
.mt.on i{background:#15161A;color:#fff}
.mt .ic{width:19px;height:19px}
'''
MT = lambda ic, label, on=False: f'<button class="mt{" on" if on else ""}" type="button" aria-label="{label}"><i>{ic}</i>{label}</button>'
SVG = lambda path: f'<svg class="ic" viewBox="0 0 24 24"><path d="{path}"/></svg>'
M_LO, M_HI, M_CUR = 13, 30, 19
def m_lines():
    out = []
    for i in range(M_LO, M_HI+1):
        cur = i == M_CUR
        body = tint(i, DOC[i-1]) if not cur else esc(DOC[i-1]) + '<i class="caret"></i>'
        gcls = " dk" if i == COLOUR_LINE else ""
        out.append(f'<div class="ln{" cur" if cur else ""}"><span class="gn{gcls}">{i}</span><span class="tx">{body}</span></div>')
    return "\n".join(out)
mobile_board = f'''<div class="mb" style="width:390px;height:844px;box-sizing:border-box;overflow:hidden;background:#fff;display:flex;flex-direction:column;position:relative">
  <div class="topline"></div>
  <header style="display:flex;align-items:center;gap:8px;padding:8px 12px 8px 8px">
    <a class="btn x" href="MobileAdmin.dc.html" style="padding:0 10px 0 6px">{I("arrow-l")}返回</a>
    <span class="chip warn">草稿</span>
    <button class="btn t" type="button" style="margin-left:auto">{I("save")}儲存</button>
    <button class="btn p" type="button">{I("check")}發布</button>
  </header>
  <div style="padding:2px 16px 10px;border-bottom:1px solid #EEEFF2">
    <h1 class="d" style="font-size:22px;line-height:1.3">為什麼我們決定用 Go 重寫一個 CMS</h1>
  </div>
  <main class="ed" aria-label="原稿" style="position:relative">
{m_lines()}
    <div style="position:absolute;left:0;right:0;bottom:0;height:36px;background:linear-gradient(180deg,rgba(255,255,255,0),#fff)"></div>
  </main>
  <!-- 工具列收進底部面板：全是圖示按鈕，往上拉可看到全部 -->
  <section aria-label="格式與插入" style="background:#fff;box-shadow:0 -10px 28px rgba(21,22,26,.12);border-radius:0 22px 0 0;padding:8px 16px 12px;position:relative">
    <span style="display:block;width:36px;height:4px;background:#D9DBE0;border-radius:2px;margin:0 auto 8px"></span>
    <div style="display:flex;align-items:center;margin-bottom:8px"><b style="font-size:13px">格式與插入</b><button class="btn x s" type="button" style="margin-left:auto;min-height:30px">{I("chevron-d",14)}收合</button></div>
    <div style="display:grid;grid-template-columns:repeat(6,1fr);gap:10px 4px">
      {MT(SVG("M7 4h7a3.5 3.5 0 0 1 0 7H7zM7 11h8a3.5 3.5 0 0 1 0 7H7z"),"粗體")}
      {MT(SVG("M10 4h8M6 20h8M14 4l-4 16"),"斜體")}
      {MT(I("link"),"連結")}
      {MT(SVG("M5 4v16M19 4v16M5 12h14"),"標題")}
      {MT(SVG("M5 15h4V9H5v6zM5 15c0 3 1 4 3 5M15 15h4V9h-4v6zM15 15c0 3 1 4 3 5"),"引用")}
      {MT(SVG("M8 6l-6 6 6 6M16 6l6 6-6 6"),"程式碼")}
      {MT(I("list"),"清單")}
      {MT(I("check"),"待辦")}
      {MT(I("image"),"圖片")}
      {MT(I("grid"),"表格")}
      {MT(SVG("M18 5H6l7 7-7 7h12"),"公式")}
      {MT(SVG("M8 20L16 4"),"更多", True)}
    </div>
  </section>
  <nav aria-label="頁面" style="display:flex;gap:8px;padding:10px 16px;background:#fff;border-top:1px solid #EEEFF2">
    <button class="btn t" type="button" aria-pressed="false" style="flex:1">{I("eye")}預覽<span class="kbd">全螢幕</span></button>
    <button class="btn t" type="button" aria-haspopup="dialog" style="flex:1">{I("settings")}設定{I("chevron-r",14)}</button>
  </nav>
  <div class="status" style="padding:7px 16px;gap:10px;background:#FAFAFB"><span><b>1,284</b> 字</span><span>·</span><span style="display:inline-flex;align-items:center;gap:5px">已自動儲存 12:03 {I("check",12)}</span><span style="margin-left:auto">第 <b>{M_CUR}</b> 行</span></div>
</div>'''

for name, title, html, css, w, h in [("EditorFull","後台 Markdown 編輯器 · 滿版", full, SLASH_CSS, 1440, 1520), ("EditorFocus","後台 Markdown 編輯器 · 專心模式", focus_html(True), CSS, 1440, 1000), ("EditorFocus2","後台 Markdown 編輯器 · 專心模式（小抄收起）", focus_html(False), CSS, 1440, 1000),
    ("EditorSlash","後台 Markdown 編輯器 · 斜線選單與診斷", slash_board, SLASH_CSS + SLASH_X, 1440, 1000), ("EditorMobile","手機 後台編輯器", mobile_board, MB_CSS, 390, 844)]:
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, css, html, w, h))
    print("wrote", name)
