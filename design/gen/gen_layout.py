import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, ADMIN_CSS, OUT

CSS = ADMIN_CSS + '''
.tool{width:30px;height:30px;display:inline-grid;place-items:center;border:0;background:transparent;border-radius:4px;color:#3A3D45;cursor:pointer}
.tool:hover{background:#EDEEF1}.tool.on{background:#E6E7EB;color:#15161A}.tool .ic{width:16px;height:16px;stroke-width:1.6}
.mono2{font-family:ui-monospace,"SF Mono",Menlo,Consolas,"Noto Sans Mono CJK TC",monospace}
.pal{display:flex;flex-direction:column;align-items:center;gap:6px;padding:10px 6px;border:1px solid #E6E7EB;border-radius:4px;font-size:12px;font-weight:600;color:#3A3D45;background:#fff;cursor:grab}
.pal:hover{border-color:#15161A}.pal .ic{width:20px;height:20px;stroke-width:1.5}
.blk{position:relative;border:1px dashed transparent;border-radius:2px}
.blk:hover{border-color:#B9BCC6}
.blk.sel{border:2px solid #2F6BFF}
.blk .tag{position:absolute;left:-2px;top:-22px;background:#2F6BFF;color:#fff;font-size:11px;font-weight:700;padding:2px 8px;border-radius:3px 3px 0 0;display:flex;gap:6px;align-items:center}
.blk .hd{position:absolute;right:-2px;top:-30px;display:flex;gap:1px;background:#15161A;border-radius:4px;padding:2px}
.blk .hd .tool{color:#fff;width:26px;height:26px}.blk .hd .tool:hover{background:#2A2B31}
.blk .lab{position:absolute;left:0;top:-18px;font-size:11px;color:#9A9FAB;font-weight:600;opacity:0}.blk:hover .lab{opacity:1}
.drop{height:0;border-top:2px solid #E8285B;position:relative;margin:14px 0}
.drop:after{content:"放到這裡";position:absolute;left:50%;top:-11px;transform:translateX(-50%);background:#E8285B;color:#fff;font-size:11px;font-weight:700;padding:2px 8px;border-radius:3px}
.code{font:12px/1.65 ui-monospace,"SF Mono",Menlo,Consolas,monospace;background:#F6F7F9;border:1px solid #E6E7EB;border-radius:4px;padding:10px 12px;white-space:pre;color:#15161A;overflow:auto}
.code b{color:#2F6BFF;font-weight:600}.code i{color:#9A9FAB;font-style:normal}
.prose{font-size:15px;line-height:1.8}.prose h2{font-size:22px;margin:0 0 8px;font-weight:700}.prose p{margin:0 0 10px}
.ex{display:flex;gap:14px;align-items:flex-start;padding:14px;border-radius:0 12px 0 12px;box-shadow:inset 0 0 0 1px #E2E3E8;cursor:pointer}
.ex.on{box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA}
.ex .ic{width:22px;height:22px;flex:0 0 auto;margin-top:2px}
.ex .cb{flex:0 0 auto}
.ex b{font-size:14px}.ex p{margin:4px 0 0;font-size:12.5px;color:#5F6470;line-height:1.55}
.status{display:flex;align-items:center;gap:16px;padding:5px 16px;border-top:1px solid #E6E7EB;font-size:12px;color:#5F6470;background:#FAFAFB}
.status b{font-weight:600;color:#3A3D45}
'''

def top(seg_on, right_extra=""):
    segs = [("page","文字"),("grid","版面"),("list","分割")]
    seg = "".join(f'<span{" class=\"on\"" if l==seg_on else ""}>{I(ic,13)}{l}</span>' for ic,l in segs)
    return f'''<header style="display:flex;align-items:center;gap:10px;padding:6px 14px;border-bottom:1px solid #E6E7EB;background:#fff">
      <a class="btn x" href="Editor.dc.html" style="padding:0 8px;color:#5F6470">{I("x")}離開滿版<span class="kbd">ESC</span></a>
      <span style="width:1px;height:18px;background:#E2E3E8"></span>
      <b style="font-size:14px">中秋禮盒預購</b><span class="chip nd">頁面</span><span class="chip warn">草稿</span>
      <span style="font-size:12px;color:#5F6470;display:inline-flex;align-items:center;gap:5px">{I("check",13)}已儲存 · 剛剛</span>
      <div style="margin-left:auto;display:flex;align-items:center;gap:6px">
        <div class="seg">{seg}</div>
        <div class="seg"><span class="on">{I("grid",13)}桌面</span><span>{I("page",13)}手機</span></div>
        {right_extra}
        <button class="btn t" type="button">{I("eye")}預覽</button>
        <button class="btn p" type="button">{I("check")}發布 <span style="opacity:.7">▾</span></button>
      </div>
    </header>'''

def pal(ic, label): return f'<div class="pal" draggable="true">{I(ic)}{label}</div>'

layout = f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#fff;position:relative">
    <div class="topline"></div>
    {top("版面", f'<button class="btn t" type="button">{I("download")}匯出</button>')}
    <div style="flex:1;display:grid;grid-template-columns:232px minmax(0,1fr) 320px;min-height:0">
      <aside style="border-right:1px solid #E6E7EB;padding:14px 12px;display:flex;flex-direction:column;gap:16px;background:#FAFAFB;overflow:auto">
        <label style="display:flex;align-items:center;gap:8px;background:#fff;border:1px solid #E6E7EB;border-radius:4px;padding:0 8px;min-height:30px;color:#5F6470;font-size:12.5px">{I("search",13)}<input type="text" placeholder="找區塊" aria-label="找區塊" style="border:0;font:inherit;flex:1;min-width:0;outline:0"></label>
        <div><div class="lbl" style="margin-bottom:8px">版面</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">{pal("page","區段")}{pal("grid","兩欄")}{pal("list","三欄")}{pal("menu","側欄 + 內容")}</div></div>
        <div><div class="lbl" style="margin-bottom:8px">元件</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">{pal("star","英雄區")}{pal("image","圖庫")}{pal("box","商品格")}{pal("doc","文章列表")}{pal("arrow-r","行動呼籲")}{pal("comment","FAQ 摺疊")}{pal("external","嵌入")}</div></div>
        <div><div class="lbl" style="margin-bottom:8px">文字</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">{pal("doc","Markdown 段落")}{pal("terminal","HTML 區塊")}</div></div>
        <div style="margin-top:auto;font-size:11.5px;color:#9A9FAB;line-height:1.6">拖進來的每個區塊都是一段文字（<span class="mono2">:::</span> 區塊），切回「文字」隨時能手改。</div>
      </aside>

      <section style="background:#EDEEF1;padding:40px 32px;overflow:auto">
        <div style="max-width:760px;margin:0 auto;background:#fff;box-shadow:0 1px 3px rgba(21,22,26,.08);padding:36px 40px 48px;display:flex;flex-direction:column;gap:28px">
          <!-- 英雄區（選取中） -->
          <div class="blk sel" style="padding:0;margin-top:14px">
            <span class="tag">{I("star",12)}英雄區 · 薄荷</span>
            <span class="hd"><button class="tool" type="button" aria-label="拖曳">{I("drag")}</button><button class="tool" type="button" aria-label="上移">{I("arrow-l",14,' style="transform:rotate(90deg)"')}</button><button class="tool" type="button" aria-label="下移">{I("arrow-r",14,' style="transform:rotate(90deg)"')}</button><button class="tool" type="button" aria-label="複製">{I("copy")}</button><button class="tool" type="button" aria-label="刪除">{I("trash")}</button></span>
            <div style="background:#13C2A3;color:#0B3A31;padding:40px 36px;border-radius:0 28px 0 28px / 0 14px 0 14px;display:flex;flex-direction:column;gap:10px">
              <span class="stripe" style="background:linear-gradient(90deg,#0B3A31 0 52%,transparent 52% 60%,#0B3A31 60% 82%,transparent 82% 88%,#0B3A31 88% 100%)"></span>
              <div class="d" style="font-size:40px;letter-spacing:-.035em;outline:1px dashed rgba(11,58,49,.4);outline-offset:4px">中秋禮盒，9/1 開放預購<span style="display:inline-block;width:2px;height:.9em;background:#E8285B;vertical-align:-4px;margin-left:2px"></span></div>
              <p style="margin:0;font-size:15px;max-width:30em">榛果可可醬 × 2、季節果凍 × 6，限量 300 盒。</p>
              <div style="display:flex;gap:8px"><span class="btn" style="pointer-events:none">{I("cart")}立即預購</span><span class="btn t" style="pointer-events:none">{I("eye")}看內容物</span></div>
            </div>
          </div>
          <div class="drop"></div>
          <!-- 兩欄 -->
          <div class="blk" style="padding:4px"><span class="lab">兩欄 · 2:1</span>
            <div style="display:grid;grid-template-columns:2fr 1fr;gap:24px">
              <div class="prose"><h2>為什麼今年只做 300 盒</h2><p>榛果今年收成少三成，我們寧願少做也不換產地。每盒附一張手寫的保存說明。</p><p>預購 9/1 開放，9/20 截止，9/25 起陸續出貨。</p></div>
              <div style="aspect-ratio:4/5;background:#E8285B;border-radius:0 20px 0 20px / 0 10px 0 10px;display:grid;place-items:center;color:#fff;font-weight:800">禮盒照</div>
            </div>
          </div>
          <!-- 商品格 -->
          <div class="blk" style="padding:4px"><span class="lab">商品格 · 分類「禮盒」· 3 件</span>
            <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:12px">
              <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px / 0 7px 0 7px;overflow:hidden"><div style="aspect-ratio:1;background:#15161A"></div><div style="padding:10px;font-size:13px"><b>限量禮盒</b><div class="mono" style="font-size:15px">NT$ 1,280</div></div></div>
              <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px / 0 7px 0 7px;overflow:hidden"><div style="aspect-ratio:1;background:#F5B800"></div><div style="padding:10px;font-size:13px"><b>小禮盒</b><div class="mono" style="font-size:15px">NT$ 680</div></div></div>
              <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px / 0 7px 0 7px;overflow:hidden"><div style="aspect-ratio:1;background:#2F6BFF"></div><div style="padding:10px;font-size:13px"><b>果凍 12 入</b><div class="mono" style="font-size:15px">NT$ 480</div></div></div>
            </div>
          </div>
          <!-- 一般 Markdown -->
          <div class="blk" style="padding:4px"><span class="lab">Markdown 段落</span><div class="prose"><h2>常見問題</h2><p>可以指定到貨日嗎？可以，結帳時備註。冷藏還是常溫？果凍冷藏，抹醬常溫，一起出貨會用保冷袋。</p></div></div>
          <div style="display:flex;justify-content:center"><button class="btn t s" type="button">{I("plus",14)}加區塊</button></div>
        </div>
      </section>

      <aside style="border-left:1px solid #E6E7EB;padding:14px 16px;display:flex;flex-direction:column;gap:14px;overflow:auto">
        <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:14px">英雄區</b><span class="chip nd" style="margin-left:auto">第 1 個區塊</span></div>
        <div class="seg" style="align-self:flex-start"><span class="on">樣式</span><span>內容</span><span>進階</span></div>
        <div><span class="lbl">背景</span><div style="display:flex;gap:6px;margin-top:6px"><span style="width:26px;height:26px;background:#fff;box-shadow:inset 0 0 0 1px #D9DBE0;border-radius:0 9px 0 9px / 0 5px 0 5px"></span><span style="width:26px;height:26px;background:#15161A;border-radius:0 9px 0 9px / 0 5px 0 5px"></span><span style="width:26px;height:26px;background:#E8285B;border-radius:0 9px 0 9px / 0 5px 0 5px"></span><span style="width:26px;height:26px;background:#2F6BFF;border-radius:0 9px 0 9px / 0 5px 0 5px"></span><span style="width:26px;height:26px;background:#13C2A3;border-radius:0 9px 0 9px / 0 5px 0 5px;box-shadow:0 0 0 2px #fff,0 0 0 4px #15161A"></span><span style="width:26px;height:26px;background:#F5B800;border-radius:0 9px 0 9px / 0 5px 0 5px"></span><span style="width:26px;height:26px;display:grid;place-items:center;box-shadow:inset 0 0 0 1px #D9DBE0;border-radius:0 9px 0 9px / 0 5px 0 5px">{I("image",14)}</span></div><div style="font-size:11.5px;color:#5F6470;margin-top:6px">只給主題的口味色，不給色票。換主題時顏色跟著換。</div></div>
        <div><span class="lbl">對齊</span><div class="seg" style="margin-top:6px"><span class="on">靠左</span><span>置中</span></div></div>
        <div><span class="lbl">高度</span><div class="seg" style="margin-top:6px"><span>緊湊</span><span class="on">一般</span><span>滿版</span></div></div>
        <div><span class="lbl">內距</span><div class="seg" style="margin-top:6px"><span>小</span><span class="on">中</span><span>大</span></div></div>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">手機時隱藏按鈕列<span class="tg off"></span></label>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">加果凍丁裝飾<span class="tg"></span></label>
        <div><span class="lbl">錨點</span><input class="ul" type="text" value="preorder" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"></div>
        <div style="border-top:1px solid #EEEFF2;padding-top:12px"><div style="display:flex;align-items:center;gap:8px;margin-bottom:6px"><span class="lbl">這個區塊的文字</span><a href="EditorFull.dc.html" style="font-size:11.5px;margin-left:auto">在文字模式打開</a></div>
<div class="code"><b>:::hero</b>{{bg=mint align=left height=normal cubes}}
## 中秋禮盒，9/1 開放預購
榛果可可醬 × 2、季節果凍 × 6，限量 300 盒。

[立即預購](/shop/gift-box){{.btn}} [看內容物](#contents){{.btn.t}}
<b>:::</b>

<i>// 下一個區塊</i>
<b>:::columns</b>{{ratio=2:1}}
<b>::: col</b>
## 為什麼今年只做 300 盒
…</div></div>
      </aside>
    </div>
    <div class="status"><span>版面模式 · 4 個區塊</span><span>同一份文字，拖拉只是改 <b>:::</b> 區塊的順序和屬性</span><span style="margin-left:auto">自動儲存 剛剛 · 本機備份 {I("check",12)}</span><span><span class="kbd">⌘Z</span> 復原</span></div>
  </div>'''

export = f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#fff;position:relative">
    <div class="topline"></div>
    {top("分割")}
    <div style="flex:1;background:#F6F7F9;opacity:.5"></div>
    <div style="position:absolute;inset:0;background:rgba(21,22,26,.35)"></div>
    <div style="position:absolute;top:0;right:0;bottom:0;width:520px;background:#fff;box-shadow:-24px 0 48px rgba(21,22,26,.12);border-radius:0 0 0 24px;display:flex;flex-direction:column">
      <div style="display:flex;align-items:center;gap:10px;padding:16px 22px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">匯出「中秋禮盒預購」</b><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x")}</button></div>
      <div style="padding:18px 22px;display:flex;flex-direction:column;gap:10px;flex:1;overflow:auto">
        <div class="lbl" style="margin-bottom:2px">格式</div>
        <label class="ex on"><span class="cb on" style="margin-top:3px"></span>{I("doc")}<span><b>Jelly Markdown（.md）</b><p>原稿原樣：一般 Markdown 加上 <span class="kbd">:::</span> 版面區塊、<span class="kbd">```table</span>、數學、LaTeX 與 HTML 區塊。任何 Markdown 編輯器都打得開，版面區塊會顯示成文字。要搬回 Jelly 或進版本控制就選這個。</p></span></label>
        <label class="ex"><span class="cb" style="margin-top:3px"></span>{I("page")}<span><b>純 Markdown（.md）</b><p>把 Jelly 專用的東西攤平：欄變成連續段落、商品格變成連結清單、YAML 表格轉成管線表格、提示框變成引言。數學保留 <span class="kbd">$…$</span>。要貼到 GitHub、Notion、別家 CMS 選這個。</p></span></label>
        <label class="ex"><span class="cb" style="margin-top:3px"></span>{I("globe")}<span><b>HTML（.html）</b><p>獨立文件附上內容樣式；數學與 LaTeX 原稿在沒有排版引擎時保留為原始碼。可勾選把圖片一起打包成 .zip。</p></span></label>
        <label class="ex"><span class="cb" style="margin-top:3px"></span>{I("download")}<span><b>PDF</b><p>以瀏覽器列印前台樣式，再選「另存為 PDF」。伺服器沒有 PDF 引擎時會提示改用列印。</p></span></label>
        <div style="display:flex;flex-direction:column;gap:8px;padding:12px 14px;background:#F5F5F7;border-radius:0 10px 0 10px;font-size:13px;margin-top:4px">
          <label style="display:flex;align-items:center;gap:8px"><span class="cb on"></span>包含圖片（複製到同一個資料夾，連結改成相對路徑）</label>
          <label style="display:flex;align-items:center;gap:8px"><span class="cb on"></span>開頭附上標題、網址、日期、標籤的 YAML 標頭</label>
        </div>
        <div style="font-size:12.5px;color:#5F6470;line-height:1.6;margin-top:4px;display:flex;gap:8px">{I("alert",15)}<span>整個網站要匯出，去「備份與匯出」：Markdown 資料夾、WXR（給 WordPress，會用「純 Markdown」的攤平規則轉成 HTML）或 JSON。</span></div>
      </div>
      <div style="padding:14px 22px;border-top:1px solid #EEEFF2;display:flex;gap:8px;align-items:center"><button class="btn p" type="button">{I("download")}下載 .md</button><button class="btn t" type="button">{I("copy")}複製到剪貼簿</button><span style="margin-left:auto;font-size:12px;color:#5F6470">約 14 KB · 3 張圖</span></div>
    </div>
  </div>'''

for name,title,html,h in [("EditorLayout","後台 · 編輯器版面模式", layout, 1380),("EditorExport","後台 · 匯出文章", export, 1000)]:
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, CSS, html, 1440, h)); print("wrote",name)
