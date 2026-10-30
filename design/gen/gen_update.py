import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, ADMIN_CSS, OUT, admin, settings_nav

CSS = ADMIN_CSS + '''
.sub a{display:flex;align-items:center;gap:8px;min-height:34px;padding:0 10px;border-radius:0 9px 0 9px;color:#5F6470;font-weight:600;font-size:13px}
.sub a.on{background:#F3F3F5;color:#15161A}
.cmd{display:flex;align-items:center;gap:10px;padding:10px 12px;background:#15161A;color:#F2F2F2;border-radius:0 9px 0 9px;font:12.5px/1.6 ui-monospace,"SF Mono",Menlo,Consolas,monospace}
.cmd b{color:#9A9FAB;font-weight:500}
.cmd .btn{margin-left:auto}
.notes h4{margin:14px 0 6px;font-size:13.5px}.notes ul{margin:0 0 8px;padding-left:20px;font-size:13px;line-height:1.7;color:#3A3D45}
.stepl{display:grid;grid-template-columns:24px minmax(0,1fr) auto;gap:10px 12px;align-items:center;font-size:13.5px}
.stepl i{width:24px;height:24px;display:grid;place-items:center;border-radius:0 8px 0 8px;font-style:normal;font-size:12px;font-weight:800;background:#EDEEF1;color:#5F6470}
.stepl i.done{background:#13C2A3;color:#fff}.stepl i.run{background:#15161A;color:#fff}
.stepl .t{color:#5F6470;font-size:12px}
'''

def subnav():
    return settings_nav("更新")

RELEASE_NOTES = f'''<div class="notes" style="font-size:13px">
          <h4>0.9.4 · 2026-10-21</h4>
          <ul><li>新增：留言管理與垃圾留言過濾</li><li>新增：回收桶，刪除的內容保留 30 天</li><li>修正：Markdown 編輯器貼上圖片偶爾不會上傳</li><li>變更：主題模板新增 <span class="kbd">excerpt</span>、<span class="kbd">firstImage</span></li><li style="color:#9C2318">注意：會執行資料庫遷移 0012（新增 comments、trash 兩張表），更新前請先備份</li></ul>
          <h4 style="color:#5F6470">0.9.3 · 2026-10-10（目前）</h4>
          <ul style="color:#9A9FAB"><li>介面重設計</li><li>Markdown 編輯器</li></ul>
          <a href="Update.dc.html" style="font-size:12px;display:inline-flex;align-items:center;gap:4px">{I("external",12)}在 GitHub 看完整發行說明</a>
        </div>'''

def common_top(mode):
    runtime = {"bin": ("執行檔","直接執行 /opt/jelly/jelly-cms · systemd 服務 jelly.service","ok"),
               "docker": ("Docker 容器","映像 ghcr.io/hazelnutparadise/jelly-cms:0.9.3 · 由 /.dockerenv 判定","info")}[mode]
    return f'''<div style="display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px">
          <div class="card" style="display:flex;flex-direction:column;gap:6px"><span class="lbl">目前版本</span><b class="mono" style="font-size:26px">0.9.3</b><span style="font-size:12px;color:#5F6470">2026-10-10 · Go 1.25 · 遷移 0011</span></div>
          <div class="card" style="display:flex;flex-direction:column;gap:6px;box-shadow:inset 0 0 0 2px #F5B800"><span class="lbl" style="color:#7A5200">有新版本</span><b class="mono" style="font-size:26px">0.9.4</b><span style="font-size:12px;color:#5F6470">2 天前發布 · 來源 GitHub Releases · 上次檢查 09:00</span></div>
          <div class="card" style="display:flex;flex-direction:column;gap:6px"><span class="lbl">執行方式</span><b style="font-size:15px;display:flex;align-items:center;gap:8px">{I("box" if mode=="docker" else "terminal")}{runtime[0]}<span class="chip {runtime[2]}">自動偵測</span></b><span style="font-size:12px;color:#5F6470">{runtime[1]}</span></div>
        </div>'''

# ========== 執行檔：可以在後台直接更新 ==========
bin_main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr);gap:32px;align-items:start">
      {subnav()}
      <section style="display:flex;flex-direction:column;gap:20px;max-width:980px">
        <p style="font-size:13px;color:#5F6470">檢查、套用或退回已排入背景處理。畫面會顯示工作編號，請到<a href="Infra.dc.html#sys-jobs">工作列表</a>查看並刷新結果。</p>
        {common_top("bin")}
        <div style="display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:20px;align-items:start">
          <div class="card" style="display:flex;flex-direction:column;gap:12px">
            <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">更新到 0.9.4</b><span class="chip nd" style="margin-left:auto">linux/amd64 · 28 MB</span></div>
            <p style="margin:0;font-size:13px;color:#5F6470;line-height:1.6">按下去會照下面的順序做，中間網站會離線約 10 秒。任何一步失敗就自動換回 0.9.3，資料庫也會從剛備好的那份還原。</p>
            <div class="stepl">
              <i class="done">{I("check",12)}</i><span>下載 jelly-cms_0.9.4_linux_amd64.tar.gz</span><span class="t">4.2 秒</span>
              <i class="done">{I("check",12)}</i><span>驗證 SHA-256 與簽章（minisign 公鑰內建）</span><span class="t">通過</span>
              <i class="run">3</i><span><b>備份資料庫</b>到 /var/lib/jelly/backups/pre-0.9.4.sql.gz</span><span class="t">38 MB · 進行中</span>
              <i>4</i><span>把新執行檔換到原位置，舊的留成 jelly-cms.prev</span><span class="t"></span>
              <i>5</i><span>重新啟動服務（systemd 會拉起來）</span><span class="t">約 10 秒</span>
              <i>6</i><span>啟動時執行遷移 0012，健康檢查通過才算完成</span><span class="t"></span>
            </div>
            <div style="height:4px;background:#EDEEF1;border-radius:2px;overflow:hidden"><span style="display:block;width:42%;height:4px;background:linear-gradient(90deg,#E8285B 0 60%,#2F6BFF 60%)"></span></div>
            <div style="display:flex;gap:8px;align-items:center"><button class="btn p" type="button" disabled style="opacity:.6">{I("refresh")}更新中… 3 / 6</button><button class="btn t" type="button">{I("x")}取消（只能在第 4 步前）</button><a class="btn x" href="Logs.dc.html" style="margin-left:auto">{I("terminal")}看詳細日誌</a></div>
          </div>
          <div class="card">{RELEASE_NOTES}</div>
        </div>
        <div>
          <div class="lbl" style="margin-bottom:4px">更新設定</div>
          <div class="srow"><div><label>自動檢查新版本</label><div class="h">每天一次向 api.github.com 問最新 release，只送出版本號與作業系統。</div></div><div class="c"><span class="tg"></span></div></div>
          <div class="srow"><div><label>頻道</label><div class="h">預覽版會提前拿到功能，但可能有 bug。</div></div><div class="c"><div class="seg"><span class="on">穩定版</span><span>預覽版</span></div></div></div>
          <div class="srow"><div><label>有新版本時</label><div class="h">自動更新只在半夜執行，且一定先備份。</div></div><div class="c"><div class="seg"><span class="on">只提醒我</span><span>寄 Email</span><span>自動更新（03:00）</span></div></div></div>
          <div class="srow"><div><label>保留舊版執行檔</label><div class="h">出問題時可以在這裡一鍵退回。</div></div><div class="c"><span style="font-size:13px;color:#5F6470">0.9.3、0.9.2</span><button class="btn t s" type="button">{I("undo",14)}退回 0.9.3</button></div></div>
        </div>
      </section>
    </main>'''

# ========== 容器：只提示，不給按鈕 ==========
docker_main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr);gap:32px;align-items:start">
      {subnav()}
      <section style="display:flex;flex-direction:column;gap:20px;max-width:980px">
        {common_top("docker")}
        <div style="display:grid;grid-template-columns:minmax(0,1fr) 340px;gap:20px;align-items:start">
          <div class="card" style="display:flex;flex-direction:column;gap:12px">
            <div style="display:flex;align-items:center;gap:8px"><span class="stripe" style="width:40px"></span><b style="font-size:15px">這台是容器，更新要換映像</b></div>
            <p style="margin:0;font-size:13px;color:#5F6470;line-height:1.6">容器裡換掉執行檔，重建容器就不見了，所以這裡不提供「更新」按鈕。請用你部署的方式把映像換成 <b class="mono" style="color:#15161A">0.9.4</b>，啟動時會自動跑遷移。先備份資料庫。</p>
            <div class="seg" style="align-self:flex-start"><span class="on">Docker</span><span>Docker Compose</span><span>Kubernetes</span><span>Zeabur / Railway</span></div>
            <div class="cmd"><span><b>$</b> docker pull ghcr.io/hazelnutparadise/jelly-cms:0.9.4</span><button class="btn t s i" type="button" aria-label="複製">{I("copy",14)}</button></div>
            <div class="cmd"><span><b>$</b> docker stop jelly &amp;&amp; docker rm jelly<br><b>$</b> docker run -d --name jelly --env-file .env -v jelly-data:/data -p 8080:8080 ghcr.io/hazelnutparadise/jelly-cms:0.9.4</span><button class="btn t s i" type="button" aria-label="複製">{I("copy",14)}</button></div>
            <div style="display:flex;align-items:center;gap:10px;padding:10px 12px;background:#F5F5F7;border-radius:0 9px 0 9px;font-size:12.5px;color:#3A3D45">{I("alert",16)}<span>用 <span class="kbd">latest</span> 標籤的話，只要 pull 再重建就好，但建議鎖版本號，才知道自己跑的是什麼。</span></div>
            <div style="display:flex;gap:8px"><button class="btn t" type="button">{I("db")}先備份資料庫</button><button class="btn t" type="button">{I("mail")}把這些指令寄給自己</button><button class="btn x" type="button" style="margin-left:auto">{I("x")}這版先略過</button></div>
          </div>
          <div style="display:flex;flex-direction:column;gap:12px">
            <div class="card">{RELEASE_NOTES}</div>
            <div class="card" style="font-size:12.5px;line-height:1.6">
              <div class="lbl" style="margin-bottom:6px">怎麼判斷執行方式</div>
              <table style="font-size:12.5px"><tbody>
                <tr><td style="padding:6px 0">/.dockerenv 或 cgroup 有 docker、containerd</td><td style="padding:6px 0;text-align:right"><span class="chip info">容器</span></td></tr>
                <tr><td style="padding:6px 0">環境變數 KUBERNETES_SERVICE_HOST</td><td style="padding:6px 0;text-align:right"><span class="chip info">Kubernetes</span></td></tr>
                <tr><td style="padding:6px 0">執行檔在 Homebrew、apt、scoop 的目錄</td><td style="padding:6px 0;text-align:right"><span class="chip warn">套件管理</span></td></tr>
                <tr><td style="padding:6px 0">其他，且執行檔目錄可寫</td><td style="padding:6px 0;text-align:right"><span class="chip ok">可自行更新</span></td></tr>
              </tbody></table>
              <div style="color:#5F6470;margin-top:6px">想強制指定：環境變數 <span class="kbd">JELLY_RUNTIME=container|binary|package</span>。套件管理的情況會改顯示 <span class="kbd">brew upgrade jelly-cms</span> 之類的指令。</div>
            </div>
          </div>
        </div>
        <div>
          <div class="lbl" style="margin-bottom:4px">更新設定</div>
          <div class="srow"><div><label>自動檢查新版本</label><div class="h">每天一次向 api.github.com 問最新 release，只送出版本號與作業系統。</div></div><div class="c"><span class="tg"></span></div></div>
          <div class="srow"><div><label>頻道</label></div><div class="c"><div class="seg"><span class="on">穩定版</span><span>預覽版</span></div></div></div>
          <div class="srow"><div><label>有新版本時</label><div class="h">容器沒有自動更新選項。</div></div><div class="c"><div class="seg"><span class="on">只提醒我</span><span>寄 Email</span></div></div></div>
        </div>
      </section>
    </main>'''

def topbar_notice(): 
    return f'<span style="display:inline-flex;align-items:center;gap:6px;font-size:12px;color:#7A5200;background:#FCF0C8;padding:4px 10px;border-radius:0 7px 0 7px;font-weight:700">{I("download",13)}0.9.4 可用</span><button class="btn t" type="button">{I("refresh")}重新檢查</button><button class="btn t" type="button">取消更新</button>'

boards = [
 ("Update", "後台 · 更新（執行檔）", admin("settings", ["系統","設定","更新"], topbar_notice(), bin_main)),
 ("UpdateContainer", "後台 · 更新（容器）", admin("settings", ["系統","設定","更新"], topbar_notice(), docker_main)),
]
for name,title,html in boards:
    if os.environ.get("JELLY_DESIGN_ONLY") and name not in os.environ["JELLY_DESIGN_ONLY"].split(","):
        continue
    open(os.path.join(OUT, f"{name}.dc.html"),"w",encoding="utf-8").write(doc(title, CSS, html, 1440, {"UpdateContainer": 1130}.get(name, 1000))); print("wrote",name)
