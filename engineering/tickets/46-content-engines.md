# 46 — LaTeX 文件排版、PDF 匯出與投票、Lottie／Rive 元件

User Story：作者寫的 `latex` 區塊在前台顯示成排版好的 A4 頁面；單篇可以匯出 PDF；文章裡可以插入投票與 Lottie／Rive 動畫。
Blocked by：無。開工前要先做排版與 PDF 引擎的技術決策。

## 背景

工單 38 核對時，`latex` 區塊只輸出原始碼，PDF 匯出回 501 `pdf_engine_unavailable`，投票與 Lottie／Rive 在工單 09 列為延後。

## 驗收

- 選定的引擎寫成決策紀錄。
- 三項功能各有可在瀏覽器實際操作的驗收。
