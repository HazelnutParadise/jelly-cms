import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, ADMIN_CSS, FRONT_CSS, OUT, admin, front, cubes

CSS = ADMIN_CSS + '''
.sub a{display:flex;align-items:center;gap:8px;min-height:34px;padding:0 10px;border-radius:0 9px 0 9px;color:#5F6470;font-weight:600;font-size:13px}
.sub a.on{background:#F3F3F5;color:#15161A}
.nrow{display:grid;grid-template-columns:36px minmax(0,1fr) auto;gap:12px;padding:12px 10px;border-top:1px solid #EEEFF2;align-items:flex-start}
.nrow.un{background:#F8F9FF}.nrow .t{font-size:12px;color:#5F6470;white-space:nowrap}
.app{display:flex;flex-direction:column;gap:10px;padding:14px;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px;background:#fff}
.app .ico{width:44px;height:44px;border-radius:0 12px 0 12px;display:grid;place-items:center;color:#fff}
.app h3{margin:0;font-size:14px}.app p{margin:0;font-size:12px;color:#5F6470;line-height:1.5}
.code{font:12px/1.65 ui-monospace,"SF Mono",Menlo,Consolas,monospace;background:#F6F7F9;border:1px solid #E6E7EB;border-radius:4px;padding:10px 12px;white-space:pre;color:#15161A;overflow:auto}
.tab{display:flex;gap:2px;border-bottom:1px solid #E6E7EB}
.tab span{padding:10px 14px;font-size:13px;font-weight:700;color:#5F6470;border-bottom:2px solid transparent;margin-bottom:-1px}
.tab span.on{color:#15161A;border-color:#E8285B}
.pol{display:flex;align-items:center;gap:12px;padding:12px;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 12px 0 12px}
.pol .ico{width:36px;height:36px;border-radius:0 10px 0 10px;display:grid;place-items:center;color:#fff;flex:0 0 auto}
'''

def settings_nav(on):
    items=[("globe","網站","Settings.dc.html"),("list","閱讀與網址","SettingsRoutes.dc.html"),("key","登入與註冊","Settings.dc.html"),("comment","留言","Settings.dc.html"),("card","金流","Settings.dc.html"),("truck","運送","Settings.dc.html"),("mail","郵件與通知","Settings.dc.html"),("db","快取與佇列","Infra.dc.html"),("terminal","程式碼注入","SettingsRoutes.dc.html"),("download","更新","Update.dc.html")]
    return '<nav class="sub" style="display:flex;flex-direction:column;gap:2px;position:sticky;top:16px" aria-label="設定分類">'+"".join(f'<a href="{h}"{" class=\"on\"" if l==on else ""}>{I(ic,14)}{l}</a>' for ic,l,h in items)+'</nav>'

def profile_nav(on):
    items=[("user","基本資料"),("mail","通知"),("key","API 權杖"),("lock","密碼與安全"),("globe","登入裝置"),("link","第三方帳號")]
    return '<nav class="sub" style="display:flex;flex-direction:column;gap:2px;position:sticky;top:16px">'+"".join(f'<a href="Profile.dc.html"{" class=\"on\"" if l==on else ""}>{I(ic,14)}{l}</a>' for ic,l in items)+'</nav>'

B = {}

# ---------- 通知中心 ----------
def nrow(ic,color,title,body,t,un=False,actions=""):
    return f'<div class="nrow{" un" if un else ""}"><span style="width:36px;height:36px;background:{color};color:#fff;display:grid;place-items:center;border-radius:0 10px 0 10px">{I(ic,16)}</span><div><b style="font-size:13.5px">{title}</b><div style="font-size:13px;color:#3A3D45;margin-top:2px;line-height:1.5">{body}</div>{actions}</div><span class="t">{t}</span></div>'
B["Notifications"] = ("後台 · 通知中心", CSS, 1440, 1000, admin("", ["通知"],
  f'<button class="btn t" type="button">{I("check")}全部標為已讀</button><a class="btn t" href="Notifications.dc.html">{I("settings")}通知設定</a>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 380px;gap:24px;align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:8px"><div class="seg"><span class="on">全部 <b>12</b></span><span>未讀 <b style="color:#E8285B">4</b></span><span>留言</span><span>訂單</span><span>系統</span><span>安全</span></div></div>
      {nrow("comment","#F5B800;color:#3A2E00","2 則新留言待審","阿凱、Mei 在「零失敗焦糖布丁」留言。","5 分鐘前",True,f'<div style="display:flex;gap:6px;margin-top:8px"><a class="btn t s" href="Comments.dc.html">{I("eye",12)}去審核</a><button class="btn x s" type="button">{I("check",12)}全部核准</button></div>')}
      {nrow("cart","#E8285B","新訂單 JL-2026-0417 · NT$ 880","Ting 下單，信用卡已付款。","16:02",True,f'<div style="display:flex;gap:6px;margin-top:8px"><a class="btn t s" href="OrderDetail.dc.html">{I("arrow-r",12)}看訂單</a></div>')}
      {nrow("alert","#9C2318","外掛「SEO Boost」載入失敗","v1.4.0 與目前版本不相容，已自動停用。網站照常運作。","15:40",True,f'<div style="display:flex;gap:6px;margin-top:8px"><a class="btn t s" href="PluginDetail.dc.html">{I("plug",12)}看外掛</a><a class="btn x s" href="Logs.dc.html">{I("terminal",12)}日誌</a></div>')}
      {nrow("globe","#2F6BFF","新裝置登入：Firefox · Windows · 台北","不是你？立刻登出它並改密碼。","3 天前",True,f'<div style="display:flex;gap:6px;margin-top:8px"><a class="btn t s" href="Profile.dc.html">{I("lock",12)}登入裝置</a></div>')}
      {nrow("download","#15161A","Jelly 0.9.4 可以更新","新增留言管理與回收桶。這台是容器，要換映像。","昨天")}
      {nrow("db","#13C2A3","每日備份完成 · 412 MB","已同步到 R2。","今天 03:00")}
      {nrow("check","#13C2A3","排程文章已發布","「開店第 200 天」在 09:00 上線。","今天 09:00")}
      {nrow("users","#5F6470","Jamie 接受邀請，已加入為編輯","","昨天")}
      <div style="text-align:center;padding:14px;font-size:12px;color:#5F6470">往下捲載入更早的 · 通知保留 90 天</div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">我要收到什麼</b></div>
      <table style="font-size:12.5px"><thead><tr><th>事件</th><th style="text-align:center">站內</th><th style="text-align:center">Email</th><th style="text-align:center">Webhook</th></tr></thead><tbody>
        <tr><td>新留言待審</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
        <tr><td>有人回覆我的留言</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
        <tr><td>新訂單</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td></tr>
        <tr><td>付款失敗、退款</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td></tr>
        <tr><td>新裝置登入</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
        <tr><td>外掛失敗、更新可用</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
        <tr><td>備份結果</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
        <tr><td>排程發布</td><td style="text-align:center"><span class="cb on"></span></td><td style="text-align:center"><span class="cb"></span></td><td style="text-align:center"><span class="cb"></span></td></tr>
      </tbody></table>
      <div><span class="lbl">Webhook（送 JSON 到）</span><input class="ul" type="text" value="https://hooks.slack.com/services/…" style="font-family:ui-monospace,Menlo,monospace;font-size:12px"><div style="font-size:12px;color:#5F6470;margin-top:6px">Slack、Discord、LINE Notify、Telegram 的格式由通知外掛提供；這裡是通用 JSON。</div></div>
      <div style="font-size:12px;color:#5F6470;line-height:1.6">Email 走佇列寄出；SMTP 在「設定 › 郵件與通知」。靜音時段 23:00–08:00 只累積不推播。</div>
      <button class="btn p" type="button" style="align-self:flex-start">{I("save")}儲存</button>
    </aside>
  </main>'''))

# ---------- 個人帳號 › API 權杖 ----------
B["Tokens"] = ("後台 · 個人帳號 · API 權杖", CSS, 1440, 1000, admin("", ["個人帳號","API 權杖"],
  f'<button class="btn p" type="button">{I("plus")}建立權杖</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr);gap:32px;align-items:start">
    {profile_nav("API 權杖")}
    <section style="display:flex;flex-direction:column;gap:20px;max-width:980px">
      <div style="display:flex;align-items:center;gap:14px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;line-height:1.5">{I("key",18)}<span>權杖用來呼叫 REST API，不用帳密。權限用角色範本限制，到期自動失效。完整 API 文件在 <a href="#">/api/docs</a>（OpenAPI）。</span></div>
      <table>
        <thead><tr><th>名稱</th><th>權限</th><th>到期</th><th>最後使用</th><th>狀態</th><th></th></tr></thead>
        <tbody>
          <tr><td><b>Obsidian 發文</b><div style="font-size:12px;color:#5F6470">jly_••••••••••••a9f2 · 從 Obsidian 外掛發草稿</div></td><td><span class="chip info">作者</span></td><td>2027-04-01</td><td>今天 14:20 · 1.160.x.x</td><td><span class="chip ok">有效</span></td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="撤銷">{I("pause")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><b>n8n 自動化</b><div style="font-size:12px;color:#5F6470">jly_••••••••••••7c10 · 新訂單同步到試算表</div></td><td><span class="chip info">訂單唯讀</span> <span class="chip info">Webhook</span></td><td>永不</td><td>16:03</td><td><span class="chip ok">有效</span></td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="撤銷">{I("pause")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><b>舊的 CI</b><div style="font-size:12px;color:#5F6470">jly_••••••••••••02be</div></td><td><span class="chip bad">管理員</span></td><td style="color:#9C2318">已過期 · 10/01</td><td>3 個月前</td><td><span class="chip">已撤銷</span></td><td class="act"><button class="btn x s" type="button">{I("undo",12)}恢復</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
        </tbody>
      </table>
    </section>
  </main>''',
  extra=f'''<div class="sheet" style="width:460px">
    <div style="display:flex;align-items:center;gap:10px;padding:16px 20px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">建立權杖</b><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x")}</button></div>
    <div style="padding:16px 20px;display:flex;flex-direction:column;gap:14px;flex:1;overflow:auto">
      <div><span class="lbl">名稱</span><input class="ul" type="text" value="Raycast 快速發文"></div>
      <div><span class="lbl">用途（可選）</span><input class="ul" type="text" placeholder="給未來的你看的"></div>
      <div><span class="lbl">到期</span><div class="seg" style="margin-top:6px"><span>7 天</span><span>30 天</span><span class="on">90 天</span><span>1 年</span><span>永不</span></div></div>
      <div><span class="lbl">權限（可多選，取聯集）</span>
        <div style="display:flex;flex-direction:column;gap:6px;margin-top:8px">
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA"><span class="cb on" style="margin-top:2px"></span><span><b>作者</b><div style="font-size:12px;color:#5F6470">建立與編輯自己的文章、上傳附件。</div></span></label>
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8"><span class="cb" style="margin-top:2px"></span><span><b>內容唯讀</b><div style="font-size:12px;color:#5F6470">讀文章、頁面、分類，含草稿。</div></span></label>
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8"><span class="cb" style="margin-top:2px"></span><span><b>訂單唯讀</b></span></label>
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8"><span class="cb" style="margin-top:2px"></span><span><b>管理員</b><div style="font-size:12px;color:#9C2318">跟你本人一樣大。外洩等於交出整站。</div></span></label>
        </div></div>
      <div style="background:#15161A;color:#fff;border-radius:0 12px 0 12px;padding:14px;display:flex;flex-direction:column;gap:8px">
        <span class="lbl" style="color:#9A9FAB">建立後只會顯示這一次</span>
        <div style="display:flex;align-items:center;gap:8px"><code style="font:12.5px ui-monospace,Menlo,monospace;flex:1;word-break:break-all">jly_8Kq2vN…（建立後出現）</code><button class="btn t s i" type="button" aria-label="複製" disabled style="opacity:.5">{I("copy",14)}</button></div>
        <div style="font-size:12px;color:#C9CBD3">請求時放在 <span class="kbd" style="background:#2A2B31;color:#C9CBD3">Authorization: Bearer …</span></div>
      </div>
    </div>
    <div style="padding:12px 20px;border-top:1px solid #EEEFF2;display:flex;gap:8px"><button class="btn p" type="button">{I("key")}建立</button><button class="btn t" type="button">{I("x")}取消</button></div>
  </div>'''))

# ---------- 附件 › 儲存策略與分組 ----------
B["MediaPolicies"] = ("後台 · 附件 · 儲存策略與分組", CSS, 1440, 1000, admin("media", ["內容","附件","儲存策略"],
  f'<a class="btn t" href="Media.dc.html">{I("arrow-l")}回附件庫</a><button class="btn p" type="button">{I("plus")}新增策略</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 380px;gap:24px;align-items:start">
    <section style="display:flex;flex-direction:column;gap:24px">
      <div>
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:10px"><span class="lbl">儲存策略</span><span style="font-size:12px;color:#5F6470">檔案實際放哪、網址長怎樣。上傳時可選，預設用第一個。</span></div>
        <div style="display:flex;flex-direction:column;gap:8px">
          <div class="pol"><span class="ico" style="background:#15161A">{I("db",16)}</span><div style="flex:1"><b>本機磁碟</b> <span class="chip ok" style="margin-left:6px">預設</span><div style="font-size:12px;color:#5F6470;font-family:ui-monospace,Menlo,monospace">/data/uploads/{{year}}/{{month}} → https://example.com/uploads/…</div></div><span style="font-size:12px;color:#5F6470">1,284 個檔 · 2.1 GB</span><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="更多">{I("more")}</button></div>
          <div class="pol"><span class="ico" style="background:#2F6BFF">{I("globe",16)}</span><div style="flex:1"><b>Cloudflare R2 · 圖片</b> <span class="chip info" style="margin-left:6px">S3 相容</span><div style="font-size:12px;color:#5F6470;font-family:ui-monospace,Menlo,monospace">bucket jelly-media → https://img.example.com/…</div></div><span style="font-size:12px;color:#5F6470">312 個檔 · 1.9 GB</span><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="更多">{I("more")}</button></div>
          <div class="pol" style="box-shadow:inset 0 0 0 1px #E2E3E8;opacity:.75"><span class="ico" style="background:#9A9FAB">{I("plug",16)}</span><div style="flex:1"><b>更多後端由外掛提供</b><div style="font-size:12px;color:#5F6470">Google Drive、WebDAV、圖床服務… 裝了外掛就會出現在「新增策略」。</div></div><a class="btn t s" href="AppStore.dc.html">{I("search",12)}找外掛</a></div>
        </div>
      </div>
      <div>
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:10px"><span class="lbl">分組</span><span style="font-size:12px;color:#5F6470">只是整理用，不影響檔案位置和網址。</span><button class="btn t s" type="button" style="margin-left:auto">{I("plus",12)}新增分組</button></div>
        <table><thead><tr><th>分組</th><th>檔案</th><th>最近加入</th><th></th></tr></thead><tbody>
          <tr><td><b>文章配圖</b></td><td class="mono">842</td><td style="color:#5F6470">今天</td><td class="act"><button class="btn x s i" type="button" aria-label="改名">{I("edit")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><b>商品照</b></td><td class="mono">206</td><td style="color:#5F6470">昨天</td><td class="act"><button class="btn x s i" type="button" aria-label="改名">{I("edit")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><b>WordPress 匯入</b></td><td class="mono">312</td><td style="color:#5F6470">10/20</td><td class="act"><button class="btn x s i" type="button" aria-label="改名">{I("edit")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td style="color:#5F6470">未分組</td><td class="mono">236</td><td style="color:#5F6470">—</td><td></td></tr>
        </tbody></table>
        <div style="font-size:12px;color:#5F6470;margin-top:8px">刪分組時可以選「檔案移到未分組」或「連檔案一起刪」，後者會先進回收桶。</div>
      </div>
      <div>
        <span class="lbl">上傳處理</span>
        <div class="srow"><div><label>自動轉 WebP 並保留原檔</label><div class="h">文章裡引用 WebP，原檔留著給下載。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>縮圖尺寸</label><div class="h">主題可以再要求別的尺寸。</div></div><div class="c"><span class="chip nd">320</span><span class="chip nd">768</span><span class="chip nd">1280</span><span class="chip nd">1920</span><button class="btn x s" type="button">{I("plus",12)}</button></div></div>
        <div class="srow"><div><label>移除照片的位置資訊（EXIF GPS）</label></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>單檔上限</label></div><div class="c"><div class="seg"><span>10 MB</span><span class="on">50 MB</span><span>200 MB</span><span>不限</span></div></div></div>
        <div class="srow"><div><label>從網址抓檔</label><div class="h">貼一個或多個網址，背景下載進所選策略。</div></div><div class="c"><button class="btn t s" type="button">{I("download",12)}從網址加入</button></div></div>
      </div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">編輯：Cloudflare R2 · 圖片</b></div>
      <div><span class="lbl">名稱</span><input class="ul" type="text" value="Cloudflare R2 · 圖片"></div>
      <div><span class="lbl">類型</span><div class="seg" style="margin-top:6px"><span>本機</span><span class="on">S3 相容</span></div></div>
      <div><span class="lbl">Endpoint</span><input class="ul" type="text" value="https://<account>.r2.cloudflarestorage.com" style="font-family:ui-monospace,Menlo,monospace;font-size:12px"></div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><div><span class="lbl">Bucket</span><input class="ul" type="text" value="jelly-media"></div><div><span class="lbl">Region</span><input class="ul" type="text" value="auto"></div></div>
      <div><span class="lbl">Access key / Secret</span><input class="ul" type="text" value="由環境變數 JELLY_S3_KEY 設定" disabled style="color:#5F6470"></div>
      <div><span class="lbl">對外網址（CDN）</span><input class="ul" type="text" value="https://img.example.com" style="font-family:ui-monospace,Menlo,monospace;font-size:12px"></div>
      <div><span class="lbl">路徑樣板</span><input class="ul" type="text" value="{{year}}/{{month}}/{{slug}}-{{short}}.{{ext}}" style="font-family:ui-monospace,Menlo,monospace;font-size:12px"></div>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">新上傳預設用這個<span class="tg off"></span></label>
      <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("save")}儲存</button><button class="btn t" type="button">{I("refresh")}測試連線</button><button class="btn x i" type="button" aria-label="刪除" style="margin-left:auto;color:#9C2318">{I("trash")}</button></div>
      <div style="font-size:12px;color:#5F6470;line-height:1.6">還有檔案的策略不能刪；要先把檔案搬到別的策略（會背景複製並改寫文章裡的網址）。</div>
    </aside>
  </main>'''))

# ---------- 應用市場 ----------
def app(color, ic, name, by, desc, meta, btn):
    return f'<div class="app"><div style="display:flex;gap:10px;align-items:center"><span class="ico" style="background:{color}">{I(ic,20)}</span><div><h3>{name}</h3><div style="font-size:11.5px;color:#5F6470">{by}</div></div></div><p>{desc}</p><div style="display:flex;align-items:center;gap:8px;margin-top:auto"><span style="font-size:11.5px;color:#5F6470">{meta}</span><span style="margin-left:auto">{btn}</span></div></div>'
B["AppStore"] = ("後台 · 應用市場", CSS, 1440, 1000, admin("plugins", ["外觀","應用市場"],
  f'<button class="btn t" type="button">{I("upload")}上傳 .zip</button><button class="btn t" type="button">{I("link")}從網址安裝</button>',
  f'''<main style="padding:16px 24px 32px;display:flex;flex-direction:column;gap:16px">
    <div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
      <div class="seg"><span class="on">{I("plug",13)}外掛 <b>86</b></span><span>{I("theme",13)}主題 <b>41</b></span><span>{I("refresh",13)}可更新 <b style="color:#E8285B">2</b></span><span>{I("check",13)}已安裝 <b>7</b></span></div>
      <label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:260px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="找外掛或主題" aria-label="搜尋" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label>
      <div class="seg"><span class="on">全部</span><span>官方</span><span>編輯器</span><span>儲存</span><span>搜尋</span><span>留言</span><span>SEO</span><span>通知</span><span>內容類型</span><span>商店</span></div>
    </div>
    <div style="display:flex;align-items:center;gap:12px;padding:10px 14px;background:#FCF0C8;color:#7A5200;border-radius:0 10px 0 10px;font-size:13px">{I("alert",16)}<span><b>2 個已安裝的外掛有新版本</b>：Meilisearch 搜尋 1.3 → 1.4、S3 儲存 2.0 → 2.1。更新前會自動備份它們的設定。</span><button class="btn s" type="button" style="margin-left:auto">{I("refresh",12)}全部更新</button></div>
    <div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px">
      {app("#2F6BFF","search","Meilisearch 搜尋","Jelly 官方 · 搜尋引擎","取代內建的 PostgreSQL 全文搜尋，中文分詞與錯字容忍更好。","v1.4 · 需要 Jelly ≥ 0.9","<button class='btn t s' type='button'>"+I("refresh",12)+"更新</button>")}
      {app("#13C2A3;color:#0B3A31","globe","S3 相容儲存","Jelly 官方 · 儲存後端","R2、S3、MinIO、B2 都能接，附 CDN 網址與路徑樣板。","v2.1 · 1.2k 站使用","<button class='btn t s' type='button'>"+I("refresh",12)+"更新</button>")}
      {app("#E8285B","comment","LINE Notify 通知","Jelly 官方 · 通知器","新訂單、新留言推到 LINE 群組。","v1.0","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#F5B800;color:#3A2E00","image","圖床：Lsky / Immich","社群 · 儲存後端","把自架圖床當附件後端。","v0.8 · 社群維護","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#15161A","star","動態（短貼文）","Jelly 官方 · 內容類型","像推文的短內容，有自己的時間軸頁與 RSS。","v1.2","<span class='chip ok'>已安裝</span>")}
      {app("#15161A","link","友站連結","Jelly 官方 · 內容類型","連結頁、分組、訪客投稿審核。","v1.1","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#15161A","image","相簿","Jelly 官方 · 內容類型","相簿分組、瀑布流、EXIF 顯示。","v1.0","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#2F6BFF","doc","文件站","Jelly 官方 · 內容類型","側欄樹狀目錄、版本切換、上一頁下一頁。","v0.9 預覽","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#9C2318","key","OAuth 登入：Google / GitHub","Jelly 官方 · 認證","後台與前台留言都能用第三方登入。","v1.5","<span class='chip ok'>已安裝</span>")}
      {app("#5F6470","terminal","Shiki 程式碼上色","Jelly 官方 · 編輯器","VS Code 同款高亮，伺服器端渲染。","v1.0","<span class='chip ok'>已安裝</span>")}
      {app("#5F6470","grid","Umami 統計","Jelly 官方 · 分析","在儀表板顯示 Umami 的流量。","v1.1","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
      {app("#E8285B","box","WooCommerce 匯入","Jelly 官方 · 遷移","把商品、訂單、顧客搬過來。","v0.5 預覽","<button class='btn s' type='button'>"+I("download",12)+"安裝</button>")}
    </div>
    <div style="display:flex;align-items:center;gap:10px;font-size:12px;color:#5F6470">{I("shield",14)}<span>官方外掛有簽章；社群外掛安裝前會顯示它要的權限（檔案、網路、資料表）。所有外掛跑在 QuickJS 隔離裡，掛了不影響網站。</span></div>
  </main>'''))

# ---------- 外掛詳情與擴充設定 ----------
B["PluginDetail"] = ("後台 · 外掛詳情", CSS, 1440, 1000, admin("plugins", ["外觀","外掛","Meilisearch 搜尋"],
  f'<label style="display:flex;align-items:center;gap:8px;font-size:13px;font-weight:600">啟用<span class="tg"></span></label><button class="btn t" type="button">{I("refresh")}重新載入</button><button class="btn p" type="button">{I("refresh")}更新到 1.4</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:24px;align-items:start">
    <section>
      <div style="display:flex;gap:14px;align-items:center;margin-bottom:14px"><span style="width:56px;height:56px;background:#2F6BFF;color:#fff;display:grid;place-items:center;border-radius:0 16px 0 16px">{I("search",26)}</span><div><h1 class="d" style="font-size:22px">Meilisearch 搜尋</h1><div style="font-size:12.5px;color:#5F6470">Jelly 官方 · v1.3.2 → <b style="color:#E8285B">1.4.0 可更新</b> · 執行中 · 記憶體 18 MB · 今天被呼叫 4,120 次</div></div></div>
      <div class="tab"><span>詳情</span><span class="on">設定</span><span>擴充設定</span><span>權限</span><span>日誌</span></div>
      <div style="padding-top:8px">
        <div class="srow"><div><label>Meilisearch 網址</label></div><div class="c"><input class="ul" type="text" value="http://search.internal:7700" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"></div></div>
        <div class="srow"><div><label>API 金鑰</label></div><div class="c"><input class="ul" type="text" value="由環境變數 MEILI_KEY 設定" disabled style="color:#5F6470"></div></div>
        <div class="srow"><div><label>索引內容</label></div><div class="c"><span class="chip nd">文章</span><span class="chip nd">頁面</span><span class="chip nd">商品</span><span class="chip nd" style="opacity:.5">留言</span></div></div>
        <div class="srow"><div><label>錯字容忍</label><div class="h">「焦塘布丁」也找得到焦糖布丁。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>重建索引</label><div class="h">上次 10/20 03:05 · 1,318 筆 · 12 秒</div></div><div class="c"><button class="btn t s" type="button">{I("refresh",12)}立刻重建</button></div></div>
        <div style="display:flex;gap:8px;margin-top:14px"><button class="btn p" type="button">{I("save")}儲存</button><button class="btn t" type="button">{I("refresh")}測試連線</button><button class="btn x" type="button" style="margin-left:auto">{I("undo")}重設為預設值</button></div>
      </div>
      <div style="margin-top:28px">
        <div class="tab" style="margin-bottom:8px"><span class="on" style="border-color:#2F6BFF">擴充設定 · 這個功能由誰提供</span></div>
        <p style="margin:0 0 10px;font-size:12.5px;color:#5F6470">同一個功能可能有好幾個外掛能做。這裡選現在用哪一個，切換不用重啟。</p>
        <table><thead><tr><th>擴充點</th><th>目前使用</th><th>其他可選</th></tr></thead><tbody>
          <tr><td><b>搜尋引擎</b></td><td><span class="chip info">Meilisearch 搜尋</span></td><td style="color:#5F6470">內建 PostgreSQL 全文搜尋</td></tr>
          <tr><td><b>附件儲存</b></td><td><span class="chip nd">本機</span> <span class="chip nd">S3 相容</span></td><td style="color:#5F6470">（可同時多個，在儲存策略選）</td></tr>
          <tr><td><b>留言元件</b></td><td><span class="chip info">內建</span></td><td style="color:#5F6470">Waline、Artalk（未安裝）</td></tr>
          <tr><td><b>通知器</b></td><td><span class="chip nd">Email</span> <span class="chip nd">Webhook</span> <span class="chip nd">LINE Notify</span></td><td style="color:#5F6470">（可同時多個）</td></tr>
          <tr><td><b>程式碼上色</b></td><td><span class="chip info">Shiki</span></td><td style="color:#5F6470">內建 highlight</td></tr>
        </tbody></table>
      </div>
    </section>
    <aside style="display:flex;flex-direction:column;gap:12px;position:sticky;top:16px">
      <div class="card" style="display:flex;flex-direction:column;gap:8px;font-size:13px">
        <div class="lbl">這個外掛要的權限</div>
        <div style="display:flex;align-items:center;gap:8px">{I("globe",14)}對外連線：search.internal:7700</div>
        <div style="display:flex;align-items:center;gap:8px">{I("doc",14)}讀：文章、頁面、商品（含草稿）</div>
        <div style="display:flex;align-items:center;gap:8px">{I("db",14)}自己的資料表：1 張（meili_sync）</div>
        <div style="display:flex;align-items:center;gap:8px">{I("grid",14)}後台：搜尋設定頁、儀表板小工具</div>
        <div style="display:flex;align-items:center;gap:8px;color:#9A9FAB">{I("x",14)}不能：寫文章、讀使用者、碰檔案系統</div>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:8px;font-size:13px">
        <div class="lbl">最近日誌</div>
        <div class="code" style="white-space:pre-wrap;font-size:11.5px">16:02 INFO  indexed post 1284 (41ms)
15:58 WARN  retry 1/3: timeout 3s
15:55 INFO  rebuild done · 1318 docs</div>
        <a href="Logs.dc.html" style="font-size:12px">在系統日誌裡篩選這個外掛</a>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:8px">
        <div class="lbl">移除</div>
        <button class="btn t s" type="button" style="justify-content:flex-start">{I("trash",12)}解除安裝，保留設定</button>
        <button class="btn danger s" type="button" style="justify-content:flex-start">{I("trash",12)}解除安裝並刪除設定與資料表</button>
        <div style="font-size:12px;color:#5F6470">解除後搜尋自動退回內建引擎。</div>
      </div>
    </aside>
  </main>'''))

# ---------- 設定 › 閱讀與網址 + 程式碼注入 ----------
B["SettingsRoutes"] = ("後台 · 設定 · 閱讀與網址、程式碼注入", CSS, 1440, 1300, admin("settings", ["系統","設定","閱讀與網址"],
  f'<button class="btn t" type="button">{I("undo")}還原</button><button class="btn p" type="button">{I("save")}儲存</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr);gap:32px;align-items:start">
    {settings_nav("閱讀與網址")}
    <section style="max-width:900px;display:flex;flex-direction:column;gap:28px">
      <div>
        <div class="lbl" style="margin-bottom:4px">網址結構</div>
        <div class="srow"><div><label>文章網址</label><div class="h">改了會自動幫舊網址建 301 轉址。</div></div><div class="c"><select class="ul"><option>/posts/{{slug}}</option><option>/{{year}}/{{month}}/{{slug}}</option><option>/{{year}}/{{slug}}</option><option>/{{category}}/{{slug}}</option><option>/archives/{{slug}}</option></select></div></div>
        <div class="srow"><div><label>網址別名怎麼產生</label></div><div class="c"><div class="seg"><span class="on">從標題（拼音／英文）</span><span>時間戳</span><span>短亂碼</span></div></div></div>
        <div class="srow"><div><label>分類 / 標籤 / 作者 / 月份 前綴</label></div><div class="c" style="gap:4px;font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><input class="ul" type="text" value="/c/" style="width:64px"><input class="ul" type="text" value="/t/" style="width:64px"><input class="ul" type="text" value="/author/" style="width:84px"><input class="ul" type="text" value="/archive/" style="width:90px"></div></div>
        <div class="srow"><div><label>預覽</label></div><div class="c" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;color:#5F6470;justify-content:flex-start;flex-direction:column;align-items:flex-start;gap:2px"><span>example.com/posts/caramel-pudding</span><span>example.com/c/recipes/desserts</span><span>example.com/t/gelatin</span></div></div>
      </div>
      <div>
        <div class="lbl" style="margin-bottom:4px">列表</div>
        <div class="srow"><div><label>首頁顯示</label></div><div class="c"><div class="seg"><span class="on">最新文章</span><span>指定頁面</span></div></div></div>
        <div class="srow"><div><label>每頁幾篇（首頁 / 分類標籤 / 作者 / 搜尋）</label></div><div class="c" style="gap:4px"><input class="ul" type="text" value="12" style="width:48px;text-align:center"><input class="ul" type="text" value="12" style="width:48px;text-align:center"><input class="ul" type="text" value="12" style="width:48px;text-align:center"><input class="ul" type="text" value="20" style="width:48px;text-align:center"></div></div>
        <div class="srow"><div><label>摘要</label><div class="h">沒手寫摘要時自動取幾個字。</div></div><div class="c"><input class="ul" type="text" value="110" style="width:64px;text-align:center"></div></div>
        <div class="srow"><div><label>RSS / Atom</label><div class="h">/feed.xml、每個分類與作者也有自己的 feed。</div></div><div class="c"><div class="seg"><span class="on">全文</span><span>摘要</span><span>關</span></div></div></div>
        <div class="srow"><div><label>Sitemap 與 robots.txt</label><div class="h">自動產生，變更後 ping 搜尋引擎（IndexNow）。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>讓搜尋引擎索引這個站</label><div class="h">關掉會加 noindex，測試站用。</div></div><div class="c"><span class="tg"></span></div></div>
      </div>
      <div>
        <div class="lbl" style="margin-bottom:4px">程式碼注入</div>
        <p style="margin:0 0 8px;font-size:12.5px;color:#5F6470">放統計、字型、驗證標籤。會原樣輸出，只有管理員能改；留言與外掛碰不到這裡。</p>
        <div class="srow" style="grid-template-columns:200px minmax(0,1fr)"><div><label>全站 &lt;head&gt;</label></div><div class="c"><textarea class="ul code" rows="3" style="width:100%;font-size:12px;text-align:left">&lt;script defer src="https://umami.example.com/script.js" data-website-id="…"&gt;&lt;/script&gt;</textarea></div></div>
        <div class="srow" style="grid-template-columns:200px minmax(0,1fr)"><div><label>文章與頁面 &lt;head&gt;</label></div><div class="c"><textarea class="ul code" rows="2" style="width:100%;font-size:12px;text-align:left" placeholder="只在內容頁輸出"></textarea></div></div>
        <div class="srow" style="grid-template-columns:200px minmax(0,1fr)"><div><label>頁尾 &lt;/body&gt; 前</label></div><div class="c"><textarea class="ul code" rows="2" style="width:100%;font-size:12px;text-align:left" placeholder="聊天小工具之類的"></textarea></div></div>
      </div>
      <div>
        <div class="lbl" style="margin-bottom:4px">維護模式</div>
        <div class="srow"><div><label>開啟維護模式</label><div class="h">前台顯示維護頁（503），登入的管理員照常看得到網站。</div></div><div class="c"><span class="tg off"></span></div></div>
        <div class="srow"><div><label>維護頁訊息</label></div><div class="c"><input class="ul" type="text" value="我們在升級，大概 10 分鐘。"></div></div>
      </div>
    </section>
  </main>'''))

# ---------- 前台 · 維護模式 ----------
B["Maintenance"] = ("前台 · 維護模式", FRONT_CSS + ".maintenance-art{position:absolute;inset:0;overflow:clip;pointer-events:none}@media(max-width:700px){.maintenance-art>div{left:auto!important;right:0;top:auto!important;bottom:5rem;transform:scale(.36);transform-origin:bottom right;opacity:.22!important}}", 1440, 900, f'''<div style="width:100%;min-height:100vh;display:flex;flex-direction:column;background:#15161A;color:#F2F3F7;position:relative;overflow:hidden">
  <div class="topline"></div>
  <div class="maintenance-art">{cubes(2.6, 900, 160, .95)}</div>
  <main style="flex:1;display:flex;flex-direction:column;justify-content:center;padding:0 80px;max-width:720px;gap:20px;position:relative">
    <span class="stripe"></span>
    <div class="lbl" style="color:#9A9FAB">503 · 維護中</div>
    <h1 class="d" style="font-size:72px;letter-spacing:-.04em;line-height:1">網站暫時維護中</h1>
    <p style="margin:0;font-size:18px;color:#C9CBD3;line-height:1.7;max-width:26em">網站正在維護，完成後會立即恢復。資料都還在，請稍後再試。</p>
    <div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap"><button class="btn" type="button" style="background:#2A2B31">{I("refresh")}重新整理</button><a class="btn t" href="Login.dc.html">{I("lock")}管理員登入</a><span style="font-size:13px;color:#9A9FAB">維護開始於 03:00 · 已過 4 分鐘</span></div>
  </main>
  <footer style="padding:24px 80px;font-size:13px;color:#5F6470;position:relative">搜尋引擎會收到 503 與 Retry-After，不會影響排名 · Powered by Jelly CMS</footer>
</div>''')

# ---------- 後台 · 折扣碼與行銷 ----------
B["Coupons"] = ("後台 · 折扣碼", CSS, 1440, 1000, admin("coupons", ["商店","折扣碼"],
  f'<button class="btn p" type="button">{I("plus")}新增折扣碼</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 380px;gap:24px;align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span class="on">進行中 <b>4</b></span><span>排程 1</span><span>已結束 8</span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="找代碼" aria-label="搜尋" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label></div>
      <table><thead><tr><th>代碼</th><th>優惠</th><th>條件</th><th>用量</th><th>期間</th><th></th></tr></thead><tbody>
        <tr><td><b class="mono" style="font-size:15px">MOON15</b><div style="font-size:12px;color:#5F6470">中秋預購</div></td><td>85 折</td><td style="color:#5F6470">禮盒分類 · 滿 1,000</td><td><b class="mono">41</b> / 300<div class="bar" style="height:4px;background:#EDEEF1;border-radius:2px;margin-top:4px;width:80px"><span style="display:block;width:14%;height:4px;background:#13C2A3"></span></div></td><td style="color:#5F6470">9/1 – 9/20</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="複製">{I("copy")}</button><button class="btn x s i" type="button" aria-label="停用">{I("pause")}</button></td></tr>
        <tr><td><b class="mono" style="font-size:15px">FREESHIP</b><div style="font-size:12px;color:#5F6470">免運</div></td><td>免運費</td><td style="color:#5F6470">滿 800 · 台灣本島</td><td><b class="mono">212</b> / 不限</td><td style="color:#5F6470">長期</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="複製">{I("copy")}</button><button class="btn x s i" type="button" aria-label="停用">{I("pause")}</button></td></tr>
        <tr><td><b class="mono" style="font-size:15px">HAZEL-xxxx</b><div style="font-size:12px;color:#5F6470">訂閱者專屬 · 一人一碼</div></td><td>NT$ 100</td><td style="color:#5F6470">每人一次 · 滿 500</td><td><b class="mono">38</b> / 120 組</td><td style="color:#5F6470">到 12/31</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="匯出">{I("download")}</button><button class="btn x s i" type="button" aria-label="停用">{I("pause")}</button></td></tr>
        <tr><td><b class="mono" style="font-size:15px">BUY3GET1</b><div style="font-size:12px;color:#5F6470">果凍買三送一</div></td><td>買 3 送 1</td><td style="color:#5F6470">果凍分類 · 贈品：Jelly 貼紙包</td><td><b class="mono">9</b> / 不限</td><td style="color:#5F6470">到 10/31</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="複製">{I("copy")}</button><button class="btn x s i" type="button" aria-label="停用">{I("pause")}</button></td></tr>
        <tr style="opacity:.6"><td><b class="mono" style="font-size:15px">XMAS</b><div style="font-size:12px;color:#5F6470">排程中</div></td><td>9 折</td><td style="color:#5F6470">全店</td><td class="mono">0</td><td style="color:#5F6470">12/15 起</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button></td></tr>
      </tbody></table>
      <div style="margin-top:20px;display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px">
        <div class="card" style="display:flex;flex-direction:column;gap:4px"><span class="lbl">本月折扣帶來</span><b class="mono" style="font-size:24px">NT$ 48,200</b><span style="font-size:12px;color:#5F6470">62 筆訂單 · 讓利 NT$ 6,930</span></div>
        <div class="card" style="display:flex;flex-direction:column;gap:4px"><span class="lbl">數位商品交付</span><b class="mono" style="font-size:24px">17 次</b><span style="font-size:12px;color:#5F6470">付款後寄下載連結，24 小時、3 次有效</span></div>
        <div class="card" style="display:flex;flex-direction:column;gap:4px"><span class="lbl">訂閱制</span><b style="font-size:14px;color:#5F6470">之後再做</b><span style="font-size:12px;color:#5F6470">每月定期扣款要金流支援定期定額，先列在里程碑 5 後段</span></div>
      </div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">新增折扣碼</b></div>
      <div><span class="lbl">代碼</span><div style="display:flex;gap:8px;align-items:center"><input class="ul" type="text" value="MOON15" style="font-family:'Bricolage Grotesque',sans-serif;font-weight:800;font-size:16px;letter-spacing:.04em"><button class="btn t s" type="button">{I("refresh",12)}隨機</button></div></div>
      <div><span class="lbl">類型</span><div class="seg" style="margin-top:6px"><span class="on">百分比</span><span>固定金額</span><span>免運</span><span>買 X 送 Y</span></div></div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><div><span class="lbl">折扣</span><input class="ul" type="text" value="15 %"></div><div><span class="lbl">最低消費</span><input class="ul" type="text" value="NT$ 1,000"></div></div>
      <div style="border:1.5px dashed #B9BCC6;border-radius:0 12px 0 12px;padding:12px;display:flex;flex-direction:column;gap:10px">
        <div style="display:flex;align-items:center;gap:8px"><span class="chip info nd">買 X 送 Y</span><span style="font-size:12px;color:#5F6470">類型選「買 X 送 Y」時，取代上面的折扣欄</span></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><div><span class="lbl">買幾件</span><input class="ul" type="text" value="3"></div><div><span class="lbl">送幾件</span><input class="ul" type="text" value="1"></div></div>
        <div><span class="lbl">贈品商品</span><div style="display:flex;align-items:center;gap:10px;margin-top:6px"><span style="width:28px;height:28px;background:#15161A;border-radius:0 8px 0 8px;flex:0 0 auto"></span><b style="font-size:14px">Jelly 貼紙包</b><span class="mono" style="color:#5F6470">ST-01</span><button class="btn x s" type="button" style="margin-left:auto">{I("refresh",12)}更換</button></div><div style="font-size:12px;color:#5F6470;margin-top:6px">贈品另計成一行、售價 NT$0；庫存不足時不送，結帳頁會提示。</div></div>
      </div>
      <div><span class="lbl">適用</span><div class="seg" style="margin-top:6px"><span>全店</span><span class="on">分類</span><span>指定商品</span></div><div style="display:flex;gap:6px;margin-top:8px;flex-wrap:wrap"><span class="chip nd">禮盒 ×</span><button class="btn x s" type="button">{I("plus",12)}加分類</button></div></div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><div><span class="lbl">總次數上限</span><input class="ul" type="text" value="300"></div><div><span class="lbl">每人</span><input class="ul" type="text" value="1 次"></div></div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px"><div><span class="lbl">開始</span><input class="ul" type="text" value="2026-09-01 00:00"></div><div><span class="lbl">結束</span><input class="ul" type="text" value="2026-09-20 23:59"></div></div>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">可與免運碼疊加<span class="tg"></span></label>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">只給登入會員<span class="tg off"></span></label>
      <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("save")}建立</button><button class="btn t" type="button">{I("eye")}在結帳頁預覽</button></div>
    </aside>
  </main>'''))

for name,(title,css,w,h,html) in B.items():
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, css, html, w, h)); print("wrote",name)
