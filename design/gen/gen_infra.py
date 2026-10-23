import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, ADMIN_CSS, OUT, admin

CSS = ADMIN_CSS + '''
.sub a{display:flex;align-items:center;gap:8px;min-height:34px;padding:0 10px;border-radius:0 9px 0 9px;color:#5F6470;font-weight:600;font-size:13px}
.sub a.on{background:#F3F3F5;color:#15161A}
.opt{display:flex;gap:10px;align-items:flex-start;padding:10px 12px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8;cursor:pointer;font-size:13px}
.opt.on{box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA}
.opt .h{font-size:12px;color:#5F6470;font-weight:400;margin-top:2px}
.bar{height:6px;background:#EDEEF1;border-radius:3px;overflow:hidden}.bar span{display:block;height:6px}
.q{display:grid;grid-template-columns:minmax(0,1fr) 44px 52px 44px 96px;gap:12px;align-items:center;padding:9px 0;border-top:1px solid #EEEFF2;font-size:13px}
.q .h{font-size:11.5px;color:#5F6470;font-weight:400}
.q .n{font-family:"Bricolage Grotesque",sans-serif;font-weight:800;font-size:15px;text-align:right}
.kv{font:12px ui-monospace,"SF Mono",Menlo,Consolas,monospace;color:#3A3D45}
'''
def subnav():
    return f'''<nav class="sub" style="display:flex;flex-direction:column;gap:2px;position:sticky;top:16px" aria-label="設定分類">
        <a href="Settings.dc.html">{I("globe",14)}網站</a><a href="Settings.dc.html">{I("key",14)}登入與 OAuth</a><a href="Settings.dc.html">{I("card",14)}金流</a><a href="Settings.dc.html">{I("truck",14)}運送</a><a href="Settings.dc.html">{I("mail",14)}郵件</a><a class="on" href="Infra.dc.html">{I("db",14)}快取與佇列</a><a href="Backup.dc.html">{I("db",14)}備份與匯入</a><a href="Users.dc.html">{I("users",14)}使用者</a><a href="Update.dc.html">{I("download",14)}更新</a><a href="Logs.dc.html">{I("terminal",14)}系統日誌</a>
      </nav>'''

main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr);gap:32px;align-items:start">
      {subnav()}
      <section style="display:flex;flex-direction:column;gap:24px;max-width:1040px">
        <div style="display:flex;align-items:center;gap:14px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;line-height:1.5">{I("check",18)}<span><b>不接任何外部服務也能跑。</b>預設快取在記憶體、佇列放在 PostgreSQL，單機夠用。網站變大、要開多台或要讓背景工作不卡網站時，再在這裡接 Redis。</span></div>

        <div style="display:grid;grid-template-columns:1fr 1fr;gap:20px;align-items:start">
          <!-- 快取 -->
          <div class="card" style="display:flex;flex-direction:column;gap:14px">
            <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">快取</b><span class="chip ok" style="margin-left:auto">Redis · 已連線 · 0.4 ms</span></div>
            <div style="display:flex;flex-direction:column;gap:6px">
              <label class="opt"><span class="cb" style="margin-top:2px"></span><span><b>記憶體（預設）</b><div class="h">不用設定。多台主機時各自一份，彼此不同步。</div></span></label>
              <label class="opt on"><span class="cb on" style="margin-top:2px"></span><span><b>Redis / Valkey</b><div class="h">頁面、物件、工作階段共用一份，多台主機也一致。</div></span></label>
              <label class="opt"><span class="cb" style="margin-top:2px"></span><span><b>Memcached</b><div class="h">只做物件快取；工作階段會留在 PostgreSQL。</div></span></label>
            </div>
            <div><span class="lbl">連線</span><div style="display:flex;gap:8px;align-items:center"><input class="ul" type="text" value="redis://cache.internal:6379/0" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><button class="btn t s" type="button">{I("refresh",14)}測試</button></div><div style="font-size:12px;color:#5F6470;margin-top:6px">密碼放環境變數 <span class="kbd">JELLY_CACHE_URL</span> 也可以，這裡會顯示「由環境變數設定」而不是明碼。</div></div>
            <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:10px">
              <div style="background:#F5F5F7;border-radius:0 10px 0 10px;padding:10px 12px"><span class="lbl">命中率（24h）</span><b class="mono" style="font-size:22px;display:block">94%</b><div class="bar" style="margin-top:6px"><span style="width:94%;background:#13C2A3"></span></div></div>
              <div style="background:#F5F5F7;border-radius:0 10px 0 10px;padding:10px 12px"><span class="lbl">鍵數 / 記憶體</span><b class="mono" style="font-size:22px;display:block">8,412</b><span style="font-size:12px;color:#5F6470">31 MB / 256 MB</span></div>
              <div style="background:#F5F5F7;border-radius:0 10px 0 10px;padding:10px 12px"><span class="lbl">省下的查詢</span><b class="mono" style="font-size:22px;display:block">1.2M</b><span style="font-size:12px;color:#5F6470">今天</span></div>
            </div>
            <div>
              <span class="lbl">快取什麼、放多久</span>
              <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>前台頁面</label><div class="h">發布、改主題、改選單時自動清。</div></div><div class="c"><div class="seg"><span>關</span><span>1 分</span><span class="on">10 分</span><span>1 小時</span></div></div></div>
              <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>物件（設定、選單、分類樹、商品）</label></div><div class="c"><div class="seg"><span>關</span><span class="on">自動</span></div></div></div>
              <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>工作階段（登入狀態、購物車）</label><div class="h">改後端時現有登入會失效。</div></div><div class="c"><div class="seg"><span>PostgreSQL</span><span class="on">Redis</span></div></div></div>
              <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>限流計數（登入失敗、API）</label></div><div class="c"><div class="seg"><span>記憶體</span><span class="on">Redis</span></div></div></div>
            </div>
            <div style="display:flex;gap:8px"><button class="btn t" type="button">{I("trash")}清除頁面快取</button><button class="btn t" type="button">{I("trash")}全部清除</button><span style="font-size:12px;color:#5F6470;align-self:center;margin-left:auto">上次清除：今天 09:12（發布文章）</span></div>
          </div>

          <!-- 佇列 -->
          <div class="card" style="display:flex;flex-direction:column;gap:14px">
            <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">背景工作佇列</b><span class="chip ok" style="margin-left:auto">PostgreSQL · 2 個工作者</span></div>
            <div style="display:flex;flex-direction:column;gap:6px">
              <label class="opt on"><span class="cb on" style="margin-top:2px"></span><span><b>PostgreSQL（預設）</b><div class="h">用資料庫當佇列，不用多裝東西。每秒幾十件以內很夠。</div></span></label>
              <label class="opt"><span class="cb" style="margin-top:2px"></span><span><b>Redis Streams</b><div class="h">跟上面的 Redis 共用連線。量大、要多台工作者時用。</div></span></label>
              <label class="opt"><span class="cb" style="margin-top:2px"></span><span><b>NATS JetStream</b><div class="h">要和別的系統互通事件時用；RabbitMQ、Kafka 由外掛提供。</div></span></label>
            </div>
            <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>工作者數量</label><div class="h">同一個程序裡跑；設 0 則這台只收件不處理（多台時給專門的那台跑）。</div></div><div class="c"><div class="seg"><span>0</span><span>1</span><span class="on">2</span><span>4</span><span>8</span></div></div></div>
            <div>
              <div class="q" style="border-top:0;font-size:11px;font-weight:700;color:#5F6470;letter-spacing:.06em;text-transform:uppercase"><span>佇列</span><span style="text-align:right">等待</span><span style="text-align:right">處理中</span><span style="text-align:right">失敗</span><span></span></div>
              <div class="q"><span><b>郵件</b><div class="h">訂單通知、邀請</div></span><span class="n">3</span><span class="n">1</span><span class="n" style="color:#B9BCC6">0</span><span><span class="chip ok">正常</span></span></div>
              <div class="q"><span><b>媒體</b><div class="h">縮圖、WebP 轉檔</div></span><span class="n">41</span><span class="n">2</span><span class="n" style="color:#B9BCC6">0</span><span><span class="chip ok">正常</span></span></div>
              <div class="q"><span><b>Webhook</b><div class="h">金流回呼、外掛事件</div></span><span class="n">0</span><span class="n">0</span><span class="n" style="color:#9C2318">2</span><span><button class="btn t s" type="button">{I("refresh",12)}重試 2 件</button></span></div>
              <div class="q"><span><b>匯入</b><div class="h">WordPress、附件下載</div></span><span class="n">124</span><span class="n">1</span><span class="n" style="color:#B9BCC6">0</span><span><span class="chip info">進行中</span></span></div>
              <div class="q"><span><b>排程</b><div class="h">排程發布、備份、檢查更新</div></span><span class="n">—</span><span class="n">—</span><span class="n" style="color:#B9BCC6">0</span><span style="font-size:11.5px;color:#5F6470">下一個 03:00</span></div>
            </div>
            <div style="background:#FBE1DE;color:#9C2318;border-radius:0 10px 0 10px;padding:10px 12px;font-size:12.5px;display:flex;gap:10px;align-items:flex-start">{I("alert",16)}<div><b>2 件 Webhook 失敗，已重試 3 次。</b> 綠界回呼 order=JL-2026-0417 逾時。失敗的工作保留 7 天，可以在<a href="Logs.dc.html" style="color:#9C2318;text-decoration:underline">日誌</a>看每次的錯誤。</div></div>
            <div style="display:flex;gap:8px"><button class="btn t" type="button">{I("pause")}暫停所有工作者</button><a class="btn t" href="Logs.dc.html">{I("terminal")}失敗紀錄</a><span style="font-size:12px;color:#5F6470;align-self:center;margin-left:auto">今天處理 2,318 件 · 平均等待 0.8 秒</span></div>
          </div>
        </div>
        <div style="font-size:12.5px;color:#5F6470;line-height:1.7;display:flex;gap:10px;align-items:flex-start">{I("alert",16)}<span>外掛也走同一套：外掛可以註冊自己的佇列和快取命名空間，但看不到別的外掛的資料。切換後端時會先把等待中的工作搬過去，再切，不會掉件。</span></div>
      </section>
    </main>'''

html = admin("settings", ["系統","設定","快取與佇列"], f'<button class="btn t" type="button">{I("undo")}還原</button><button class="btn p" type="button">{I("save")}儲存</button>', main)
open(os.path.join(OUT, "Infra.dc.html"),"w",encoding="utf-8").write(doc("後台 · 快取與佇列", CSS, html, 1440, 1100)); print("wrote Infra")
