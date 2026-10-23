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
          <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:6px;margin-top:6px">
            <div class="pre"><span class="sw" style="background:#D9DBE0"></span>無</div>
            <div class="pre on"><span class="sw"><span style="position:absolute;left:4px;right:4px;bottom:-6px;height:12px;background:#fff;opacity:.9;border-radius:2px"></span></span>淡入上浮</div>
            <div class="pre"><span class="sw" style="background:#13C2A3"><span style="position:absolute;left:-10px;top:5px;width:14px;height:14px;background:#fff;opacity:.9;border-radius:2px"></span></span>滑入</div>
            <div class="pre"><span class="sw" style="background:#E8285B;transform:scale(.8)"></span>放大</div>
            <div class="pre"><span class="sw" style="background:#F5B800"><span style="position:absolute;inset:0;background:repeating-linear-gradient(90deg,transparent 0 6px,#fff 6px 8px);opacity:.7"></span></span>逐字</div>
            <div class="pre"><span class="sw" style="background:linear-gradient(90deg,#2F6BFF 50%,#D9DBE0 50%)"></span>擦除</div>
            <div class="pre"><span class="sw" style="background:#15161A;transform:rotate(-6deg)"></span>果凍彈</div>
            <div class="pre"><span class="sw" style="background:#fff;border:1px dashed #B9BCC6"></span>{I("plus",12)}</div>
          </div></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
          <div><span class="lbl">時長</span><div class="seg" style="margin-top:6px"><span>快</span><span class="on">中</span><span>慢</span></div></div>
          <div><span class="lbl">延遲</span><div class="seg" style="margin-top:6px"><span class="on">0</span><span>0.2s</span><span>0.4s</span></div></div>
        </div>
        <div><span class="lbl">子元素依序出現</span><div style="display:flex;align-items:center;gap:10px;margin-top:6px;font-size:13px"><span class="tg"></span><span>每個隔 <b>80ms</b></span></div></div>
        <div><span class="lbl">滑鼠移上去</span><div class="seg" style="margin-top:6px"><span>無</span><span class="on">浮起</span><span>圓角變大</span><span>變色</span></div></div>
        <div><span class="lbl">捲動時</span><div class="seg" style="margin-top:6px"><span class="on">無</span><span>視差</span><span>釘住</span><span>進度綁定</span></div></div>
        <div><span class="lbl">時間軸（這個區塊）</span>
          <div class="tl" style="margin-top:6px">
            <span class="tick" style="left:25%"></span><span class="tick" style="left:50%"></span><span class="tick" style="left:75%"></span>
            <span class="bar" style="left:2%;width:30%;background:#2F6BFF">標題 淡入上浮</span>
            <span class="bar" style="left:12%;width:30%;top:28px;background:#13C2A3">內文 延遲 0.2</span>
            <span class="bar" style="left:22%;width:30%;top:28px;margin-left:34%;background:#E8285B">按鈕 果凍彈</span>
            <span style="position:absolute;right:6px;bottom:4px;font-size:10px;color:#9A9FAB">0 · 0.3 · 0.6 · 0.9s</span>
          </div></div>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">尊重「減少動態效果」設定<span class="tg"></span></label>
        <div style="font-size:11.5px;color:#5F6470;line-height:1.6">使用者系統有開「減少動態」時，全部改成直接出現。這個開關預設開，關掉要想清楚。</div>
        <div style="border-top:1px solid #EEEFF2;padding-top:12px"><div style="display:flex;align-items:center;gap:8px;margin-bottom:6px"><span class="lbl">對應的文字</span><button class="btn t s" type="button" style="margin-left:auto">{I("play",12)}播一次</button></div>
<div class="code"><b>:::hero</b>{{bg=mint <u>reveal=fade-up stagger=80 hover=lift</u>}}
## 中秋禮盒，9/1 開放預購{{<u>reveal=fade-up</u>}}
榛果可可醬 × 2…{{<u>delay=.2</u>}}

[立即預購](/shop/gift-box){{.btn <u>reveal=jelly</u>}}
<b>:::</b></div></div>
      </aside>'''
layout_motion = L.layout
a = layout_motion.index('<aside style="border-left:1px solid #E6E7EB;padding:14px 16px')
b = layout_motion.index('</aside>', a)+len('</aside>')
layout_motion = layout_motion[:a] + right + layout_motion[b:]
layout_motion = layout_motion.replace('<div class="status"><span>版面模式 · 4 個區塊</span>','<div class="status"><span>版面模式 · 動效</span><span>動效是屬性，不是程式；主題決定每個名字長什麼樣</span>')
# 左側面板加互動元件群
layout_motion = layout_motion.replace('<div><div class="lbl" style="margin-bottom:8px">文字</div>',
 f'<div><div class="lbl" style="margin-bottom:8px">互動元件</div><div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">{L.pal("list","分頁")}{L.pal("chevron-d","摺疊")}{L.pal("image","輪播")}{L.pal("eye","燈箱")}{L.pal("clock","倒數")}{L.pal("plus","數字跳動")}{L.pal("grid","前後比較")}{L.pal("play","Lottie 動畫")}{L.pal("comment","投票")}{L.pal("terminal","自訂元件")}</div></div>\n        <div><div class="lbl" style="margin-bottom:8px">文字</div>')

# ---------- 後台 · 互動元件（自訂） ----------
TABS_DESC = ':::tabs 容器，每個 ::: tab {title="…"} 一頁，鍵盤可操作'
def wrow(color, ic, name, desc, by, used, state, cls):
    return f'<div class="wrow"><span style="width:48px;height:48px;background:{color};color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px">{I(ic,20)}</span><div><b>{name}</b><div style="font-size:12px;color:#5F6470">{desc}</div></div><span style="font-size:12px;color:#5F6470">{by}</span><span class="mono" style="font-size:14px">{used} 頁用到</span><span><span class="chip {cls}">{state}</span></span></div>'
widgets_main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 520px;gap:24px;align-items:start">
    <section style="display:flex;flex-direction:column;gap:16px">
      <div style="display:flex;align-items:center;gap:14px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;line-height:1.5">{I("shield",18)}<span><b>三層，權限不一樣。</b>動效屬性和內建互動元件誰都能用、沒有程式。自訂元件可以寫 HTML／CSS／JS，但跑在隔離框裡，拿不到登入狀態和網站其他部分，只有管理員能建立。</span></div>
      <div style="display:flex;align-items:center;gap:10px"><div class="seg"><span class="on">全部 <b>9</b></span><span>內建 7</span><span>自訂 2</span><span>來自外掛 0</span></div><button class="btn p" type="button" style="margin-left:auto">{I("plus")}新增自訂元件</button></div>
      <div>
        <div class="wrow" style="border-top:0;padding-top:0;font-size:11px;font-weight:700;color:#5F6470;letter-spacing:.06em;text-transform:uppercase"><span></span><span>元件</span><span>來源</span><span>使用</span><span>狀態</span></div>
        {wrow("#15161A","list","分頁 tabs",TABS_DESC,"內建","12","可用","ok")}
        {wrow("#15161A","chevron-d","摺疊 accordion","FAQ 常用，可預設展開第一項","內建","8","可用","ok")}
        {wrow("#15161A","image","輪播 carousel","拖曳、自動播放可關、縮圖列","內建","3","可用","ok")}
        {wrow("#15161A","eye","燈箱 lightbox","圖片點開放大，圖庫自動套用","內建","41","可用","ok")}
        {wrow("#15161A","plus","數字跳動 counter","捲到時從 0 跳到目標值","內建","2","可用","ok")}
        {wrow("#15161A","grid","前後比較 compare","兩張圖拉桿比較","內建","1","可用","ok")}
        {wrow("#15161A","play","Lottie / Rive 動畫","上傳 .json 或 .riv，可設觸發方式","內建","4","可用","ok")}
        {wrow("#E8285B","clock","預購倒數","到 to 指定時間為止，結束後顯示 done 文字","Hazel · 自訂","2","已發布","ok")}
        {wrow("#2F6BFF","star","果凍物理","滑鼠靠近會晃的果凍丁，純 CSS + 20 行 JS","Ting · 自訂","0","草稿","warn")}
      </div>
      <div style="font-size:12.5px;color:#5F6470;line-height:1.7;display:flex;gap:10px;align-items:flex-start">{I("alert",16)}<span>內建元件的 JS 只在頁面真的用到時才載入，每個都在 3 KB 以內。自訂元件的 JS 不會和網站主程式同一個環境執行，所以也不能改網站其他地方；要做到那種程度請寫外掛。</span></div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:12px;padding:0;overflow:hidden">
      <div style="display:flex;align-items:center;gap:10px;padding:14px 16px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">自訂元件：預購倒數</b><span class="chip ok" style="margin-left:auto">已發布 v3</span></div>
      <div class="tab" style="padding:0 16px"><span class="on">程式</span><span>參數</span><span>預覽</span><span>版本</span></div>
      <div style="padding:0 16px;display:flex;flex-direction:column;gap:10px">
        <div><span class="lbl">在文章裡這樣用</span><div class="code" style="margin-top:6px"><b>::widget</b>{{name=countdown to="2026-09-25T00:00+08:00" done="開賣了！"}}</div></div>
        <div><span class="lbl">HTML + CSS + JS（同一個檔）</span>
<div class="cm" style="margin-top:6px;max-height:260px">&lt;div class="cd"&gt;&lt;b data-d&gt;&lt;/b&gt; 天 &lt;b data-h&gt;&lt;/b&gt; 時&lt;/div&gt;
&lt;style&gt;.cd{{font:800 2rem/1 var(--font-display);color:var(--flavor)}}&lt;/style&gt;
&lt;script&gt;
  <i>// props 來自 ::widget 的屬性，已驗證過型別</i>
  const to = new Date(<b>props.to</b>);
  <u>tick</u>(() =&gt; {{
    const ms = to - Date.now();
    if (ms &lt;= 0) return <u>replace</u>(<b>props.done</b>);
    el('[data-d]').textContent = Math.floor(ms/864e5);
    el('[data-h]').textContent = Math.floor(ms/36e5)%24;
  }}, 1000);
&lt;/script&gt;</div></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px;font-size:12px;color:#5F6470;line-height:1.6">
          <div><b style="color:#15161A">拿得到</b><br>props、主題 CSS 變數、自己的 DOM、tick／replace 這類小工具、fetch 到白名單網域</div>
          <div><b style="color:#15161A">拿不到</b><br>cookie、登入狀態、頁面其他 DOM、localStorage、父頁面的 JS</div>
        </div>
        <div><span class="lbl">參數宣告</span><div class="code" style="margin-top:6px">to:   datetime  必填
done: string    預設 "開始了"
size: enum(sm,md,lg) 預設 md</div></div>
        <div style="display:flex;align-items:center;gap:8px;padding:8px 10px;background:#F5F5F7;border-radius:0 9px 0 9px;font-size:12px">{I("check",14)}<span>自動檢查通過：無外部 script、無 eval、大小 1.8 KB、減少動態下可用</span></div>
      </div>
      <div style="padding:12px 16px;border-top:1px solid #EEEFF2;display:flex;gap:8px"><button class="btn p" type="button">{I("save")}儲存並發布 v4</button><button class="btn t" type="button">{I("eye")}預覽</button><button class="btn x" type="button" style="margin-left:auto">{I("history")}版本</button></div>
    </aside>
  </main>'''

for name,title,html,h in [("EditorMotion","後台 · 編輯器版面模式 · 動效", layout_motion, 1380)]:
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, CSS, html, 1440, h)); print("wrote",name)
html = admin("themes", ["外觀","互動元件"], f'<a class="btn t" href="AppStore.dc.html">{I("search")}市場裡找元件</a><button class="btn p" type="button">{I("plus")}新增自訂元件</button>', widgets_main)
open(os.path.join(OUT, "Widgets.dc.html"),"w",encoding="utf-8").write(doc("後台 · 互動元件", CSS, html, 1440, 1100)); print("wrote Widgets")
