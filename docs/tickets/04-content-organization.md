# 04 — 編輯者可以整理內容：分類標籤、選單、留言、回收桶、版本

Epic：編輯與發布
User Story：編輯者能把文章歸類、組出前台導覽、審核留言、找回誤刪的內容、回到任何一個舊版本。
Blocked by：03 的角色權限（admin / editor / author）。
Status：done（API 與後台頁面已實作並以真實伺服器驗收；契約見 `internal/server/content_routes.go` 與各 `handlers_*.go` 檔頭註解；設計稿 `design/project/Taxonomy、Menus、Comments、Trash、Revisions、Archive、Tag、Author、ArchiveIndex.dc.html`）

## 流程與驗收

- [x] 編輯者能建立有層級的分類與扁平標籤，自動產生不重複的網址別名，刪除分類時文章保留、子分類上移。
- [x] 作者只能讀分類、建標籤，把它們指派到自己的文章；文章 API 回傳分類與標籤。
- [x] 訪客能在 `/c/<slug>`、`/t/<slug>`、`/author/<username>`、`/archive`、`/archive/<yyyy>/<mm>` 看到對應的已發布公開文章，分頁正確，未知的 term 回 404。
- [x] 編輯者能建立選單、指派到頁首／頁尾／手機位置、拖拉成兩層；主題用 `{{range menu "header"}}` 取得已解析網址的樹，失效的目標會被略過。
- [x] 訪客能在允許留言的已發布文章留言；依設定進待審或直接顯示；蜜罐、長度、Email 格式、每分鐘次數有限制；Markdown 轉成無 script 的 HTML。
- [x] 編輯者能依狀態篩選、搜尋、核准、取消核准、標垃圾、丟回收桶、還原、批次處理、以站長身分回覆；永久刪除只能對回收桶裡的留言。
- [x] 刪除文章、頁面、商品、留言都進回收桶；可還原、永久刪除、清空；逾期（預設 30 天，`trash_retention_days` 可調）自動清除；作者只看得到自己的。
- [x] 每次有變更的儲存都留版本，自動儲存只留最近 20 個；能列出、看單一版本、逐行比較任兩版、還原到舊版（還原本身也是一個版本）。
- [x] 後台頁面照設計稿：分類與標籤、導覽選單、留言管理、回收桶、版本比較；側欄入口與 ⌘K 指令同步出現。

## 錯誤與邊界

未登入 401、角色不足 403、找不到 404、輸入錯誤 400、對非回收桶項目永久刪除 404、永久刪除仍被訂單引用的商品 409、留言過於頻繁 429。分類不能成為自己的祖先。選單樹最多兩層。被軟刪除的文章別名不能擋住新文章使用同一個別名。

## 分段實作與驗收

每段先由測試代理照檔頭契約建立失敗案例，主代理審查後交另一代理實作。段落通過不代表本工作單完成。

1. C1 分類標籤 API + 文章欄位（`handlers_taxonomy.go`、`handlers_posts.go` 的 applyPostTerms）。測試檔 `taxonomy_integration_test.go`。
2. C2 選單 API + 主題 `menu` 函式（`handlers_menus.go`）。測試檔 `menus_integration_test.go`。
3. C3 留言公開 API、審核 API、主題資料（`handlers_comments.go`）。測試檔 `comments_integration_test.go`。
4. C4 回收桶與版本（`handlers_trash.go`、`handlers_revisions.go`、`handlers_posts.go` 的 RecordRevision 呼叫、`cmd/server` 的定期清除）。測試檔 `trash_integration_test.go`、`revisions_test.go`、`revisions_integration_test.go`。
5. C5 前台彙整頁與主題模板（`handlers_archives.go`、`web/themes/default/{category,tag,author,archive,archive_month,comments}.html`、posts/post 模板的分類標籤 chip）。測試檔 `archives_integration_test.go`。
6. C6 後台頁面（`web/admin/{taxonomy,menus,comments,trash,revisions}.html`、post_editor 與 page_editor 的屬性面板）。驗收以 `cmd/jellypreview` 靜態渲染加 Playwright 截圖對照設計稿，並以真實伺服器操作一遍。

## 留待後續

- 分類合併與「從 WordPress 匯入」按鈕（匯入在 06）。
- 留言的 IP 顯示與「疑似垃圾」狀態、回收桶的附件類型（附件在 03 的媒體所有權）。
- 編輯器屬性面板的排程、作者、SEO 列（編輯器重做在 07）；版本比較目前只比標題與 HTML 內文。
- 選單 `login_only` 項目要等前台有登入狀態才會顯示。
