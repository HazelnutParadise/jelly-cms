# AI agent 管理畫面（docs/tickets/14-agent-api.md）：Agents（清單、建立、金鑰一次性顯示）與
# AgentDetail（詳情、金鑰與過渡倒數、輪替歷史、活動紀錄、輪替對話框各狀態、提前撤銷、停用與刪除）。
import sys, os
sys.path.insert(0, os.path.dirname(__file__))
import gen
from gen import I, doc, ADMIN_CSS, OUT, admin

# These two boards share the shipped admin fonts; no network fonts in previews.
gen.FONTS = '<link rel="stylesheet" href="./fonts.css">'

# 側欄「系統」區加上 AI agent（只影響這支腳本產生的板）。
for _sec, _items in gen.NAV:
    if _sec == "系統" and not any(k == "agents" for k, *_ in _items):
        _items.insert(1, ("agents", "Agents.dc.html", "AI agent", "key", "new"))

CSS = ADMIN_CSS + '''
.btn.p{background:#D82050;color:#fff}.btn.danger{background:#CF3025;color:#fff}
.chip.ok{background:color-mix(in srgb,#15A06A 14%,#fff);color:color-mix(in srgb,#15A06A 70%,#15161A)}
.chip.warn{background:color-mix(in srgb,#F5B800 22%,#fff);color:color-mix(in srgb,#F5B800 45%,#15161A)}
fieldset{border:0;padding:0;margin:0;min-inline-size:0}
td.act{min-width:72px;white-space:nowrap}.chip.nd::before{display:none}
.key-table-wrap{max-width:100%;overflow:auto}.key-table-wrap code{white-space:nowrap;word-break:normal}
@media(min-width:601px){.key-table{min-width:864px}.key-table td{white-space:nowrap}}
@media(max-width:600px){.key-scroll-hint{display:none}.key-table,.key-table tbody,.key-table tr,.key-table td{display:block;width:100%;box-sizing:border-box}.key-table thead{display:none}.key-table td{white-space:normal}}
.k{font:12.5px/1.5 ui-monospace,"SF Mono",Menlo,Consolas,monospace;color:#15161A;letter-spacing:0}
.k.m{color:#5F6470}
.note{display:flex;align-items:flex-start;gap:14px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;line-height:1.6}
.note .ic{margin-top:2px}
.keybox{background:#15161A;color:#fff;border-radius:0 14px 0 14px;padding:16px;display:flex;flex-direction:column;gap:10px}
.keybox .k{color:#fff;font-size:13px;word-break:break-all;flex:1}
.keybox .lbl{color:#9A9FAB}
.keybox .h{font-size:12px;color:#C9CBD3;line-height:1.6}
.keybox .btn.t{background:#2A2B31;color:#fff}
.pg{display:grid;grid-template-columns:minmax(0,1fr) 64px 64px;align-items:center;gap:6px;padding:8px 0;border-top:1px solid #EEEFF2;font-size:13px}
.pg .c{text-align:center}.pg .na{color:#B9BCC6;text-align:center;font-size:12px}
.pg b{font-weight:700}.pg .h{font-size:11.5px;color:#5F6470}
.opt{display:flex;gap:10px;align-items:flex-start;padding:10px 12px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8;font-size:13px}
.opt.on{box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA}
.opt .rad{width:16px;height:16px;border-radius:0 6px 0 6px;box-shadow:inset 0 0 0 2px #B9BCC6;flex:0 0 auto;margin-top:2px}
.opt.on .rad{background:#E8285B;box-shadow:inset 0 0 0 4px #fff,0 0 0 2px #E8285B}
.opt .h{font-size:12px;color:#5F6470;font-weight:400;margin-top:2px;line-height:1.5}
.dlg{background:#fff;border-radius:0 18px 0 18px;box-shadow:inset 0 0 0 1px #E2E3E8,0 18px 48px rgba(21,22,26,.10);display:flex;flex-direction:column}
.dlg .hd{display:flex;align-items:center;gap:10px;padding:14px 18px;border-bottom:1px solid #EEEFF2}
.dlg .bd{padding:14px 18px;display:flex;flex-direction:column;gap:12px;flex:1}
.dlg .ft{padding:12px 18px;border-top:1px solid #EEEFF2;display:flex;gap:8px;align-items:center}
.st{display:inline-flex;align-items:center;gap:6px;font-size:11px;font-weight:700;letter-spacing:.06em;color:#5F6470;text-transform:uppercase}
.st i{font-style:normal;width:20px;height:20px;display:grid;place-items:center;border-radius:0 7px 0 7px;background:#15161A;color:#fff;font-size:11px;letter-spacing:0}
.err{font-size:12px;color:color-mix(in srgb,#E03B2E 75%,#15161A);display:flex;align-items:center;gap:6px;margin-top:6px}
.ul.bad{border-bottom-color:color-mix(in srgb,#E03B2E 75%,#15161A)}
.tl{display:grid;grid-template-columns:120px minmax(0,1fr);gap:14px;padding:10px 0;border-top:1px solid #EEEFF2;font-size:13px;align-items:baseline}
.tl .t{color:#5F6470;font-size:12px}
.lg{display:grid;grid-template-columns:72px 104px minmax(0,1fr) 196px 100px;gap:12px;padding:8px 10px;border-bottom:1px solid #EEEFF2;align-items:baseline;font-size:12.5px}
.lg.hd{font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:#5F6470;font-weight:700;border-bottom:1px solid #E2E3E8}
.lg .t{color:#5F6470;font-variant-numeric:tabular-nums}
.mth{display:inline-block;min-width:44px;font:800 10.5px "Bricolage Grotesque",sans-serif;letter-spacing:.04em;color:#5F6470}
.sel{display:inline-flex;align-items:center;gap:6px;min-height:30px;padding:0 10px;background:#F3F3F5;border-radius:0 8px 0 8px;font-size:12px;font-weight:700;color:#15161A}
.danger-row{display:flex;align-items:flex-start;gap:12px;padding:12px 0;border-top:1px solid #EEEFF2}
.danger-row .h{font-size:12px;color:#5F6470;line-height:1.55;margin-top:2px}
@media(max-width:900px){main,main>div[style*="grid-template-columns"],main section>div[style*="grid-template-columns"]{grid-template-columns:minmax(0,1fr)!important}aside{position:static!important}.side{display:none!important}.main{margin-left:0!important}main{padding:16px!important}table{display:block;max-width:100%;overflow:auto}.lg{grid-template-columns:70px minmax(0,1fr)!important}.lg>*{overflow-wrap:anywhere}}
@media(prefers-reduced-motion:reduce){*{animation:none!important;transition:none!important}}
'''

def chips(perms):
    return " ".join(f'<span class="chip nd k" style="font-size:11px">{p}</span>' for p in perms)

def arow(name, desc, perms, owner, role, key, extra_key, last, state, scls, eff=None):
    eff_html = f'<div style="margin-top:6px"><span class="chip warn">有效權限 {eff}</span></div>' if eff else ""
    return f'''<tr><td style="min-width:150px"><a href="AgentDetail.dc.html" style="color:#15161A"><b>{name}</b></a><div style="font-size:12px;color:#5F6470">{desc}</div><div style="font-size:12px;color:#5F6470;margin-top:4px">{I("user",12)} {owner} · {role}</div></td>
      <td style="max-width:200px"><div style="display:flex;flex-wrap:wrap;gap:4px">{chips(perms)}</div>{eff_html}</td>
      <td style="white-space:nowrap"><span class="k">{key}</span>{extra_key}<div style="font-size:12px;color:#5F6470;margin-top:4px">{last}</div></td>
      <td><span class="chip {scls}">{state}</span></td>
      <td class="act"><a class="btn x s i" href="AgentDetail.dc.html" aria-label="詳情">{I("eye")}</a><button class="btn x s i" type="button" aria-label="更多">{I("more")}</button></td></tr>'''

PERM_ROWS = [
    ("文章", "posts", True, True, "含分類與標籤的讀取", False),
    ("頁面", "pages", True, True, "", False),
    ("附件", "media", True, True, "上傳、取代、改名、刪除", False),
    ("商品", "products", True, True, "", True),
    ("訂單", "orders", True, False, "只能讀", True),
    ("主題", "themes", True, True, "啟用、改主題設定", True),
    ("外掛", "plugins", True, True, "寫入只有重新載入", True),
    ("設定", "settings", True, True, "讀取時秘密一律遮罩", True),
    ("紀錄", "logs", True, False, "只看自己的請求", True),
]

def perm_grid(checked):
    rows = []
    for label, res, can_r, can_w, hint, admin_only in PERM_ROWS:
        tag = ' <span class="chip nd" style="font-size:10px;min-height:18px;margin-left:4px">需管理員</span>' if admin_only else ""
        def cell(op, ok):
            if not ok:
                return '<span class="na">—</span>'
            on = " on" if f"{res}.{op}" in checked else ""
            return f'<span class="c"><span class="cb{on}" role="checkbox" aria-label="{res}.{op}"></span></span>'
        h = f'<div class="h">{hint}</div>' if hint else ""
        rows.append(f'<div class="pg"><div><b>{label}</b>{tag}<div class="h k m" style="font-size:11px">{res}.*</div>{h}</div>{cell("read", can_r)}{cell("write", can_w)}</div>')
    head = '<div class="pg" style="border-top:0;padding-top:0"><span class="lbl">資源</span><span class="lbl" style="text-align:center">讀取</span><span class="lbl" style="text-align:center">寫入</span></div>'
    return head + "".join(rows)

FULL_KEY = "jla_Qm3d8sKe2VbN7xLwP0rTqZ4yHcJ1uGfA9eDiMkOs5nB"
NEW_KEY = "jla_Wz6pR1tYcE8qN3vLbK0sXu5mJaHg7dFo2iCeQ9wTlZr"

# ---------- 清單與建立 ----------
agents_main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:24px;align-items:start">
  <section style="display:flex;flex-direction:column;gap:20px;min-width:0">
    <div class="note">{I("key",18)}<span>AI agent 用自己的金鑰呼叫 <b class="k">/api/agent/v1</b>，不必借用人的帳號。能做的事＝這裡勾的權限，再和擁有者目前的角色取交集；擁有者被降級，agent 下一次請求就跟著縮小。agent 不能管理 agent，也不能輪替自己的金鑰。不需金鑰的說明：<a class="k" href="#">GET /api/agent/v1</a> · <a class="k" href="#">openapi.json</a></span></div>
    <div style="display:flex;align-items:center;gap:10px"><div class="seg"><span class="on">全部 <b>4</b></span><span>啟用 3</span><span>停用 1</span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="名稱或金鑰前綴" aria-label="搜尋 agent" style="border:0;background:transparent;font:inherit;flex:1;min-width:0;color:#15161A"></label></div>
    <table>
      <thead><tr><th>agent 與擁有者</th><th>權限</th><th>金鑰與最後使用</th><th>狀態</th><th></th></tr></thead>
      <tbody>
        {arow("內容小幫手","寫草稿、補摘要與標籤",["posts.read","posts.write","media.write"],"Hazel","管理員","jla_Qm3d8sKe…","","2 分鐘前 · 1.160.10.0","啟用","ok")}
        {arow("訂單日報","每天 08:00 彙整訂單到試算表",["orders.read","products.read","logs.read"],"Ting","管理員","jla_x91LfPz0…",'<div style="margin-top:6px"><span class="chip warn">過渡中 · 剩 23 分</span> <span class="k m" style="font-size:11px">jla_c0AaV2r9…</span></div>',"今天 08:00 · 203.0.113.0","啟用","ok")}
        {arow("SEO 巡檢","每週檢查標題、摘要與設定",["posts.read","pages.read","settings.read"],"小魚","編輯 · 已降級","jla_Hn4Ty7Uq…","","3 天前 · 2001:db8:abcd::","啟用","ok","2/3 · 設定需要管理員")}
        {arow("舊的匯入腳本","WordPress 搬家時用",["posts.write","media.write"],"Wen","管理員","jla_Bv8Mx2Ls…","","上個月","停用","")}
      </tbody>
    </table>
    <div>
      <div style="display:flex;align-items:center;gap:8px;margin-bottom:12px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">建立之後</b><span style="font-size:12px;color:#5F6470">完整金鑰只出現一次；關閉後任何畫面、API、資料庫都拿不到明文</span></div>
      <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;align-items:start">
        <div style="display:flex;flex-direction:column;gap:8px"><span class="st"><i>A</i>剛建立</span>
          <div class="keybox"><span class="lbl">金鑰只會顯示這一次</span>
            <div style="display:flex;align-items:flex-start;gap:8px"><code class="k">{FULL_KEY}</code><button class="btn t s i" type="button" aria-label="複製金鑰">{I("copy",14)}</button></div>
            <div class="h">放在 <span class="kbd" style="background:#2A2B31;color:#C9CBD3">Authorization: Bearer …</span>。遺失了不能找回，只能輪替。</div>
            <button class="btn p s" type="button" disabled style="opacity:.5;align-self:flex-start">{I("check",12)}我已複製，關閉</button></div></div>
        <div style="display:flex;flex-direction:column;gap:8px"><span class="st"><i>B</i>已複製</span>
          <div class="keybox"><span class="lbl">金鑰只會顯示這一次</span>
            <div style="display:flex;align-items:flex-start;gap:8px"><code class="k">{FULL_KEY}</code><button class="btn s i" type="button" aria-label="已複製" style="background:#13C2A3;color:#0B3A31">{I("check",14)}</button></div>
            <div class="h" style="color:#13C2A3">已複製到剪貼簿</div>
            <button class="btn p s" type="button" style="align-self:flex-start">{I("check",12)}我已複製，關閉</button></div></div>
        <div style="display:flex;flex-direction:column;gap:8px"><span class="st"><i>C</i>關閉後</span>
          <div class="card" style="display:flex;flex-direction:column;gap:10px">
            <div style="display:flex;align-items:center;gap:8px"><b>內容小幫手</b><span class="chip ok" style="margin-left:auto">啟用</span></div>
            <div><span class="lbl">目前金鑰</span><div class="k" style="margin-top:4px">jla_Qm3d8sKe…</div></div>
            <div style="font-size:12px;color:#5F6470;line-height:1.6">只保留前 12 個字元方便辨認。資料庫只存雜湊。</div>
            <a class="btn t s" href="AgentDetail.dc.html" style="align-self:flex-start">{I("refresh",12)}要新金鑰？去輪替</a></div></div>
      </div>
    </div>
  </section>
  <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
    <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">建立 agent</b></div>
    <div><span class="lbl">名稱</span><input class="ul" type="text" value="內容小幫手" aria-label="名稱"><div style="font-size:11.5px;color:#5F6470;margin-top:4px">1–80 字，不能和其他 agent 重複</div></div>
    <div><span class="lbl">說明（可選）</span><textarea class="ul" rows="2" aria-label="說明">寫草稿、補摘要與標籤</textarea></div>
    <div><span class="lbl">權限</span><div style="margin-top:8px">{perm_grid({"posts.read","posts.write","media.write"})}</div></div>
    <div style="font-size:12px;color:#5F6470;line-height:1.6;display:flex;gap:8px">{I("user",14)}<span>擁有者是你（Hazel · 管理員）。標「需管理員」的權限，擁有者不是管理員時不會生效。</span></div>
    <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("key")}建立並產生金鑰</button><button class="btn t" type="button">{I("x")}取消</button></div>
    <div class="note">連線中斷時保留名稱、說明與權限，恢復後手動重試，不自動重送建立。確認狀態後再重試，避免重複操作。</div>
  </aside>
</main>'''

# ---------- 詳情 ----------
def krow(prefix, state, scls, created, last, expires, act):
    return f'<tr><td><span class="k">{prefix}</span></td><td><span class="chip {scls}">{state}</span></td><td style="color:#5F6470;font-size:12.5px">{created}</td><td style="color:#5F6470;font-size:12.5px">{last}</td><td style="font-size:12.5px">{expires}</td><td class="act">{act}</td></tr>'

def lrow(t, key, mth, route, status, scls, ip, sub):
    return f'<div class="lg"><span class="t">{t}</span><span class="k" style="font-size:12px">{key}</span><span><span class="mth">{mth}</span><span class="k">{route}</span><div style="font-size:11.5px;color:#5F6470;margin-top:2px">{sub}</div></span><span><span class="chip {scls}">{status}</span></span><span class="k m" style="font-size:11.5px">{ip}</span></div>'

def tlrow(t, who, what):
    return f'<div class="tl"><span class="t">{t}</span><span><b>{who}</b> · {what}</span></div>'

def rot_opts(mode):
    imm = " on" if mode == "immediate" else ""
    gr = " on" if mode != "immediate" else ""
    return f'''<div class="opt{imm}"><span class="rad"></span><span><b>立即失效</b>（預設）<div class="h">舊金鑰下一次請求就 401。確定 agent 那邊能馬上換金鑰時用。</div></span></div>
      <div class="opt{gr}"><span class="rad"></span><span><b>過渡期</b><div class="h">新舊金鑰同時可用，期滿舊金鑰 401。最長 24 小時，可隨時提前撤銷。</div></span></div>'''

def minutes(value, bad=False):
    cls = " bad" if bad else ""
    err = f'<div class="err">{I("alert",14)}過渡期必須是 1 到 1440 分鐘（最長 24 小時）</div>' if bad else ""
    return f'''<div><span class="lbl">過渡時長</span>
        <div style="display:flex;align-items:center;gap:10px;margin-top:4px"><input class="ul{cls}" type="text" value="{value}" aria-label="過渡分鐘" style="width:90px"><span style="font-size:13px;color:#5F6470">分鐘</span><div class="seg" style="margin-left:auto"><span>15</span><span{' class="on"' if value=="30" else ''}>30</span><span>60</span><span>1440</span></div></div>{err}</div>'''

def dlg(step, title, body, foot):
    return f'''<div style="display:flex;flex-direction:column;gap:8px"><span class="st"><i>{step}</i>{title}</span>
      <div class="dlg"><div class="hd"><span class="stripe" style="width:32px"></span><b style="font-size:14px">{title.split("·")[0].strip() if "·" in title else title}</b><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x")}</button></div>
      <div class="bd">{body}</div><div class="ft">{foot}</div></div></div>'''

rot_title_imm = "輪替金鑰"
d1 = dlg("1", "輪替金鑰 · 立即失效（預設）", rot_opts("immediate") + '<div style="font-size:12px;color:#5F6470;line-height:1.6">agent 的 ID、名稱、權限、擁有者與紀錄都不變，只換金鑰。</div>',
         f'<button class="btn p" type="button">{I("refresh")}輪替</button><button class="btn t" type="button">取消</button>')
d2 = dlg("2", "輪替金鑰 · 過渡期", rot_opts("grace") + minutes("30") + f'<div style="font-size:12px;color:#3A3D45;background:#F5F5F7;border-radius:0 10px 0 10px;padding:8px 10px">舊金鑰 <span class="k">jla_Qm3d8sKe…</span> 會在 <b>今天 16:12</b> 失效。</div>',
         f'<button class="btn p" type="button">{I("refresh")}輪替</button><button class="btn t" type="button">取消</button>')
d3 = dlg("3", "輪替金鑰 · 過渡期超過上限", rot_opts("grace") + minutes("1500", True),
         f'<button class="btn p" type="button" disabled style="opacity:.45">{I("refresh")}輪替</button><button class="btn t" type="button">取消</button><span style="font-size:12px;color:color-mix(in srgb,#E03B2E 75%,#15161A);margin-left:auto">API 也會回 400 grace_out_of_range</span>')
d4 = dlg("4", "新金鑰 · 只顯示這一次", f'''<div class="keybox"><span class="lbl">新金鑰</span>
        <div style="display:flex;align-items:flex-start;gap:8px"><code class="k">{NEW_KEY}</code><button class="btn t s i" type="button" aria-label="複製新金鑰">{I("copy",14)}</button></div>
        <div class="h">舊金鑰 <span class="k" style="color:#fff;font-size:12px">jla_Qm3d8sKe…</span> 過渡到 16:12，可在金鑰表提前撤銷。</div></div>''',
         f'<button class="btn p" type="button">{I("check")}我已複製，關閉</button>')
d5 = dlg("5", "提前撤銷舊金鑰", f'''<div style="font-size:13px;line-height:1.6">撤銷 <span class="k">jla_7Hq2xP0a…</span>？還剩 23 分的過渡期會直接結束，這把金鑰的下一次請求就 401。</div>
        <div style="font-size:12px;color:#5F6470">生效中的 <span class="k">jla_Qm3d8sKe…</span> 不受影響。</div>''',
         f'<button class="btn danger" type="button">{I("x")}立即撤銷</button><button class="btn t" type="button">取消</button>')
d6 = dlg("6", "刪除 agent", f'''<div style="font-size:13px;line-height:1.6">刪除「內容小幫手」？所有金鑰（含過渡中的）立刻失效，清單與詳情不再出現。</div>
        <div style="font-size:12px;color:#5F6470;line-height:1.6">活動紀錄與輪替歷史會保留，之後仍可在紀錄裡依這個 agent 篩選。</div>
        <div><span class="lbl">輸入名稱確認</span><input class="ul" type="text" placeholder="內容小幫手" aria-label="輸入名稱確認"></div>''',
         f'<button class="btn danger" type="button">{I("trash")}刪除</button><button class="btn t" type="button">取消</button>')

detail_main = f'''<main style="padding:16px 24px 32px;display:flex;flex-direction:column;gap:28px">
  <div style="display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:24px;align-items:start">
  <section style="display:flex;flex-direction:column;gap:22px;min-width:0">
    <div style="display:flex;align-items:center;gap:16px"><span style="width:56px;height:56px;display:grid;place-items:center;background:#2F6BFF;color:#fff;border-radius:0 18px 0 18px">{I("key",24)}</span>
      <div><div style="display:flex;align-items:center;gap:10px"><h1 class="d" style="font-size:26px">內容小幫手</h1><span class="chip ok">啟用</span></div>
      <div style="color:#5F6470;font-size:13px;margin-top:4px">寫草稿、補摘要與標籤 · ID 3 · 擁有者 Hazel（管理員）· 建立於 2026/10/02</div></div></div>

    <div>
      <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px"><span class="lbl">金鑰</span><span style="font-size:12px;color:#5F6470">同時最多兩把有效：生效中一把、過渡中一把</span></div>
      <p class="key-scroll-hint" style="font-size:12px;color:#5F6470">可左右捲動查看完整金鑰資料與操作。</p>
      <div class="key-table-wrap" tabindex="0" role="region" aria-label="金鑰"><table class="key-table"><thead><tr><th>前綴</th><th>狀態</th><th>建立</th><th>最後使用</th><th>到期</th><th></th></tr></thead><tbody>
        {krow("jla_Qm3d8sKe…","生效中","ok","10/24 15:42 · Hazel","2 分鐘前 · 1.160.10.0","—","")}
        {krow("jla_7Hq2xP0a…","過渡中 · 剩 23:41","warn","10/02 09:12 · Ting","16 分鐘前 · 1.160.10.0","今天 16:12",f'<button class="btn danger s" type="button">{I("x",12)}提前撤銷</button>')}
        {krow("jla_Lp0Wq8Zd…","已撤銷","","09/14 10:03 · Ting","10/02 09:10","—","")}
        {krow("jla_m2Kc9Bv1…","已過期","","09/01 11:20 · Hazel","09/14 09:58","09/14 10:33","")}
      </tbody></table></div>
    </div>

    <div style="display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:20px;align-items:start">
      <div class="card" style="display:flex;flex-direction:column;gap:10px">
        <div style="display:flex;align-items:center;gap:8px"><span class="lbl">權限</span><span class="chip ok" style="margin-left:auto">有效 3/3</span><button class="btn x s" type="button">{I("edit",12)}修改</button></div>
        <label><span class="lbl">名稱</span><input class="ul" value="內容小幫手" aria-label="名稱"></label>
        <label><span class="lbl">說明（可選）</span><textarea class="ul" aria-label="說明">寫草稿、補摘要與標籤</textarea></label>
        {perm_grid({"posts.read","posts.write","media.write"})}
        <button class="btn p" type="button">{I("save",12)}儲存變更</button>
        <div style="font-size:12px;color:#5F6470;line-height:1.6">有效權限＝勾選的權限 ∩ 擁有者目前角色。擁有者若降為作者，文章與附件仍可用；降為非員工則全部 403。</div>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:2px">
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:4px"><span class="lbl">輪替歷史</span></div>
        {tlrow("10/24 15:42","Hazel",'輪替 · 過渡期 30 分 · <span class="k">jla_7Hq2…</span> → <span class="k">jla_Qm3d…</span>')}
        {tlrow("10/02 09:12","Ting",'輪替 · 立即失效 · <span class="k">jla_Lp0W…</span> → <span class="k">jla_7Hq2…</span>')}
        {tlrow("09/14 10:03","Ting",'提前撤銷 <span class="k">jla_m2Kc…</span>')}
        {tlrow("09/01 11:20","Hazel","建立 agent")}
      </div>
    </div>

    <div>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:10px;flex-wrap:wrap"><span class="lbl">活動紀錄</span>
        <div class="seg"><span class="on">全部</span><span>2xx</span><span>4xx</span><span>5xx</span></div>
        <span class="sel">全部模組 {I("chevron-d",12)}</span><span class="sel">所有金鑰 {I("chevron-d",12)}</span><span class="sel">最近 24 小時 {I("chevron-d",12)}</span>
        <span style="margin-left:auto;font-size:12px;color:#5F6470">共 <b class="mono" style="font-size:14px;color:#15161A">1,284</b> 筆 · 跨越 3 次輪替</span></div>
      <div class="lg hd"><span>時間</span><span>金鑰</span><span>請求</span><span>狀態</span><span>來源</span></div>
      {lrow("15:44:10","jla_Qm3d…","PUT","/posts/:id","200","ok","1.160.10.0","params id=128 · 本文 2.1 KB")}
      {lrow("15:43:57","jla_Qm3d…","GET","/posts/:id","200","ok","1.160.10.0","params id=128")}
      {lrow("15:30:02","jla_Lp0W…","GET","/me","401 key_revoked","bad","203.0.113.0","舊金鑰仍在被使用")}
      {lrow("15:12:44","jla_7Hq2…","PUT","/posts/:id","409 version_conflict","warn","1.160.10.0","params id=121 · base_updated_at 過期")}
      {lrow("15:02:31","jla_7Hq2…","POST","/pages","403 permission_missing","warn","1.160.10.0","缺 pages.write")}
      {lrow("14:58:10","jla_7Hq2…","GET","/posts","200","ok","1.160.10.0","query status")}
      <div style="display:flex;align-items:center;gap:10px;margin-top:10px"><button class="btn t s" type="button">{I("chevron-d",12)}載入更多</button><span style="font-size:12px;color:#5F6470">只存請求摘要（路徑參數、查詢鍵名、本文長度），不存內容；IP 去掉最後一段。</span></div>
    </div>
  </section>

  <aside style="display:flex;flex-direction:column;gap:16px;position:sticky;top:16px">
    <div class="card" style="display:flex;flex-direction:column;gap:10px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:14px">給 agent 的連線資訊</b></div>
      <div><span class="lbl">API</span><div class="k" style="margin-top:2px">/api/agent/v1</div></div>
      <div><span class="lbl">說明（不需金鑰）</span><div class="k" style="margin-top:2px">GET /api/agent/v1</div><div class="k">GET /api/agent/v1/openapi.json</div></div>
      <div><span class="lbl">驗證</span><div class="k" style="margin-top:2px">Authorization: Bearer jla_…</div></div>
      <div><span class="lbl">Agent Skill</span><div class="k" style="margin-top:2px">skills/jelly-cms/SKILL.md</div></div>
      <button class="btn t s" type="button" style="align-self:flex-start">{I("copy",12)}複製給 agent 的說明</button>
    </div>
    <div class="card" style="display:flex;flex-direction:column">
      <span class="lbl" style="margin-bottom:4px">停用與刪除</span>
      <div class="danger-row" style="border-top:0"><div style="flex:1"><b style="font-size:13.5px">停用</b><div class="h">所有金鑰（含過渡中的）下一次請求 401。可以再啟用。</div></div><button class="btn t s" type="button">{I("pause",12)}停用</button></div>
      <div class="danger-row"><div style="flex:1"><b style="font-size:13.5px">刪除</b><div class="h">金鑰全部失效；活動紀錄保留。</div></div><button class="btn danger s" type="button">{I("trash",12)}刪除</button></div>
    </div>
  </aside>
  </div>

  <div>
    <div style="display:flex;align-items:center;gap:8px;margin-bottom:14px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">對話框狀態</b><span style="font-size:12px;color:#5F6470">輪替的兩種模式、過渡時長錯誤、新金鑰一次性顯示、提前撤銷、刪除</span></div>
    <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px;align-items:start">{d1}{d2}{d3}{d4}{d5}{d6}</div>
  </div>
</main>'''

B = {
    "Agents": ("後台 · AI agent", 1440, 1200, admin("agents", ["系統", "AI agent"],
        f'<a class="btn t" href="#">{I("external")}API 說明</a><button class="btn p" type="button">{I("plus")}建立 agent</button>', agents_main)),
    "AgentDetail": ("後台 · AI agent 詳情與輪替", 1440, 2850, admin("agents", ["系統", "AI agent", "內容小幫手"],
        f'<button class="btn t" type="button">{I("pause")}停用</button><button class="btn p" type="button">{I("refresh")}輪替金鑰</button>', detail_main)),
}

for name, (title, w, h, html) in B.items():
    with open(os.path.join(OUT, f"{name}.dc.html"), "w", encoding="utf-8") as f:
        rendered = doc(title, CSS, html, w, h)
        f.write("\n".join(line.rstrip() for line in rendered.splitlines()) + "\n")
    print("wrote", name)
