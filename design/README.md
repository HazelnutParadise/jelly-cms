# 設計稿

Jelly CMS 全部畫面的設計稿原始檔。程式與這裡必須一致：改畫面就改對應的板，新畫面先補板再動程式。

- 線上畫布（Claude 設計 Artifact）：https://claude.ai/artifact/PbvpBN6hh2Eg3WJ8KfSz4a
- 每一張板是 `project/<名稱>.dc.html`，直接用瀏覽器打開就能看（`support.js` 是畫布執行環境提供的，本機沒有也能正常顯示）。
- `project/canvas.json` 是畫布索引：每張板的位置、尺寸、標題與分排標題。標題後面的「（尚未實作）」表示程式還沒有這個畫面。

## 兩種板

| 來源 | 板 | 怎麼改 |
|---|---|---|
| 手寫 | 前台原有頁面、登入安裝手機版、後台原有功能（Main、PostList、Post、Page、Search、NotFound、Login、Install、MobileHome、MobileAdmin、Dashboard、Posts、Editor、Media、Products、ProductEditor、Orders、OrderDetail、Themes、Plugins、Settings、Cart、Product、Shop） | 直接改 `.dc.html` |
| 產生 | 其餘由 `gen/*.py` 產出的板 | 改 `gen/` 裡的腳本，再執行腳本重新產生，不要直接改產出的 `.dc.html` |

重新產生：

```sh
python3 design/gen/gen.py          # 前台商店流程、後台缺的功能、營運與狀態畫面
python3 design/gen/gen_editor.py   # 編輯器滿版、專心模式、斜線選單與診斷、手機版
python3 design/gen/gen_update.py   # 版本更新（執行檔／容器）
python3 design/gen/gen_front2.py   # 作者、標籤、彙整索引、商品分類、會員中心、訂單查詢
python3 design/gen/gen_accounts.py # 登入驗證碼步驟、接受邀請、重設密碼
```

截圖檢查（需要 Playwright）：

```sh
node design/tools/shot.mjs Main Posts EditorFull
```

## 同步規則

1. 改程式的畫面時，同一次提交裡更新對應的板（手寫板直接改，產生板改腳本並重新產生）。
2. 新畫面先在這裡補板、加進 `canvas.json` 所屬的那一排，再實作。
3. 實作與畫面驗收以 Repo 裡的設計板為準。提交並推送後，在對應 PR 標記 `@claude`，附上 commit 與變更板清單，請他同步至上面的既有線上畫布並保留其他 PR 的板；收到完成回報後才標示已同步。
4. 視覺系統（顏色、形狀、字型、裝飾）定義在 `web/themes/default/layout.html` 與 `web/admin/layout.html` 的 CSS 變數，和板裡的 helmet 樣式對應；改其中一邊就要改另一邊。
