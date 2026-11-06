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
for s in gen gen_editor gen_update gen_front2 gen_crawlers gen_halo gen_infra gen_layout gen_motion gen_accounts gen_integrations; do
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
| `gen_integrations.py` | API 串接清單、詳情與輪替對話框 |

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

## Jelly UI 元件對照（界線）

後台依「控制項的種類」劃線，同一類在整個後台只有一種長相與一種實作；界線的決定與理由在 `docs/tickets/26-jelly-ui-consistency.md`。前台、登入、安裝與帳號復原頁不用 Jelly UI，板上維持自家樣子。

| 類別 | 在板上的樣子 | 程式用什麼 |
|---|---|---|
| 選擇與切換 | 開關 `.tg.j`：膠囊軌道、白色圓形拇指與柔影，開是薄荷色、關是灰。分段選擇 `.seg.j`：膠囊軌道，選中的段是墨色膠囊與柔影，用在卡片或清單內選值、篩選清單、切換卡片內的來源（例：文章狀態、使用者角色、折扣碼類型、編輯器的檢視模式、設定頁的「環境」）。勾選框 `.cb.j`：32px 圓角方塊，沒勾是淺灰、勾了是薄荷色底加白色勾。單選 `.rd.j`：32px 圓形，沒選是淺灰，選了是薄荷色圓加白色圓點。尺寸是 `--cb`，與上游預設的 `--jelly-checkbox-size`、`--jelly-radio-size` 相同。表格的全選與逐列勾選、勾選卡片（邀請角色、匯出格式、快取驅動）裡的指示也用它們 | `<jelly-switch>`、`<jelly-segmented>` 加 `<jelly-segment>`、`<jelly-checkbox>`、`<jelly-radio-group>` 加 `<jelly-radio>` |
| 回饋與覆蓋層 | 通知 `.jt`：墨色底、圓形色點、右側圓形 ✕。對話框 `.jd`：對角單角圓角卡片、右上圓形 ✕、背景暗化加輕微模糊（`.jd-scrim`）；一個板上若同時並列多種對話框狀態，用 `.jd.st` 畫成不絕對定位的靜態範例（IntegrationDetail 的輪替對話框、Profile 的備用碼） | `window.Jelly.toast()`、`window.Jelly.confirm()`、`<jelly-dialog>` |
| 輸入與動作 | 底線欄位、對角單角按鈕、覆盆子紅主要動作、下拉選單、徽章、卡片、表格、側欄、⌘K 面板（Palette）維持自家，不畫成 Jelly | 自家 CSS，不採用 `jelly-button`、`jelly-input`、`jelly-select`、`jelly-badge` 等 |
| 導覽 | 會換網址或切換整頁區塊的分頁列維持自家分頁樣式：設定頁分頁列、個人資料分頁、附件策略頁分頁、轉址與 SEO 的三個分頁、應用市場的外掛／主題分頁 | 自家分頁 |

判斷分段選擇還是分頁：在卡片或清單內選一個值、篩選清單、切換卡片內的來源，畫 `.seg.j`；換網址或切換整頁的主要區塊，畫自家分頁。使用者內容不算後台控制項：編輯器預覽裡的待辦清單勾選框不換。

手機版的文章狀態選擇會縮減左右內距。標籤與篇數過長時，只在控制項所在的列內橫向捲動，不讓整頁溢出；開關與分段選擇的繪圖裁切保留 8px 焦點空間。

上游限制寫在這裡，免得有人誤會是板畫錯：開關與分段選擇是 canvas 繪製的膠囊形，勾選框與單選也是圓角方塊與圓形，都不能改成對角單角；只有對話框與通知有 `--jelly-<元件>-radius` 變數，所以只有這兩個用對角單角。勾選框與單選也是圓角，不是對角單角。
