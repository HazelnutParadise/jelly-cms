# 0006 — 數學式在瀏覽器以自帶 KaTeX 渲染，伺服器只輸出跳脫後的原文

**Status:** accepted
**Date:** 2026-10-24

## Context

工單 09 要求伺服器是唯一的渲染器，數學式原本打算在伺服器用 QuickJS 執行 KaTeX。工單同時允許在伺服器無法安全執行時改用前台備援。

## Decision

`RenderMath` 回 `ErrNoEngine`，伺服器輸出跳脫後的 TeX 與 `jc-math[data-tex]`，瀏覽器用自帶的 KaTeX 渲染，並關閉 `trust`。

## Options rejected

- 在請求中以 qjs／Wazero 執行 KaTeX：實際探針在 CGO 關閉時，請求取消會讓 `QJS_Eval` 與 `JS_FreeValue` 因 context deadline 關閉模組而 panic，不能安全地放在請求裡。

## Consequences

伺服器輸出的 HTML 不含排版好的數學式，關閉 JavaScript 時只看得到 TeX 原文。HTML 匯出要自帶數學字型與 KaTeX。日後找到能安全取消的伺服器端引擎時，可以重新評估。
