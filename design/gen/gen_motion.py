import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, ADMIN_CSS, OUT, admin
import gen_layout as L

CSS = L.CSS + '''
.pre{display:flex;flex-direction:column;align-items:center;gap:6px;padding:10px 6px;border:1px solid #E6E7EB;border-radius:4px;font-size:11.5px;font-weight:600;color:#3A3D45;background:#fff;cursor:pointer}
.pre.on{border:2px solid #15161A}
.pre .sw{width:36px;height:24px;border-radius:0 8px 0 8px / 0 4px 0 4px;background:#2F6BFF;position:relative;overflow:hidden}
.tl{position:relative;height:56px;background:#F6F7F9;border:1px solid #E6E7EB;border-radius:4px}
.tl .tick{position:absolute;top:0;bottom:0;width:1px;background:#E6E7EB}
.tl .bar{position:absolute;top:10px;height:14px;border-radius:3px;font-size:10px;color:#fff;display:flex;align-items:center;padding:0 6px;white-space:nowrap}
.wrow{display:grid;grid-template-columns:48px minmax(0,1fr) 110px 90px 110px;gap:12px;align-items:center;padding:10px 0;border-top:1px solid #EEEFF2;font-size:13.5px}
.cm{font:12px/1.6 ui-monospace,"SF Mono",Menlo,Consolas,monospace;background:#15161A;color:#E6E7EB;border-radius:4px;padding:12px 14px;white-space:pre;overflow:auto}
.cm b{color:#F5B800;font-weight:500}.cm i{color:#9A9FAB;font-style:normal}.cm u{color:#13C2A3;text-decoration:none}
'''

# ---------- 版面模式 › 動效分頁 ----------
right = f'''<aside style="border-left:1px solid #E6E7EB;padding:14px 16px;display:flex;flex-direction:column;gap:14px;overflow:auto">
        <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:14px">英雄區</b><span class="chip nd" style="margin-left:auto">第 1 個區塊</span></div>
        <div class="seg" style="align-self:flex-start"><span>樣式</span><span>內容</span><span class="on">動效</span><span>進階</span></div>
        <div><span class="lbl">出現時</span>
          <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:6px;margin-top:6px">
            <div class="pre"><span class="sw" style="background:#D9DBE0"></span>無</div>
            <div class="pre on"><span class="sw"></span>淡入上浮</div>
            <div class="pre"><span class="sw" style="background:#13C2A3"></span>淡入</div>
            <div class="pre"><span class="sw" style="background:#E8285B"></span>滑入左側</div>
            <div class="pre"><span class="sw" style="background:#2F6BFF"></span>滑入右側</div>
            <div class="pre"><span class="sw" style="background:#15161A;transform:scale(.8)"></span>放大</div>
          </div></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
          <div><span class="lbl">時長</span><div class="seg" style="margin-top:6px"><span>快</span><span class="on">中</span><span>慢</span></div></div>
          <div><span class="lbl">延遲</span><div class="seg" style="margin-top:6px"><span class="on">0</span><span>0.2s</span><span>0.4s</span></div></div>
        </div>
        <div><span class="lbl">子元素依序出現</span><div style="display:flex;align-items:center;gap:10px;margin-top:6px;font-size:13px"><span class="tg"></span><span>每個隔 <b>80ms</b></span></div></div>
        <div><span class="lbl">滑鼠移上去</span><div class="seg" style="margin-top:6px"><span>無</span><span class="on">浮起</span><span>發光</span><span>傾斜</span></div></div>
        <div><span class="lbl">捲動時</span><div class="seg" style="margin-top:6px"><span class="on">無</span><span>視差</span></div></div>
        <div><span class="lbl">時間軸（這個區塊）</span>
          <div class="tl" style="margin-top:6px">
            <span class="tick" style="left:25%"></span><span class="tick" style="left:50%"></span><span class="tick" style="left:75%"></span>
            <span class="bar" style="left:2%;width:30%;background:#2F6BFF">標題 淡入上浮</span>
            <span class="bar" style="left:12%;width:30%;top:28px;background:#13C2A3">內文 延遲 0.2</span>
            <span class="bar" style="left:22%;width:30%;top:28px;margin-left:34%;background:#E8285B">按鈕 滑入左側</span>
            <span style="position:absolute;right:6px;bottom:4px;font-size:10px;color:#9A9FAB">0 · 0.3 · 0.6 · 0.9s</span>
          </div></div>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">只播放一次<span class="tg"></span></label>
        <div style="font-size:11.5px;color:#5F6470;line-height:1.6">使用者系統有開「減少動態」時，全部改成直接出現。依系統設定直接出現，持續保留內容與操作。</div>
        <div style="border-top:1px solid #EEEFF2;padding-top:12px"><div style="display:flex;align-items:center;gap:8px;margin-bottom:6px"><span class="lbl">對應的文字</span><button class="btn t s" type="button" style="margin-left:auto">{I("play",12)}播一次</button></div>
<div class="code"><b>:::hero</b>{{bg=mint <u>reveal=fade-up stagger=80 hover=lift</u>}}
## 中秋禮盒，9/1 開放預購{{<u>reveal=fade-up</u>}}
榛果可可醬 × 2…{{<u>delay=.2</u>}}

[立即預購](/shop/gift-box){{.btn <u>reveal=slide-left</u>}}
<b>:::</b></div></div>
      </aside>'''
layout_motion = L.layout
a = layout_motion.index('<aside style="border-left:1px solid #E6E7EB;padding:14px 16px')
b = layout_motion.index('</aside>', a)+len('</aside>')
layout_motion = layout_motion[:a] + right + layout_motion[b:]
layout_motion = layout_motion.replace('<div class="status"><span>版面模式 · 4 個區塊</span>','<div class="status"><span>版面模式 · 動效</span><span>動效是屬性，不是程式；主題決定每個名字長什麼樣</span>')
# 左側面板加互動元件群
layout_motion = layout_motion.replace('<div><div class="lbl" style="margin-bottom:8px">文字</div>',
 f'<div><div class="lbl" style="margin-bottom:8px">互動元件</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">{L.pal("list","分頁")}{L.pal("chevron-d","摺疊")}{L.pal("image","輪播")}{L.pal("eye","燈箱")}{L.pal("clock","倒數")}{L.pal("plus","數字跳動")}{L.pal("grid","前後比較")}{L.pal("terminal","自訂元件")}</div></div>\n        <div><div class="lbl" style="margin-bottom:8px">文字</div>')

# ---------- 後台 · 互動元件（自訂） ----------
TABS_DESC = ':::tabs 容器，每個 ::: tab {title="…"} 一頁，鍵盤可操作'
def wrow(color, ic, name, desc, by, used, state, cls):
    return f'<div class="wrow"><span style="width:48px;height:48px;background:{color};color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px">{I(ic,20)}</span><div><b>{name}</b><div style="font-size:12px;color:#5F6470">{desc}</div></div><span style="font-size:12px;color:#5F6470">{by}</span><span class="mono" style="font-size:14px">{("使用數未提供" if used == "—" else used + " 頁用到")}</span><span><span class="chip {cls}">{state}</span></span></div>'
widgets_main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 520px;gap:24px;align-items:start">
    <section style="display:flex;flex-direction:column;gap:16px">
      <div style="padding:14px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;color:#5F6470">內建元件可直接使用；自訂元件在隔離框中執行。</div>
      <div class="seg" style="align-self:flex-start"><span class="on">全部</span><span>內建</span><span>自訂</span><span>來自外掛</span></div>
      <div>
        {wrow("#15161A","list","分頁 tabs",TABS_DESC,"內建","—","可用","ok")}
        {wrow("#15161A","chevron-d","摺疊 accordion","原生 details，鍵盤可操作","內建","—","可用","ok")}
        {wrow("#15161A","image","輪播 carousel","水平捲動、上一張與下一張","內建","—","可用","ok")}
        {wrow("#15161A","eye","燈箱 lightbox","圖庫圖片點開放大，Escape 關閉","內建","—","可用","ok")}
        {wrow("#15161A","clock","倒數 countdown","指定截止時間，隔離框執行","內建","—","可用","ok")}
        {wrow("#15161A","plus","數字跳動 counter","捲到時跳到目標值，減少動態直接顯示","內建","—","可用","ok")}
        {wrow("#15161A","grid","前後比較 compare","原生圖片與可鍵盤操作的滑桿","內建","—","可用","ok")}
      </div>
      <p style="font-size:12px;color:#5F6470">內建元件使用數未提供；自訂元件的使用頁數由登錄資料顯示。</p>
    </section>
    <aside class="card" style="display:flex;flex-direction:column;gap:14px;padding:20px;background:#F6F7F9">
      <b style="font-size:16px">新增自訂元件</b>
      <label class="lbl">名稱<input class="ul" type="text" value=""></label>
      <label class="lbl">顯示名稱<input class="ul" type="text" value=""></label>
      <label class="lbl">HTML、CSS 與 JavaScript<textarea style="width:100%;min-height:280px;margin-top:8px;border:1px solid #DDE0E7;background:#fff;font:12px/1.65 ui-monospace,monospace"></textarea></label>
      <label class="lbl">參數宣告（JSON）<textarea style="width:100%;min-height:100px;margin-top:8px;border:1px solid #DDE0E7;background:#fff;font:12px/1.65 ui-monospace,monospace">[]</textarea></label>
      <small style="color:#5F6470;line-height:1.7">陣列欄位：name、type、required、default、values、maxLength。</small>
      <div style="padding:12px;background:#fff;font-size:12px">自動檢查<br>原始碼與參數在儲存時由伺服器驗證。</div>
      <small style="color:#5F6470;line-height:1.7">每次儲存建立新版本，既有已發布版本保持不變。</small>
      <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("save")}儲存新版本</button></div>
      <b style="margin-top:12px">版本</b>
    </aside>
  </main>'''

for name,title,html,h in [("EditorMotion","後台 · 編輯器版面模式 · 動效", layout_motion, 1380)]:
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, CSS, html, 1440, h)); print("wrote",name)
html = admin("themes", ["外觀","互動元件"], f'<a class="btn t" href="AppStore.dc.html">{I("search")}市場裡找元件</a><button class="btn p" type="button">{I("plus")}新增自訂元件</button>', widgets_main)
open(os.path.join(OUT, "Widgets.dc.html"),"w",encoding="utf-8").write(doc("後台 · 互動元件", CSS, html, 1440, 1100)); print("wrote Widgets")
