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
pages["ShopCategory"] = ("前台商品分類頁", 1650, front("shop", f'''
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
ACCT_NAV = [("orders","Account.dc.html","cart","我的訂單"),("addresses","AccountAddresses.dc.html","truck","收件地址"),("invoices","AccountInvoices.dc.html","card","發票設定"),("security","AccountSecurity.dc.html","user","帳號與密碼"),("notifications","AccountNotifications.dc.html","mail","通知")]
def acct_nav(on):
    links = []
    for key, href, ic, label in ACCT_NAV:
        if key == on:
            links.append(f'<a class="on" href="{href}"><span style="display:flex;align-items:center;gap:10px;color:inherit">{I(ic,16)}{label}</span>' + ('<span>5</span>' if key == "orders" else '') + '</a>')
        else:
            links.append(f'<a href="{href}"><span style="display:flex;align-items:center;gap:10px;color:#15161A">{I(ic,16)}{label}</span></a>')
    return '<nav class="side" style="display:flex;flex-direction:column;gap:2px">' + "".join(links) + '</nav>'
def acct_page(on, body):
    return f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:40px 40px 96px;display:grid;grid-template-columns:240px minmax(0,1fr);gap:48px;align-items:start">
    <aside style="display:flex;flex-direction:column;gap:20px;position:sticky;top:24px">
      <div style="display:flex;align-items:center;gap:12px"><span style="width:48px;height:48px;background:#E8285B;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">T</span><div><b>Ting</b><div style="font-size:12px;color:#5F6470">ting@example.com</div></div></div>
      {acct_nav(on)}
      <a href="Login.dc.html" style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:6px">{I("x",13)}登出</a>
    </aside>
    <section style="display:flex;flex-direction:column;gap:20px">{body}</section>
  </main>'''

pages["Account"] = ("前台會員中心", 1300, front("shop", f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:40px 40px 96px;display:grid;grid-template-columns:240px minmax(0,1fr);gap:48px;align-items:start">
    <aside style="display:flex;flex-direction:column;gap:20px;position:sticky;top:24px">
      <div style="display:flex;align-items:center;gap:12px"><span style="width:48px;height:48px;background:#E8285B;color:#fff;display:grid;place-items:center;border-radius:0 14px 0 14px;font-size:20px" class="d">T</span><div><b>Ting</b><div style="font-size:12px;color:#5F6470">ting@example.com</div></div></div>
      {acct_nav("orders")}
      <a href="Login.dc.html" style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:6px">{I("x",13)}登出</a>
    </aside>
    <section style="display:flex;flex-direction:column;gap:20px">
      <div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">我的訂單</h1></div>
      <div class="seg" style="align-self:flex-start"><span class="on">全部 5</span><span>處理中 1</span><span>已出貨 1</span><span>已完成 3</span></div>
      <div>
        <div style="border-top:0;padding:0 0 10px;display:grid;grid-template-columns:150px minmax(0,1fr) 130px 120px 44px;gap:16px;font-size:12px;font-weight:700;color:#5F6470;letter-spacing:.06em;text-transform:uppercase"><span>訂單</span><span>商品</span><span>金額</span><span>狀態</span><span></span></div>
        {orow("JL-2026-0417","今天 16:02","榛果可可醬 ＋ 藍莓果凍 6 入 × 2","NT$ 880","處理中","warn",["#E8285B","#2F6BFF"]).replace('href="Account.dc.html"','href="AccountOrder.dc.html"')}
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
      <p style="margin:0;color:#5F6470;font-size:17px;line-height:1.7;max-width:26em">輸入訂單編號和下單用的 Email。如果資料相符，我們會寄出 15 分鐘有效的連結，點開即可查看訂單。</p>
      <form style="display:flex;flex-direction:column;gap:14px;max-width:420px" onsubmit="return false">
        <div><label class="flbl" for="no">訂單編號</label><input id="no" class="field" type="text" placeholder="JL-2026-0417" style="font-family:'Bricolage Grotesque',sans-serif;font-weight:700"></div>
        <div><label class="flbl" for="em">下單的 Email</label><input id="em" class="field" type="email" placeholder="you@example.com"></div>
        <button class="btn p" type="button" style="align-self:flex-start;min-height:52px">{I("mail")}寄查詢連結給我</button>
        <div style="font-size:13px;color:#5F6470">訂單編號在確認信的主旨裡。</div>
      </form>
    </section>
    <aside style="background:#15161A;color:#fff;border-radius:0 32px 0 32px;padding:32px;display:flex;flex-direction:column;gap:16px;position:relative;overflow:hidden">
      {cubes(1.2, 460, 200, .85)}
      <div class="lbl" style="color:#9A9FAB">寄出後</div>
      <div style="display:flex;align-items:center;gap:12px"><span style="width:44px;height:44px;background:#13C2A3;color:#0B3A31;display:grid;place-items:center;border-radius:0 12px 0 12px">{I("check",20)}</span><div><b style="font-size:17px">如果編號和 Email 相符，系統會寄出查詢連結到 ti•••@example.com</b><div style="font-size:13px;color:#C9CBD3">不論是否相符都顯示同一句；連結 15 分鐘有效，沒收到請看垃圾信。</div></div></div>
      <div style="border-top:1px solid #2A2B31;padding-top:16px;display:flex;flex-direction:column;gap:8px;font-size:14px;color:#C9CBD3;position:relative">
        <b style="color:#fff">為什麼不直接顯示？</b>
        <span>訂單裡有地址和電話。只知道編號的人（例如看到你的包裹）不該看得到。</span>
        <b style="color:#fff;margin-top:8px">沒收到信？</b>
        <span>先看看垃圾信匣，再確認編號和 Email 與下單時相同後重新送出。</span>
        <b style="color:#fff;margin-top:8px">想以後不用查？</b>
        <span>結帳時勾「建立帳號」，訂單都在<a href="Account.dc.html" style="color:#fff;text-decoration:underline">會員中心</a>。</span>
      </div>
    </aside>
  </main>'''))

# ---------- 訪客/權杖訂單檢視 ----------
pages["OrderView"] = ("前台訂單檢視", 1300, front("shop", f'''
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:40px 40px 96px;display:flex;flex-direction:column;gap:32px">
    <div style="display:flex;align-items:flex-end;justify-content:space-between;gap:24px;flex-wrap:wrap">
      <div style="display:flex;flex-direction:column;gap:12px">
        <span class="stripe"></span>
        <div style="display:flex;align-items:center;gap:12px">
          <h1 class="d" style="font-size:44px;letter-spacing:-.035em">訂單 JL-2026-0417</h1>
          <span class="chip warn">處理中</span>
        </div>
        <div style="font-size:14px;color:#5F6470">下單時間：今天 16:02 · 以查詢連結開啟</div>
      </div>
      <div style="display:flex;gap:10px">
        <a class="btn t sm" href="OrderLookup.dc.html">{I("arrow-l")}查其他訂單</a>
        <a class="btn t sm" href="Shop.dc.html">{I("box")}繼續逛商店</a>
      </div>
    </div>

    <div style="display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);gap:40px;align-items:start">
      <section style="display:flex;flex-direction:column;gap:24px">
        <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:24px;display:flex;flex-direction:column;gap:16px">
          <div class="lbl">訂單商品</div>
          <div style="display:flex;gap:14px;align-items:center;padding:12px 0;border-bottom:1px solid #E2E3E8">
            <span style="width:56px;height:56px;background:#E8285B;border-radius:0 10px 0 10px;flex:0 0 auto"></span>
            <div style="flex:1"><b>榛果可可醬 200g</b><div style="font-size:13px;color:#5F6470">200g · 單價 NT$ 320</div></div>
            <span style="font-size:14px;color:#5F6470">× 1</span>
            <b class="mono" style="font-size:16px">NT$ 320</b>
          </div>
          <div style="display:flex;gap:14px;align-items:center;padding:12px 0;border-bottom:1px solid #E2E3E8">
            <span style="width:56px;height:56px;background:#2F6BFF;border-radius:0 10px 0 10px;flex:0 0 auto"></span>
            <div style="flex:1"><b>藍莓果凍 6 入</b><div style="font-size:13px;color:#5F6470">單價 NT$ 240</div></div>
            <span style="font-size:14px;color:#5F6470">× 2</span>
            <b class="mono" style="font-size:16px">NT$ 480</b>
          </div>
          <div style="display:grid;grid-template-columns:1fr auto;gap:8px;font-size:14px;padding-top:8px">
            <span style="color:#5F6470">商品小計</span><b>NT$ 800</b>
            <span style="color:#5F6470">運費（宅配）</span><b>NT$ 80</b>
            <span style="color:#5F6470">尾數調整</span><b>NT$ 0</b>
            <span style="color:#5F6470">尾數調整</span><b>NT$ 0</b>
            <span style="font-size:16px;font-weight:700;padding-top:8px;border-top:1px solid #E2E3E8">總計金額</span>
            <span class="mono" style="font-size:24px;font-weight:800;padding-top:8px;border-top:1px solid #E2E3E8">NT$ 880</span>
          </div>
        </div>

        <div style="background:#F5F5F7;border-radius:0 16px 0 16px;padding:24px;display:flex;flex-direction:column;gap:16px">
          <div class="lbl">出貨時間軸</div>
          <div class="tl" style="font-size:14px">
            <span class="dot on"></span><div><b>訂單成立並付款</b> <span style="color:#5F6470;font-size:13px">今天 16:02 · 信用卡付款完成</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot on"></span><div><b>備貨中</b> <span style="color:#5F6470;font-size:13px">預計 1-2 個工作天內出貨</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot"></span><div style="color:#9A9FAB"><b>已出貨</b> <span style="font-size:13px">黑貓宅配，出貨時更新包裹追蹤號碼</span></div>
            <span class="ln"></span><div style="height:14px"></div>
            <span class="dot"></span><div style="color:#9A9FAB"><b>已送達</b></div>
          </div>
        </div>
      </section>

      <aside style="display:flex;flex-direction:column;gap:20px">
        <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;display:flex;flex-direction:column;gap:14px">
          <div class="lbl">收件與聯絡資料</div>
          <div style="font-size:14px;line-height:1.7">
            <div><b>收件人：</b>賴庭蓁</div>
            <div><b>電話：</b>0912-345-678</div>
            <div><b>配送方式：</b>宅配（本島 2 天）</div>
            <div><b>地址：</b>台中市西屯區台灣大道三段 99 號</div>
          </div>
        </div>

        <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;display:flex;flex-direction:column;gap:14px">
          <div class="lbl">付款與發票</div>
          <div style="font-size:14px;line-height:1.7">
            <div><b>付款方式：</b>信用卡（綠界 ECPay）</div>
            <div><b>付款狀態：</b>已付款（NT$ 880）</div><div style="font-size:12px;color:#5F6470">無需付款的訂單顯示「訂單已成立」，不標示付款成功。</div>
            <div><b>發票載具：</b>手機條碼 /ABC1234</div>
          </div>
        </div>

        <div style="display:flex;flex-direction:column;gap:8px">
          <button class="btn t" type="button">{I("download")}列印收據</button>
          <a class="btn t" href="#">{I("comment")}聯絡客服（帶訂單編號）</a>
          <div style="font-size:12px;color:#5F6470;line-height:1.6">要取消訂單請聯絡客服；有帳號的話也可以到會員中心取消未出貨的訂單。</div>
        </div>
      </aside>
    </div>
  </main>'''))

# ---------- 沙盒付款測試 ----------
pages["SandboxPay"] = ("前台沙盒付款測試", 900, front("shop", f'''
  <main style="max-width:800px;margin:0 auto;width:100%;box-sizing:border-box;padding:56px 40px 96px;display:flex;flex-direction:column;gap:28px">
    <div style="background:#FCF0C8;color:#7A5200;border-radius:0 16px 0 16px;padding:16px 20px;display:flex;align-items:center;gap:12px">
      {I("alert",20)}
      <div style="font-size:14px;line-height:1.6">
        <b>本機沙盒測試模式：</b>此頁僅在非正式環境且啟用沙盒金流時可用，不會產生真實付款或扣款交易。
      </div>
    </div>

    <div style="display:flex;flex-direction:column;gap:12px">
      <span class="stripe"></span>
      <div class="lbl">金流沙盒 · 本地測試</div>
      <h1 class="d" style="font-size:44px;letter-spacing:-.035em">模擬付款閘道</h1>
      <p style="margin:0;color:#5F6470;font-size:16px">交易編號：<b class="mono" style="color:#15161A">SANDBOX-20261024-001</b></p>
    </div>

    <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:28px;display:flex;flex-direction:column;gap:20px">
      <div style="display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #E2E3E8;padding-bottom:16px">
        <span style="color:#5F6470">訂單金額</span>
        <span class="d" style="font-size:32px">NT$ 880</span>
      </div>
      <div style="font-size:14px;color:#5F6470;line-height:1.7">
        選擇要模擬的金流結果。伺服器會用本站自己的沙盒金鑰簽署通知，並和真實金流回呼走同一套處理；瀏覽器拿不到金鑰。
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:12px">
        <button class="btn p" type="button" style="min-height:52px">{I("check")}模擬付款成功</button>
        <button class="btn t" type="button" style="min-height:52px;color:#9C2318">{I("x")}模擬付款失敗</button>
        <button class="btn t" type="button" style="min-height:52px">{I("clock")}模擬 ATM 待繳</button>
      </div>
    </div>
  </main>''', cart=False))


# ---------- 會員中心：訂單詳情 ----------
pages["AccountOrder"] = ("前台會員訂單詳情", 1400, front("shop", acct_page("orders", f'''
      <a href="Account.dc.html" style="font-size:13px">{I("arrow-l",13)}全部訂單</a>
      <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap"><h1 class="d" style="font-size:40px;letter-spacing:-.035em">訂單 JL-2026-0417</h1><span class="chip info">處理中</span></div>
      <div style="font-size:14px;color:#5F6470;margin-top:-12px">下單時間：今天 16:02</div>
      <div style="background:#E6F7F4;color:#0B5E4E;border-radius:0 10px 0 10px;padding:12px 14px;font-size:13px">無需付款的訂單顯示「訂單已成立」，付款進度顯示「無需付款」。</div>
      <div style="display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);gap:32px;align-items:start">
        <div style="display:flex;flex-direction:column;gap:20px">
          <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;display:flex;flex-direction:column;gap:12px">
            <div class="lbl">訂單商品</div>
            <div style="display:flex;gap:12px;align-items:center;padding-bottom:12px;border-bottom:1px solid #E2E3E8"><span style="width:52px;height:52px;background:#E8285B;border-radius:0 10px 0 10px"></span><div style="flex:1"><b>榛果可可醬 200g</b><div style="font-size:12px;color:#5F6470">200g · 單價 NT$ 320</div></div><span style="color:#5F6470;font-size:13px">× 1</span><b class="mono">NT$ 320</b></div>
            <div style="display:flex;gap:12px;align-items:center;padding-bottom:12px;border-bottom:1px solid #E2E3E8"><span style="width:52px;height:52px;background:#2F6BFF;border-radius:0 10px 0 10px"></span><div style="flex:1"><b>藍莓果凍 6 入</b><div style="font-size:12px;color:#5F6470">單價 NT$ 240</div></div><span style="color:#5F6470;font-size:13px">× 2</span><b class="mono">NT$ 480</b></div>
            <div style="display:grid;grid-template-columns:1fr auto;gap:6px;font-size:14px"><span style="color:#5F6470">商品小計</span><b>NT$ 800</b><span style="color:#5F6470">運費 · 宅配</span><b>NT$ 80</b><span style="color:#5F6470">尾數調整</span><b>NT$ 0</b><span style="font-weight:700;padding-top:8px;border-top:1px solid #E2E3E8">總計金額</span><span class="mono" style="font-size:22px;padding-top:8px;border-top:1px solid #E2E3E8">NT$ 880</span></div>
          </div>
          <div style="background:#F5F5F7;border-radius:0 16px 0 16px;padding:20px"><div class="lbl" style="margin-bottom:10px">出貨進度</div>
            <div class="tl" style="font-size:14px">
              <span class="dot on"></span><div><b>訂單成立</b> <span style="color:#5F6470;font-size:13px">今天 16:02</span></div><span class="ln"></span><div style="height:12px"></div>
              <span class="dot on"></span><div><b>已付款</b> <span style="color:#5F6470;font-size:13px">今天 16:03</span></div><span class="ln"></span><div style="height:12px"></div>
              <span class="dot on"></span><div><b>備貨中</b></div><span class="ln"></span><div style="height:12px"></div>
              <span class="dot"></span><div style="color:#9A9FAB"><b>已出貨</b> <span style="font-size:13px">出貨後顯示物流與追蹤號碼</span></div><span class="ln"></span><div style="height:12px"></div>
              <span class="dot"></span><div style="color:#9A9FAB"><b>已送達</b></div>
            </div></div>
        </div>
        <div style="display:flex;flex-direction:column;gap:16px">
          <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;font-size:14px;line-height:1.8"><div class="lbl" style="margin-bottom:6px">收件與聯絡資料</div><b>收件人</b> 賴庭蓁<br><b>電話</b> 0912-345-678<br><b>配送方式</b> 宅配<br><b>地址</b> 407 台中市西屯區台灣大道三段 99 號</div>
          <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;font-size:14px;line-height:1.8"><div class="lbl" style="margin-bottom:6px">付款與發票</div><b>付款方式</b> 信用卡<br><b>付款狀態</b> 已付款（NT$ 880）· 末四碼 4321<br><b>發票</b> 手機條碼 /ABC1234</div>
          <a class="btn t" href="#">{I("download")}列印收據</a>
          <a class="btn t" href="#">{I("comment")}聯絡客服（帶訂單編號）</a>
          <a class="btn t" href="Cart.dc.html">{I("refresh")}再買一次</a>
          <button class="btn t" type="button" style="color:#9C2318">{I("undo")}申請退款（出貨前）</button>
          <div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;font-size:13px;padding:12px;background:#FBE1DE;color:#9C2318;border-radius:0 10px 0 10px">已付款的訂單會送出退款申請，等商家確認後安排退款。<button class="btn t sm" type="button" style="color:#9C2318">確定送出</button><button class="btn x sm" type="button">先不要</button></div>
        </div>
      </div>''')))

# ---------- 會員中心：收件地址 ----------
def addr(label, who, lines, default=False):
    d = '<span class="chip ok">預設</span>' if default else ''
    mk = '' if default else '<button class="btn t sm" type="button">設為預設</button>'
    return f'<div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;display:flex;flex-direction:column;gap:10px"><div style="display:flex;gap:8px;align-items:center"><b>{label} · {who}</b>{d}</div><div style="font-size:13px;color:#5F6470;line-height:1.7">{lines}</div><div style="display:flex;gap:8px"><button class="btn t sm" type="button">編輯</button>{mk}<button class="btn x sm" type="button" style="color:#9C2318">刪除</button></div></div>'
pages["AccountAddresses"] = ("前台會員收件地址", 1100, front("shop", acct_page("addresses", f'''
      <div style="display:flex;align-items:flex-end;justify-content:space-between;gap:16px"><div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">收件地址</h1></div><button class="btn p sm" type="button">{I("plus")}新增地址</button></div>
      <div style="font-size:14px;color:#5F6470">結帳時會自動帶入預設地址。</div>
      <form style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:24px;display:flex;flex-direction:column;gap:14px" onsubmit="return false">
        <b class="d" style="font-size:20px">新增地址</b>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:14px">
          <div><label class="flbl" for="a1">名稱（可選）</label><input id="a1" class="field" type="text" placeholder="家、公司…"></div>
          <div><label class="flbl" for="a2">收件人</label><input id="a2" class="field" type="text" value="賴庭蓁"></div>
          <div><label class="flbl" for="a3">電話</label><input id="a3" class="field" type="tel" value="0912" style="box-shadow:inset 0 0 0 1.5px #E8285B"><div style="font-size:12px;color:#9C2318;margin-top:6px;font-weight:600">請輸入有效的電話號碼。</div></div>
          <div><label class="flbl" for="a4">郵遞區號</label><input id="a4" class="field" type="text"></div>
          <div><label class="flbl" for="a5">縣市</label><input id="a5" class="field" type="text"></div>
          <div><label class="flbl" for="a6">地址</label><input id="a6" class="field" type="text"></div>
        </div>
        <label style="display:inline-flex;align-items:center;gap:8px;font-size:14px"><input type="checkbox" style="accent-color:#E8285B;width:16px;height:16px;margin:0">設為預設地址</label>
        <div style="display:flex;gap:8px"><button class="btn p sm" type="button">儲存</button><button class="btn x sm" type="button">取消</button></div>
      </form>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:14px">
        {addr("家","賴庭蓁","0912-345-678<br>407 台中市西屯區台灣大道三段 99 號",True)}
        {addr("公司","賴庭蓁","0912-345-678<br>100 台北市中正區重慶南路一段 1 號")}
      </div>''')))

# ---------- 會員中心：發票設定 ----------
pages["AccountInvoices"] = ("前台會員發票設定", 1000, front("shop", acct_page("invoices", f'''
      <div style="display:flex;align-items:flex-end;justify-content:space-between;gap:16px"><div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">發票設定</h1></div><button class="btn p sm" type="button">{I("plus")}新增設定</button></div>
      <div style="font-size:14px;color:#5F6470">儲存手機條碼、公司統編或捐贈碼，結帳時會帶入預設的那一組。</div>
      <form style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:24px;display:flex;flex-direction:column;gap:14px" onsubmit="return false">
        <b class="d" style="font-size:20px">新增發票設定</b>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:14px">
          <div><label class="flbl" for="i1">類型</label><select id="i1" class="field"><option>公司統編</option><option>手機條碼載具</option><option>捐贈</option><option>個人（Email 收據）</option></select></div>
          <div></div>
          <div><label class="flbl" for="i2">統一編號</label><input id="i2" class="field" type="text" value="12345678" style="box-shadow:inset 0 0 0 1.5px #E8285B"><div style="font-size:12px;color:#9C2318;margin-top:6px;font-weight:600">統一編號格式或檢查碼不正確。</div></div>
          <div><label class="flbl" for="i3">公司抬頭</label><input id="i3" class="field" type="text" value="榛果繽紛樂有限公司"></div>
        </div>
        <label style="display:inline-flex;align-items:center;gap:8px;font-size:14px"><input type="checkbox" style="accent-color:#E8285B;width:16px;height:16px;margin:0">結帳時預設使用</label>
        <div style="display:flex;gap:8px"><button class="btn p sm" type="button">儲存</button><button class="btn x sm" type="button">取消</button></div>
      </form>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:14px">
        {addr("手機條碼載具","/ABC1234","存在手機條碼，發票中獎會自動通知。",True)}
        {addr("捐贈","8585","捐給慈善團體。")}
      </div>''')))

# ---------- 會員中心：帳號與密碼 ----------
def card(title, inner):
    return f'<div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:22px;display:flex;flex-direction:column;gap:14px"><b class="d" style="font-size:20px">{title}</b>{inner}</div>'
pages["AccountSecurity"] = ("前台會員帳號與密碼", 1750, front("shop", acct_page("security", f'''
      <div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">帳號與密碼</h1></div>
      {card("基本資料", '<div style="display:grid;grid-template-columns:1fr 1fr;gap:14px"><div><label class="flbl" for="s1">顯示名稱</label><input id="s1" class="field" type="text" value="Ting"></div><div><label class="flbl" for="s2">語言</label><select id="s2" class="field"><option>繁體中文</option><option>English</option></select></div></div><div><button class="btn t sm" type="button">儲存</button></div>')}
      {card("Email", '<div style="font-size:14px">目前：<b>ting@example.com</b></div><span class="chip warn" style="align-self:flex-start">等待確認：ting@new.example</span><div style="display:grid;grid-template-columns:1fr 1fr;gap:14px"><div><label class="flbl" for="s3">新的 Email</label><input id="s3" class="field" type="email"></div><div><label class="flbl" for="s4">目前密碼</label><input id="s4" class="field" type="password"></div></div><div style="font-size:12px;color:#5F6470">我們會寄確認連結到新信箱，打開後才會更換。</div><div><button class="btn t sm" type="button">更換 Email</button></div>')}
      {card("密碼", '<div style="display:grid;grid-template-columns:1fr 1fr;gap:14px"><div><label class="flbl" for="s5">目前密碼</label><input id="s5" class="field" type="password"><div style="font-size:12px;color:#5F6470;margin-top:6px">只用 Google 或 GitHub 登入、還沒設過密碼可留空。</div></div><div><label class="flbl" for="s6">新密碼</label><input id="s6" class="field" type="password"><div style="font-size:12px;color:#5F6470;margin-top:6px">至少 8 個字元。更改後其他裝置會登出。</div></div></div><div><button class="btn t sm" type="button">更改密碼</button></div>')}
      {card("兩步驟驗證", '<div style="font-size:13px;color:#5F6470">登入時除了密碼，還要輸入驗證器 App 的 6 位數驗證碼。</div><span class="chip ok" style="align-self:flex-start">已啟用</span><span style="font-size:13px;color:#5F6470">剩 8 組備用碼</span><div style="display:grid;grid-template-columns:1fr 1fr;gap:14px"><div><label class="flbl" for="s7">目前密碼</label><input id="s7" class="field" type="password"></div><div><label class="flbl" for="s8">6 位數驗證碼或備用碼</label><input id="s8" class="field" type="text"></div></div><div><button class="btn t sm" type="button" style="color:#9C2318">停用</button></div>')}
      {card("登入裝置", '<div style="display:flex;justify-content:flex-end;margin-top:-44px"><button class="btn t sm" type="button">登出其他裝置</button></div><div style="display:flex;gap:12px;align-items:center;padding:12px 0;border-top:1px solid #E2E3E8"><div style="flex:1"><b>Chrome · macOS</b><div style="font-size:12px;color:#5F6470">1.2.x.x · 最後使用 今天 16:02</div></div><span class="chip ok">目前這台</span></div><div style="display:flex;gap:12px;align-items:center;padding:12px 0;border-top:1px solid #E2E3E8"><div style="flex:1"><b>Safari · iPhone</b><div style="font-size:12px;color:#5F6470">1.3.x.x · 最後使用 10/20 08:00</div></div><span class="chip warn">新裝置</span><button class="btn t sm" type="button">登出這台</button></div>')}''')))

# ---------- 會員中心：通知 ----------
def nrow(title, when, unread=False):
    dot = '<span style="display:inline-block;width:8px;height:8px;margin-left:8px;background:#E8285B;border-radius:0 3px 0 3px"></span>' if unread else ''
    btn = '<button class="btn t sm" type="button">標為已讀</button>' if unread else ''
    return f'<div style="display:flex;gap:14px;align-items:flex-start;padding:14px 0;border-top:1px solid #E2E3E8"><div style="flex:1"><b>{title}</b>{dot}<div style="font-size:12px;color:#5F6470">{when}</div></div>{btn}</div>'
pages["AccountNotifications"] = ("前台會員通知", 1000, front("shop", acct_page("notifications", f'''
      <div style="display:flex;align-items:flex-end;justify-content:space-between;gap:16px"><div style="display:flex;flex-direction:column;gap:12px"><span class="stripe"></span><h1 class="d" style="font-size:44px;letter-spacing:-.035em">通知</h1></div><button class="btn t sm" type="button">全部標為已讀</button></div>
      <div style="background:#DCE6FF;color:#1A3F9E;border-radius:0 12px 0 12px;padding:12px 16px;font-size:13px">訂單確認、付款與出貨通知一律寄到你的 Email，無法關閉。</div>
      <div>
        {nrow("有新裝置登入：Safari · iPhone","今天 08:00",True)}
        {nrow("密碼已變更","10/20 21:14")}
      </div>
      <button class="btn t sm" type="button" style="align-self:center">載入更多</button>''')))

# ---------- 綠界付款中繼頁（獨立頁，不載入主題 layout 與外部字型） ----------
from gen import MARK
HANDOFF_CSS = CSS + """
.hp-row{display:flex;gap:40px;align-items:flex-start;justify-content:center;flex-wrap:wrap;padding:48px 40px 72px;background:#F5F5F7;min-height:100%;box-sizing:border-box}
.hp-col{display:flex;flex-direction:column;gap:12px;align-items:flex-start}
.hp-cap{font-size:12px;font-weight:700;letter-spacing:.08em;color:#5F6470;text-transform:uppercase}
.hp{box-sizing:border-box;display:grid;place-items:center;padding:40px 24px;font-family:system-ui,-apple-system,"PingFang TC","Microsoft JhengHei",sans-serif;background:#fff;color:#15161A;box-shadow:0 0 0 1px #E2E3E8}
.hp.dark{background:#141519;color:#ECEDF1;box-shadow:0 0 0 1px #2A2B31}
.hp-card{width:100%;max-width:440px;display:flex;flex-direction:column;gap:16px}
.hp-brand{display:flex;align-items:center;gap:10px;font-weight:800;font-size:17px}
.hp-brand .mark{width:26px;height:30px;object-fit:contain}
.hp h1{margin:0;font-size:32px;line-height:1.1;letter-spacing:-.03em;font-weight:800}
.hp p{margin:0;line-height:1.7;font-size:15px}
.hp .muted{color:#5F6470}.hp.dark .muted{color:#9A9FAB}
.hp-bar{height:4px;background:#EDEEF1;border-radius:0 2px 0 2px;overflow:hidden}.hp.dark .hp-bar{background:#2A2B31}
.hp-bar i{display:block;width:45%;height:100%;background:#E8285B}
.hp-sum{display:grid;grid-template-columns:auto 1fr;gap:4px 16px;padding:14px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:14px}.hp.dark .hp-sum{background:#1E2026}
.hp-sum b{text-align:right}
.hp-btn{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:48px;padding:0 22px;font:inherit;font-weight:700;font-size:15px;border:0;border-radius:0 10px 0 10px;cursor:pointer;background:#E8285B;color:#fff;width:100%;box-sizing:border-box}
.hp-link{font-size:14px;color:#2F6BFF;text-decoration:underline;text-underline-offset:3px}.hp.dark .hp-link{color:#8FB0FF}
.hp-note{display:flex;gap:10px;align-items:flex-start;font-size:13px;line-height:1.6}
.hp-err{padding:12px 14px;border-radius:0 10px 0 10px;background:#FBE1DE;color:#9C2318;font-size:14px;line-height:1.6}
.hp-spec{max-width:420px;font-size:13px;line-height:1.7;color:#3A3D46;background:#fff;border-radius:0 16px 0 16px;padding:20px;box-shadow:inset 0 0 0 1px #E2E3E8}
.hp-spec code{font-size:12px;background:#F3F3F5;padding:1px 5px;border-radius:0 4px 0 4px}
"""
def handoff(dark, en, w, h, state="go"):
    ink = "#ECEDF1" if dark else "#15161A"
    t = (lambda zh, e: e if en else zh)
    head = f'<div class="hp-brand">{MARK.format(ink=ink)}{t("榛果繽紛樂","Hazel Jelly")}</div><span class="stripe"></span>'
    summary = f'<div class="hp-sum"><span class="muted">{t("訂單","Order")}</span><b class="mono">JL-2026-0417</b><span class="muted">{t("金額","Amount")}</span><b class="mono">NT$ 880</b><span class="muted">{t("付款方式","Method")}</span><b>{t("信用卡","Credit card")}</b></div>'
    if state == "go":
        body = f"""<div class="lbl">{t("安全付款","Secure payment")}</div>
        <h1>{t("正在前往綠界付款頁","Taking you to ECPay")}</h1>
        <div class="hp-bar" role="progressbar" aria-label="{t("前往付款頁","Redirecting")}"><i></i></div>
        <p class="muted">{t("這一頁不會收集卡號。卡片資料只在綠界的付款頁輸入，付款完成後會回到本站顯示結果。","This page never asks for your card. You enter card details only on the ECPay page, then return here for the result.")}</p>
        {summary}
        <button class="hp-btn" type="submit">{t("沒有自動跳轉？繼續前往綠界","Not redirected? Continue to ECPay")}</button>
        <a class="hp-link" href="OrderDone.dc.html">{I("arrow-l",14)}{t("回到訂單","Back to your order")}</a>"""
    else:
        body = f"""<div class="lbl">{t("安全付款","Secure payment")}</div>
        <h1>{t("暫時無法前往付款頁","Can't reach the payment page")}</h1>
        <div class="hp-err" role="alert">{t("付款服務沒有回應，訂單沒有被扣款，商品仍保留到 10/27 16:02。","The payment service did not respond. You were not charged; your items stay reserved until 10/27 16:02.")}</div>
        {summary}
        <button class="hp-btn" type="button">{I("refresh",16)}{t("再試一次","Try again")}</button>
        <a class="hp-link" href="OrderDone.dc.html">{t("回到訂單換付款方式","Back to your order to change method")}</a>"""
    return f'<div class="hp{" dark" if dark else ""}" style="width:{w}px;min-height:{h}px"><div class="hp-card">{head}{body}</div></div>'

pages["ECPayHandoff"] = ("前台綠界付款中繼頁", 1500, f"""<div class="hp-row">
  <div class="hp-col"><span class="hp-cap">1280 · 淺色 · 繁中</span>{handoff(False, False, 640, 640)}</div>
  <div class="hp-col"><span class="hp-cap">390 · 深色 · English</span>{handoff(True, True, 390, 640)}</div>
  <div class="hp-col"><span class="hp-cap">390 · 淺色 · 連線失敗</span>{handoff(False, False, 390, 640, "fail")}</div>
  <div class="hp-spec">
    <b>實作對照</b><br>
    獨立頁，不載入主題 layout、外部字型或圖片；只用 inline style，符合目前 <code>script-src 'unsafe-inline'; style-src 'unsafe-inline'</code> 的 CSP。<br>
    顏色：底 <code>#FFFFFF</code>／深色 <code>#141519</code>，字 <code>#15161A</code>／<code>#ECEDF1</code>，說明 <code>#5F6470</code>／<code>#9A9FAB</code>，主要按鈕 <code>#E8285B</code> 白字，連結 <code>#2F6BFF</code>／<code>#8FB0FF</code>；依 <code>prefers-color-scheme</code> 切換。<br>
    形狀：按鈕與摘要只圓右上與左下角（<code>0 10px 0 10px</code>），品牌記號用果凍丁 SVG，上方三色斜線。<br>
    行為：載入後自動送出表單；「繼續前往綠界」按鈕永遠可見，供停用 JavaScript 或自動送出被擋時使用；「回到訂單」連回 <code>/checkout/done/&lt;number&gt;?t=…</code>。進度條在「減少動態」時不動。<br>
    語言：依訂單或請求語系輸出完整中文或英文，不並排。
  </div>
</div>""")

if __name__ == "__main__":
    for name,(title,h,html) in pages.items():
        rendered = doc(title, CSS if name != "ECPayHandoff" else HANDOFF_CSS, html, 1440, h)
        rendered = "\n".join(line.rstrip() for line in rendered.splitlines()) + "\n"
        open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(rendered); print("wrote",name,h)
