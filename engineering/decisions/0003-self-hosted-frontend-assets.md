# 0003 — 前端資源全部自帶，不從外部網址載入

**Status:** accepted
**Date:** 2026-10-23

## Context

後台與預設主題需要第三方前端套件，例如 Markdown 轉換器、Web Components 元件庫、字型與圖示。這些檔案可以從 CDN 載入，也可以隨程式一起發佈。

## Decision

後台與預設主題不從 CDN 或任何外部網址載入腳本、樣式、字型或圖示。第三方套件以建置後的檔案自帶在 `web/` 下，每個套件附 `LICENSE` 與 `VERSION`，取得與重建的腳本放在 `scripts/vendor/`。

## Options rejected

原始紀錄只寫下理由「離線環境下每一頁都要完整可用」，沒有記下比較過的其他做法。

## Consequences

沒有對外網路時每一頁仍完整可用。自帶套件不會自己更新，所以每次發版前依 `/docs/release-checklist.md` 更新到最新版並重跑驗收。
