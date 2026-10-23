import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, FRONT_CSS, OUT, front, cubes

CSS = FRONT_CSS + '''
.pc{position:relative;background:#fff;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;overflow:hidden;display:flex;flex-direction:column;transition:box-shadow .15s,border-radius .25s}
.pc:hover{box-shadow:inset 0 0 0 2px #15161A;border-radius:0 28px 0 28px}
.pc .img{aspect-ratio:1;display:grid;place-items:center;color:#fff;font-weight:800;font-size:20px}
.pc .b{padding:16px;display:flex;flex-direction:column;gap:6px}.pc h3{margin:0;font-size:18px}.pc h3 a{color:#15161A}
.price{font-family:"Bricolage Grotesque",sans-serif;font-weight:800;font-size:20px;letter-spacing:-.02em}
.hero{padding:56px 40px 40px;position:relative;overflow:hidden}
.hero .in{max-width:1360px;margin:0 auto;display:flex;flex-direction:column;gap:12px;position:relative}
.side a{display:flex;justify-content:space-between;padding:8px 10px;color:#15161A;font-weight:600;border-radius:0 8px 0 8px}
.side a.on{background:#15161A;color:#fff}.side a span{color:#5F6470}.side a.on span{color:#fff}
.orow{display:grid;grid-template-columns:150px minmax(0,1fr) 130px 120px 44px;gap:16px;align-items:center;padding:18px 0;border-top:1px solid #E2E3E8}
.tl{display:grid;grid-template-columns:20px minmax(0,1fr);gap:0 14px}
.tl .dot{width:12px;height:12px;border-radius:0 4px 0 4px;background:#D9DBE0;margin:5px 4px}.tl .dot.on{background:#13C2A3}
.tl .ln{width:2px;background:#E2E3E8;margin:0 9px;height:100%}
'''
WSTRIPE = 'style="background:linear-gradient(90deg,#fff 0 52%,transparent 52% 60%,#fff 60% 82%,transparent 82% 88%,#fff 88% 100%)"'
ISTRIPE = 'style="background:linear-gradient(90deg,#15161A 0 52%,transparent 52% 60%,#15161A 60% 82%,transparent 82% 88%,#15161A 88% 100%)"'

def entry(day, ym, title, text, cat, n):
    return f'<article class="entry"><div><div class="day mono">{day}</div><div class="ym">{ym}</div></div><div><h3><a href="Post.dc.html">{title}</a></h3><p>{text}</p><div style="display:flex;gap:8px;margin-top:12px;align-items:center"><span class="chip">{cat}</span><span style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:4px">{I("comment",14)}{n}</span></div></div></article>'

def pager():
    return f'<nav aria-label="分頁" style="display:flex;justify-content:center;align-items:center;gap:8px;margin-top:24px"><a class="btn t" href="#">{I("arrow-l")}上一頁</a><span style="padding:0 16px;font-weight:700;color:#5F6470">1 / 3</span><a class="btn t" href="#">下一頁{I("arrow-r")}</a></nav>'

pages = {}

# ---------- 作者頁 ----------
pages["Author"] = ("前台作者頁", 1700, front("posts", f'''
  <section class="hero" style="background:#13C2A3;color:#0B3A31">
    {cubes(1.6, 1080, -10, .9)}
    <div class="in" style="flex-direction:row;align-items:flex-end;gap:28px">
      <span style="width:120px;height:120px;display:grid;place-items:center;background:#15161A;color:#fff;font-size:48px;border-radius:0 36px 0 36px;flex:0 0 auto" class="d">H</span>
      <div style="display:flex;flex-direction:column;gap:10px;flex:1">
        <span class="stripe" {ISTRIPE}></span>
        <div class="lbl" style="color:#0B5E4E">作者 · 41 篇 · 2026 年 3 月加入</div>
        <h1 class="d" style="font-size:64px;letter-spacing:-.04em">Hazel</h1>
        <p style="margin:0;font-size:17px;max-width:30em">在台中做果凍，順便寫字。主要寫食譜和開店踩過的坑。</p>
        <div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:4px"><a class="btn" href="#">{I("refresh")}訂閱 RSS</a><a class="btn t" href="#">{I("mail")}寫信給她</a><a class="btn t i" href="#" aria-label="Instagram">{I("external")}</a></div>
      </div>
    </div>
  </section>
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:24px 40px 96px;display:grid;grid-template-columns:minmax(0,1fr) 300px;gap:48px;align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:12px;padding-bottom:16px"><div class="seg"><span class="on">最新</span><span>最多留言</span><span>只看食譜</span></div><span style="color:#5F6470;font-size:14px;margin-left:auto">第 1 頁，共 3 頁</span></div>
      {entry("21","2026 · 10","零失敗焦糖布丁，重點在火","焦糖只有十秒的窗口。這篇把爐火、鍋子、顏色都拍下來，照著做第一次就會成功。","甜點",12)}
      {entry("14","2026 · 10","榛果醬自己做，比買的便宜一半","烤 12 分鐘、打 4 分鐘，剩下的時間都在等它涼。附三種濃稠度的比例。","抹醬",4)}
      {entry("30","2026 · 09","開店第 200 天：營業額、退貨率、一天工時","數字全部攤開。有些比想像中好，有些很糟。","開店日記",31)}
      {entry("02","2026 · 09","夏天的薄荷果凍，用吉利丁還是寒天","兩種都做了。口感、融化速度、脫模難度一張表看完。","甜點",27)}
      {pager()}
    </section>
    <aside style="display:flex;flex-direction:column;gap:28px;position:sticky;top:24px">
      <div><div class="lbl" style="margin-bottom:10px">她常寫</div><div style="display:flex;gap:6px;flex-wrap:wrap"><a class="chip nd" href="Archive.dc.html">食譜 28</a><a class="chip nd" href="Archive.dc.html">開店日記 9</a><a class="chip nd" href="Archive.dc.html">產地 4</a></div></div>
      <div><div class="lbl" style="margin-bottom:10px">其他作者</div><nav class="side" style="display:flex;flex-direction:column;gap:2px"><a href="Author.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A"><i style="width:26px;height:26px;background:#2F6BFF;color:#fff;display:grid;place-items:center;border-radius:0 8px 0 8px;font-style:normal;font-weight:800;font-size:12px">T</i>Ting</span><span>12</span></a><a href="Author.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A"><i style="width:26px;height:26px;background:#E8285B;color:#fff;display:grid;place-items:center;border-radius:0 8px 0 8px;font-style:normal;font-weight:800;font-size:12px">小</i>小魚</span><span>6</span></a></nav></div>
      <div><div class="lbl" style="margin-bottom:10px">所有彙整</div><p style="margin:0;font-size:13px;color:#5F6470;line-height:1.7"><a href="ArchiveIndex.dc.html">分類、標籤、月份、作者</a>一頁看完。</p></div>
    </aside>
  </main>'''))

# ---------- 標籤頁 ----------
pages["Tag"] = ("前台標籤頁", 1500, front("posts", f'''
  <section class="hero" style="background:#15161A;color:#fff">
    {cubes(1.6, 1080, -10, .9)}
    <div class="in">
      <span class="stripe"></span>
      <div class="lbl" style="color:#9A9FAB">標籤 · 9 篇 · 跨 3 個分類</div>
      <h1 class="d" style="font-size:72px;letter-spacing:-.04em">#吉利丁</h1>
      <p style="margin:0;font-size:17px;max-width:30em;color:#C9CBD3">所有提到吉利丁的文章。想比較寒天的請看 <a href="Tag.dc.html" style="color:#fff;text-decoration:underline">#寒天</a>。</p>
      <div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:8px"><span style="font-size:13px;color:#9A9FAB;align-self:center">常一起出現：</span><a class="chip nd" href="Tag.dc.html" style="background:#2A2B31;color:#fff">#果凍</a><a class="chip nd" href="Tag.dc.html" style="background:#2A2B31;color:#fff">#新手</a><a class="chip nd" href="Tag.dc.html" style="background:#2A2B31;color:#fff">#脫模</a><a class="chip nd" href="Tag.dc.html" style="background:#2A2B31;color:#fff">#夏天</a></div>
    </div>
  </section>
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:24px 40px 96px;display:grid;grid-template-columns:minmax(0,1fr) 300px;gap:48px;align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:12px;padding-bottom:16px"><span style="color:#5F6470;font-size:14px">9 篇</span><div class="seg" style="margin-left:auto"><span class="on">{I("list",14)}列表</span><span>{I("grid",14)}格狀</span></div></div>
      {entry("02","2026 · 10","夏天的薄荷果凍，用吉利丁還是寒天","兩種都做了。口感、融化速度、脫模難度一張表看完。","甜點",27)}
      {entry("18","2026 · 08","吉利丁片和吉利丁粉怎麼換算","1 片 = 2.5g 粉，但泡法不一樣。附換算表。","甜點",8)}
      {entry("03","2026 · 07","果凍脫模不破的三個條件","溫度、油、等待。第三個最難。","甜點",15)}
      {pager()}
    </section>
    <aside style="display:flex;flex-direction:column;gap:28px;position:sticky;top:24px">
      <div><div class="lbl" style="margin-bottom:10px">月份彙整長這樣</div><div style="background:#F5B800;color:#3A2E00;border-radius:0 16px 0 16px;padding:18px"><span class="stripe" {ISTRIPE}></span><div class="lbl" style="color:#6B5300;margin-top:10px">月份 · 6 篇</div><div class="d" style="font-size:30px;margin-top:4px">2026 年 10 月</div><div style="font-size:13px;margin-top:8px">同一版型，頭換成果凍黃。側欄多一個月份清單。</div></div></div>
      <div><div class="lbl" style="margin-bottom:10px">月份</div><nav class="side" style="display:flex;flex-direction:column;gap:2px"><a href="Tag.dc.html">2026 年 10 月<span>6</span></a><a href="Tag.dc.html">2026 年 9 月<span>8</span></a><a href="Tag.dc.html">2026 年 8 月<span>5</span></a><a href="ArchiveIndex.dc.html" style="color:#2F6BFF;font-weight:600">更早…</a></nav></div>
    </aside>
  </main>'''))

# ---------- 彙整索引：全部分類、標籤、月份、作者 ----------
def tagc(t,n): return f'<a class="chip nd" href="Tag.dc.html" style="font-size:14px;min-height:32px;padding:0 12px">{t} <b style="color:#5F6470">{n}</b></a>'
pages["ArchiveIndex"] = ("前台彙整索引", 1400, front("posts", f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:48px 40px 96px;display:flex;flex-direction:column;gap:48px">
    <div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:56px;letter-spacing:-.04em">所有文章，四種找法</h1><p style="margin:0;color:#5F6470;font-size:17px">共 41 篇。按分類、標籤、月份或作者進去。</p></div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:48px">
      <section><h2 class="d" style="font-size:24px;margin-bottom:16px;display:flex;align-items:center;gap:10px">{I("folder")}分類</h2>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
          <a href="Archive.dc.html" style="background:#2F6BFF;color:#fff;border-radius:0 16px 0 16px;padding:18px;display:flex;flex-direction:column;gap:4px"><span class="d" style="font-size:24px">食譜</span><span style="font-size:13px;opacity:.85">18 篇 · 甜點、抹醬、飲料</span></a>
          <a href="Archive.dc.html" style="background:#E8285B;color:#fff;border-radius:0 16px 0 16px;padding:18px;display:flex;flex-direction:column;gap:4px"><span class="d" style="font-size:24px">開店日記</span><span style="font-size:13px;opacity:.85">9 篇</span></a>
          <a href="Archive.dc.html" style="background:#13C2A3;color:#0B3A31;border-radius:0 16px 0 16px;padding:18px;display:flex;flex-direction:column;gap:4px"><span class="d" style="font-size:24px">產地</span><span style="font-size:13px;opacity:.85">6 篇</span></a>
          <a href="Archive.dc.html" style="background:#F5B800;color:#3A2E00;border-radius:0 16px 0 16px;padding:18px;display:flex;flex-direction:column;gap:4px"><span class="d" style="font-size:24px">公告</span><span style="font-size:13px;opacity:.85">3 篇</span></a>
        </div></section>
      <section><h2 class="d" style="font-size:24px;margin-bottom:16px;display:flex;align-items:center;gap:10px">{I("user")}作者</h2>
        <div style="display:flex;flex-direction:column;gap:8px">
          <a href="Author.dc.html" style="display:flex;align-items:center;gap:14px;padding:12px;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px;color:#15161A"><span style="width:48px;height:48px;background:#15161A;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">H</span><span style="flex:1"><b>Hazel</b><div style="font-size:13px;color:#5F6470">41 篇 · 在台中做果凍，順便寫字。</div></span>{I("arrow-r")}</a>
          <a href="Author.dc.html" style="display:flex;align-items:center;gap:14px;padding:12px;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px;color:#15161A"><span style="width:48px;height:48px;background:#2F6BFF;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">T</span><span style="flex:1"><b>Ting</b><div style="font-size:13px;color:#5F6470">12 篇 · 管網站和出貨。</div></span>{I("arrow-r")}</a>
          <a href="Author.dc.html" style="display:flex;align-items:center;gap:14px;padding:12px;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px;color:#15161A"><span style="width:48px;height:48px;background:#E8285B;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">小</span><span style="flex:1"><b>小魚</b><div style="font-size:13px;color:#5F6470">6 篇 · 校稿、拍照。</div></span>{I("arrow-r")}</a>
        </div></section>
      <section><h2 class="d" style="font-size:24px;margin-bottom:16px;display:flex;align-items:center;gap:10px">{I("tag")}標籤 <span style="font-size:14px;color:#5F6470;font-weight:600">42 個，字越大用越多</span></h2>
        <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">{tagc("榛果",14)}{tagc("新手",11)}{tagc("吉利丁",9)}{tagc("焦糖",7)}{tagc("冰",6)}{tagc("失敗紀錄",5)}{tagc("台中",5)}{tagc("禮盒",3)}{tagc("寒天",3)}{tagc("脫模",2)}{tagc("夏天",2)}{tagc("中秋",2)}<a href="Tag.dc.html" style="font-size:13px">還有 30 個…</a></div></section>
      <section><h2 class="d" style="font-size:24px;margin-bottom:16px;display:flex;align-items:center;gap:10px">{I("calendar")}月份</h2>
        <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:6px">
          <a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">2026 · 10<span style="color:#5F6470">6</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">09<span style="color:#5F6470">8</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">08<span style="color:#5F6470">5</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">07<span style="color:#5F6470">4</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">06<span style="color:#5F6470">7</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">05<span style="color:#5F6470">3</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">04<span style="color:#5F6470">5</span></a><a href="Tag.dc.html" style="padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px;color:#15161A;font-weight:600;display:flex;justify-content:space-between">03<span style="color:#5F6470">3</span></a>
        </div></section>
    </div>
  </main>'''))

# ---------- 商品分類頁 ----------
def pcard(bg, fg, name, price, extra=""):
    return f'<article class="pc"><div class="img" style="background:{bg};color:{fg}">{name}</div><div class="b"><h3><a href="Product.dc.html">{name}</a></h3><div style="display:flex;align-items:center;justify-content:space-between"><span class="price">{price}{extra}</span><button class="btn sm" type="button">{I("plus")}加入</button></div></div></article>'
pages["ShopCategory"] = ("前台商品分類頁", 1500, front("shop", f'''
  <section class="hero" style="background:#2F6BFF;color:#fff;padding-bottom:0">
    {cubes(1.6, 1080, -10, .9)}
    <div class="in">
      <nav style="font-size:13px;color:#C9D8FF;display:flex;gap:8px" aria-label="路徑"><a href="Shop.dc.html" style="color:#fff">商店</a><span>/</span><b>果凍</b></nav>
      <span class="stripe" {WSTRIPE}></span>
      <div class="lbl" style="color:#C9D8FF">商品分類 · 18 件</div>
      <h1 class="d" style="font-size:72px;letter-spacing:-.04em">果凍</h1>
      <p style="margin:0;font-size:17px;max-width:30em;color:#E3EBFF">冷藏寄出，到貨兩天內最好吃。6 入起跳，混搭請選「自選組」。</p>
      <div style="display:flex;gap:6px;flex-wrap:wrap;margin-top:16px;padding-bottom:28px"><span class="chip nd" style="background:#fff;color:#15161A;min-height:32px;padding:0 12px">全部 18</span><a class="chip nd" href="ShopCategory.dc.html" style="background:rgba(255,255,255,.18);color:#fff;min-height:32px;padding:0 12px">水果 8</a><a class="chip nd" href="ShopCategory.dc.html" style="background:rgba(255,255,255,.18);color:#fff;min-height:32px;padding:0 12px">茶與咖啡 5</a><a class="chip nd" href="ShopCategory.dc.html" style="background:rgba(255,255,255,.18);color:#fff;min-height:32px;padding:0 12px">自選組 3</a><a class="chip nd" href="ShopCategory.dc.html" style="background:rgba(255,255,255,.18);color:#fff;min-height:32px;padding:0 12px">季節限定 2</a></div>
    </div>
  </section>
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:24px 40px 96px;display:grid;grid-template-columns:240px minmax(0,1fr);gap:40px;align-items:start">
    <aside style="display:flex;flex-direction:column;gap:24px;position:sticky;top:24px">
      <div><div class="lbl" style="margin-bottom:8px">分類</div><nav class="side" style="display:flex;flex-direction:column;gap:2px"><a href="Shop.dc.html">全部<span>42</span></a><a href="ShopCategory.dc.html">抹醬<span>8</span></a><a class="on" href="ShopCategory.dc.html">果凍<span>18</span></a><a href="ShopCategory.dc.html" style="padding-left:24px">水果<span>8</span></a><a href="ShopCategory.dc.html" style="padding-left:24px">茶與咖啡<span>5</span></a><a href="ShopCategory.dc.html" style="padding-left:24px">自選組<span>3</span></a><a href="ShopCategory.dc.html">禮盒<span>6</span></a><a href="ShopCategory.dc.html">周邊<span>10</span></a></nav></div>
      <div><div class="lbl" style="margin-bottom:8px">價格</div><div style="display:flex;gap:6px;flex-wrap:wrap"><a class="chip nd" href="#">300 以下</a><a class="chip nd" href="#" style="background:#15161A;color:#fff">300–800</a><a class="chip nd" href="#">800 以上</a></div></div>
      <div><div class="lbl" style="margin-bottom:8px">特性</div><div style="display:flex;flex-direction:column;gap:6px;font-size:14px;font-weight:600"><label style="display:flex;align-items:center;gap:8px"><input type="checkbox" checked style="accent-color:#E8285B;width:16px;height:16px;margin:0">只看有貨</label><label style="display:flex;align-items:center;gap:8px"><input type="checkbox" style="accent-color:#E8285B;width:16px;height:16px;margin:0">素食（寒天）</label><label style="display:flex;align-items:center;gap:8px"><input type="checkbox" style="accent-color:#E8285B;width:16px;height:16px;margin:0">可常溫寄送</label></div></div>
      <a href="Shop.dc.html" style="font-size:13px;display:inline-flex;align-items:center;gap:4px">{I("x",13)}清除篩選</a>
    </aside>
    <section style="display:flex;flex-direction:column;gap:16px">
      <div style="display:flex;align-items:center;gap:12px"><span style="color:#5F6470;font-size:14px">果凍 · 300–800 · 有貨 · 共 11 件</span><label for="sort" style="margin-left:auto;font-size:13px;color:#5F6470">排序</label><select id="sort" class="field" style="min-height:38px;width:160px"><option>人氣</option><option>最新上架</option><option>價格低到高</option></select></div>
      <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px">
        {pcard("#2F6BFF","#fff","藍莓果凍 6 入","NT$ 240")}
        {pcard("#13C2A3","#0B3A31","薄荷果凍 6 入","NT$ 240",' <span style="font-size:12px;color:#9C2318;font-weight:700">剩 4</span>')}
        {pcard("#E8285B","#fff","草莓果凍 6 入","NT$ 260")}
        {pcard("#F5B800","#3A2E00","芒果寒天 6 入","NT$ 260")}
        {pcard("#15161A","#fff","烏龍茶凍 6 入","NT$ 280")}
        {pcard("#EDEEF1","#15161A","自選 12 入","NT$ 480")}
      </div>
      {pager()}
    </section>
  </main>'''))

# ---------- 會員中心：我的訂單 ----------
def orow(no, date, items, total, state, cls, colors):
    thumbs = "".join(f'<span style="width:40px;height:40px;background:{c};border-radius:0 8px 0 8px;margin-left:-8px;box-shadow:0 0 0 2px #fff"></span>' for c in colors)
    return f'<div class="orow"><div><b class="mono" style="font-size:15px">{no}</b><div style="font-size:12px;color:#5F6470">{date}</div></div><div style="display:flex;align-items:center;gap:12px"><span style="display:flex;padding-left:8px">{thumbs}</span><span style="font-size:14px">{items}</span></div><span class="mono" style="font-size:16px">{total}</span><span class="chip {cls}">{state}</span><a class="btn t sm i" href="Account.dc.html" aria-label="查看">{I("arrow-r")}</a></div>'
pages["Account"] = ("前台會員中心", 1300, front("shop", f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:40px 40px 96px;display:grid;grid-template-columns:240px minmax(0,1fr);gap:48px;align-items:start">
    <aside style="display:flex;flex-direction:column;gap:20px;position:sticky;top:24px">
      <div style="display:flex;align-items:center;gap:12px"><span style="width:48px;height:48px;background:#E8285B;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">T</span><div><b>Ting</b><div style="font-size:12px;color:#5F6470">ting@example.com</div></div></div>
      <nav class="side" style="display:flex;flex-direction:column;gap:2px"><a class="on" href="Account.dc.html"><span style="display:flex;align-items:center;gap:10px;color:inherit">{I("cart",16)}我的訂單</span><span>5</span></a><a href="Account.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A">{I("truck",16)}收件地址</span></a><a href="Account.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A">{I("card",16)}發票設定</span></a><a href="Account.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A">{I("user",16)}帳號與密碼</span></a><a href="Account.dc.html"><span style="display:flex;align-items:center;gap:10px;color:#15161A">{I("mail",16)}通知</span></a></nav>
      <a href="Login.dc.html" style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:6px">{I("x",13)}登出</a>
    </aside>
    <section style="display:flex;flex-direction:column;gap:20px">
      <div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">我的訂單</h1></div>
      <div class="seg" style="align-self:flex-start"><span class="on">全部 5</span><span>處理中 1</span><span>已出貨 1</span><span>已完成 3</span></div>
      <div>
        <div style="border-top:0;padding:0 0 10px;display:grid;grid-template-columns:150px minmax(0,1fr) 130px 120px 44px;gap:16px;font-size:12px;font-weight:700;color:#5F6470;letter-spacing:.06em;text-transform:uppercase"><span>訂單</span><span>商品</span><span>金額</span><span>狀態</span><span></span></div>
        {orow("JL-2026-0417","今天 16:02","榛果可可醬 ＋ 藍莓果凍 6 入 × 2","NT$ 880","處理中","warn",["#E8285B","#2F6BFF"])}
        {orow("JL-2026-0388","10/15","自選 12 入","NT$ 480","已出貨 · 黑貓 9012-3344","info",["#EDEEF1"])}
        {orow("JL-2026-0301","09/28","限量禮盒","NT$ 1,280","已完成","ok",["#15161A"])}
        {orow("JL-2026-0255","09/02","薄荷果凍 6 入 × 3","NT$ 720","已完成","ok",["#13C2A3"])}
        {orow("JL-2026-0190","08/11","果凍杯 DIY 組","NT$ 680","已退款","",["#F5B800"])}
      </div>
      <div style="background:#F5F5F7;border-radius:0 16px 0 16px;padding:20px 24px;display:grid;grid-template-columns:minmax(0,1fr) 320px;gap:24px;align-items:start">
        <div><div class="lbl" style="margin-bottom:10px">JL-2026-0417 · 點開長這樣</div>
          <div class="tl" style="font-size:14px">
            <span class="dot on"></span><div><b>已付款</b> <span style="color:#5F6470;font-size:13px">今天 16:02 · 信用卡末四碼 4321</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot on"></span><div><b>備貨中</b> <span style="color:#5F6470;font-size:13px">預計明天出貨</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot"></span><div style="color:#9A9FAB"><b>已出貨</b> <span style="font-size:13px">黑貓宅配，會給追蹤號碼</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot"></span><div style="color:#9A9FAB"><b>已送達</b></div>
          </div></div>
        <div style="display:flex;flex-direction:column;gap:8px"><a class="btn t sm" href="#">{I("download")}下載發票</a><a class="btn t sm" href="#">{I("comment")}聯絡客服（帶訂單號）</a><a class="btn t sm" href="#">{I("x")}取消訂單（出貨前）</a><a class="btn t sm" href="Cart.dc.html">{I("refresh")}再買一次</a></div>
      </div>
    </section>
  </main>'''))

# ---------- 訪客訂單查詢 ----------
pages["OrderLookup"] = ("前台訂單查詢", 1000, front("shop", f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:56px 40px 96px;display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:64px;align-items:start">
    <section style="display:flex;flex-direction:column;gap:20px">
      <span class="stripe"></span>
      <div class="lbl">沒有帳號也能查</div>
      <h1 class="d" style="font-size:56px;letter-spacing:-.04em">查訂單</h1>
      <p style="margin:0;color:#5F6470;font-size:17px;line-height:1.7;max-width:26em">輸入訂單編號和下單用的 Email，我們寄一個 15 分鐘有效的連結給你。信裡的連結點開就是訂單頁。</p>
      <form style="display:flex;flex-direction:column;gap:14px;max-width:420px" onsubmit="return false">
        <div><label class="flbl" for="no">訂單編號</label><input id="no" class="field" type="text" placeholder="JL-2026-0417" style="font-family:'Bricolage Grotesque',sans-serif;font-weight:700"></div>
        <div><label class="flbl" for="em">下單的 Email</label><input id="em" class="field" type="email" placeholder="you@example.com"></div>
        <button class="btn p" type="button" style="align-self:flex-start;min-height:52px">{I("mail")}寄查詢連結給我</button>
        <div style="font-size:13px;color:#5F6470">訂單編號在確認信的主旨。找不到信？<a href="#">重寄確認信</a></div>
      </form>
    </section>
    <aside style="background:#15161A;color:#fff;border-radius:0 32px 0 32px;padding:32px;display:flex;flex-direction:column;gap:16px;position:relative;overflow:hidden">
      {cubes(1.2, 460, 200, .85)}
      <div class="lbl" style="color:#9A9FAB">寄出後</div>
      <div style="display:flex;align-items:center;gap:12px"><span style="width:44px;height:44px;background:#13C2A3;color:#0B3A31;display:grid;place-items:center;border-radius:0 12px 0 12px">{I("check",20)}</span><div><b style="font-size:17px">連結寄到 ti•••@example.com 了</b><div style="font-size:13px;color:#C9CBD3">15 分鐘內有效，沒收到請看垃圾信。</div></div></div>
      <div style="border-top:1px solid #2A2B31;padding-top:16px;display:flex;flex-direction:column;gap:8px;font-size:14px;color:#C9CBD3;position:relative">
        <b style="color:#fff">為什麼不直接顯示？</b>
        <span>訂單裡有地址和電話。只知道編號的人（例如看到你的包裹）不該看得到。</span>
        <b style="color:#fff;margin-top:8px">想以後不用查？</b>
        <span>結帳時勾「建立帳號」，訂單都在<a href="Account.dc.html" style="color:#fff;text-decoration:underline">會員中心</a>。</span>
      </div>
    </aside>
  </main>'''))

for name,(title,h,html) in pages.items():
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, CSS, html, 1440, h)); print("wrote",name,h)
