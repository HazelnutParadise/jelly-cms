# 43 — 上傳的圖片自動轉 WebP、產生縮圖並移除位置資訊

User Story：作者上傳圖片後，系統產生 WebP 與各尺寸縮圖、移除 EXIF 位置資訊，並可以把檔案複製成 Markdown 或 HTML 語法。
Blocked by：無。

## 背景

工單 38 核對時，上傳流程沒有轉檔，縮圖欄位沒有寫入，EXIF 只處理 JPEG，複製功能只複製網址。`internal/ext/ext.go` 已有媒體處理擴充點，開工前先確認有沒有既有掛接。

## 驗收

- JPEG、PNG、WebP 上傳後都有 WebP 版本與設定的縮圖尺寸，且不含 GPS 資訊。
- 附件庫能複製 Markdown 與 HTML 兩種語法。
