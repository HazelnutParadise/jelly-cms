# 設計稿

Jelly CMS 全部畫面的設計稿原始檔。程式與這裡必須一致：改畫面就改對應的板，新畫面先補板再動程式。

- 線上畫布（Claude 設計 Artifact）：https://claude.ai/artifact/PbvpBN6hh2Eg3WJ8KfSz4a
- 每一張板是 `project/<名稱>.dc.html`，直接用瀏覽器打開就能看（`support.js` 是畫布執行環境提供的，本機沒有也能正常顯示）。
- `project/canvas.json` 是畫布索引：每張板的位置、尺寸、標題與分排標題。標題後面的「（尚未實作）」表示程式還沒有這個畫面。

## 兩種板

| 來源 | 板 | 怎麼改 |
|---|---|---|
| 手寫 | 前台原有頁面、登入安裝手機版、後台原有功能（Main、PostList、Post、Page、Search、NotFound、Login、Install、MobileHome、MobileAdmin、Dashboard、Posts、Editor、Media、Products、ProductEditor、Orders、OrderDetail、Themes、Plugins、Settings、Cart、Product、Shop） | 直接改 `.dc.html` |
| 腳本 | Mermaid（圖表是 Pele 的實際輸出） | 改 `tools/mermaid-board/` 的 `build.cjs`、`board.css` 或 `render.cjs`，在 repo 根目錄執行 `node design/tools/mermaid-board/render.cjs && node design/tools/mermaid-board/build.cjs`；升級 Pele 後也要重跑 |
| 產生 | 其餘由 `gen/*.py` 產出的板 | 改 `gen/` 裡的腳本，再執行腳本重新產生，不要直接改產出的 `.dc.html` |

重新產生：

```sh
cd design/gen
for s in gen gen_editor gen_update gen_front2 gen_crawlers gen_halo gen_infra gen_layout gen_motion gen_accounts gen_agents; do
  PYTHONDONTWRITEBYTECODE=1 python3 $s.py
done
```

| 腳本 | 產出 |
|---|---|
| `gen.py` | 結帳、付款完成、彙整、分類標籤、導覽選單、留言、使用者、個人資料、回收桶、版本比較、匯入、備份、轉址、日誌、指令面板、狀態畫面（共用 CSS 與字型 helmet 在這裡） |
| `gen_editor.py` | 編輯器滿版與專心模式 |
| `gen_update.py` | 版本更新（執行檔／容器） |
| `gen_front2.py` | 作者、標籤、彙整索引、商品分類、會員中心、訂單查詢 |
| `gen_crawlers.py` | 設定 · 搜尋引擎與 AI 爬蟲 |
| `gen_halo.py` | 通知、API 權杖、附件策略、應用市場、外掛詳情、設定 · 閱讀與網址、維護模式、折扣碼 |
| `gen_infra.py` | 快取與佇列 |
| `gen_layout.py` | 編輯器版面模式、匯出文章（EditorLayout、EditorExport） |
| `gen_motion.py` | 編輯器動效與互動元件（EditorMotion、Widgets） |
| `gen_accounts.py` | 登入驗證碼步驟、接受邀請、重設密碼 |
| `gen_agents.py` | AI agent 清單、詳情與輪替對話框 |

十一支腳本重新產生後 `git diff` 必須是空的（除非你正好改了它們）；改共用 CSS 或 helmet 後全部重跑。

截圖檢查（需要 Playwright）：

```sh
node design/tools/shot.mjs Main Posts EditorFull
```

## 同步規則

1. 改程式的畫面時，同一次提交裡更新對應的板（手寫板直接改，產生板改腳本並重新產生）。
2. 新畫面先在這裡補板、加進 `canvas.json` 所屬的那一排，再實作。
3. 實作與畫面驗收以 Repo 裡的設計板為準。提交並推送後，在對應 PR 標記 `@claude`，附上 commit 與變更板清單，請他同步至上面的既有線上畫布並保留其他 PR 的板；收到完成回報後才標示已同步。同步時包含 `project/fonts.css` 與 `project/fonts/*.woff2`。
4. 視覺系統（顏色、形狀、字型、裝飾）定義在 `web/themes/default/layout.html` 與 `web/admin/layout.html` 的 CSS 變數，和板裡的 helmet 樣式對應；改其中一邊就要改另一邊。


## Logo 素材

正式來源為 `../web/admin/assets/jelly-mark.svg`，設計板一律引用本目錄的 `project/jelly-mark.svg` 可攜副本，不各自重畫或改色。共用 Python 產生器會從正式來源同步副本；兩份檔案位元組必須相同。手寫板與 Mermaid 板也引用此副本，保留原始比例。

同步線上畫布時，除了板與字型，`files` 也必須包含 `project/jelly-mark.svg`。原本的背景果凍丁與空狀態插圖是裝飾，不取代品牌位置的完整 logo。

## 字型

板不從網路載入字型。`project/fonts.css` 以 `@font-face` 指向 `project/fonts/*.woff2`（Bricolage Grotesque 與 Noto Sans TC 的子集，和 `web/admin/assets/fonts/` 是同一份檔案）；每張板的 helmet 只有一行 `<link rel="stylesheet" href="./fonts.css">`。

- `project/fonts/` 不進版控（`.gitignore`）。第一次取得專案、或字型更新後執行 `scripts/vendor/fonts.sh`，它在產生網站字型時一併複製到這裡。
- 發布線上畫布時，`files` 要把 `project/fonts.css` 和 `project/fonts/*.woff2` 一起列進去，否則線上畫布會退回系統字型。
- 新板用 `gen.py` 的 `doc()` 就自動帶這行；手寫新板自己加這一行，不要再貼 Google Fonts。

## Jelly UI 元件對照

後台採用 [Jelly UI](https://jelly-ui.com/)（Web Components）的四類控制項；版面、側欄、卡片、表格、按鈕、自家圖示不換。板上是它們在本專案語言裡的樣子，程式用的標籤、屬性與對接規則在 `docs/tickets/12-zero-cdn-jelly-ui.md`。

| 控制項 | 板 | 板上的樣子 | 程式 |
|---|---|---|---|
| 開關 | Settings、SettingsRoutes、SettingsCrawlers（`.tg.j`） | 膠囊軌道、白色圓形拇指與柔影；開是薄荷色、關是灰；上游拇指移動時會拉長，靜態板不畫 | `<jelly-switch>`，設定頁的布林欄位，包含 OAuth、金流、閱讀、搜尋引擎與維護模式 |
| 分段選擇 | Posts、Settings（`.seg.j`） | 膠囊軌道，選中的段是墨色膠囊與柔影；Posts 每段標籤後面接篇數，同字級同色（上游的段標籤只能是純文字）；Settings 的「環境」兩段是「測試」「正式」 | `<jelly-segmented>` 加 `<jelly-segment>`：文章列表的狀態篩選；設定頁綠界與藍新的「環境」（測試＝`test_mode: true`） |
| 對話框 | Trash（`.jd`） | 對角單角圓角卡片、右上角圓形 ✕、背景暗化加輕微模糊；回收桶的「輸入刪除確認」是 `Jelly.confirm` 的 `requireText` 版 | `<jelly-dialog>`，由 `window.Jelly.confirm()` 產生 |
| 通知 | States（`.jt`） | 墨色底、圓形色點、右側圓形 ✕；動作按鈕與細節文字沿用 | `<jelly-toaster>`，由 `window.Jelly.toast()` 轉呼叫 `jellyToast()` |

手機版的文章狀態選擇會縮減左右內距。標籤與篇數過長時，只在控制項所在的列內橫向捲動，不讓整頁溢出；開關與分段選擇的繪圖裁切保留 8px 焦點空間。

兩個上游限制寫在這裡，免得有人誤會是板畫錯：開關與分段選擇是 canvas 繪製的膠囊形，圓角不能改成對角單角；只有對話框與通知有 `--jelly-<元件>-radius` 變數，所以這兩個維持對角圓角。
