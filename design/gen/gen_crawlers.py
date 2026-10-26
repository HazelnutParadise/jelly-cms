import sys, os
sys.path.insert(0, os.path.dirname(__file__))
from gen import I, doc, OUT, admin
import gen_halo as H

CSS = H.CSS + '''
.bot{display:grid;grid-template-columns:minmax(0,1fr) 120px 120px 90px;gap:10px;align-items:center;padding:9px 0;border-top:1px solid #EEEFF2;font-size:13px}
.bot .ua{font:12px ui-monospace,"SF Mono",Menlo,Consolas,monospace;color:#5F6470}
.tri{display:inline-flex;gap:2px;padding:2px;background:#F3F3F5;border-radius:0 8px 0 8px}
.tri span{min-height:24px;padding:0 8px;display:inline-flex;align-items:center;font-size:11.5px;font-weight:700;color:#5F6470;border-radius:0 6px 0 6px}
.tri span.ok{background:#13C2A3;color:#fff}.tri span.no{background:#E8285B;color:#fff}.tri span.in{background:#15161A;color:#fff}
'''
nav=H.settings_nav("搜尋引擎與 AI 爬蟲")

def tri(v): # v in ok/no/in
    return f'<span class="tri"><span class="{"ok" if v=="ok" else ""}">允許</span><span class="{"no" if v=="no" else ""}">封鎖</span></span>'
def bot(name, ua, by, train, search, enforce):
    return f'<div class="bot"><div><b>{name}</b> <span style="font-size:12px;color:#5F6470">· {by}</span><div class="ua">{ua}</div></div>{tri(train)}{tri(search) if search else "<span style=\"font-size:12px;color:#B9BCC6\">—</span>"}<span style="font-size:12px;color:{"#9C2318" if enforce else "#5F6470"}">{"會無視 robots" if enforce else "遵守 robots"}</span></div>'

main = f'''<main style="padding:16px 24px 32px;display:grid;grid-template-columns:200px minmax(0,1fr) 380px;gap:28px;align-items:start">
    {nav}
    <section style="display:flex;flex-direction:column;gap:28px">
      <div style="display:flex;align-items:center;gap:14px;padding:12px 16px;background:#F5F5F7;border-radius:0 12px 0 12px;font-size:13px;line-height:1.5">{I("shield",18)}<span>這頁的設定會同時寫進 <b>robots.txt</b>、每頁的 <b>meta robots／X-Robots-Tag</b>、<b>TDM 保留標頭</b>、<b>sitemap</b> 和 <b>llms.txt</b>，你不用自己改檔案。老實說：robots 只是「請求」，守規矩的爬蟲才聽；不守規矩的用右邊「伺服器端直接擋」。</span></div>

      <div>
        <div class="lbl" style="margin-bottom:4px">搜尋引擎（Google、Bing、DuckDuckGo…）</div>
        <div class="srow"><div><label>讓搜尋引擎索引這個網站</label><div class="h">關掉等於整站 noindex，測試站或還沒上線用。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>哪些頁面可以被索引</label><div class="h">不索引的頁面不會進 sitemap，並加 noindex。單篇文章可以另外覆寫。</div></div><div class="c" style="flex-wrap:wrap;justify-content:flex-start;gap:6px"><span class="chip ok">首頁</span><span class="chip ok">文章</span><span class="chip ok">頁面</span><span class="chip ok">分類頁</span><span class="chip">標籤頁</span><span class="chip ok">作者頁</span><span class="chip">月份彙整</span><span class="chip ok">商品</span><span class="chip">搜尋結果</span><span class="chip">購物車與結帳</span><span class="chip">會員中心</span></div></div>
        <div class="srow"><div><label>圖片</label></div><div class="c"><div class="seg"><span class="on">可被圖片搜尋</span><span>不要（noimageindex）</span></div></div></div>
        <div class="srow"><div><label>摘要長度與快照</label><div class="h">對應 max-snippet、max-image-preview、noarchive。</div></div><div class="c"><div class="seg"><span class="on">不限制</span><span>只給短摘要</span><span>不給快取副本</span></div></div></div>
        <div class="srow"><div><label>變更時通知搜尋引擎</label><div class="h">IndexNow（Bing、Yandex、Naver…）。金鑰留空保留，可明確清除。</div></div><div class="c"><span class="tg"></span></div></div>
      </div>

      <div>
        <div style="display:flex;align-items:center;gap:10px;margin-bottom:6px"><span class="lbl">AI 爬蟲</span><span style="font-size:12px;color:#5F6470">分「拿去訓練模型」和「拿去回答問題／AI 搜尋」兩種用途，可以分開決定。</span></div>
        <div style="display:flex;gap:8px;margin-bottom:8px;align-items:center"><div class="seg"><span class="on">全部封鎖訓練、允許搜尋</span><span>全部封鎖</span><span>全部允許</span><span>自訂</span></div><span style="font-size:12px;color:#5F6470;margin-left:auto">名單每月隨更新自動補，自訂規則不會被動到</span></div>
        <div class="bot" style="border-top:0;font-size:11px;font-weight:700;color:#5F6470;letter-spacing:.06em;text-transform:uppercase;padding-top:0"><span>爬蟲</span><span>訓練用</span><span>搜尋／回答用</span><span>行為</span></div>
        {bot("OpenAI","GPTBot · ChatGPT-User · OAI-SearchBot","ChatGPT","no","ok",False)}
        {bot("Anthropic","ClaudeBot · Claude-User · Claude-SearchBot","Claude","no","ok",False)}
        {bot("Google AI","Google-Extended","Gemini 訓練（不影響 Google 搜尋）","no",None,False)}
        {bot("Apple","Applebot-Extended","Apple Intelligence 訓練","no",None,False)}
        {bot("Perplexity","PerplexityBot · Perplexity-User","AI 搜尋","no","ok",True)}
        {bot("Meta","meta-externalagent · FacebookBot","Llama 訓練","no",None,False)}
        {bot("Common Crawl","CCBot","公開資料集，很多模型從這裡拿","no",None,False)}
        {bot("ByteDance","Bytespider","豆包","no",None,True)}
        {bot("Amazon","Amazonbot","Alexa、AI 功能","no","ok",False)}
        {bot("其他","cohere-ai · Diffbot · omgili · YouBot · PetalBot · 共 14 個","小型或資料仲介","no",None,False)}
        <div class="srow"><div><label>允許瀏覽器 AI 使用網站工具（WebMCP）</label><div class="h">提供搜尋、列出文章、讀取公開內容與網站資訊的唯讀工具。關閉後，重新載入的前台頁面不再提供工具。</div></div><div class="c"><span class="tg"></span></div></div>
        <div style="display:flex;gap:8px;margin-top:10px;align-items:center"><input class="ul" type="text" placeholder="加一個 User-Agent，例如 MyCompanyBot" style="max-width:320px;font-family:ui-monospace,Menlo,monospace;font-size:12.5px"><button class="btn t s" type="button">{I("plus",12)}加入</button></div>
        <div class="srow" style="margin-top:8px"><div><label>伺服器端直接擋不守規矩的</label><div class="h">對標成「會無視 robots」的爬蟲，依 User-Agent 與已知 IP 段回 403，並寫進日誌。可能誤擋，所以預設只擋名單上那幾個。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>宣告資料探勘保留（TDM-Reservation）</label><div class="h">歐盟 DSM 指令承認的機器可讀聲明，加在 HTTP 標頭與 HTML。</div></div><div class="c"><span class="tg"></span></div></div>
        <div class="srow"><div><label>提供 llms.txt</label><div class="h">給允許的 AI 一份乾淨的站點摘要與重要頁面清單，少爬一點、答得準一點。</div></div><div class="c"><span class="tg"></span></div></div>
      </div>

      <div>
        <div class="lbl" style="margin-bottom:4px">單篇覆寫</div>
        <p style="margin:0 0 8px;font-size:12.5px;color:#5F6470;line-height:1.6">每篇文章、頁面、商品的屬性面板都有「搜尋引擎：繼承／索引／不索引」和「AI：繼承／允許／封鎖」。例如付費內容或還在審的新聞稿可以單獨關。目前有 <b style="color:#15161A">6 篇</b>覆寫了全站設定 · <a href="Posts.dc.html">列出來</a></p>
      </div>
    </section>

    <aside style="display:flex;flex-direction:column;gap:12px;position:sticky;top:16px">
      <div class="card" style="display:flex;flex-direction:column;gap:8px">
        <div style="display:flex;align-items:center;gap:8px"><span class="lbl">會產生的 robots.txt</span><span class="chip ok" style="margin-left:auto">即時預覽</span></div>
<div class="code" style="max-height:360px"><i># 由 Jelly 產生，改設定頁就好，不要手改</i>
User-agent: *
Allow: /
Disallow: /search
Disallow: /cart
Disallow: /checkout
Disallow: /account
Disallow: /t/
Disallow: /archive/

<i># AI 訓練：封鎖</i>
User-agent: GPTBot
User-agent: ClaudeBot
User-agent: Google-Extended
User-agent: Applebot-Extended
User-agent: CCBot
User-agent: meta-externalagent
User-agent: Bytespider
<i>…共 22 個</i>
Disallow: /

<i># AI 搜尋／回答：允許</i>
User-agent: OAI-SearchBot
User-agent: Claude-SearchBot
User-agent: PerplexityBot
Allow: /

Sitemap: https://example.com/sitemap.xml</div>
        <div style="display:flex;gap:6px"><a class="btn t s" href="#">{I("external",12)}/robots.txt</a><a class="btn t s" href="#">{I("external",12)}/llms.txt</a><button class="btn x s" type="button">{I("copy",12)}複製</button></div>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:6px;font-size:12.5px">
        <span class="lbl">每頁會帶的標頭</span>
        <div class="code" style="font-size:11.5px">&lt;meta name="robots" content="index,follow,max-image-preview:large"&gt;
X-Robots-Tag: noai, noimageai
TDM-Reservation: 1
TDM-Policy: https://example.com/tdm-policy.json</div>
        <span style="color:#5F6470;line-height:1.6">單篇設成「不索引」時 meta 變 noindex 並從 sitemap 移除；設成「封鎖 AI」時這頁另外加 Disallow。</span>
      </div>
      <div class="card" style="display:flex;flex-direction:column;gap:6px;font-size:12.5px">
        <span class="lbl">最近 7 天誰來爬過</span>
        <div style="display:flex;justify-content:space-between"><span>Googlebot</span><b class="mono">4,120</b></div>
        <div style="display:flex;justify-content:space-between"><span>GPTBot <span class="chip bad" style="min-height:16px;font-size:10px">已封鎖</span></span><b class="mono">812</b></div>
        <div style="display:flex;justify-content:space-between"><span>Bytespider <span class="chip bad" style="min-height:16px;font-size:10px">403 × 1,300</span></span><b class="mono">1,300</b></div>
        <div style="display:flex;justify-content:space-between"><span>ClaudeBot <span class="chip bad" style="min-height:16px;font-size:10px">已封鎖</span></span><b class="mono">96</b></div>
        <a href="Logs.dc.html" style="font-size:12px">在日誌裡看完整名單</a>
      </div>
    </aside>
  </main>'''
html = admin("settings", ["系統","設定","搜尋引擎與 AI 爬蟲"], f'<button class="btn t" type="button">{I("undo")}還原</button><button class="btn p" type="button">{I("save")}儲存並重新產生</button>', main)
open(os.path.join(OUT, "SettingsCrawlers.dc.html"),"w",encoding="utf-8").write(doc("後台 · 設定 · 搜尋引擎與 AI 爬蟲", CSS, html, 1440, 1900)); print("wrote SettingsCrawlers")
