# 45 — 訂閱者可以用 Atom 訂閱網站

User Story：訪客除了 RSS，也能用 Atom 訂閱文章，全文或摘要依站長設定。
Blocked by：無。

## 背景

工單 38 核對時，`internal/server/feed_routes.go` 只提供 RSS，沒有 Atom。

## 驗收

- Atom 輸出通過格式驗證，全文與摘要設定和 RSS 一致。
