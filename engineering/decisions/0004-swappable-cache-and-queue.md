# 0004 — 快取與背景工作佇列做成可抽換介面，預設不依賴外部服務

**Status:** accepted
**Date:** 2026-10-23

## Context

頁面快取、工作階段、限流計數與背景工作都需要存放處。小型站台只有 Jelly CMS 加 PostgreSQL，較大的部署可能已有 Redis 或訊息佇列。

## Decision

快取與佇列是可抽換的介面。預設是記憶體快取與 PostgreSQL 佇列，不需要任何外部服務。可以切換成 Redis／Valkey（頁面與物件快取、工作階段、限流計數、Redis Streams 佇列），佇列也可選 NATS JetStream，其他中介軟體由外掛提供。

## Options rejected

原始紀錄沒有記下比較過的其他做法。

## Consequences

最小部署只要一個執行檔加 PostgreSQL。切換後端時要搬移等待中的工作，不能掉件。
