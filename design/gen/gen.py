# Generates the remaining Jelly CMS design boards (front shop flow + missing admin screens).
import os, json
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "project")

FONTS = '''<link rel="stylesheet" href="./fonts.css">'''

MARK = '<img class="mark" src="./jelly-mark.svg" alt="" aria-hidden="true">'

ICONS = {
 "plus": '<path d="M12 5v14M5 12h14"/>',
 "check": '<path d="M4 12l5 5L20 7"/>',
 "x": '<path d="M6 6l12 12M18 6L6 18"/>',
 "trash": '<path d="M4 7h16M10 7V4h4v3M6 7l1 13h10l1-13M10 11v6M14 11v6"/>',
 "edit": '<path d="M4 20h4L19 9l-4-4L4 16zM13 7l4 4"/>',
 "save": '<path d="M5 4h11l3 3v13H5zM8 4v5h8V4M8 20v-6h8v6"/>',
 "undo": '<path d="M9 14L4 9l5-5M4 9h10a6 6 0 0 1 0 12h-3"/>',
 "arrow-r": '<path d="M4 12h16M14 6l6 6-6 6"/>',
 "arrow-l": '<path d="M20 12H4M10 6l-6 6 6 6"/>',
 "search": '<circle cx="11" cy="11" r="6"/><path d="M16 16l4 4"/>',
 "cart": '<path d="M3 4h2l2 12h11l2-8H6"/><circle cx="9" cy="20" r="1"/><circle cx="17" cy="20" r="1"/>',
 "lock": '<path d="M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4"/>',
 "mail": '<path d="M3 5h18v14H3zM3 7l9 6 9-6"/>',
 "truck": '<path d="M3 7h12v10H3zM15 10h4l2 3v4h-6"/><circle cx="7" cy="18" r="1.5"/><circle cx="18" cy="18" r="1.5"/>',
 "card": '<path d="M3 6h18v12H3zM3 10h18"/>',
 "eye": '<path d="M2 12s4-6 10-6 10 6 10 6-4 6-10 6S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
 "link": '<path d="M10 14l4-4M8 16l-2 2a3 3 0 0 1-4-4l4-4M16 8l2-2a3 3 0 0 1 4 4l-4 4"/>',
 "tag": '<path d="M3 12V3h9l9 9-9 9z"/><circle cx="8" cy="8" r="1.5"/>',
 "folder": '<path d="M3 5h7l2 2h9v12H3z"/>',
 "menu": '<path d="M4 6h16M4 12h16M4 18h16"/>',
 "drag": '<circle cx="9" cy="6" r="1.2" fill="currentColor"/><circle cx="15" cy="6" r="1.2" fill="currentColor"/><circle cx="9" cy="12" r="1.2" fill="currentColor"/><circle cx="15" cy="12" r="1.2" fill="currentColor"/><circle cx="9" cy="18" r="1.2" fill="currentColor"/><circle cx="15" cy="18" r="1.2" fill="currentColor"/>',
 "comment": '<path d="M4 4h16v12H9l-5 4z"/>',
 "user": '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
 "users": '<circle cx="9" cy="8" r="3.5"/><path d="M2 20a7 7 0 0 1 14 0M16 4a3.5 3.5 0 0 1 0 7M22 20a7 7 0 0 0-5-6.7"/>',
 "shield": '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/>',
 "clock": '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/>',
 "history": '<path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5M12 7v5l3 3"/>',
 "upload": '<path d="M12 16V4M6 10l6-6 6 6M4 20h16"/>',
 "download": '<path d="M12 4v12M6 10l6 6 6-6M4 20h16"/>',
 "db": '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
 "globe": '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18"/>',
 "redirect": '<path d="M4 7h10a4 4 0 0 1 0 8H4M8 11l-4 4 4 4"/>',
 "list": '<path d="M8 6h13M8 12h13M8 18h13M3 6h1M3 12h1M3 18h1"/>',
 "terminal": '<path d="M3 4h18v16H3zM7 9l3 3-3 3M12 15h5"/>',
 "cmd": '<path d="M9 9V6a3 3 0 1 0-3 3h3zM15 9V6a3 3 0 1 1 3 3h-3zM9 15v3a3 3 0 1 1-3-3h3zM15 15v3a3 3 0 1 0 3-3h-3zM9 9h6v6H9z"/>',
 "settings": '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9L7 7M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1"/>',
 "home": '<path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"/>',
 "doc": '<path d="M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6"/>',
 "page": '<path d="M3 4h18v16H3zM3 10h18M9 10v10"/>',
 "image": '<path d="M3 5h18v14H3zM21 15l-5-5-8 8"/><circle cx="8" cy="10" r="1.5"/>',
 "box": '<path d="M3 7l9-4 9 4v10l-9 4-9-4zM3 7l9 4 9-4M12 11v10"/>',
 "theme": '<path d="M12 3l9 5-9 5-9-5zM3 13l9 5 9-5"/>',
 "plug": '<path d="M9 3v5M15 3v5M6 8h12l-1 5a5 5 0 0 1-10 0zM12 18v3"/>',
 "tools": '<path d="M14 6a4 4 0 0 0 4 4l3-3-4-4-3 3zM14 6L4 16l4 4 10-10"/>',
 "alert": '<path d="M12 3l10 18H2zM12 10v5M12 18v1"/>',
 "refresh": '<path d="M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5"/>',
 "filter": '<path d="M3 5h18l-7 8v6l-4 2v-8z"/>',
 "more": '<circle cx="5" cy="12" r="1.5" fill="currentColor"/><circle cx="12" cy="12" r="1.5" fill="currentColor"/><circle cx="19" cy="12" r="1.5" fill="currentColor"/>',
 "star": '<path d="M12 3l2.8 6 6.2.7-4.6 4.3 1.3 6.3L12 17l-5.7 3.3 1.3-6.3L3 9.7 9.2 9z"/>',
 "key": '<circle cx="8" cy="14" r="4"/><path d="M11 11l9-9M16 6l3 3M14 8l2 2"/>',
 "calendar": '<path d="M3 5h18v16H3zM3 10h18M8 3v4M16 3v4"/>',
 "reply": '<path d="M9 14L4 9l5-5M4 9h10a6 6 0 0 1 0 12h-3"/>',
 "spam": '<circle cx="12" cy="12" r="9"/><path d="M5.6 5.6l12.8 12.8"/>',
 "external": '<path d="M14 4h6v6M20 4l-9 9M18 14v6H4V6h6"/>',
 "copy": '<path d="M9 9h11v11H9zM4 15V4h11"/>',
 "play": '<path d="M7 4l12 8-12 8z"/>',
 "pause": '<path d="M7 4h4v16H7zM13 4h4v16h-4z"/>',
 "grid": '<path d="M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z"/>',
 "chevron-d": '<path d="M6 9l6 6 6-6"/>',
 "chevron-r": '<path d="M9 6l6 6-6 6"/>',
 "ticket": '<path d="M3 6h18v4a2 2 0 0 0 0 4v4H3v-4a2 2 0 0 0 0-4zM14 6v2M14 11v2M14 16v2"/>',
 "wp": '<circle cx="12" cy="12" r="9"/><path d="M5 9l3 9 3-7 3 7 3-9M9 9h2M14 9h2"/>',
}
def I(name, size=None, extra=""):
    s = f' style="width:{size}px;height:{size}px"' if size else ""
    return f'<svg class="ic"{s} viewBox="0 0 24 24"{extra}>{ICONS[name]}</svg>'

# 設定頁分頁列：所有設定板共用這一份（Settings、SettingsRoutes、SettingsCrawlers、Infra、Update、UpdateContainer）。
# 手寫的 Settings 板要貼同一份標記；改這裡就要同步 Settings.dc.html。
SETTINGS_TABS = [("globe","網站","Settings.dc.html"),("list","閱讀與網址","SettingsRoutes.dc.html"),("search","搜尋引擎與 AI 爬蟲","SettingsCrawlers.dc.html"),("key","登入與 OAuth","Settings.dc.html"),("comment","留言","Settings.dc.html"),("card","金流","Settings.dc.html"),("truck","運送","Settings.dc.html"),("mail","郵件與通知","Settings.dc.html"),("db","快取與佇列","Infra.dc.html"),("download","更新","Update.dc.html")]
SETTINGS_LINKS = [("upload","備份與匯入","Backup.dc.html"),("users","使用者","Users.dc.html"),("terminal","系統日誌","Logs.dc.html")]
def settings_nav(on=None):
    def tab(ic, l, h):
        selected = ' class="on" aria-current="page"' if l == on else ""
        return f'<a href="{h}"{selected}>{I(ic,14)}{l}</a>'
    return '<nav class="sub" style="display:flex;flex-direction:column;gap:2px;position:sticky;top:16px" aria-label="設定分類">'+"".join(tab(*t) for t in SETTINGS_TABS)+'<div style="height:1px;background:#EEEFF2;margin:6px 10px" role="separator"></div>'+"".join(tab(*t) for t in SETTINGS_LINKS)+'</nav>'

BASE_CSS = '''
body{margin:0;font-family:"Noto Sans TC","PingFang TC",system-ui,sans-serif;color:#15161A;background:#fff;-webkit-font-smoothing:antialiased}
a{color:#2F6BFF;text-decoration:none}a:hover{color:#E8285B}
.d{font-family:"Bricolage Grotesque","Noto Sans TC",sans-serif;font-weight:800;letter-spacing:-.02em;line-height:1.08;margin:0}
.ic{width:17px;height:17px;stroke:currentColor;fill:none;stroke-width:1.75;stroke-linecap:square;stroke-linejoin:miter;flex:0 0 auto}
.lbl{font-size:11px;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:#5F6470}
.stripe{display:block;width:56px;height:3px;transform:skewX(-32deg);transform-origin:left bottom;background:linear-gradient(90deg,#E8285B 0 52%,transparent 52% 60%,#2F6BFF 60% 82%,transparent 82% 88%,#13C2A3 88% 100%)}
.topline{height:3px;background:linear-gradient(90deg,#E8285B 0 64%,#2F6BFF 64% 86%,#13C2A3 86% 100%)}
.chip{display:inline-flex;align-items:center;gap:5px;min-height:22px;padding:0 8px;font-size:11px;font-weight:700;border-radius:0 7px 0 7px;background:#EDEEF1;color:#15161A;white-space:nowrap}
.chip:before{content:"";width:6px;height:6px;background:currentColor;border-radius:0 2px 0 2px}
.chip.nd:before{display:none}
.ok{background:#DCF3E9;color:#0F6A45}.warn{background:#FCF0C8;color:#7A5200}.info{background:#DCE6FF;color:#1A3F9E}.bad{background:#FBE1DE;color:#9C2318}.mint{background:#D4F5EE;color:#0B5E4E}
.mark{display:inline-block;width:22px;height:26px;object-fit:contain}
.seg{display:inline-flex;gap:3px;padding:3px;background:#F3F3F5;border-radius:0 9px 0 9px}
.seg span{min-height:26px;padding:0 10px;display:inline-flex;align-items:center;gap:6px;font-size:12px;font-weight:700;color:#5F6470;border-radius:0 7px 0 7px;white-space:nowrap}
.seg span.on{background:#15161A;color:#fff}
.tg{position:relative;width:40px;height:22px;border-radius:0 11px 0 11px;background:#13C2A3;display:inline-block;flex:0 0 auto}.tg:after{content:"";position:absolute;top:3px;right:3px;width:16px;height:16px;background:#fff;border-radius:0 7px 0 7px}
.tg.off{background:#D9DBE0}.tg.off:after{right:auto;left:3px}
.tg.j{width:46px;height:26px;border-radius:13px}.tg.j:after{top:3px;right:3px;width:20px;height:20px;border-radius:50%;box-shadow:0 1px 2px rgba(21,22,26,.3),0 4px 10px -3px rgba(21,22,26,.28)}
.tg.j.off:after{right:auto;left:3px}
.seg.j{padding:4px;gap:2px;background:#EDEEF1;border-radius:999px}.seg.j span,.seg.j a{min-height:30px;padding:0 14px;border-radius:999px}
.seg.j span.on,.seg.j a.on{background:#15161A;color:#fff;box-shadow:0 3px 8px -3px rgba(21,22,26,.45)}
.jt{display:flex;align-items:center;gap:11px;padding:11px 12px 11px 14px;background:#15161A;color:#fff;border-radius:0 12px 0 12px;font-size:13px;line-height:1.3;font-weight:600;box-shadow:0 14px 34px -16px rgba(21,22,26,.42),0 4px 12px -8px rgba(21,22,26,.28)}
.jt>i{width:11px;height:11px;border-radius:50%;flex:0 0 auto}.jt .d2{color:#C9CBD3;font-weight:500}
.jt .xx{margin-left:auto;display:inline-grid;place-items:center;width:22px;height:22px;border-radius:50%;color:#C9CBD3;flex:0 0 auto}.jt .xx .ic{width:13px;height:13px}
.jt.light{background:#fff;color:#15161A;box-shadow:inset 0 0 0 1px #E2E3E8,0 14px 34px -16px rgba(21,22,26,.2)}.jt.light .d2{color:#5F6470}.jt.light .xx{color:#5F6470}
.jd{position:absolute;left:50%;top:50%;transform:translate(-50%,-50%);width:440px;box-sizing:border-box;background:#fff;border-radius:0 24px 0 24px;box-shadow:0 14px 34px -16px rgba(21,22,26,.42),0 4px 12px -8px rgba(21,22,26,.28);padding:26px;display:flex;flex-direction:column;gap:14px}
.jd .close{position:absolute;top:16px;right:16px;width:30px;height:30px;border-radius:50%;display:grid;place-items:center;color:#5F6470}
.jd-scrim{position:absolute;inset:0;background:rgba(21,22,26,.42);backdrop-filter:blur(1.5px)}
.ul{width:100%;min-height:34px;padding:4px 0;border:0;border-bottom:1.5px solid #D9DBE0;background:transparent;font:inherit;font-size:14px;color:#15161A;box-sizing:border-box}
.mono{font-family:"Bricolage Grotesque",sans-serif;font-weight:800;letter-spacing:-.02em}
.cube{position:absolute;border-radius:0 26% 0 26%}
.kbd{display:inline-block;font:700 11px "Bricolage Grotesque",sans-serif;padding:2px 6px;border-radius:0 5px 0 5px;background:#EDEEF1;color:#5F6470}
'''
FRONT_CSS = BASE_CSS + '''
body{font-size:15px}
.ic{width:18px;height:18px}
.btn{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:46px;padding:0 22px;font-weight:700;font-size:15px;border:0;border-radius:0 10px 0 10px;cursor:pointer;background:#15161A;color:#fff;font-family:inherit;white-space:nowrap}
.btn.p{background:#E8285B}.btn.t{background:#EDEEF1;color:#15161A}.btn.x{background:transparent;color:#15161A}.btn.i{width:46px;padding:0}.btn.sm{min-height:36px;padding:0 12px;font-size:13px}.btn.sm.i{width:36px}
.nav a{color:#15161A;font-weight:600;font-size:15px;padding:0 14px;min-height:44px;display:inline-flex;align-items:center;border-radius:0 8px 0 8px}
.nav a.on{background:#15161A;color:#fff}
.field{width:100%;box-sizing:border-box;min-height:46px;padding:0 14px;border:0;background:#F3F3F5;border-radius:0 10px 0 10px;font:inherit;font-size:15px;color:#15161A}
.flbl{font-size:13px;font-weight:700;color:#15161A;display:block;margin-bottom:6px}
.step{display:flex;align-items:center;gap:10px;font-weight:700;font-size:14px}
.step i{width:28px;height:28px;display:grid;place-items:center;border-radius:0 9px 0 9px;background:#EDEEF1;color:#5F6470;font-style:normal;font-family:"Bricolage Grotesque",sans-serif}
.step.on i{background:#15161A;color:#fff}.step.done i{background:#13C2A3;color:#fff}
.opt{display:flex;align-items:center;gap:12px;padding:14px 16px;border-radius:0 12px 0 12px;box-shadow:inset 0 0 0 1px #E2E3E8;cursor:pointer}
.opt.on{box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA}
.opt .rad{width:18px;height:18px;border-radius:0 6px 0 6px;box-shadow:inset 0 0 0 2px #B9BCC6;flex:0 0 auto}
.opt.on .rad{background:#E8285B;box-shadow:inset 0 0 0 5px #fff,0 0 0 2px #E8285B}
.mark{width:26px;height:30px;object-fit:contain}
.entry{display:grid;grid-template-columns:96px minmax(0,1fr);gap:24px;padding:28px 0;border-top:1px solid #E2E3E8}
.entry h3{margin:0 0 8px;font-size:26px}.entry h3 a{color:#15161A}
.entry p{margin:0;color:#5F6470;line-height:1.7}
.day{font-size:44px;line-height:1}.ym{font-size:12px;color:#5F6470;font-weight:700;letter-spacing:.06em}
.cm{display:grid;grid-template-columns:44px minmax(0,1fr);gap:16px;padding:20px 0;border-top:1px solid #E2E3E8}
.cm .av{width:44px;height:44px;display:grid;place-items:center;color:#fff;font-weight:800;border-radius:0 12px 0 12px}
'''
ADMIN_CSS = BASE_CSS + '''
:root{--primary:#E8285B;--primary-solid:#D92153}
body{font-size:14px}
.btn{display:inline-flex;align-items:center;justify-content:center;gap:6px;min-height:36px;padding:0 14px;font-weight:700;font-size:13px;border:0;border-radius:0 9px 0 9px;cursor:pointer;background:#15161A;color:#fff;font-family:inherit;white-space:nowrap}
.btn.p{background:var(--primary-solid)}.btn.t{background:#EDEEF1;color:#15161A}.btn.x{background:transparent;color:#15161A}.btn.i{width:36px;padding:0}.btn.s{min-height:30px;font-size:12px;padding:0 10px}.btn.s.i{width:30px}.btn.danger{background:#FBE1DE;color:#9C2318}
.rail{background:#15161A;color:#fff;display:flex;flex-direction:column;padding:14px 10px;gap:2px}
.ns{font-size:11px;letter-spacing:.08em;color:#7E838F;padding:14px 12px 4px;text-transform:uppercase;font-weight:700}
.nl{display:flex;align-items:center;gap:10px;min-height:34px;padding:0 12px;border-radius:0 10px 0 10px;color:#C9CBD3;font-weight:600;font-size:13.5px}
.nl:hover{background:#2A2B31;color:#fff}.nl.on{background:var(--primary-solid);color:#fff}
.nl .n{margin-left:auto;font-size:11px;background:#2A2B31;padding:1px 7px;border-radius:0 6px 0 6px;color:#C9CBD3}.nl.on .n{background:#fff;color:var(--primary-solid)}
.nl.new:after{content:"新";margin-left:auto;font-size:10px;padding:1px 6px;border-radius:0 5px 0 5px;background:#13C2A3;color:#0B3A31}
.nl.new .n{display:none}
table{width:100%;border-collapse:collapse;font-size:13.5px}
.cell-title{font-weight:700}
input::placeholder,textarea::placeholder{color:#5F6470;opacity:1}
@media(max-width:640px){.cell-title{min-width:12rem}}
th{text-align:left;font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:#5F6470;padding:8px 10px;border-bottom:1px solid #E2E3E8;font-weight:700;white-space:nowrap}
td{padding:10px;border-bottom:1px solid #EEEFF2;vertical-align:middle}
tr:hover td{background:#F8F8FA}
td.act{text-align:right;white-space:nowrap}td.act .btn{opacity:.55}tr:hover td.act .btn{opacity:1}
.cb{width:16px;height:16px;border-radius:0 5px 0 5px;box-shadow:inset 0 0 0 1.5px #B9BCC6;display:inline-block;vertical-align:middle;background:#fff}
.cb.on{background:#15161A;box-shadow:none;position:relative}.cb.on:after{content:"";position:absolute;left:5px;top:2px;width:4px;height:8px;border:solid #fff;border-width:0 2px 2px 0;transform:rotate(45deg)}
.card{background:#fff;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 14px 0 14px;padding:16px}
.srow{display:grid;grid-template-columns:minmax(0,1fr) minmax(260px,.9fr);gap:8px 32px;align-items:center;padding:12px 0;border-top:1px solid #EEEFF2}
.srow label{font-weight:700;font-size:14px;color:#15161A;display:block}.srow .h{font-size:12px;color:#5F6470;margin-top:2px}
.srow .c{display:flex;align-items:center;justify-content:flex-end;gap:8px}
.sheet{position:absolute;top:0;right:0;bottom:0;width:440px;background:#fff;box-shadow:-1px 0 0 #E2E3E8,-24px 0 48px rgba(21,22,26,.08);border-radius:0 0 0 24px;display:flex;flex-direction:column}
.tree a{display:flex;align-items:center;gap:8px;min-height:34px;padding:0 10px;border-radius:0 9px 0 9px;color:#15161A;font-weight:600;font-size:13.5px}
.tree a.on{background:#15161A;color:#fff}.tree a .n{margin-left:auto;font-size:11px;color:#7E838F;font-weight:700}.tree a.on .n{color:#fff}
.tree .lv1{padding-left:28px}.tree .lv2{padding-left:46px}
.mi{display:flex;align-items:center;gap:10px;min-height:40px;padding:0 10px 0 6px;background:#fff;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 10px 0 10px;font-weight:600;font-size:13.5px}
.mi .k{font-size:11px;color:#5F6470;font-weight:700;margin-left:auto}
.mi.drop{box-shadow:inset 0 0 0 2px #2F6BFF;background:#F0F4FF}
.diff{font:13px/1.7 ui-monospace,"SF Mono",Menlo,monospace;white-space:pre-wrap}
.diff .a{background:#FBE1DE;color:#9C2318;text-decoration:line-through;padding:0 2px}.diff .b{background:#DCF3E9;color:#0F6A45;padding:0 2px}
.log{font:12.5px/1.7 ui-monospace,"SF Mono",Menlo,monospace}
.log .r{display:grid;grid-template-columns:150px 64px 110px minmax(0,1fr);gap:12px;padding:6px 10px;border-bottom:1px solid #EEEFF2;align-items:baseline}
.log .r:hover{background:#F8F8FA}
.lv{font-weight:800;font-size:11px;letter-spacing:.06em;padding:1px 6px;border-radius:0 5px 0 5px;text-align:center}
.lv.i{background:#DCE6FF;color:#1A3F9E}.lv.w{background:#FCF0C8;color:#7A5200}.lv.e{background:#FBE1DE;color:#9C2318}.lv.d{background:#EDEEF1;color:#5F6470}
.pal{display:flex;align-items:center;gap:10px;min-height:40px;padding:0 12px;border-radius:0 10px 0 10px;font-weight:600;font-size:14px;color:#15161A}
.pal.on{background:#15161A;color:#fff}.pal .k{margin-left:auto;font-size:11px;color:#9A9FAB;font-weight:700}.pal.on .k{color:#C9CBD3}
.ph{display:flex;flex-direction:column;gap:8px}.ph i{display:block;height:12px;border-radius:0 5px 0 5px;background:linear-gradient(90deg,#EDEEF1 0 40%,#F6F6F8 50%,#EDEEF1 60%);background-size:200% 100%;animation:sh 1.4s linear infinite}
@keyframes sh{to{background-position:-200% 0}}
.step{display:flex;align-items:center;gap:8px;font-weight:700;font-size:13px}
.step i{width:24px;height:24px;display:grid;place-items:center;border-radius:0 8px 0 8px;background:#EDEEF1;color:#5F6470;font-style:normal;font-family:"Bricolage Grotesque",sans-serif;font-size:12px}
.step.on i{background:#15161A;color:#fff}.step.done i{background:#13C2A3;color:#fff}
.av{width:28px;height:28px;display:grid;place-items:center;color:#fff;font-weight:800;font-size:12px;border-radius:0 9px 0 9px;flex:0 0 auto}
'''

def cubes(scale=1.0, left=0, top=0, op=1):
    # the jelly cube pile as absolutely positioned squares
    s = lambda v: f"{v*scale:.0f}px"
    return (f'<div style="position:absolute;left:{left}px;top:{top}px;width:{s(170)};height:{s(200)};opacity:{op}" aria-hidden="true">'
      f'<span class="cube" style="left:{s(20)};top:{s(18)};width:{s(64)};height:{s(64)};background:#2F6BFF;opacity:.85"></span>'
      f'<span class="cube" style="left:{s(52)};top:{s(60)};width:{s(64)};height:{s(64)};background:#13C2A3;opacity:.85"></span>'
      f'<span class="cube" style="left:{s(84)};top:{s(104)};width:{s(64)};height:{s(64)};background:#E8285B;opacity:.92;transform:rotate(-7deg)"></span>'
      f'<span class="cube" style="left:{s(24)};top:{s(154)};width:{s(28)};height:{s(28)};background:#F5B800"></span>'
      f'<span class="cube" style="left:{s(126)};top:{s(34)};width:{s(20)};height:{s(20)};background:#15161A;opacity:.9"></span></div>')

def doc(title, css, body, w, h):
    return f'''<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<title>{title}</title>
<script src="./support.js"></script>
</head>
<body>
<x-dc>
<helmet>
{FONTS}
<style>{css}</style>
</helmet>
{body}
</x-dc>
<script type="text/x-dc" data-dc-script data-props='{{"$preview":{{"width":{w},"height":{h}}}}}'>
class Component extends DCLogic {{
  renderVals() {{ return {{}}; }}
}}
</script>
</body>
</html>
'''

# ---------- front shell ----------
def front(active, main, cart=True):
    nav = [("Main.dc.html","首頁","home"),("PostList.dc.html","文章","posts"),("Shop.dc.html","商店","shop"),("Page.dc.html","關於","about")]
    links = "".join(('<a href="%s"%s>%s</a>' % (h, ' class="on"' if k == active else '', t)) for h, t, k in nav)
    cartbtn = f'<a class="btn t" href="Cart.dc.html">{I("cart")}購物車<span style="background:#E8285B;color:#fff;font-size:11px;min-width:18px;height:18px;display:inline-grid;place-items:center;border-radius:0 6px 0 6px">2</span></a>' if cart else ""
    return f'''<div style="width: 100%; box-sizing: border-box; display: flex; flex-direction: column; background: #fff; min-height: 100vh">
  <div class="topline"></div>
  <header style="display: flex; align-items: center; gap: 24px; padding: 14px 40px; max-width: 1360px; margin: 0 auto; width: 100%; box-sizing: border-box">
    <a href="Main.dc.html" style="display: inline-flex; align-items: center; gap: 12px; color: #15161A; font-size: 20px" class="d">{MARK.format(ink="#15161A")}榛果繽紛樂</a>
    <nav class="nav" style="display: flex; gap: 4px; margin-left: auto">{links}</nav>
    {cartbtn}
  </header>
  {main}
  <footer style="background: #15161A; color: #EDEDF0; padding: 48px 40px; margin-top: auto"><div style="max-width: 1360px; margin: 0 auto; display: flex; justify-content: space-between; font-size: 13px; color: #9A9FAB"><span>© 榛果繽紛樂</span><span>Powered by Jelly CMS</span></div></footer>
</div>'''

# ---------- admin shell ----------
NAV = [
 ("內容", [("dashboard","Dashboard.dc.html","儀表板","home",None),("posts","Posts.dc.html","文章","doc",None),("pages","Posts.dc.html","頁面","page",None),
          ("taxonomy","Taxonomy.dc.html","分類與標籤","tag","new"),("comments","Comments.dc.html","留言","comment","7"),("media","Media.dc.html","附件","image",None),("trash","Trash.dc.html","回收桶","trash","new")]),
 ("商店", [("products","Products.dc.html","商品","box",None),("orders","Orders.dc.html","訂單","cart","3"),("coupons","Coupons.dc.html","折扣碼","ticket",None)]),
 ("外觀", [("themes","Themes.dc.html","主題","theme",None),("menus","Menus.dc.html","導覽選單","menu","new"),("plugins","Plugins.dc.html","外掛","plug",None)]),
 ("系統", [("users","Users.dc.html","使用者","users","new"),("redirects","Redirects.dc.html","轉址與 SEO","redirect","new"),("tools","Import.dc.html","工具","tools","new"),("logs","Logs.dc.html","日誌","terminal","new"),("settings","Settings.dc.html","設定","settings",None)]),
]
def rail(active):
    out = []
    for sec, items in NAV:
        out.append(f'<div class="ns">{sec}</div>')
        for k,h,t,ic,badge in items:
            cls = "nl" + (" on" if k==active else "") + (" new" if badge=="new" else "")
            n = f'<span class="n">{badge}</span>' if badge and badge!="new" else ""
            out.append(f'<a class="{cls}" href="{h}">{I(ic)}{t}{n}</a>')
    return "\n    ".join(out)

def admin(active, crumbs, actions, main, extra="", search=True):
    crumb = "".join((f'<span>{c}</span>' if i < len(crumbs)-1 else f'<b style="color:#15161A">{c}</b>') + ('<span>/</span>' if i < len(crumbs)-1 else "") for i,c in enumerate(crumbs))
    s = f'<label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:34px;width:260px;color:#5F6470;font-size:13px">{I("search")}<input type="text" placeholder="搜尋或輸入指令…" aria-label="搜尋" style="border:0;background:transparent;font:inherit;flex:1;min-width:0;color:#15161A"><span class="kbd">⌘K</span></label>' if search else ""
    return f'''<div style="width: 100%; min-height: 100vh; display: flex; flex-wrap: wrap; background: #fff; position: relative">
  <aside class="rail" style="flex: 0 0 224px">
    <a href="Dashboard.dc.html" style="display: flex; align-items: center; gap: 10px; padding: 6px 12px 10px; color: #fff; font-size: 17px" class="d">{MARK.format(ink="#fff")}Jelly CMS</a>
    {rail(active)}
    <div style="margin-top: auto; display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 0 10px 0 10px; background: #2A2B31"><span style="width: 30px; height: 30px; display: grid; place-items: center; background: var(--primary-solid); font-weight: 800; border-radius: 0 9px 0 9px">A</span><div><div style="font-weight: 700; font-size: 13px">管理員</div><a href="Profile.dc.html" style="color: #9A9FAB; font-size: 12px">個人帳號</a></div></div>
  </aside>
  <div style="flex: 999 1 560px; min-width: 0; display: flex; flex-direction: column">
    <div class="topline"></div>
    <header style="display: flex; align-items: center; gap: 12px; padding: 10px 24px; border-bottom: 1px solid #EEEFF2">
      <nav style="display: flex; align-items: center; gap: 8px; color: #5F6470; font-size: 13px" aria-label="路徑">{crumb}</nav>
      {s}
      <div style="margin-left: auto; display: flex; align-items: center; gap: 8px">{actions}</div>
    </header>
    {main}
  </div>
  {extra}
</div>'''

# ---------- shared by Profile / Tokens / Notifications ----------
SUB_CSS = '''
.sub a{display:flex;align-items:center;gap:8px;min-height:34px;padding:0 10px;border-radius:0 9px 0 9px;color:#5F6470;font-weight:600;font-size:13px}
.sub a.on{background:#F3F3F5;color:#15161A}'''

def profile_nav(on):
    items=[("user","基本資料"),("mail","通知"),("key","API 權杖"),("lock","密碼與安全"),("globe","登入裝置"),("link","第三方帳號")]
    return '<nav class="sub" style="display:flex;flex-direction:column;gap:2px;position:sticky;top:16px">'+"".join(('<a href="Profile.dc.html"%s>%s%s</a>' % (' class="on"' if l==on else '', I(ic,14), l)) for ic,l in items)+'</nav>'

pages = {}

# ======================= FRONT: Checkout =======================
pages["Checkout"] = ("前台結帳", FRONT_CSS, 1440, 1400, front("shop", f'''
  <main style="max-width: 1360px; margin: 0 auto; width: 100%; box-sizing: border-box; padding: 32px 40px 96px; display: flex; flex-direction: column; gap: 32px">
    <div style="display: flex; align-items: flex-end; justify-content: space-between; gap: 24px; flex-wrap: wrap">
      <div style="display: flex; flex-direction: column; gap: 12px"><span class="stripe"></span><h1 class="d" style="font-size: 56px; letter-spacing: -.04em">結帳</h1></div>
      <div style="display: flex; gap: 20px"><span class="step done"><i>{I("check",14)}</i>購物車</span><span class="step on"><i>2</i>結帳</span><span class="step"><i>3</i>付款</span><span class="step"><i>4</i>完成</span></div>
    </div>
    <div style="display: grid; grid-template-columns: minmax(0, 1fr) 380px; gap: 48px; align-items: start">
      <form style="display: flex; flex-direction: column; gap: 36px" onsubmit="return false">
        <section style="display: flex; flex-direction: column; gap: 16px">
          <h2 class="d" style="font-size: 24px; display: flex; align-items: center; gap: 10px"><span class="chip nd" style="background:#15161A;color:#fff">1</span>聯絡方式</h2>
          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 16px">
            <div><label class="flbl" for="em">Email</label><input id="em" class="field" type="email" value="ting@example.com"><div style="font-size:12px;color:#5F6470;margin-top:6px">訂單通知與收據會寄到這裡。</div></div>
            <div><label class="flbl" for="ph">手機</label><input id="ph" class="field" type="tel" placeholder="09xx xxx xxx"></div>
          </div>
          <label style="display: inline-flex; align-items: center; gap: 8px; font-size: 14px"><input type="checkbox" style="accent-color:#E8285B;width:16px;height:16px;margin:0">建立帳號，下次免填（可選）</label>
          <div style="font-size:13px;color:#5F6470">已經有帳號？<a href="Login.dc.html">登入</a>。</div>
          <div style="font-size:12px;color:#5F6470">會員結帳會帶入本人 Email、預設地址與發票設定，並隱藏建立帳號及登入提示；已自行編輯的欄位不會被較晚載入的資料覆蓋。</div>
        </section>
        <section style="display: flex; flex-direction: column; gap: 16px">
          <h2 class="d" style="font-size: 24px; display: flex; align-items: center; gap: 10px"><span class="chip nd" style="background:#15161A;color:#fff">2</span>配送</h2>
          <div style="display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 10px">
            <label class="opt on"><span class="rad"></span><span>{I("truck")}</span><span><b>宅配</b><div style="font-size:12px;color:#5F6470">NT$ 80 · 約 3 天</div></span></label>
            <label class="opt" aria-disabled="true" style="opacity:.55;cursor:not-allowed;background:#F8F8FA"><span class="rad" style="box-shadow:inset 0 0 0 2px #D9DBE0"></span><span>{I("box")}</span><span><b>超商取貨</b><div style="font-size:12px;color:#5F6470;margin-top:2px"><span class="chip nd">稍後開放</span></div></span></label>
            <label class="opt"><span class="rad"></span><span>{I("home")}</span><span><b>自取</b><div style="font-size:12px;color:#5F6470">免運 · 約 1 天 · 取貨說明</div></span></label>
          </div>
          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 16px">
            <div><label class="flbl" for="nm">收件人</label><input id="nm" class="field" type="text" value="賴庭蓁"></div>
            <div><label class="flbl" for="zip">郵遞區號</label><input id="zip" class="field" type="text" placeholder="407"></div>
            <div style="grid-column: 1 / -1"><label class="flbl" for="ad">地址</label><input id="ad" class="field" type="text" placeholder="台中市西屯區…"></div>
          </div>
          <div style="font-size:12px;color:#5F6470">純數位訂單會隱藏配送與地址欄位，可直接結帳；零額的實體商品仍要選配送方式。</div>
        </section>
        <section style="display: flex; flex-direction: column; gap: 16px">
          <h2 class="d" style="font-size: 24px; display: flex; align-items: center; gap: 10px"><span class="chip nd" style="background:#15161A;color:#fff">3</span>付款與發票</h2>
          <div style="font-size:12px;color:#5F6470">只列出站長在設定裡啟用的付款方式；運費與到貨天數來自運送設定。</div>
          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px">
            <label class="opt on"><span class="rad"></span><span>{I("card")}</span><span><b>信用卡</b><div style="font-size:12px;color:#5F6470">前往付款頁刷卡</div></span></label>
            <label class="opt"><span class="rad"></span><span>{I("db")}</span><span><b>ATM 轉帳</b><div style="font-size:12px;color:#5F6470">取得虛擬帳號後匯款</div></span></label>
            <label class="opt"><span class="rad"></span><span>{I("box")}</span><span><b>超商代碼</b><div style="font-size:12px;color:#5F6470">取得繳費代碼到超商繳費</div></span></label>
            <label class="opt"><span class="rad"></span><span>{I("home")}</span><span><b>銀行轉帳</b><div style="font-size:12px;color:#5F6470">下單後依轉帳說明匯款，人工確認</div></span></label>
          </div>
          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 16px">
            <div><label class="flbl" for="inv">發票</label><select id="inv" class="field"><option>手機條碼載具</option><option>個人（Email 收據）</option><option>公司統編</option><option>捐贈發票</option></select></div>
            <div><label class="flbl" for="bc">手機條碼</label><input id="bc" class="field" type="text" placeholder="/ABC1234"></div>
          </div>
        </section>
      </form>
      <aside style="position: sticky; top: 24px; background: #15161A; color: #fff; border-radius: 0 24px 0 24px; padding: 28px; display: flex; flex-direction: column; gap: 16px">
        <div class="lbl" style="color: #B9BCC6">你的訂單</div>
        <div style="display: flex; gap: 12px; align-items: center"><span style="width:48px;height:48px;background:#E8285B;border-radius:0 10px 0 10px;flex:0 0 auto"></span><span style="flex:1"><b>榛果可可醬 200g</b><div style="font-size:12px;color:#B9BCC6">× 1</div></span><b>NT$ 320</b></div>
        <div style="display: flex; gap: 12px; align-items: center"><span style="width:48px;height:48px;background:#2F6BFF;border-radius:0 10px 0 10px;flex:0 0 auto"></span><span style="flex:1"><b>藍莓果凍 6 入</b><div style="font-size:12px;color:#B9BCC6">× 2</div></span><b>NT$ 480</b></div>
        <div style="display: grid; grid-template-columns: 1fr auto; gap: 8px; font-size: 15px; padding-top: 16px; border-top: 1px solid #2A2B31"><span style="color:#B9BCC6">小計</span><b>NT$ 800</b><span style="color:#B9BCC6">運費（預估）· 宅配</span><b>NT$ 80</b><span style="color:#B9BCC6">尾數調整（四捨五入至整元）</span><b>NT$ 0</b></div>
        <div style="display: flex; align-items: baseline; justify-content: space-between; padding-top: 16px; border-top: 1px solid #2A2B31"><span>總計</span><span class="d" style="font-size: 36px">NT$ 880</span></div>
        <div style="font-size: 12px; color: #9A9FAB; line-height: 1.6">價格已含稅。實際金額以送出時系統重新計算為準。</div>
        <label style="display: flex; align-items: flex-start; gap: 10px; font-size: 13px; line-height: 1.6; color: #E3E4E8"><input type="checkbox" checked style="accent-color:#E8285B;width:16px;height:16px;margin:2px 0 0;flex:0 0 auto">我已閱讀並同意<a href="Page.dc.html" style="color:#8FB0FF">購物須知與退換貨政策</a></label>
        <a class="btn p" href="OrderDone.dc.html" style="min-height: 56px; font-size: 17px">{I("lock")}前往付款 NT$ 880</a>
        <div style="font-size:12px;color:#9A9FAB;line-height:1.6">應付金額四捨五入後若為 NT$ 0，改顯示「確認訂單（無需付款）」，不要求選付款方式。</div>
        <div style="font-size: 12px; color: #9A9FAB; line-height: 1.6">信用卡、ATM 與超商代碼會前往付款頁；銀行轉帳送出後會顯示匯款說明。付款完成後回到本站看訂單結果。</div>
        <a href="Cart.dc.html" style="color: #C9CBD3; font-size: 13px; display: inline-flex; align-items: center; gap: 6px">{I("arrow-l",14)}回購物車修改</a>
      </aside>
    </div>
  </main>'''))

# ======================= FRONT: OrderDone =======================
pages["OrderDone"] = ("前台付款完成", FRONT_CSS, 1440, 1000, front("shop", f'''
  <main style="max-width: 1360px; margin: 0 auto; width: 100%; box-sizing: border-box; padding: 32px 40px 96px; display: flex; flex-direction: column; gap: 32px">
    <div style="display: flex; gap: 20px; justify-content: flex-end"><span class="step done"><i>{I("check",14)}</i>購物車</span><span class="step done"><i>{I("check",14)}</i>結帳</span><span class="step done"><i>{I("check",14)}</i>付款</span><span class="step on"><i>4</i>完成</span></div>
    <div style="display: grid; grid-template-columns: minmax(0, 1.1fr) minmax(0, 1fr); gap: 48px; align-items: start">
      <section style="position: relative; background: #13C2A3; color: #0B3A31; border-radius: 0 48px 0 48px; padding: 56px 48px 48px; min-height: 420px; overflow: hidden">
        {cubes(1.3, 430, 300, .9)}
        <span class="stripe" style="background: linear-gradient(90deg,#0B3A31 0 52%,transparent 52% 60%,#0B3A31 60% 82%,transparent 82% 88%,#0B3A31 88% 100%)"></span>
        <div class="lbl" style="color:#0B5E4E;margin-top:16px">付款成功</div>
        <h1 class="d" style="font-size: clamp(40px, 4vw, 52px); letter-spacing: -.04em; margin: 8px 0 20px">收到了，<br>正在幫你準備。</h1>
        <p style="margin: 0; font-size: 17px; line-height: 1.7; max-width: 24em">訂單編號 <b class="mono">JL-2026-0417</b>。確認信將寄至 ting@example.com，出貨時會再通知一次。</p>
        <div style="display: flex; gap: 10px; margin-top: 32px; flex-wrap: wrap"><a class="btn t" href="Shop.dc.html">{I("arrow-l")}繼續逛</a></div>
      </section>
      <aside style="display: flex; flex-direction: column; gap: 20px">
        <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px">
          <div style="background:#F5F5F7;border-radius:0 14px 0 14px;padding:16px;display:flex;flex-direction:column;gap:6px"><span class="lbl">付款方式</span><b style="display:flex;align-items:center;gap:8px">{I("card")}信用卡（綠界）</b><span style="font-size:13px;color:#5F6470">末四碼 4321 · 一次付清</span></div>
          <div style="background:#F5F5F7;border-radius:0 14px 0 14px;padding:16px;display:flex;flex-direction:column;gap:6px"><span class="lbl">配送</span><b style="display:flex;align-items:center;gap:8px">{I("truck")}宅配 · 約 3 天</b><span style="font-size:13px;color:#5F6470">台中市西屯區…</span></div>
        </div>
        <div style="box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 16px 0 16px;padding:20px;display:flex;flex-direction:column;gap:14px">
          <div class="lbl">明細</div>
          <div style="display:flex;gap:12px;align-items:center"><span style="width:44px;height:44px;background:#E8285B;border-radius:0 10px 0 10px"></span><span style="flex:1"><b>榛果可可醬 200g</b><div style="font-size:12px;color:#5F6470">× 1</div></span><b>NT$ 320</b></div>
          <div style="display:flex;gap:12px;align-items:center"><span style="width:44px;height:44px;background:#2F6BFF;border-radius:0 10px 0 10px"></span><span style="flex:1"><b>藍莓果凍 6 入</b><div style="font-size:12px;color:#5F6470">× 2</div></span><b>NT$ 480</b></div>
          <div style="display:grid;grid-template-columns:1fr auto;gap:6px;font-size:14px;padding-top:12px;border-top:1px solid #E2E3E8"><span style="color:#5F6470">小計</span><b>NT$ 800</b><span style="color:#5F6470">運費</span><b>NT$ 80</b><span style="color:#5F6470">內含稅額</span><b>NT$ 42</b><span style="color:#5F6470">尾數調整</span><b>NT$ 0</b><span style="font-weight:700">總計</span><span class="mono" style="font-size:22px">NT$ 880</span></div>
          <div style="font-size:12px;color:#5F6470">無需付款的訂單顯示「訂單已成立」，不標示付款成功。</div>
        </div>
        <div style="display:flex;gap:10px;flex-wrap:wrap"><button class="btn t sm" type="button">{I("download")}列印收據</button><button class="btn t sm" type="button">{I("mail")}重寄確認信</button><a class="btn t sm" href="Page.dc.html">{I("comment")}聯絡我們</a></div>
      </aside>
    </div>
    <section style="background:#FBE1DE;color:#9C2318;border-radius:0 16px 0 16px;padding:16px 20px;display:flex;align-items:center;gap:12px;max-width:720px">{I("alert")}<div style="font-size:14px"><b>付款失敗時長這樣：</b>整塊換成覆盆子底，標題「付款沒有完成」，主要按鈕變成「重新付款」，副按鈕「換付款方式」，並寫出訂單保留到幾點（依付款方式的保留設定）。ATM、超商代碼與轉帳換成黃底「還差一步」並列出付款資訊與期限；金流尚未回報時顯示「正在等待付款結果」並自動更新。</div></section>
  </main>'''))

# ======================= FRONT: Archive (category + comments) =======================
pages["Archive"] = ("前台分類頁與留言區", FRONT_CSS, 1440, 1700, front("posts", f'''
  <section style="background:#2F6BFF;color:#fff;padding:56px 40px 40px;position:relative;overflow:hidden">
    {cubes(1.6, 1080, -10, .9)}
    <div style="max-width:1360px;margin:0 auto;display:flex;flex-direction:column;gap:12px;position:relative">
      <span class="stripe" style="background:linear-gradient(90deg,#fff 0 52%,transparent 52% 60%,#fff 60% 82%,transparent 82% 88%,#fff 88% 100%)"></span>
      <div class="lbl" style="color:#C9D8FF">分類 · 18 篇</div>
      <h1 class="d" style="font-size:72px;letter-spacing:-.04em">食譜</h1>
      <p style="margin:0;font-size:17px;max-width:30em;color:#E3EBFF">做過才敢寫。每篇食譜都有份量、時間，和哪一步最容易失敗。</p>
      <div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:8px"><span class="chip nd" style="background:#fff;color:#15161A">全部</span><a class="chip nd" href="Archive.dc.html" style="background:rgba(255,255,255,.18);color:#fff">甜點</a><a class="chip nd" href="Archive.dc.html" style="background:rgba(255,255,255,.18);color:#fff">抹醬</a><a class="chip nd" href="Archive.dc.html" style="background:rgba(255,255,255,.18);color:#fff">飲料</a></div>
    </div>
  </section>
  <main style="max-width:1360px;margin:0 auto;width:100%;box-sizing:border-box;padding:24px 40px 96px;display:grid;grid-template-columns:minmax(0,1fr) 300px;gap:48px;align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:12px;padding-bottom:16px"><span style="color:#5F6470;font-size:14px">第 1 頁，共 2 頁</span><div class="seg" style="margin-left:auto"><span class="on">{I("list",14)}列表</span><span>{I("grid",14)}格狀</span></div><a class="btn t sm" href="Archive.dc.html">{I("refresh")}RSS</a></div>
      <article class="entry"><div><div class="day mono">21</div><div class="ym">2026 · 10</div></div><div><h3><a href="Post.dc.html">零失敗焦糖布丁，重點在火</a></h3><p>焦糖只有十秒的窗口。這篇把爐火、鍋子、顏色都拍下來，照著做第一次就會成功。</p><div style="display:flex;gap:8px;margin-top:12px;align-items:center"><span class="chip">甜點</span><span style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:4px">{I("comment",14)}12</span></div></div></article>
      <article class="entry"><div><div class="day mono">14</div><div class="ym">2026 · 10</div></div><div><h3><a href="Post.dc.html">榛果醬自己做，比買的便宜一半</a></h3><p>烤 12 分鐘、打 4 分鐘，剩下的時間都在等它涼。附三種濃稠度的比例。</p><div style="display:flex;gap:8px;margin-top:12px;align-items:center"><span class="chip">抹醬</span><span style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:4px">{I("comment",14)}4</span></div></div></article>
      <article class="entry"><div><div class="day mono">02</div><div class="ym">2026 · 10</div></div><div><h3><a href="Post.dc.html">夏天的薄荷果凍，用吉利丁還是寒天</a></h3><p>兩種都做了。口感、融化速度、脫模難度一張表看完。</p><div style="display:flex;gap:8px;margin-top:12px;align-items:center"><span class="chip">甜點</span><span style="font-size:13px;color:#5F6470;display:inline-flex;align-items:center;gap:4px">{I("comment",14)}27</span></div></div></article>

      <section style="margin-top:56px;padding-top:32px;border-top:3px solid #15161A">
        <div style="display:flex;align-items:baseline;gap:16px"><h2 class="d" style="font-size:32px">留言區</h2><span style="color:#5F6470">文章頁底部長這樣 · 12 則</span></div>
        <form style="display:grid;grid-template-columns:44px minmax(0,1fr);gap:16px;margin-top:20px;padding:20px;background:#F5F5F7;border-radius:0 16px 0 16px" onsubmit="return false">
          <span style="width:44px;height:44px;background:#15161A;color:#fff;display:grid;place-items:center;font-weight:800;border-radius:0 12px 0 12px">你</span>
          <div style="display:flex;flex-direction:column;gap:10px"><textarea class="field" rows="3" placeholder="說點什麼。支援 Markdown，留言會先審核。" style="padding:12px 14px;resize:vertical"></textarea><div style="display:grid;grid-template-columns:1fr 1fr auto;gap:10px"><input class="field" type="text" placeholder="名字" style="background:#fff"><input class="field" type="email" placeholder="Email（不公開）" style="background:#fff"><button class="btn p" type="button">{I("comment")}送出</button></div></div>
        </form>
        <div class="cm"><span class="av" style="background:#2F6BFF">小</span><div><div style="display:flex;align-items:center;gap:10px"><b>小魚</b><span style="font-size:12px;color:#5F6470">2 天前</span><span class="chip ok" style="margin-left:4px">作者</span></div><p style="margin:8px 0;line-height:1.7">焦糖我總是煮過頭…這次看圖對顏色真的有差，謝謝！</p><div style="display:flex;gap:4px"><button class="btn x sm" type="button">{I("reply",14)}回覆</button><button class="btn x sm" type="button">{I("star",14)}3</button></div></div></div>
        <div class="cm" style="padding-left:60px"><span class="av" style="background:#E8285B">H</span><div><div style="display:flex;align-items:center;gap:10px"><b>Hazel</b><span class="chip" style="background:#E8285B;color:#fff">站長</span><span style="font-size:12px;color:#5F6470">1 天前</span></div><p style="margin:8px 0;line-height:1.7">對，顏色到「琥珀」就要離火，餘溫還會再深一階。</p><div style="display:flex;gap:4px"><button class="btn x sm" type="button">{I("reply",14)}回覆</button></div></div></div>
        <div class="cm"><span class="av" style="background:#F5B800;color:#3A2E00">阿</span><div><div style="display:flex;align-items:center;gap:10px"><b>阿凱</b><span style="font-size:12px;color:#5F6470">剛剛</span><span class="chip warn">待審核，只有你看得到</span></div><p style="margin:8px 0;line-height:1.7">請問可以用黑糖嗎？</p></div></div>
      </section>
    </section>
    <aside style="display:flex;flex-direction:column;gap:28px;position:sticky;top:24px">
      <div><div class="lbl" style="margin-bottom:10px">其他分類</div><nav style="display:flex;flex-direction:column;gap:2px"><a href="Archive.dc.html" style="display:flex;justify-content:space-between;padding:8px 10px;color:#15161A;font-weight:600">開店日記<span style="color:#5F6470">9</span></a><a href="Archive.dc.html" style="display:flex;justify-content:space-between;padding:8px 10px;color:#15161A;font-weight:600">產地<span style="color:#5F6470">6</span></a><a href="Archive.dc.html" style="display:flex;justify-content:space-between;padding:8px 10px;color:#15161A;font-weight:600">公告<span style="color:#5F6470">3</span></a></nav></div>
      <div><div class="lbl" style="margin-bottom:10px">標籤</div><div style="display:flex;gap:6px;flex-wrap:wrap"><a class="chip nd" href="Archive.dc.html">#榛果</a><a class="chip nd" href="Archive.dc.html">#吉利丁</a><a class="chip nd" href="Archive.dc.html">#焦糖</a><a class="chip nd" href="Archive.dc.html">#冰</a><a class="chip nd" href="Archive.dc.html">#新手</a><a class="chip nd" href="Archive.dc.html">#失敗紀錄</a></div></div>
      <div><div class="lbl" style="margin-bottom:10px">作者頁、月份彙整</div><p style="margin:0;font-size:13px;color:#5F6470;line-height:1.7">同一版型：藍色頭換成薄荷（作者）或果凍黃（月份），標題換成作者名或「2026 年 10 月」。</p></div>
    </aside>
  </main>'''))

# ======================= ADMIN: Taxonomy =======================
def row_tax(name, slug, n, lv=0, color=None, kind="分類"):
    pad = f' style="padding-left:{10+lv*22}px"' if lv else ""
    dot = f'<span style="width:10px;height:10px;background:{color};border-radius:0 3px 0 3px;display:inline-block;margin-right:8px;vertical-align:middle"></span>' if color else ""
    arrow = '<span style="color:#B9BCC6;margin-right:6px">└</span>' if lv else ""
    return f'<tr><td style="width:36px"><span class="cb"></span></td><td{pad}>{arrow}{dot}<b>{name}</b></td><td style="color:#5F6470">/{slug}</td><td class="mono" style="font-size:15px">{n}</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="看前台">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>'

def prow(name, slug, n, lv=0, color=None, sel=False):
    pad = f' style="padding-left:{10+lv*22}px"' if lv else ""
    dot = f'<span style="width:10px;height:10px;background:{color};border-radius:0 3px 0 3px;display:inline-block;margin-right:8px;vertical-align:middle"></span>' if color else ""
    arrow = '<span style="color:#B9BCC6;margin-right:6px">└</span>' if lv else ""
    bg = ' style="background:#F8F9FF"' if sel else ""
    return f'<tr{bg}><td style="width:36px"><span class="cb{" on" if sel else ""}"></span></td><td{pad}>{arrow}{dot}<b>{name}</b></td><td style="color:#5F6470">/shop/c/{slug}</td><td class="mono" style="font-size:15px">{n}</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="看前台">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>'

pages["Taxonomy"] = ("後台分類與標籤", ADMIN_CSS + "\n@media(max-width:700px){.seg{flex-wrap:wrap;max-width:100%;box-sizing:border-box}}\n", 1440, 1300, admin("taxonomy", ["內容","分類與標籤"],
  f'<button class="btn t" type="button">{I("upload")}從 WordPress 匯入</button><button class="btn p" type="button">{I("plus")}新增分類</button>',
  f'''<main style="padding: 16px 24px 32px; display: grid; grid-template-columns: minmax(0, 1fr) 360px; gap: 24px; align-items: start">
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span class="on">{I("folder",14)}分類 <b>7</b></span><span>{I("tag",14)}標籤 <b>42</b></span><span>{I("box",14)}商品分類 <b>5</b></span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="篩選" aria-label="篩選" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label><button class="btn t s" type="button">{I("drag")}拖曳排序</button></div>
      <table>
        <thead><tr><th><span class="cb"></span></th><th>名稱</th><th>網址</th><th>文章</th><th></th></tr></thead>
        <tbody>
          {row_tax("食譜","recipes",18,0,"#2F6BFF")}
          {row_tax("甜點","recipes/desserts",11,1)}
          {row_tax("抹醬","recipes/spreads",5,1)}
          {row_tax("飲料","recipes/drinks",2,1)}
          {row_tax("開店日記","diary",9,0,"#E8285B")}
          {row_tax("產地","origin",6,0,"#13C2A3")}
          {row_tax("公告","news",3,0,"#F5B800")}
        </tbody>
      </table>
      <div style="display:flex;align-items:center;gap:10px;margin-top:12px;font-size:12px;color:#5F6470">{I("alert",14)}刪除分類不會刪文章，文章會回到「未分類」。勾選後可一次合併到另一個分類。</div>
      <div style="margin-top:28px"><div class="lbl" style="margin-bottom:10px">標籤（預覽前 12 個，依使用量）</div><div style="display:flex;gap:6px;flex-wrap:wrap"><span class="chip nd" style="font-size:13px;min-height:28px">榛果 <b style="color:#5F6470">14</b></span><span class="chip nd" style="font-size:13px;min-height:28px">新手 <b style="color:#5F6470">11</b></span><span class="chip nd" style="font-size:13px;min-height:28px">吉利丁 <b style="color:#5F6470">9</b></span><span class="chip nd" style="font-size:13px;min-height:28px">焦糖 <b style="color:#5F6470">7</b></span><span class="chip nd" style="font-size:13px;min-height:28px">冰 <b style="color:#5F6470">6</b></span><span class="chip nd" style="font-size:13px;min-height:28px">失敗紀錄 <b style="color:#5F6470">5</b></span><span class="chip nd" style="font-size:13px;min-height:28px">台中 <b style="color:#5F6470">5</b></span><span class="chip nd" style="font-size:13px;min-height:28px">禮盒 <b style="color:#5F6470">3</b></span><span class="chip nd warn" style="font-size:13px;min-height:28px">沒用到 × 6</span></div></div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">編輯：甜點</b><span class="chip info" style="margin-left:auto">食譜 的子分類</span></div>
      <div><span class="lbl">名稱</span><input class="ul" type="text" value="甜點"></div>
      <div><span class="lbl">網址</span><div style="display:flex;align-items:center;gap:6px"><span style="color:#9A9FAB;font-size:13px">/recipes/</span><input class="ul" type="text" value="desserts"></div></div>
      <div><span class="lbl">上層分類</span><select class="ul"><option>食譜</option><option>（無）</option><option>開店日記</option></select></div>
      <div><span class="lbl">顏色（前台卡片會用）</span><div style="display:flex;gap:6px;margin-top:6px"><span style="width:28px;height:28px;background:#2F6BFF;border-radius:0 9px 0 9px;box-shadow:0 0 0 2px #fff,0 0 0 4px #15161A"></span><span style="width:28px;height:28px;background:#E8285B;border-radius:0 9px 0 9px"></span><span style="width:28px;height:28px;background:#13C2A3;border-radius:0 9px 0 9px"></span><span style="width:28px;height:28px;background:#F5B800;border-radius:0 9px 0 9px"></span><span style="width:28px;height:28px;background:#15161A;border-radius:0 9px 0 9px"></span></div></div>
      <div><span class="lbl">描述（SEO 與分類頁開頭）</span><textarea class="ul" rows="3">做過才敢寫。每篇食譜都有份量、時間，和哪一步最容易失敗。</textarea></div>
      <div style="display:flex;gap:8px;margin-top:4px"><button class="btn p" type="button">{I("save")}儲存</button><button class="btn t" type="button">{I("external")}看前台</button><button class="btn x i" type="button" aria-label="刪除" style="margin-left:auto;color:#9C2318">{I("trash")}</button></div>
    </aside>
  </main>
  <div style="margin:4px 24px 0;padding-top:14px;border-top:1px dashed #D9DBE0;display:flex;align-items:center;gap:10px;color:#5F6470;font-size:13px">{I("box",14)}<b style="color:#15161A">切到「商品分類」分段</b>商品有自己的分類樹，網址是 <span class="mono">/shop/c/…</span>，跟文章分類互不相干。</div>
  <main style="padding: 16px 24px 32px; display: grid; grid-template-columns: minmax(0, 1fr) 360px; gap: 24px; align-items: start">
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span>{I("folder",14)}分類 <b>7</b></span><span>{I("tag",14)}標籤 <b>42</b></span><span class="on">{I("box",14)}商品分類 <b>5</b></span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="篩選" aria-label="篩選商品分類" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label><button class="btn p s" type="button">{I("plus")}新增商品分類</button></div>
      <table>
        <thead><tr><th><span class="cb"></span></th><th>名稱</th><th>網址</th><th>商品</th><th></th></tr></thead>
        <tbody>
          {prow("果凍","jelly",18,0,"#2F6BFF")}
          {prow("水果","jelly-fruit",8,1,sel=True)}
          {prow("茶與咖啡","jelly-tea-coffee",5,1)}
          {prow("禮盒","gift-box",6,0,"#F5B800")}
          {prow("器具","tools",4,0,"#13C2A3")}
        </tbody>
      </table>
      <div style="display:flex;align-items:center;gap:10px;margin-top:12px;font-size:12px;color:#5F6470">{I("alert",14)}分類底下還有子分類或商品時不能刪除，先把商品移出、子分類移走再刪。一個商品可以同時放進多個分類，父分類的數量含子分類；隱藏的分類不出現在商店列表。</div>
      <div class="card" style="margin-top:20px;display:flex;gap:12px;align-items:flex-start;background:#F8F9FF">
        <span style="width:36px;height:36px;display:grid;place-items:center;background:#2F6BFF;color:#fff;border-radius:0 10px 0 10px;flex:0 0 auto">{I("box")}</span>
        <div style="font-size:13px;line-height:1.7"><b>商品分類跟文章分類是兩棵樹。</b><br>文章的「食譜」「甜點」不會出現在商店，商品的「果凍」「禮盒」也不會出現在文章。兩邊可以有同名分類，網址各自獨立：文章在 <span class="mono">/recipes</span>，商品在 <span class="mono">/shop/c/jelly</span>。</div>
      </div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">編輯：水果</b><span class="chip info" style="margin-left:auto">果凍 的子分類</span></div>
      <div><span class="lbl">名稱</span><input class="ul" type="text" value="水果"></div>
      <div><span class="lbl">網址</span><div style="display:flex;align-items:center;gap:6px"><span style="color:#9A9FAB;font-size:13px">/shop/c/</span><input class="ul" type="text" value="jelly-fruit"></div></div>
      <div><span class="lbl">上層分類</span><select class="ul"><option>果凍</option><option>（無）</option><option>禮盒</option><option>器具</option></select></div>
      <div><span class="lbl">封面圖網址（分類頁頂端）</span><input class="ul" type="text" value="/uploads/jelly-fruit.jpg"></div>
      <div><span class="lbl">描述（SEO 與分類頁開頭）</span><textarea class="ul" rows="3">真的水果榨汁做的，不加香精。冷藏寄出，兩天內到貨最好吃。</textarea></div>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13px;font-weight:600">在商店隱藏（網址仍可進入）<span class="tg off"></span></label>
      <div style="display:flex;gap:8px;margin-top:4px"><button class="btn p" type="button">{I("save")}儲存</button><button class="btn t" type="button">{I("external")}看前台</button><button class="btn x i" type="button" aria-label="刪除" style="margin-left:auto;color:#9C2318">{I("trash")}</button></div>
      <div style="display:flex;gap:8px;align-items:flex-start;padding:10px 12px;background:#FBE1DE;color:#9C2318;border-radius:0 10px 0 10px;font-size:12px;line-height:1.6">{I("alert",14)}<span><b>無法刪除「水果」</b><br>這個分類裡還有 8 個商品，先把商品移出再刪。</span></div>
    </aside>
  </main>'''))

# ======================= ADMIN: Menus =======================
def mi(label, kind, lv=0, drop=False, extra=""):
    cls = "mi" + (" drop" if drop else "")
    drag = I("drag", 16, ' style="color:#B9BCC6;cursor:grab"')
    chv = I("chevron-d", 14)
    return f'<div class="{cls}" style="margin-left:{lv*28}px">{drag}<span>{label}</span><span class="chip nd" style="margin-left:4px">{kind}</span><span class="k">{extra}</span><button class="btn x s i" type="button" aria-label="展開">{chv}</button></div>'

pages["Menus"] = ("後台導覽選單", ADMIN_CSS, 1440, 1000, admin("menus", ["外觀","導覽選單"],
  f'<span style="font-size:12px;color:#7A5200;background:#FCF0C8;padding:4px 10px;border-radius:0 7px 0 7px;font-weight:700">1 項未儲存</span><button class="btn t" type="button">{I("eye")}預覽</button><button class="btn p" type="button">{I("save")}儲存選單</button>',
  f'''<main style="padding: 16px 24px 32px; display: grid; grid-template-columns: 300px minmax(0, 1fr); gap: 24px; align-items: start">
    <aside style="display:flex;flex-direction:column;gap:16px">
      <div class="card" style="display:flex;flex-direction:column;gap:6px">
        <div class="lbl" style="margin-bottom:4px">選單</div>
        <div class="tree"><a class="on" href="Menus.dc.html">{I("menu",14)}主選單<span class="n">頁首</span></a><a href="Menus.dc.html">{I("menu",14)}頁尾<span class="n">頁尾左</span></a><a href="Menus.dc.html">{I("menu",14)}手機側欄<span class="n">未指派</span></a></div>
        <button class="btn t s" type="button" style="margin-top:6px;align-self:flex-start">{I("plus")}新增選單</button>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:10px">
        <div class="lbl">加入項目</div>
        <div class="seg" style="align-self:flex-start"><span class="on">頁面</span><span>分類</span><span>文章</span><span>商店</span><span>自訂</span></div>
        <label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;color:#5F6470;font-size:13px">{I("search",14)}<input type="text" placeholder="找頁面" aria-label="找頁面" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label>
        <div style="display:flex;flex-direction:column;gap:4px;font-size:13.5px">
          <label style="display:flex;align-items:center;gap:8px"><span class="cb on"></span>關於我們</label>
          <label style="display:flex;align-items:center;gap:8px"><span class="cb"></span>門市資訊</label>
          <label style="display:flex;align-items:center;gap:8px"><span class="cb on"></span>常見問題</label>
          <label style="display:flex;align-items:center;gap:8px"><span class="cb"></span>隱私權政策</label>
          <label style="display:flex;align-items:center;gap:8px"><span class="cb"></span>退換貨說明</label>
        </div>
        <button class="btn s" type="button" style="align-self:flex-start">{I("arrow-r")}加到選單（2）</button>
      </div>
    </aside>
    <section style="display:grid;grid-template-columns:minmax(0,1fr) 320px;gap:24px;align-items:start">
      <div>
        <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><h2 class="d" style="font-size:20px">主選單</h2><span class="chip ok">顯示在 頁首</span><span style="margin-left:auto;font-size:12px;color:#5F6470">拖曳排序，往右拖變成子項目。最多兩層。</span></div>
        <div style="display:flex;flex-direction:column;gap:6px">
          {mi("首頁","首頁")}
          {mi("文章","分類頁",0,False,"18 篇")}
          {mi("食譜","分類",1)}
          {mi("開店日記","分類",1)}
          {mi("商店","商店")}
          {mi("關於我們","頁面",0,True)}
          {mi("常見問題","頁面",1,True,"剛加入")}
          {mi("IG","自訂連結",0,False,"新分頁 ↗")}
        </div>
        <div style="margin-top:12px;padding:12px;border:1.5px dashed #D9DBE0;border-radius:0 10px 0 10px;color:#5F6470;font-size:13px;text-align:center">把項目拖到這裡加到最後</div>
      </div>
      <aside class="card" style="display:flex;flex-direction:column;gap:12px;position:sticky;top:16px">
        <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">項目：關於我們</b></div>
        <div><span class="lbl">顯示文字</span><input class="ul" type="text" value="關於我們"></div>
        <div><span class="lbl">連到</span><div style="display:flex;align-items:center;gap:8px;font-size:13px;margin-top:6px">{I("page",14)}頁面「關於我們」<a href="Menus.dc.html" style="margin-left:auto;font-size:12px">換一個</a></div></div>
        <div><span class="lbl">圖示（可選）</span><div style="display:flex;gap:6px;margin-top:6px"><span class="btn t s i" style="box-shadow:inset 0 0 0 2px #15161A">{I("user",14)}</span><span class="btn t s i">{I("home",14)}</span><span class="btn t s i">{I("star",14)}</span><span class="btn t s i">{I("mail",14)}</span><span class="btn t s i">{I("plus",14)}</span></div></div>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13.5px;font-weight:600">開新分頁<span class="tg off"></span></label>
        <label style="display:flex;align-items:center;justify-content:space-between;font-size:13.5px;font-weight:600">只給登入者看<span class="tg off"></span></label>
        <div><span class="lbl">CSS class</span><input class="ul" type="text" placeholder="可留空"></div>
        <div style="display:flex;gap:8px;margin-top:4px"><button class="btn t s" type="button">{I("check")}套用</button><button class="btn x s" type="button" style="margin-left:auto;color:#9C2318">{I("trash")}移除</button></div>
      </aside>
    </section>
  </main>'''))

# ======================= ADMIN: Comments =======================
def crow(av, color, name, meta, text, post, state, cls):
    return f'''<tr><td style="width:36px"><span class="cb"></span></td>
<td style="width:260px"><div style="display:flex;gap:10px;align-items:flex-start"><span class="av" style="background:{color}">{av}</span><div><b>{name}</b><div style="font-size:12px;color:#5F6470">{meta}</div></div></div></td>
<td><div style="line-height:1.6;max-width:52ch">{text}</div><div style="display:flex;gap:4px;margin-top:6px"><button class="btn x s" type="button" style="color:#0F6A45">{I("check",14)}核准</button><button class="btn x s" type="button">{I("reply",14)}回覆</button><button class="btn x s" type="button">{I("edit",14)}編輯</button><button class="btn x s" type="button" style="color:#9C2318">{I("spam",14)}垃圾</button><button class="btn x s" type="button" style="color:#9C2318">{I("trash",14)}</button></div></td>
<td style="width:200px"><a href="Post.dc.html" style="color:#15161A;font-weight:600">{post}</a><div style="font-size:12px;color:#5F6470">2 天前</div></td>
<td style="width:110px"><span class="chip {cls}">{state}</span></td></tr>'''

pages["Comments"] = ("後台留言管理", ADMIN_CSS, 1440, 1000, admin("comments", ["內容","留言"],
  f'<button class="btn t" type="button">{I("settings")}留言設定</button><button class="btn p" type="button">{I("check")}核准全部待審（7）</button>',
  f'''<main style="padding: 16px 24px 32px">
    <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px;flex-wrap:wrap">
      <div class="seg"><span>全部 <b>184</b></span><span class="on">待審 <b style="color:#F5B800">7</b></span><span>已核准 <b>171</b></span><span>垃圾 <b>6</b></span><span>回收桶 <b>0</b></span></div>
      <label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:240px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="找留言、名字、Email" aria-label="搜尋留言" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label>
      <button class="btn t s" type="button">{I("filter")}文章</button>
    </div>
    <div style="display:flex;align-items:center;gap:8px;padding:8px 10px;background:#F8F8FA;border-radius:0 9px 0 9px;margin-bottom:4px;font-size:13px"><span class="cb"></span><span style="color:#5F6470">已選 0 則</span><button class="btn t s" type="button" disabled style="opacity:.5">{I("check",14)}核准</button><button class="btn t s" type="button" disabled style="opacity:.5">{I("spam",14)}垃圾</button><button class="btn t s" type="button" disabled style="opacity:.5">{I("trash",14)}刪除</button><span style="margin-left:auto;color:#5F6470">按 <span class="kbd">J</span>/<span class="kbd">K</span> 上下、<span class="kbd">A</span> 核准、<span class="kbd">S</span> 垃圾</span></div>
    <table>
      <thead><tr><th></th><th>誰</th><th>內容</th><th>在哪篇</th><th>狀態</th></tr></thead>
      <tbody>
        {crow("阿","#F5B800;color:#3A2E00","阿凱","kai@example.com · 1.160.x.x","請問可以用黑糖嗎？","零失敗焦糖布丁，重點在火","待審","warn")}
        {crow("M","#2F6BFF","Mei","mei@example.com · 第 3 則留言","做了兩次都成功，第二次把糖減 10g 更合我口味。附上照片 📷","零失敗焦糖布丁，重點在火","待審","warn")}
        {crow("?","#9A9FAB","casino-best-2026","spam@…ru · 第一次留言","Check out <span style='background:#FBE1DE;padding:0 4px'>best casino bonus</span> http://…","榛果醬自己做，比買的便宜一半","疑似垃圾","bad")}
        {crow("小","#13C2A3","小魚","yu@example.com · 第 12 則留言","焦糖我總是煮過頭…這次看圖對顏色真的有差，謝謝！","零失敗焦糖布丁，重點在火","已核准","ok")}
        {crow("H","#E8285B","Hazel（你）","站長 · 回覆小魚","對，顏色到「琥珀」就要離火，餘溫還會再深一階。","零失敗焦糖布丁，重點在火","已核准","ok")}
      </tbody>
    </table>
    <div style="display:flex;align-items:center;gap:12px;margin-top:14px;font-size:13px;color:#5F6470"><span>顯示 1–5，共 7 則</span><div style="margin-left:auto;display:flex;gap:6px"><button class="btn t s i" type="button" aria-label="上一頁">{I("arrow-l",14)}</button><button class="btn t s i" type="button" aria-label="下一頁">{I("arrow-r",14)}</button></div></div>
  </main>''',
  extra=f'''<div class="sheet" style="width:420px">
    <div style="display:flex;align-items:center;gap:10px;padding:16px 20px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">回覆 阿凱</b><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x")}</button></div>
    <div style="padding:16px 20px;display:flex;flex-direction:column;gap:12px;flex:1">
      <div style="background:#F5F5F7;border-radius:0 10px 0 10px;padding:12px;font-size:13px;line-height:1.6"><b>阿凱：</b>請問可以用黑糖嗎？<div style="font-size:12px;color:#5F6470;margin-top:4px">在「零失敗焦糖布丁，重點在火」</div></div>
      <textarea class="ul" rows="6" placeholder="用站長身分回覆。送出後會自動核准原留言。" style="line-height:1.6">可以，但黑糖的焦糖化溫度比較低，顏色會比較難判斷。建議第一次先用白砂糖。</textarea>
      <div style="display:flex;gap:6px;flex-wrap:wrap"><span class="chip nd">{I("check",12)}送出時一併核准</span><span class="chip nd">{I("mail",12)}Email 通知對方</span></div>
    </div>
    <div style="padding:12px 20px;border-top:1px solid #EEEFF2;display:flex;gap:8px"><button class="btn p" type="button">{I("reply")}送出回覆</button><button class="btn t" type="button">{I("x")}取消</button></div>
  </div>'''))

# ======================= ADMIN: Users =======================
USER_MENU = lambda: f'''<div role="menu" aria-label="小魚的更多操作" style="position:absolute;right:6px;top:38px;z-index:5;width:188px;padding:6px;background:#fff;border-radius:0 12px 0 12px;box-shadow:0 0 0 1px #E2E3E8,0 12px 32px rgba(21,22,26,.14);display:flex;flex-direction:column;gap:2px;text-align:left"><button class="btn x s" role="menuitem" type="button" style="justify-content:flex-start;opacity:1;width:100%">{I("lock",14)}登出所有裝置</button><button class="btn x s" role="menuitem" type="button" style="justify-content:flex-start;opacity:1;width:100%;color:#9C2318">{I("trash",14)}刪除</button></div>'''

def urow(av,color,name,email,role,rcls,last,state,scls,me=False,menu=False):
    return f'''<tr><td style="width:36px"><span class="cb"></span></td><td><div style="display:flex;gap:10px;align-items:center"><span class="av" style="background:{color}">{av}</span><div><b>{name}</b>{' <span class="chip nd" style="margin-left:4px">你</span>' if me else ''}<div style="font-size:12px;color:#5F6470">{email}</div></div></div></td><td><span class="chip {rcls}">{role}</span></td><td style="color:#5F6470">{last}</td><td><span class="chip {scls}">{state}</span></td><td class="act"{' style="position:relative"' if menu else ''}><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="重設密碼">{I("key")}</button><button class="btn x s i" type="button" aria-label="更多"{' aria-haspopup="menu" aria-expanded="true" style="opacity:1;background:#EDEEF1"' if menu else ''}>{I("more")}</button>{USER_MENU() if menu else ''}</td></tr>'''

pages["Users"] = ("後台使用者與角色", ADMIN_CSS, 1440, 1000, admin("users", ["系統","使用者"],
  f'<button class="btn t" type="button">{I("shield")}角色與權限</button><button class="btn p" type="button">{I("plus")}邀請使用者</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: minmax(0,1fr) 380px; gap:24px; align-items:start">
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span class="on">全部 <b>6</b></span><span>管理員 2</span><span>編輯 2</span><span>作者 1</span><span>顧客 1</span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="名字或 Email" aria-label="搜尋使用者" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label></div>
      <table>
        <thead><tr><th></th><th>使用者</th><th>角色</th><th>最後登入</th><th>狀態</th><th></th></tr></thead>
        <tbody>
          {urow("H","#E8285B","Hazel","hazel@example.com","管理員","bad","剛剛 · Chrome / macOS","啟用","ok",True)}
          {urow("T","#2F6BFF","Ting","ting@example.com","管理員","bad","昨天 · Safari / iPhone","啟用","ok")}
          {urow("小","#13C2A3","小魚","yu@example.com","編輯","info","3 天前","啟用","ok",menu=True)}
          {urow("J","#F5B800;color:#3A2E00","Jamie","jamie@example.com","編輯","info","從未登入","邀請中 · 2 天後過期","warn")}
          {urow("W","#15161A","Wen","wen@example.com","作者","mint","上個月","啟用","ok")}
          {urow("K","#9A9FAB","kai@example.com","來自商店訂單","顧客","","—","未啟用","")}
        </tbody>
      </table>
      <div style="margin-top:24px"><div class="lbl" style="margin-bottom:10px">角色能做什麼</div>
        <table style="font-size:13px"><thead><tr><th>權限</th><th style="text-align:center">管理員</th><th style="text-align:center">編輯</th><th style="text-align:center">作者</th><th style="text-align:center">顧客</th></tr></thead>
        <tbody>
          <tr><td>發布、編輯所有文章與頁面</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#B9BCC6">只有自己的</td><td style="text-align:center;color:#B9BCC6">—</td></tr>
          <tr><td>審核留言</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#B9BCC6">—</td><td style="text-align:center;color:#B9BCC6">—</td></tr>
          <tr><td>商品與訂單</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#B9BCC6">—</td><td style="text-align:center;color:#B9BCC6">—</td><td style="text-align:center;color:#5F6470">看自己的訂單</td></tr>
          <tr><td>主題、外掛、設定、使用者</td><td style="text-align:center;color:#0F6A45">{I("check",16)}</td><td style="text-align:center;color:#B9BCC6">—</td><td style="text-align:center;color:#B9BCC6">—</td><td style="text-align:center;color:#B9BCC6">—</td></tr>
        </tbody></table>
      </div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">邀請使用者</b></div>
      <div><span class="lbl">Email</span><input class="ul" type="email" placeholder="name@example.com"></div>
      <div><span class="lbl">角色</span>
        <div style="display:flex;flex-direction:column;gap:6px;margin-top:8px">
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 2px #15161A;background:#F8F8FA"><span class="cb on" style="margin-top:2px"></span><span><b>編輯</b><div style="font-size:12px;color:#5F6470">管所有內容和留言，碰不到設定和商店。</div></span></label>
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8"><span class="cb" style="margin-top:2px"></span><span><b>作者</b><div style="font-size:12px;color:#5F6470">只能寫和發自己的文章。</div></span></label>
          <label style="display:flex;gap:10px;align-items:flex-start;padding:10px;border-radius:0 10px 0 10px;box-shadow:inset 0 0 0 1px #E2E3E8"><span class="cb" style="margin-top:2px"></span><span><b>管理員</b><div style="font-size:12px;color:#9C2318">什麼都能做，包含刪站。想清楚。</div></span></label>
        </div>
      </div>
      <div><span class="lbl">附一句話（可選）</span><textarea class="ul" rows="2" placeholder="嗨，來幫我顧站～"></textarea></div>
      <div style="font-size:12px;color:#5F6470;line-height:1.6">邀請連結 7 天有效。對方第一次登入會被要求設密碼，建議順便開兩步驟驗證。</div>
      <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("mail")}寄出邀請</button><button class="btn t" type="button">{I("copy")}複製連結</button></div>
    </aside>
  </main>''',
  extra=f'''<div class="sheet" role="dialog" aria-label="刪除使用者" style="width:400px">
    <div style="display:flex;align-items:center;gap:10px;padding:16px 20px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">刪除使用者</b><button class="btn x s i" type="button" aria-label="關閉" style="margin-left:auto">{I("x")}</button></div>
    <div style="padding:16px 20px;display:flex;flex-direction:column;gap:14px;flex:1;overflow:auto">
      <div style="display:flex;align-items:center;gap:12px"><span class="av" style="background:#13C2A3;width:40px;height:40px;font-size:16px;border-radius:0 12px 0 12px">小</span><div><b>小魚</b><div style="font-size:12px;color:#5F6470">yu@example.com · 編輯</div></div></div>
      <div><span class="lbl">這個帳號名下還有</span>
        <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin-top:8px">
          <div style="padding:10px 12px;background:#F5F5F7;border-radius:0 10px 0 10px;display:flex;align-items:baseline;gap:6px;white-space:nowrap">{I("doc",14)}<span style="font-size:13px">文章</span><b class="d" style="font-size:20px">12</b></div>
          <div style="padding:10px 12px;background:#F5F5F7;border-radius:0 10px 0 10px;display:flex;align-items:baseline;gap:6px;white-space:nowrap">{I("page",14)}<span style="font-size:13px">頁面</span><b class="d" style="font-size:20px">2</b></div>
          <div style="padding:10px 12px;background:#F5F5F7;border-radius:0 10px 0 10px;display:flex;align-items:baseline;gap:6px;white-space:nowrap">{I("comment",14)}<span style="font-size:13px">留言</span><b class="d" style="font-size:20px">31</b></div>
        </div></div>
      <div><label class="lbl" for="rt">轉移給</label><select id="rt" class="ul"><option>Hazel · 管理員</option><option>Ting · 管理員</option><option>Wen · 作者</option></select><div style="font-size:12px;color:#5F6470;margin-top:6px;line-height:1.5">只能選啟用中、能寫作的人。文章、頁面和留言的作者會改成他。</div></div>
      <div style="display:flex;gap:10px;align-items:flex-start;padding:12px;background:#FBE1DE;color:#9C2318;border-radius:0 10px 0 10px;font-size:13px;line-height:1.6"><span style="margin-top:2px">{I("alert",16)}</span><span><b>刪除後無法復原。</b>小魚在所有裝置上的登入會立刻登出，Google、GitHub 綁定也一併移除。</span></div>
    </div>
    <div style="padding:12px 20px;border-top:1px solid #EEEFF2;display:flex;gap:8px"><button class="btn danger" type="button">{I("trash")}轉移並刪除</button><button class="btn t" type="button">{I("x")}取消</button></div>
  </div>'''))

# ======================= ADMIN: Profile =======================
def tfa_card(state, chip, body):
    return f'<div class="card" style="display:flex;flex-direction:column;gap:12px;position:relative"><div style="display:flex;align-items:center;gap:8px"><span class="lbl">{state}</span><span style="margin-left:auto">{chip}</span></div>{body}</div>'

RECOVERY = ["7QK2-9XMD","H4TA-RN8E","B6WP-3ZCJ","M9LV-5YQF","D2GS-8KHT","X5NR-4PUB","J8CE-6VAW","T3FY-7MZD","K9QH-2LSX","R4BN-5WGE"]
pages["Profile"] = ("後台個人帳號", ADMIN_CSS + SUB_CSS, 1440, 1000, admin("", ["個人帳號"],
  f'<button class="btn p" type="button">{I("save")}儲存</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: 200px minmax(0,1fr); gap:32px; align-items:start">
    {profile_nav("基本資料")}
    <section style="max-width:936px;display:flex;flex-direction:column;gap:32px">
      <div style="display:flex;align-items:center;gap:20px"><span style="width:72px;height:72px;display:grid;place-items:center;background:#E8285B;color:#fff;font-size:28px;border-radius:0 22px 0 22px" class="d">H</span><div><h1 class="d" style="font-size:26px">Hazel</h1><div style="color:#5F6470;font-size:13px">管理員 · 加入於 2026/03 · 寫了 41 篇</div></div><div style="margin-left:auto;display:flex;gap:6px"><button class="btn t s" type="button">{I("upload")}換頭像</button><a class="btn t s" href="Archive.dc.html">{I("external")}作者頁</a></div></div>
      <div>
        <div class="lbl" style="margin-bottom:4px">基本資料</div>
        <div class="srow"><div><label for="p1">顯示名稱</label><div class="h">前台文章署名用這個。</div></div><div class="c"><input id="p1" class="ul" type="text" value="Hazel"></div></div>
        <div class="srow"><div><label for="p2">Email</label><div class="h">登入與通知。改了要輸入密碼，並到新信箱按確認連結。</div></div><div class="c"><input id="p2" class="ul" type="email" value="hazel@example.com"></div></div>
        <div class="srow"><div><label for="p3">簡介</label><div class="h">作者頁與文章末尾的小卡。</div></div><div class="c"><textarea id="p3" class="ul" rows="2">在台中做果凍，順便寫字。</textarea></div></div>
        <div class="srow"><div><label for="p4">介面語言</label></div><div class="c"><div class="seg"><span class="on">繁中</span><span>English</span></div></div></div>
        <div class="srow"><div><label>外觀</label><div class="h">後台配色。</div></div><div class="c"><div class="seg"><span>跟系統</span><span class="on">淺色</span><span>深色</span></div></div></div>
      </div>
      <div>
        <div class="lbl" style="margin-bottom:4px">密碼與安全</div>
        <div class="srow"><div><label>密碼</label><div class="h">上次更改：4 個月前。改完其他裝置會全部登出。</div></div><div class="c"><button class="btn t s" type="button">{I("key")}更改密碼</button></div></div>
        <div class="srow" style="grid-template-columns:minmax(0,1fr)"><div><label>兩步驟驗證</label><div class="h">登入時除了密碼，還要輸入驗證器 App 的 6 位數驗證碼。用 Google 或 GitHub 登入不受影響。下面是三種狀態。</div></div></div>
        <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;align-items:start">
          {tfa_card("1 · 尚未啟用", '<span class="chip nd">未開啟</span>', f"""<span style="width:44px;height:44px;display:grid;place-items:center;background:#EDEEF1;border-radius:0 12px 0 12px">{I("shield",22)}</span>
            <div style="font-size:13px;line-height:1.6;color:#5F6470">帳號被盜用時，多一道關卡。需要 Google Authenticator、1Password 之類的驗證器 App。</div>
            <button class="btn p" type="button" style="align-self:flex-start">{I("shield")}啟用兩步驟驗證</button>""")}
          {tfa_card("2 · 設定中", '<span class="chip warn">尚未驗證</span>', f"""<div style="display:flex;gap:12px;align-items:center"><div role="img" aria-label="QR code 位置" style="width:104px;height:104px;flex:0 0 auto;display:grid;place-items:center;background:#F3F3F5;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 12px 0 12px;color:#9A9FAB">{I("grid",44)}</div><div style="font-size:12px;line-height:1.6;color:#5F6470">用驗證器 App 掃這個 QR code。</div></div>
            <div style="font-size:12px;color:#5F6470">掃不了？手動輸入密鑰<div style="margin-top:4px"><span class="kbd" style="font-size:12px;letter-spacing:.04em;user-select:all">JBSW Y3DP EHPK 3PXP</span></div></div>
            <div><span class="lbl">驗證碼</span><input class="ul" type="text" inputmode="numeric" maxlength="6" autocomplete="one-time-code" placeholder="000000" aria-label="6 位數驗證碼" style="font-family:'Bricolage Grotesque',sans-serif;font-weight:800;font-size:20px;letter-spacing:.35em"></div>
            <div style="display:flex;gap:6px"><button class="btn p" type="button">{I("check")}驗證並啟用</button><button class="btn x" type="button">{I("x")}取消</button></div>""")}
          {tfa_card("3 · 已啟用", '<span class="chip ok">已開啟</span>', f"""<div style="font-size:13px;color:#5F6470;line-height:1.6">2026/08/14 啟用。每次登入都要輸入驗證碼。</div>
            <div style="display:flex;align-items:center;gap:10px;padding:10px 12px;background:#F5F5F7;border-radius:0 10px 0 10px"><span style="width:32px;height:32px;display:grid;place-items:center;background:#15161A;color:#fff;border-radius:0 9px 0 9px">{I("key",16)}</span><div><b>備用碼 剩 8 組</b><div style="font-size:12px;color:#5F6470">手機不在身邊時用，一組只能用一次。</div></div></div>
            <div style="display:flex;gap:6px;flex-wrap:wrap"><button class="btn t s" type="button">{I("refresh",14)}重新產生備用碼</button><button class="btn danger s" type="button">{I("x",14)}關閉</button></div>
            <div style="font-size:12px;color:#5F6470">重新產生會讓舊的 10 組全部失效；關閉要輸入密碼與驗證碼。</div>""")}
        </div>
        <div style="display:grid;grid-template-columns:minmax(0,360px) minmax(0,1fr);gap:24px;align-items:start;margin-top:20px">
          <div role="dialog" aria-label="備用碼" style="background:#fff;border-radius:0 24px 0 24px;box-shadow:0 0 0 1px #E2E3E8,0 24px 48px rgba(21,22,26,.1);display:flex;flex-direction:column">
            <div style="display:flex;align-items:center;gap:10px;padding:16px 20px;border-bottom:1px solid #EEEFF2"><span class="stripe" style="width:40px"></span><b style="font-size:15px">備用碼</b><span class="chip warn" style="margin-left:auto">只顯示這一次</span></div>
            <div style="padding:16px 20px;display:flex;flex-direction:column;gap:12px">
              <div style="font-size:13px;line-height:1.6;color:#5F6470">每組只能用一次。關掉這張卡就看不到了，請現在存到密碼管理員或印出來。</div>
              <div style="display:grid;grid-template-columns:1fr 1fr;gap:6px 12px;padding:14px;background:#F6F7F9;border-radius:0 12px 0 12px">
                {"".join(f'<span style="font:700 14px/1.9 ui-monospace,SF Mono,Menlo,Consolas,monospace;letter-spacing:.04em"><span style="color:#9A9FAB;font-size:11px;margin-right:6px">{i+1:02d}</span>{c}</span>' for i,c in enumerate(RECOVERY))}
              </div>
            </div>
            <div style="padding:12px 20px;border-top:1px solid #EEEFF2;display:flex;gap:8px"><button class="btn t" type="button">{I("copy")}複製</button><button class="btn p" type="button">{I("check")}我已存好</button></div>
          </div>
          <div style="font-size:12.5px;line-height:1.7;color:#5F6470;padding-top:6px">啟用成功後會自動出現這張卡，重新產生備用碼後也會出現。「我已存好」之前不能關。</div>
        </div>
      </div>
      <div>
        <div class="lbl" style="margin-bottom:4px">登入裝置</div>
        <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label style="display:flex;align-items:center;gap:8px">Chrome · macOS<span class="chip ok">這台</span></label><div class="h">1.160.12.x · 登入於 今天 09:12 · 最近活動 現在</div></div><div class="c"><span style="font-size:12px;color:#5F6470">目前</span></div></div>
        <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label>Safari · iPhone</label><div class="h">42.77.31.x · 登入於 10/18 · 最近活動 昨天 22:14</div></div><div class="c"><button class="btn x s" type="button" style="color:#9C2318">{I("x")}登出它</button></div></div>
        <div class="srow" style="grid-template-columns:minmax(0,1fr) auto"><div><label style="display:flex;align-items:center;gap:8px">Firefox · Windows<span class="chip warn">新裝置</span></label><div class="h">114.25.88.x · 登入於 3 天前 · 最近活動 3 天前</div></div><div class="c"><button class="btn x s" type="button" style="color:#9C2318">{I("x")}登出它</button></div></div>
        <button class="btn t s" type="button" style="margin-top:10px">{I("lock")}登出其他所有裝置</button>
      </div>
    </section>
  </main>'''))

# ======================= ADMIN: Trash =======================
def trow(kind, ic, title, who, when, left, cls=""):
    return f'<tr><td style="width:36px"><span class="cb"></span></td><td><div style="display:flex;align-items:center;gap:10px"><span class="chip nd">{I(ic,12)}{kind}</span><b>{title}</b></div></td><td style="color:#5F6470">{who}</td><td style="color:#5F6470">{when}</td><td><span class="chip {cls}">{left}</span></td><td class="act"><button class="btn t s" type="button">{I("undo",14)}還原</button><button class="btn x s i" type="button" aria-label="永久刪除" style="color:#9C2318">{I("trash")}</button></td></tr>'

pages["Trash"] = ("後台回收桶", ADMIN_CSS, 1440, 1000, admin("trash", ["內容","回收桶"],
  f'<button class="btn t" type="button">{I("undo")}全部還原</button><button class="btn danger" type="button">{I("trash")}清空回收桶（9）</button>',
  f'''<main style="padding: 16px 24px 32px">
    <div style="display:flex;align-items:center;gap:14px;padding:14px 18px;background:#F5F5F7;border-radius:0 14px 0 14px;margin-bottom:16px">{I("clock",20)}<div style="font-size:13.5px;line-height:1.5"><b>東西放這裡 30 天</b>，期間可以還原。到期自動永久刪除，附件的檔案也會一起從硬碟移除。<a href="Settings.dc.html" style="margin-left:6px">改保留天數</a></div></div>
    <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span class="on">全部 <b>9</b></span><span>文章 4</span><span>頁面 1</span><span>附件 2</span><span>商品 1</span><span>留言 1</span></div><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:220px;color:#5F6470;font-size:13px;margin-left:auto">{I("search",14)}<input type="text" placeholder="找被刪的東西" aria-label="搜尋回收桶" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label></div>
    <table>
      <thead><tr><th></th><th>項目</th><th>誰刪的</th><th>何時</th><th>剩餘</th><th></th></tr></thead>
      <tbody>
        {trow("文章","doc","草稿：中秋禮盒預購（未完成）","Hazel","今天 14:02","30 天","ok")}
        {trow("文章","doc","舊版門市公告","Ting","3 天前","27 天","ok")}
        {trow("頁面","page","2025 活動頁","Hazel","12 天前","18 天","ok")}
        {trow("附件","image","IMG_4821.heic · 4.2 MB","小魚","20 天前","10 天","warn")}
        {trow("附件","image","banner-old.png · 820 KB","Hazel","26 天前","4 天","warn")}
        {trow("商品","box","試賣：芒果果凍","Ting","28 天前","2 天","bad")}
        {trow("留言","comment","「請問有素食版…」 by 阿凱","Hazel","29 天前","明天刪","bad")}
        {trow("文章","doc","測試文章請忽略","Hazel","29 天前","明天刪","bad")}
      </tbody>
    </table>
  </main>''',
  extra=f'''<div class="jd-scrim"></div>
  <div class="jd" role="alertdialog" aria-label="永久刪除">
    <span class="close">{I("x",14)}</span>
    <div style="display:flex;align-items:center;gap:10px;padding-right:34px"><span style="width:40px;height:40px;display:grid;place-items:center;background:#FBE1DE;color:#9C2318;border-radius:0 12px 0 12px;flex:0 0 auto">{I("alert",20)}</span><b style="font-size:16px">永久刪除「試賣：芒果果凍」？</b></div>
    <p style="margin:0;font-size:13.5px;line-height:1.6;color:#5F6470">刪了就真的沒了，連同 3 張商品圖。有 2 筆舊訂單引用這個商品，訂單會保留但商品名字會變灰。</p>
    <div><span class="lbl">輸入 <b style="color:#15161A">刪除</b> 確認</span><input class="ul" type="text" placeholder="刪除"></div>
    <div style="display:flex;gap:8px;justify-content:flex-end"><button class="btn t" type="button">{I("x")}取消</button><button class="btn danger" type="button">{I("trash")}永久刪除</button></div>
  </div>'''))

# ======================= ADMIN: Revisions =======================
pages["Revisions"] = ("後台版本比較", ADMIN_CSS, 1440, 1000, admin("posts", ["內容","文章","零失敗焦糖布丁","版本"],
  f'<button class="btn t" type="button">{I("arrow-l")}回編輯器</button><button class="btn p" type="button">{I("undo")}還原到這個版本</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: 280px minmax(0,1fr); gap:24px; align-items:start">
    <aside style="position:sticky;top:16px;display:flex;flex-direction:column;gap:10px">
      <div class="lbl">14 個版本</div>
      <div class="tree" style="display:flex;flex-direction:column;gap:2px">
        <a href="Revisions.dc.html" style="flex-direction:column;align-items:flex-start;min-height:0;padding:8px 10px;gap:2px"><span style="display:flex;align-items:center;gap:8px;width:100%"><span class="av" style="background:#E8285B;width:20px;height:20px;font-size:10px">H</span>目前（已發布）<span class="n">v14</span></span><span style="font-size:12px;color:#5F6470;font-weight:500">今天 15:40 · Hazel</span></a>
        <a class="on" href="Revisions.dc.html" style="flex-direction:column;align-items:flex-start;min-height:0;padding:8px 10px;gap:2px"><span style="display:flex;align-items:center;gap:8px;width:100%"><span class="av" style="background:#2F6BFF;width:20px;height:20px;font-size:10px">T</span>自動儲存<span class="n">v13</span></span><span style="font-size:12px;color:#C9CBD3;font-weight:500">今天 15:12 · Ting · +84 −12</span></a>
        <a href="Revisions.dc.html" style="flex-direction:column;align-items:flex-start;min-height:0;padding:8px 10px;gap:2px"><span style="display:flex;align-items:center;gap:8px;width:100%"><span class="av" style="background:#E8285B;width:20px;height:20px;font-size:10px">H</span>改標題<span class="n">v12</span></span><span style="font-size:12px;color:#5F6470;font-weight:500">昨天 22:03 · Hazel · +3 −3</span></a>
        <a href="Revisions.dc.html" style="flex-direction:column;align-items:flex-start;min-height:0;padding:8px 10px;gap:2px"><span style="display:flex;align-items:center;gap:8px;width:100%"><span class="av" style="background:#E8285B;width:20px;height:20px;font-size:10px">H</span>發布<span class="n">v11</span></span><span style="font-size:12px;color:#5F6470;font-weight:500">10/21 09:30 · Hazel</span></a>
        <a href="Revisions.dc.html" style="flex-direction:column;align-items:flex-start;min-height:0;padding:8px 10px;gap:2px"><span style="display:flex;align-items:center;gap:8px;width:100%"><span class="av" style="background:#13C2A3;width:20px;height:20px;font-size:10px">小</span>校稿<span class="n">v10</span></span><span style="font-size:12px;color:#5F6470;font-weight:500">10/20 · 小魚 · +0 −9</span></a>
        <a href="Revisions.dc.html" style="color:#5F6470;font-weight:600">{I("chevron-d",14)}更早的 9 個</a>
      </div>
      <div style="font-size:12px;color:#5F6470;line-height:1.6;padding:10px;background:#F5F5F7;border-radius:0 10px 0 10px">自動儲存每 60 秒一次，只留最近 20 個；手動儲存和發布的版本永久保留。</div>
    </aside>
    <section>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><div class="seg"><span class="on">{I("list",14)}逐行比較</span><span>{I("eye",14)}只看 v13</span><span>{I("grid",14)}左右並排</span></div><span style="font-size:13px;color:#5F6470">比較 <b class="mono">v13</b> → <b class="mono">v14（目前）</b></span><label style="margin-left:auto;display:flex;align-items:center;gap:8px;font-size:13px;font-weight:600">只顯示有變的段落<span class="tg"></span></label></div>
      <div class="card" style="padding:0;overflow:hidden">
        <div style="display:grid;grid-template-columns:1fr 1fr;border-bottom:1px solid #EEEFF2;font-size:12px;font-weight:700;color:#5F6470"><div style="padding:8px 14px;border-right:1px solid #EEEFF2">標題</div><div style="padding:8px 14px"></div></div>
        <div class="diff" style="padding:12px 14px;border-bottom:1px solid #EEEFF2"><span class="a">焦糖布丁食譜</span><span class="b">零失敗焦糖布丁，重點在火</span></div>
        <div style="display:grid;grid-template-columns:1fr 1fr;border-bottom:1px solid #EEEFF2;font-size:12px;font-weight:700;color:#5F6470"><div style="padding:8px 14px">內文（Markdown） · 第 1–3 段相同，已收合</div></div>
        <div class="diff" style="padding:12px 14px">## 焦糖
焦糖只有<span class="a">大概十幾秒</span><span class="b">十秒</span>的窗口。<span class="b">這篇把爐火、鍋子、顏色都拍下來，照著做第一次就會成功。</span>

1. 糖 80g 下鍋，<span class="a">中火</span><span class="b">中小火</span>，不要攪。
2. 邊緣開始變色後輕晃鍋子。
<span class="b">3. 顏色到「琥珀」就離火，餘溫還會再深一階。
4. 立刻倒進模子，會冒煙是正常的。</span>
<span class="a">3. 變成深褐色就可以倒了。</span>

![焦糖顏色對照](/uploads/2026/10/caramel-<span class="a">1</span><span class="b">stages</span>.jpg)</div>
        <div style="display:grid;grid-template-columns:1fr 1fr;border-top:1px solid #EEEFF2;border-bottom:1px solid #EEEFF2;font-size:12px;font-weight:700;color:#5F6470"><div style="padding:8px 14px">摘要與分類</div></div>
        <div class="diff" style="padding:12px 14px">分類：食譜 › 甜點　標籤：<span class="b">+焦糖 +新手</span>　摘要：<span class="a">（空）</span><span class="b">焦糖只有十秒的窗口…</span></div>
      </div>
      <div style="display:flex;gap:8px;margin-top:12px;align-items:center;font-size:13px;color:#5F6470"><span class="diff"><span class="b">綠底</span>是目前版本新增、<span class="a">紅底</span>是 v13 有但現在沒有的。</span><button class="btn t s" type="button" style="margin-left:auto">{I("copy")}複製 v13 內文</button><button class="btn t s" type="button">{I("plus")}從 v13 開新草稿</button></div>
    </section>
  </main>'''))

# ======================= ADMIN: Import (WordPress) =======================
pages["Import"] = ("後台 WordPress 匯入", ADMIN_CSS, 1440, 1000, admin("tools", ["系統","工具","從 WordPress 匯入"],
  f'<button class="btn t" type="button">{I("x")}取消匯入</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: 220px minmax(0,1fr); gap:32px; align-items:start">
    <nav class="sub tree" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:2px"><a class="on" href="Import.dc.html">{I("wp",14)}從 WordPress 匯入</a><a href="Backup.dc.html">{I("db",14)}備份與匯出</a><a href="Redirects.dc.html">{I("redirect",14)}轉址</a><a href="Logs.dc.html">{I("terminal",14)}系統日誌</a><a href="Import.dc.html">{I("refresh",14)}重建索引與快取</a><a href="Import.dc.html">{I("alert",14)}站台健康檢查</a></nav>
    <section style="max-width:900px;display:flex;flex-direction:column;gap:24px">
      <div style="display:flex;gap:16px;align-items:center"><span class="step done"><i>{I("check",12)}</i>上傳 WXR</span><span style="color:#D9DBE0">—</span><span class="step done"><i>{I("check",12)}</i>對應作者</span><span style="color:#D9DBE0">—</span><span class="step on"><i>3</i>選要匯入的</span><span style="color:#D9DBE0">—</span><span class="step"><i>4</i>執行</span></div>
      <div style="position:relative;background:#15161A;color:#fff;border-radius:0 24px 0 24px;padding:24px 28px;display:flex;gap:24px;align-items:center;overflow:hidden">
        {cubes(.9, 760, -30, .85)}
        <div style="position:relative"><div class="lbl" style="color:#9A9FAB">讀到的檔案</div><h2 class="d" style="font-size:22px;margin-top:4px">hazelnut.wordpress.2026-10-20.xml</h2><div style="font-size:13px;color:#C9CBD3;margin-top:6px">WordPress 6.7 · 匯出於 2 天前 · 18 MB · 附件 312 個（1.9 GB，會從原站下載）</div></div>
      </div>
      <div class="card" style="padding:0;overflow:hidden">
        <table>
          <thead><tr><th style="width:36px"><span class="cb on"></span></th><th>內容</th><th>數量</th><th>怎麼處理</th><th>備註</th></tr></thead>
          <tbody>
            <tr><td><span class="cb on"></span></td><td><b>文章</b></td><td class="mono" style="font-size:15px">214</td><td><div class="seg"><span class="on">HTML 轉 Markdown</span><span>保留 HTML</span></div></td><td style="color:#5F6470;font-size:12px">Gutenberg 區塊會轉成一般段落；短代碼會原樣留著並標記。</td></tr>
            <tr><td><span class="cb on"></span></td><td><b>頁面</b></td><td class="mono" style="font-size:15px">11</td><td><div class="seg"><span class="on">HTML 轉 Markdown</span><span>保留 HTML</span></div></td><td style="color:#5F6470;font-size:12px"></td></tr>
            <tr><td><span class="cb on"></span></td><td><b>分類與標籤</b></td><td class="mono" style="font-size:15px">7 / 42</td><td><span class="chip ok">合併同名</span></td><td style="color:#5F6470;font-size:12px">「未分類」會略過。</td></tr>
            <tr><td><span class="cb on"></span></td><td><b>留言</b></td><td class="mono" style="font-size:15px">1,840</td><td><div class="seg"><span class="on">已核准的</span><span>全部</span></div></td><td style="color:#5F6470;font-size:12px">垃圾留言 612 則不匯入。</td></tr>
            <tr><td><span class="cb on"></span></td><td><b>附件</b></td><td class="mono" style="font-size:15px">312</td><td><div class="seg"><span class="on">下載到本站</span><span>只連回原站</span></div></td><td style="color:#7A5200;font-size:12px">約 1.9 GB，背景下載，文章裡的網址會自動改。</td></tr>
            <tr><td><span class="cb"></span></td><td><b>WooCommerce 商品</b></td><td class="mono" style="font-size:15px">38</td><td><span class="chip warn">需要外掛</span></td><td style="color:#5F6470;font-size:12px">裝「WooCommerce 匯入」外掛才能選。</td></tr>
            <tr><td><span class="cb on"></span></td><td><b>固定網址</b></td><td class="mono" style="font-size:15px">225</td><td><span class="chip ok">自動建轉址</span></td><td style="color:#5F6470;font-size:12px">/2026/10/21/slug → /posts/slug，舊連結不會壞。</td></tr>
          </tbody>
        </table>
      </div>
      <div style="display:flex;align-items:center;gap:12px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px">{I("eye",18)}<span>執行前會先<b>試跑</b>，列出會衝突的網址和找不到的附件，確認試跑報告後，再按「執行匯入」才會寫入。</span><label style="margin-left:auto;display:flex;align-items:center;gap:8px;font-weight:600">試跑完成<span class="chip ok">可以匯入</span></label></div>
      <div style="display:flex;gap:8px"><button class="btn t" type="button">{I("arrow-l")}上一步</button><button class="btn t" type="button">{I("eye")}試跑</button><button class="btn p" type="button">{I("play")}執行匯入</button></div>
      <p style="font-size:13px;color:#5F6470">匯入已排程或執行中時會自動刷新，兩分鐘後可手動重新整理。<a href="Infra.dc.html#sys-jobs">查看工作與編號</a></p>
      <div class="card"><h3>完成後的匯入報告</h3><table><thead><tr><th>類型</th><th>已建立</th></tr></thead><tbody><tr><td>文章</td><td>1</td></tr><tr><td>頁面</td><td>0</td></tr><tr><td>附件</td><td>0</td></tr><tr><td>留言</td><td>0</td></tr><tr><td>作者</td><td>1</td></tr></tbody></table></div>
      <div class="card"><h3>已略過</h3><p>依實際原因列出筆數：已匯入過、未選擇的類型、附件未啟用抓取等；沒有略過時顯示空狀態。</p></div>
    </section>
  </main>'''))

# ======================= ADMIN: Backup =======================
pages["Backup"] = ("後台備份與匯出", ADMIN_CSS, 1440, 1000, admin("tools", ["系統","工具","備份與匯出"],
  f'<button class="btn t" type="button">{I("upload")}從備份還原</button><button class="btn p" type="button">{I("db")}立刻備份</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: 220px minmax(0,1fr); gap:32px; align-items:start">
    <nav class="sub tree" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:2px"><a href="Import.dc.html">{I("wp",14)}從 WordPress 匯入</a><a class="on" href="Backup.dc.html">{I("db",14)}備份與匯出</a><a href="Redirects.dc.html">{I("redirect",14)}轉址</a><a href="Logs.dc.html">{I("terminal",14)}系統日誌</a><a href="Import.dc.html">{I("refresh",14)}重建索引與快取</a><a href="Import.dc.html">{I("alert",14)}站台健康檢查</a></nav>
    <section style="display:flex;flex-direction:column;gap:24px">
      <div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px">
        <div class="card" style="display:flex;flex-direction:column;gap:6px"><span class="lbl">上次備份</span><b class="mono" style="font-size:24px">今天 03:00</b><span style="font-size:12px;color:#0F6A45;display:flex;align-items:center;gap:4px">{I("check",14)}成功 · 412 MB · 48 秒</span></div>
        <div class="card" style="display:flex;flex-direction:column;gap:6px"><span class="lbl">排程</span><b class="mono" style="font-size:24px">每天 03:00</b><span style="font-size:12px;color:#5F6470">保留 14 天，週日的保留 3 個月。關閉可停用排程</span></div>
        <div class="card" style="display:flex;flex-direction:column;gap:6px;background:#15161A;color:#fff;box-shadow:none"><span class="lbl" style="color:#9A9FAB">存到哪</span><b style="font-size:16px;display:flex;align-items:center;gap:8px">{I("globe")}Cloudflare R2 · jelly-backups</b><span style="font-size:12px;color:#C9CBD3">本機也留 3 份 · <a href="Settings.dc.html" style="color:#fff">設定</a></span></div>
      </div>
      <p style="font-size:13px;color:#5F6470">已排入背景處理，工作編號：backup-job。<a href="Infra.dc.html#sys-jobs">查看工作並刷新狀態</a>。待處理與執行中的備份不可刪除。</p>
      <div>
        <div style="display:flex;align-items:center;gap:10px;margin-bottom:10px"><div class="lbl">備份紀錄</div><span style="margin-left:auto;font-size:12px;color:#5F6470">還原前先備份現況，輸入 RESTORE 與加密密碼確認；完成後保留維護模式。</span></div>
        <table>
          <thead><tr><th>時間</th><th>內容</th><th>大小</th><th>位置</th><th>狀態</th><th></th></tr></thead>
          <tbody>
            <tr><td><b>今天 03:00</b><div style="font-size:12px;color:#5F6470">自動</div></td><td>資料庫 + 附件 + 主題外掛</td><td class="mono">412 MB</td><td><span class="chip nd">R2</span> <span class="chip nd">本機</span></td><td><span class="chip ok">完成</span></td><td class="act"><button class="btn t s" type="button">{I("undo",14)}還原</button><button class="btn x s i" type="button" aria-label="下載">{I("download")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
            <tr><td><b>昨天 15:48</b><div style="font-size:12px;color:#5F6470">手動 · Hazel · 「更新外掛前」</div></td><td>資料庫</td><td class="mono">38 MB</td><td><span class="chip nd">R2</span> <span class="chip nd">本機</span></td><td><span class="chip ok">完成</span></td><td class="act"><button class="btn t s" type="button">{I("undo",14)}還原</button><button class="btn x s i" type="button" aria-label="下載">{I("download")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
            <tr><td><b>昨天 03:00</b><div style="font-size:12px;color:#5F6470">自動</div></td><td>資料庫 + 附件 + 主題外掛</td><td class="mono">410 MB</td><td><span class="chip nd">R2</span></td><td><span class="chip ok">完成</span></td><td class="act"><button class="btn t s" type="button">{I("undo",14)}還原</button><button class="btn x s i" type="button" aria-label="下載">{I("download")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
            <tr><td><b>10/20 03:00</b><div style="font-size:12px;color:#5F6470">自動</div></td><td>資料庫 + 附件 + 主題外掛</td><td class="mono">—</td><td><span class="chip nd">R2</span></td><td><span class="chip bad">失敗 · R2 逾時</span></td><td class="act"><button class="btn t s" type="button">{I("refresh",14)}重試</button><a class="btn x s i" href="Logs.dc.html" aria-label="看日誌">{I("terminal")}</a></td></tr>
          </tbody>
        </table>
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
        <div class="card" style="display:flex;flex-direction:column;gap:10px"><div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">匯出內容</b></div><p style="margin:0;font-size:13px;color:#5F6470;line-height:1.6">可選 jmd（Markdown ZIP）、WXR 或 JSON。備份另可輸入註記與加密密碼，密碼不會儲存。</p><div style="display:flex;gap:6px;flex-wrap:wrap"><button class="btn t s" type="button">{I("download",14)}Markdown 資料夾（.zip）</button><button class="btn t s" type="button">{I("download",14)}WXR（給 WordPress）</button><button class="btn t s" type="button">{I("download",14)}JSON</button></div></div>
        <div class="card" style="display:flex;flex-direction:column;gap:10px"><div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">搬到另一台主機</b></div><p style="margin:0;font-size:13px;color:#5F6470;line-height:1.6">整站打包，在新主機的安裝畫面選「從備份還原」就能接著用。</p><div style="display:flex;gap:6px"><button class="btn s" type="button">{I("box",14)}打包整站</button><button class="btn t s" type="button">{I("copy",14)}複製還原指令</button></div></div>
      </div>
    </section>
  </main>'''))

# ======================= ADMIN: Redirects + SEO =======================
pages["Redirects"] = ("後台轉址與 SEO", ADMIN_CSS, 1440, 1000, admin("redirects", ["系統","轉址與 SEO"],
  f'<button class="btn t" type="button">{I("upload")}匯入 CSV</button><button class="btn p" type="button">{I("plus")}新增轉址</button>',
  f'''<main style="padding: 16px 24px 32px; display:grid; grid-template-columns: minmax(0,1fr) 360px; gap:24px; align-items:start">
    <section>
      <div class="seg" style="margin-bottom:14px"><span class="on">{I("redirect",14)}轉址 <b>231</b></span><span>{I("alert",14)}404 紀錄 <b style="color:#E8285B">18</b></span><span style="padding:0"><a href="Settings.dc.html" style="display:inline-flex;align-items:center;gap:6px;min-height:26px;padding:0 10px;color:inherit">{I("globe",14)}SEO 預設</a></span></div>
      <div style="display:flex;align-items:center;gap:10px;margin-bottom:12px"><label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;width:280px;color:#5F6470;font-size:13px">{I("search",14)}<input type="text" placeholder="找來源或目標網址，CSV 匯入先預覽" aria-label="搜尋轉址" style="border:0;background:transparent;font:inherit;flex:1;min-width:0"></label><div class="seg"><span class="on">全部</span><span>301</span><span>302</span><span>自動建立</span></div><span style="margin-left:auto;font-size:12px;color:#5F6470">225 筆是 WordPress 匯入時自動建的</span></div>
      <table>
        <thead><tr><th style="width:36px"><span class="cb"></span></th><th>從</th><th></th><th>到</th><th>類型</th><th>命中</th><th></th></tr></thead>
        <tbody>
          <tr><td><span class="cb"></span></td><td class="mono" style="font-weight:600;font-family:ui-monospace,Menlo,monospace;font-size:12.5px">/2026/10/21/caramel-pudding/</td><td style="color:#B9BCC6">{I("arrow-r",14)}</td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><a href="Post.dc.html">/post/caramel-pudding</a></td><td><span class="chip nd">301</span></td><td class="mono">1,204</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="測試">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><span class="cb"></span></td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;font-weight:600">/shop/gift-box-2025</td><td style="color:#B9BCC6">{I("arrow-r",14)}</td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><a href="Product.dc.html">/shop/gift-box</a></td><td><span class="chip nd">301</span></td><td class="mono">388</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="測試">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><span class="cb"></span></td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;font-weight:600">/promo</td><td style="color:#B9BCC6">{I("arrow-r",14)}</td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><a href="Shop.dc.html">/shop?tag=midautumn</a></td><td><span class="chip info">302 · 到 10/31</span></td><td class="mono">2,910</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="測試">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr><td><span class="cb"></span></td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;font-weight:600">/category/recipes/*</td><td style="color:#B9BCC6">{I("arrow-r",14)}</td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><a href="Archive.dc.html">/c/recipes/$1</a></td><td><span class="chip nd">301 · 規則</span></td><td class="mono">7,120</td><td class="act"><button class="btn x s i" type="button" aria-label="編輯">{I("edit")}</button><button class="btn x s i" type="button" aria-label="測試">{I("external")}</button><button class="btn x s i" type="button" aria-label="刪除">{I("trash")}</button></td></tr>
          <tr style="background:#FBE1DE"><td><span class="cb"></span></td><td style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;font-weight:600">/old-menu.pdf</td><td style="color:#B9BCC6">{I("arrow-r",14)}</td><td style="color:#9C2318;font-size:12.5px">目標 404 了</td><td><span class="chip bad">壞掉</span></td><td class="mono">41</td><td class="act"><button class="btn t s" type="button">{I("edit",14)}修</button></td></tr>
        </tbody>
      </table>
      <div style="margin-top:24px"><div style="display:flex;align-items:center;gap:8px;margin-bottom:8px"><span class="lbl">最近 404（7 天）</span><span style="font-size:12px;color:#5F6470">點一下就能建轉址</span></div>
        <div style="display:flex;flex-direction:column;gap:4px">
          <div style="display:flex;align-items:center;gap:12px;padding:8px 10px;background:#F8F8FA;border-radius:0 9px 0 9px;font-size:13px"><span style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;flex:1">/wp-content/uploads/2024/05/hero.jpg</span><span class="mono" style="color:#9C2318">× 96</span><span style="color:#5F6470;font-size:12px">來自 Google 圖片</span><button class="btn t s" type="button">{I("plus",14)}建轉址</button></div>
          <div style="display:flex;align-items:center;gap:12px;padding:8px 10px;background:#F8F8FA;border-radius:0 9px 0 9px;font-size:13px"><span style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;flex:1">/feed/</span><span class="mono" style="color:#9C2318">× 52</span><span style="color:#5F6470;font-size:12px">RSS 閱讀器</span><button class="btn t s" type="button">{I("plus",14)}建轉址</button></div>
          <div style="display:flex;align-items:center;gap:12px;padding:8px 10px;background:#F8F8FA;border-radius:0 9px 0 9px;font-size:13px"><span style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px;flex:1">/wp-login.php</span><span class="mono" style="color:#9C2318">× 1,300</span><span style="color:#5F6470;font-size:12px">機器人</span><button class="btn x s" type="button">{I("spam",14)}忽略</button></div>
        </div>
      </div>
    </section>
    <aside class="card" style="position:sticky;top:16px;display:flex;flex-direction:column;gap:14px">
      <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">新增轉址</b></div>
      <div><span class="lbl">從（舊網址）</span><input class="ul" type="text" value="/wp-content/uploads/2024/05/hero.jpg" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"></div>
      <div><span class="lbl">到</span><input class="ul" type="text" placeholder="/uploads/2024/05/hero.jpg 或完整網址" style="font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><div style="font-size:12px;color:#0F6A45;margin-top:6px;display:flex;align-items:center;gap:4px">{I("check",14)}附件庫有同名檔案，已幫你填好。</div></div>
      <div><span class="lbl">類型</span><div class="seg" style="margin-top:6px"><span class="on">301 永久</span><span>302 暫時</span><span>410 已移除</span></div></div>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13.5px;font-weight:600">保留原本的 ?query<span class="tg"></span></label>
      <label style="display:flex;align-items:center;justify-content:space-between;font-size:13.5px;font-weight:600">當成規則（支援 * 和 $1）<span class="tg off"></span></label>
      <div style="display:flex;gap:8px"><button class="btn p" type="button">{I("save")}儲存</button><button class="btn t" type="button">{I("external")}先測試</button></div>
      </aside>
  </main>
  <div style="padding:0 24px 40px"><div style="border-top:2px dashed #D9DBE0;padding-top:16px;display:flex;flex-direction:column;gap:12px;max-width:1040px">
    <span class="lbl">分頁：SEO 預設</span>
    <div class="seg" style="align-self:flex-start"><span>{I("redirect",14)}轉址 <b>231</b></span><span>{I("alert",14)}404 紀錄 <b style="color:#E8285B">18</b></span><span class="on">{I("globe",14)}SEO 預設</span></div>
    <div class="card" style="display:flex;align-items:center;gap:14px;padding:16px 18px"><span style="width:36px;height:36px;background:#F3F3F5;display:grid;place-items:center;border-radius:0 10px 0 10px">{I("globe",18)}</span><div style="flex:1;font-size:13px;line-height:1.6"><b style="font-size:14px">SEO 預設不在這裡改</b><div style="color:#5F6470">標題格式「%title% · %site%」、預設 OG 圖、是否讓搜尋引擎索引、各類型索引、摘要模式與 IndexNow，都在設定頁的「SEO 預設」；這裡可改標題格式與預設 OG 圖；完整索引與爬蟲規則在「搜尋引擎與 AI 爬蟲」，每篇內容可以覆寫。</div></div><a class="btn" href="Settings.dc.html">{I("settings")}前往設定</a></div>
  </div></div>'''))

# ======================= ADMIN: Logs =======================
def lrow(t, lv, src, msg):
    return f'<div class="r"><span style="color:#5F6470">{t}</span><span class="lv {lv[0]}">{lv[1]}</span><span style="color:#2F6BFF">{src}</span><span>{msg}</span></div>'

pages["Logs"] = ("後台系統日誌", ADMIN_CSS, 1440, 1000, admin("logs", ["系統","日誌"],
  f'<label style="display:flex;align-items:center;gap:8px;font-size:13px;font-weight:600">即時<span class="tg"></span></label><button class="btn t" type="button">{I("download")}下載 24 小時</button>',
  f'''<main style="padding: 16px 24px 32px; display:flex; flex-direction:column; gap:14px">
    <div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px">
      <div class="card" style="display:flex;flex-direction:column;gap:4px;padding:14px"><span class="lbl">請求 / 分</span><b class="mono" style="font-size:26px">342</b><span style="font-size:12px;color:#5F6470">p95 86 ms</span></div>
      <div class="card" style="display:flex;flex-direction:column;gap:4px;padding:14px"><span class="lbl">錯誤（1 小時）</span><b class="mono" style="font-size:26px;color:#9C2318">3</b><span style="font-size:12px;color:#5F6470">2 個 500、1 個付款逾時</span></div>
      <div class="card" style="display:flex;flex-direction:column;gap:4px;padding:14px"><span class="lbl">背景工作</span><b class="mono" style="font-size:26px">2 <span style="font-size:13px;color:#5F6470;font-weight:600">執行中</span></b><span style="font-size:12px;color:#5F6470">附件下載 188/312 · 縮圖</span></div>
      <div class="card" style="display:flex;flex-direction:column;gap:4px;padding:14px;background:#15161A;color:#fff;box-shadow:none"><span class="lbl" style="color:#9A9FAB">版本</span><b class="mono" style="font-size:18px">Jelly 0.9.3 · Go 1.25 · PG 17</b><span style="font-size:12px;color:#C9CBD3">上線 12 天 · 磁碟 38% · <a href="Logs.dc.html" style="color:#fff">健康檢查</a></span></div>
    </div>
    <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
      <div class="seg"><span class="on">全部</span><span>應用</span><span>HTTP</span><span>金流</span><span>外掛</span><span>登入與稽核</span><span>排程</span></div>
      <div class="seg"><span class="on" style="background:#9C2318">錯誤</span><span class="on" style="background:#7A5200">警告</span><span class="on">資訊</span><span>除錯</span></div>
      <label style="display:flex;align-items:center;gap:8px;background:#F3F3F5;border-radius:0 9px 0 9px;padding:0 10px;min-height:32px;flex:1;min-width:200px;color:#5F6470;font-size:13px">{I("search",14)}<input type="text" placeholder='搜尋訊息或請求 ID' aria-label="篩選日誌" style="border:0;background:transparent;font:inherit;flex:1;min-width:0;font-family:ui-monospace,Menlo,monospace;font-size:12.5px"></label>
      <div class="seg"><span>15 分</span><span class="on">1 小時</span><span>今天</span><span>{I("calendar",12)}自訂</span></div>
    </div>
    <div class="card log" style="padding:0;overflow:hidden">
      {lrow("16:02:11.204",("e","ERR"),"payment.ecpay","callback timeout after 30s order=JL-2026-0417 · 已排重試 (1/3)")}
      {lrow("16:02:09.881",("i","INFO"),"http","POST /checkout 302 · 412ms · 1.160.x.x")}
      {lrow("16:01:40.013",("w","WARN"),"plugin.seo-boost","deprecated hook `post.render` 仍在使用，下個版本移除")}
      {lrow('16:01:12.552',('e','ERR'),'http','GET /posts/midautumn 500 · template: posts.html: no such template &quot;share-card&quot; · <a href=Logs.dc.html>看堆疊</a>')}
      {lrow("16:00:00.002",("i","INFO"),"cron","backup.daily skipped · 上次 03:00 已成功")}
      {lrow("15:58:31.719",("i","INFO"),"audit","user=hazel 更新設定 payments.ecpay.merchant_id · 從 Chrome/macOS")}
      {lrow("15:55:04.100",("i","INFO"),"job.media-import","已下載 188/312 · 1.1 GB · 預估剩 9 分")}
      {lrow("15:52:48.336",("w","WARN"),"auth","登入失敗 × 5 · user=admin · 114.x.x.x · 已暫時封鎖 15 分")}
      {lrow("15:50:12.901",("d","DBG"),"theme","render index.html 38ms · cache miss")}
      {lrow("15:49:59.470",("i","INFO"),"http","GET / 200 · 12ms · cache hit")}
      <div style="padding:10px;text-align:center;font-size:12px;color:#5F6470;font-family:'Noto Sans TC',sans-serif"><button class="btn t s" type="button">載入更多</button> · 點請求 ID 可篩選同一請求；即時模式可暫停與重新連線</div>
    </div>
  </main>'''))

# ======================= ADMIN: Palette (⌘K) =======================
pages["Palette"] = ("後台指令面板", ADMIN_CSS, 1440, 1000, admin("dashboard", ["儀表板"], "",
  f'''<main style="padding:16px 24px;opacity:.35;pointer-events:none">
    <div style="display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px"><div class="card" style="height:90px"></div><div class="card" style="height:90px"></div><div class="card" style="height:90px"></div><div class="card" style="height:90px"></div></div>
    <div class="card" style="height:420px;margin-top:12px"></div>
  </main>''',
  extra=f'''<div style="position:absolute;inset:0;background:rgba(21,22,26,.35)"></div>
  <div style="position:absolute;left:50%;top:96px;transform:translateX(-50%);width:640px;background:#fff;border-radius:0 24px 0 24px;box-shadow:0 0 0 1px #E2E3E8,0 32px 80px rgba(21,22,26,.28);overflow:hidden;display:flex;flex-direction:column">
    <div class="topline"></div>
    <label style="display:flex;align-items:center;gap:12px;padding:14px 18px;border-bottom:1px solid #EEEFF2">{I("search",20)}<input type="text" value="焦糖" aria-label="搜尋或指令" style="border:0;font:inherit;font-size:17px;flex:1;min-width:0;outline:0"><span class="kbd">ESC</span></label>
    <div style="display:flex;gap:6px;padding:10px 18px;border-bottom:1px solid #EEEFF2;font-size:12px"><span class="chip nd" style="background:#15161A;color:#fff">全部</span><span class="chip nd">文章 2</span><span class="chip nd">商品 0</span><span class="chip nd">留言 3</span><span class="chip nd">設定 0</span><span class="chip nd">指令 1</span><span style="margin-left:auto;color:#5F6470">輸入 <span class="kbd">&gt;</span> 只找指令、<span class="kbd">/</span> 跳頁面</span></div>
    <div style="padding:8px;display:flex;flex-direction:column;gap:2px;max-height:460px;overflow:auto">
      <div class="lbl" style="padding:8px 12px 4px">文章</div>
      <a class="pal on" href="Editor.dc.html">{I("doc")}零失敗焦糖布丁，重點在火<span class="chip ok" style="margin-left:6px">已發布</span><span class="k">↵ 編輯</span></a>
      <a class="pal" href="Editor.dc.html">{I("doc")}焦糖榛果醬（草稿）<span class="chip warn" style="margin-left:6px">草稿</span><span class="k">↵</span></a>
      <div class="lbl" style="padding:8px 12px 4px">留言</div>
      <a class="pal" href="Comments.dc.html">{I("comment")}小魚：焦糖我總是煮過頭…<span class="k">零失敗焦糖布丁</span></a>
      <a class="pal" href="Comments.dc.html">{I("comment")}阿凱：請問可以用黑糖嗎？<span class="chip warn" style="margin-left:6px">待審</span><span class="k"></span></a>
      <div class="lbl" style="padding:8px 12px 4px">指令</div>
      <a class="pal" href="Editor.dc.html">{I("plus")}新文章：「焦糖」<span class="k">⌘ N</span></a>
      <div class="lbl" style="padding:8px 12px 4px">常用</div>
      <a class="pal" href="Editor.dc.html">{I("plus")}新文章<span class="k">⌘ N</span></a>
      <a class="pal" href="Main.dc.html">{I("external")}查看網站<span class="k">⌘ ⇧ V</span></a>
      <a class="pal" href="Comments.dc.html">{I("check")}核准待審留言 7 則<span class="k"></span></a>
      <a class="pal" href="Backup.dc.html">{I("db")}立刻備份<span class="k"></span></a>
      <a class="pal" href="Import.dc.html">{I("refresh")}清除快取<span class="k"></span></a>
      <a class="pal" href="Profile.dc.html">{I("theme")}切換深色模式<span class="k"></span></a>
    </div>
    <div style="display:flex;align-items:center;gap:14px;padding:10px 18px;border-top:1px solid #EEEFF2;font-size:12px;color:#5F6470"><span><span class="kbd">↑↓</span> 移動</span><span><span class="kbd">↵</span> 開啟</span><span><span class="kbd">⌘↵</span> 新分頁</span><span style="margin-left:auto;display:flex;align-items:center;gap:6px">{MARK.format(ink="#15161A").replace('class="mark"','class="mark" style="width:14px;height:16px"')}Jelly 指令面板</span></div>
  </div>'''))

# ======================= ADMIN: States =======================
pages["States"] = ("後台狀態畫面", ADMIN_CSS, 1440, 1000, admin("posts", ["內容","文章"],
  f'<button class="btn p" type="button">{I("plus")}新文章</button>',
  f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:1fr 1fr;gap:16px">
    <section class="card" style="position:relative;min-height:400px;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;gap:10px;overflow:hidden">
      <span class="chip nd" style="position:absolute;left:14px;top:14px">空白狀態</span>
      {cubes(1.0, 380, 36, .9)}
      <div style="position:relative;display:flex;flex-direction:column;align-items:center;gap:10px"><span class="stripe"></span><h2 class="d" style="font-size:28px;margin-top:8px">還沒有文章。</h2><p style="margin:0;color:#5F6470;max-width:30em;line-height:1.6">寫第一篇，或把 WordPress 的文章搬過來。草稿不會出現在前台。</p><div style="display:flex;gap:8px;margin-top:6px"><button class="btn p" type="button">{I("plus")}寫第一篇</button><a class="btn t" href="Import.dc.html">{I("wp")}從 WordPress 匯入</a></div></div>
    </section>
    <section class="card" style="position:relative;min-height:400px;display:flex;flex-direction:column;gap:14px;overflow:hidden">
      <span class="chip nd" style="position:absolute;left:14px;top:14px;z-index:1">載入中</span>
      <div style="display:flex;align-items:center;gap:10px;margin-top:30px"><span style="width:18px;height:18px;border-radius:0 6px 0 6px;background:conic-gradient(#E8285B 0 25%,#2F6BFF 25% 50%,#13C2A3 50% 75%,#F5B800 75%);animation:sp 1s linear infinite;display:inline-block"></span><b>載入文章…</b><span style="font-size:12px;color:#5F6470">超過 3 秒才顯示這行，避免閃一下</span></div>
      <style>@keyframes sp{{to{{transform:rotate(360deg)}}}}</style>
      <div class="ph"><i style="width:40%"></i><i style="width:92%"></i><i style="width:70%"></i><i style="width:85%;margin-top:10px"></i><i style="width:55%"></i><i style="width:78%"></i><i style="width:88%;margin-top:10px"></i><i style="width:62%"></i></div>
      <div style="margin-top:auto;display:flex;align-items:center;gap:10px;padding:10px 12px;background:#F5F5F7;border-radius:0 10px 0 10px;font-size:13px">{I("save",16)}<span>儲存中的樣子：右上角按鈕變成</span><span class="btn p s" style="opacity:.7;pointer-events:none">{I("refresh",12)}儲存中…</span><span>，成功後 2 秒內顯示</span><span class="chip ok">已儲存 15:42</span></div>
    </section>
    <section class="card" style="position:relative;min-height:300px;display:flex;flex-direction:column;align-items:center;justify-content:center;text-align:center;gap:10px;background:#15161A;color:#fff;box-shadow:none">
      <span class="chip nd" style="position:absolute;left:14px;top:14px;background:#2A2B31;color:#fff">錯誤（整頁）</span>
      <span class="stripe"></span>
      <div class="lbl" style="color:#9A9FAB;margin-top:8px">500 · req_8f3a21</div>
      <h2 class="d" style="font-size:28px">出事了，但不是你的錯。</h2>
      <p style="margin:0;color:#C9CBD3;max-width:32em;line-height:1.6;font-size:13.5px">主題檔 posts.html 少了 share-card 範本。你剛才的變更已自動存成草稿。</p>
      <div style="display:flex;gap:8px;margin-top:6px"><button class="btn t" type="button">{I("refresh")}再試一次</button><a class="btn" href="Logs.dc.html" style="background:#2A2B31">{I("terminal")}看日誌</a><button class="btn x" type="button" style="color:#C9CBD3">{I("copy")}複製錯誤代碼</button></div>
    </section>
    <section style="display:flex;flex-direction:column;gap:16px">
      <div class="card" style="position:relative;display:flex;flex-direction:column;gap:10px;flex:1;justify-content:center;align-items:center;text-align:center">
        <span class="chip nd" style="position:absolute;left:14px;top:14px">無權限</span>
        <span style="width:48px;height:48px;display:grid;place-items:center;background:#FCF0C8;color:#7A5200;border-radius:0 14px 0 14px">{I("lock",22)}</span>
        <h2 class="d" style="font-size:22px">這頁要管理員才能看。</h2>
        <p style="margin:0;color:#5F6470;font-size:13px;line-height:1.6">你現在是「編輯」。需要的話請 Hazel 幫你加權限。</p>
        <div style="display:flex;gap:8px"><a class="btn t s" href="Dashboard.dc.html">{I("arrow-l",14)}回儀表板</a><button class="btn t s" type="button">{I("mail",14)}向管理員申請</button></div>
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
        <div class="jt"><i style="background:#13C2A3"></i><b>已發布</b><a href="Post.dc.html" style="color:#C9CBD3;white-space:nowrap">看前台 ↗</a><span class="xx">{I("x")}</span></div>
        <div class="jt"><i style="background:#E8285B"></i><b>儲存失敗</b><span class="d2">連線中斷</span><button class="btn x s" type="button" style="color:#fff;margin-left:auto">{I("refresh",12)}重試</button><span class="xx" style="margin-left:0">{I("x")}</span></div>
        <div class="jt"><i style="background:#F5B800"></i><b>已移到回收桶</b><button class="btn x s" type="button" style="color:#fff;margin-left:auto">{I("undo",12)}復原</button><span class="xx" style="margin-left:0">{I("x")}</span></div>
        <div class="jt light"><i style="background:#2F6BFF"></i><b>離線</b><span class="d2">會自動重連，草稿留在本機</span><span class="xx">{I("x")}</span></div>
      </div>
    </section>
  </main>'''))

if __name__ == "__main__":
    import shutil
    shutil.copyfile(os.path.join(OUT, "../../web/admin/assets/jelly-mark.svg"), os.path.join(OUT, "jelly-mark.svg"))
    for name,(title,css,w,h,body) in pages.items():
        if os.environ.get("JELLY_DESIGN_ONLY") and name not in os.environ["JELLY_DESIGN_ONLY"].split(","):
            continue
        with open(os.path.join(OUT, f"{name}.dc.html"), "w", encoding="utf-8") as f:
            f.write(doc(title, css, body, w, h))
        print("wrote", name)
