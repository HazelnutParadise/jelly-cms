# Account boards that come before code (ticket 06): Login2FA, InviteAccept, PasswordReset.
# Same visual language as Login.dc.html (hand-written) -- keep the split frame and the CSS in sync with it.
import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, OUT, MARK, cubes

CSS = '''
body{margin:0;font-family:"Noto Sans TC","PingFang TC",system-ui,sans-serif;color:#15161A;background:#fff;-webkit-font-smoothing:antialiased}
a{color:#2F6BFF;text-decoration:none}a:hover{color:#E8285B}
.d{font-family:"Bricolage Grotesque","Noto Sans TC",sans-serif;font-weight:800;letter-spacing:-.02em;line-height:1.08;margin:0}
.btn{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:48px;padding:0 22px;font-weight:700;font-size:15px;border:0;border-radius:0 10px 0 10px;cursor:pointer;background:#15161A;color:#fff;font-family:inherit;white-space:nowrap;width:100%;box-sizing:border-box}
.btn.p{background:#E8285B}.btn.t{background:#EDEEF1;color:#15161A}.btn[disabled]{opacity:.45;cursor:not-allowed}
.stripe{display:block;width:56px;height:3px;transform:skewX(-32deg);transform-origin:left bottom;background:linear-gradient(90deg,#E8285B 0 52%,transparent 52% 60%,#2F6BFF 60% 82%,transparent 82% 88%,#13C2A3 88% 100%)}
.topline{height:3px;background:linear-gradient(90deg,#E8285B 0 64%,#2F6BFF 64% 86%,#13C2A3 86% 100%)}
.ic{width:18px;height:18px;stroke:currentColor;fill:none;stroke-width:1.75;stroke-linecap:square;stroke-linejoin:miter;flex:0 0 auto}
.ul{width:100%;min-height:44px;padding:8px 0;border:0;border-bottom:1.5px solid #D9DBE0;background:transparent;font:inherit;font-size:16px;color:#15161A;box-sizing:border-box}
.lbl{font-size:12px;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:#5F6470}
label{display:block;font-size:13px;font-weight:600;color:#5F6470;margin-bottom:2px}
.mark{display:inline-block;width:26px;height:30px;object-fit:contain}
.chip{display:inline-flex;align-items:center;gap:5px;min-height:22px;padding:0 8px;font-size:11px;font-weight:700;border-radius:0 7px 0 7px;background:#EDEEF1;color:#15161A;white-space:nowrap}
.chip:before{content:"";width:6px;height:6px;background:currentColor;border-radius:0 2px 0 2px}
.chip.nd:before{display:none}
.ok{background:#DCF3E9;color:#0F6A45}.warn{background:#FCF0C8;color:#7A5200}.info{background:#DCE6FF;color:#1A3F9E}.bad{background:#FBE1DE;color:#9C2318}
.otp{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:8px}
.otp input{width:100%;height:56px;padding:0;text-align:center;border:0;border-radius:0 10px 0 10px;background:#F3F3F5;box-shadow:inset 0 0 0 1.5px transparent;font:800 24px "Bricolage Grotesque",sans-serif;color:#15161A;box-sizing:border-box;caret-color:#E8285B}
.otp input:focus{outline:0;background:#fff;box-shadow:inset 0 0 0 2px #2F6BFF}
.otp.err input{background:#FDF3F2;box-shadow:inset 0 0 0 1.5px #E8285B}
.otp.err input:focus{box-shadow:inset 0 0 0 2px #2F6BFF}
.panel{background:#fff;box-shadow:inset 0 0 0 1px #E2E3E8;border-radius:0 24px 0 24px;padding:32px;display:flex;flex-direction:column;gap:20px;position:relative}
.tag{position:absolute;left:16px;top:-11px;background:#15161A;color:#fff}
.meter{display:grid;grid-template-columns:repeat(4,1fr);gap:4px}.meter i{height:4px;background:#E2E3E8;border-radius:0 2px 0 2px}.meter i.on{background:#F5B800}.meter i.good{background:#13C2A3}
'''

# Keep the existing placeholder API used by the login-board template.
LOGIN_MARK = '<img class="mark" src="./jelly-mark.svg" alt="" aria-hidden="true">'

LEFT = '''<aside style="position: relative; overflow: hidden; background: #15161A; color: #fff; padding: 32px; display: flex; flex-direction: column; justify-content: space-between; border-radius: 0 56px 0 0" aria-hidden="true">
    <span style="position: absolute; left: 14%; top: 16%; width: 36%; aspect-ratio: 1; background: #2F6BFF; opacity: .85; border-radius: 0 24% 0 24%"></span>
    <span style="position: absolute; left: 32%; top: 33%; width: 36%; aspect-ratio: 1; background: #13C2A3; opacity: .85; border-radius: 0 24% 0 24%"></span>
    <span style="position: absolute; left: 50%; top: 50%; width: 36%; aspect-ratio: 1; background: #E8285B; opacity: .92; border-radius: 0 24% 0 24%; rotate: -7deg"></span>
    <span style="position: absolute; left: 16%; top: 70%; width: 16%; aspect-ratio: 1; background: #F5B800; opacity: .95; border-radius: 0 24% 0 24%"></span>
    <span style="position: absolute; left: 72%; top: 22%; width: 12%; aspect-ratio: 1; background: #fff; opacity: .9; border-radius: 0 24% 0 24%"></span>
    <span style="position: absolute; left: -20%; top: 34%; width: 140%; height: 1px; background: #fff; opacity: .14; transform: rotate(-32deg)"></span>
    <div style="position: relative; display: inline-flex; align-items: center; gap: 12px; font-size: 20px" class="d">%MARK%Jelly CMS</div>
    <div style="position: relative; display: flex; flex-direction: column; gap: 12px; max-width: 24em"><span class="stripe"></span><div class="d" style="font-size: 40px; letter-spacing: -.03em">比 WordPress 快，<br>主題像換衣服。</div><div style="color: #B9BCC6">Go 核心 · QuickJS 外掛 · 不用重啟</div></div>
  </aside>'''.replace("%MARK%", LOGIN_MARK)

pages = {}

# ======================= 登入 › 兩步驟驗證 =======================
pages["Login2FA"] = ("登入 · 兩步驟驗證", CSS, 1440, 900, f'''<div style="width: 100%; min-height: 100vh; box-sizing: border-box; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); background: #fff">
  {LEFT}
  <main style="display: grid; place-items: center; padding: 48px 24px">
    <div style="width: 100%; max-width: 360px; display: flex; flex-direction: column; gap: 22px">
      <div style="display: flex; flex-direction: column; gap: 12px"><span class="stripe"></span><h1 class="d" style="font-size: 44px; letter-spacing: -.035em">輸入驗證碼</h1><p style="margin: 0; color: #5F6470; line-height: 1.6">密碼正確。打開驗證器 App，輸入 6 位數驗證碼。5 分鐘內要完成，逾時請重新登入。</p></div>
      <div style="display: flex; align-items: center; gap: 10px; padding: 10px 12px; background: #F5F5F7; border-radius: 0 10px 0 10px; font-size: 13px">{I("user")}<b>hazel@example.com</b><span class="chip ok" style="margin-left: auto">密碼正確</span></div>
      <form style="display: flex; flex-direction: column; gap: 16px; margin: 0" onsubmit="return false">
        <div id="otp-err" role="alert" style="display: flex; align-items: center; gap: 10px; padding: 12px 16px; background: #FBE1DE; color: #9C2318; border-radius: 0 10px 0 10px; font-weight: 600; font-size: 14px">{I("alert")}驗證碼不對，再試一次</div>
        <div>
          <label id="otp-l">驗證碼</label>
          <div class="otp err" role="group" aria-labelledby="otp-l" aria-describedby="otp-err">
            <input type="text" inputmode="numeric" maxlength="1" autocomplete="one-time-code" autofocus aria-label="第 1 碼" aria-invalid="true">
            <input type="text" inputmode="numeric" maxlength="1" aria-label="第 2 碼" aria-invalid="true">
            <input type="text" inputmode="numeric" maxlength="1" aria-label="第 3 碼" aria-invalid="true">
            <input type="text" inputmode="numeric" maxlength="1" aria-label="第 4 碼" aria-invalid="true">
            <input type="text" inputmode="numeric" maxlength="1" aria-label="第 5 碼" aria-invalid="true">
            <input type="text" inputmode="numeric" maxlength="1" aria-label="第 6 碼" aria-invalid="true">
          </div>
        </div>
        <button class="btn p" type="button">{I("shield")}驗證並登入</button>
      </form>
      <div style="display: flex; align-items: center; justify-content: space-between; gap: 12px; font-size: 14px">
        <a href="Login2FA.dc.html" style="display: inline-flex; align-items: center; gap: 6px; font-weight: 600">{I("key")}使用備用碼</a>
        <a href="Login.dc.html" style="display: inline-flex; align-items: center; gap: 6px; color: #5F6470; font-weight: 600">{I("arrow-l")}回到登入</a>
      </div>
      <div style="position: relative; margin-top: 4px; padding: 18px 16px 14px; border-radius: 0 12px 0 12px; box-shadow: inset 0 0 0 1px #E2E3E8; display: flex; flex-direction: column; gap: 10px">
        <span class="chip nd tag">連續錯 5 次之後</span>
        <div role="alert" style="display: flex; align-items: center; gap: 10px; padding: 12px 16px; background: #FBE1DE; color: #9C2318; border-radius: 0 10px 0 10px; font-weight: 600; font-size: 14px">{I("clock")}嘗試太多次，15 分鐘後再試</div>
        <button class="btn" type="button" disabled>{I("shield")}驗證並登入</button>
      </div>
    </div>
  </main>
</div>'''
)

# ======================= 共用：單欄獨立框（接受邀請、重設密碼） =======================
def frame(body):
    return f'''<div style="width: 100%; min-height: 100vh; box-sizing: border-box; display: flex; flex-direction: column; background: #fff">
  <div class="topline"></div>
  <header style="display: flex; align-items: center; gap: 24px; padding: 18px 40px; max-width: 1360px; margin: 0 auto; width: 100%; box-sizing: border-box">
    <span style="display: inline-flex; align-items: center; gap: 12px; color: #15161A; font-size: 20px" class="d">{MARK.format(ink="#15161A")}榛果繽紛樂</span>
    <a href="Login.dc.html" style="margin-left: auto; display: inline-flex; align-items: center; gap: 6px; font-size: 14px; font-weight: 600">{I("arrow-l")}回到登入</a>
  </header>
  <main style="flex: 1; display: grid; place-items: center; padding: 32px 40px 64px; box-sizing: border-box">
    {body}
  </main>
  <footer style="padding: 0 40px 24px; text-align: center; font-size: 12px; color: #9A9FAB">Powered by Jelly CMS</footer>
</div>'''

def state_panel(icon, tone, title, body, action, tagtext):
    bg, fg = tone
    return f'''<section class="panel" style="gap: 16px">
        <span class="chip nd tag">{tagtext}</span>
        <span style="width: 52px; height: 52px; display: grid; place-items: center; background: {bg}; color: {fg}; border-radius: 0 16px 0 16px">{I(icon, 24)}</span>
        <h2 class="d" style="font-size: 24px; letter-spacing: -.03em; line-height: 1.2">{title}</h2>
        <p style="margin: 0; color: #5F6470; line-height: 1.7">{body}</p>
        {action}
      </section>'''

# ======================= 接受邀請 =======================
pages["InviteAccept"] = ("接受邀請", CSS, 1440, 900, frame(f'''<div style="width: 100%; max-width: 1040px; display: grid; grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr); gap: 32px; align-items: stretch">
      <section class="panel" style="gap: 20px">
        <div style="display: flex; flex-direction: column; gap: 12px"><span class="stripe"></span><h1 class="d" style="font-size: 36px; letter-spacing: -.03em; line-height: 1.2">你被邀請加入 榛果繽紛樂</h1>
          <p style="margin: 0; color: #5F6470; line-height: 1.7">Hazel 邀請 <b style="color: #15161A">jamie@example.com</b> 加入。設定好帳號和密碼就能登入。</p>
          <div style="display: flex; align-items: center; gap: 10px; flex-wrap: wrap"><span class="lbl">角色</span><span class="chip info">編輯</span><span style="font-size: 13px; color: #5F6470">管所有內容和留言，碰不到設定和商店。</span></div></div>
        <form style="display: flex; flex-direction: column; gap: 18px; margin: 0" onsubmit="return false">
          <div><label for="iu">使用者名稱</label><input id="iu" class="ul" type="text" value="jamie" autocomplete="username"><div style="font-size: 12px; color: #5F6470; margin-top: 4px">從 Email 帶入，可以改。登入時用這個或 Email 都行。</div></div>
          <div><label for="idn">顯示名稱</label><input id="idn" class="ul" type="text" placeholder="前台文章署名用這個" autocomplete="name"></div>
          <div><label for="ip">密碼</label><input id="ip" class="ul" type="password" value="jelly-2026" autocomplete="new-password" aria-describedby="ip-h">
            <div class="meter" style="margin-top: 10px" aria-hidden="true"><i class="on"></i><i class="on"></i><i class="on"></i><i></i></div>
            <div id="ip-h" style="font-size: 12px; color: #5F6470; margin-top: 6px">強度：中。至少 8 個字元，加上大小寫或符號會更好。</div></div>
          <button class="btn p" type="button">{I("user")}建立帳號</button>
        </form>
        <div style="font-size: 12px; color: #5F6470; line-height: 1.6">這個連結只能用一次，7 天內有效。建立後直接進後台。</div>
      </section>
      {state_panel("clock", ("#FCF0C8", "#7A5200"), "這個邀請已過期，請站長重寄", "邀請連結 7 天內有效，而且只能用一次。你可以直接回覆邀請你的人，請他在「使用者」頁面重寄一封新的。", f'<a class="btn t" href="Login.dc.html">{I("arrow-l")}回到登入</a>', "連結過期時")}
    </div>'''))

# ======================= 重設密碼 =======================
pages["PasswordReset"] = ("重設密碼", CSS, 1440, 900, frame(f'''<div style="width: 100%; max-width: 1360px; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 32px; align-items: stretch">
      <section class="panel" style="gap: 20px">
        <span class="chip nd tag">設定新密碼</span>
        <div style="display: flex; flex-direction: column; gap: 12px"><span class="stripe"></span><h1 class="d" style="font-size: 32px; letter-spacing: -.03em; line-height: 1.2">重設密碼</h1>
          <p style="margin: 0; color: #5F6470; line-height: 1.7">為 <b style="color: #15161A">hazel@example.com</b> 設定新密碼。連結 1 小時內有效，只能用一次。</p></div>
        <form style="display: flex; flex-direction: column; gap: 18px; margin: 0" onsubmit="return false">
          <div><label for="rp">新密碼</label><input id="rp" class="ul" type="password" value="jelly-2026-new" autocomplete="new-password" aria-describedby="rp-h">
            <div class="meter" style="margin-top: 10px" aria-hidden="true"><i class="good"></i><i class="good"></i><i class="good"></i><i></i></div>
            <div id="rp-h" style="font-size: 12px; color: #5F6470; margin-top: 6px">強度：強。至少 8 個字元。</div></div>
          <div><label for="rp2">再輸入一次</label><input id="rp2" class="ul" type="password" value="jelly-2026-new" autocomplete="new-password"></div>
          <button class="btn p" type="button">{I("key")}更新密碼</button>
        </form>
        <div style="font-size: 12px; color: #5F6470; line-height: 1.6">更新後，這個帳號在其他裝置上的登入會全部登出。</div>
      </section>
      {state_panel("alert", ("#FBE1DE", "#9C2318"), "連結已失效", "這個連結已經用過，或超過 1 小時了。請站長在「使用者」頁面重新寄一封，或回到登入頁。", f'<a class="btn t" href="Login.dc.html">{I("arrow-l")}回到登入</a>', "已用過或過期時")}
      {state_panel("check", ("#DCF3E9", "#0F6A45"), "密碼已更新，其他裝置都已登出", "請用新密碼重新登入。", f'<a class="btn p" href="Login.dc.html">{I("arrow-r")}前往登入</a>', "完成後")}
    </div>'''))

for name,(title,css,w,h,body) in pages.items():
    with open(os.path.join(OUT, f"{name}.dc.html"), "w", encoding="utf-8") as f:
        f.write(doc(title, css, body, w, h))
    print("wrote", name)
